# Engineering tracker — read before every task

Last updated: 2026-11-17. Owner authorized merge and continued work on main. Scope authority: [product scope](product-scope.md). Delivery gates: [implementation plan](implementation-plan.md). Historical source: [master plan](../OPENCOMPUTE_MASTER_BUILD_PLAN.md).

## Task procedure

1. Read this file and the task's scope. Inspect git status/log; resume completed work instead of recreating it.
2. Search existing domain modules and tests. Record reuse and the single owner of new invariants.
3. Write the smallest behavioral regression/acceptance tests, observe failure, implement, verify.
4. Check SOLID boundaries, bounded resource use, authentication, consent and isolation. Avoid wrappers, frameworks or duplicate logic without a concrete need.
5. Update the ledger, evidence and remaining gates. Commit only verified work; never mark future work complete.

## Existing implementation owners — reuse these

| Responsibility                | Authoritative module / boundary                                                                          |
| ----------------------------- | -------------------------------------------------------------------------------------------------------- |
| Identity / roles              | `services/api/src/auth.ts` (OIDC), `app.ts` enrollment and gateway                                       |
| Configuration / wire schema   | `packages/config`, `packages/protocol`                                                                   |
| Job / session mutations       | `services/api/src/compute.ts`; shared lock ordering in `compute-maintenance.ts`                          |
| Expiry / revocation           | Existing API expiry sweep and device revocation transaction; never add an independent state authority    |
| History pagination            | `services/api/src/job-pages.ts`                                                                          |
| Verification                  | `services/api/src/verifier.ts`, compiled-in algorithm in `hash-worker.mjs` / Go worker                   |
| Durable events                | `services/api/src/outbox.ts`, schema-3 job-event trigger; PostgreSQL authoritative                       |
| Contributor consent / runtime | `agents/node-agent` session manager, cgroup enforcement, parent-death supervision and loopback dashboard |
| Browser / full stack gates    | `tests/end-to-end`, `.github/workflows/ci.yaml`                                                          |

Separate hash implementations are required cross-language verification boundaries; they must share known vectors. Test fixtures are not alternate domain implementations.

## Quality and efficiency register

| Concern                    | Rule / present evidence                                                                                   | Remaining work                                                                                       |
| -------------------------- | --------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| Single responsibility      | Auth, history, verifier, outbox, expiry extracted; Go runtime separate process                            | Extract scheduling boundary while preserving API transaction ownership                               |
| Open/closed + substitution | Add tested runtime/policy adapters behind explicit capability contracts; unsupported controls fail closed | General runtime and policy adapter contracts                                                         |
| Interface segregation      | Narrow workload SDK, scheduler API and usage APIs; no financial interface                                 | Scheduler + SDK next                                                                                 |
| Dependency inversion       | Domain uses injected pool/auth; SDK owns transport boundary, scheduler never owns SQL state               | Keep claim validation in API                                                                         |
| Duplication                | Search owners above before task; one mutation authority and lock convention                               | Audit on each increment                                                                              |
| Performance                | Verifier 2 workers/32 queued/10s deadline; outbox batches10; cursor history limit200; cgroup limits       | Benchmark scheduler/usage, then optimize measured bottlenecks; global compute lock limits throughput |
| Token/work efficiency      | Inline implementation, concise ledger, targeted searches, bounded output, one final independent review    | Do not reread full master plan or rerun unchanged suites without a reason                            |
| Security                   | Public bounded synthetic work only; local Stop and fail-closed cgroups                                    | VM/GPU independent review; production supply chain; cross-platform gates                             |

## Delivery ledger

| ID           | Work                                                                                                     | State / evidence                                                                   | Next action                                                                              |
| ------------ | -------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| BASE         | Foundations, identity, bounded CPU sessions/jobs                                                         | M0/M1 gates passed; initial M2 synthetic slice                                     | Preserve tests                                                                           |
| HARDEN       | Parent death, safe cleanup, async verification, chronological pagination, transactional JetStream outbox | Completed `aba2355`; CI 44175864569 source and full Compose passed; docs `54078c2` | Reuse existing modules                                                                   |
| MERGE        | PR #1                                                                                                    | Merged as `0d4c383` after head CI 44176589763 passed                               | Continue on main as authorized                                                           |
| SCOPE        | Core technology only; exclude all financial/business management                                          | Documented here and product-scope; active plan being aligned                       | No credit ledger tasks                                                                   |
| SCHED        | Authenticated standalone Go scheduler, fresh work intents, atomic API claims                             | Pending                                                                            | Eligibility/ordering tests, service auth, contention and stale-request tests, real stack |
| SDK          | Typed workload client, safe transport, idempotency and pagination                                        | Pending                                                                            | Reuse API protocol; actual API + redirect tests                                          |
| USAGE        | Durable idempotent technical usage records and verification/export                                       | Pending                                                                            | Schema and trust model; replay/forgery/retry tests; no accounting                        |
| RELIABILITY  | Recovery/retries, artifacts, reconnect matrix                                                            | Pending                                                                            | Fenced retries and result integrity; worker/broker/DB interruption tests                 |
| POLICIES     | Idle, power, thermal, resource diagnostics, cross-platform support                                       | Pending                                                                            | Implement/test adapter by platform; unsupported remains disabled                         |
| ISOLATION    | Reviewed general runtime and GPU                                                                         | Blocked by implementation and independent runtime security review                  | Maintain fail-closed gate; do not enable unreviewed workloads                            |
| DISTRIBUTION | Signing, updater, credentials, two companies, scoped admin                                               | Pending                                                                            | Reproducible signed artifacts, tamper tests, company separation                          |
| OPERATIONS   | Observability, HA, deploy/backup/restore/upgrade, scale and benchmarks                                   | Pending                                                                            | Evidence for stated deployment/workload matrix                                           |

No whole-project completion or enterprise-readiness claim is made by this ledger. Entries require actual implementation and passing acceptance evidence.
