import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { chromium } from "playwright";
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";

export async function verifyCompute({ base, headers, dashboard }) {
  const browser = await chromium.launch({ headless: true });
  let paused = false;
  const state = async () => {
    const r = await fetch(dashboard + "/state", {
      signal: AbortSignal.timeout(2000),
    });
    assert(r.ok);
    return r.json();
  };
  const until = async (probe, timeout = 60000) => {
    const end = Date.now() + timeout;
    while (Date.now() < end) {
      const r = await probe();
      if (r) return r;
      await new Promise((r) => setTimeout(r, 100));
    }
    throw Error("Compute acceptance condition timed out");
  };
  const compose = async (action) => {
    const child = spawn("docker", ["compose", action, "api"], {
      stdio: "ignore",
    });
    assert.equal((await once(child, "exit"))[0], 0);
  };
  const submit = async (key, iterations = 1000) => {
    const r = await fetch(base + "/v1/jobs", {
      method: "POST",
      headers: { ...headers, "Content-Type": "application/json" },
      body: JSON.stringify({
        idempotency_key: key,
        workload_type: "synthetic-hash-v1",
        seed: "public-acceptance",
        iterations,
        timeout_seconds: 10,
      }),
    });
    assert.equal(r.status, 201);
    return r.json();
  };
  try {
    const page = await browser.newPage();
    await page.goto(dashboard);
    await page.locator("#consent").check();
    await page.locator("#cpu").fill("10");
    await page.locator("#memory").fill("64");
    await page
      .getByRole("button", { name: "Start sharing", exact: true })
      .click();
    const initial = await until(async () => {
      const s = await state();
      return s.session.status === "ACTIVE" ? s : false;
    }, 15000);
    assert.equal(initial.session.policy.memory_mb, 64);
    assert.equal(initial.session.policy.cpu_percent, 10);
    const ownSession = await fetch(
      base + "/v1/availability-sessions/" + initial.session.id,
      { headers },
    );
    const session = await ownSession.json();
    assert.equal(session.requested_duration_seconds, 21600);
    const foreign = await fetch(dashboard + "/stop", {
      method: "POST",
      headers: {
        Origin: "https://attacker.invalid",
        "Content-Type": "application/json",
        "X-OpenCompute-CSRF": initial.csrf,
      },
      body: "{}",
    });
    assert.equal(foreign.status, 403);
    const ids = new Set();
    for (let i = 0; i < 100; i++)
      ids.add((await submit("batch-" + randomUUID())).id);
    const deadline = Date.now() + 90000;
    while (true) {
      const r = await fetch(base + "/v1/operator/jobs", { headers });
      assert.equal(r.status, 200);
      const jobs = (await r.json()).jobs.filter((j) => ids.has(j.id));
      if (jobs.length === 100 && jobs.every((j) => j.state === "SUCCEEDED"))
        break;
      assert(
        !jobs.some((j) => j.state === "FAILED"),
        "synthetic worker failed",
      );
      if (Date.now() > deadline)
        throw Error("100-job completion deadline exceeded");
      await new Promise((r) => setTimeout(r, 1000));
    }
    const usageResponse = await fetch(base + "/v1/operator/usage?limit=200", {
      headers,
    });
    assert.equal(usageResponse.status, 200);
    const usage = (await usageResponse.json()).records.filter((r) =>
      ids.has(r.job_id),
    );
    assert.equal(usage.length, 100);
    assert.equal(new Set(usage.map((r) => r.attempt_id)).size, 100);
    for (const record of usage) {
      assert.equal(record.output_verified, true);
      assert.equal(record.logical_iterations, 1000);
      assert.equal(record.telemetry_status, "REPORTED_UNTRUSTED");
      assert(BigInt(record.cpu_usage_usec) > 0n);
      assert(
        BigInt(record.memory_peak_bytes) > 0n &&
          BigInt(record.memory_peak_bytes) <= 64n * 1024n * 1024n,
      );
    }
    console.log(
      "PASS: 100 unique durable usage records, verified outputs and explicitly untrusted bounded telemetry",
    );
    const completed = await until(async () => {
      const s = await state();
      return s.last_result.memory_peak_bytes > 0 ? s : false;
    });
    assert(completed.last_result.memory_peak_bytes <= 64 * 1024 * 1024);
    assert(completed.last_result.cpu_usage_usec > 0);
    const long = await submit("stop-" + randomUUID(), 1000000);
    await until(async () => {
      const s = await state();
      return s.current_job === long.id && s.worker_running;
    }, 15000);
    const root = process.env.OPENCOMPUTE_CGROUP_ROOT;
    assert(root);
    const groups = (await readdir(root)).filter((name) =>
      name.startsWith("job-"),
    );
    assert.equal(groups.length, 1);
    const group = join(root, groups[0]);
    assert.equal(
      (await readFile(join(group, "cpu.max"), "utf8")).trim(),
      "10000 100000",
    );
    assert.equal(
      (await readFile(join(group, "memory.max"), "utf8")).trim(),
      String(64 * 1024 * 1024),
    );
    assert.equal(
      (await readFile(join(group, "memory.swap.max"), "utf8")).trim(),
      "0",
    );
    assert.equal(
      (await readFile(join(group, "pids.max"), "utf8")).trim(),
      "32",
    );
    await compose("pause");
    paused = true;
    const stoppedAt = Date.now();
    await page.getByRole("button", { name: "Stop sharing now" }).click();
    await until(async () => {
      const s = await state();
      return s.session.status === "STOPPED" && !s.worker_running;
    }, 2000);
    assert(
      Date.now() - stoppedAt < 2000,
      "local Stop waited for company network",
    );
    await compose("unpause");
    paused = false;
    await until(async () => {
      const r = await fetch(
        base + "/v1/availability-sessions/" + initial.session.id,
        { headers },
      );
      return (await r.json()).status === "STOPPED";
    }, 20000);
    const queued = await submit("after-stop-" + randomUUID());
    await new Promise((r) => setTimeout(r, 1000));
    const after = await fetch(base + "/v1/jobs/" + queued.id, { headers });
    assert.equal((await after.json()).state, "QUEUED");
    console.log(
      "PASS: local Chromium sharing consent, six-hour session, 100 real constrained hash jobs, measured RAM/CPU, CSRF rejection and immediate Stop during network partition",
    );
  } finally {
    if (paused) await compose("unpause");
    await browser.close();
  }
}
