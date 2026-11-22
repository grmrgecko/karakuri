package core

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/crypto"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// Crypto operations.
const (
	cryptoHash    = "hash"
	cryptoHMAC    = "hmac"
	cryptoVerify  = "verify"
	cryptoEncrypt = "encrypt"
	cryptoDecrypt = "decrypt"
	cryptoRandom  = "random"
	cryptoTOTP    = "totp"
)

// cryptoTypeRule derives what the crypto node emits. Every operation writes a
// string except verify, which writes the answer to a yes-or-no question. The
// payload path is what keeps that honest: a signature written to meta.signature
// leaves the payload exactly as it arrived, so the port must not claim to carry
// a string.
func cryptoTypeRule() *flow.PortTypeRule {
	return &flow.PortTypeRule{
		Cases: []flow.PortTypeCase{
			{Match: map[string][]string{"operation": {cryptoVerify}}, Type: flow.PortBool},
		},
		Default:     flow.PortString,
		PayloadPath: []string{"target"},
	}
}

// CryptoNode hashes, signs, verifies, encrypts, and generates random values.
//
// Verify is the one that earns the node on its own: a webhook authenticated
// only by the token in its URL is not authenticated, and checking the signature
// the sender computed over the raw body is the fix. It compares in constant
// time, which is the reason this is a node rather than four lines in a script.
//
// Encryption is AES-256-GCM through pkg/crypto, the same sealing used for
// secrets at rest, and takes the same 32-byte hex key that config uses. There
// is no passphrase and no key derivation: one key format across the system
// beats a menu of ciphers nobody can tell apart. Generate one with this node's
// random operation, 32 bytes as hex.
type CryptoNode struct{}

func (CryptoNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "cryptography",
		Label:       "Cryptography",
		Folder:      flow.FolderUtility,
		Description: "Hashes, signs, verifies, encrypts, and generates random values",
		Icon:        "las la-key",
		Retryable:   true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortString, TypeFrom: "in", TypeRule: cryptoTypeRule()},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:      "operation",
				Label:   "Operation",
				Type:    flow.FieldSelect,
				Default: cryptoHash,
				Options: []engine.Option{
					{Value: cryptoHash, Label: "Hash"},
					{Value: cryptoHMAC, Label: "Sign (HMAC)"},
					{Value: cryptoVerify, Label: "Verify a signature (HMAC)"},
					{Value: cryptoEncrypt, Label: "Encrypt"},
					{Value: cryptoDecrypt, Label: "Decrypt"},
					{Value: cryptoRandom, Label: "Generate a random value"},
					{Value: cryptoTOTP, Label: "Generate a TOTP code"},
				},
			},
			{
				ID:          "source",
				Label:       "Value",
				Type:        flow.FieldString,
				Default:     "payload",
				Placeholder: "payload.body",
				ShowIf: &flow.ShowIf{Field: "operation", Equals: []string{
					cryptoHash, cryptoHMAC, cryptoVerify, cryptoEncrypt, cryptoDecrypt,
				}},
				Help: "The value to operate on. Dot path into the message; a bare path is read " +
					"from the payload. Binary is used as it stands, anything else as its text.",
			},
			{
				ID:          "target",
				Label:       "Write the result to",
				Type:        flow.FieldString,
				Default:     "payload",
				Placeholder: "meta.signature",
				Help:        "Dot path on the outgoing message. A bare path is written inside the payload.",
			},
			{
				ID:      "algorithm",
				Label:   "Algorithm",
				Type:    flow.FieldSelect,
				Default: "sha256",
				Options: []engine.Option{
					{Value: "sha256", Label: "SHA-256"},
					{Value: "sha512", Label: "SHA-512"},
					{Value: "sha1", Label: "SHA-1"},
					{Value: "md5", Label: "MD5"},
				},
				ShowIf: &flow.ShowIf{Field: "operation", Equals: []string{cryptoHash, cryptoHMAC, cryptoVerify}},
				Help:   "SHA-1 and MD5 are here for checksums and for talking to systems that ask for them, not for signing anything new.",
			},
			{
				// TOTP gets a field of its own rather than sharing the one
				// above, because the defaults point opposite ways. Hashing
				// should default to SHA-256; TOTP has to default to SHA-1,
				// which is what RFC 6238 specifies and what every authenticator
				// app issues. Sharing the field would hand a new user SHA-256
				// and codes that are rejected everywhere with no clue why.
				ID:      "totp_algorithm",
				Label:   "Algorithm",
				Type:    flow.FieldSelect,
				Default: "sha1",
				Options: []engine.Option{
					{Value: "sha1", Label: "SHA-1"},
					{Value: "sha256", Label: "SHA-256"},
					{Value: "sha512", Label: "SHA-512"},
				},
				ShowIf: &flow.ShowIf{Field: "operation", Equals: cryptoTOTP},
				Help:   "SHA-1 unless the site said otherwise, which almost none do.",
			},
			{
				ID:      "encoding",
				Label:   "Result encoding",
				Type:    flow.FieldSelect,
				Default: "hex",
				Options: []engine.Option{
					{Value: "hex", Label: "Hex"},
					{Value: "base64", Label: "Base64"},
				},
				ShowIf: &flow.ShowIf{Field: "operation", Equals: []string{cryptoHash, cryptoHMAC, cryptoRandom}},
			},
			{
				ID:      "secret",
				Label:   "Secret",
				Type:    flow.FieldString,
				Control: "password",
				ShowIf:  &flow.ShowIf{Field: "operation", Equals: []string{cryptoHMAC, cryptoVerify, cryptoTOTP}},
				Help: "The shared secret. For a signature it is used as written; for TOTP it is the " +
					"base32 string the site showed beside the QR code, spaces and padding included.",
			},
			{
				ID:          "expected",
				Label:       "Signature to check",
				Type:        flow.FieldString,
				Placeholder: "meta.headers.x-hub-signature-256",
				Required:    true,
				ShowIf:      &flow.ShowIf{Field: "operation", Equals: cryptoVerify},
				Help:        "Dot path to the signature the sender supplied.",
			},
			{
				ID:          "prefix",
				Label:       "Signature prefix",
				Type:        flow.FieldString,
				Placeholder: "sha256=",
				ShowIf:      &flow.ShowIf{Field: "operation", Equals: cryptoVerify},
				Help:        "Stripped from the supplied signature before comparing. GitHub sends sha256=, most senders send nothing.",
			},
			{
				ID:      "key",
				Label:   "Key",
				Type:    flow.FieldString,
				Control: "password",
				ShowIf:  &flow.ShowIf{Field: "operation", Equals: []string{cryptoEncrypt, cryptoDecrypt}},
				Help:    "AES-256 key as 64 hex characters. Generate one with this node's random operation: 32 bytes, hex.",
			},
			{
				ID:      "kind",
				Label:   "Generate",
				Type:    flow.FieldSelect,
				Default: "uuid",
				Options: []engine.Option{
					{Value: "uuid", Label: "A UUID"},
					{Value: "bytes", Label: "Random bytes"},
				},
				ShowIf: &flow.ShowIf{Field: "operation", Equals: cryptoRandom},
			},
			{
				ID:          "length",
				Label:       "Bytes",
				Type:        flow.FieldString,
				Default:     "32",
				Placeholder: "32",
				ShowIf:      &flow.ShowIf{Field: "kind", Equals: "bytes"},
				Help:        "How many random bytes to generate before encoding them.",
			},
			{
				ID:          "digits",
				Label:       "Digits",
				Type:        flow.FieldString,
				Default:     "6",
				Placeholder: "6",
				ShowIf:      &flow.ShowIf{Field: "operation", Equals: cryptoTOTP},
				Help:        "Length of the code. Almost every site uses 6.",
			},
			{
				ID:          "period",
				Label:       "Step",
				Type:        flow.FieldDuration,
				Default:     "30s",
				Placeholder: "30s",
				ShowIf:      &flow.ShowIf{Field: "operation", Equals: cryptoTOTP},
				Help:        "How long one code lasts. RFC 6238 says 30s and almost nothing differs.",
			},
		},
	}
}

// cryptoConfig is the decoded crypto node configuration.
type cryptoConfig struct {
	Operation string `json:"operation"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	Algorithm string `json:"algorithm"`
	Encoding  string `json:"encoding"`
	Secret    string `json:"secret"`
	Expected  string `json:"expected"`
	Prefix    string `json:"prefix"`
	Key       string `json:"key"`
	Kind      string `json:"kind"`
	Length    string `json:"length"`
	TOTPAlgo  string `json:"totp_algorithm"`
	Digits    string `json:"digits"`
	Period    string `json:"period"`
}

// Execute performs the configured operation and writes the result onto a copy
// of the message.
func (CryptoNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg cryptoConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	var result any
	var err error
	switch cfg.Operation {
	case "", cryptoHash:
		result, err = cryptoDoHash(cfg, msg)
	case cryptoHMAC:
		result, err = cryptoDoHMAC(cfg, msg)
	case cryptoVerify:
		result, err = cryptoDoVerify(cfg, msg)
	case cryptoEncrypt:
		result, err = cryptoDoEncrypt(cfg, msg)
	case cryptoDecrypt:
		result, err = cryptoDoDecrypt(cfg, msg)
	case cryptoRandom:
		result, err = cryptoDoRandom(cfg)
	case cryptoTOTP:
		result, err = cryptoDoTOTP(cfg, ec.Clock().Now())
	default:
		return nil, fmt.Errorf("unknown crypto operation %q", cfg.Operation)
	}
	if err != nil {
		return nil, err
	}

	// Write onto a copy so the upstream message the engine recorded stays as
	// it was.
	target := strings.TrimSpace(cfg.Target)
	if target == "" {
		target = "payload"
	}
	out := &engine.Msg{
		Payload: deepCopyValue(msg.Payload),
		Topic:   msg.Topic,
		Meta:    nodeutil.CloneMeta(msg.Meta),
		Group:   msg.Group,
	}
	if err := setProperty(out, target, result); err != nil {
		return nil, fmt.Errorf("cannot write the result to %q: %w", target, err)
	}
	return engine.One("out", out), nil
}

// cryptoDoHash digests the source value.
func cryptoDoHash(cfg cryptoConfig, msg *engine.Msg) (any, error) {
	h, err := cryptoHasher(cfg.Algorithm)
	if err != nil {
		return nil, err
	}
	data, err := cryptoSource(cfg, msg)
	if err != nil {
		return nil, err
	}
	h.Write(data)
	return cryptoEncode(cfg.Encoding, h.Sum(nil))
}

// cryptoDoHMAC signs the source value with the shared secret.
func cryptoDoHMAC(cfg cryptoConfig, msg *engine.Msg) (any, error) {
	mac, err := cryptoMAC(cfg, msg)
	if err != nil {
		return nil, err
	}
	return cryptoEncode(cfg.Encoding, mac)
}

// cryptoDoVerify recomputes the signature and compares it with the one the
// sender supplied. The comparison is constant time, and both hex and base64
// encodings of the same signature are accepted, because the node cannot make
// the sender say which one it used.
func cryptoDoVerify(cfg cryptoConfig, msg *engine.Msg) (any, error) {
	path := strings.TrimSpace(cfg.Expected)
	if path == "" {
		return nil, fmt.Errorf("verify needs the path to the signature to check")
	}
	supplied, found := resolveProperty(msg, path)
	if !found {
		// A missing signature is a failed check, not a broken node: an
		// unsigned request is exactly what this is here to catch.
		return false, nil
	}
	mac, err := cryptoMAC(cfg, msg)
	if err != nil {
		return nil, err
	}

	given := strings.TrimSpace(stringify(supplied))
	given = strings.TrimPrefix(given, cfg.Prefix)
	wantHex := hex.EncodeToString(mac)
	wantB64 := base64.StdEncoding.EncodeToString(mac)
	ok := subtle.ConstantTimeCompare([]byte(strings.ToLower(given)), []byte(wantHex)) == 1 ||
		subtle.ConstantTimeCompare([]byte(given), []byte(wantB64)) == 1
	return ok, nil
}

// cryptoDoEncrypt seals the source value under the configured key.
func cryptoDoEncrypt(cfg cryptoConfig, msg *engine.Msg) (any, error) {
	key, err := crypto.KeyFromHex(strings.TrimSpace(cfg.Key))
	if err != nil {
		return nil, err
	}
	data, err := cryptoSource(cfg, msg)
	if err != nil {
		return nil, err
	}
	return crypto.Encrypt(key, data)
}

// cryptoDoDecrypt opens a value sealed by the encrypt operation. The plaintext
// comes back as text, so binary plaintext should be base64 encoded before it is
// sealed and decoded again after.
func cryptoDoDecrypt(cfg cryptoConfig, msg *engine.Msg) (any, error) {
	key, err := crypto.KeyFromHex(strings.TrimSpace(cfg.Key))
	if err != nil {
		return nil, err
	}
	data, err := cryptoSource(cfg, msg)
	if err != nil {
		return nil, err
	}
	plaintext, err := crypto.Decrypt(key, string(data))
	if err != nil {
		return nil, err
	}
	return string(plaintext), nil
}

// cryptoDoRandom generates a fresh value, reading nothing from the message.
func cryptoDoRandom(cfg cryptoConfig) (any, error) {
	switch cfg.Kind {
	case "", "uuid":
		return uuid.NewString(), nil
	case "bytes":
		n, err := parseGroupCount(cfg.Length)
		if err != nil {
			return nil, fmt.Errorf("bytes: %w", err)
		}
		if n == 0 {
			n = 32
		}
		buf := make([]byte, n)
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("generating %d random bytes: %w", n, err)
		}
		return cryptoEncode(cfg.Encoding, buf)
	default:
		return nil, fmt.Errorf("unknown random kind %q", cfg.Kind)
	}
}

// cryptoDoTOTP computes the current time-based one-time code (RFC 6238), for a
// flow that has to sign in to something that asks for one.
//
// The time comes from the injectable clock rather than time.Now, like every
// other clock read in a node, which is also what makes it testable against the
// RFC's published vectors.
func cryptoDoTOTP(cfg cryptoConfig, now time.Time) (any, error) {
	if cfg.Secret == "" {
		return nil, fmt.Errorf("totp needs the shared secret")
	}
	key, err := decodeBase32Secret(cfg.Secret)
	if err != nil {
		return nil, err
	}
	digits, err := parseGroupCount(cfg.Digits)
	if err != nil {
		return nil, fmt.Errorf("digits: %w", err)
	}
	if digits == 0 {
		digits = 6
	}
	if digits > 9 {
		// The truncation yields a 31-bit number, so ten digits would always
		// begin with a zero or one rather than being uniform.
		return nil, fmt.Errorf("digits must be 9 or fewer, got %d", digits)
	}
	period, err := parseDurationDefault(cfg.Period, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("invalid step %q: %w", cfg.Period, err)
	}
	if period < time.Second {
		return nil, fmt.Errorf("step must be at least one second")
	}

	// An unset algorithm is SHA-1 here rather than the hashing default, so a
	// hand-written graph that omits the field still produces working codes.
	algorithm := cfg.TOTPAlgo
	if algorithm == "" {
		algorithm = "sha1"
	}
	newHash, err := cryptoHashFunc(algorithm)
	if err != nil {
		return nil, err
	}
	// The counter is the number of whole steps since the Unix epoch, as eight
	// big-endian bytes.
	counter := uint64(now.Unix() / int64(period.Seconds()))
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(newHash, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	// Dynamic truncation: the low nibble of the last byte picks where to read
	// four bytes, and the top bit is masked off so the result is positive.
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, value%mod), nil
}

// decodeBase32Secret reads the base32 string an authenticator app shows.
// Those are written in groups with spaces and usually without padding, so both
// are fixed up rather than handed back as an error nobody can act on.
func decodeBase32Secret(secret string) ([]byte, error) {
	cleaned := strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(secret)))
	if pad := len(cleaned) % 8; pad != 0 {
		cleaned += strings.Repeat("=", 8-pad)
	}
	key, err := base32.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return nil, fmt.Errorf("the secret is not valid base32: %w", err)
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("the secret is empty")
	}
	return key, nil
}

// cryptoMAC computes the HMAC shared by the sign and verify operations.
func cryptoMAC(cfg cryptoConfig, msg *engine.Msg) ([]byte, error) {
	if cfg.Secret == "" {
		return nil, fmt.Errorf("%s needs a secret", cfg.Operation)
	}
	newHash, err := cryptoHashFunc(cfg.Algorithm)
	if err != nil {
		return nil, err
	}
	data, err := cryptoSource(cfg, msg)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(newHash, []byte(cfg.Secret))
	mac.Write(data)
	return mac.Sum(nil), nil
}

// cryptoSource reads the bytes an operation acts on. Binary is used as it
// stands, which is what makes a signature over a raw webhook body come out
// right; everything else is its text, so a JSON payload signs as the document.
func cryptoSource(cfg cryptoConfig, msg *engine.Msg) ([]byte, error) {
	path := strings.TrimSpace(cfg.Source)
	if path == "" {
		path = "payload"
	}
	value, found := resolveProperty(msg, path)
	if !found {
		return nil, fmt.Errorf("value %q is not present on the message", path)
	}
	if b, ok := value.([]byte); ok {
		return b, nil
	}
	return []byte(stringify(value)), nil
}

// cryptoHashFunc resolves the algorithm name to a hash constructor, which is
// what HMAC needs.
func cryptoHashFunc(name string) (func() hash.Hash, error) {
	switch name {
	case "", "sha256":
		return sha256.New, nil
	case "sha512":
		return sha512.New, nil
	case "sha1":
		return sha1.New, nil
	case "md5":
		return md5.New, nil
	default:
		return nil, fmt.Errorf("unknown algorithm %q", name)
	}
}

// cryptoHasher builds one hash from the algorithm name.
func cryptoHasher(name string) (hash.Hash, error) {
	newHash, err := cryptoHashFunc(name)
	if err != nil {
		return nil, err
	}
	return newHash(), nil
}

// cryptoEncode renders a digest as text.
func cryptoEncode(encoding string, data []byte) (string, error) {
	switch encoding {
	case "", "hex":
		return hex.EncodeToString(data), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(data), nil
	default:
		return "", fmt.Errorf("unknown result encoding %q", encoding)
	}
}
