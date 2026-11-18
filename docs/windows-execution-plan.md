# Windows bounded execution acceptance plan

Scope: implement the existing compiled-in public hash workload on Windows. Reuse the agent's protocol, session manager, workload validation, context cancellation and usage schema. This plan does not permit arbitrary executables, containers, scripts, files or network input. Read the engineering tracker and host matrix first.

Current prerequisite: `de7544b` passes native Windows storage/identity tests and Linux source checks in CI 44221909237. The protected-state regression first failed in 44189215468 and now passes. All three jobs, including full Linux Compose acceptance, passed in 44221909237. Windows execution remains unavailable.

## Ownership and invariants

- One Windows runtime backend owns availability, worker creation/status, enforced limits and usage measurement. Do not duplicate the shared hash algorithm, policy validation, Stop/session logic, authentication or job scheduler.
- Only the current agent executable's internal worker can be launched by the production workload path. No filename, argument vector or executable comes from job input.
- Configure Job Object limits before creation: hard CPU cap, process/job memory limits, one active process, kill on final Job Object handle close. Reject unsupported settings instead of falling back to an unrestricted process.
- Job CPU rate is a fraction of total eligible host CPU. Convert the selected percentage of **one core** conservatively using the host active processor count across all groups (not Go’s process-affinity count); reject unrepresentable caps instead of rounding above consent. Record measured behavior, especially with processor groups or nested jobs; a running backend must detect topology changes and cancel/reconfigure before they can relax the cap.
- Assign the worker atomically at process creation with the Job List attribute. Starting and subsequently assigning leaves a supervision gap; a suspended orphan is also unacceptable.
- Kernel CPU rate is defined in cycles per scheduling interval, whereas Job Object usage reports CPU time. Do not assume configuring a cycle quota proves the selected CPU-time bound. Compare independent accounting and sustained intervals, and retain the runtime gate if measured usage exceeds consent. Reference: [Microsoft CPU rate control](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-jobobject_cpu_rate_control_information).
- Inherit only the input/output pipe handles through an explicit handle list. The Job Object handle belongs only to the supervisor, so its death terminates the worker. Do not inherit company tokens, credential handles or the whole agent environment.
- Validate and bound all job fields before pipe I/O. Enforce context/lease deadlines through setup, execution and output collection; cap output bytes. No pipe operation may indefinitely delay Stop.
- The private Windows pipe exchange accepts at most4096 input bytes and collects at most8192 output bytes, rejecting rather than truncating oversized responses. It applies a30-second maximum, preserves earlier cancellation, and monitors the context through both process exit and pipe completion. Any failure returns no partial output. Native acceptance includes retained pipe handles after worker exit and peer-closed input; this helper alone does not enable execution.
- A direct internal-worker invocation must reject execution without the required resource-enforcement context. Do not treat membership in an unrelated runner/desktop Job Object as evidence of the selected limits.
- Report Job Object CPU time and peak committed memory with clear units. Reports stay untrusted at the API. Wall time, configured iterations and CPU time are different measurements.

## Task sequence

1. **Kernel resource boundary.** Implement small handle-owned Job Object/configuration/query helpers. Native tests read back CPU, memory, process and kill-on-close settings. Test failure and handle cleanup. Keep execution unavailable.
2. **Atomic worker supervision.** Add restricted-handle process creation with a fixed production executable/entry point. Native subprocess tests check job membership before work and actual worker death after terminating the supervisor. Exercise startup cancellation and prevent orphaned children.
3. **Bounded hash runner.** Connect the kernel helpers to the shared workload/session interface, bounded pipe I/O and metrics. Test the known hash vector, CPU/memory bounds, process limit, malformed input, context expiry, Stop and rejected direct entry. Resource-limit failures never return successful work.
4. **Capability enablement.** Advertise the runtime only when the platform can enforce its controls. Preserve fail-closed behavior under unsupported or restrictive nested jobs. Test native agent request/offer/accept/start/result flow, duplicate/stale attempts, local Stop while disconnected, suspend/resume expiry and restart non-resumption.
5. **Release acceptance.** Test real company OIDC/browser enrollment, connection/reconnect and revocation on declared Windows 10/11 x64 clients. Produce company-bound signed installers/update artifacts and verify tamper/revocation/downgrade behavior before a production claim.

## Required failure tests

- Supervisor SIGKILL/TerminateProcess during startup and active work; no surviving child.
- Job assignment or limit configuration rejected by the OS; no input or computation occurs.
- CPU cap normalization across processor counts; no claim based on readback alone when runtime measurements are required.
- Memory pressure/allocation exceeds the cap; worker stops without affecting agent responsiveness.
- Process creation inside the worker exceeds active-process limit; it fails.
- Disconnection, expired lease, clock rollback, suspended/resumed host and contributor Stop; no resumed consent or stale accepted output.
- Partial/oversized worker output and broken pipes; bounded memory and prompt cleanup.
- Handle/permission failures; no destructive state replacement or unrestricted execution fallback.

Job Objects provide resource and process-lifetime controls. They are **not** filesystem, credential or network isolation for hostile code. General company workloads require a separately reviewed VM boundary and adversarial tests before enablement. GPU support is a separate reviewed capability. Payments and business management remain excluded.
