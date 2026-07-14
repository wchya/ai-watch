# Secure Provider and DingTalk Configuration Design

**Date:** 2026-07-14  
**Status:** Approved

## Goal

Allow users to create Codex and Claude providers and configure DingTalk from the
browser. Redis is the durable configuration source. API keys and webhook URLs
are encrypted with AES-GCM before they are written to Redis and are never
returned to the browser in plaintext.

## Architecture

- Automatic Codex, Claude, and CC Switch discovery remains read-only.
- Manual providers use IDs namespaced as `manual:<id>` and are merged into the
  existing provider list.
- A hybrid resolver routes namespaced IDs to the encrypted configuration store
  and all other IDs to the existing configuration scanner.
- Business and HTTP layers depend on narrow secure-configuration interfaces;
  they never depend on a Redis client or ciphertext representation.
- A dynamic DingTalk notifier reads the stored webhook for each send. Stored
  configuration takes precedence over the environment variable, which remains
  a fallback and first-run import source.

## Data Model

### Manual Provider

- `id`, `name`, `cli`
- `baseUrl`, `model`, `provider`
- encrypted `apiKey`
- `createdAt`, `updatedAt`

Public responses replace the API key with `hasApiKey` and `maskedKey`. On
update, an omitted or blank API key keeps the existing value. An explicit
`clearApiKey` flag removes it. Creating a provider requires a usable API key.

### DingTalk Configuration

- encrypted `webhookUrl`
- `updatedAt`

Public responses contain only `configured`, `source`, `maskedWebhook`, and
`updatedAt`. `source` is `redis`, `environment`, or `none`.

## API

- `GET /api/manual-providers`
- `POST /api/manual-providers`
- `GET /api/manual-providers/{id}`
- `PUT /api/manual-providers/{id}`
- `DELETE /api/manual-providers/{id}`
- `GET /api/notifications/dingtalk/config`
- `PUT /api/notifications/dingtalk/config`
- `POST /api/notifications/test` validates the effective dynamic webhook

Deleting a provider referenced by an active job or schedule returns HTTP 409.
Provider writes validate CLI, HTTP(S) URL, field lengths, and credential
presence. Errors never include decrypted values.

## User Interface

Add a dedicated provider configuration view with separate Codex and Claude
categories. Manual-provider cards support create, edit, delete, and launching
the existing probe flow. Secret inputs are write-only; editing shows only a
masked status, and successful submissions clear secret state immediately.

Add a DingTalk configuration card to Settings. It shows the effective source,
masked webhook, save/clear controls, and a test action. The established dark
signal-console visual language remains unchanged, with dense operational cards,
44px controls, visible focus, focus-trapped dialogs, inline errors, and reduced
motion support.

## Security and Lifecycle

- AES-GCM uses a fresh nonce for every write.
- Encryption key material comes from service configuration and is never stored
  in Redis.
- Redis stores ciphertext envelopes and non-sensitive metadata only.
- API responses, events, logs, and DingTalk test errors never expose plaintext
  credentials.
- Provider deletion removes its encrypted credential and metadata together.

## Verification

- Store tests cover encryption round trips, random nonces, wrong-key failures,
  and absence of plaintext in Redis.
- API tests cover masking, retain/replace/clear secret semantics, validation,
  and referenced-provider deletion conflicts.
- Resolver tests cover manual and automatic provider routing.
- Notifier tests cover Redis precedence, environment fallback, clearing, and
  test delivery.
- Frontend production build and responsive/accessibility checks cover CRUD and
  configuration dialogs.
