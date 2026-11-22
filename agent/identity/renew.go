package identity

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/internal/version"
	"github.com/grmrgecko/karakuri/pkg/agentwire"
	"github.com/grmrgecko/karakuri/pkg/ca"
)

// renewInterval is how often the agent asks the host whether its certificate or
// its trust bundle needs replacing. The agent never decides on its own: the
// round trip is one handshake and a few hundred bytes, and only the host knows
// whether a CA rollover is under way.
const renewInterval = 6 * time.Hour

// RenewLoop keeps the stored identity current for as long as the agent runs.
// A failed check is logged and retried on the next tick — the certificate has
// a third of its life left when renewal starts, so there is a long runway
// before anything actually expires.
func RenewLoop(ctx context.Context, dir, server string, log *zap.Logger) {
	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()
	for {
		stored, err := renewOnce(dir, server)
		if err != nil {
			log.Warn("certificate check failed", zap.Error(err))
		}
		// A CA rollover counts the certificate an agent presents, not the one
		// it was sent, and a long-lived session would not present the new one
		// until it reconnects. Checking again at once presents it.
		if stored {
			if _, err := renewOnce(dir, server); err != nil {
				log.Warn("presenting the renewed certificate failed", zap.Error(err))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// renewOnce runs one renewal round trip and stores whatever came back,
// reporting whether a new certificate was stored. The new certificate takes
// effect on the next connection; the host ends a live session when the
// certificate it handshook with expires.
func renewOnce(dir, server string) (bool, error) {
	_, cert, caPool, err := Load(dir)
	if err != nil {
		return false, err
	}
	keyPEM, err := readKey(dir)
	if err != nil {
		return false, fmt.Errorf("reading key: %w", err)
	}
	currentCA, err := readCA(dir)
	if err != nil {
		return false, fmt.Errorf("reading CA: %w", err)
	}
	trust, err := ca.ParseBundle(currentCA)
	if err != nil {
		return false, err
	}
	csrPEM, err := ca.CreateCSR(keyPEM, "renewal")
	if err != nil {
		return false, err
	}

	resp, err := renewExchange(server, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		ServerName:   ca.ServerName,
	}, agentwire.RenewRequest{
		CSR:         string(csrPEM),
		Fingerprint: trust.Fingerprint(),
		Version:     version.Version,
	})
	if err != nil {
		return false, err
	}
	if resp.Error != "" {
		return false, fmt.Errorf("renewal refused: %s", resp.Error)
	}
	if resp.Cert == "" && resp.CA == "" {
		return false, nil
	}

	// A new bundle only ever extends what is already trusted, and a new
	// certificate has to chain to the result. Store neither if either fails,
	// so a bad reply cannot leave half an identity behind.
	trustPEM := currentCA
	if resp.CA != "" {
		bundle, err := ca.AcceptBundle(currentCA, []byte(resp.CA))
		if err != nil {
			return false, err
		}
		trustPEM = bundle.PEM()
		trust = bundle
	}
	var newCert []byte
	if resp.Cert != "" {
		if _, err := ca.VerifyIssuedCert([]byte(resp.Cert), keyPEM, trust.Pool()); err != nil {
			return false, err
		}
		newCert = []byte(resp.Cert)
	}
	var newCA []byte
	if resp.CA != "" {
		newCA = trustPEM
	}
	if err := StoreRenewal(dir, newCert, newCA); err != nil {
		return false, err
	}
	return len(newCert) > 0, nil
}

// renewExchange dials the host on the renewal mode and completes one round
// trip. The connection is mutually authenticated, so the host reads the agent's
// identity from the certificate rather than being told it.
func renewExchange(server string, tlsCfg *tls.Config, req agentwire.RenewRequest) (agentwire.RenewResponse, error) {
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", server, tlsCfg)
	if err != nil {
		return agentwire.RenewResponse{}, fmt.Errorf("dialing %s: %w", server, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	if _, err := conn.Write([]byte{agentwire.ModeRenew}); err != nil {
		return agentwire.RenewResponse{}, err
	}
	if err := agentwire.WriteJSON(conn, req); err != nil {
		return agentwire.RenewResponse{}, err
	}
	var resp agentwire.RenewResponse
	if err := agentwire.ReadJSON(conn, &resp); err != nil {
		return agentwire.RenewResponse{}, fmt.Errorf("reading renew response: %w", err)
	}
	return resp, nil
}
