package agenthub

import (
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/grmrgecko/karakuri/pkg/crypto"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// CARekey is the agent CA's stored state re-sealed under a new encryption key,
// staged so rotate-key can verify every ciphertext opens before it writes
// anything. Leaving the CA behind on a rotation makes the next start refuse to
// build the agent hub, and every enrolled identity is unreachable with it.
type CARekey struct {
	roots  string
	legacy string
	count  int
}

// StageCARekey opens every stored CA private key with oldKey and seals it
// with newKey. A key that does not open reports crypto.ErrDecrypt. It returns
// nil when no CA state is stored.
func StageCARekey(oldKey, newKey []byte) (*CARekey, error) {
	var out CARekey
	rootsSetting, err := models.GetSetting(settingCARoots)
	switch {
	case err == nil:
		var stored []storedRoot
		if err := json.Unmarshal([]byte(rootsSetting.Value), &stored); err != nil {
			return nil, fmt.Errorf("reading stored CA roots: %w", err)
		}
		for i := range stored {
			keyPEM, err := crypto.Decrypt(oldKey, stored[i].Key)
			if err != nil {
				return nil, fmt.Errorf("agent CA root %d: %w", i+1, err)
			}
			if stored[i].Key, err = crypto.Encrypt(newKey, keyPEM); err != nil {
				return nil, fmt.Errorf("re-encrypting agent CA root %d: %w", i+1, err)
			}
			out.count++
		}
		raw, err := json.Marshal(stored)
		if err != nil {
			return nil, err
		}
		out.roots = string(raw)
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return nil, err
	}

	legacy, err := models.GetSetting(settingCAKeyLegacy)
	switch {
	case err == nil:
		keyPEM, err := crypto.Decrypt(oldKey, legacy.Value)
		if err != nil {
			return nil, fmt.Errorf("agent CA key: %w", err)
		}
		if out.legacy, err = crypto.Encrypt(newKey, keyPEM); err != nil {
			return nil, fmt.Errorf("re-encrypting the agent CA key: %w", err)
		}
		out.count++
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return nil, err
	}

	if out.count == 0 {
		return nil, nil
	}
	return &out, nil
}

// Count reports how many CA private keys are staged.
func (r *CARekey) Count() int { return r.count }

// Apply writes the re-sealed state inside tx.
func (r *CARekey) Apply(tx *gorm.DB) error {
	if r.roots != "" {
		if err := models.SetSettingIn(tx, settingCARoots, r.roots); err != nil {
			return fmt.Errorf("updating the agent CA roots: %w", err)
		}
	}
	if r.legacy != "" {
		if err := models.SetSettingIn(tx, settingCAKeyLegacy, r.legacy); err != nil {
			return fmt.Errorf("updating the agent CA key: %w", err)
		}
	}
	return nil
}
