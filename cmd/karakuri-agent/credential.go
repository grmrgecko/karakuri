package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/agent/credkey"
	"github.com/grmrgecko/karakuri/agent/credstore"
	"github.com/grmrgecko/karakuri/agent/identity"
	"github.com/grmrgecko/karakuri/internal/config"
)

// CredentialCmd manages the account passwords this agent can unlock with. A
// stored credential is the account password sealed under a key the server
// holds, so enrolling one and using one both reach the server for the key; the
// password itself is encrypted on this host and never sent anywhere. The whole
// group needs the agent to be enrolled, since the key belongs to the agent's
// identity.
type CredentialCmd struct {
	Set  CredentialSetCmd  `cmd:"" help:"Store an account password for credentialed unlock or login."`
	List CredentialListCmd `cmd:"" help:"List the stored credentials, without their secrets."`
	Rm   CredentialRmCmd   `cmd:"" help:"Remove a stored credential."`
}

// CredentialSetCmd seals an account password into the agent's store.
type CredentialSetCmd struct {
	User     string `arg:"" help:"Account whose password this is, e.g. the user whose screen it unlocks."`
	Name     string `help:"Reference name for the credential, used by the Session node; defaults to the user."`
	Password string `help:"The password. Omit to be prompted, which does not echo." `
}

// Run fetches the store key, seals the password, and writes the store.
func (c *CredentialSetCmd) Run(cli *CLI) error {
	cfg := config.AC
	server, err := resolveServer(cfg.Dir, cfg.Server)
	if err != nil {
		return err
	}

	name := c.Name
	if name == "" {
		name = c.User
	}
	password := c.Password
	if password == "" {
		password, err = promptPassword(fmt.Sprintf("Password for %s: ", c.User))
		if err != nil {
			return err
		}
	}
	if password == "" {
		return fmt.Errorf("no password given")
	}

	// The key lives on the server; hold it only long enough to seal, then zero
	// it so the plaintext key does not linger in this short-lived process.
	key, err := credkey.Fetch(context.Background(), server, cfg.Dir)
	if err != nil {
		return err
	}
	defer zero(key)

	if err := credstore.Open(credstore.DefaultPath(cfg.Dir)).Set(key, name, c.User, password); err != nil {
		return err
	}
	fmt.Printf("stored credential %q for account %s\n", name, c.User)
	return nil
}

// CredentialListCmd lists the stored credentials without their secrets, so it
// needs neither the server nor the key.
type CredentialListCmd struct{}

// Run prints the stored credential names and their accounts.
func (c *CredentialListCmd) Run(cli *CLI) error {
	metas, err := credstore.Open(credstore.DefaultPath(config.AC.Dir)).List()
	if err != nil {
		return err
	}
	if len(metas) == 0 {
		fmt.Println("no credentials stored")
		return nil
	}
	for _, m := range metas {
		fmt.Printf("%-24s %s\n", m.Name, m.User)
	}
	return nil
}

// CredentialRmCmd removes a stored credential.
type CredentialRmCmd struct {
	Name string `arg:"" help:"Reference name of the credential to remove."`
}

// Run deletes the named credential.
func (c *CredentialRmCmd) Run(cli *CLI) error {
	if err := credstore.Open(credstore.DefaultPath(config.AC.Dir)).Remove(c.Name); err != nil {
		return err
	}
	fmt.Printf("removed credential %q\n", c.Name)
	return nil
}

// resolveServer resolves the server address the same way run does: the address
// enrollment stored, overridden by a configured one.
func resolveServer(dir, configured string) (string, error) {
	id, _, _, err := identity.Load(dir)
	if err != nil {
		return "", fmt.Errorf("this agent is not enrolled: %w", err)
	}
	server := id.Server
	if configured != "" {
		server = configured
	}
	if server == "" {
		return "", fmt.Errorf("no server address; pass --server")
	}
	return server, nil
}

// promptPassword reads a password from the terminal without echoing it, falling
// back to a plain line read when input is not a terminal (a piped set).
func promptPassword(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, prompt)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("reading the password: %w", err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return readPasswordLine(os.Stdin)
}

// readPasswordLine reads one piped password line and strips its line ending.
// Everything else on the line is the password, spaces included.
func readPasswordLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", fmt.Errorf("reading the password: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// zero wipes a secret from memory.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// credentialResolver builds the function the Session node uses to turn a stored
// credential reference into an account and password. Each call fetches the
// store key from the server, opens the entry, and lets the key go — the
// per-use fetch that keeps the key out of the agent's memory between unlocks.
func credentialResolver(server, dir string) agentnode.CredentialResolver {
	return func(ctx context.Context, ref string) (string, string, error) {
		key, err := credkey.Fetch(ctx, server, dir)
		if err != nil {
			return "", "", err
		}
		defer zero(key)
		return credstore.Open(credstore.DefaultPath(dir)).Get(key, ref)
	}
}
