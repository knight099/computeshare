"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import Jobs from "./jobs";

type Config = {
  operator_id: string;
  operator_name: string;
  oidc_issuer: string;
  oidc_client_id: string;
  consent_version: string;
};
type Device = {
  id: string;
  name: string;
  capabilities: {
    os: string;
    arch: string;
    cpu_cores: number;
    memory_mb: number;
  };
  last_seen_at: string | null;
  revoked_at: string | null;
  status?: string;
};
const base64url = (buffer: ArrayBuffer | Uint8Array) =>
  btoa(String.fromCharCode(...new Uint8Array(buffer)))
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replaceAll("=", "");

export default function Home() {
  const [config, setConfig] = useState<Config>();
  const [token, setToken] = useState("");
  const [roles, setRoles] = useState<string[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [view, setView] = useState<"devices" | "activity" | "setup" | "jobs">(
    "devices",
  );
  const [events, setEvents] = useState<
    { id: number; action: string; object_id: string; created_at: string }[]
  >([]);
  const [consented, setConsented] = useState(false);
  const [busy, setBusy] = useState(false);
  const initialized = useRef(false);
  const operator = roles.some((r) =>
    ["operator_admin", "operator_support", "auditor"].includes(r),
  );

  const request = useCallback(
    async (path: string, options: RequestInit = {}) => {
      const response = await fetch(path, {
        ...options,
        headers: {
          ...options.headers,
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
      });
      if (response.status === 401) {
        setToken("");
        setRoles([]);
        setDevices([]);
        throw new Error("Your session expired. Sign in again.");
      }
      const data = await response.json();
      if (!response.ok) throw new Error(data.error ?? "Request failed");
      return data;
    },
    [token],
  );

  useEffect(() => {
    if (initialized.current) return;
    initialized.current = true;
    void (async () => {
      try {
        const response = await fetch("/v1/config");
        if (!response.ok) throw new Error("The company API is unavailable.");
        const c: Config = await response.json();
        setConfig(c);
        const params = new URLSearchParams(location.search);
        if (params.has("error"))
          throw new Error("Sign-in was canceled or denied.");
        const code = params.get("code");
        if (code) {
          const state = sessionStorage.getItem("oidc_state");
          const verifier = sessionStorage.getItem("oidc_verifier");
          sessionStorage.removeItem("oidc_state");
          sessionStorage.removeItem("oidc_verifier");
          history.replaceState(null, "", "/");
          if (!state || params.get("state") !== state || !verifier)
            throw new Error("Sign-in state mismatch. Please sign in again.");
          const result = await fetch(
            `${c.oidc_issuer}/protocol/openid-connect/token`,
            {
              method: "POST",
              headers: { "Content-Type": "application/x-www-form-urlencoded" },
              body: new URLSearchParams({
                grant_type: "authorization_code",
                client_id: c.oidc_client_id,
                code,
                code_verifier: verifier,
                redirect_uri: location.origin + "/",
              }),
            },
          );
          if (!result.ok) throw new Error("Unable to complete sign-in.");
          const credentials = await result.json();
          setToken(credentials.access_token);
        }
      } catch (e) {
        setError(e instanceof Error ? e.message : "Connection failed");
      } finally {
        setLoading(false);
      }
    })();
  }, []);

  useEffect(() => {
    if (!token) return;
    let active = true;
    const refresh = async () => {
      try {
        const me = await request("/v1/me");
        const isOperator = me.roles.some((r: string) =>
          ["operator_admin", "operator_support", "auditor"].includes(r),
        );
        const data = await request(
          isOperator ? "/v1/operator/devices" : "/v1/devices",
        );
        if (active) {
          setRoles(me.roles);
          setDevices(data.devices);
          setError("");
        }
      } catch (e) {
        if (active)
          setError(e instanceof Error ? e.message : "Unable to load devices");
      }
    };
    void refresh();
    const timer = setInterval(() => void refresh(), 15000);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, [token, request]);

  async function login() {
    if (!config) return;
    const verifier = base64url(crypto.getRandomValues(new Uint8Array(32)));
    const state = base64url(crypto.getRandomValues(new Uint8Array(24)));
    sessionStorage.setItem("oidc_state", state);
    sessionStorage.setItem("oidc_verifier", verifier);
    const challenge = base64url(
      await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier)),
    );
    const url = new URL(`${config.oidc_issuer}/protocol/openid-connect/auth`);
    url.search = new URLSearchParams({
      client_id: config.oidc_client_id,
      redirect_uri: location.origin + "/",
      response_type: "code",
      scope: "openid",
      state,
      code_challenge: challenge,
      code_challenge_method: "S256",
    }).toString();
    location.assign(url);
  }
  async function consent() {
    setBusy(true);
    try {
      await request("/v1/consent", {
        method: "POST",
        body: JSON.stringify({
          version: config?.consent_version,
          accepted: true,
        }),
      });
      setConsented(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Consent failed");
    } finally {
      setBusy(false);
    }
  }
  async function activity() {
    setView("activity");
    try {
      setEvents((await request("/v1/operator/audit-events")).events);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Activity unavailable");
    }
  }
  return (
    <div className="shell">
      <aside>
        <a className="brand" href="/">
          <span className="brandmark">◎</span>
          {config?.operator_name ?? "OpenCompute"}
        </a>
        <div className="workspace">
          <span className="status-dot" />
          Independent company network<small>SELF-HOSTED PLATFORM</small>
        </div>
        <nav aria-label="Main navigation">
          <button
            className={view === "devices" ? "selected" : ""}
            onClick={() => setView("devices")}
          >
            ▦ <span>{operator ? "Device fleet" : "My devices"}</span>
          </button>
          {token && (
            <button
              className={view === "jobs" ? "selected" : ""}
              onClick={() => setView("jobs")}
            >
              ▷ <span>Jobs</span>
            </button>
          )}
          {roles.some((r) => ["operator_admin", "auditor"].includes(r)) && (
            <button
              className={view === "activity" ? "selected" : ""}
              onClick={() => void activity()}
            >
              ≋ <span>Audit activity</span>
            </button>
          )}
          <button
            className={view === "setup" ? "selected" : ""}
            onClick={() => setView("setup")}
          >
            ⌘ <span>Getting started</span>
          </button>
        </nav>
        <div className="sidebar-bottom">
          <span className="version">FOUNDATION · v0.1.0</span>
          <p>
            Company-owned infrastructure.
            <br />
            Contributor-controlled devices.
          </p>
        </div>
      </aside>
      <div className="main">
        <header>
          <span>
            Company Platform{" "}
            <span className="crumb">
              /{" "}
              {view === "devices"
                ? "Devices"
                : view === "jobs"
                  ? "Jobs"
                  : view === "activity"
                    ? "Activity"
                    : "Setup"}
            </span>
          </span>
          <div className="header-right">
            <span className="dev-badge">Development</span>
            {token ? (
              <button
                className="subtle"
                onClick={() => {
                  setToken("");
                  setRoles([]);
                  setDevices([]);
                  setEvents([]);
                  setConsented(false);
                }}
              >
                Sign out
              </button>
            ) : (
              <button
                className="primary compact"
                disabled={!config || loading}
                onClick={() => void login()}
              >
                Sign in
              </button>
            )}
          </div>
        </header>
        <main>
          <div className="page-title">
            <div>
              <div className="eyebrow">YOUR COMPUTE NETWORK</div>
              <h1>
                {view === "devices"
                  ? operator
                    ? "Device fleet"
                    : "Connected devices"
                  : view === "jobs"
                    ? "Jobs"
                    : view === "activity"
                      ? "Audit activity"
                      : "Build your network"}
              </h1>
              <p>
                {view === "devices"
                  ? "A clear view of the devices enrolled with your organization."
                  : view === "jobs"
                    ? "Jobs"
                    : view === "activity"
                      ? "Recorded identity, enrollment, and device-security events."
                      : "Start with identity, then enroll your first Linux device."}
              </p>
            </div>
            <span className="outline-badge">Outbound connections only ↗</span>
          </div>
          {error && (
            <div className="error" role="alert">
              {error}
            </div>
          )}
          {!token ? (
            <section className="welcome">
              <div className="welcome-icon">◎</div>
              <div className="eyebrow">YOUR INFRASTRUCTURE. YOUR NETWORK.</div>
              <h2>Compute starts with trust.</h2>
              <p>
                Sign in with your organization to manage device enrollment,
                review connectivity, and establish your independent compute
                network.
              </p>
              <button
                className="primary"
                disabled={!config || loading}
                onClick={() => void login()}
              >
                {loading ? "Connecting…" : "Sign in to your organization →"}
              </button>
              <small>
                Authentication is handled by your company’s identity provider.
              </small>
            </section>
          ) : (
            <>
              {view === "devices" && (
                <>
                  <div className="stats">
                    <article>
                      <span>Enrolled devices</span>
                      <strong>{devices.length}</strong>
                      <small>Registered with this operator</small>
                    </article>
                    <article>
                      <span>Online now</span>
                      <strong>
                        {
                          devices.filter(
                            (d) =>
                              !d.revoked_at &&
                              d.last_seen_at &&
                              Date.now() - new Date(d.last_seen_at).getTime() <
                                45000,
                          ).length
                        }
                        <i />
                      </strong>
                      <small>Heartbeat within 45 seconds</small>
                    </article>
                    <article>
                      <span>Registered CPU cores</span>
                      <strong>
                        {devices
                          .filter((d) => !d.revoked_at)
                          .reduce(
                            (sum, d) => sum + d.capabilities.cpu_cores,
                            0,
                          )}
                      </strong>
                      <small>Inventory · not available compute</small>
                    </article>
                    <article>
                      <span>Execution</span>
                      <strong className="word">Synthetic only</strong>
                      <small>Explicit local consent required</small>
                    </article>
                  </div>
                  <section className="panel">
                    <div className="panel-heading">
                      <h2>
                        {operator
                          ? "Registered devices"
                          : "Your registered devices"}
                      </h2>
                      <span className="muted">Refreshes every 15 seconds</span>
                    </div>
                    {devices.length ? (
                      <div className="table-wrap">
                        <table>
                          <thead>
                            <tr>
                              <th>Device</th>
                              <th>Status</th>
                              <th>Hardware</th>
                              <th>Last heartbeat</th>
                            </tr>
                          </thead>
                          <tbody>
                            {devices.map((d) => (
                              <tr key={d.id}>
                                <td>
                                  <strong>{d.name}</strong>
                                  <code>{d.id}</code>
                                </td>
                                <td>
                                  <span
                                    className={`pill ${d.revoked_at ? "revoked" : d.last_seen_at && Date.now() - new Date(d.last_seen_at).getTime() < 45000 ? "online" : ""}`}
                                  >
                                    {d.revoked_at
                                      ? "Revoked"
                                      : d.last_seen_at &&
                                          Date.now() -
                                            new Date(d.last_seen_at).getTime() <
                                            45000
                                        ? "Online"
                                        : "Offline"}
                                  </span>
                                </td>
                                <td>
                                  {d.capabilities.os} · {d.capabilities.arch}
                                  <small>
                                    {d.capabilities.cpu_cores} cores ·{" "}
                                    {Math.round(
                                      d.capabilities.memory_mb / 1024,
                                    )}{" "}
                                    GB RAM
                                  </small>
                                </td>
                                <td>
                                  {d.last_seen_at
                                    ? new Date(d.last_seen_at).toLocaleString()
                                    : "Not connected"}
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    ) : (
                      <div className="empty">
                        <span>▧</span>
                        <h3>No devices enrolled yet</h3>
                        <p>
                          Connect a Linux agent to see its hardware and live
                          connectivity here.
                        </p>
                        <button
                          className="subtle"
                          onClick={() => setView("setup")}
                        >
                          View enrollment steps →
                        </button>
                      </div>
                    )}
                  </section>
                  <div className="notice">
                    <span>ⓘ</span>
                    <p>
                      <strong>Explicit sharing consent is required.</strong>{" "}
                      This preview runs only bounded public CPU hash jobs. Start
                      and Stop live in the agent’s local contributor dashboard.
                      Registering a device does not start work.
                    </p>
                  </div>
                </>
              )}
              {view === "jobs" && (
                <Jobs
                  request={request}
                  canSubmit={roles.some((r) =>
                    ["operator_admin", "workload_submitter"].includes(r),
                  )}
                  contributor={
                    !operator && !roles.includes("workload_submitter")
                  }
                />
              )}
              {view === "activity" && (
                <section className="panel">
                  <div className="panel-heading">
                    <h2>Recent events</h2>
                    <span className="muted">Latest 200 records</span>
                  </div>
                  <div className="table-wrap">
                    <table>
                      <thead>
                        <tr>
                          <th>Event</th>
                          <th>Object</th>
                          <th>Time</th>
                        </tr>
                      </thead>
                      <tbody>
                        {events.map((e) => (
                          <tr key={e.id}>
                            <td>{e.action}</td>
                            <td>
                              <code>{e.object_id}</code>
                            </td>
                            <td>{new Date(e.created_at).toLocaleString()}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </section>
              )}
              {view === "setup" && (
                <section className="panel setup">
                  <h2>Enroll a contributor device</h2>
                  <ol>
                    <li>
                      <h3>Configure your identity provider</h3>
                      <p>
                        An administrator creates company accounts in Keycloak
                        and assigns contributor or operator roles.
                      </p>
                    </li>
                    <li>
                      <h3>Review and accept device enrollment</h3>
                      <p>
                        The agent shares OS, architecture, CPU count, memory
                        capacity, agent version, and connectivity with this
                        operator. Execution requires separate local sharing
                        consent. Availability alone is not completed compute.
                        Participation is voluntary; stop the agent locally at
                        any time.
                      </p>
                      {roles.includes("contributor") && (
                        <button
                          className="primary"
                          disabled={consented || busy}
                          onClick={() => void consent()}
                        >
                          {consented
                            ? "Enrollment consent recorded"
                            : busy
                              ? "Recording…"
                              : "I agree to device enrollment"}
                        </button>
                      )}
                      <small>Consent version: {config?.consent_version}</small>
                    </li>
                    <li>
                      <h3>Run the Linux agent</h3>
                      <p>
                        Use the repository’s operator guide to authenticate in
                        your system browser and enroll the agent. The agent
                        stores a device key locally and opens an outbound
                        connection.
                      </p>
                      <pre>
                        opencompute-agent login --gateway http://localhost:8080
                        {"\n"}opencompute-agent enroll --gateway
                        http://localhost:8080 --name my-linux-pc
                        --accept-consent{"\n"}
                        opencompute-agent run
                      </pre>
                    </li>
                  </ol>
                  <div className="notice">
                    Device enrollment does not enable compute execution.
                    Resource-sharing consent will be requested separately when
                    sessions are implemented.
                  </div>
                </section>
              )}
            </>
          )}
          <footer>
            <span>OpenCompute · Independent by design</span>
            <span>Protocol v1 · Linux enrollment preview</span>
          </footer>
        </main>
      </div>
    </div>
  );
}
