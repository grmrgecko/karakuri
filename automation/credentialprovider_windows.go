//go:build windows

package automation

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/grmrgecko/karakuri/automation/internal/windows"
)

// The credential provider is part of the agent, not a download: the DLL the
// logon screen loads rides inside this binary and is written out beside it
// when a SYSTEM agent starts, so enroll-then-install-service is the whole
// setup. The committed copy under unlockprovider/ is the build input this
// embeds; `make unlockprovider` rebuilds it from the C++ source.
//
//go:embed unlockprovider/karakuri-unlock.dll
var unlockProviderDLL []byte

// UnlockProviderCLSID is the COM class of the embedded provider; the C++
// source declares the same value, and the registration keys name it.
const UnlockProviderCLSID = `{7E1A0C42-8D3B-4F5A-9C3D-6E10A3F4E5B6}`

// unlockProviderFile names the written DLL after its own content hash: a
// loaded DLL cannot be replaced (LogonUI holds it whenever a lock or logon
// screen is showing), and a content-addressed name lets every build land in
// its own file — an update is a new file plus a registration change, never a
// fight with a loaded copy. Older copies are pruned best-effort.
func unlockProviderFile() string {
	sum := sha256.Sum256(unlockProviderDLL)
	return fmt.Sprintf("karakuri-unlock-%s.dll", hex.EncodeToString(sum[:])[:16])
}

const (
	unlockProviderDisplayName = "Karakuri Unlock"
	unlockProviderKey         = `SOFTWARE\Microsoft\Windows\CurrentVersion\Authentication\Credential Providers\` + UnlockProviderCLSID
	unlockProviderClassKey    = `SOFTWARE\Classes\CLSID\` + UnlockProviderCLSID
)

// EnsureUnlockProvider installs or updates the karakuri credential provider
// for a system agent: the embedded DLL is written beside this binary under a
// content-addressed name, and the registration that points the logon screen
// at it is re-asserted. A caller that is not SYSTEM is a no-op — installing
// a credential provider is an administrator's action, and the feature is
// meant for the system service. The answer names what changed, and is empty
// for a no-op.
func EnsureUnlockProvider() (string, error) {
	if !windows.RunningAsSystem() {
		return "", nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("finding this binary to place the provider DLL beside: %w", err)
	}
	dir := filepath.Dir(exe)
	dllPath := filepath.Join(dir, unlockProviderFile())

	// The DLL is content-addressed, so an update is a new file; only the
	// very first install creates anything.
	status := ""
	if _, err := os.Stat(dllPath); err != nil {
		if writeErr := os.WriteFile(dllPath, unlockProviderDLL, 0o644); writeErr != nil {
			return "", fmt.Errorf("writing the provider DLL to %s: %w", dllPath, writeErr)
		}
		status = "installed the credential provider"
	}
	pruneOldProviders(dir, dllPath)

	// The registration: COM's half names the DLL, the logon screen's half
	// lists the provider. Re-asserted every start, which is what makes a
	// tampered or partial registration heal itself.
	clsid, _, err := registry.CreateKey(registry.LOCAL_MACHINE, unlockProviderClassKey, registry.SET_VALUE)
	if err != nil {
		return "", fmt.Errorf("writing the provider's COM registration: %w", err)
	}
	defer clsid.Close()
	if err := clsid.SetStringValue("", unlockProviderDisplayName); err != nil {
		return "", err
	}
	server, _, err := registry.CreateKey(clsid, "InprocServer32", registry.SET_VALUE)
	if err != nil {
		return "", fmt.Errorf("writing the provider's server path: %w", err)
	}
	defer server.Close()
	if err := server.SetStringValue("", dllPath); err != nil {
		return "", err
	}
	if err := server.SetStringValue("ThreadingModel", "Apartment"); err != nil {
		return "", err
	}
	providers, _, err := registry.CreateKey(registry.LOCAL_MACHINE, unlockProviderKey, registry.SET_VALUE)
	if err != nil {
		return "", fmt.Errorf("registering the provider with the logon screen: %w", err)
	}
	defer providers.Close()
	if err := providers.SetStringValue("", unlockProviderDisplayName); err != nil {
		return "", err
	}
	if status == "" {
		status = "verified the credential provider"
	}
	return status, nil
}

// pruneOldProviders deletes content-addressed provider DLLs other than the
// current one. A copy a logon screen still holds fails the delete and stays
// until the next start, which is fine: the registration no longer names it.
func pruneOldProviders(dir, current string) {
	matches, err := filepath.Glob(filepath.Join(dir, "karakuri-unlock-*.dll"))
	if err != nil {
		return
	}
	for _, m := range matches {
		if strings.EqualFold(m, current) {
			continue
		}
		_ = os.Remove(m)
	}
}

// RemoveUnlockProvider unregisters the provider and deletes the DLL beside
// this binary. A DLL a logon screen still holds cannot be deleted; the
// registration is what makes it inert, so that part is the error that
// matters.
func RemoveUnlockProvider() error {
	if err := registry.DeleteKey(registry.LOCAL_MACHINE, unlockProviderKey); err != nil {
		return fmt.Errorf("unregistering the provider from the logon screen: %w", err)
	}
	if err := registry.DeleteKey(registry.LOCAL_MACHINE, unlockProviderClassKey+`\InprocServer32`); err != nil {
		return err
	}
	if err := registry.DeleteKey(registry.LOCAL_MACHINE, unlockProviderClassKey); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err == nil {
		matches, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), "karakuri-unlock-*.dll"))
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}
	return nil
}
