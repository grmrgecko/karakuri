package engine

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"hash"
	"net/netip"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// DefaultWebhookResponseTimeout bounds how long a synchronous webhook request
// waits for its flow to produce a response.
const DefaultWebhookResponseTimeout = 30 * time.Second

// WebhookRegistration describes one armed webhook trigger.
type WebhookRegistration struct {
	// Token is the path segment the route matches on.
	Token string
	// Methods is a comma-separated list of accepted methods, or "ANY". A single
	// name is the common case and reads the same as it always did.
	Methods string
	// AllowedIPs are the addresses and CIDR blocks that may call this webhook.
	// Empty accepts any caller.
	AllowedIPs []string
	// SignatureHeader names the header carrying the sender's HMAC. Empty means
	// signatures are not checked.
	SignatureHeader string
	// SignatureSecret is the shared secret the HMAC is computed with.
	SignatureSecret string
	// SignatureAlgorithm is sha256, sha1, or sha512. Empty means sha256.
	SignatureAlgorithm string
	// SignaturePrefix is stripped from the supplied value before comparing,
	// for senders like GitHub that write "sha256=<hex>".
	SignaturePrefix string
	// BodyFormat is how the request body becomes the trigger payload: "auto",
	// "text", "json", or "binary". It lives on the registration because the
	// HTTP route builds the message before any node runs.
	BodyFormat string
	// Synchronous holds the request open until the run answers it instead of
	// replying 202 immediately.
	Synchronous bool
	// ResponseTimeout bounds the wait in synchronous mode.
	ResponseTimeout time.Duration
	// Emit starts a run with the trigger message.
	Emit func(*Msg)
}

// webhookEntry is one armed webhook trigger as stored by the registry.
type webhookEntry struct {
	registration WebhookRegistration
}

// WebhookRegistry maps webhook tokens to armed webhook triggers. The HTTP
// layer resolves /hook/{token} requests through it; webhook nodes register
// themselves while armed. When two armed flows configure the same token the
// last one armed wins and a warning is logged; the earlier registration's
// unregister then becomes a no-op so it cannot tear down the winner.
type WebhookRegistry struct {
	log *zap.Logger

	mu      sync.Mutex
	entries map[string]*webhookEntry
}

// NewWebhookRegistry creates an empty webhook registry.
func NewWebhookRegistry(log *zap.Logger) *WebhookRegistry {
	if log == nil {
		log = zap.NewNop()
	}
	return &WebhookRegistry{log: log, entries: make(map[string]*webhookEntry)}
}

// Register arms a token. It returns an unregister function that removes the
// entry only while it is still the active registration for the token.
func (w *WebhookRegistry) Register(reg WebhookRegistration) func() {
	if reg.ResponseTimeout <= 0 {
		reg.ResponseTimeout = DefaultWebhookResponseTimeout
	}
	entry := &webhookEntry{registration: reg}
	w.mu.Lock()
	if _, exists := w.entries[reg.Token]; exists {
		w.log.Warn("webhook token registered by multiple armed flows; last armed wins",
			zap.String("token", reg.Token))
	}
	w.entries[reg.Token] = entry
	w.mu.Unlock()

	return func() {
		w.mu.Lock()
		if w.entries[reg.Token] == entry {
			delete(w.entries, reg.Token)
		}
		w.mu.Unlock()
	}
}

// Resolve looks up an armed token and checks the request method against its
// configuration. found reports whether the token is armed; allowed whether the
// method may fire it. The registration is complete only when both hold.
func (w *WebhookRegistry) Resolve(token, method string) (reg WebhookRegistration, found, allowed bool) {
	w.mu.Lock()
	entry, ok := w.entries[token]
	w.mu.Unlock()
	if !ok {
		return WebhookRegistration{}, false, false
	}
	if !entry.registration.acceptsMethod(method) {
		return WebhookRegistration{}, true, false
	}
	return entry.registration, true, true
}

// acceptsMethod reports whether the trigger answers this HTTP method. The
// configured value is a comma-separated list so a trigger can take PUT and
// PATCH without also taking DELETE, which one shared "ANY" could not express.
func (r WebhookRegistration) acceptsMethod(method string) bool {
	list := strings.TrimSpace(r.Methods)
	if list == "" {
		return true
	}
	for _, want := range strings.Split(list, ",") {
		want = strings.ToUpper(strings.TrimSpace(want))
		if want == "ANY" || want == strings.ToUpper(method) {
			return true
		}
	}
	return false
}

// AllowsIP reports whether a caller's address may reach this webhook. An empty
// allowlist accepts anyone, which is the default; an address that cannot be
// parsed is refused rather than waved through, since the alternative is an
// allowlist that silently stops applying behind a proxy that rewrites it.
func (r WebhookRegistration) AllowsIP(addr string) bool {
	if len(r.AllowedIPs) == 0 {
		return true
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	// A link-local caller is reported with the interface it arrived on. The
	// zone is not part of the address an entry names, and a prefix never
	// contains a zoned address.
	// An IPv4 caller seen through a dual-stack socket arrives IPv6-mapped,
	// and neither an IPv4 entry nor an IPv4 prefix matches that form.
	ip = ip.WithZone("").Unmap()
	// The mapped form is kept for a prefix that was itself written mapped.
	mapped := ip
	if ip.Is4() {
		mapped = netip.AddrFrom16(ip.As16())
	}
	for _, entry := range r.AllowedIPs {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			prefix, err := netip.ParsePrefix(entry)
			if err != nil {
				continue
			}
			if prefix.Contains(ip) || prefix.Contains(mapped) {
				return true
			}
			continue
		}
		allowed, err := netip.ParseAddr(entry)
		if err != nil {
			continue
		}
		if allowed.Unmap() == ip {
			return true
		}
	}
	return false
}

// VerifiesSignatures reports whether this trigger checks a sender's HMAC.
func (r WebhookRegistration) VerifiesSignatures() bool {
	return strings.TrimSpace(r.SignatureHeader) != "" && r.SignatureSecret != ""
}

// VerifySignature checks the sender's HMAC over the raw request body.
//
// The comparison is constant time, and both hex and base64 encodings of the
// same MAC are accepted because senders differ and the trigger cannot make them
// agree. This is the check that makes a public webhook mean anything: the token
// in the URL is a name, not a credential, and anything that learns it can fire
// the flow.
func (r WebhookRegistration) VerifySignature(supplied string, body []byte) bool {
	if !r.VerifiesSignatures() {
		return true
	}
	var newHash func() hash.Hash
	switch strings.ToLower(strings.TrimSpace(r.SignatureAlgorithm)) {
	case "", "sha256":
		newHash = sha256.New
	case "sha1":
		newHash = sha1.New
	case "sha512":
		newHash = sha512.New
	default:
		return false
	}

	mac := hmac.New(newHash, []byte(r.SignatureSecret))
	mac.Write(body)
	sum := mac.Sum(nil)

	given := strings.TrimSpace(supplied)
	given = strings.TrimPrefix(given, r.SignaturePrefix)
	wantHex := hex.EncodeToString(sum)
	wantB64 := base64.StdEncoding.EncodeToString(sum)
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(given)), []byte(wantHex)) == 1 ||
		subtle.ConstantTimeCompare([]byte(given), []byte(wantB64)) == 1
}

// defaultWebhooks is the process-wide registry shared by the webhook node
// (registration on arm) and the HTTP /hook route (resolution).
var defaultWebhooks = NewWebhookRegistry(nil)

// Webhooks returns the process-wide webhook registry.
func Webhooks() *WebhookRegistry { return defaultWebhooks }

// SetWebhookLogger attaches the server logger to the default registry so
// token collisions are visible in operational logs.
func SetWebhookLogger(log *zap.Logger) {
	if log != nil {
		defaultWebhooks.log = log
	}
}
