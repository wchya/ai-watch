# AI Watch Docker Client Design

**Date:** 2026-07-13  
**Status:** Approved  
**Source of truth:** `/Users/admin/IdeaProjects/ai-watch.sh`

## 1. Goal

Build a polished local Web client that exposes the operations currently provided by
`ai-watch.sh`. The application runs with one Docker Compose command, installs the
Linux versions of the Codex and Claude CLIs inside the image, and reads the host's
Codex, Claude, and CC Switch configuration through read-only mounts.

The default browser endpoint is `http://127.0.0.1:8787`. The application does not
mount `docker.sock` and does not attempt to execute macOS CLI binaries from inside
the container.

## 2. Confirmed Product Decisions

- Runtime: Go backend plus React/Vite frontend.
- Access scope: local host only by default.
- CLI execution: Codex and Claude are installed in the image.
- Configuration: host configuration is mounted read-only and copied into
  per-job temporary directories when needed.
- Script execution: the Web application reimplements the script's behavior as a
  job service; it does not automate the interactive TTY menu.
- Probe logs: never persisted. They are streamed live, kept only in bounded
  memory while a job is running, and destroyed promptly when the job ends.
- Persistent history: summary metadata only, with no raw CLI output or secrets.
- Notifications: DingTalk is server-side and secret-backed. Browser notifications
  replace the macOS `osascript` notification unavailable in Linux containers.

## 3. Detailed Interpretation of `ai-watch.sh`

### 3.1 Entry and State Machine

The script is a 1,874-line interactive Bash controller. It accepts no operational
command-line arguments. Apart from `-h` and `--help`, every argument is rejected,
and stdin must be a TTY.

Its startup sequence is:

1. Parse and reject unsupported arguments.
2. Require an interactive terminal.
3. Choose `probe` or `keepalive` mode.
4. Choose the Codex or Claude CLI.
5. Check required and optional dependencies.
6. Choose the current CLI configuration or a CC Switch provider.
7. Prepare temporary configuration for the selected provider.
8. Validate timeouts, intervals, retry values, and CLI availability.
9. Discover the active model, provider, base URL, and API key source.
10. Acquire a lock derived from CLI, base URL, and API key.
11. Start the probe or keepalive loop.

The TTY requirement makes the script unsuitable as a durable Web backend entry
point. The client will preserve its behavior through typed backend services rather
than answering its menu through a pseudo-terminal.

### 3.2 User Choices

The script has three interactive choices:

1. Mode: `probe` or `keepalive`.
2. CLI: `codex` or `claude`.
3. Configuration source: current CLI configuration or a CC Switch provider.

Choosing a CC Switch provider applies it only to the current task. It does not
change CC Switch's current provider and does not write to the CC Switch database.

### 3.3 Probe Mode

Probe mode repeatedly invokes the selected CLI until one of two terminal outcomes:

- Success: exit code is zero and output contains `READY`.
- Fatal failure: output matches an authentication, configuration, billing, login,
  or unsupported-argument error.

Timeouts, overloaded services, rate limits, and unmatched responses are considered
retryable. The original script has a zero-second retry interval, which can generate
high request volume. The client exposes the retry interval clearly and applies a
safer non-zero default while still allowing zero for compatibility.

On success or fatal failure, the original script sends DingTalk and macOS desktop
notifications. The client preserves DingTalk notification behavior and substitutes
browser notification support for `osascript`.

### 3.4 Keepalive Mode

Keepalive mode invokes the CLI immediately, then repeats at the configured interval
(120 seconds by default). Every result is classified, but no result terminates the
loop. It stops only when the user sends a stop command or the service shuts down.

The original script does not send notifications for keepalive attempts. The first
release preserves this behavior.

### 3.5 Codex Invocation

Codex is executed with an ephemeral, read-only, non-interactive command roughly
equivalent to:

```text
codex exec
  -c model_providers.<provider>.request_max_retries=<value>
  -c model_providers.<provider>.stream_max_retries=<value>
  --disable hooks
  --ephemeral
  --ignore-rules
  --skip-git-repo-check
  -s read-only
  "hi，只回复 READY"
```

For a CC Switch provider, the script creates a temporary `CODEX_HOME` containing
`config.toml` and `auth.json`, then injects the selected API key.

### 3.6 Claude Invocation

Claude is executed non-interactively with tools disabled and session persistence
disabled:

```text
claude
  --print
  --output-format text
  --no-session-persistence
  --safe-mode
  --permission-mode dontAsk
  --name claude-watch
  --tools ""
  [--model <model>]
  [--fallback-model <model>]
```

The prompt is sent through stdin. A CC Switch provider is applied through temporary
environment variables for the base URL, API key, and optional model.

### 3.7 Configuration Discovery

Current Codex configuration may come from:

- `${CODEX_HOME:-~/.codex}/config.toml`
- `${CODEX_HOME:-~/.codex}/auth.json`
- Provider-specific API key environment variables
- `OPENAI_API_KEY`, `CODEX_API_KEY`, `OPENAI_BASE_URL`, or `CODEX_BASE_URL`

Current Claude configuration may come from:

- `${CLAUDE_CONFIG_DIR:-~/.claude}/settings.json`
- Anthropic API key or auth-token environment variables
- Bedrock environment variables
- Vertex environment variables
- An Anthropic-compatible base URL

CC Switch providers are read from `~/.cc-switch/cc-switch.db` using read-only
SQLite queries and JSON extraction. Provider cards need to expose name, ID,
current status, model, base URL, and masked API key.

### 3.8 Process, Timeout, and Lock Behavior

Each attempt runs as a child process. The script polls it, terminates the process
tree on timeout, and returns status 124. On interruption it recursively terminates
descendants, waits briefly, then sends a forceful kill if required.

The lock key is a hash of:

```text
CLI | base URL | API key
```

Only one job may target the same tuple. Different providers may run concurrently.
The Go implementation will use an in-process job registry and OS process groups to
preserve this rule without risking termination of unrelated processes.

### 3.9 Result Classification

The backend exposes these normalized states:

- `success`: exit code zero and expected text matched.
- `timeout`: attempt exceeded its deadline.
- `overloaded`: overload, rate-limit, capacity, or usage-limit response.
- `fatal`: authentication, login, billing, configuration, or argument error.
- `unmatched`: command completed but expected text was absent.
- `stopped`: user stopped the job.
- `lock_conflict`: an equivalent target is already running.

Probe mode retries timeout, overloaded, and unmatched results. It stops on success,
fatal error, or a user stop. Keepalive mode continues after all attempt results
until stopped.

### 3.10 Security Findings

The source script contains a hard-coded DingTalk webhook token. It must not be
copied into source control, the image, browser JavaScript, logs, or API responses.
The client has no webhook default and accepts it only as a server-side environment
secret or non-persisted runtime value.

API keys must be masked in every response and redacted from process output before
streaming. Temporary configuration belongs to one job, is permission-restricted,
and is deleted only after ownership and path checks.

## 4. System Architecture

```text
Browser / React UI
  -> REST API for discovery, settings, and job control
  -> SSE for live job events

Go service
  -> Config Scanner
  -> Job Manager and target lock registry
  -> Codex Runner / Claude Runner
  -> Output classifier and redactor
  -> DingTalk notifier
  -> SQLite summary store

Container resources
  -> read-only host configuration mounts
  -> /run/ai-watch tmpfs for per-job configuration
  -> /data volume for settings and summary metadata
```

The production frontend build is embedded in the Go binary, so the runtime image
has one application service in addition to the CLI child processes it launches.
An init process forwards container shutdown signals correctly.

## 5. User Interface

The interface uses a dark operations-console visual language: deep navy surfaces,
cyan and emerald status accents, restrained gradients, clear typography, and
responsive layouts.

### 5.1 Dashboard

- Codex and Claude CLI availability.
- Readability of mounted Codex, Claude, and CC Switch configuration.
- Optional dependency health.
- Running jobs and their current attempt.
- Recent summary outcomes without raw logs.

### 5.2 New Job Flow

A guided drawer presents:

1. Probe or keepalive.
2. Codex or Claude.
3. Current configuration or CC Switch provider.
4. Advanced parameters.
5. Sanitized confirmation before start.

Advanced fields include probe timeout, retry interval, keepalive interval, Codex
request/stream retries, Claude model/fallback/retries/session name, prompt, expected
text, and notification enablement.

### 5.3 Provider Cards

Each card shows provider name, ID, current marker, model, base URL, and masked key.
The page states explicitly that selection affects only the new task and does not
switch the provider in CC Switch.

### 5.4 Job Detail

- Current status and latest attempt classification.
- Start time, elapsed time, attempt count, and next keepalive countdown.
- CLI, model, provider, base URL, and masked key source.
- Start and stop controls.
- Live system events and raw CLI output in separate visual streams.
- Auto-scroll toggle, copy, and download only while live data exists in the
  browser. The server does not offer historical log retrieval after destruction.

There is no pause/resume control because the source script has no such operation.

### 5.5 Settings

- Default timeouts and intervals.
- Default retry values.
- Summary history retention.
- DingTalk enabled/configured status with a masked value.
- Browser notification permission and test action.

## 6. Runtime Data Flow and Log Destruction

1. The browser submits a validated job request.
2. The backend resolves the selected configuration and computes the target lock.
3. Required configuration is copied into `/run/ai-watch/jobs/<job-id>` on tmpfs.
4. The runner launches the CLI in a dedicated process group.
5. Stdout and stderr are redacted and streamed through SSE.
6. A bounded in-memory ring buffer supports brief SSE reconnections and result
   classification. It is never written to `/data`.
7. At attempt completion, output is classified and the attempt buffer is cleared
   as soon as the classification event is emitted.
8. At job completion, all remaining log buffers are cleared, temporary files are
   removed, streams are closed, and the lock is released.

The persistent database stores only job summary fields: identifier, mode, CLI,
provider identifier, sanitized target label, status, timestamps, attempt count,
and elapsed duration. It stores no prompt, expected response, API key, webhook,
environment dump, or CLI output.

## 7. API Surface

Initial endpoints:

- `GET /api/health`
- `GET /api/config/status`
- `GET /api/providers?cli=codex|claude`
- `GET /api/jobs`
- `GET /api/jobs/{id}`
- `POST /api/jobs`
- `POST /api/jobs/{id}/stop`
- `GET /api/jobs/{id}/events`
- `GET /api/settings`
- `PUT /api/settings`
- `POST /api/notifications/test`

Responses never include an unmasked credential. SSE events use typed payloads for
job state, attempt start, sanitized output, classification, countdown, and cleanup.

## 8. Docker Packaging

The repository provides:

- A multi-stage Dockerfile for frontend build, Go build, and runtime packaging.
- A `compose.yaml` with `127.0.0.1:8787:8787` port binding.
- Read-only mounts for host Codex, Claude, and CC Switch directories.
- A named `/data` volume.
- A tmpfs mount for `/run/ai-watch`.
- Health checks and correct signal forwarding.
- `.env.example` for optional server-side settings and DingTalk configuration.

The documented startup command is:

```bash
docker compose up -d --build
```

## 9. Testing Strategy

### 9.1 Go Unit Tests

- Codex TOML and auth discovery.
- Claude settings and environment discovery.
- CC Switch provider parsing.
- API key masking and output redaction.
- Fatal, overload, timeout, unmatched, and success classification.
- Target lock identity and conflict handling.
- State transitions for probe and keepalive jobs.
- Prompt cleanup and summary persistence rules.

### 9.2 Process Integration Tests

Fake Codex and Claude executables exercise:

- Immediate success.
- Retry then success.
- Fatal error termination.
- Timeout and process-group termination.
- User stop and cleanup.
- Concurrent target conflict.
- Bounded log buffers and destruction after completion.

### 9.3 Frontend Tests

- New-job validation and provider selection.
- Running, success, fatal, timeout, and stopped states.
- SSE reconnect behavior.
- Live-only log controls.
- Secret masking and no historical log retrieval.
- Responsive layout and keyboard accessibility.

### 9.4 Container Verification

- Build the production image.
- Start with Docker Compose in one command.
- Verify health and local-only port binding.
- Verify read-only host configuration behavior.
- Run fake-CLI end-to-end probe and keepalive scenarios.
- Confirm `/data` contains no raw output or credentials.
- Confirm job tmpfs content and in-memory logs disappear after completion/restart.

## 10. Acceptance Criteria

- Docker Compose starts the client with one documented command.
- The browser provides every meaningful operation from `ai-watch.sh`: mode, CLI,
  configuration source, advanced parameters, start, stop, live status, live output,
  result classification, target lock feedback, and notifications.
- Codex and Claude current configurations and CC Switch providers are discoverable.
- Probe and keepalive behavior matches the source script's terminal and retry rules.
- The interface is polished, responsive, and usable without terminal interaction.
- API keys and DingTalk secrets never reach the browser in clear text.
- Probe logs are not persisted and are promptly destroyed after classification and
  job completion.
- Process trees, temporary configuration, and target locks are cleaned reliably.
- Automated tests and container-level checks cover the critical behaviors above.
