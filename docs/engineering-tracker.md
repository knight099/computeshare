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

## Windows/Linux platform owners

- Shared identity, proof, wire protocol, session state, Stop and dashboard remain in the existing Go agent.
- `platform_linux.go` / `platform_windows.go`: inventory and lifetime clock.
- `storage_linux.go` / `storage_windows.go`: private state; Windows DPAPI and validated owner/DACL policy.
- `session_lock_linux.go` / `session_lock_windows.go`: interprocess session lock.
- `runtime_linux.go` / `runtime_windows.go`: execution availability, worker launch/status and direct-worker entry gate. Windows execution is disabled.
- `supervision.go` is Linux-only. Windows resource/supervisor enforcement must have its own tested backend before enabling work.
- [Host support matrix](host-support.md) records the Windows and Linux production gates. Keep this index current before adding another OS-specific implementation.

## Quality and efficiency register

| Concern                    | Rule / present evidence                                                                                   | Remaining work                                                                                       |
| -------------------------- | --------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| Single responsibility      | Auth, history, verifier, outbox, expiry extracted; Go runtime separate process                            | Extract scheduling boundary while preserving API transaction ownership                               |
| Open/closed + substitution | Add tested runtime/policy adapters behind explicit capability contracts; unsupported controls fail closed | General runtime and policy adapter contracts                                                         |
| Interface segregation      | Narrow workload SDK, scheduler API and usage APIs; no financial interface                                 | Private SDK and scheduler passed CI 44179371785; usage acceptance pending                            |
| Dependency inversion       | Domain uses injected pool/auth; SDK owns transport boundary, scheduler never owns SQL state               | Keep claim validation in API                                                                         |
| Duplication                | Search owners above before task; one mutation authority and lock convention                               | Audit on each increment                                                                              |
| Performance                | Verifier 2 workers/32 queued/10s deadline; outbox batches10; cursor history limit200; cgroup limits       | Benchmark scheduler/usage, then optimize measured bottlenecks; global compute lock limits throughput |
| Token/work efficiency      | Inline implementation, concise ledger, targeted searches, bounded output, one final independent review    | Do not reread full master plan or rerun unchanged suites without a reason                            |
| Security                   | Public bounded synthetic work only; local Stop and fail-closed cgroups                                    | VM/GPU independent review; production supply chain; cross-platform gates                             |

## Delivery ledger

| ID           | Work                                                                                                     | State / evidence                                                                      | Next action                                                                     |
| ------------ | -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------- |
| BASE         | Foundations, identity, bounded CPU sessions/jobs                                                         | M0/M1 gates passed; initial M2 synthetic slice                                        | Preserve tests                                                                  |
| HARDEN       | Parent death, safe cleanup, async verification, chronological pagination, transactional JetStream outbox | Completed `aba2355`; CI 44175864569 source and full Compose passed; docs `54078c2`    | Reuse existing modules                                                          |
| MERGE        | PR #1                                                                                                    | Merged as `0d4c383` after head CI 44176589763 passed                                  | Continue on main as authorized                                                  |
| SCOPE        | Core technology only; exclude all financial/business management                                          | Documented here and product-scope; active plan being aligned                          | No credit ledger tasks                                                          |
| SCHED        | Authenticated standalone Go scheduler, fresh work intents, atomic API claims                             | Implemented; source/full CI 44179371785 passed; starvation and DB-loss fixes verified | Retain uniform-workload assumptions until heterogeneous matching is implemented |
| SDK          | Typed workload client, safe transport, idempotency and pagination                                        | Implemented; unit/integration + full-stack source build passed                        | Publishable package and expanded workload APIs remain                           |
| USAGE        | Durable idempotent technical usage records and verification/export                                       | Implemented; full CI 44181102875 passed, including 100 records                        | Maintain untrusted telemetry semantics; no financial logic                      |
| RELIABILITY  | Recovery/retries, artifacts, reconnect matrix                                                            | Bounded built-in retry implementation; local 25 integration tests and review passed   | Full retry CI 44182487190 passed; artifacts and wider failure matrix remain     |
| POLICIES     | Idle, power, thermal, resource diagnostics, cross-platform support                                       | Pending                                                                               | Implement/test adapter by platform; unsupported remains disabled                |
| ISOLATION    | Reviewed general runtime and GPU                                                                         | Blocked by implementation and independent runtime security review                     | Maintain fail-closed gate; do not enable unreviewed workloads                   |
| DISTRIBUTION | Signing, updater, credentials, two companies, scoped admin                                               | Pending                                                                               | Reproducible signed artifacts, tamper tests, company separation                 |
| OPERATIONS   | Observability, HA, deploy/backup/restore/upgrade, scale and benchmarks                                   | Pending                                                                               | Evidence for stated deployment/workload matrix                                  |

No whole-project completion or enterprise-readiness claim is made by this ledger. Entries require actual implementation and passing acceptance evidence.

SDK task: reused existing job API and history contract; zero dependencies; authenticated real API submission/replay/read/cancel and two-server redirect leak test passed. Single transport owner is `packages/sdk-typescript/src/index.ts`; package remains private source distribution pending release packaging.

Scheduler task: reused `lockCompute`, `auth.ts`, and extracted `reserveJob` for both scheduling modes. New single owners: API `scheduling.ts` validates/reserves; Go `matching.go` selects, `client.go` authenticates/transports, `main.go` owns lifecycle/readiness. Schema4 adds bounded-per-device fresh work requests. No extra broker dispatch state or hidden fallback. Test coverage: service role isolation, no intent, stale revision/expiry, deadline safety, concurrent claims, Stop invalidation, FIFO/resource matching, token cache and redirect rejection.

Review fix: candidate caps could hide feasible jobs/devices behind incompatible prefixes. Regression with 128 long jobs and 128 short-lived devices observed failing; candidate snapshot now keeps longest sessions for the current uniform CPU/64MiB workload and filters feasible jobs before FIFO limit. General heterogeneous workloads must replace this optimization with resource-aware bounded matching before enablement.

Review outcome: no further Critical/Important findings. Starvation regression RED→GREEN; all 23 integration tests passed afterward. Current supported workload has uniform resource requirements, and that constraint is explicit in candidate code. No review gate bypass.

Usage task: reused shared history cursor parser (extracted once into `pagination.ts`), existing auth/compute transaction, compiled-in worker cgroup measurements and output verification. `usage.ts` owns recording/query; schema5 enforces one record per job/attempt. Wire telemetry is optional and remains untrusted, with bounds rejection and no financial interpretation. Local 24 integration tests passed: duplicate completion, owner/role isolation, forged bounds, first-observation preservation, invalid-output exclusion and legacy no-report. SDK paging and 100-record real-worker CI assertions added; acceptance pending.

Database outage regression: CI 44179862877 passed browser/reconnect/100 jobs/Stop but failed liveness after PostgreSQL shutdown. Root cause was an unhandled checked-out pg client error between queries (idle pool errors were already handled). A real pg_terminate_backend subprocess reproduced exit 1; a per-connection listener preserves process liveness while subsequent queries reject and release evicts the broken client. Targeted 3 DB tests passed after fix; full-stack rerun required.

Usage final review: no Critical, Important or Minor findings. Local lint/typecheck,11 Node unit / 25 integration and both Go race suites passed. Full-stack CI expanded to assert100 durable records; pending run on committed usage increment.

Recovery task: schema 6 records immutable max_attempts 1..3 (default 1) and backoff. `recovery.ts` is the single owner of interrupted-job transitions, reused by expiry, control loss, local Stop and worker failure; new reservations derive durable epochs. Red missing-column failure preceded implementation. 25 integration tests passed, including budget exhaustion, backoff, Stop/new-session reassignment, stale completion rejection and exactly one accepted usage record. Focused review found no Critical/Important issues; documented semantics and added explicit worker-failure/invalid-output/company-cancel protocol cases as acceptance coverage. Usage 8ca23b5 passed full CI 44181102875 with 100 durable records. DB-outagefix3d4a762 passed full CI 44180788407.

Next task must re-read this tracker: scheduler, SDK, usage, bounded retries and revocation/session fixes are implemented. Revocation passed full CI 44182919710; continue with broader contributor policy/runtime gates. Do not rebuild scheduler, SDK, usage or outbox modules. Full enterprise product remains incomplete.

Revocation task: regression demonstrated an ACTIVE session after device revocation. Fixed by acquiring the existing compute lock before the device row and reusing the single session Stop implementation inside the revocation transaction. Active attempts are canceled and opted-in company retries remain fenced; credentials and audit commit atomically. Control mutations recheck live device status under lock. Regression RED→GREEN; all 25 integration tests, lint/typecheck passed; focused review found no findings. No duplicate session cancellation or retry implementation added.

Current checkpoint: retries passed source/Compose CI 44182487190. Revocation source and full Compose run 44182919710 passed. Removed financial feature wording from the contributor disclosure; payments remain outside scope. Owner selected Windows and Linux for the first production release; macOS is deferred. Keep unsupported execution disabled.

Documentation audit: superseded the old financial ADR with technical usage semantics, corrected the old NATS-dispatch description, and updated contributor enrollment/sharing disclosures. Historical master-plan financial sections remain explicitly superseded by current product scope. No new implementation or duplicated service was introduced.

Verified checkpoint: main5ec9e59 passed source and Compose CI44182919710; later changes align documentation/disclosure text only. Repository is clean after each push. Remaining platform/runtime, artifact, signing/distribution, observability/restore and scale gates are still open; no enterprise-ready completion claim.

Windows/Linux task (owner decision 2026-11-17): first extract existing Linux-specific secure storage, hardware/clock, file locking and runtime behind build-tagged functions. Reuse all protocol, identity, session and dashboard logic. Add Windows native CI, per-user protected storage and enrollment/connectivity tests. Windows execution remains fail-closed until Job Object resource enforcement/parent-death behavior and native acceptance pass; general workloads retain independent isolation review. No duplicated control plane, scheduler or session model.

Windows foundation implementation: extracted Linux-specific storage/locking/inventory/clock/cgroup/worker status; shared consent, protocol, proof, session manager and dashboard retained. Windows adds current-user DPAPI, protected user/SYSTEM DACLs set on creation, validation of existing file/directory/lock ACLs without modification, final/intermediate reparse rejection and native locking. Secret readers allow atomic replacement; lock handles keep replacement forbidden. Windows direct worker entry is rejected and no runtime is advertised. Added native Windows CI and HTTP enrollment/device-proof fixture tests (not real IdP acceptance). Linux race suite passed after extraction and review fixes; Windows amd64 cross-compile/static verification passed before final review fixes, repeating now. Native CI remains pending; do not claim production Windows support.

Review fix evidence: review identified unsafe existing-directory ACL replacement, broad file ACL acceptance and concurrent reader blocking atomic writes. Added native regressions before fixes; these cannot execute on this Linux host and require Windows CI. Removed permission rewriting, validated private ACLs by handle, shared-delete only on readers, retained lock-file identity, gated direct Windows worker invocation. Added trusted-parent requirement and junction tests. Linux ownership validation now matches the CLI contract. No fabricated native test result or Windows resource-isolation claim.

Final local platform checks: Linux race tests passed after all shared/Linux changes; Windows amd64 test binary and Windows-targeted vet passed after ACL/share/worker-gate fixes; Node lint/format/typecheck passed. Native Windows regressions are committed for actual CI execution, not represented as locally executed. Only named task-generated Go build caches were removed to stay within the 8 GB workspace; no application data or unrelated Docker resources were deleted.

2026-11-18 continuation: CI44189215468 passed Linux source and complete Compose acceptance. Native Windows tests failed `TestWindowsAtomicReplacementWorksWhileReaderIsOpen` with access denied: delete sharing alone does not give MoveFileEx replacement of an open destination POSIX semantics. Existing native regression is RED evidence. Changed only the protected-state replacement boundary to FileRenameInfoEx with replace/POSIX flags; temp data is flushed, source handle permits readers/deletion and failed replacement has no destructive fallback. Added old-reader snapshot preservation and denied-delete preservation assertions. Native rerun required before marking this gate passed.

Native Windows result (2026-11-18): de7544b passes Windows job and Linux source job in CI44221909237. The original concurrent-read regression is now GREEN with real Windows kernel semantics; preservation under denied replacement also passes. Full Compose regression is still installing its browser prerequisite. Keep Windows job execution disabled; next implementation after the checkpoint is native Job Object resource/process enforcement and atomic child assignment, with real parent-death/Stop/resource tests before advertising any runtime.

Next implementation brief: [Windows bounded execution plan](windows-execution-plan.md). Complete kernel limits and atomic supervision tests before connecting or advertising the worker. The brief preserves one-core CPU normalization, narrow handle inheritance, bounded setup/I/O, parent-death and local Stop requirements so the next task does not repeat design work or bypass isolation gates.

Platform checkpoint complete: all three CI44221909237 jobs passed on de7544b, including native Windows acceptance and full Linux Compose. Windows enrollment/storage foundation is verified on Server2022; clientOS/browser/connectivity and compute release gates remain.

Next task started: Windows kernel resource boundary, `job_windows.go` single owner. Initial Windows compile failed for missing helper symbols, then passed after implementation; Windows-targeted vet passed. Tests exercise conservative one-core cap normalization and real kernel readback of CPU/memory/process/lifetime configuration, plus invalid policy rejection. Native CI pending. Backend remains disconnected from workload launch and execution is still disabled. Actual enforcement, atomic assignment, Stop and supervisor-death tests are required before capability enablement.

CPU topology ruling before launch enablement: Go NumCPU is process-affinity-based on Windows, so it is not the denominator for host-wide Job Object CPU rate. The kernel helper uses GetActiveProcessorCount across groups; a native subprocess born with one-core affinity checks the selected quota cannot inflate. This is configuration groundwork only; runtime CPU pressure/topology-change tests remain necessary before enablement.

Kernel configuration review: no additional amd64 ABI/flag/byte-unit blocker was found. Added a real kernel handle-lifetime regression for close, idempotent close and queries after close. Coverage limitation: post-create SetInformation rejection is inspected but not fault-injected; no test-only constructor or fake kernel success was added. This is deferred coverage, not a claim that actual worker enforcement has passed. Windows runtime stays disabled.

Task 1 native result: Windows Job Object configuration, one-core-affinity quota regression and handle-lifetime tests passed in CI44224534151; source checks passed. Full-stack rerun is corroboration of unchanged Linux/shared paths; the preceding foundation full-stack run44221909237 already passed.

Task 2 implementation (native pending): `process_windows.go` is the single process/pipe/attribute/wait owner. Uses atomic Job List assignment and an explicit stdin/stdout handle list, does not inherit the job handle or supervisor environment, closes child thread/pipe handles and retains parent-owned process handles. Context is checked before creation and after synchronous CreateProcess; canceled startup terminates/reaps before returning. Wait rechecks cancellation before accepting exit. No production adapter invokes it and Windows runtime remains disabled.

Task 2 review fixes: added pre-canceled/startup-race cleanup cases, cancellation-before-exit handshake, worker readiness before supervisor death, and an unrelated inheritable-event sentinel to test handle exclusion. A readiness failure closes the owned job before closing pipes. Tests cross-compile/vet locally; native execution remains required. CreateProcess itself is a synchronous OS call; no company input may be sent until startup returns with a live context, and release acceptance must test startup/Stop under real endpoint security software. Do not claim full Windows Stop/runtime support from these helpers alone.

Task 1 complete: Windows, source and full Compose jobs all passed CI44224534151 (814bb33). Task 2 now also checks repeated real CreateProcess failures do not accumulate kernel handles. Post-review Windows cross-compile/vet is green; native process-lifetime/assignment cases have not yet executed. No worker capability enabled.
