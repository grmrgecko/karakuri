package core

import (
	"context"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The signature GitHub would send for this body and secret, computed with
// openssl:
//
//	printf '%s' '{"action":"opened"}' | openssl dgst -sha256 -hmac 's3cr3t'
//
// Both encodings of the same MAC are here because senders differ on which one
// they use and the node accepts either.
const (
	webhookBody   = `{"action":"opened"}`
	webhookSecret = "s3cr3t"
	webhookHex    = "893b8b176c39b217756497563a1b9ba1507d371642b1edf4ee9f876b8fd74ecc"
	webhookB64    = "iTuLF2w5shd1ZJdWOhuboVB9NxZCse307p+Ha4/XTsw="
)

// runCrypto executes the crypto node and returns the value it wrote.
func runCrypto(t *testing.T, cfg string, msg *engine.Msg) (any, error) {
	t.Helper()
	out, err := CryptoNode{}.Execute(context.Background(), newTestCtx(cfg, nil), map[string]*engine.Msg{"in": msg})
	if err != nil {
		return nil, err
	}
	require.Len(t, out["out"], 1, "a crypto operation that did not fail emits exactly one message")
	return out["out"][0].Payload, nil
}

// TestCryptoSignsAndVerifiesAWebhookSignature pins the node against the
// signature scheme GitHub, Stripe, and most other senders use: HMAC-SHA-256
// over the raw body, sent in a header, often with an algorithm prefix. The
// expected MAC comes from openssl rather than from this code, so a change to
// how the source bytes are read fails here instead of on a live webhook.
//
// The missing-signature row is the reason verify answers false rather than
// erroring: an unsigned request is precisely what the check exists to catch,
// and routing it to the error branch would make "unsigned" and "the node is
// misconfigured" the same outcome.
func TestCryptoSignsAndVerifiesAWebhookSignature(t *testing.T) {
	signed := `{"operation":"hmac","secret":"` + webhookSecret + `","source":"payload","target":"payload"}`
	got, err := runCrypto(t, signed, &engine.Msg{Payload: webhookBody})
	require.NoError(t, err, "signing a string body needs nothing but the secret")
	assert.Equal(t, webhookHex, got, "the HMAC must match what openssl computes for the same body and secret")

	// Binary is signed as it stands, which is what a webhook read as binary
	// hands over. Re-encoding it as text first would change the bytes and the
	// signature with them.
	got, err = runCrypto(t, signed, &engine.Msg{Payload: []byte(webhookBody)})
	require.NoError(t, err, "a binary body is valid input to a signature")
	assert.Equal(t, webhookHex, got, "signing raw bytes must give the same MAC as signing the same text")

	tests := []struct {
		name      string
		signature any
		prefix    string
		want      bool
		why       string
	}{
		{"hex as sent", webhookHex, "", true, "the plain hex encoding senders use by default"},
		{"hex uppercased", "893B8B176C39B217756497563A1B9BA1507D371642B1EDF4EE9F876B8FD74ECC", "", true, "hex is case-insensitive and some senders uppercase it"},
		{"base64", webhookB64, "", true, "the other encoding of the same MAC"},
		{"github prefix stripped", "sha256=" + webhookHex, "sha256=", true, "GitHub sends X-Hub-Signature-256 as sha256=<hex>"},
		{"prefix left on", "sha256=" + webhookHex, "", false, "without the prefix configured the value does not match, and guessing at it is how a check stops checking"},
		{"wrong signature", webhookHex[:63] + "0", "", false, "one flipped character must fail"},
		{"no signature at all", nil, "", false, "an unsigned request fails the check rather than the node"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := &engine.Msg{Payload: webhookBody, Meta: map[string]any{}}
			if tc.signature != nil {
				msg.Meta["signature"] = tc.signature
			}
			cfg := `{"operation":"verify","secret":"` + webhookSecret + `","source":"payload",` +
				`"expected":"meta.signature","prefix":"` + tc.prefix + `","target":"payload"}`
			got, err := runCrypto(t, cfg, msg)
			require.NoError(t, err, "verify reports its answer, it does not fail on a bad signature")
			assert.Equal(t, tc.want, got, "%s: %s", tc.name, tc.why)
		})
	}
}

// TestCryptoEncryptRoundTrip locks the sealing to the same AES-256-GCM format
// and 64-hex-character key that pkg/crypto uses for secrets at rest, so an
// operator has one key format to understand rather than two. The wrong-key row
// pins that opening fails loudly: GCM authenticates, so a value sealed under
// another key is refused rather than coming back as noise.
func TestCryptoEncryptRoundTrip(t *testing.T) {
	const (
		key   = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
		other = "1f1e1d1c1b1a191817161514131211100f0e0d0c0b0a09080706050403020100"
	)

	sealed, err := runCrypto(t, `{"operation":"encrypt","key":"`+key+`","source":"payload","target":"payload"}`,
		&engine.Msg{Payload: "the back door is unlocked"})
	require.NoError(t, err, "a 32-byte hex key is the key format this node takes")
	require.NotEqual(t, "the back door is unlocked", sealed, "the payload must not survive encryption as itself")

	opened, err := runCrypto(t, `{"operation":"decrypt","key":"`+key+`","source":"payload","target":"payload"}`,
		&engine.Msg{Payload: sealed})
	require.NoError(t, err, "the same key opens what it sealed")
	assert.Equal(t, "the back door is unlocked", opened, "a round trip through the node returns the plaintext unchanged")

	_, err = runCrypto(t, `{"operation":"decrypt","key":"`+other+`","source":"payload","target":"payload"}`,
		&engine.Msg{Payload: sealed})
	assert.Error(t, err, "GCM authenticates, so another key must fail rather than return garbage")
}

// TestCryptoWritesOffThePayload pins that a target outside the payload leaves
// the payload alone. It is the behavior the port type rule promises through
// PayloadPath: a signature written to meta.signature means the message still
// carries whatever it arrived with, and the editor colors the port from that
// promise.
func TestCryptoWritesOffThePayload(t *testing.T) {
	in := &engine.Msg{Payload: map[string]any{"action": "opened"}, Meta: map[string]any{}}
	out, err := CryptoNode{}.Execute(context.Background(),
		newTestCtx(`{"operation":"hash","algorithm":"sha256","source":"payload.action","target":"meta.digest"}`, nil),
		map[string]*engine.Msg{"in": in})
	require.NoError(t, err, "hashing a field of the payload is an ordinary operation")
	require.Len(t, out["out"], 1, "one message in, one message out")

	msg := out["out"][0]
	assert.Equal(t, map[string]any{"action": "opened"}, msg.Payload,
		"writing to meta must leave the payload exactly as it arrived")
	// sha256("opened"), from `printf opened | sha256sum`.
	assert.Equal(t, "5023662705d96810758a40e018035f9a90b8df6e8250eb522e9ba9e188443ba7", msg.Meta["digest"],
		"the digest of the named field lands at the configured path")
}
