# Credentialed session login & unlock — design

Status: implemented on Linux, Windows, and macOS.

## Goal

Make **login** and **unlock** mean the same thing on every platform: prove the target
user's password (or an equivalent credential), then act. No action here bypasses
authentication — everything *supplies* the credential to the OS the way a person typing
it would. If the password is wrong, it fails like a wrong password.

This replaces today's Linux `unlock`, which calls `loginctl unlock-session` as root — an
administrative override that dismisses the lock **without** the password. That is exactly
the behavior we no longer want.

## The two verbs

Both are credentialed. Both decompose into a **verifier** (the OS authenticates the
secret) and an **actuator** (a privileged action that unlocks or switches).

- **unlock(user, secret)** — verify, then dismiss the lock on *user*'s session.
- **login(user, secret)** — verify, then bring *user*'s session to the foreground.
  - If *user* already has a session (someone else is logged in, or they are in the
    background), this is a **fast user switch** to it, followed by an unlock — because a
    switch lands on that session's lock screen. This is the common case, and the reason
    login and unlock share almost all their machinery.
  - If *user* has **no** session, Linux reports that it can only switch to an existing
    graphical session. The separate `greeter` action can raise the display manager, which
    remains responsible for creating the session and opening its PAM session.

The password-free actions stay password-free because they authenticate nobody: `lock`,
`logout`, `activate` (raw switch, lands on the lock), `greeter`.

## Where the secret comes from — envelope, key on the server (decided)

The credential never lives as plaintext in the flow graph (graphs export and import — the
same reason connection secrets use the sentinel), and it is never usable by the agent
alone. **Split of custody:**

- **Ciphertext on the agent.** The agent keeps an encrypted credential store on its own
  disk (under `--dir`, beside the identity), each entry AEAD-encrypted (AES-256-GCM) under
  a store master key `K`. `K` is **not** on the agent.
- **Key on the server.** The server generates `K` per agent and holds it, encrypted under
  the existing `security.encryption_key` the way every other server secret is. The agent
  fetches `K` over the authenticated mTLS agent channel when it needs to read or write its
  store, and holds it only in memory, zeroed after use.

So the flow at unlock time:

```
AGENT disk:  entry = AES-256-GCM(K, alice-password)   [ciphertext, useless alone]
SERVER:      K                                         [under security.encryption_key]

unlock:
  agent -> server  : give me my store key            (over mTLS, agent identity)
  server -> agent  : K
  agent            : decrypt entry -> alice-password, verify (unixauth), unlock
  agent            : zero K and the password
```

Two properties this buys:

- **Agent-disk theft yields nothing** — the ciphertext does not decrypt without the
  server, and the server hands `K` only to the enrolled agent over its authenticated
  channel.
- **The server never sees the account passwords.** Only `K` crosses the wire; encryption
  and decryption happen agent-side. The server holds the key, never the credential.

`credential set` encrypts the same way: the CLI fetches `K` over the identity channel,
AEAD-encrypts the account password, writes the ciphertext entry. The account password
never leaves the host.

**No runtime-value source for now** — every credentialed unlock resolves its secret from
this store. (A flow-supplied secret can be added later if wanted.)

**This is still the highest-value secret in play** — a reversible credential to an
interactive account. Opt-in, install-gated on Windows/macOS, zeroed after use, never
logged, never in the graph, and now never resident-and-usable on the agent alone.

## Linux — no plug-in needed (the good case)

Root plus PAM plus logind is the whole answer; nothing has to be installed into a login
stack.

- **Verify**: authenticate *user* + *secret* through the host's PAM `login` service.
  `unixauth` loads libpam dynamically through purego, so the agent stays a cgo-free Go
  binary while the configured `pam_systemd_home`, `pam_mount`, SSSD, Kerberos, and
  `pam_faillock` modules receive the original credential. This is required for a password
  that also unlocks encrypted home storage; comparing `/etc/shadow` proves the secret but
  cannot supply it to those modules. The pure-Go password-hash verifier remains available
  as a low-level local-file facility, but it is not the Linux session-authentication path.
- **Actuate**:
  - unlock: `loginctl unlock-session <id>` (root override) — but now gated behind a real
    password check, so the override is only reached after the OS-equivalent verification we
    performed. Well-behaved lockers (KDE kscreenlocker, GNOME Shell) drop the lock on the
    logind `Unlock` signal.
  - login/switch: verify *user*, `ActivateSession` (VT switch) to their session, then unlock
    it as above. If none exists, return unsupported because the display manager must create
    the graphical session and own its PAM session lifetime.
- **Privilege**: needs root, which the session actions already require for another user.

## Windows — a Credential Provider

The lock screen is the secure desktop; no process outside it can inject input or command a
dismiss (`SendInput` is blocked by UIPI even as SYSTEM). The sanctioned path is a plug-in
that lives on that desktop.

- Ship a signed **Credential Provider DLL** (`CPUS_LOGON` / `CPUS_UNLOCK_WORKSTATION`),
  registered under `HKLM`. It runs inside LogonUI.
- The agent service cannot reach the secure desktop, so it **signals** the provider over a
  secured local channel ("unlock session N as user U"); the provider takes the secret from
  the local keystore, serializes the credential to LSA, and LogonUI unlocks. Fast user
  switch to another user is `WTSConnectSession` with that user's credential — which fits
  the credentialed model exactly, since Windows already demands the target's password to
  connect their session.
- Cost: code signing, an install step that registers the provider, and the provider being
  present. A separate C++/COM build artifact.
- No-plug-in fallback (best-effort, caveated): RDP-loopback authenticated with the password,
  then `tscon <id> /dest:console` to move the session to the console. Reconnects/relocates
  the session, needs admin, drops the RDP client. Offer as a degraded mode, not the default.

## macOS: typed through the session helper

The root agent verifies the password with `ODRecordVerifyPassword`, wakes the display, and asks a
LaunchAgent in the target GUI session to type the password into loginwindow. Login uses
`SACSwitchToLoginWindow`, selects loginwindow's name-and-password form, and types the account name
and password. This resumes an existing session or creates one. Unlock types only the password into
the target session's lock screen. Both paths poll the session table until loginwindow reports the
requested result.

There is no third-party API that can hand a pre-verified password or certificate to loginwindow on
Sonoma:

- `SACSwitchToUser` needs `com.apple.private.logind.sessionswitch`. logind checks the caller's audit
  token, and AMFI accepts that restricted entitlement only from Apple-signed code.
- `LAAuthenticationProvider` connects to `com.apple.LocalAuthentication.apd`; its password, Touch
  ID, CryptoTokenKit, keybag, and Authentication Hints endpoints all reject callers without the
  Apple-private `com.apple.ahp` entitlement. Running as root and signing against a local CA do not
  satisfy that check. loginwindow has the entitlement.
- Screen Sharing verifies the remote viewer, then attaches to the locked console's LoginWindow
  agent. It does not pass that credential into the console authentication flow; unlocking still
  comes from the viewer's remote input.
- Apple Watch Auto Unlock is a loginwindow-supported alternative when the operator has already
  enabled and paired it. It cannot consume Karakuri's known password or a minted certificate, so it
  is not the credentialed session backend.

The typed path therefore remains the macOS implementation. It needs Accessibility permission for
the agent binary, but no authorization plug-in, authdb edit, private entitlement, or SIP change.

## Node / API shape

- The `session` node grows credentialed `unlock` and `login` actions that take a **credential
  reference** (a keystore entry name), never a raw password. `lock` / `logout` / `activate` /
  `greeter` stay as they are — they authenticate nobody.
- New agent subcommands: `credential set|list|rm` (manage per-agent secrets in the encrypted
  store). The Windows credential provider installs from the embedded DLL. Linux and macOS need no
  login plug-in.
- The `Sessions` capability gains `Authenticate(user, secretRef) error` and a credentialed
  `Login/Unlock(user, secretRef, action)`. Linux runs in-agent, Windows brokers to its credential
  provider, and macOS delegates only the typing to its per-session helper. `Sessions` stays in the
  privileged parent, which is where password verification and session selection belong.

## Security model / non-goals

- The OS does the verification through PAM or the local password database on Linux, LSA on Windows,
  and Directory Services followed by loginwindow on macOS. Karakuri never compares a supplied
  password to its own stored value.
- Credentialed unlock is opt-in. The operator stores a credential, Windows installs its provider,
  and macOS grants Accessibility permission to the helper binary. A default agent has none of this
  access.
- Not a bypass. Wrong password fails like a wrong password. No session-hijack default (the
  Windows no-password `tscon` trick is explicitly excluded).
- The secret is zeroed after use, never logged or written to the flow graph, and remains encrypted
  in the agent credential store when not in use.

## Build / packaging impact

Linux adds no executable artifact and keeps `CGO_ENABLED=0`; purego loads the system's
libpam at runtime. The Windows credential provider is the only native component:

- Windows credential provider. C++/COM, embedded in the agent and installed by a SYSTEM agent.

macOS adds no native component. Its helper is the agent binary relaunched in the GUI session.

## Suggested phasing

1. **Linux, credentialed, no plug-ins** — PAM authentication through purego, gated logind
   unlock, verify-then-`ActivateSession` for login/switch, and credential-store integration.
2. **Windows credential provider.** Ship the embedded provider, with typed input as its fallback.
3. **macOS typed unlock and login.** Use Directory Services verification plus the per-session helper.

## Decisions made

- **Credential storage**: envelope — ciphertext on the agent, master key `K` on the server,
  fetched over the mTLS channel per use. Server-backed store only (no runtime-value source
  yet).
- **Scope**: all three platforms, Linux first end-to-end, then the Windows credential provider and
  macOS typed path. Windows carries the only native component; macOS reuses the agent binary in the
  target GUI session.
- **Linux verify**: the host PAM `login` stack, called from the cgo-free agent through
  purego. This preserves encrypted-home and site-specific authentication behavior.

## Build progress

- [x] `automation/internal/unixauth` — Linux PAM authentication through purego, plus the
  pure-Go local password verifier via `go-passwd` for shadow and master.passwd hashes.
- [x] `agent/credstore` — AES-256-GCM encrypted store, the ciphertext half of the envelope,
  tested for round-trip / wrong-key / tamper / slot-binding. (committed)
- [x] Server-side `K` + wire — `ModeCredentialKey` on the agent wire, `models.AgentCredentialKey`
  seals `K` per agent under `security.encryption_key`, `agent/credkey` fetches it over mTLS.
  (committed)
- [x] `credential set|list|rm` CLI. (committed)
- [x] `Sessions.Login` credentialed unlock/login + Session node wiring (Linux via PAM and logind,
  Windows via the credential provider with typed fallback, macOS via Directory Services and the
  session helper; no-session-manager hosts say so). Replaced the password-free Linux unlock.
  **Verified live end to end on the
  localhost root agent: right password unlocks, wrong password fails and stays locked.**
  (committed)
- [x] Windows typed unlock (`automation/internal/windows/secureinput_windows.go`) — the same
  shape macOS uses, no credential provider: a SYSTEM agent starts the agent binary once inside
  the locked session (`session-input`), which joins WinSta0, dismisses the lock screen's clock,
  waits for the input desktop to become Winlogon, and types the password; LogonUser proves it in
  the parent first. Sign-in for an account with no session rides the same screen through the
  `dontdisplaylastusername` flip, restored after. **Verified live on Windows 11 Pro 26200:
  lock, unlock (wrong password refused before typing, right password clears the lock, repeated),
  greeter, sign-in from scratch for a second account, switch-and-unlock of a parked session,
  activate, logout.** Supersedes the credential-provider plan below for unlock; the provider
  would still be the answer for a *user-scope* agent, which cannot start the SYSTEM helper.
- [x] Windows credential provider (`automation/unlockprovider/karakuri-unlock.cpp` + the
  `credential-provider install|remove` CLI) — a COM DLL LogonUI loads on WinSta0, **embedded in
  the agent binary** and installed or updated beside itself at startup by a SYSTEM agent
  (enroll-then-install-service is the whole setup), hosting a per-session pipe the agent queues
  the enrolled credential over; the provider arms an auto-logon tile
  (`KERB_INTERACTIVE_UNLOCK_LOGON` / `KERB_INTERACTIVE_LOGON` per usage scenario). **Verified
  live on Windows 11 Pro 26200 up to the tile enumeration (DllGetClassObject → Advise →
  GetCredentialCount all instrumented and confirmed); the OS currently re-enumerates an armed
  tile without submitting it — on lock and logon surfaces alike, at initialization and on
  CredentialsChanged — so the provider arms first and the typed path above carries the unlock,
  with a 10-minute backoff after a declined submit.** The submission mechanics are the piece to
  revisit when Windows honors `pbAutoLogonWithDefault` on refresh.
- [x] macOS typed unlock and login (`automation/internal/macos/sessions_darwin.go`). Directory
  Services verifies the password, a LaunchAgent in the relevant GUI session supplies it to
  loginwindow, and the backend waits for the lock or console-session state to change. Private
  LocalAuthentication, smart-card, session-switch, and Screen Sharing alternatives were traced and
  rejected for the reasons above.
