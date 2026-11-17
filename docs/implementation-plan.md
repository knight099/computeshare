# Full staged implementation plan

Source of truth: [master specification](../OPENCOMPUTE_MASTER_BUILD_PLAN.md). This checklist records delivery, not promises of completed features. Each milestone's acceptance tests must pass before advancing. M5 arbitrary-runtime enablement additionally requires independent security review.

## M0 — Foundation

- [x] Strict TypeScript workspace, Go module, pinned dependency lockfile, lint/format/typecheck/unit-test commands.
- [x] Modular Fastify API, distinct health/readiness, validated configuration with redacted failures.
- [x] Transactional repeatable migrations with deployment identity binding.
- [x] Compose definitions for database, Redis, durable NATS, S3, local OIDC, API and console.
- [x] Initial ADR-001–010, threat model, security/contribution/license files, setup and API documentation.
- [x] CI definitions for source checks, integration tests, agent tests, builds, Compose smoke and enrollment E2E.
- [x] Gate: clean-clone `make dev`, full Compose acceptance, source checks and native enrollment E2E passed in [GitHub Actions](https://github.com/knight099/computeshare/actions/runs/43633202994). Local Docker VFS remains disk-limited; CI provides the acceptance evidence.

## M1 — Identity and device registry

- [x] OIDC issuer/audience/expiry/signature verification and operator/contributor role boundaries.
- [x] PKCE browser sign-in for console and Linux CLI; explicit versioned enrollment consent.
- [x] Ed25519 registration proof, owner-scoped device APIs, unique device keys.
- [x] Operator-bound single-use challenge exchange, hashed short-lived device credentials.
- [x] Outbound gateway, capability inventory, 15-second heartbeat, replay/identity validation, reconnect backoff, revocation and audit records.
- [x] Operator device-list UI and contributor owner-scoped enrollment/device views.
- [x] Database-backed security tests for two-user isolation, consent, revocation, protocol and challenge replay.
- [x] Initial native CLI/provider/registry E2E and revocation passed against the real Compose stack in CI.
- [x] Full gate: Chromium console PKCE/CORS/state failure checks and gateway restart/reconnect passed in CI run 43636374418.
- [x] Scheduled credential/challenge retention, atomic capability-change snapshots, and documented manual key replacement.
- [ ] Follow-up: administrator-assisted revocation/key rotation and production proxy/IPv6/CGNAT matrix.

## M2 — Contributor sessions and safe compute

Initial CPU-only vertical slice implemented; full M2 remains incomplete. See ADR-011. The CPU slice passed source and full-stack CI on a996ef4 (run 43640458682), including browser controls, 100 constrained jobs and Stop during a network partition. Separate Go scheduler, TypeScript SDK/artifacts, full policy adapters, diagnostics and complete screen coverage remain follow-ups.

- [x] Availability sessions with 1/2/4/6/8/12-hour presets, immutable policy and consent snapshots; no automatic renewal.
- [x] Local Stop, boot-clock/offline expiry, reboot non-resumption, and connection-loss cancellation.
- [ ] Idle/power/thermal policy adapters and full platform transition matrix.
- [x] Enforced worker CPU/RAM/swap/pids limits. Unsupported GPU/idle/custom resource controls are rejected; no workload files or network destinations.
- [ ] Broader disk/network/power/thermal policy controls.
- [ ] Go scheduler: deterministic eligibility, remaining-time safety margin, atomic capacity reservation, offer/accept/reject, leases and epochs.
- [x] Allowlisted bounded public CPU hash worker; no scripts, arbitrary binaries, shell commands, images or GPU access.
- [ ] Additional reviewed public-data transforms.
- [x] Transactional job-event outbox with leased delivery into operator-scoped durable JetStream events; stable message IDs, bounded stream retention and retry after failures.
- [x] Parent-death worker termination, empty owned-cgroup cleanup, scoped chronological history pagination, bounded verification outside the API event loop and compute transaction locks.
- [ ] TypeScript SDK, artifact integrity and expanded result interfaces.
- [ ] Contributor Home/settings/jobs/activity/diagnostics/privacy screens, resource telemetry and redacted job history.
- [x] Initial CPU gate: six-hour session creation, 100 synthetic jobs, cgroup limits, offline Stop during partition, outbound/NAT baseline, and local boot-clock/expiry tests.
- [ ] Full M2 gate remains open for the follow-ups above; no six-hour wall-clock soak or complete platform-policy certification claimed.

## M3 — Reliability, verification and simulated credits

- [ ] Durable job/attempt transitions, bounded retry/backoff, dead-letter state and lease fencing.
- [ ] Reconnect reconciliation and immutable artifact/result association; deterministic server-owned verification vectors.
- [ ] Evidence-based usage records, availability/allocation/execution/verified-unit separation.
- [ ] Versioned fixed-point credit policies, balanced immutable journals, settlement uniqueness, pending/confirmed/reversal history.
- [ ] Contributor statements and credit screens; operator policy/export and audited dual-approved adjustments.
- [ ] Gate: worker death and reassignment, delayed/stale events, duplicates with zero duplicate settlements, invalid/forged usage rejection, ledger reconciliation, complete audit trail.

## M4 — White-label products and distribution

- [ ] Operator configuration/theme/logo/support/legal links/credit naming and approved-workload policy.
- [ ] Console overview/jobs/workload policies/identity/access/credits/security administration, complete RBAC including submitter and auditor.
- [ ] Contributor Tauri shell, separate installer artifacts and platform capability gating.
- [ ] Operator-bound signed configuration/installers/manifests, update verification, release revocation, downgrade protection and rollback.
- [ ] OS-backed credential storage, rotated device identities, company-specific signing workflow, SBOM/dependency scans and reproducibility guidance.
- [ ] Independent Company A/B examples, account/device deletion and retention guides, download/checksum distribution.
- [ ] Gate: separate branded deployments with distinct identities, data, keys and credit policies; tampering/retargeting rejected; no central service.

## M5 — Reviewed runtime and GPU research

- [ ] Linux VM/microVM runtime, no host filesystem or credentials, resource/disk limits, denied-by-default egress.
- [ ] Artifact scanning/signatures, hostile workload tests, sandbox escape threat model and independent security review.
- [ ] Energy/thermal controls and verified host/driver/runtime compatibility matrix.
- [ ] GPU isolation prototypes; only reviewed supported entries can enable public jobs.
- [ ] Gate: documented review evidence and hostile-workload tests; failed or absent review keeps arbitrary/GPU execution disabled.

## M6 — Optional P2P and production operations

- [ ] Explicitly opt-in P2P for authorized public immutable artifacts, peer identity, content-addressed cache, corruption rejection and HTTPS/relay fallback.
- [ ] Transfer/relay economics benchmarks; no sensitive payloads distributed by default.
- [ ] HA gateway/scheduler operations, shared presence/revocation, fleet rate limits, Helm/Terraform and HTTPS deployment examples.
- [ ] OpenTelemetry, Prometheus/Grafana and structured log dashboards; retention, redacted diagnostic exports, alerts and incident response.
- [ ] Migration/upgrade/rollback procedures, tested database/object backup and restore, release/API compatibility policies.
- [ ] Gate: 10-node/1,000-job test with 30% interruptions, conditional ≥99% completion, zero duplicate settlements, latency/recovery/bandwidth/cost report, restored state verified.

## Requirements coverage

| Master sections                                                  | Owning stages                                |
| ---------------------------------------------------------------- | -------------------------------------------- |
| 0–1: product boundaries and independent company ownership        | All; ADR-001                                 |
| 2: onboarding, availability and all product screens              | M1–M4                                        |
| 3–4: architecture, outbound networking, P2P and failure handling | M0–M3, M6                                    |
| 5–6: scheduling, recovery, isolation, supply chain               | M2–M5                                        |
| 7: metering, verification and credits                            | M3                                           |
| 8–10: identity, white-labeling, APIs, protocol, schema           | M0–M4                                        |
| 11–12: monorepo and stage acceptance                             | All                                          |
| 13–15: tests, observability, self-hosting and operations         | All; scale/HA in M6                          |
| 16–18: governance, compatibility, risks and ADRs                 | M0, M4–M6; review gates retained             |
| 19: first autonomous delivery                                    | M0 / initial M1                              |
| 20: two-company final demonstration                              | M4 synthetic baseline; M5/M6 gated additions |

## Decisions and progress ledger

- Approved approach: full staged program; implement sequentially and never represent future stages as complete.
- Ruling: resolve the section 6.2/M5 timing conflict in favor of the milestone list. M3 remains synthetic-only; arbitrary-runtime review occurs in M5. Cost: isolated real workloads arrive later.
- Ruling: use maintained SeaweedFS S3 for local storage after official MinIO references failed. Cost: different development storage administration; S3 compatibility must be tested, and upstream licenses remain separate.
- Ruling: Linux enrollment only in the initial agent, with no declared executable runtime. Cost: Windows/macOS enrollment waits for tested adapters.
- Review focus: cross-user access, revoked/expired credentials, replayed proofs/messages, state-file exposure, failure of a dependency while liveness stays healthy.

## Next small delivery

Complete M1 browser/reconnect acceptance before starting compute: add a real-browser console sign-in test (including CORS/state failure paths), exercise agent reconnect after gateway restart, capture capability revisions, and add scheduled credential/challenge retention. Require existing source/Compose tests plus the new browser/reconnect tests to pass. Then begin M2 with locally enforced availability and Stop/expiry tests, before allowing synthetic job dispatch.

## Latest execution record

M1 completion and initial M2 CPU slice are in PR #1. Current implementation evidence: CI 43640458682 on a996ef4, 4 unit + 19 PostgreSQL integration + 11 Go tests, production build, real Chromium/Keycloak/cgroup flow.

Ruling: local Go-served contributor controls keep Stop independent of company connectivity; expanded Next.js/desktop presentation remains later. Ruling: initial FIFO/leases stay in the modular API, and one advisory transaction lock orders compute mutations; this trades throughput for simple, tested cancellation ordering. Standalone Go scheduling and durable queue delivery remain explicit follow-ups.

Deferred review minors are listed in docs/verification.md and docs/threat-model.md: crash cleanup/parent-death handling, chronological history pagination, and asynchronous known-result verification.
