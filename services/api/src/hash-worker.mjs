import { Buffer } from "node:buffer";
import { workerData, parentPort } from "node:worker_threads";
import { createHash } from "node:crypto";
const { seed, iterations } = workerData;
if (
  typeof seed !== "string" ||
  !seed.length ||
  seed.length > 64 ||
  !/^[a-zA-Z0-9 _.-]+$/.test(seed) ||
  !Number.isInteger(iterations) ||
  iterations < 1 ||
  iterations > 1000000
)
  throw new Error("Invalid synthetic input");
let value = Buffer.from(seed);
for (let i = 0; i < iterations; i++)
  value = createHash("sha256").update(value).digest();
parentPort.postMessage(value.toString("hex"));
