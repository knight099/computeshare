# OpenCompute TypeScript SDK (workspace preview)

Zero-dependency typed client for the company's workload API. Source package is private until release packaging/versioning is verified. Requires a TypeScript-capable bundler/runtime and Fetch/AbortSignal support; tests use Node 24 with tsx.

```ts
import { OpenComputeClient } from "@opencompute/sdk";
const client = new OpenComputeClient("https://compute.example.com", async () =>
  getCompanyAccessToken(),
);
const job = await client.submitSyntheticJob({
  idempotency_key: "stable-business-operation-id",
  seed: "public-data-only",
  iterations: 1000,
  timeout_seconds: 10,
});
const current = await client.getJob(job.id);
const first = await client.listJobs({ limit: 50 });
if (first.next_cursor)
  await client.listJobs({ cursor: first.next_cursor, limit: 50 });
await client.cancelJob(job.id);
```

The caller supplies/refreshes OIDC access tokens and explicit stable idempotency keys. The client does not retry automatically, follow redirects, store credentials, calculate prices or award credits. HTTPS origins are required except loopback development. Errors expose status and optional trace ID, never response bodies or credentials. Server-side schemas and authorization remain authoritative. Only built-in bounded public CPU hash work is supported today.
