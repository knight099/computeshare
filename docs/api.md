# API v1 and protocol

This is the M0/M1 interface. Unimplemented endpoints in the master specification are future work and are not exposed as successful mocks. See `docs/openapi.json` for the initial OpenAPI contract.

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

Errors: 400 validation, 401 authentication, 403 consent/role, 404 absent or unowned device, 409 duplicate key, 429 rate limit, 500 redacted internal failure. Errors include a request trace ID. The gateway intentionally has no job/control execution messages enabled yet.
