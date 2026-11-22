package main

import (
	"fmt"

	"github.com/grmrgecko/karakuri/automation"
)

// CredentialProviderCmd installs and removes the karakuri credential
// provider, the COM DLL LogonUI loads on the secure desktop to submit the
// credentials the agent queues. The DLL is embedded in this binary, so the
// install writes it beside the agent and registers it; both commands write
// HKLM and need an administrator. A system agent runs the same install by
// itself at startup, which is why this command is mostly a force button.
type CredentialProviderCmd struct {
	Install CredentialProviderInstallCmd `cmd:"" help:"Register the credential provider (the DLL is embedded in this binary)."`
	Remove  CredentialProviderRemoveCmd  `cmd:"" help:"Unregister the credential provider and delete its DLL."`
}

// CredentialProviderInstallCmd registers the provider.
type CredentialProviderInstallCmd struct{}

// Run runs the same ensure the agent's startup runs.
func (c *CredentialProviderInstallCmd) Run() error {
	status, err := automation.EnsureUnlockProvider()
	if err != nil {
		return err
	}
	fmt.Println(status)
	return nil
}

// CredentialProviderRemoveCmd unregisters the provider.
type CredentialProviderRemoveCmd struct{}

// Run deletes the registration and the DLL beside the agent.
func (c *CredentialProviderRemoveCmd) Run() error {
	if err := automation.RemoveUnlockProvider(); err != nil {
		return err
	}
	fmt.Println("removed the credential provider")
	return nil
}
