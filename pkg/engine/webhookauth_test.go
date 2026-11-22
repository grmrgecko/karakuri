package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWebhookVerifiesGitHubSignature checks the signature path against the
// scheme GitHub, Stripe, and most other senders use, with the expected MAC
// computed outside this code:
//
//	printf '%s' '{"action":"opened"}' | openssl dgst -sha256 -hmac 's3cr3t'
//
// This is the check that makes a public webhook mean anything: the token in the
// URL is a name, not a credential, so anything that learns it can fire the flow
// unless the body is signed.
func TestWebhookVerifiesGitHubSignature(t *testing.T) {
	const (
		body   = `{"action":"opened"}`
		digest = "893b8b176c39b217756497563a1b9ba1507d371642b1edf4ee9f876b8fd74ecc"
		b64    = "iTuLF2w5shd1ZJdWOhuboVB9NxZCse307p+Ha4/XTsw="
	)
	reg := WebhookRegistration{
		SignatureHeader: "X-Hub-Signature-256",
		SignatureSecret: "s3cr3t",
		SignaturePrefix: "sha256=",
	}

	tests := []struct {
		name     string
		supplied string
		want     bool
		why      string
	}{
		{"as GitHub sends it", "sha256=" + digest, true, "prefix stripped, hex compared"},
		{"without the prefix", digest, true, "TrimPrefix leaves a bare digest alone, so both spellings pass"},
		{"uppercased hex", "sha256=893B8B176C39B217756497563A1B9BA1507D371642B1EDF4EE9F876B8FD74ECC", true, "hex is case-insensitive and some senders uppercase it"},
		{"base64", b64, true, "the other encoding of the same MAC"},
		{"one character off", "sha256=" + digest[:63] + "0", false, "a flipped character must fail"},
		{"empty", "", false, "an unsigned request fails the check"},
		{"another secret's MAC", "sha256=0000000000000000000000000000000000000000000000000000000000000000", false, "a MAC this secret did not produce fails"},
	}

	for _, tc := range tests {
		assert.Equal(t, tc.want, reg.VerifySignature(tc.supplied, []byte(body)), "%s: %s", tc.name, tc.why)
	}

	// A body that changed after signing is the attack the check exists for.
	assert.False(t, reg.VerifySignature("sha256="+digest, []byte(`{"action":"closed"}`)),
		"the MAC is over the body, so editing the body has to invalidate it")

	// A trigger with no signature configured verifies nothing, which is the
	// default and must not accidentally reject every caller.
	assert.True(t, WebhookRegistration{}.VerifySignature("", []byte(body)),
		"a trigger that does not verify accepts whatever it is given")
	assert.False(t, WebhookRegistration{}.VerifiesSignatures(), "and reports that it does not verify")
}

// TestWebhookMethodAndAddressGates pins the two coarse gates. The method list
// is what lets a trigger take PUT and PATCH without also taking DELETE, which
// the old single value could not express, and the allowlist has to accept a
// CIDR because that is how anyone writes "my LAN".
func TestWebhookMethodAndAddressGates(t *testing.T) {
	methods := []struct {
		configured string
		method     string
		want       bool
		why        string
	}{
		{"POST", "POST", true, "the single-value form still reads the way it always did"},
		{"POST", "GET", false, "and still rejects everything else"},
		{"PUT,PATCH", "PATCH", true, "a list accepts any of its entries"},
		{"PUT,PATCH", "DELETE", false, "and nothing outside them, which is the point of the list"},
		{"put, patch", "PUT", true, "spacing and case are how a person types it, not part of the value"},
		{"ANY", "DELETE", true, "the wildcard still means everything"},
		{"POST,ANY", "HEAD", true, "a wildcard anywhere in the list wins"},
		{"", "GET", true, "an unset list accepts anything rather than nothing"},
	}
	for _, tc := range methods {
		reg := WebhookRegistration{Methods: tc.configured}
		assert.Equal(t, tc.want, reg.acceptsMethod(tc.method), "%q accepting %s: %s", tc.configured, tc.method, tc.why)
	}

	addresses := []struct {
		allowed []string
		addr    string
		want    bool
		why     string
	}{
		{nil, "203.0.113.9", true, "no allowlist accepts anyone, which is the default"},
		{[]string{"10.0.0.0/8"}, "10.4.2.1", true, "a CIDR is how a LAN is written"},
		{[]string{"10.0.0.0/8"}, "203.0.113.9", false, "and it excludes what is outside it"},
		{[]string{"192.168.1.50"}, "192.168.1.50", true, "a bare address matches itself"},
		{[]string{"192.168.1.50"}, "192.168.1.51", false, "and only itself"},
		{[]string{"10.0.0.0/8", "192.168.1.50"}, "192.168.1.50", true, "entries are alternatives"},
		{[]string{"10.0.0.0/8"}, "not-an-address", false, "an unparseable caller is refused rather than waved through"},
		{[]string{"nonsense"}, "10.0.0.1", false, "an unparseable entry matches nothing instead of everything"},
		{[]string{"::1"}, "::1", true, "IPv6 works, since that is what a local caller often is"},
		{[]string{"fe80::/10"}, "fe80::1c2d:3eff:fe4f:5a6b%eth0", true, "a link-local caller arrives with its interface zone, which is not part of the address a block names"},
		{[]string{"fe80::1c2d:3eff:fe4f:5a6b"}, "fe80::1c2d:3eff:fe4f:5a6b%eth0", true, "and the zone must not stop a bare address matching itself"},
		{[]string{"192.168.1.0/24"}, "::ffff:192.168.1.50", true, "a proxy on a dual-stack socket reports an IPv4 caller in its IPv6-mapped form, which is still the address the block names"},
		{[]string{"192.168.1.50"}, "::ffff:192.168.1.50", true, "and the mapped form must not stop a bare address matching itself"},
		{[]string{"::ffff:192.168.1.50"}, "192.168.1.50", true, "nor must an entry written in the mapped form refuse the plain caller"},
		{[]string{"::ffff:192.168.1.0/120"}, "::ffff:192.168.1.50", true, "a block written in the mapped form keeps matching the mapped caller it names"},
		{[]string{"192.168.1.0/24"}, "::ffff:203.0.113.9", false, "a mapped caller outside the block is still refused"},
	}
	for _, tc := range addresses {
		reg := WebhookRegistration{AllowedIPs: tc.allowed}
		assert.Equal(t, tc.want, reg.AllowsIP(tc.addr), "%v allowing %s: %s", tc.allowed, tc.addr, tc.why)
	}
}
