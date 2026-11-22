package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

const (
	jwtSign   = "sign"
	jwtVerify = "verify"
	jwtDecode = "decode"

	jwtRS256 = "RS256"
	jwtES256 = "ES256"
)

// JWTNode signs and reads JSON Web Tokens with asymmetric PEM keys.
type JWTNode struct{}

func (JWTNode) Meta() engine.NodeMeta {
	inputType := &flow.PortTypeRule{
		Cases: []flow.PortTypeCase{
			{Match: map[string][]string{"operation": {jwtVerify, jwtDecode}}, Type: flow.PortString},
		},
		Default:     flow.PortObject,
		PayloadPath: []string{"property"},
	}
	resultType := &flow.PortTypeRule{
		Cases: []flow.PortTypeCase{
			{Match: map[string][]string{"operation": {jwtVerify, jwtDecode}}, Type: flow.PortObject},
		},
		Default:     flow.PortString,
		PayloadPath: []string{"target", "property"},
	}
	return engine.NodeMeta{
		Type:        "jwt",
		Label:       "JWT",
		Folder:      flow.FolderUtility,
		Description: "Signs, verifies, or decodes a JSON Web Token",
		Icon:        "las la-id-badge",
		Retryable:   true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortObject, TypeRule: inputType},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortString, TypeFrom: "in", TypeRule: resultType},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:      "operation",
				Label:   "Operation",
				Type:    flow.FieldSelect,
				Default: jwtSign,
				Options: []engine.Option{
					{Value: jwtSign, Label: "Sign"},
					{Value: jwtVerify, Label: "Verify"},
					{Value: jwtDecode, Label: "Decode without verification"},
				},
			},
			parserSourceField("payload"),
			parserTargetField(),
			{
				ID:      "algorithm",
				Label:   "Signing method",
				Type:    flow.FieldSelect,
				Default: jwtRS256,
				Options: []engine.Option{
					{Value: jwtRS256, Label: "RS256"},
					{Value: jwtES256, Label: "ES256"},
				},
				ShowIf: &flow.ShowIf{Field: "operation", Equals: []string{jwtSign, jwtVerify}},
				Help:   "Verification rejects a token whose alg header names another method.",
			},
			{
				ID:          "private_key",
				Label:       "Private key",
				Type:        flow.FieldText,
				Required:    true,
				Placeholder: "-----BEGIN PRIVATE KEY-----",
				ShowIf:      &flow.ShowIf{Field: "operation", Equals: jwtSign},
				Help:        "PEM private key used to sign the claims.",
			},
			{
				ID:          "public_key",
				Label:       "Public key",
				Type:        flow.FieldText,
				Required:    true,
				Placeholder: "-----BEGIN PUBLIC KEY-----",
				ShowIf:      &flow.ShowIf{Field: "operation", Equals: jwtVerify},
				Help:        "PEM public key used to verify the signature.",
			},
		},
	}
}

type jwtConfig struct {
	converterConfig
	Operation  string `json:"operation"`
	Algorithm  string `json:"algorithm"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
}

// Execute performs the configured JWT operation and writes its result onto a
// copy of the input message.
func (JWTNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg jwtConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)
	property, value, err := parserInput(msg, cfg.Property)
	if err != nil {
		return nil, err
	}

	var result any
	switch cfg.Operation {
	case "", jwtSign:
		claims, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("property %q is %T, not a claims object", property, value)
		}
		result, err = signJWT(cfg, claims)
	case jwtVerify:
		var token string
		token, err = jwtTokenText(value, property)
		if err == nil {
			result, err = verifyJWT(cfg, token, execTime(ec))
		}
	case jwtDecode:
		var token string
		token, err = jwtTokenText(value, property)
		if err == nil {
			result, err = decodeJWT(token)
		}
	default:
		return nil, fmt.Errorf("unknown JWT operation %q", cfg.Operation)
	}
	if err != nil {
		return nil, err
	}

	out, err := parserResult(msg, property, cfg.Target, result)
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

func signJWT(cfg jwtConfig, claims map[string]any) (string, error) {
	method, err := jwtSigningMethod(cfg.Algorithm)
	if err != nil {
		return "", err
	}
	key, err := jwtPrivateKey(cfg.Algorithm, cfg.PrivateKey)
	if err != nil {
		return "", err
	}
	token, err := jwt.NewWithClaims(method, jwt.MapClaims(claims)).SignedString(key)
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}
	return token, nil
}

func verifyJWT(cfg jwtConfig, raw string, now time.Time) (map[string]any, error) {
	algorithm := strings.TrimSpace(cfg.Algorithm)
	if algorithm == "" {
		algorithm = jwtRS256
	}
	key, err := jwtPublicKey(algorithm, cfg.PublicKey)
	if err != nil {
		return nil, err
	}
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(jwt.WithValidMethods([]string{algorithm}), jwt.WithTimeFunc(func() time.Time { return now }))
	token, err := parser.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) { return key, nil })
	if err != nil {
		return nil, fmt.Errorf("verifying JWT: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("verifying JWT: token is invalid")
	}
	return map[string]any(claims), nil
}

func decodeJWT(raw string) (map[string]any, error) {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(raw, claims); err != nil {
		return nil, fmt.Errorf("decoding JWT: %w", err)
	}
	return map[string]any(claims), nil
}

func jwtTokenText(value any, property string) (string, error) {
	text, err := parserText(value, property)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

func jwtSigningMethod(algorithm string) (jwt.SigningMethod, error) {
	switch strings.TrimSpace(algorithm) {
	case "", jwtRS256:
		return jwt.SigningMethodRS256, nil
	case jwtES256:
		return jwt.SigningMethodES256, nil
	default:
		return nil, fmt.Errorf("unknown JWT signing method %q", algorithm)
	}
}

func jwtPrivateKey(algorithm, pem string) (any, error) {
	var key any
	var err error
	switch strings.TrimSpace(algorithm) {
	case "", jwtRS256:
		key, err = jwt.ParseRSAPrivateKeyFromPEM([]byte(pem))
	case jwtES256:
		key, err = jwt.ParseECPrivateKeyFromPEM([]byte(pem))
	default:
		return nil, fmt.Errorf("unknown JWT signing method %q", algorithm)
	}
	if err != nil {
		return nil, fmt.Errorf("parsing %s private key: %w", defaultJWTAlgorithm(algorithm), err)
	}
	return key, nil
}

func jwtPublicKey(algorithm, pem string) (any, error) {
	var key any
	var err error
	switch strings.TrimSpace(algorithm) {
	case "", jwtRS256:
		key, err = jwt.ParseRSAPublicKeyFromPEM([]byte(pem))
	case jwtES256:
		key, err = jwt.ParseECPublicKeyFromPEM([]byte(pem))
	default:
		return nil, fmt.Errorf("unknown JWT signing method %q", algorithm)
	}
	if err != nil {
		return nil, fmt.Errorf("parsing %s public key: %w", defaultJWTAlgorithm(algorithm), err)
	}
	return key, nil
}

func defaultJWTAlgorithm(algorithm string) string {
	if strings.TrimSpace(algorithm) == "" {
		return jwtRS256
	}
	return algorithm
}

func execTime(ec engine.ExecContext) time.Time {
	if clock := ec.Clock(); clock != nil {
		return clock.Now()
	}
	return time.Now()
}
