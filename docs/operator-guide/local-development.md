# Local operator setup

Run the README setup commands from the repository root. Keep `.env` private. `make configure` creates random per-install secrets and an operator UUID, never overwrites an existing configuration, and prints no passwords. Changing the UUID against an existing database is rejected by migrations.

## Identity

1. Open `http://localhost:8081/admin/`, sign in as `admin` using `KEYCLOAK_ADMIN_PASSWORD` from your local `.env`.
2. Select the `opencompute` realm. Create a user with username, verified email, first and last name; set a non-temporary password.
3. In **Role mapping**, assign `contributor` for device owners; assign `operator_admin` for the company administrator. Support and auditor roles have narrower read access. Do not assign administrator roles to every contributor.
4. Open `http://localhost:3000` and sign in. The public `opencompute-console` client uses Authorization Code + S256 PKCE; direct password grants are disabled.
5. Build the Linux agent and run `login`. Its separate `opencompute-agent` PKCE client redirects only to `http://127.0.0.1:8787/callback`. Port 8787 is a temporary loopback listener, never a public contributor port.

The console stores its access token in memory. Reloading may require sign-in again; no refresh token is persisted. The agent's short-lived login token is a private local file removed after enrollment. Subsequent connections use the enrolled Ed25519 identity.

## Native development

Start dependencies with `docker compose up -d postgres redis nats storage identity`. For the native API, set `DATABASE_URL`, `REDIS_URL`, `NATS_URL`, `S3_ENDPOINT`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, `OIDC_ISSUER`, `OIDC_AUDIENCE`, and `OPERATOR_ID`. Use the generated `.env` credentials, localhost ports, issuer `http://localhost:8081/realms/opencompute` and audience `opencompute-api`.

Run `pnpm migrate`, `pnpm dev:api`, and `pnpm dev:console`. Native tools do not automatically load `.env`; keep credentials in your process environment and never paste them into issue reports. The console proxies `/v1/*` to `API_INTERNAL_URL` (default `http://127.0.0.1:8080`).

## State and operations

`make down` stops the stack while preserving volumes. Database migrations are transactional and repeatable. Do not delete volumes to fix an identity/configuration mismatch: restore the original operator UUID or deliberately provision a separate deployment.

The initial API has a 32 KiB HTTP body limit, 16 KiB WebSocket limit, 240 requests/verified-user/minute and a separate 240 device-auth requests/IP/minute and a single-process gateway. Console lists show the latest 200 devices/events. Resource inventory is not available capacity. Production deployment, backup, signed releases, retention, HA and migration rollback are tracked milestones, not implemented guarantees.

## Credential retention and device key replacement

The API removes expired device credentials and authentication challenges at startup and once per minute. Cleanup never deletes registry, consent, capability history, or audit records. Failed cleanup is logged without credential contents and retried on the next interval. Live authentication always checks expiry, so cleanup timing cannot extend credential validity.

Capability changes received over an authenticated connection update the device and append a snapshot plus `CAPABILITIES_CHANGED` audit event in one transaction. Unchanged HELLO/heartbeats do not duplicate history. Device claims remain untrusted inventory.

For a deliberate device-key replacement: revoke the old device using the owner's authenticated endpoint, stop its local agent, and enroll a new identity using a new private `--state-dir`. Verify the old device cannot reconnect before deleting its old private key. Do not overwrite a state file to silently retarget an operator. Automatic rotating identities and administrator-assisted recovery remain later distribution work.

## Network verification boundaries

CI verifies outbound enrollment/heartbeats from a Linux agent, actual browser-to-Keycloak PKCE/CORS, and reconnection after the gateway restarts. CLI login briefly listens on loopback only. Go HTTP and WebSocket clients honor standard proxy environment variables. A production operator must still validate its own HTTPS proxy configuration, IPv6, residential CGNAT, TLS termination, certificates, and idle timeouts; local CI is not a substitute for that compatibility matrix.

## Scheduler identity and upgrades

Fresh `make dev` deployments import the confidential `opencompute-scheduler` client and `scheduler_service` realm role. `make configure` appends a unique `SCHEDULER_CLIENT_SECRET` to older `.env` files while preserving all existing values. The scheduler uses client credentials, has no human login/password flow, and runs without root or published host ports.

Keycloak does not reimport an existing realm. For an existing development volume, use the Keycloak admin console to add realm role `scheduler_service`; create confidential OIDC client `opencompute-scheduler` with service accounts enabled, standard/password flows disabled, secret matching your local `.env`, and the same `opencompute-api` audience mapper as the other clients. Assign only `scheduler_service` to its service-account user. Restart identity with the new environment and start the scheduler. Preserve volumes and existing users; do not delete data to make import work.

The Compose HTTP allowance is development-only. Production scheduler deployments require HTTPS API/token origins, a dedicated confidential client and rotated secret supplied through the operator's secret manager. Never grant this role to contributors or ordinary submitters. A scheduler outage prevents new external assignments; active local Stop, expiry and API job state remain independent.
