// Package agentwire is the small shared vocabulary the host and the agent both
// speak on the agent listener before the plugin protocol takes over. It carries
// no database or engine dependencies so the agent binary links it cheaply.
//
// One TLS port serves two jobs, told apart by a single mode byte the agent
// writes first: enrollment (the agent has no certificate yet and verifies the
// host by the CA pin in its token) and connect (mutual TLS, then yamux and the
// plugin protocol). Enrollment frames are length-prefixed JSON.
package agentwire

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// Mode bytes select what an accepted connection is for.
const (
	ModeEnroll        byte = 'E'
	ModeConnect       byte = 'C'
	ModeRenew         byte = 'R'
	ModeCredentialKey byte = 'K'
)

// maxFrame caps a single enrollment frame. A CSR and cert are small; this only
// guards against a peer claiming an absurd length.
const maxFrame = 1 << 20

// EnrollRequest is what an unenrolled agent sends: the secret half of its join
// token, a CSR built from a key it generated locally, and what it is. Name is
// the agent's preference, used only when the token did not name it. The private
// key never appears here.
type EnrollRequest struct {
	Token    string `json:"token"`
	CSR      string `json:"csr"`
	Hostname string `json:"hostname"`
	Name     string `json:"name,omitempty"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Version  string `json:"version"`
}

// EnrollResponse is the host's reply: the signed client certificate, the trust
// bundle to store, and the identity the host assigned. Error is set instead
// when enrollment was refused. The bundle does not replace the token's CA — the
// agent accepts it only if it still contains a root the token carried, so a
// rollover in progress reaches a new agent without the reply being able to name
// a CA the agent has no reason to believe.
type EnrollResponse struct {
	Cert      string `json:"cert,omitempty"`
	CA        string `json:"ca,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	AgentName string `json:"agent_name,omitempty"`
	Error     string `json:"error,omitempty"`
}

// RenewRequest is what an enrolled agent sends on a mutually authenticated
// renewal connection: a CSR built from the key it already holds, and the
// fingerprint of the trust bundle it currently stores so the host can skip
// resending an unchanged one. The agent asks on a timer whether or not anything
// is due; the host decides.
type RenewRequest struct {
	CSR         string `json:"csr"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Version     string `json:"version,omitempty"`
}

// RenewResponse carries whatever changed. Cert is set when the leaf was close
// enough to expiry to reissue, or when it was signed by a root the host is
// rolling away from; CA is set when the agent's bundle is out of date. Both
// empty means there was nothing to do, which is the usual answer.
type RenewResponse struct {
	Cert  string `json:"cert,omitempty"`
	CA    string `json:"ca,omitempty"`
	Error string `json:"error,omitempty"`
}

// CredentialKeyResponse carries the agent's credential-store master key, the
// envelope key that keeps the agent's on-disk credential store inert without the
// server. The agent asks for it on a mutually authenticated connection — the
// host reads which agent from the certificate — and holds it only in memory,
// zeroed after use. Key is base64; Error is set instead when the host refused.
type CredentialKeyResponse struct {
	Key   string `json:"key,omitempty"`
	Error string `json:"error,omitempty"`
}

// WriteFrame writes a length-prefixed byte slice.
func WriteFrame(w io.Writer, data []byte) error {
	if len(data) > maxFrame {
		return fmt.Errorf("frame too large: %d bytes", len(data))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

// ReadFrame reads a length-prefixed byte slice.
func ReadFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxFrame {
		return nil, fmt.Errorf("frame too large: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// WriteJSON frames a JSON-encoded value.
func WriteJSON(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return WriteFrame(w, data)
}

// ReadJSON reads a framed JSON value into v.
func ReadJSON(r io.Reader, v any) error {
	data, err := ReadFrame(r)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
