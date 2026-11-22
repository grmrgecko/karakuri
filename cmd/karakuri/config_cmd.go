package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/resources"
	"github.com/grmrgecko/karakuri/pkg/agenthub"
	"github.com/grmrgecko/karakuri/pkg/crypto"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/models"
	"gorm.io/gorm"
)

// ConfigCmd groups configuration subcommands.
type ConfigCmd struct {
	Gen       ConfigGenCmd       `cmd:"" help:"Generate configuration file."`
	RotateKey ConfigRotateKeyCmd `cmd:"" help:"Re-encrypt stored secrets under a new encryption key."`
}

// ConfigGenCmd writes a YAML configuration template to stdout.
type ConfigGenCmd struct {
	Env string `help:"Environment (dev, prod)." default:"prod"`
}

func (c *ConfigGenCmd) Run() error {
	fmt.Print(config.Gen(c.Env))
	return nil
}

// ConfigRotateKeyCmd migrates every secret stored at rest from the configured
// security.encryption_key to a new one, then writes the new key into the
// config file. Both halves must move together: a rotated key without rotated
// data (or the reverse) leaves every stored credential undecryptable.
type ConfigRotateKeyCmd struct {
	Key       string `help:"New 64-character hex key. Generated when omitted."`
	DryRun    bool   `help:"Report what would be re-encrypted without changing anything." default:"false"`
	PrintOnly bool   `help:"Re-encrypt stored secrets but leave the config file for you to update." default:"false"`
}

// encryptedRecord is one stored ciphertext staged for rewriting.
type encryptedRecord struct {
	kind    string
	id      string
	name    string
	rotated string
}

func (c *ConfigRotateKeyCmd) Run() error {
	// Resolve the keys before touching anything.
	oldKey, err := crypto.KeyFromHex(config.C.Security.EncryptionKey)
	if err != nil {
		return fmt.Errorf("current security.encryption_key is unusable: %w", err)
	}
	newHex := c.Key
	if newHex == "" {
		if newHex, err = config.NewSecret(); err != nil {
			return fmt.Errorf("failed to generate a new key: %w", err)
		}
	}
	newKey, err := crypto.KeyFromHex(newHex)
	if err != nil {
		return fmt.Errorf("new key is unusable: %w", err)
	}
	if newHex == config.C.Security.EncryptionKey {
		return fmt.Errorf("the new key is identical to the current one")
	}

	if err := initDatabaseQuiet(); err != nil {
		return err
	}

	// Verify every stored secret opens with the current key before writing
	// anything, so a partially rotatable database aborts intact.
	conns, err := models.ListConnections()
	if err != nil {
		return fmt.Errorf("failed to load connections: %w", err)
	}
	var staged []encryptedRecord
	var unreadable []string
	for _, conn := range conns {
		if conn.Config == "" {
			continue
		}
		plaintext, err := crypto.Decrypt(oldKey, conn.Config)
		if err != nil {
			unreadable = append(unreadable, "connection "+conn.Name)
			continue
		}
		sealed, err := crypto.Encrypt(newKey, plaintext)
		if err != nil {
			return fmt.Errorf("failed to re-encrypt connection %q: %w", conn.Name, err)
		}
		staged = append(staged, encryptedRecord{
			kind: "connection", id: conn.ID.String(), name: conn.Name, rotated: sealed,
		})
	}
	var apiKeys []*models.APIKey
	if err := db.DB.Where("encrypted_key <> ''").Order("name ASC").Find(&apiKeys).Error; err != nil {
		return fmt.Errorf("failed to load API keys: %w", err)
	}
	for _, apiKey := range apiKeys {
		plaintext, err := crypto.Decrypt(oldKey, apiKey.EncryptedKey)
		if err != nil {
			unreadable = append(unreadable, "API key "+apiKey.Name)
			continue
		}
		sealed, err := crypto.Encrypt(newKey, plaintext)
		if err != nil {
			return fmt.Errorf("failed to re-encrypt API key %q: %w", apiKey.Name, err)
		}
		staged = append(staged, encryptedRecord{
			kind: "api-key", id: fmt.Sprint(apiKey.ID), name: "API key " + apiKey.Name, rotated: sealed,
		})
	}
	// The agent CA's private keys and every agent's credential-store key are
	// sealed under the same setting. Left behind, the next start refuses to
	// build the agent hub and every enrolled identity is cut off with it.
	caRekey, err := agenthub.StageCARekey(oldKey, newKey)
	if errors.Is(err, crypto.ErrDecrypt) {
		unreadable = append(unreadable, err.Error())
	} else if err != nil {
		return fmt.Errorf("failed to load the agent CA: %w", err)
	}
	agentKeys, err := models.StageAgentCredentialRekey(oldKey, newKey)
	if errors.Is(err, crypto.ErrDecrypt) {
		unreadable = append(unreadable, err.Error())
	} else if err != nil {
		return fmt.Errorf("failed to load agent credential keys: %w", err)
	}
	// The backup schedule keeps its passphrase sealed the same way. Left
	// behind, every scheduled backup fails once the key changes.
	backupSchedule, err := stageBackupPassphraseRekey(oldKey, newKey)
	if errors.Is(err, crypto.ErrDecrypt) {
		unreadable = append(unreadable, "the stored backup passphrase")
	} else if err != nil {
		return fmt.Errorf("failed to load the backup schedule: %w", err)
	}
	if len(unreadable) > 0 {
		return fmt.Errorf("these secrets do not open with the current key, so they "+
			"cannot be rotated: %s\nReplace each unreadable secret, then rotate",
			strings.Join(unreadable, ", "))
	}

	names := make([]string, 0, len(staged)+len(agentKeys)+1)
	for _, rec := range staged {
		names = append(names, rec.name)
	}
	if caRekey != nil {
		names = append(names, fmt.Sprintf("agent CA (%d private key(s))", caRekey.Count()))
	}
	for _, rk := range agentKeys {
		names = append(names, "credential key of agent "+rk.Name)
	}
	if backupSchedule != "" {
		names = append(names, "backup passphrase")
	}

	if c.DryRun {
		fmt.Printf("Would re-encrypt %d stored secret(s):\n", len(names))
		for _, name := range names {
			fmt.Printf("  - %s\n", name)
		}
		fmt.Printf("\nNothing was changed.\n")
		return nil
	}

	// Stage the config rewrite before committing, so a config file that cannot
	// be written aborts while the database is still readable.
	configPath := resources.GetConfigPath()
	var pending *pendingConfig
	if !c.PrintOnly {
		if pending, err = stageConfigKey(configPath, newHex); err != nil {
			return err
		}
		defer pending.cleanup()
	}

	// Persist to the database.
	if err := db.DB.Transaction(func(tx *gorm.DB) error {
		for _, rec := range staged {
			var result *gorm.DB
			switch rec.kind {
			case "connection":
				result = tx.Model(&models.Connection{}).Where("id = ?", rec.id).Update("config", rec.rotated)
			case "api-key":
				result = tx.Model(&models.APIKey{}).Where("id = ?", rec.id).Update("encrypted_key", rec.rotated)
			default:
				return fmt.Errorf("unknown encrypted record kind %q", rec.kind)
			}
			if result.Error != nil {
				return fmt.Errorf("failed to update %s: %w", rec.name, result.Error)
			}
		}
		if caRekey != nil {
			if err := caRekey.Apply(tx); err != nil {
				return err
			}
		}
		for _, rk := range agentKeys {
			if err := rk.Apply(tx); err != nil {
				return fmt.Errorf("failed to update the credential key of agent %s: %w", rk.Name, err)
			}
		}
		if backupSchedule != "" {
			if err := models.SetSettingIn(tx, backupSettingKey, backupSchedule); err != nil {
				return fmt.Errorf("failed to update the backup passphrase: %w", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	fmt.Printf("Re-encrypted %d stored secret(s).\n", len(names))
	// No server is running to record this, so the command writes its own
	// security entry. Failing to write it does not undo the rotation.
	osUser := os.Getenv("USER")
	if osUser == "" {
		osUser = os.Getenv("USERNAME")
	}
	_ = events.WriteNow(db.DB, events.Entry{
		Category: models.EventSecurity,
		Subject:  events.Subject{Kind: "server", Name: "encryption key"},
		Message:  fmt.Sprintf("Rotated the encryption key and re-encrypted %d stored secrets", len(names)),
		Actor:    events.Actor{Kind: events.ActorSystem, Name: osUser, Via: events.ViaCLI},
	})

	// Move the config into place last; the key is reported either way so a
	// failure here is recoverable by hand.
	if pending != nil {
		if err := pending.commit(); err != nil {
			fmt.Printf("\nThe database was rotated but %s could not be updated: %v\n", configPath, err)
			fmt.Printf("Set this immediately or stored secrets will not decrypt:\n")
			fmt.Printf("  encryption_key: %q\n", newHex)
			return err
		}
		fmt.Printf("Updated %s with the new key.\n", configPath)
		fmt.Printf("Restart karakuri to pick it up.\n")
		return nil
	}

	fmt.Printf("\nSet this in %s, then restart karakuri:\n", configPath)
	fmt.Printf("  encryption_key: %q\n", newHex)
	return nil
}

// backupSettingKey is the setting pkg/backup stores its schedule under.
const backupSettingKey = "backup"

// stageBackupPassphraseRekey opens the backup schedule's stored passphrase
// with oldKey, seals it with newKey, and returns the schedule setting to
// write. It returns an empty string when no passphrase is stored, and
// crypto.ErrDecrypt when the stored one does not open. Every other field of
// the schedule is carried over as stored.
func stageBackupPassphraseRekey(oldKey, newKey []byte) (string, error) {
	setting, err := models.GetSetting(backupSettingKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(setting.Value), &fields); err != nil {
		return "", fmt.Errorf("reading the backup schedule: %w", err)
	}
	var sealed string
	if raw, ok := fields["passphrase"]; ok {
		if err := json.Unmarshal(raw, &sealed); err != nil {
			return "", fmt.Errorf("reading the backup passphrase: %w", err)
		}
	}
	if sealed == "" {
		return "", nil
	}
	plain, err := crypto.Decrypt(oldKey, sealed)
	if err != nil {
		return "", fmt.Errorf("backup passphrase: %w", err)
	}
	rotated, err := crypto.Encrypt(newKey, plain)
	if err != nil {
		return "", fmt.Errorf("re-encrypting the backup passphrase: %w", err)
	}
	if fields["passphrase"], err = json.Marshal(rotated); err != nil {
		return "", err
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// pendingConfig is a rewritten config file staged beside the original, ready
// to be renamed into place.
type pendingConfig struct {
	target string
	temp   string
}

// commit atomically replaces the config file with the staged copy.
func (p *pendingConfig) commit() error { return os.Rename(p.temp, p.target) }

// cleanup removes the staged copy if it was never committed.
func (p *pendingConfig) cleanup() { _ = os.Remove(p.temp) }

// configLine matches one top-level-or-nested YAML setting by name, whatever
// quoting style its value uses. It never reads past the line end: a setting
// left empty because the environment supplies it would otherwise take the
// following line as its value.
func configLine(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^([ \t]*` + regexp.QuoteMeta(key) + `:)[^\r\n]*`)
}

// stageConfigKey writes a copy of the config file with the new encryption key
// substituted, preserving every other line and its comments. Symlinks resolve
// so the real file is replaced rather than the link.
func stageConfigKey(path, newHex string) (*pendingConfig, error) {
	pending, err := stageConfigValues(path, map[string]string{"encryption_key": newHex}, ".rotating")
	if errors.Is(err, errNoSetting) {
		return nil, fmt.Errorf("%w; rerun with --print-only and set it by hand", err)
	}
	return pending, err
}

// errNoSetting reports a config file lacking a setting to rewrite.
var errNoSetting = errors.New("setting not found")

// stageConfigValues writes a copy of the config file beside it, named with
// suffix, with each named setting's value replaced and every other line and
// comment kept. Symlinks resolve so the real file is replaced rather than the
// link, which is how the development config is installed.
func stageConfigValues(path string, values map[string]string, suffix string) (*pendingConfig, error) {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve %s: %w", path, err)
	}
	updated, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", target, err)
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		line := configLine(key)
		if !line.Match(updated) {
			return nil, fmt.Errorf("%w: no %s setting found in %s", errNoSetting, key, target)
		}
		// The value is JSON-quoted, which YAML reads as a double-quoted
		// string. ReplaceAllFunc keeps a $ in a secret literal.
		quoted, _ := json.Marshal(values[key])
		updated = line.ReplaceAllFunc(updated, func(m []byte) []byte {
			prefix := line.FindSubmatch(m)[1]
			return append(append(append([]byte{}, prefix...), ' '), quoted...)
		})
	}

	// Keep the original's permissions: the config holds secrets.
	info, err := os.Stat(target)
	if err != nil {
		return nil, fmt.Errorf("failed to stat %s: %w", target, err)
	}
	temp := target + suffix
	if err := os.WriteFile(temp, updated, info.Mode().Perm()); err != nil {
		return nil, fmt.Errorf("failed to stage %s: %w", temp, err)
	}
	if err := preserveOwner(temp, info); err != nil {
		_ = os.Remove(temp)
		return nil, err
	}
	return &pendingConfig{target: target, temp: temp}, nil
}
