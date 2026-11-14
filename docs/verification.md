# Verification evidence — identity and synthetic compute preview

Date: 2026-11-14. Source and full-stack acceptance passed on [implementation commit a996ef4](https://github.com/knight099/computeshare/actions/runs/43640458682). The earlier [M1 gate](https://github.com/knight099/computeshare/actions/runs/43636374418) passed before M2 implementation began.

| Check                                    | Observed result                                                                                                                                    |
| ---------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| Lint / formatting / strict typecheck     | Passed locally and in CI                                                                                                                           |
| Unit tests                               | 4 named tests passed                                                                                                                               |
| Real PostgreSQL integration tests        | 19 named tests passed                                                                                                                              |
| Go tests with race detector              | 11 named tests passed                                                                                                                              |
| Go vet / formatting / Linux binary build | Passed                                                                                                                                             |
| Next.js production build                 | Passed                                                                                                                                             |
| Clean Compose startup                    | Passed in CI with PostgreSQL, Redis, NATS, authenticated S3, Keycloak, API and console                                                             |
| Browser authentication                   | Real Chromium PKCE/CORS, authenticated fleet/job submission, rejected state and blocked token exchange passed                                      |
| Native identity / connection             | Real Keycloak CLI login, enrollment, heartbeat, revocation and reconnect after gateway restart passed                                              |
| Contributor sharing                      | Real local browser six-hour consent/session and resource selection passed                                                                          |
| Synthetic compute                        | 100 real built-in jobs completed through constrained worker processes; server verified result hashes                                               |
| Resource evidence                        | Measured positive CPU usage and peak RAM within selected 64 MB cap; acceptance also reads back CPU/memory/swap/pids kernel configuration           |
| Stop under partition                     | Company API paused while a worker ran; local Stop terminated it within two seconds and prevented new execution                                     |
| Local expiry                             | Boot-clock, wall-clock rollback, reboot, offline cancellation and persisted non-resumption covered by Go tests; no six-hour real-time wait claimed |
| Dependency outage                        | Liveness survived Redis and PostgreSQL shutdown; readiness reported unavailable dependencies                                                       |

Integration tests use isolated PostgreSQL schemas and signed RSA/JWKS identity fixtures. Real Keycloak/browser/cgroup acceptance is a separate CI job; no mock service or ordinary directory substitutes for enforced cgroups. Local Docker VFS remains constrained by the workspace's 8 GB disk; the complete stack was verified on CI instead.

## Review fixes

A separate reviewer found four Important M2 issues. Fixed regressions cover cancellation not being overwritten by another offer, expired sessions/leases becoming terminal without reconnect, consistent transaction ordering for cancellation/completion, and Stop overriding pending Start including CLI Stop. All preview compute mutations now take one PostgreSQL advisory transaction lock before row locks. This deliberately limits throughput; it is not an HA scheduler claim.

M1 additions cover atomic capability-change history without unchanged-heartbeat duplicates and scheduled removal of only expired credentials/challenges. Registry, consent and audit history are preserved.

## Remaining gates and review minors

This is the initial M2 CPU-only vertical slice, not the complete product. Separate Go scheduling/NATS outbox delivery, SDK/artifacts, broader power/thermal/idle policies and full contributor diagnostics remain M2 follow-ups. Reliability/retries and credits are M3; signing, distribution, reviewed VM/GPU execution and P2P are later stages.

Deferred review minors: parent-death/stale-cgroup cleanup on SIGKILL, chronological contributor history beyond the 200-row preview cap, and moving bounded synchronous hash verification off the API event loop. A crashed agent's bounded trusted worker can finish under its existing cgroup limits; dedicated crash acceptance is still needed. No hostile allocator, arbitrary-code isolation, production signing, fraud-proof compute, or credit-settlement claim is made.

The development storage substitution (SeaweedFS for unavailable MinIO images) remains documented in ADR-009. Earlier broad Docker cleanup was rejected by automatic review; only specifically identified task resources were cleaned up, preserving active services and data volumes.
