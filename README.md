# OpenCompute

An open-source, self-hosted compute-contribution framework. Each company owns its control plane, identity provider, device fleet, policies, branding, and data.

**Current release: M0 / initial M1 enrollment preview.** The API, company console, and Linux agent support identity, explicit enrollment consent, device-key registration, outbound heartbeats, revocation, and audit history. **This release does not run compute jobs, start sharing sessions, or issue credits.** It is not production-ready.

## Run locally

Requires Docker Engine + Compose v2, Node.js 24+, pnpm 10.20.0, and Go 1.25+ for building the Linux agent. Reserve at least 12 GB free disk and 4 GB RAM for development images; Docker's VFS storage driver needs more space.

```sh
corepack enable
corepack prepare pnpm@10.20.0 --activate
pnpm install --frozen-lockfile
make dev
```

`make dev` generates unique local secrets in an ignored `.env`, builds the API/console image, migrates PostgreSQL, and starts PostgreSQL, Redis, NATS JetStream, SeaweedFS S3, Keycloak, the API, and console. It preserves an existing `.env` and persistent data. All published service ports bind to loopback.

- Console: <http://localhost:3000>
- API health / dependency readiness: <http://localhost:8080/healthz> / <http://localhost:8080/readyz>
- Keycloak: <http://localhost:8081/admin/> — username `admin`; password is your generated `KEYCLOAK_ADMIN_PASSWORD` in `.env`.

Create users in the `opencompute` realm and assign roles as described in the [operator guide](docs/operator-guide/local-development.md). No default user password is committed. Sign in through Keycloak using PKCE.

```sh
make build
bin/opencompute-agent login --gateway http://localhost:8080
bin/opencompute-agent enroll --gateway http://localhost:8080 --name my-linux-pc --accept-consent
bin/opencompute-agent run
```

Review the [enrollment disclosure](docs/contributor-guide.md) before accepting. Stop the agent with Ctrl+C. Registering a device does not authorize future compute execution. Future sharing sessions require their own explicit resource and duration consent.

## Verify

```sh
make lint
make typecheck
make test
make build
# Disposable database; tests create/drop their own isolated schema.
TEST_DATABASE_URL=postgres://user:password@localhost:5432/test_db make test-integration
# Requires make dev, built agent, and generated .env; uses a temporary Keycloak user.
pnpm test:e2e
```

See [verification evidence](docs/verification.md) for what was actually run and environment limitations. CI includes unit, database-backed security/integration tests, Go race tests, production UI build, and a complete Compose acceptance job.

## Architecture and delivery

The Company Platform and Contributor Application are independently deployable products. A modular Fastify API owns identity/device domains, PostgreSQL owns durable state, and the Go agent connects outbound. The initial Next.js console includes owner-scoped device and enrollment views; later contributor session/history screens are tracked separately.

- [Master product specification](OPENCOMPUTE_MASTER_BUILD_PLAN.md)
- [All stages and implementation status](docs/implementation-plan.md)
- [Architecture decisions](docs/adr/001.md)
- [Threat model](docs/threat-model.md)
- [API and protocol](docs/api.md)
- [Troubleshooting](docs/troubleshooting.md)

Development storage uses maintained SeaweedFS rather than unavailable MinIO images; both expose S3. See [ADR-009](docs/adr/009.md). Production deployments must supply HTTPS, maintained infrastructure, least-privilege credentials, privacy/legal terms, signing keys, and operational review. No author-controlled service, blockchain, or central marketplace is required.

Apache-2.0 for framework code. Infrastructure services retain their own licenses; operators must review dependencies and applicable patent/legal requirements.
