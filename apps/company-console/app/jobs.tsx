"use client";
import { useEffect, useState } from "react";
type Job = {
  id: string;
  workload_type: string;
  state: string;
  created_at: string;
  result_hash: string | null;
};
export default function Jobs({
  request,
  canSubmit,
  contributor,
}: {
  request: (path: string, options?: RequestInit) => Promise<{ jobs?: Job[] }>;
  canSubmit: boolean;
  contributor: boolean;
}) {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let active = true;
    const refresh = async () => {
      try {
        const data = await request(
          contributor ? "/v1/contributor/jobs" : "/v1/operator/jobs",
        );
        if (active) setJobs(data.jobs ?? []);
      } catch (e) {
        if (active)
          setError(e instanceof Error ? e.message : "Unable to load jobs");
      }
    };
    void refresh();
    const timer = setInterval(() => void refresh(), 2000);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, [request, contributor]);
  async function submit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const data = new FormData(e.currentTarget);
      await request("/v1/jobs", {
        method: "POST",
        body: JSON.stringify({
          idempotency_key: crypto.randomUUID(),
          workload_type: "synthetic-hash-v1",
          seed: String(data.get("seed")),
          iterations: Number(data.get("iterations")),
          timeout_seconds: 10,
        }),
      });
    } catch (e) {
      setError(e instanceof Error ? e.message : "Submission failed");
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <div className="notice">
        Synthetic CPU preview · public inputs only · CPU/RAM limits and local
        consent required · no credits or arbitrary code.
      </div>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {canSubmit && (
        <section className="panel setup">
          <h2>Submit a safe hash job</h2>
          <form onSubmit={(e) => void submit(e)} className="job-form">
            <label>
              Public seed{" "}
              <input
                name="seed"
                defaultValue="public-demo"
                maxLength={64}
                pattern="[a-zA-Z0-9 _.\-]+"
                required
              />
            </label>
            <label>
              Hash iterations{" "}
              <input
                name="iterations"
                type="number"
                defaultValue={10000}
                min={1}
                max={1000000}
                required
              />
            </label>
            <button className="primary" disabled={busy}>
              {busy ? "Submitting…" : "Submit synthetic job"}
            </button>
          </form>
        </section>
      )}
      <section className="panel" style={{ marginTop: 24 }}>
        <div className="panel-heading">
          <h2>{contributor ? "Your job history" : "Job queue and results"}</h2>
          <span className="muted">Refreshes every 2 seconds</span>
        </div>
        {jobs.length ? (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Job</th>
                  <th>Status</th>
                  <th>Result hash</th>
                </tr>
              </thead>
              <tbody>
                {jobs.map((j) => (
                  <tr key={j.id}>
                    <td>
                      {j.workload_type}
                      <code>{j.id}</code>
                    </td>
                    <td>
                      <span
                        className={
                          "pill " + (j.state === "SUCCEEDED" ? "online" : "")
                        }
                      >
                        {j.state}
                      </span>
                    </td>
                    <td>
                      <code>{j.result_hash ?? "—"}</code>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="empty">
            <h3>No jobs yet</h3>
            <p>
              Jobs run only during a contributor’s locally approved availability
              session.
            </p>
          </div>
        )}
      </section>
    </>
  );
}
