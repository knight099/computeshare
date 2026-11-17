# Core compute technology scope

Owner decision: 2026-11-17. This document takes precedence over credit, payout, pricing and management requirements in the original master plan. It does not waive isolation, consent or independent security-review gates.

OpenCompute is self-hosted infrastructure companies can use to build compute businesses. Each company operates its own identity, control plane, device fleet, policies, keys and data. People explicitly offer limited compute from home laptops or PCs; enrollment alone never grants execution permission. There is no required author-operated service.

## Included

- Company identity integration, scoped machine and user APIs, enrollment, key rotation, revocation and audited security actions.
- Outbound device connectivity across home networks, reconnect, leases, fencing and recovery under intermittent connectivity.
- Explicit time/resource consent, local Stop, idle/power/thermal controls, transparent activity and diagnostics.
- Capability-aware scheduling, cancellation, bounded queues, workload/artifact validation, verified outputs and usage evidence.
- Reviewed isolation of company work from host files, credentials, network and other workloads. Arbitrary workloads and GPU remain disabled until their runtime-specific tests and independent review pass. Home devices are untrusted; confidentiality cannot be guaranteed against a device owner.
- Idempotent technical usage records (CPU time, peak memory, workload/attempt/session identity, validation state), export/events and SDKs. Agent metrics are evidence, not proof; distinguish measured, reported, verified and rejected values.
- Operator configuration, branding, installation, upgrades, observability, backups, compatibility tests and reference deployments for independent companies.

## Excluded

Payments, wallets, prices, billing, credit balances, earnings, payouts, financial settlement, taxes, redemption, marketplace matching and business/customer administration. Companies own those systems outside this repository. The technology may expose authenticated technical records for their integrations; it does not calculate money or award credits.

## Completion gates

A usable release needs supported workload and OS matrices, reproducible installation, consent/Stop guarantees, tested resource isolation, two independent company deployments, scoped APIs/SDK, resilient scheduling, idempotent usage, restore/upgrade tests and published performance/failure results. Enterprise readiness requires evidence against declared workloads and deployment limits, not a promise that every company or workload is supported.

Initial supported execution remains the bounded Linux CPU hash worker. Windows/macOS, general workloads and GPUs are planned, unsupported until individually implemented and verified. Do not advertise them as available.
