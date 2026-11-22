# Research: Configured notification services

## Upstream inventory

Decision: Pin nikoksr/notify v1.6.0, the latest tagged release inspected on
2026-10-06. Its 33 service directories contain 36 variants: separate Lark
app/webhook, LINE bot/Notify, and Pushbullet push/SMS variants.

Rationale: Main requires Go 1.26.8; the tag accepts the project's Go 1.26.5.
The settings catalog covers the tag rather than following an unpinned branch.
Source and constructor research covers each variant.

## Storage and authorization

Decision: Reuse encrypted Connection rows and connection permissions. Add nested
mask/restore hooks and a notification settings page rather than parallel tables.

Rationale: Credentials already have tested encryption and unchanged-value edits.
Endpoint target restoration must use stable IDs so reordering cannot exchange
recipients. Node config carries IDs only; allowlist checks precede construction.

## Adapter correctness

Decision: Use notify instances per send, never its global registry. Fix integration
boundaries with owned Notifier wrappers when upstream adapters lose configuration,
ignore rejected messages, or start hidden heartbeat goroutines.

Rationale: FCM applies client options after constructing its client and ignores
batch failures. Lark's v1 adapter hides its heartbeat. Several SDKs have partial
cancellation support; capacity stays reserved until those operations finish.

WhatsApp is an upstream no-op stub and cannot report success. LINE Notify is a
retired provider, so both entries explain unavailability. All other variants
have adapters. Syslog requires platform-specific implementation.

Sources: [notify v1.6.0](https://github.com/nikoksr/notify/tree/v1.6.0),
[official LINE Notify retirement](https://notify-bot.line.me/), and dependency
source for each constructor and Send implementation.
