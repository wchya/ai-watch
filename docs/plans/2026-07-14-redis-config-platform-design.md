# Redis Configuration Platform Design

**Date:** 2026-07-14
**Status:** Approved by user direction

## Goal

Replace SQLite as AI Watch's runtime persistence layer with a lightweight Redis service. During startup, AI Watch must initialize Redis, migrate existing durable data once, and warm all configuration required by request handling and scheduling before exposing HTTP traffic.

The UI must support complete manual Codex and Claude provider configuration and DingTalk notification configuration. Secrets are encrypted before entering Redis and are never returned unmasked.

## Runtime Topology

- `ai-watch`: Go backend and React static frontend.
- `redis:7-alpine`: private Compose-only service with no published host port.
- Redis enables AOF with `appendfsync everysec`, a bounded memory budget, and `noeviction` so configuration writes never disappear silently.
- AI Watch starts only after the Redis health check succeeds.

## Startup Sequence

1. Connect to Redis and verify read/write availability.
2. Initialize the application namespace and schema version.
3. If the Redis migration marker is absent, import the existing SQLite settings, summaries, events, provider examples, and schedules.
4. Preserve the SQLite database as a read-only backup; it is no longer used after successful migration.
5. Load settings, schedules, provider definitions, DingTalk configuration, examples, and retained summaries into bounded in-memory snapshots.
6. Start scheduler, event writer, and notification workers.
7. Start the HTTP server.

If Redis is unavailable or initialization fails, the backend must not advertise itself as ready.

## Redis Data Model

- Settings: one versioned JSON/hash value.
- Job summaries: sorted index plus per-summary values, trimmed to `historyLimit`.
- Events: time-ordered index plus values, trimmed by age, row count, and logical byte budget.
- Provider examples: hash keyed by example ID.
- Schedules: hash keyed by schedule ID; last-run fields overwrite instead of creating run history.
- Manual providers: hash keyed by provider ID plus CLI-specific indexes.
- DingTalk configuration: one encrypted configuration record.
- Migration/schema metadata: namespaced scalar keys.

All keys use an `ai-watch:` namespace. No unbounded stream, list, or per-run schedule history is allowed.

## Secret Storage

- API keys, auth JSON, provider-specific secret environment values, and DingTalk Webhook URLs use AES-256-GCM.
- A local master key is read from `AI_WATCH_MASTER_KEY` or generated once into the persistent AI Watch data volume with mode `0600`.
- Redis stores ciphertext, nonce, and version metadata only.
- List/read APIs return masked values; an empty secret in an update means keep the existing value.
- Logs and structured events must never include plaintext or ciphertext secrets.

## Manual Provider UI

- Separate Codex and Claude Code categories.
- Create, edit, enable/disable, delete, and test.
- Fields include name, Base URL, model, provider type, and the CLI-specific authentication/configuration fields.
- Automatically discovered current/CC Switch providers remain visible and read-only; manual providers are explicitly labeled.
- Resolver priority is explicit provider ID, then discovered current configuration.

## DingTalk UI

- Configure, update, disable, and test the Webhook from the settings page.
- The browser sees only configured state and a masked Webhook fingerprint.
- Notification aggregation settings remain hot-reloadable.

## Bounded Lifecycle

- Redis memory is limited by Compose.
- Application retention remains the primary event and summary bound.
- AOF rewrite settings prevent indefinite append-only file growth.
- Startup cache sizes are bounded by schedules, providers, examples, and history limits.
- Clearing events removes both indexes and payloads.

## Verification

- Fresh Redis initialization and readiness gating.
- One-time SQLite import with idempotent migration marker.
- Restart persistence through AOF.
- Secret encryption and masked API responses.
- Provider and DingTalk CRUD/test flows.
- Event age/row/byte trimming and manual clear.
- Startup warm cache correctness.
- Redis outage produces a non-ready backend rather than partial silent operation.
