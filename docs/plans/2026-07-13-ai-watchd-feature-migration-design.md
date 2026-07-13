# AI Watchd Feature Migration Design

**Date:** 2026-07-13
**Status:** Approved

## Goal

Port the useful long-running provider monitoring capabilities from `/Users/admin/tool/ai-watchd` into AI Watch without copying its unbounded `events.log`, persistent runtime credentials, zero-delay failure loops, or mutable in-container CLI upgrade behavior.

AI Watch remains a single local Go service plus React frontend. SQLite is embedded in the service; no Redis, MySQL, or additional database container is introduced.

## Storage Model

SQLite under `/data/ai-watch.db` becomes the durable source of truth.

- `schema_migrations`: applied schema versions.
- `settings`: hot-reloadable typed application settings.
- `provider_profiles`: optional custom/provider example metadata. Secrets must be encrypted at rest or reference environment variables; API responses are always masked.
- `job_summaries`: sanitized completed-job metadata only.
- `events`: structured operational events. Raw CLI output, full prompts, API keys, auth JSON, and webhook URLs are forbidden.

SQLite uses WAL mode, foreign keys, busy timeout, transactions, and indexes for time/provider/type queries.

## Bounded Data Lifecycle

Events are bounded by all three policies:

1. Maximum age.
2. Maximum row count.
3. Maximum configured database/log budget.

Cleanup runs after inserts on a throttled cadence and at startup. Users can filter events and clear all events through an explicit confirmation flow. Clearing events does not delete settings, provider profiles, or job summaries.

Docker stdout uses log rotation. CLI raw output remains in bounded memory only and is never copied into the structured event table or container logs.

## Runtime and Secret Lifecycle

- Active jobs alone remain in the in-memory runtime map.
- On completion, a sanitized summary is copied, notification data is captured, and the runtime's API key, auth JSON, Claude environment, Codex config, prompt, event buffer, and subscribers are cleared.
- Completed job lookup falls back to sanitized history.
- `/run/ai-watch/jobs` is cleared on service startup.
- Every prepare, start, success, failure, cancellation, and shutdown path removes the task directory.
- Shutdown waits for workers and process-group cleanup within the container grace period.
- Probe retry has a non-zero safety floor and bounded backoff to prevent hot loops.

## Migrated Capabilities

### Phase 1

- Embedded SQLite and automatic migrations.
- Hot-reloadable settings.
- Provider examples/custom metadata without replacing automatic Codex, Claude, and CC Switch discovery.
- Bounded operational events with filter and clear UI.
- Runtime secret/memory cleanup and stale directory cleanup.
- Docker log rotation and resource limits.

### Phase 2

- Keepalive failure threshold that transitions a provider into recovery probing.
- Return to healthy keepalive after recovery.
- Provider schedules and timezone-aware probe jobs.
- Bulk start, stop, probe, and keepalive actions.
- Merged recovery, probe-progress, and keepalive-summary notifications.

## Explicit Non-Goals

- No unbounded append-only log file.
- No persisted raw CLI output.
- No persistent runtime credential directories.
- No in-container CLI self-upgrade.
- No zero-delay failure retry loop.
- No Mihomo sidecar until proxy management is separately requested.

## User Interface

- Add an Events view with filters for provider, type, level, and time.
- Show retention policy and current event count.
- Provide a danger-styled “Clear events” action with explicit confirmation.
- Add hot-reload settings for event age/count and safe retry behavior.
- Keep Codex and Claude Code Provider categories separate.

## Verification

- Migration tests cover fresh and existing databases.
- Persistence tests prove forbidden raw fields and secrets never enter SQLite.
- Cleanup tests cover row count, age, explicit clear, startup cleanup, and database reopen.
- Runtime tests prove completed jobs no longer retain resolved secrets or temporary directories.
- Failure-loop tests prove retries cannot spin at zero delay.
- Container checks verify stdout rotation, resource limits, health, CLI availability, and an empty runtime directory after task completion.
