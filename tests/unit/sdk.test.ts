import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { once } from "node:events";
import {
  OpenComputeClient,
  OpenComputeError,
} from "../../packages/sdk-typescript/src/index.ts";

test("SDK rejects unsafe company URLs", () => {
  for (const url of [
    "http://company.example",
    "https://user:secret@company.example",
    "https://company.example/path",
    "https://company.example/?token=secret",
    "file:///tmp/key",
  ]) {
    assert.throws(() => new OpenComputeClient(url, () => "token"), /URL/);
  }
});
test("SDK preserves idempotency, pagination, errors and does not forward tokens across redirects", async () => {
  let leaked = false;
  const target = createServer((_, res) => {
    leaked = true;
    res.end("{}");
  });
  const requests: { url?: string; auth?: string; body: string }[] = [];
  const server = createServer(async (req, res) => {
    let body = "";
    for await (const part of req) body += part;
    requests.push({ url: req.url, auth: req.headers.authorization, body });
    if (req.url === "/v1/jobs/redirect") {
      res.writeHead(302, {
        location: `http://127.0.0.1:${(target.address() as { port: number }).port}/`,
      });
      res.end();
      return;
    }
    if (req.url === "/v1/jobs/missing") {
      res.writeHead(404, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: "Not found", trace_id: "trace-safe" }));
      return;
    }
    res.setHeader("content-type", "application/json");
    res.end(
      JSON.stringify(
        req.url?.startsWith("/v1/operator/jobs")
          ? { jobs: [], next_cursor: null }
          : { id: "job", state: "QUEUED" },
      ),
    );
  });
  target.listen(0, "127.0.0.1");
  await once(target, "listening");
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const client = new OpenComputeClient(
    `http://127.0.0.1:${(server.address() as { port: number }).port}`,
    async () => "sensitive",
  );
  try {
    const input = { idempotency_key: "stable", seed: "public", iterations: 2 };
    await client.submitSyntheticJob(input);
    await client.submitSyntheticJob(input);
    assert.equal(requests[0].body, requests[1].body);
    assert.equal(
      JSON.parse(requests[0].body).workload_type,
      "synthetic-hash-v1",
    );
    assert.equal(requests[0].auth, "Bearer sensitive");
    await client.listJobs({ limit: 20, cursor: "opaque" });
    assert.equal(requests[2].url, "/v1/operator/jobs?limit=20&cursor=opaque");
    await assert.rejects(client.getJob("redirect"));
    assert.equal(leaked, false);
    await assert.rejects(
      client.getJob("missing"),
      (e: unknown) =>
        e instanceof OpenComputeError &&
        e.status === 404 &&
        e.traceId === "trace-safe" &&
        !e.message.includes("sensitive"),
    );
  } finally {
    server.closeAllConnections();
    target.closeAllConnections();
    await Promise.all([
      new Promise<void>((r) => server.close(() => r())),
      new Promise<void>((r) => target.close(() => r())),
    ]);
  }
});
