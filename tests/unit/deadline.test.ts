import { test } from "node:test";
import assert from "node:assert/strict";
import { withDeadline } from "../../services/api/src/deadline.ts";
test("stalled probes cancel their connection and return a bounded failure", async () => {
  let canceled = false;
  await assert.rejects(
    () =>
      withDeadline(new Promise(() => {}), 20, () => {
        canceled = true;
      }),
    /timed out/,
  );
  assert(canceled);
  assert.equal(
    await withDeadline(Promise.resolve("ok"), 100, () => {
      throw new Error("unexpected cancellation");
    }),
    "ok",
  );
});
