# Repository work rules

Before every implementation task, read `docs/engineering-tracker.md`, `docs/product-scope.md`, and the relevant section of `docs/implementation-plan.md`. Update the tracker after the task with changed modules, tests, evidence and remaining work. Search for existing implementations before adding one; the tracker is an index, not a substitute for inspecting code.

The owner explicitly authorized development and pushes directly on `main` on 2026-11-17. Preserve unrelated changes. Never bypass failing checks or runtime security gates to mark a milestone complete.

Build the compute technology: company-owned control planes, contributor consent, secure connectivity, scheduling, isolation, usage evidence and integration APIs. Do not implement payments, pricing, credits, financial settlement, marketplace or business administration. `docs/product-scope.md` overrides conflicting financial requirements in the historical master plan.

Use focused modules, explicit interfaces at external boundaries, composition and dependency injection where they improve testing. Follow SOLID without speculative abstractions. Prefer one authoritative implementation for each domain invariant. Test behavior, concurrency and failure modes. Measure before optimizing; retain bounded queues, deadlines and resource limits.

Keep tool output scoped; batch independent reads; reuse test fixtures and existing modules; avoid repeatedly reading entire plans. Run relevant tests per task and full checks at release boundaries. Record costs/benchmarks as observations, never invented numbers. Never call the entire product production-ready based on synthetic tests.
