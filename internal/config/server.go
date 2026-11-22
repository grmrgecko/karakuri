package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/grmrgecko/karakuri/internal/resources"
	"github.com/spf13/viper"
)

// ServerConfig controls the HTTP listener and timeouts.
type ServerConfig struct {
	Host            string `mapstructure:"host"`
	Port            int    `mapstructure:"port"`
	BaseURL         string `mapstructure:"base_url"`
	ReadTimeout     int    `mapstructure:"read_timeout"`
	WriteTimeout    int    `mapstructure:"write_timeout"`
	ShutdownTimeout int    `mapstructure:"shutdown_timeout"`
	CORSOrigins     string `mapstructure:"cors_origins"`
	TrustProxy      bool   `mapstructure:"trust_proxy"`
	// TrustedProxies lists the peer addresses, as IPs or CIDRs separated by
	// commas, whose forwarding headers are believed when TrustProxy is on.
	// A request from any other peer keeps its own address.
	TrustedProxies string `mapstructure:"trusted_proxies"`
}

// DatabaseConfig controls the database type, DSN, and connection pool.
// Path is the SQLite database file; DSN is used for postgres and mysql.
type DatabaseConfig struct {
	Type            string `mapstructure:"type"`
	DSN             string `mapstructure:"dsn"`
	Path            string `mapstructure:"path"`
	MaxOpenConns    int    `mapstructure:"max_open_conns"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns"`
	ConnMaxLifetime int    `mapstructure:"conn_max_lifetime"`
}

// SecurityConfig controls JWT signing, password hashing, and payload encryption.
// EncryptionKey is reserved for AES-GCM encryption of stored secrets.
type SecurityConfig struct {
	JWTSecret     string `mapstructure:"jwt_secret"`
	JWTExpiry     int    `mapstructure:"jwt_expiry"`
	RefreshExpiry int    `mapstructure:"refresh_expiry"`
	BcryptCost    int    `mapstructure:"bcrypt_cost"`
	EncryptionKey string `mapstructure:"encryption_key"`
	// MaxUploadSize bounds a single dashboard widget upload in bytes.
	MaxUploadSize int64 `mapstructure:"max_upload_size"`
}

// LocationConfig is the server's default geographic position, used by schedule
// nodes triggering on sun events when they do not carry coordinates of their
// own. Most installations serve one house, so setting it once here beats
// repeating it on every node.
type LocationConfig struct {
	Latitude  float64 `mapstructure:"latitude"`
	Longitude float64 `mapstructure:"longitude"`
}

// EngineConfig controls flow execution concurrency and run timeouts.
type EngineConfig struct {
	MaxParallelNodes         int `mapstructure:"max_parallel_nodes"`
	MaxConcurrentRunsPerFlow int `mapstructure:"max_concurrent_runs_per_flow"`
	DefaultRunTimeout        int `mapstructure:"default_run_timeout"`
}

// ScriptConfig controls the sandboxed script node: what the network policy
// allows and the resource ceiling a single script execution may reach.
//
// Network selects one of three policies:
//   - "full" allows any destination the host can reach.
//   - "bogons" allows public addresses only, rejecting loopback, private, and
//     other reserved ranges so a script cannot reach the LAN or the host's own
//     services. The check runs against the resolved IP of every connection.
//   - "none" denies all outbound requests.
type ScriptConfig struct {
	Network         string `mapstructure:"network"`
	Timeout         int    `mapstructure:"timeout"`
	MaxOutput       int    `mapstructure:"max_output"`
	MaxHTTPRequests int    `mapstructure:"max_http_requests"`
	MaxHTTPResponse int    `mapstructure:"max_http_response"`
	MemoryLimitMB   int    `mapstructure:"memory_limit_mb"`
	// PythonWasm overrides discovery of the CPython WebAssembly module. When
	// empty the module is looked for in the standard resource directories.
	PythonWasm string `mapstructure:"python_wasm"`
	// PythonEnabled turns the Python runtime off entirely, for deployments
	// that do not want to ship or compile the 26 MB wasm module.
	PythonEnabled bool `mapstructure:"python_enabled"`
}

// StorageConfig bounds what the file nodes (file-read, file-write, file-watch)
// may touch. The nodes run as the service account and can reach anything it
// can, so Roots confines them and does it fail-closed, the way the WebSocket
// topic gate does: a path outside every root is refused, and an empty list
// refuses every path rather than allowing them all.
//
// Unset, Roots defaults to the files directory under the install's own data
// directory, which is where a flow's own output belongs. Reaching further is
// an explicit choice: name the directory, or write ["/"] to lift the
// confinement entirely. A path is accepted when it resolves inside a root,
// symlinks included, so a link planted under one cannot escape it. Wanting the
// whole disk is usually the sign that the work belongs on an agent, which runs
// as a user the OS already confines.
type StorageConfig struct {
	Roots       []string `mapstructure:"roots"`
	MaxReadSize int      `mapstructure:"max_read_size"`
}

// PluginsConfig locates plugin directories. Each path is scanned for
// subdirectories holding a manifest.yaml; a plugin contributes an executable,
// dashboard widget packages, or both. Leaving paths unset uses the standard
// resource search — `plugins/` beside the binary, then /etc/karakuri and
// /usr/share/karakuri — while an explicit empty list disables plugins
// entirely. HealthInterval bounds how often loaded plugin processes are
// checked for liveness, ShutdownTimeout how long a graceful shutdown gets.
type PluginsConfig struct {
	Paths           []string `mapstructure:"paths"`
	HealthInterval  int      `mapstructure:"health_interval"`
	ShutdownTimeout int      `mapstructure:"shutdown_timeout"`
}

// RenderConfig controls the headless browser that photographs dashboards.
// Workers is how many renders may run at once and MaxQueueSize how many may
// wait; a full queue is refused rather than buffered, since every render costs
// a browser page. Timeout bounds one page, from navigation to screenshot.
//
// BrowserPath names a Chrome or Chromium binary. Left empty the usual install
// locations are searched, and a host with none is told to install one: a
// server should not have a browser downloaded into a cache directory nobody
// chose. StoragePath holds the rendered files, defaulting to the renders
// directory under the data directory, and Retain is how long an ad-hoc render
// is kept there before the sweeper removes it. A scheduled render is kept
// until the next one of that schedule replaces it, whatever Retain says.
type RenderConfig struct {
	Workers      int    `mapstructure:"workers"`
	Timeout      int    `mapstructure:"timeout"`
	MaxQueueSize int    `mapstructure:"max_queue_size"`
	BrowserPath  string `mapstructure:"browser_path"`
	StoragePath  string `mapstructure:"storage_path"`
	Retain       int    `mapstructure:"retain"`
}

// ThemeConfig selects the dashboard theme applied when neither the user nor
// the dashboard picks one. Default names a theme package under themes/.
type ThemeConfig struct {
	Default string `mapstructure:"default"`
}

// Config holds all application configuration sections.
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	Security SecurityConfig `mapstructure:"security"`
	Logging  LoggingConfig  `mapstructure:"logging"`
	Engine   EngineConfig   `mapstructure:"engine"`
	Script   ScriptConfig   `mapstructure:"script"`
	Storage  StorageConfig  `mapstructure:"storage"`
	Location LocationConfig `mapstructure:"location"`
	Plugins  PluginsConfig  `mapstructure:"plugins"`
	Render   RenderConfig   `mapstructure:"render"`
	Theme    ThemeConfig    `mapstructure:"theme"`
	Agents   AgentsConfig   `mapstructure:"agents"`
	MCP      MCPConfig      `mapstructure:"mcp"`
	Update   UpdateConfig   `mapstructure:"update"`
}

// UpdateConfig controls automatic updates. A server started by a service
// manager checks GitHub releases once a day and installs a newer one over its
// own files when the account it runs as can write them, then exits so the
// manager starts the new version.
//
// Notify reports a newer release, in the event log and on the overview, on a
// server that does not install it itself: a container, which is updated by
// pulling a new image, or one with Enabled off. AllowDowngrade lets a release
// older than the one that last ran start on the database anyway, for someone
// who has decided to accept what the newer release changed.
type UpdateConfig struct {
	Enabled        bool `mapstructure:"enabled"`
	Prerelease     bool `mapstructure:"prerelease"`
	Notify         bool `mapstructure:"notify"`
	AllowDowngrade bool `mapstructure:"allow_downgrade"`
}

// AgentsConfig controls the remote-agent listener. Listen is the address agents
// dial, on every interface by default since an agent is a remote host by
// definition; empty turns the subsystem off. Nothing here names the address
// agents reach the server by: a join token carries the CA, and that is what an
// agent verifies. The TTLs bound enrollment tokens and issued client
// certificates.
type AgentsConfig struct {
	Listen    string `mapstructure:"listen"`
	EnrollTTL int    `mapstructure:"enroll_ttl"`
	CertTTL   int    `mapstructure:"cert_ttl"`
}

// MCPConfig controls the Model Context Protocol endpoint at /mcp, which lets
// an AI agent operate this instance with the same bearer credentials and
// api:* permissions the REST API enforces, behind the api:mcp:use gate an
// administrator grants per group.
type MCPConfig struct {
	AssistantTraceDir string `mapstructure:"assistant_trace_dir"`
	Enabled           bool   `mapstructure:"enabled"`
}

func setDefaults() {
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.base_url", "http://localhost:8080")
	viper.SetDefault("server.read_timeout", 30)
	viper.SetDefault("server.write_timeout", 30)
	viper.SetDefault("server.shutdown_timeout", 15)
	viper.SetDefault("server.cors_origins", "*")
	viper.SetDefault("server.trust_proxy", false)
	viper.SetDefault("server.trusted_proxies", "127.0.0.1/32,::1/128")

	viper.SetDefault("database.type", "sqlite")
	viper.SetDefault("database.dsn", "")
	viper.SetDefault("database.path", "")
	viper.SetDefault("database.max_open_conns", 25)
	viper.SetDefault("database.max_idle_conns", 5)
	viper.SetDefault("database.conn_max_lifetime", 300)

	viper.SetDefault("security.jwt_secret", DefaultJWTSecret)
	viper.SetDefault("security.jwt_expiry", 3600)
	viper.SetDefault("security.refresh_expiry", 86400)
	viper.SetDefault("security.bcrypt_cost", 12)
	viper.SetDefault("security.encryption_key", "")
	viper.SetDefault("security.max_upload_size", 1073741824)

	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "json")

	viper.SetDefault("engine.max_parallel_nodes", 4)
	viper.SetDefault("engine.max_concurrent_runs_per_flow", 10)
	viper.SetDefault("engine.default_run_timeout", 300)

	viper.SetDefault("script.network", "full")
	viper.SetDefault("script.timeout", 30)
	viper.SetDefault("script.max_output", 262144)
	viper.SetDefault("script.max_http_requests", 20)
	viper.SetDefault("script.max_http_response", 8388608)
	viper.SetDefault("script.memory_limit_mb", 256)
	viper.SetDefault("script.python_wasm", "")
	viper.SetDefault("script.python_enabled", true)

	// An unset roots list is not an empty one: unset confines the file nodes
	// to the install's own data directory, while an explicit empty list turns
	// them off. Set through viper rather than applyDerivedDefaults because
	// only viper can still tell the two apart.
	viper.SetDefault("storage.roots", []string{filepath.Join(resources.GetDataPath(), "files")})
	viper.SetDefault("storage.max_read_size", 8388608)

	// plugins.paths is deliberately left without a default: unset means "use
	// the resource search path", which an explicit empty list must not.
	viper.SetDefault("plugins.health_interval", 30)
	viper.SetDefault("plugins.shutdown_timeout", 10)

	viper.SetDefault("location.latitude", 0.0)
	viper.SetDefault("location.longitude", 0.0)

	viper.SetDefault("render.workers", 2)
	viper.SetDefault("render.timeout", 30)
	viper.SetDefault("render.max_queue_size", 0)
	viper.SetDefault("render.browser_path", "")
	viper.SetDefault("render.storage_path", "")
	viper.SetDefault("render.retain", 3600)

	viper.SetDefault("theme.default", "catppuccin-mocha")

	// The agent listener answers on every interface: an agent runs on another
	// machine, so a loopback default would only ever serve a demo. Enrolling
	// still takes a minted token. The TTLs are in seconds: a short window to
	// enroll, and a certificate life everything renews well before.
	viper.SetDefault("agents.listen", "0.0.0.0:9443")
	viper.SetDefault("agents.enroll_ttl", 900)
	viper.SetDefault("agents.cert_ttl", 7776000)
	viper.SetDefault("mcp.enabled", true)
	// Off unless the configuration turns it on, which production configs do,
	// so a development build or a container never replaces itself.
	viper.SetDefault("update.enabled", false)
	viper.SetDefault("update.prerelease", false)
	viper.SetDefault("update.notify", true)
	viper.SetDefault("update.allow_downgrade", false)
	viper.SetDefault("mcp.assistant_trace_dir", "")
}

// C is the package-level configuration used by all services after Load.
var C *Config

// Load reads configuration from file and environment variables, applying
// defaults for any unset values. Environment variables use the KARAKURI_ prefix.
// Sets the package-level C global on success.
func Load() error {
	setDefaults()

	viper.SetConfigType("yaml")
	viper.SetEnvPrefix("KARAKURI")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	configPath := resources.GetConfigPath()
	viper.SetConfigFile(configPath)

	if err := viper.ReadInConfig(); err != nil {
		// A missing file means the defaults and the environment apply. Any
		// other failure, a permission error or a YAML mistake, would otherwise
		// start the server on the placeholder secrets with only a warning
		// nobody reads.
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("reading config file %s: %w", configPath, err)
		}
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	cfg.applyDerivedDefaults()

	C = &cfg
	return nil
}

// applyDerivedDefaults fills in paths that depend on the data directory
// whenever the corresponding config field is still empty. For SQLite the
// DSN resolves to the database file path.
func (c *Config) applyDerivedDefaults() {
	if c.Render.StoragePath == "" {
		c.Render.StoragePath = filepath.Join(resources.GetDataPath(), "renders")
	}
	if c.Database.Type == "sqlite" {
		if c.Database.Path == "" {
			c.Database.Path = resources.GetDataPath() + "/karakuri.db"
		}
		if c.Database.DSN == "" {
			c.Database.DSN = c.Database.Path
		}
	}
}

// Placeholder secrets written by config gen. They are deliberately fixed and
// well known: regenerating a config must never invalidate an existing install,
// and a rotated encryption key silently orphans every secret already stored in
// the database. Operators replace them before exposing an instance; UsesDefaultSecrets
// reports when that has not happened yet.
const (
	// DefaultJWTSecret signs access tokens until replaced.
	DefaultJWTSecret = "CHANGE-ME-generate-with-openssl-rand-hex-32"
	// DefaultEncryptionKey encrypts stored connection secrets until replaced.
	// It must stay valid 64-character hex so a fresh install works out of the
	// box; the value spells out that it is not a real key.
	DefaultEncryptionKey = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
)

// Gen produces a YAML configuration template for the given environment.
func Gen(env string) string {
	switch env {
	case "dev":
		return devConfig(DefaultJWTSecret, DefaultEncryptionKey)
	default:
		return prodConfig(DefaultJWTSecret, DefaultEncryptionKey)
	}
}

// NewSecret returns a 64-character hex string from a cryptographic source, for
// operators generating replacements for the placeholders above.
func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// UsesDefaultSecrets reports which placeholder secrets are still in effect, so
// startup can warn about them by name.
func (c *Config) UsesDefaultSecrets() []string {
	var stale []string
	if c.Security.JWTSecret == DefaultJWTSecret {
		stale = append(stale, "security.jwt_secret")
	}
	if c.Security.EncryptionKey == DefaultEncryptionKey {
		stale = append(stale, "security.encryption_key")
	}
	return stale
}
