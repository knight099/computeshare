import { test } from "node:test";
import assert from "node:assert/strict";
import { controlMessageSchema } from "../../packages/protocol/src/index.ts";

test("protocol rejects unknown critical fields, versions and unsafe sequences", () => {
  const valid = {
    protocol_version: 1,
    type: "HEARTBEAT",
    device_id: crypto.randomUUID(),
    sequence: 1,
    trace_id: crypto.randomUUID(),
  };
  assert.equal(controlMessageSchema.safeParse(valid).success, true);
  for (const invalid of [
    { ...valid, protocol_version: 2 },
    { ...valid, sequence: -1 },
    { ...valid, sequence: 1e20 },
    { ...valid, execute: "sh" },
  ]) {
    assert.equal(controlMessageSchema.safeParse(invalid).success, false);
  }
});

test("usage wire schema rejects unsafe, negative and extra metrics", () => {
  const valid = {
    protocol_version: 1,
    type: "JOB_COMPLETE",
    device_id: crypto.randomUUID(),
    sequence: 1,
    trace_id: crypto.randomUUID(),
    attempt: {
      id: crypto.randomUUID(),
      epoch: 1,
      usage: { cpu_usage_usec: 1000, memory_peak_bytes: 1024 },
    },
  };
  assert(controlMessageSchema.safeParse(valid).success);
  for (const usage of [
    { cpu_usage_usec: -1, memory_peak_bytes: 1 },
    { cpu_usage_usec: 1e20, memory_peak_bytes: 1 },
    { cpu_usage_usec: 1, memory_peak_bytes: 1, credits: 100 },
    { cpu_usage_usec: 1 },
  ])
    assert.equal(
      controlMessageSchema.safeParse({
        ...valid,
        attempt: { ...valid.attempt, usage },
      }).success,
      false,
    );
});
