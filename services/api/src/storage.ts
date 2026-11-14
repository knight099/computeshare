import { createHash, createHmac } from "node:crypto";

// Minimal SigV4 ListBuckets probe; no unsigned or anonymous fallback.
export async function probeStorage(
  endpoint: string,
  accessKey: string,
  secret: string,
  now = new Date(),
) {
  const url = new URL(endpoint);
  if (
    (url.pathname !== "/" && url.pathname !== "") ||
    url.search ||
    url.username ||
    url.password
  )
    throw new Error("S3 endpoint must be an origin");
  const hash = (s: string) => createHash("sha256").update(s).digest("hex");
  const hmac = (key: string | Buffer, s: string) =>
    createHmac("sha256", key).update(s).digest();
  const timestamp = now.toISOString().replace(/[:-]|\.\d{3}/g, "");
  const date = timestamp.slice(0, 8);
  const scope = `${date}/us-east-1/s3/aws4_request`;
  const headers = `host:${url.host}\nx-amz-content-sha256:${hash("")}\nx-amz-date:${timestamp}\n`;
  const signed = "host;x-amz-content-sha256;x-amz-date";
  const canonical = `GET\n/\n\n${headers}\n${signed}\n${hash("")}`;
  const key = hmac(
    hmac(hmac(hmac(`AWS4${secret}`, date), "us-east-1"), "s3"),
    "aws4_request",
  );
  const signature = hmac(
    key,
    `AWS4-HMAC-SHA256\n${timestamp}\n${scope}\n${hash(canonical)}`,
  ).toString("hex");
  const response = await fetch(url, {
    signal: AbortSignal.timeout(2000),
    redirect: "error",
    headers: {
      "x-amz-content-sha256": hash(""),
      "x-amz-date": timestamp,
      Authorization: `AWS4-HMAC-SHA256 Credential=${accessKey}/${scope}, SignedHeaders=${signed}, Signature=${signature}`,
    },
  });
  if (
    !response.ok ||
    !(await response.text()).includes("ListAllMyBucketsResult")
  )
    throw new Error("S3 probe failed");
}
