# Redis Host Port And Probe Log Cache Design

## Goals

- Publish Redis on the host loopback interface at the default Redis port.
- Keep container-to-container access on the existing Compose network address.
- Cache complete probe job log details for 24 hours after the latest write.
- Allow completed probe jobs to replay cached logs in the existing detail view.
- Keep cached output bounded and redact credentials before persistence.

## Redis Exposure

The Redis service publishes `127.0.0.1:${REDIS_PORT:-6379}:6379`. Host tools connect to `127.0.0.1:6379`; AI Watch continues to connect to `redis://redis:6379/0`. Binding only to loopback avoids exposing the unauthenticated Redis service to the LAN.

## Probe Log Storage

Each probe job uses an isolated Redis list under the AI Watch namespace. Entries contain the existing ordered job event payload so the current SSE and frontend normalization paths can be reused. Every append refreshes a fixed 24-hour TTL.

Only probe jobs persist detailed events. Keepalive jobs retain the existing in-memory-only output behavior. Cached probe logs are bounded to 5,000 entries and approximately 2 MiB per job, discarding the oldest entries first.

Before persistence, output is passed through the existing redaction layer with the resolved provider credentials and known environment secrets. Live execution behavior remains unchanged, while persisted replay never stores raw configured secrets.

## Read And Replay Flow

The job event endpoint first loads cached probe events newer than the requested event ID. For a running job it then subscribes to live events; for a completed historical job it returns the cached replay and closes the stream. If the TTL has expired, the detail page presents an expired/unavailable state instead of implying that logs were deliberately cleared immediately.

## Compatibility And Documentation

The Redis-backed production store implements the cache directly through an optional cache interface. Other stores keep their existing behavior. Compose examples and README guidance document host and container connection addresses plus the 24-hour retention boundary.

## Verification

- Validate Compose renders the loopback Redis port mapping.
- Test Redis append, ordering, bounds, and 24-hour expiration.
- Test credential redaction before probe log persistence.
- Test completed-job replay through the manager/API path.
- Test that keepalive output remains uncached.
