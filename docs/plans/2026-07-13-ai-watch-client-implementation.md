# AI Watch Docker Client Implementation Plan

**Design:** `docs/plans/2026-07-13-ai-watch-client-design.md`

## Phase 1: Project Foundation

1. Create the Go module and application entry point.
2. Define backend packages for API, configuration discovery, jobs, runners,
   classification, redaction, notification, and summary storage.
3. Create the React/Vite/TypeScript application and shared API types.
4. Add repository defaults: `.gitignore`, editor settings, and task commands.

Verification:

- `go test ./...`
- `npm run build`

## Phase 2: Domain Model and Classification

1. Define job modes, CLI types, configuration sources, job states, attempt states,
   and sanitized DTOs.
2. Port Codex and Claude fatal/overload matching rules from `ai-watch.sh`.
3. Implement exact success semantics: exit code zero plus expected text match.
4. Implement API key masking and output redaction.
5. Add table-driven tests covering Chinese and English response patterns.

Verification:

- Unit tests prove every normalized state.
- Tests prove credentials are absent from sanitized output.

## Phase 3: Configuration Discovery

1. Scan mounted Codex configuration and auth files.
2. Scan mounted Claude settings and environment overrides.
3. Query the mounted CC Switch database in read-only mode.
4. Return masked provider cards and dependency health.
5. Copy selected configuration into per-job tmpfs directories without modifying
   the mounted source.

Verification:

- Fixture-based configuration tests.
- Source mount timestamps and content remain unchanged.
- API responses contain no clear-text API key.

## Phase 4: Runner and Job Manager

1. Build Codex and Claude commands with the approved safety flags.
2. Launch each attempt in a dedicated process group.
3. Implement attempt deadlines and process-group termination.
4. Implement probe retry and keepalive scheduling.
5. Implement the CLI/base URL/API key target lock.
6. Implement start, stop, shutdown cleanup, and stale job recovery.
7. Keep output in bounded memory only and clear it after classification/job end.

Verification:

- Fake CLI integration tests cover success, retry, fatal, timeout, stop, and lock
  conflict behavior.
- Cleanup tests prove temporary directories and log buffers are removed.

## Phase 5: API and Live Events

1. Implement health, configuration, provider, job, settings, and notification APIs.
2. Implement SSE subscriptions with event IDs and bounded reconnect support.
3. Ensure completed jobs do not expose a historical raw-log endpoint.
4. Persist summary metadata only.
5. Add request validation, structured errors, and graceful shutdown.

Verification:

- HTTP handler tests cover success and validation failures.
- Persistence tests inspect stored records for forbidden fields.
- SSE tests cover connection, replay while running, and cleanup after completion.

## Phase 6: React User Interface

1. Build the responsive dark operations shell and navigation.
2. Build the dashboard and dependency/configuration status cards.
3. Build the guided new-job drawer.
4. Build CC Switch provider cards.
5. Build the live job detail page with state, countdown, and SSE output.
6. Build settings and notification controls.
7. Add empty, loading, error, disconnected, and cleanup states.
8. Add browser notifications and accessible keyboard/focus behavior.

Verification:

- TypeScript build succeeds.
- Component tests cover core flows.
- Manual checks at desktop, tablet, and phone widths.

## Phase 7: Docker and One-Command Operation

1. Add a multi-stage Dockerfile.
2. Install Linux Codex and Claude CLIs and required runtime tools.
3. Add `compose.yaml` with local-only port binding, read-only configuration mounts,
   named data volume, and `/run/ai-watch` tmpfs.
4. Add health checks, an init process, and environment examples.
5. Add startup and troubleshooting documentation.

Verification:

- `docker compose config`
- `docker compose build`
- `docker compose up -d`
- Health endpoint succeeds at `127.0.0.1:8787`.

## Phase 8: Completion Audit

1. Map every `ai-watch.sh` operation to a UI/API implementation.
2. Run backend, frontend, integration, and container checks.
3. Inspect `/data` and tmpfs lifecycle for raw logs and credentials.
4. Verify stop/timeout kills only the intended process group.
5. Verify local-only network exposure and read-only host mounts.
6. Record any platform prerequisites and exact one-command startup instructions.

The project is complete only after all acceptance criteria in the approved design
have current, direct evidence.
