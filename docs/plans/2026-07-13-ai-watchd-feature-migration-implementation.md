# AI Watchd Feature Migration Implementation Plan

**Design:** `docs/plans/2026-07-13-ai-watchd-feature-migration-design.md`

## Phase 1A: SQLite Foundation

1. Add a pure-Go SQLite driver and repository abstraction.
2. Create migrations, WAL configuration, typed settings, summaries, and event tables.
3. Migrate existing JSON settings/summaries once when the database is empty.
4. Add retention and secret-persistence tests.

## Phase 1B: Runtime Cleanup

1. Remove completed runtimes from the active map.
2. Fall back to sanitized history for completed job lookup.
3. Zero resolved secrets and option data after notification snapshotting.
4. Clear stale runtime directories at startup and wait during shutdown.
5. Add retry safety floor and cleanup tests.

## Phase 1C: Events API and UI

1. Record bounded structured lifecycle events.
2. Add list, count, filter, and clear endpoints.
3. Add an Events navigation view with retention status and confirmation-based clear.
4. Add event retention settings to the settings view.

## Phase 1D: Container Boundaries

1. Add Docker stdout rotation, PID/memory limits, init, and stop grace period.
2. Verify bubblewrap and architecture-specific Codex installation.
3. Exercise startup, health, task cleanup, restart, and volume persistence.

## Phase 2

Implement recovery state machine, scheduling, bulk actions, and merged notification policies after Phase 1 acceptance evidence is complete.
