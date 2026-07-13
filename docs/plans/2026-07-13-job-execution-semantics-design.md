# Job Execution Semantics Design

## Goal

Expose four user-facing operations without replacing the established job modes:

- one-shot probe
- continuous probe until success or fatal failure
- immediate one-shot keepalive check
- continuous keepalive monitoring

## Model

Keep `mode` limited to `probe` and `keepalive`. Add `runOnce` to job input and
job output. Its default is `false`, so existing callers retain their current
continuous behavior.

| mode | runOnce | behavior |
| --- | --- | --- |
| probe | true | execute exactly one attempt, then finish |
| probe | false | retry until success, fatal failure, or stop |
| keepalive | true | execute exactly one attempt, then finish |
| keepalive | false | continue keepalive and recovery-probe state transitions |

For a one-shot attempt, success maps to job `success`, fatal classification to
`fatal`, and every other non-stopped result to `failed`. One-shot keepalive does
not enter the recovery state machine.

## API compatibility

`POST /api/jobs` accepts optional `runOnce`; omission remains compatible.
Bulk jobs retain `probe`, `keepalive`, and `stop`, and add `probe_once` and
`keepalive_once` actions. Job responses include `runOnce` for unambiguous UI
labels.

Scheduled probes map `untilSuccess=false` to `runOnce=true` and
`untilSuccess=true` to continuous probe behavior. Scheduled keepalive remains
continuous. This uses the existing schedule schema and avoids persisting a new
field.

## Tests

Cover one-shot terminal mapping and attempt count, unchanged continuous probe
and keepalive behavior, bulk action mapping, and schedule option mapping.
