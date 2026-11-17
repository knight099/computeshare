# API v1 and protocol

This documents the M0/M1 interface and CPU-only M2 preview. Unimplemented endpoints in the master specification are future work and are not exposed as successful mocks. See `docs/openapi.json` for the initial OpenAPI contract.

| Method / path                    | Authentication             | Behavior                                                                               |
| -------------------------------- | -------------------------- | -------------------------------------------------------------------------------------- |
| GET `/healthz`                   | Public                     | Process liveness; no dependency checks                                                 |
| GET `/readyz`                    | Public                     | Database/schema, Redis, NATS, authenticated S3 and identity probes; 503 if unavailable |
| GET `/v1/config`                 | Public                     | Operator name/UUID, OIDC issuer/client, consent version, execution disabled            |
| GET `/v1/me`                     | OIDC                       | Account ID and trusted issuer roles                                                    |
| POST `/v1/consent`               | Contributor OIDC           | `{version:"2026-11-14",accepted:true}`                                                 |
| POST `/v1/devices`               | Contributor OIDC + consent | Device name, raw Ed25519 public key (base64), signature, capabilities                  |
| GET `/v1/devices`                | OIDC                       | Up to 200 owned devices                                                                |
| GET `/v1/devices/{id}`           | Owner OIDC                 | Owned device, otherwise 404                                                            |
| POST `/v1/devices/{id}/revoke`   | Owner OIDC                 | Invalidate credentials, close local gateway connection, audit                          |
| POST `/v1/device-auth/challenge` | Public device ID           | Single-use random nonce, 60-second validity                                            |
| POST `/v1/device-auth/token`     | Device signature           | `{device_id,nonce,signature}` yields opaque bearer credential, default 300 seconds     |
| GET `/v1/agent/connect`          | Device bearer              | WebSocket upgrade; bearer header, never query string                                   |
| GET `/v1/operator/devices`       | Admin/support/auditor      | Fleet inventory and last-heartbeat-derived status                                      |
| GET `/v1/operator/audit-events`  | Admin/auditor              | Last 200 events; viewing is audited                                                    |

Registration signature message (UTF-8): `opencompute-enroll-v1:{operator_id}:{public_key}`. Challenge message: `opencompute-auth-v1:{operator_id}:{device_id}:{nonce}`. Sign with Ed25519 and base64-encode the signature. Public keys are the raw 32-byte key, not PEM. No private key reaches the API.

Capabilities: `os` (`linux`, `windows`, `darwin`), `arch`, integer `cpu_cores`, integer `memory_mb`, `agent_version` (`0.1.0`), `runtimes` (empty in this release).

WebSocket messages use `{protocol_version:1,type,device_id,sequence,trace_id,capabilities?}`. Send `HELLO` with capabilities first, then `HEARTBEAT` every 15 seconds. Sequence must strictly increase on each connection; device ID must match the credential. The server returns `ACK` with matching sequence/trace plus UTC server time. Unknown fields/versions, replay, wrong identity, revoked/expired credentials, or missing heartbeat close the connection. Reconnect with a new challenge/credential and HELLO; the local agent adds exponential backoff/jitter.

Errors: 400 validation, 401 authentication, 403 consent/role, 404 absent or unowned device, 409 duplicate key, 429 rate limit, 500 redacted internal failure. Errors include a request trace ID. CPU-only job/control additions are described below.

## M2 synthetic compute additions

- `POST /v1/availability-sessions` (owner contributor) accepts `{device_id,duration_seconds,policy,consent_version:"sharing-v1",accepted:true}`. Durations are 3600/7200/14400/21600/28800/43200 seconds; policy is `{cpu_percent:10..100,memory_mb:64..1024,gpu_enabled:false,idle_only:false}`. GET the resulting `/v1/availability-sessions/{id}` or POST its `/stop`; ownership is enforced. The agent has corresponding device-authenticated `/v1/agent/availability-sessions` POST/stop routes.
- `POST /v1/jobs` (admin/workload submitter) accepts `{idempotency_key,workload_type:"synthetic-hash-v1",seed,iterations,timeout_seconds}`. Public ASCII seed length is 1–64, iterations 1–1,000,000, timeout 1–30 seconds. A repeated key with identical input returns the original job; different input returns 409. Unknown executable/file/image fields are rejected.
- `GET /v1/jobs/{id}`, POST `/v1/jobs/{id}/cancel`, GET `/v1/operator/jobs` and GET `/v1/contributor/jobs` expose scoped lifecycle/result metadata. Contributor history contains no input payload. Credits remain unimplemented.
- Control messages add `WORK_REQUEST`, `JOB_ACCEPT`, `JOB_START`, `JOB_COMPLETE`, `JOB_FAIL`, optional `session:{id,status}` and `attempt:{id,epoch,result?}`. ACK may include an `offer`, `cancel_attempt_id`, or validated `job_state`. The agent must locally approve the matching session and enforce its own limits before starting. No arbitrary command or artifact appears in this protocol.
- Offers have a 30-second fenced lease bounded by session expiry. This slice fails lost attempts rather than automatically retrying; durable retry/queue delivery is subsequent work. Completion is verified against the server-computed known hash. At-most-one active attempt per device and at-most-one active availability session are database constraints.

Job lists accept `limit=1..200` (default 200) and an opaque `cursor`, and return `{jobs,next_cursor}`. Pass `next_cursor` unchanged until it is null. Both lists sort newest first by creation time and job ID; cursors preserve PostgreSQL microseconds. Authorization is reapplied to every page. New jobs may appear before the current page during concurrent submission. Contributor history excludes input payloads.

Submission performs bounded asynchronous verification before opening its transaction. A saturated, failed or timed-out verifier returns 503; clients may retry with the **same** idempotency key. A duplicate of an already committed job bypasses verification.

Job events are durably captured into the database outbox and published on `opencompute.{operator_uuid}.jobs.events` in JetStream stream `OC_{operator_uuid_without_hyphens}`. Metadata includes `protocol_version`, string `event_id`, `job_id`, optional `attempt_id`, `state`, `trace_id`, and `occurred_at`; workload seeds and credentials are excluded. Delivery is at least once and can reorder: consumers must deduplicate by operator/event ID and consult authoritative job state when needed. This stream does not yet dispatch work or award credits.

## Scheduler service boundary

Compose runs `SCHEDULER_MODE=external` and a separate Go scheduler. The API's default `inline` mode retains the initial development FIFO path; both modes use the same reservation function and database transaction rules. The service authenticates with OIDC client credentials and the `scheduler_service` realm role. Human operator roles do not grant service access.

- `GET /internal/scheduler/candidates`: bounded snapshot (128 jobs/devices) with IDs, resource requirements, FIFO timestamps, request revision and remaining seconds; no workload seeds or device credentials.
- `POST /internal/scheduler/claims`: `{job_id,device_id,session_id,request_revision}`; returns 201/attempt ID or 409 when stale/ineligible. API rechecks a local work request fresher than three seconds, active consent/session, revocation, capabilities, memory, deadline plus five-second margin, job state and exclusive capacity under the compute transaction lock. No scheduler-owned database state exists.

Offers are returned only on a subsequent authenticated matching agent work request. Snapshots are advisory; stale/conflicting proposals are expected. The scheduler caches short-lived service tokens, rejects redirects, uses bounded requests, and marks readiness only after successful polling. JetStream remains a durable metadata event channel, not the dispatch authority. The global API compute lock is still a throughput limit; this service is not an HA/scale certification.
