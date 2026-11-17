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
