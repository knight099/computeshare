// Real Keycloak -> native CLI PKCE -> API -> PostgreSQL -> gateway acceptance.
import { readFile, mkdtemp, rm, mkdir, access, rmdir } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { randomBytes } from "node:crypto";
import assert from "node:assert/strict";
import { verifyBrowserLogin } from "./browser.mjs";
import { verifyCompute } from "./compute.mjs";

const env = Object.fromEntries(
  (await readFile(".env", "utf8"))
    .split("\n")
    .filter((s) => s && !s.startsWith("#"))
    .map((s) => {
      const i = s.indexOf("=");
      return [s.slice(0, i), s.slice(i + 1)];
    }),
);
const base = process.env.E2E_API_URL ?? "http://localhost:8080";
const identity = process.env.E2E_IDENTITY_URL ?? "http://localhost:8081";
const dir = await mkdtemp(join(tmpdir(), "opencompute-e2e-"));
const childProcesses = [];
let adminToken, userID;
async function json(url, options = {}) {
  const r = await fetch(url, {
    ...options,
    signal: AbortSignal.timeout(15000),
  });
  if (!r.ok)
    throw new Error(
      `E2E request failed: HTTP ${r.status} (${new URL(url).pathname})`,
    );
  return r.status === 204 ? null : r.json();
}
function command(args) {
  const child = spawn("./bin/opencompute-agent", args, {
    stdio: ["ignore", "pipe", "pipe"],
  });
  childProcesses.push(child);
  const completed = once(child, "exit");
  let output = "";
  for (const stream of [child.stdout, child.stderr])
    stream.on("data", (b) => {
      output += b.toString();
    });
  return { child, completed, output: () => output };
}
async function until(probe, timeout = 30000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const value = await probe();
    if (value) return value;
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error("E2E condition timed out");
}
async function successful(cmd) {
  const [code] = await cmd.completed;
  assert.equal(
    code,
    0,
    "Agent command failed (output redacted to protect auth codes)",
  );
}
const deadline = setTimeout(() => {
  for (const child of childProcesses) child.kill("SIGKILL");
  process.exitCode = 1;
}, 300000);
try {
  assert.equal((await fetch(base + "/readyz")).status, 200);
  const token = await json(
    identity + "/realms/master/protocol/openid-connect/token",
    {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        grant_type: "password",
        client_id: "admin-cli",
        username: "admin",
        password: env.KEYCLOAK_ADMIN_PASSWORD,
      }),
    },
  );
  adminToken = token.access_token;
  const adminHeaders = {
    Authorization: `Bearer ${adminToken}`,
    "Content-Type": "application/json",
  };
  const username = "acceptance-" + randomBytes(6).toString("hex");
  const password = randomBytes(24).toString("base64url");
  const adminBase = identity + "/admin/realms/opencompute";
  const created = await fetch(adminBase + "/users", {
    method: "POST",
    headers: adminHeaders,
    body: JSON.stringify({
      username,
      enabled: true,
      emailVerified: true,
      email: username + "@example.invalid",
      firstName: "Acceptance",
      lastName: "Test",
      credentials: [{ type: "password", value: password, temporary: false }],
    }),
  });
  assert.equal(created.status, 201);
  userID = created.headers.get("location").split("/").at(-1);
  const roles = await Promise.all(
    ["contributor", "operator_admin"].map((name) =>
      json(adminBase + "/roles/" + name, { headers: adminHeaders }),
    ),
  );
  const assigned = await fetch(
    adminBase + "/users/" + userID + "/role-mappings/realm",
    { method: "POST", headers: adminHeaders, body: JSON.stringify(roles) },
  );
  assert.equal(assigned.status, 204);

  const login = command(["login", "--gateway", base, "--state-dir", dir]);
  const authURL = await until(
    () =>
      login
        .output()
        .match(/https?:\/\/\S+\/protocol\/openid-connect\/auth\?\S+/)?.[0],
  );
  const page = await fetch(authURL, { redirect: "manual" });
  assert.equal(page.status, 200);
  const html = await page.text();
  const action = html
    .match(/<form[^>]*action="([^"]+)"/i)?.[1]
    ?.replaceAll("&amp;", "&");
  assert(action, "Keycloak login form unavailable");
  const cookie = page.headers
    .getSetCookie()
    .map((c) => c.split(";")[0])
    .join("; ");
  const signed = await fetch(action, {
    method: "POST",
    redirect: "manual",
    headers: {
      "Content-Type": "application/x-www-form-urlencoded",
      Cookie: cookie,
    },
    body: new URLSearchParams({ username, password, credentialId: "" }),
  });
  assert.equal(signed.status, 302, "Keycloak rejected the test login");
  const callback = new URL(signed.headers.get("location"));
  assert.equal(callback.origin, "http://127.0.0.1:8787");
  assert.equal((await fetch(callback)).status, 200);
  await successful(login);
  const credentials = JSON.parse(
    await readFile(join(dir, "login.json"), "utf8"),
  );
  await successful(
    command([
      "enroll",
      "--gateway",
      base,
      "--state-dir",
      dir,
      "--name",
      "acceptance-linux",
      "--accept-consent",
    ]),
  );
  const device = JSON.parse(await readFile(join(dir, "device.json"), "utf8"));
  const cgroupRoot = process.env.OPENCOMPUTE_CGROUP_ROOT;
  assert(cgroupRoot, "real delegated cgroup root required");
  const staleGroup = join(
    cgroupRoot,
    "job-11111111-1111-4111-8111-111111111111",
  );
  const unknownGroup = join(cgroupRoot, "unowned-acceptance");
  await mkdir(staleGroup);
  await mkdir(unknownGroup);
  const runner = command(["run", "--state-dir", dir]);
  await until(async () => {
    try {
      await access(staleGroup);
      return false;
    } catch (error) {
      if (error.code === "ENOENT") return true;
      throw error;
    }
  });
  await access(unknownGroup);
  await rmdir(unknownGroup);
  console.log(
    "PASS: startup removes empty owned cgroups and preserves unknown groups",
  );
  const headers = { Authorization: `Bearer ${credentials.access_token}` };
  await until(async () => {
    const fleet = await json(base + "/v1/operator/devices", { headers });
    return fleet.devices.some(
      (d) => d.id === device.device_id && d.status === "online",
    );
  });
  await verifyBrowserLogin({
    consoleURL: process.env.E2E_CONSOLE_URL ?? "http://localhost:3000",
    identity,
    username,
    password,
    deviceName: "acceptance-linux",
  });
  const connections =
    runner.output().split("Connected to company gateway").length - 1;
  const restarted = spawn("docker", ["compose", "restart", "api"], {
    stdio: "ignore",
  });
  childProcesses.push(restarted);
  assert.equal((await once(restarted, "exit"))[0], 0);
  await until(
    () =>
      runner.output().split("Connected to company gateway").length - 1 >
      connections,
    45000,
  );
  console.log(
    "PASS: agent reconnect after real gateway restart with fresh device authentication",
  );
  const dashboard = await until(
    () =>
      runner
        .output()
        .match(/Contributor dashboard: (http:\/\/127\.0\.0\.1:\d+)/)?.[1],
  );
  await verifyCompute({ base, headers, dashboard });
  runner.child.kill("SIGTERM");
  await successful(runner);
  assert.equal(
    (
      await fetch(base + "/v1/devices/" + device.device_id + "/revoke", {
        method: "POST",
        headers,
      })
    ).status,
    200,
  );
  assert.equal(
    (
      await fetch(base + "/v1/device-auth/challenge", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_id: device.device_id }),
      })
    ).status,
    401,
  );
  const consolePage = await fetch(
    process.env.E2E_CONSOLE_URL ?? "http://localhost:3000",
  );
  assert.equal(consolePage.status, 200);
  assert((await consolePage.text()).includes("Compute starts with trust."));
  console.log(
    "PASS: real OIDC PKCE, explicit consent, Linux enrollment, outbound heartbeat, local Stop, revocation, and rendered console",
  );
} finally {
  clearTimeout(deadline);
  for (const child of childProcesses)
    if (child.exitCode === null) child.kill("SIGKILL");
  if (userID && adminToken)
    await fetch(identity + "/admin/realms/opencompute/users/" + userID, {
      method: "DELETE",
      headers: { Authorization: `Bearer ${adminToken}` },
    }).catch(() => {});
  await rm(dir, { recursive: true, force: true });
}
