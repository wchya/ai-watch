# CC Switch Startup Sync Design

**Date:** 2026-07-14  
**Status:** Approved

## Goal

CC Switch SQLite is an optional startup import source, never a runtime
dependency. AI Watch synchronizes CC Switch providers into Redis before serving
HTTP traffic. Provider listing, task startup, schedules, and retries read only
Redis and in-memory snapshots.

## Startup Flow

1. Initialize Redis, verify read/write access, run legacy AI Watch migration,
   and prewarm the last successful CC Switch snapshot.
2. If the mounted CC Switch database exists, read all Codex and Claude provider
   records through a bounded read-only startup loader.
3. Normalize provider connection data and encrypt API keys, Codex config, and
   Claude environment values with the AI Watch master key.
4. Write a complete temporary Redis snapshot and atomically replace the active
   CC Switch provider hash only after every record succeeds.
5. Update the in-process provider cache and record sync time/status.
6. Start the job manager, scheduler, and HTTP server.

If the database is absent, locked, malformed, or times out, startup continues
with the last successful Redis snapshot. The failure is exposed as a sanitized
diagnostic warning and does not discard cached providers.

## Runtime Provider Resolution

- Current Codex and Claude configurations continue to be read from their
  mounted read-only configuration directories.
- IDs prefixed with `manual:` resolve from the encrypted manual-provider Redis
  store.
- Other non-empty provider IDs resolve from the encrypted CC Switch Redis
  snapshot.
- No request, task, retry, or schedule execution opens SQLite.

CC Switch provider IDs remain unchanged so existing schedules and operational
state continue to match. Imported providers remain read-only in the UI.

## Redis Model

- Active snapshot: `ai-watch:cc-switch-providers`, a hash keyed by provider ID.
- Each record stores non-sensitive metadata plus separately versioned AES-GCM
  envelopes for API key, Codex configuration, and Claude environment JSON.
- Sync metadata records source availability, last successful sync time, record
  count, and a sanitized error state.
- Replacement uses a temporary hash plus atomic rename so partial imports are
  never visible.
- An explicit sentinel field represents a valid empty snapshot and is ignored
  by readers.

## Lifecycle Semantics

- Every successful startup sync is authoritative for CC Switch-managed
  providers. Providers deleted from CC Switch disappear from the Redis snapshot.
- Manual providers are stored separately and are never overwritten.
- A failed startup sync leaves the previous snapshot untouched.
- A missing database is treated as source unavailable, not as an authoritative
  empty snapshot, so temporary mount failures do not erase providers.

## Security

- SQLite is mounted read-only and accessed only during startup.
- Plaintext credentials exist only during bounded startup normalization and
  runtime resolution, and are never serialized in API responses or logs.
- Errors expose neither database paths nor provider secrets.
- Redis contains no plaintext API key, auth JSON, Codex config, or Claude secret
  environment value.

## Verification

- Successful startup import and runtime resolution from Redis after the source
  database is removed.
- Atomic replacement, including provider deletion and valid empty snapshots.
- Failed or timed-out sync preserves the previous snapshot.
- Wrong encryption key and plaintext-absence checks.
- Task start after HTTP readiness performs no SQLite file access.
- Existing schedule IDs continue to resolve imported providers.
- Diagnostics report last sync status without exposing paths or secrets.
