package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type jwtTestKeys struct {
	private string
	public  string
}

// makeJWTTestKeys produces the PEM forms users receive from OpenSSL and cloud
// service-account tooling.
func makeJWTTestKeys(t *testing.T, algorithm string) jwtTestKeys {
	t.Helper()
	var private any
	var public any
	switch algorithm {
	case jwtRS256:
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err, "the RSA fixture key must be generated")
		private, public = key, &key.PublicKey
	case jwtES256:
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err, "the P-256 fixture key must be generated")
		private, public = key, &key.PublicKey
	default:
		require.FailNowf(t, "unknown fixture algorithm", "no key generator for %q", algorithm)
	}

	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	require.NoError(t, err, "the private fixture key must marshal as PKCS#8")
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	require.NoError(t, err, "the public fixture key must marshal as PKIX")
	return jwtTestKeys{
		private: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})),
		public:  string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})),
	}
}

// runJWT executes one JWT node and returns its sole output message.
func runJWT(t *testing.T, cfg map[string]any, clock engine.Clock, msg *engine.Msg) (*engine.Msg, error) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err, "the JWT node config fixture must encode")
	out, err := JWTNode{}.Execute(context.Background(), newTestCtx(string(raw), clock), map[string]*engine.Msg{"in": msg})
	if err != nil {
		return nil, err
	}
	require.Len(t, out["out"], 1, "one token operation must emit one message")
	return out["out"][0], nil
}

// TestJWTRoundTripsAsymmetricPEMKeys pins both supported JOSE wire methods to
// PKCS#8 private and PKIX public PEM keys. It also locks the converter contract:
// a nested target changes a copy while topic, metadata, and the source claims
// continue through the node.
func TestJWTRoundTripsAsymmetricPEMKeys(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, algorithm := range []string{jwtRS256, jwtES256} {
		t.Run(algorithm, func(t *testing.T) {
			keys := makeJWTTestKeys(t, algorithm)
			in := &engine.Msg{
				Payload: map[string]any{"claims": map[string]any{
					"sub": "sensor-7",
					"exp": now.Add(time.Minute).Unix(),
				}},
				Topic: "devices/7",
				Meta:  map[string]any{"request_id": "req-1"},
			}
			signed, err := runJWT(t, map[string]any{
				"operation": jwtSign, "algorithm": algorithm, "private_key": keys.private,
				"property": "payload.claims", "target": "payload.token",
			}, engine.NewFakeClock(now), in)
			require.NoError(t, err, "a matching private PEM key must sign %s claims", algorithm)
			token, ok := signed.Payload.(map[string]any)["token"].(string)
			require.True(t, ok, "sign must write a compact JWT string for %s", algorithm)
			assert.Equal(t, 2, strings.Count(token, "."), "a compact JWT has two separators for %s", algorithm)

			verified, err := runJWT(t, map[string]any{
				"operation": jwtVerify, "algorithm": algorithm, "public_key": keys.public,
				"property": "payload.token", "target": "payload.verified",
			}, engine.NewFakeClock(now), signed)
			require.NoError(t, err, "the matching public PEM key must verify %s signatures", algorithm)
			payload, ok := verified.Payload.(map[string]any)
			require.True(t, ok, "a nested target requires an object payload for %s", algorithm)
			claims, ok := payload["verified"].(map[string]any)
			require.True(t, ok, "verify must write a claims object for %s", algorithm)
			assert.Equal(t, "sensor-7", claims["sub"], "verified claims must survive the JWT wire round trip for %s", algorithm)
			assert.Equal(t, float64(now.Add(time.Minute).Unix()), claims["exp"], "numeric dates decode from JSON for %s", algorithm)
			assert.Equal(t, "devices/7", verified.Topic, "JWT property writes must preserve the topic for %s", algorithm)
			assert.Equal(t, "req-1", verified.Meta["request_id"], "JWT property writes must preserve metadata for %s", algorithm)
			assert.NotContains(t, in.Payload.(map[string]any), "token", "sign must not mutate its recorded input for %s", algorithm)
		})
	}
}

// TestJWTVerificationUsesParserPolicy pins jwt/v5's standard NumericDate
// behavior to the execution clock. Decode still returns expired claims because
// it is explicitly untrusted, while verify rejects expired, not-yet-valid, and
// differently signed tokens before accepting a key for the wrong alg header.
func TestJWTVerificationUsesParserPolicy(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	rsaKeys := makeJWTTestKeys(t, jwtRS256)

	tests := []struct {
		name   string
		claims map[string]any
		want   string
	}{
		{"expired", map[string]any{"sub": "old", "exp": now.Add(-time.Second).Unix()}, "token is expired"},
		{"not before", map[string]any{"sub": "early", "nbf": now.Add(time.Second).Unix()}, "token is not valid yet"},
	}
	var expiredToken string
	for _, tc := range tests {
		signed, err := runJWT(t, map[string]any{
			"operation": jwtSign, "algorithm": jwtRS256, "private_key": rsaKeys.private,
		}, engine.NewFakeClock(now), &engine.Msg{Payload: tc.claims})
		require.NoError(t, err, "the %s fixture must be a valid signed JWT", tc.name)
		token := signed.Payload.(string)
		if tc.name == "expired" {
			expiredToken = token
		}
		_, err = runJWT(t, map[string]any{
			"operation": jwtVerify, "algorithm": jwtRS256, "public_key": rsaKeys.public,
		}, engine.NewFakeClock(now), &engine.Msg{Payload: token})
		assert.ErrorContains(t, err, tc.want, "jwt/v5 must reject the %s claim against the execution clock", tc.name)
	}

	// Corrupt the signature while keeping its base64url shape. ParseUnverified
	// must still return the claims because callers use decode for inspection,
	// never authentication.
	tampered := expiredToken[:len(expiredToken)-1] + "A"
	if tampered == expiredToken {
		tampered = expiredToken[:len(expiredToken)-1] + "B"
	}
	decoded, err := runJWT(t, map[string]any{"operation": jwtDecode}, engine.NewFakeClock(now), &engine.Msg{Payload: tampered})
	require.NoError(t, err, "decode must not apply expiration or signature validation")
	assert.Equal(t, "old", decoded.Payload.(map[string]any)["sub"], "decode returns the untrusted payload even after exp")

	esKeys := makeJWTTestKeys(t, jwtES256)
	esToken, err := runJWT(t, map[string]any{
		"operation": jwtSign, "algorithm": jwtES256, "private_key": esKeys.private,
	}, engine.NewFakeClock(now), &engine.Msg{Payload: map[string]any{"sub": "method-check"}})
	require.NoError(t, err, "the ES256 mismatch fixture must be signed")
	_, err = runJWT(t, map[string]any{
		"operation": jwtVerify, "algorithm": jwtRS256, "public_key": rsaKeys.public,
	}, engine.NewFakeClock(now), esToken)
	assert.ErrorContains(t, err, "signing method ES256 is invalid", "WithValidMethods must reject an alg header outside the configured method")
}
