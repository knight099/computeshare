import { test } from "node:test";
import assert from "node:assert/strict";
import { setImmediate } from "node:timers/promises";
import { HashVerifier } from "../../services/api/src/verifier.ts";

test("verification preserves the deterministic hash while leaving the event loop responsive", async () => {
  const verifier = new HashVerifier(1, 1);
  try {
    let finished = false;
    const result = verifier.compute("public", 1000000).then((r) => {
      finished = true;
      return r;
    });
    await setImmediate();
    assert.equal(
      finished,
      false,
      "hashing blocked or completed on the API event loop",
    );
    assert.match(await result, /^[a-f0-9]{64}$/);
    assert.equal(
      await verifier.compute("public", 2),
      "315fc33dbdb63592565e8ed77eff5d3cf512638ea9b26631b7277cc73a3fc395",
    );
  } finally {
    await verifier.close();
  }
});
test("verification capacity is bounded, recovers after a task, and fails closed on shutdown", async () => {
  const verifier = new HashVerifier(1, 0);
  const first = verifier.compute("public", 1000000);
  await assert.rejects(() => verifier.compute("next", 1), /capacity/);
  await first;
  assert.match(await verifier.compute("next", 1), /^[a-f0-9]{64}$/);
  await verifier.close();
  await assert.rejects(() => verifier.compute("public", 1), /closed/);
});

test("verification rejects bad worker input and releases capacity", async () => {
  const verifier = new HashVerifier(1, 1);
  try {
    await assert.rejects(verifier.compute("invalid/seed", 1), /failed/);
    assert.match(await verifier.compute("public", 1), /^[a-f0-9]{64}$/);
  } finally {
    await verifier.close();
  }
});

test("verification has a deadline for active and queued work", async () => {
  const verifier = new HashVerifier(1, 1, 1);
  try {
    const first = assert.rejects(
      verifier.compute("public", 1000000),
      /timed out/,
    );
    const queued = assert.rejects(verifier.compute("queued", 1), /timed out/);
    await Promise.all([first, queued]);
  } finally {
    await verifier.close();
  }
});
