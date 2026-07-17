# Mihomo Proxy Integration Design

**Date:** 2026-07-14  
**Status:** Approved

## Goal

Make Codex and Claude health checks usable in networks that require an outbound
proxy. AI Watch will run Mihomo as a private Compose sidecar and allow each
manual provider to inherit the default proxy, bypass it, or use a custom proxy
URL.

## Runtime Topology

- `mihomo` runs in the same Compose network as AI Watch and Redis.
- Mihomo exposes HTTP/mixed proxy port `7890`, SOCKS port `7891`, and controller
  port `9090` only to the Compose network. No proxy or controller port is
  published to the host by default.
- Mihomo configuration is stored in a dedicated named volume. The external
  base configuration remains read-only, while AI Watch can write only its
  generated `runtime.yaml` into the private shared volume.
- AI Watch uses `http://mihomo:7890` as the default proxy and excludes
  `localhost`, AI Watch, Redis, and Mihomo service names through `NO_PROXY`.
- AI Watch startup is gated by a Mihomo health check when the bundled proxy is
  enabled. A valid process is not treated as a usable external route; runtime
  diagnostics distinguish service availability from outbound connectivity.

## Provider Proxy Model

Manual providers contain a `proxyMode` field:

- `default`: inherit `AI_WATCH_DEFAULT_PROXY_URL`.
- `direct`: remove all HTTP, HTTPS, and SOCKS proxy variables for the CLI job.
- `custom`: use the provider's encrypted `proxyUrl`.

Automatic current and CC Switch providers inherit the service default. Custom
proxy URLs may contain credentials, so they are encrypted with the same
versioned AES-GCM envelope used for API keys and are never returned in
plaintext. Public API responses contain only `hasProxyUrl` and a masked proxy
description.

## CLI Environment

The runner continues to construct a narrow environment instead of inheriting
the complete service environment. It applies provider proxy policy after the
base allowlist:

1. Load safe process variables and the default proxy configuration.
2. For `direct`, delete uppercase and lowercase proxy variables.
3. For `default`, set HTTP/HTTPS proxy variables to the configured default and
   preserve `NO_PROXY`.
4. For `custom`, set the provider proxy URL. SOCKS URLs also populate
   `ALL_PROXY`.

Proxy URLs containing user information are treated as secrets by output
redaction. Logs and events record only proxy mode, never the raw URL.

## Mihomo Configuration

The repository provides a safe example configuration and a first-run bootstrap
path. The placeholder configuration defaults to `DIRECT` until the user adds a
subscription or nodes. Subscription URLs are secrets and must not be copied to
logs, API responses, or source control.

The initial implementation exposed read-only Mihomo status and connectivity in
diagnostics. The 2026-07-16 extension adds a constrained subscription editor in
System Settings: the URL is encrypted in Redis, AI Watch generates a fixed
runtime configuration, and the private Controller reloads it with rollback on
failure. Arbitrary YAML editing, custom Controller addresses, custom test URLs,
and manual node switching remain deferred.

## Validation

- Only `http`, `https`, `socks5`, and `socks5h` proxy URLs are accepted.
- URLs must be absolute, have a host, contain no fragment, and stay within the
  configured size limit.
- `custom` requires a proxy URL on create; blank updates retain the existing
  encrypted value unless `clearProxyUrl` is explicit.
- A provider using `custom` without a stored URL cannot start.

## Verification

- Runner tests cover default, direct, and custom proxy environments, including
  uppercase/lowercase variables and credential redaction.
- Secure configuration tests cover proxy validation, retain/replace/clear
  semantics, random nonces, wrong-key failures, and absence of plaintext in
  Redis.
- API and UI tests cover masked proxy state and provider editing.
- Compose validation confirms private ports, health-gated startup, volumes, and
  `NO_PROXY` entries.
- Container smoke tests confirm direct and proxied outbound checks behave
  independently.
