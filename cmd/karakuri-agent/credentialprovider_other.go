//go:build !windows

package main

import "fmt"

// CredentialProviderCmd exists on every platform so the command tree is one
// shape, and answers only that the credential provider is a Windows
// component. Windows is the one platform whose logon screen takes a
// credential from a COM DLL it loads.
type CredentialProviderCmd struct {
	Install CredentialProviderInstallCmd `cmd:"" help:"Register the credential provider on this machine."`
	Remove  CredentialProviderRemoveCmd  `cmd:"" help:"Unregister the credential provider."`
}

// CredentialProviderInstallCmd is the Windows-only half.
type CredentialProviderInstallCmd struct {
	Dll string `help:"Path to karakuri-unlock.dll; defaults to one beside this binary."`
}

// Run reports the platform that cannot run it.
func (c *CredentialProviderInstallCmd) Run() error {
	return fmt.Errorf("the credential provider is a Windows component")
}

// CredentialProviderRemoveCmd is the Windows-only half.
type CredentialProviderRemoveCmd struct{}

// Run reports the platform that cannot run it.
func (c *CredentialProviderRemoveCmd) Run() error {
	return fmt.Errorf("the credential provider is a Windows component")
}
