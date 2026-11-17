# Verification evidence — identity and synthetic compute preview

Date: 2026-11-17. Latest verified implementation: [usage commit 8ca23b5](https://github.com/knight099/computeshare/actions/runs/44181102875), with source and complete Compose acceptance passing. [Database-outage recovery 3d4a762](https://github.com/knight099/computeshare/actions/runs/44180788407) also passed. Earlier M1 and CPU-preview gates remain linked in the implementation history.

| Check                                    | Observed result                                                                                                                                                     |
| ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Lint / formatting / strict typecheck     | Passed locally and in CI                                                                                                                                            |
| Unit tests                               | 11 named tests passed                                                                                                                                               |
| Real PostgreSQL integration tests        | 25 named tests passed                                                                                                                                               |
| Go tests with race detector              | Race tests passed, including real supervisor SIGKILL                                                                                                                |
| Go vet / formatting / Linux binary build | Passed                                                                                                                                                              |
| Next.js production build                 | Passed                                                                                                                                                              |
| Clean Compose startup                    | Passed in CI with PostgreSQL, Redis, NATS, authenticated S3, Keycloak, API and console                                                                              |
| Browser authentication                   | Real Chromium PKCE/CORS, authenticated fleet/job submission, rejected state and blocked token exchange passed                                                       |
| Native identity / connection             | Real Keycloak CLI login, enrollment, heartbeat, revocation and reconnect after gateway restart passed                                                               |
| Contributor sharing                      | Real local browser six-hour consent/session and resource selection passed                                                                                           |
| Synthetic compute                        | 100 real built-in jobs completed via the authenticated separate Go scheduler and constrained workers; server verified hashes and 100 unique technical usage records |
| Resource evidence                        | Measured positive CPU usage and peak RAM within selected 64 MB cap; acceptance also reads back CPU/memory/swap/pids kernel configuration                            |
| Stop under partition                     | Company API paused while a worker ran; local Stop terminated it within two seconds and prevented new execution                                                      |
| Local expiry                             | Boot-clock, wall-clock rollback, reboot, offline cancellation and persisted non-resumption covered by Go tests; no six-hour real-time wait claimed                  |
| Dependency outage                        | Liveness survived Redis and PostgreSQL shutdown; readiness reported unavailable dependencies                                                                        |

Integration tests use isolated PostgreSQL schemas and signed RSA/JWKS identity fixtures. Real Keycloak/browser/cgroup acceptance is a separate CI job; no mock service or ordinary directory substitutes for enforced cgroups. Local Docker VFS remains constrained by the workspace's 8 GB disk; the complete stack was verified on CI instead.

## Review fixes

A separate reviewer found four Important M2 issues. Fixed regressions cover cancellation not being overwritten by another offer, expired sessions/leases becoming terminal without reconnect, consistent transaction ordering for cancellation/completion, and Stop overriding pending Start including CLI Stop. All preview compute mutations now take one PostgreSQL advisory transaction lock before row locks. This deliberately limits throughput; it is not an HA scheduler claim.

M1 additions cover atomic capability-change history without unchanged-heartbeat duplicates and scheduled removal of only expired credentials/challenges. Registry, consent and audit history are preserved.

## Remaining gates and review minors

This is the initial M2 CPU-only vertical slice, not the complete product. Artifacts, broader power/thermal/idle policies and full contributor diagnostics remain M2 follow-ups. Standalone scheduling and a workspace SDK are implemented. Technical usage is implemented; bounded opt-in retries have passed local integration checks and await their full-stack gate. Signing, distribution, reviewed VM/GPU execution and P2P remain later stages. Payments, credits and business management are excluded by the current scope.

The former parent-death, chronological-history and synchronous-verification review minors are addressed in the current increment. Local checks on 2026-11-17 passed strict typecheck/lint, eight Node unit tests, 21 real PostgreSQL integration tests (including real JetStream acknowledgement replay), and Go race tests including SIGKILL of a real supervising process. The outbox test injects a broker publication failure and a database acknowledgement failure, then checks retry against real JetStream; it does not claim a production network outage soak. New full-stack CI acceptance also checks cleanup of real empty owned cgroups while preserving unknown groups. Both source and Compose acceptance passed in [CI run 44175864569](https://github.com/knight099/computeshare/actions/runs/44175864569), including the new real cgroup cleanup check, 100 constrained jobs and offline Stop.

No hostile allocator, arbitrary-code isolation, production signing, fraud-proof compute, or financial-settlement claim is made.

The development storage substitution (SeaweedFS for unavailable MinIO images) remains documented in ADR-009. Earlier broad Docker cleanup was rejected by automatic review; only specifically identified task resources were cleaned up, preserving active services and data volumes.

Independent review of this increment found no Critical or Important issues. A minor legacy-job prefetch race across overlapping connections was hardened by prefetching active attempt states while preserving locked validation.

## Core technology continuation on main

PR #1 was merged as 0d4c383 after its head CI passed, at the owner's explicit request. Main is the authorized development branch. The engineering tracker is the mandatory pre-task index for scope, module ownership, SOLID boundaries, duplication checks, performance limits and evidence.

Scheduler/SDK implementation 8db2d16 passed full CI 44179371785. A subsequent bounded-candidate starvation regression was reproduced and fixed in 96b33c6. Its full-stack compute checks passed, but database shutdown exposed a checked-out client error crash; a real subprocess regression reproduced exit 1 before the fix and exit 0/reconnected query afterward. The fixed main then passed the full database-outage gate. No failed run is counted as a complete passing gate.

Usage schema 5 passed independent review, 25 integration tests, and full CI 44181102875 with 100 unique records, validated outputs and positive bounded cgroup metrics explicitly marked untrusted. Rejected or plausible host claims never become attestation or financial balances. Logical configured iterations are not proof of physical CPU time.

Recovery schema 6 has local coverage for lease loss, attempt-budget exhaustion, delayed eligibility, local Stop/new consent, higher epochs, stale-result rejection, worker failure retry, terminal invalid output and terminal company cancellation. No Critical/Important review finding remains. Full-stack evidence for this last increment will be recorded after its run completes.
