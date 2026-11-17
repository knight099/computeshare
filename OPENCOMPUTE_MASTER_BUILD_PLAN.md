> Scope amendment (2026-11-17): [docs/product-scope.md](docs/product-scope.md) overrides all credit, pricing, settlement, payment and business-management requirements below. Preserve technical usage verification, isolation and security gates. This original specification is retained as historical design context; use the active implementation plan and engineering tracker for delivery.

# OpenCompute — Master Product Requirements & Engineering Implementation Plan

**Status:** Implementation-ready architecture and phased delivery specification  
**Version:** 1.0 · 14 November 2026  
**Audience:** Autonomous coding agents, engineers, security reviewers, and organization operators  
**Working name:** OpenCompute (name/trademark/domain not verified)  
**License target:** Apache-2.0, pending dependency and patent review

---

## 0. Instructions to the coding agent

You are implementing an **open-source, self-hosted, white-label compute-contribution framework**. Follow this document as the product specification. **Do not attempt to deliver the entire production system in one pass.** Implement milestones sequentially with executable tests and documentation. Do not represent mocks as production-ready features. Do not silently weaken isolation, verification, authentication, or user consent to make a demo work.

**First deliverable:** a bootable monorepo with a Docker Compose company control plane, a functioning Linux node agent CLI, a contributor web UI (desktop shell can follow), a job API, a safe *allowlisted synthetic workload* executor, a scheduler with leases, auditable metering, and a simulated credit ledger. A company operator must be able to deploy it without contacting the framework author.

Before writing code: (1) inspect the existing repository, if any; (2) produce a concise implementation plan and ADRs; (3) establish lint, typecheck, unit/integration test, and CI pipelines; (4) execute each milestone's acceptance tests. Ask for clarification only on decisions that block secure implementation. All unknown secrets, signing certificates, production domain names, legal terms, and payment credentials must remain configurable placeholders and must never be fabricated.

### Hard constraints

- Two independently distributable software products: **Company Platform** and **Contributor Application**. Internal services/packages are allowed.
- Any organization can self-host, brand, operate, and extend the software. No mandatory centralized marketplace, cryptocurrency, blockchain, or author-controlled service.
- Companies own their identities, accounts, device fleets, workloads, credit policies, infrastructure, signing keys, and data.
- A contributor explicitly opts in, chooses an availability duration (e.g. six hours), can stop immediately, sees resource usage, job activity, and credit history. Availability is **not** synonymous with billable usage.
- No inbound public IP, DNS record, or router changes required on the contributor PC for the baseline.
- **Never execute arbitrary untrusted customer code directly on the host OS.** Phase 1 uses safe allowlisted synthetic jobs only; production arbitrary jobs require a reviewed isolated runtime.
- Do not assume enterprise model weights or sensitive datasets can safely be sent to consumer PCs.
- Separate validated compute usage from accounting settlement. Avoid double credits across retries.
- Preserve user privacy, provide explicit consent and resource controls, and honor stop/expiry.
- Make all workflows observable, testable, recoverable, and documented.

---

## 1. Vision and product boundaries

### 1.1 Problem

Organizations own compute-intensive workloads but may want to utilize voluntarily shared idle CPU/GPU capacity from computers outside their data centers. Ordinary device owners need a transparent, controllable application that shows what is running and awards credits for verified contribution. Existing decentralized compute systems do not automatically solve organization-specific branding, account integration, compliance, security, metering, or deployment.

### 1.2 Product

**Software A — Company Compute Platform:** Self-hosted server/control plane, APIs, administration console, device registry, scheduling, job lifecycle, validation, credit ledger, observability, and deployment assets. Branded and operated by each organization.

**Software B — Contributor Application:** Branded desktop application plus privileged-minimal background node agent; authenticates with that organization's identity provider; detects hardware; enforces user-selected availability/resource policies; connects outbound; executes supported jobs in isolation; displays status, activity, and credits.

The company is the **operator and compute consumer** in the initial product. There is **no public cross-company marketplace**. Optional federation is future scope and must require explicit operator agreements and user consent.

### 1.3 Example (illustrative, not a partnership)

An AI provider deploys the platform on its servers, publishes a signed contributor installer, and lets a user sign in with its existing account. The user opts in for six hours. The scheduler allocates suitable approved batch jobs while the device is available. The application displays live status, task identifiers, usage, and a transparent credit balance. The company chooses the credit conversion and redemption policy.

### 1.4 Personas

1. **Organization operator/admin:** Installs the platform; configures identity, branding, workload policy, credits, security, deployment, and logs.
2. **Organization workload developer:** Submits approved jobs; inspects execution, outputs, cost, errors, and retry history.
3. **Contributor:** Installs app, logs in, opts in, sets limits, reviews tasks and credits, revokes participation.
4. **Auditor/support:** Reviews tamper-evident job and credit events with least-privilege access.

### 1.5 Non-goals for v1

No globally shared marketplace; no fiat payout rails; no blockchain; no cross-organization identity federation; no real-time low-latency APIs; no giant-model distributed synchronous training; no live GPU process migration; no arbitrary Windows-host code execution; no guarantees of enterprise data confidentiality on untrusted hosts; no unsupported promises of 99.99% uptime.

---

## 2. Product requirements and user journeys

### 2.1 Company onboarding

1. Clone repository and configure environment variables.
2. Run `docker compose up -d` for local development; use documented production manifests later.
3. Configure public HTTPS hostname, OIDC issuer/client, service credentials, signing keys, storage, database, and policies.
4. Configure brand: company display name, logo asset, theme, support URL, terms/privacy URLs, credit display name, credit rate policy, approved workloads.
5. Build and sign a company-specific contributor installer; publish download and checksum.
6. Register workload submitter/service account; submit a safe test job.
7. View devices, sessions, job states, results, metering, and credit ledger.

### 2.2 Contributor onboarding

1. Download a signed installer from the organization.
2. Verify publisher identity; install with minimum privileges.
3. Open the application; sign in via system browser using OAuth 2.0 Authorization Code + PKCE / OIDC.
4. Read clear consent and workload/privacy disclosure; accept explicitly.
5. Agent inventories supported hardware and tests virtualization, GPU runtime, network, and storage capabilities.
6. Choose resources (CPU cores/%, RAM, GPU eligibility/VRAM, disk, bandwidth), idle-only or dedicated mode, and availability window.
7. Click **Start sharing**; see active session timer, connection, device health, and jobs.
8. View all job state changes, verified usage, pending/settled credits, and errors.
9. Click **Stop** at any time; new work stops immediately and running work is canceled/checkpointed according to user-approved policy.
10. On expiry, automatically stop accepting work and release resources; show session summary.

### 2.3 Availability semantics

- `availability_session`: UTC `starts_at`, `expires_at`, `requested_duration_seconds`, `status`, policy snapshot, and user consent version.
- Session must not auto-renew without renewed consent. Device offline does not extend the window.
- Agent enforces expiry locally even when disconnected from control plane.
- Jobs admitted only if expected completion fits the remaining window plus explicit safety margin; longer checkpointable jobs require policy support.
- User activity in idle-only mode immediately suspends new scheduling and triggers safe pause/cancel; behavior configurable and visible.
- User stop overrides organization scheduling policy. Stop must not depend on server connectivity.
- Screen lock, sleep, reboot, network failure, battery, thermals, and metered network changes trigger documented state transitions.
- Device owner may disable GPU, set minimum battery/AC-power requirement, max temperature, max disk use, and monthly data cap.

### 2.4 Contributor UI screens

- Sign in / consent / device pairing.
- Home: Online/Offline/Running/Paused; session countdown; Start/Stop; resource utilization; active job; credits earned today.
- Share settings: duration presets (1, 2, 4, 6, 8, 12 hours), idle-only toggle, CPU/GPU/RAM/disk/network caps, temperature limits, power requirements.
- Jobs: timestamped job list with job ID, type, state, resource allocation, duration, validation status, and credit status (do not expose proprietary payloads).
- Credits: pending/confirmed/reversed, rate policy, usage basis, transaction history, downloadable statement.
- Activity/audit: authentication, registration, agent updates, permission changes, task starts/stops, disconnections, policy triggers, errors.
- Device diagnostics: hardware compatibility, NAT/connectivity, agent/runtime version, storage use, self-test, export redacted diagnostic bundle.
- Privacy/security: consent version, data retained, uninstall, revoke device, delete request instructions.

### 2.5 Company console screens

- Overview: active devices, available CPU/GPU, queue depth, running jobs, verified throughput, failure rate, credit liabilities.
- Devices: hardware, capabilities, version, connectivity, reputation, session status, last heartbeat, quarantine.
- Jobs: submit, list, inspect DAG/attempts, cancel, retry, validation, outputs, latency.
- Workload policies: allowlist, resource quotas, regions, timeouts, verification level, data classification.
- Credits: rate rules, ledger entries, manual adjustments with dual approval/audit, export.
- Identity/access: OIDC integration, service accounts, RBAC.
- Branding: logo, theme, company strings, credit unit, installer config.
- Security/observability: signing keys, agent versions, audit logs, alerts, metrics, retention.

---

## 3. System architecture

```text
Company's own infrastructure
┌─────────────────────────────────────────────────────────────┐
│ Company Console / Workload SDK / Internal Job Producers      │
│                   HTTPS + OIDC / mTLS                       │
│ API Gateway / AuthZ / Device Registry / Policy Engine       │
│ Scheduler + Lease Manager  ←→  NATS JetStream               │
│ Job Service / Validation / Metering / Credits Ledger        │
│ PostgreSQL  Redis  Object Storage  Observability            │
│ Outbound-agent connection gateway / optional relay          │
└─────────────────────────┬───────────────────────────────────┘
                          │ agent-initiated TLS connection
          ┌───────────────┼────────────────────┐
          ▼               ▼                    ▼
    Contributor A    Contributor B        Contributor C
    Desktop UI       Desktop UI           Desktop UI
    Go agent         Go agent             Go agent
    Isolated runner  Isolated runner      Isolated runner
```

**Control plane centralized per company, execution distributed.** Agent maintains authenticated outbound connection to company gateway. For the MVP, task and control messages travel through this connection, with artifacts downloaded from signed URLs over HTTPS. P2P is an optional optimization, not a launch prerequisite.

### 3.1 Recommended stack

| Subsystem | Technology | Rationale |
|---|---|---|
| API, auth integration, console backend | Fastify + TypeScript | Extensible interfaces and ecosystem |
| Operator and contributor web UI | Next.js + TypeScript | Reusable components and white-label theming |
| Desktop shell | Tauri | Cross-platform UI, modest resource footprint |
| Background node agent | Go | Cross-platform system telemetry and robust networking |
| Scheduler / lease service | Go | Concurrency, clear state machine, bounded retries |
| Durable events | NATS JetStream | At-least-once dispatch, durable consumers |
| System of record | PostgreSQL | Transactions, constraints, audit/ledger |
| Ephemeral cache | Redis | Presence, rate limits, not authoritative billing |
| Artifact store | S3-compatible (MinIO in dev) | Immutable artifacts and signed URL transfers |
| Transport | TLS 1.3 over WebSocket/gRPC stream | Outbound connectivity; no host inbound ports |
| Optional P2P | libp2p or ICE/STUN/TURN after benchmark | Bandwidth optimization only |
| Execution | Phase 1 allowlisted synthetic runner; later reviewed VM sandbox | Avoid unsafe host execution |
| Observability | OpenTelemetry, Prometheus, Grafana, Loki or equivalent | Traceability and operational visibility |
| Deployment | Docker Compose dev; Helm/Terraform later | Self-hosting without central dependency |

**Platform caveat:** Tauri UI does not itself provide secure GPU isolation. Consumer Windows GPU access through isolated VMs varies by driver, edition, virtualization and hardware. Start with Linux-only approved runtimes; support Windows for enrollment/telemetry first, and mark GPU execution unsupported until a tested isolation strategy exists.

### 3.2 Trust boundaries

- Organization controls company services and policy but must not gain unrestricted access to contributors' personal OS/files.
- Contributor devices are untrusted: may disconnect, lie about resources, tamper with clocks, or return incorrect results.
- The company should assume workload artifacts sent to consumer machines can be inspected or copied unless protected by a specifically validated confidential-computing solution.
- Company-issued code and agent updates must be signed; signed does not mean harmless. Limit capabilities and audit supply chain.
- Device telemetry is sensitive and must be minimized, disclosed, encrypted, and retention-limited.

---

## 4. Network architecture: NAT, CGNAT, DNS, connectivity

### 4.1 Baseline protocol (mandatory)

1. Agent resolves the company's public HTTPS gateway DNS, e.g. `contribute.example.org`.
2. Agent opens outbound TLS 1.3 on TCP 443 and authenticates using short-lived device credentials bound to its registered identity.
3. Gateway maintains device session and sends jobs/control messages over the established authenticated channel; agent reconnects with exponential backoff + jitter.
4. Agent downloads signed, content-addressed artifacts using outbound HTTPS, verifies hashes/signatures, and uploads results via signed URLs or gateway.
5. No public IPv4, static IP, public DNS entry, UPnP, port forwarding, or direct incoming connection required on contributor PCs.
6. Support proxies, IPv6, DNS changes, intermittent Wi-Fi, NAT idle timeouts, and token rotation; log failures without leaking secrets.

### 4.2 Optional peer-to-peer transport (later)

- Attempt direct paths via ICE/STUN or a vetted P2P transport; relay via TURN/company relay if direct paths fail.
- CGNAT/symmetric NAT can prevent direct connectivity; never promise universal hole punching.
- Use content hashes, authenticated peer identities, encrypted channels, rate limits, and explicit operator/user policy.
- Peer-to-peer transfers should initially be restricted to **public/non-sensitive immutable artifacts**, not customer secrets.
- Track relay bandwidth, upload bottlenecks, peer churn, and transfer cost; P2P may be less economical than CDN for small networks.

### 4.3 DNS and addressing

- Only the **company-operated gateway** needs a stable DNS name and TLS certificate.
- Each device receives an opaque `device_id` and authenticated connection identity, not a public DNS hostname.
- Gateway maps `device_id → current authenticated connection` in ephemeral presence storage; registry remains in PostgreSQL.
- Never treat an IP address as device identity or as proof of location.

### 4.4 Network failure handling

- Heartbeat interval initially 15 seconds; suspect after 45 seconds; lease expiry configurable (e.g. 90 seconds) with jitter and false-positive safeguards.
- A disconnected agent cannot receive new jobs; local expiry/stop policies remain enforceable.
- On reconnect, reconcile attempt IDs, lease epochs, completed artifacts, and unsettled usage without duplicate execution credits.
- Server remains authoritative for job acceptance and settlement; agent metrics are evidence, not sole proof.

---

## 5. Job execution, scheduling, and reliability

### 5.1 Workload classes

**V1 supported:** Synthetic deterministic CPU benchmark; deterministic hash/checksum processing; small public-data batch transforms using allowlisted runner.  
**V1.5:** Independent batch rendering/transcoding/inference in reviewed isolated Linux runtime.  
**Later:** GPU inference and checkpointable training microtasks where legally and technically appropriate.  
**Not initial:** Arbitrary privileged containers, confidential enterprise datasets, tightly coupled multi-node GPU training, real-time serving.

### 5.2 Job and attempt states

- Job: `SUBMITTED → VALIDATING → QUEUED → RUNNING → VERIFYING → SUCCEEDED`.
- Alternate job states: `RETRY_WAIT`, `FAILED`, `CANCELED`, `EXPIRED`.
- Attempt: `OFFERED → ACCEPTED → STARTING → RUNNING → UPLOADING → FINISHED → VERIFIED` or `REJECTED/TIMED_OUT/LOST/FAILED/CANCELED`.
- State transitions use PostgreSQL transactions with version/epoch checks. Events can be delivered more than once; consumers must be idempotent.
- A job may have several attempts; only an accepted verified attempt (or an explicitly configured redundant-verification policy) can trigger payout.

### 5.3 Scheduler

1. Validate workload signature, type, resource bounds, and data classification.
2. Filter nodes by active consent/session, remaining time, available resources, runtime compatibility, policy, geography if required, thermal/power constraints, and connectivity.
3. Rank eligible nodes by predicted completion, reliability, free resources, network cost, and operator policy.
4. Atomically reserve capacity and issue short-lived job lease with fencing token/epoch.
5. Agent explicitly accepts/rejects before execution; do not oversubscribe beyond configured limits.
6. Renew lease while running; on expiry, requeue if retry-safe and under attempt limit.
7. Verify output and reconcile credits exactly once using unique settlement keys.

Example ranking (tunable, not patented algorithm implementation):

`score = w1 * predicted_success - w2 * predicted_latency - w3 * network_cost - w4 * interruption_risk`

The MVP uses simple deterministic resource matching; introduce learned prediction only after collecting sufficient data.

### 5.4 Failure recovery

- Durable queue + lease timeout + bounded exponential retry + dead-letter state.
- Idempotency key per submitted job and per credit settlement.
- Execution must be **at-least-once**, not falsely advertised as exactly-once. Exactly-once *accounting effect* is enforced transactionally.
- On disconnect, job is recoverable by retry on another compatible node. Non-idempotent jobs require stronger application semantics and are out of scope for MVP.
- Checkpoints later: immutable artifact manifest, checksum, job version, lineage, retention, replication target, restore compatibility.
- Speculative duplicate attempts later for stragglers; avoid duplicate payouts unless the company intentionally pays both verification workers.

### 5.5 Verification

- Phase 1 deterministic result hashes against expected output and server-controlled test vectors.
- Later: randomized challenge tasks, redundant execution, spot checking, anomaly detection, trust/reputation, operator review.
- Never claim that a result hash proves correct arbitrary computation; hashes only verify artifact integrity or known expected results.
- Do not award credits solely based on agent-reported GPU utilization or wall-clock time.

---

## 6. Secure execution and contributor safety

### 6.1 Mandatory controls

- Explicit opt-in and documented permission scope; local Stop button works offline.
- Signed company build, signed update manifests, artifact signature/hash verification, rollback and revocation.
- Principle of least privilege; agent runs unprivileged wherever possible; separate tightly controlled helper only when required.
- No direct host filesystem access, no host credentials, no browser profile access, no clipboard/camera/microphone access.
- Sandboxed workload filesystem; resource cgroups/quotas; network egress restrictions; process lifetime and disk quotas.
- Host temperature/power monitoring; pause on overheating, user activity, battery threshold, metered-network cap, and user stop.
- Secure secrets storage; short-lived tokens; no plaintext auth tokens in logs.
- Explicit security review and threat modeling before enabling arbitrary third-party workloads.

### 6.2 Isolation roadmap

- **M0–M2:** Allowlisted built-in workload runner; no arbitrary scripts, shell commands, OCI images, or GPU access.
- **M3:** Reviewed Linux VM/microVM sandbox with measured escape-resistance assumptions, constrained egress, artifact integrity, and host isolation tests.
- **M4+:** GPU isolation research by platform; document supported GPU models, drivers, hypervisors, and limitations. If isolation is not demonstrably safe, do not enable public GPU jobs.

### 6.3 Supply-chain protection

- SBOMs, dependency scanning, pinned images, signed release artifacts, reproducible build efforts, vulnerability disclosure policy, automated update rollback.
- Company-specific installer configuration must be signed and bound to an expected operator domain and identity. Prevent silent retargeting to another organization.
- An operator's ability to customize branding must not bypass mandatory consent, auditability, or sandbox safety.

---

## 7. Credits, usage metering, and accounting

### 7.1 Definitions

- **Available time:** user volunteered device availability; may be unpaid.
- **Allocated time:** capacity reserved for a job; may be billable under a separate explicit policy.
- **Executed time:** runtime reports work in progress; requires validation.
- **Verified billable units:** accepted execution, measured using server-observed lease intervals and corroborating agent/runtime telemetry, subject to caps and verification.
- **Credits:** operator-defined non-cash units; redemption, expiration, taxes, transferability, and legal status depend on operator policy.

### 7.2 Credit formula (initial example)

`credits = verified_billable_seconds / 3600 * configured_rate_per_hour * hardware_multiplier`

Use decimal/fixed-point accounting; never binary floating point for financial balances. Store rate policy version and calculation evidence. Make time, hardware, and policy changes auditable.

### 7.3 Ledger requirements

- Immutable double-entry or balanced ledger design: `credit_accounts`, `credit_transactions`, `credit_entries`, `credit_adjustments`.
- Every settlement references `job_id`, `attempt_id`, `device_id`, `session_id`, policy version, verified units, and unique settlement key.
- Transactional unique constraint prevents double settlement on retries/replayed events.
- Pending → confirmed → reversed/expired states with reason codes and audit events.
- Credits ledger is separate from external payment/payout providers; MVP includes no real-money payments.
- Manual adjustment requires role authorization, reason, and complete audit trail.

### 7.4 Example six-hour session

User volunteers 6 hours. Jobs occupy 4 hours, of which 3.5 hours are verified and eligible. At 20 credits per verified compute-hour, contributor earns 70 credits, **not** 120 credits for availability alone. All values illustrative.

---

## 8. Identity, tenancy, and white labeling

### 8.1 Tenant model

Each company runs an **independent deployment**, database, signing authority, gateway, device registry, workload pool, and ledger. Within one company deployment, optional organizations/projects can have RBAC and quotas; this is not cross-company shared compute.

### 8.2 Authentication and authorization

- OIDC Authorization Code + PKCE for desktop login through system browser.
- Short-lived access tokens; secure refresh rotation or device-bound sessions; no password collection by the framework when federated OIDC is used.
- Service-to-service mTLS or short-lived service credentials.
- RBAC: `operator_admin`, `operator_support`, `auditor`, `workload_submitter`, `contributor`.
- Device enrollment with proof-of-possession key, revocation, and separate device/session identity.
- Authorization checks on all APIs; contributors can only read their own devices/jobs/credits.

### 8.3 Branding configuration

Operator-controlled name, logo, colors, links, credit unit label, OIDC endpoints, gateway domain, app icon, installer metadata, and update channel. Configurations are signed; secrets never embedded in installers. Supply a brandable UI kit and documented build pipeline.

### 8.4 Operator plugin interfaces

- `IdentityProviderAdapter` (OIDC baseline)
- `CreditPolicyProvider` (calculation, limits, redemption metadata)
- `WorkloadRunner` (allowlisted runner first)
- `ArtifactStore` (S3-compatible)
- `NodePlacementPolicy` (scheduler strategy)
- `EventSink` (observability/webhooks)
- Future `PayoutAdapter` optional; not required for the credits-only product.

Plugin SDK must version interfaces, validate configuration, isolate failures, and document compatibility.

---

## 9. API contract (initial)

Use OpenAPI 3.1 as source of truth; generate TypeScript and Go clients. All writes support request IDs and appropriate idempotency keys. Use cursor pagination and structured error codes. Endpoints below are indicative; finalize schemas before implementation.

### Contributor/device

- `POST /v1/devices/enroll` — pair authenticated contributor and device public key.
- `GET /v1/devices/me` — hardware, capability, health, status.
- `PATCH /v1/devices/{id}/policy` — update owner-approved resource policy.
- `POST /v1/availability-sessions` — duration and policy snapshot.
- `POST /v1/availability-sessions/{id}/stop` — stop immediately.
- `GET /v1/availability-sessions/{id}` — timer, state, summary.
- `GET /v1/contributor/jobs` — redacted job/activity history.
- `GET /v1/contributor/credits` — balances and policy.
- `GET /v1/contributor/credit-transactions` — ledger entries.

### Workloads/operator

- `POST /v1/jobs` — submit allowlisted workload and requirements.
- `GET /v1/jobs/{id}` — job state, attempts, validation, result metadata.
- `POST /v1/jobs/{id}/cancel` — cancel.
- `GET /v1/operator/devices` — device fleet.
- `GET /v1/operator/jobs` — queue and history.
- `GET /v1/operator/metrics` — aggregated metrics.
- `GET /v1/operator/audit-events` — privileged audit.
- `POST /v1/operator/credit-adjustments` — authorized audited adjustments.

### Agent control channel

Authenticated bidirectional protocol: `HELLO`, `CAPABILITIES`, `HEARTBEAT`, `SESSION_STATE`, `JOB_OFFER`, `JOB_ACCEPT`, `JOB_REJECT`, `JOB_START`, `JOB_PROGRESS`, `JOB_COMPLETE`, `JOB_FAIL`, `JOB_CANCEL`, `POLICY_UPDATE`, `CREDIT_SETTLEMENT_NOTICE`, `DRAIN`, `ACK`. Every message includes protocol version, device ID, monotonic sequence, trace ID, and applicable lease epoch. Prefer protobuf or versioned JSON schemas; reject unknown critical fields.

### Example job request

```json
{
  "idempotency_key": "client-job-001",
  "workload_type": "synthetic-hash-v1",
  "input_artifact_id": "artifact-public-test-001",
  "requirements": {"cpu_cores": 2, "ram_mb": 1024, "gpu_required": false},
  "timeout_seconds": 300,
  "max_attempts": 3,
  "verification_policy": "expected-output-hash"
}
```

---

## 10. Data model (PostgreSQL)

Create migrations and constraints for:

- `operators_config` — single deployment branding, policy version, feature flags.
- `users` — external subject ID, role mapping, consent status.
- `devices` — owner ID, public key fingerprint, capabilities, agent version, revocation state.
- `device_capability_snapshots` — time-versioned hardware and supported runtimes.
- `device_policies` — owner limits and revision.
- `availability_sessions` — start, expiry, stop, consent/policy snapshot, session state.
- `device_presence` — durable last seen; Redis stores ephemeral connection mapping.
- `jobs` — type, immutable payload ref, requirements, state, submitter, deadlines, idempotency key.
- `job_attempts` — job, device, session, lease epoch, state, timestamps, output ref, error.
- `job_events` — append-only state transitions and trace metadata.
- `artifacts` — content hash, storage ref, classification, retention, size, signature.
- `verification_results` — policy, result, verifier evidence, timestamps.
- `usage_records` — attempt, measured intervals, validated units, evidence refs.
- `credit_accounts` — owner and currency/credit-unit code.
- `credit_transactions` / `credit_entries` — balanced immutable journal and settlement key.
- `credit_policies` — rate version, hardware multipliers, validity windows.
- `audit_events` — actor, action, object, reason, correlation, hash chain if enabled.
- `agent_release_channels` — signed release manifests, minimum supported versions.

**Constraints:** FK integrity; check constraints on nonnegative units; unique device public keys; unique job idempotency key per submitter; unique settlement key per accepted billable unit; optimistic concurrency/version columns; UTC timestamps; retention/partitioning strategy. Protect personal data and avoid recording sensitive workload payloads in logs.

---

## 11. Monorepo structure

```text
opencompute/
├── apps/
│   ├── company-console/          # Next.js operator UI
│   ├── contributor-desktop/      # Tauri + web UI
│   └── contributor-web/          # optional account/history web portal
├── services/
│   ├── api/                      # Fastify public/operator API
│   ├── gateway/                  # authenticated outbound agent connections
│   ├── scheduler/                # Go scheduling + leases
│   ├── job-service/              # job state, artifacts, verification
│   ├── metering/                 # validated usage
│   └── credits/                  # ledger and policy
├── agents/
│   └── node-agent/               # Go local daemon, local policies, telemetry
├── runtimes/
│   ├── synthetic-runner/         # safe allowlisted MVP executor
│   └── linux-vm-runner/         # future, gated by security review
├── packages/
│   ├── protocol/                 # protobuf/JSON schema and generated SDKs
│   ├── plugin-sdk/               # operator extension points
│   ├── ui-kit/                   # theme/branding components
│   ├── config/                   # validated config schema
│   └── sdk-typescript/           # workload client
├── plugins/
│   ├── identity-oidc/
│   ├── credits-fixed-rate/
│   └── storage-s3/
├── deploy/
│   ├── compose/
│   ├── helm/                     # later
│   └── terraform/                # later
├── tests/
│   ├── contract/
│   ├── integration/
│   ├── end-to-end/
│   ├── chaos/
│   └── security/
├── docs/
│   ├── architecture/
│   ├── adr/
│   ├── operator-guide/
│   ├── contributor-guide/
│   ├── api/
│   ├── threat-model.md
│   └── troubleshooting.md
├── .github/workflows/
├── LICENSE
├── CONTRIBUTING.md
├── SECURITY.md
└── README.md
```

Avoid splitting into excessive deployable microservices initially. Keep a **modular Fastify API** and one Go scheduler/agent. Separate services only when operational boundaries justify it.

---

## 12. Build milestones and coding-agent work orders

### M0 — Foundations (target 1–2 weeks)

**Tasks:** Initialize monorepo, package manager, Go modules, TS strict mode, formatting/lint, test runners, CI, local Compose for Postgres/NATS/Redis/MinIO/API, environment schema, OpenAPI skeleton, ADRs, license/contributing/security templates.

**Acceptance:** `make dev` starts stack; `make test` passes; `/healthz` and `/readyz` distinguish health/readiness; no embedded secrets; a clean clone can reproduce setup.

### M1 — Company platform + device identity (target 2–3 weeks)

**Tasks:** OIDC integration with local dev identity provider, roles, device enrollment/public keys, gateway connection, capabilities, heartbeats, operator device list, audit events, contributor consent API.

**Acceptance:** Two users have isolated access; device enrollment is authenticated; revoked device cannot reconnect; NATed Linux agent connects outbound; operator sees accurate presence.

### M2 — Contributor session + safe compute (target 2–3 weeks)

**Tasks:** Go agent daemon/CLI, local duration timer, policy enforcement, idle detection abstraction, synthetic allowlisted executor, scheduler filters, leases, job submission API, results, basic contributor UI and activity log.

**Acceptance:** User chooses six hours; a synthetic job runs; stop immediately blocks new work and cancels local execution; session expires offline; a device without public IP participates.

### M3 — Reliability and credits (target 2–4 weeks)

**Tasks:** Durable state machine, retry/DLQ, lease fencing, reconnect reconciliation, deterministic output verification, metering evidence, fixed-rate credit policy, immutable ledger, contributor credit history, chaos tests.

**Acceptance:** Kill a worker mid-job and observe retry on another; duplicate events never double-credit; pending vs confirmed balances correct; invalid results not credited; all transitions auditable.

### M4 — White-label distribution (target 2–3 weeks)

**Tasks:** Brand configuration, signed installer configuration, theme kit, per-operator OIDC/credit settings, release signing pipeline, updater verification, operator install guide, contributor install guide, two independent sample deployments.

**Acceptance:** Company A and B run fully isolated instances with different branding/identity/credit names; agents are bound to intended operator; no central author service required.

### M5 — Hardened runtime + GPU research (timeline TBD; security gate)

**Tasks:** Linux VM runner, egress controls, sandbox threat-model tests, GPU capability matrix, GPU isolation prototypes, energy/thermal policy, workload artifact scanning, review by independent security experts.

**Acceptance:** Documented supported host/driver/runtime matrix; verified isolation boundaries; hostile workload tests; no public arbitrary GPU job support until security gate passes.

### M6 — P2P artifacts + production operations (timeline TBD)

**Tasks:** Optional P2P content distribution, immutable artifact cache, peer identity and authorization, relay fallback, benchmarks, HA deployment, observability dashboards, upgrade/backup/restore, retention and incident response.

**Acceptance:** P2P mode disabled by default; baseline HTTPS always works; benchmark shows whether P2P improves total cost; operator can backup and restore state; security controls preserved.

**Milestone rule:** Do not proceed to the next stage if acceptance tests fail. Document known limitations and manual operator steps.

---

## 13. Acceptance tests and measurable success criteria

### 13.1 Core E2E

1. Deploy Company A locally; configure branding and OIDC.
2. Install agent on Linux PC behind ordinary home NAT; authenticate.
3. Choose six-hour availability with CPU/RAM caps.
4. Submit 100 deterministic safe jobs; verify completed outputs.
5. Stop sharing; ensure new jobs are rejected and local running job is stopped.
6. Restart sharing; disconnect device mid-job; observe lease expiry and reassignment.
7. Replay duplicate job-completion and credit events; confirm no double settlement.
8. View matching job history, resource telemetry, usage, and credit records in UI.
9. Expire a session while offline; verify no further work runs.
10. Deploy Company B separately; verify no cross-company data, device, or account access.

### 13.2 Scale/chaos demo

- 10 participating nodes (physical or appropriately modeled), 1,000 independent safe jobs, simulated 30% node interruptions.
- **Target:** ≥99% eventual job completion within a defined test deadline, conditional on sufficient remaining capacity and retry budget.
- Report p50/p95 queue wait, job latency, reconnect time, recovery time, verification failures, duplicate settlement count (must be zero), data transferred, and per-completed-job infrastructure cost.
- This is a test target, not a production SLA or promise of cloud-equivalent performance.

### 13.3 Security tests

- Unauthorized device enrollment/revocation; cross-user IDOR; stale/replayed leases; invalid signed config; malicious artifact hash; forged usage; fake device heartbeat; local expiry offline; user Stop during network partition; compromised agent token; agent downgrade; log secret redaction.
- Fuzz protocol parser; test resource caps and runaway job termination; verify all audit-sensitive actions have actor and correlation IDs.

---

## 14. Observability, logs, and operational dashboards

### Agent-visible events

`AGENT_STARTED`, `LOGIN_SUCCEEDED`, `CONSENT_ACCEPTED`, `DEVICE_REGISTERED`, `SESSION_STARTED`, `HEARTBEAT_OK`, `JOB_OFFERED`, `JOB_ACCEPTED`, `JOB_STARTED`, `JOB_PROGRESS`, `JOB_FINISHED`, `JOB_VERIFIED`, `CREDITS_PENDING`, `CREDITS_CONFIRMED`, `USER_ACTIVE_PAUSE`, `THERMAL_PAUSE`, `NETWORK_LOST`, `SESSION_EXPIRED`, `USER_STOPPED`, `AGENT_UPDATED`.

### Operational metrics

Active/available/busy nodes, usable GPU VRAM, job queue age, offer acceptance, task completion, retry ratio, lease expiry, verification failures, credit liability, latency percentiles, artifact bandwidth, relay egress, temperature-related pauses, protocol errors, and security alerts.

### Logging principles

Structured JSON, UTC timestamps, correlation/trace IDs, device/job/session IDs, redacted tokens and payloads, contributor-friendly descriptions, retention configuration, audit export. Company operators see operational logs; contributors see their own activity and credit evidence. Avoid showing confidential workload content to contributors or storing personal files in telemetry.

---

## 15. Deployment, operations, and self-hosting

- Development: one-command Docker Compose with example `.env`, MinIO, NATS, PostgreSQL, Redis, identity dev server, dashboards.
- Production: HTTPS reverse proxy, secrets manager, database backup/restore, migration tooling, per-service health checks, resource quotas, monitoring/alerting, upgrade/rollback instructions, incident response.
- Helm charts and Terraform are later deliverables; no hard dependency on AWS, Azure, GCP, or author-managed infrastructure.
- Operators must provide domain, TLS, OIDC configuration, artifact storage, signed installer/update pipeline, legal terms, privacy policy, support channel, and credit policy.
- Document network egress requirements, firewall/proxy support, host compatibility, storage footprint, and bandwidth cost.
- Provide example configuration for two distinct fictitious organizations and a documented migration/backup path.

---

## 16. Open-source governance and commercialization

- Target Apache-2.0 for broad commercial reuse and white-labeling; obtain counsel review for patents and dependency licenses. Apache-2.0 does not grant rights under third-party patents.
- Repository includes `LICENSE`, `NOTICE` if needed, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, release policy, contributor DCO/CLA decision, and versioned API compatibility policy.
- A company can modify and commercialize the platform under the license terms; no requirement to share revenue with project author.
- Potential project maintainer revenue: optional managed deployment, support, enterprise integration, security review, and training; **not** required for use.
- Microsoft's reported edge-GPU training patent should be independently reviewed before implementing any claim-specific training partitioning/aggregation. Build a general job execution framework without copying patented claim combinations.
- Organization operators remain responsible for user consent, consumer protection, energy/electricity disclosure, privacy, data residency, taxes, credits redemption, and applicable local laws.

---

## 17. Key engineering tradeoffs and risks

| Decision | Benefit | Cost / limitation | Initial choice |
|---|---|---|---|
| Central control plane per operator | Auditable, simpler governance | Company-hosted SPOF unless HA | Yes |
| Outbound agent gateway | Works behind NAT/CGNAT | Gateway traffic/cost | Yes |
| Direct P2P transfers | Less central bandwidth for large artifacts | NAT failures, abuse, security complexity | Later |
| CPU-first synthetic jobs | Safe and testable MVP | Not yet a commercial GPU product | Yes |
| Consumer Windows GPU execution | Large contributor pool | Major isolation/driver/VM risk | Research later |
| At-least-once jobs | Recoverable under churn | Must handle duplicate attempts | Yes |
| Verified compute billing | Fairer and fraud resistant | Verification overhead | Yes |
| Fixed-rate credits | Simple operator integration | Hardware fairness and valuation challenges | MVP |
| Company-owned deployment | White-label independence | Each operator must run infra | Yes |
| Model training across home PCs | Potential high capacity | Latency, bandwidth, confidentiality, staleness, patents | Not v1 |

### Top unresolved technical risks

1. Strong isolation for arbitrary GPU jobs on consumer Windows systems.
2. Preventing extraction of proprietary model weights/data from untrusted PCs.
3. Fraud-resistant proof of arbitrary computation without costly duplication.
4. Heterogeneous GPU driver/runtime support and usable VRAM constraints.
5. Residential bandwidth, CGNAT, relay economics, and upload bottlenecks.
6. Electricity, wear, user experience, and credits that are worth contributing for.
7. Availability churn, thermal limits, and mismatch between volunteered hours and real billable work.
8. Safe automatic updates and company-specific software signing.
9. Credits legal/accounting treatment in each operator's jurisdiction.
10. Patent clearance for specialized AI training techniques.

---

## 18. Decision records to create before coding

- ADR-001: Centralized operator control plane vs global P2P control plane.
- ADR-002: Outbound gateway protocol and reconnect semantics.
- ADR-003: Job lease/fencing and idempotent state machine.
- ADR-004: Safe allowlisted MVP runner and isolation roadmap.
- ADR-005: Credit ledger, verified usage, and policy versioning.
- ADR-006: OIDC PKCE + device identity and token rotation.
- ADR-007: Operator branding and signed configuration.
- ADR-008: PostgreSQL vs Redis authority boundaries.
- ADR-009: Artifact security and content-addressed storage.
- ADR-010: Windows/macOS/Linux compatibility and feature gating.

---

## 19. First autonomous coding-agent execution prompt

> You are the lead engineer implementing OpenCompute. Read `OPENCOMPUTE_MASTER_BUILD_PLAN.md` fully. Build only **M0 and the smallest secure vertical slice of M1** first. Inspect the repository, create a milestone checklist and ADRs, then initialize the documented monorepo. Implement a Docker Compose stack (Fastify API, PostgreSQL, Redis, NATS JetStream, MinIO, local OIDC development identity provider), `/healthz` and `/readyz`, validated environment config, OpenAPI skeleton, a Go agent that can register/connect outbound using development credentials, and a basic operator device-list page. Add unit/integration tests, CI, README, setup guide, and threat-model starter. Use mock/synthetic compute only. Do not implement arbitrary code execution, real payouts, GPU passthrough, or P2P yet. Run tests and report what works, what is mocked, security gaps, and exact commands to reproduce. Then propose the next small PR for M1/M2; do not claim production readiness.

### Definition of done for every pull request

- Source compiles; format/lint/typecheck/tests pass.
- New behavior has automated tests and documented error paths.
- API/protocol/schema changes are versioned and migration-safe.
- Security/privacy implications and threat-model changes are noted.
- UI shows real state or explicitly labels simulated state.
- Operator and contributor controls are understandable and auditable.
- README/docs updated with exact run commands.
- No secrets or signing keys committed.
- PR summary lists limitations and follow-up work.

---

## 20. Final target demonstration

**Two separate organizations deploy the same open-source project under different brands.** Each organization configures its own identity provider, control plane, gateway, policies, credits, and signed contributor installer. A user installs one organization's contributor app, signs in, offers six hours of capacity, sees live jobs and logs, and receives credits only for verified work. Jobs recover from disconnections without duplicate credits. No contributor needs a public IP or DNS record. Neither deployment depends on infrastructure operated by the framework author.

**That is the product.** GPU support, production isolation, scale, and advanced P2P are explicit subsequent security-gated engineering milestones—not assumptions hidden inside the MVP.
