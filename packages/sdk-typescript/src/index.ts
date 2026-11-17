/** Technical workload API. Authentication and commercial systems belong to the caller. */
export type JobState =
  "QUEUED" | "RUNNING" | "VERIFYING" | "SUCCEEDED" | "FAILED" | "CANCELED";
export type JobSummary = {
  id: string;
  workload_type: "synthetic-hash-v1";
  state: JobState;
  created_at: string;
  result_hash: string | null;
};
export type Job = JobSummary & {
  timeout_seconds: number;
  payload: { seed: string; iterations: number };
  attempts?: {
    id: string;
    device_id: string;
    state: string;
    epoch: number;
    result_hash: string | null;
  }[];
};
export type JobPage = { jobs: JobSummary[]; next_cursor: string | null };
export type PageOptions = { limit?: number; cursor?: string };
export type SyntheticJobInput = {
  idempotency_key: string;
  seed: string;
  iterations: number;
  timeout_seconds?: number;
};
export type UsageRecord = {
  id: string;
  job_id: string;
  attempt_id: string;
  device_id: string;
  session_id: string;
  output_verified: true;
  logical_iterations: number;
  cpu_usage_usec: string | null;
  memory_peak_bytes: string | null;
  server_elapsed_usec: string;
  telemetry_status: "NOT_REPORTED" | "REPORTED_UNTRUSTED" | "REJECTED";
  rejection_reason: string | null;
  policy_snapshot: {
    cpu_percent: number;
    memory_mb: number;
    gpu_enabled: false;
    idle_only: false;
  };
  observed_at: string;
};
export type UsagePage = { records: UsageRecord[]; next_cursor: string | null };
export class OpenComputeError extends Error {
  constructor(
    public readonly status: number,
    public readonly traceId?: string,
  ) {
    super(`OpenCompute request failed (HTTP ${status})`);
    this.name = "OpenComputeError";
  }
}
export class OpenComputeClient {
  private readonly base: string;
  constructor(
    baseURL: string,
    private readonly token: () => string | Promise<string>,
    private readonly timeoutMs = 15000,
  ) {
    const u = new URL(baseURL);
    if (
      u.username ||
      u.password ||
      u.search ||
      u.hash ||
      u.pathname !== "/" ||
      !(
        u.protocol === "https:" ||
        (u.protocol === "http:" &&
          ["localhost", "127.0.0.1", "[::1]"].includes(u.hostname))
      )
    )
      throw new Error(
        "Invalid company API URL: HTTPS origin required except loopback",
      );
    if (!Number.isFinite(timeoutMs) || timeoutMs < 1 || timeoutMs > 60000)
      throw new Error("Invalid request timeout");
    this.base = u.origin;
  }
  private async request<T>(
    path: string,
    method = "GET",
    body?: unknown,
  ): Promise<T> {
    const bearer = await this.token();
    if (!bearer || /[\r\n]/.test(bearer))
      throw new Error("Invalid access token");
    const response = await fetch(this.base + path, {
      method,
      redirect: "error",
      signal: AbortSignal.timeout(this.timeoutMs),
      headers: {
        Authorization: `Bearer ${bearer}`,
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!response.ok) {
      let trace: string | undefined;
      try {
        const error: unknown = await response.json();
        if (
          typeof error === "object" &&
          error !== null &&
          "trace_id" in error &&
          typeof error.trace_id === "string"
        )
          trace = error.trace_id;
      } catch {
        /* Body is optional and never included in errors. */
      }
      throw new OpenComputeError(response.status, trace);
    }
    return response.json() as Promise<T>;
  }
  submitSyntheticJob(input: SyntheticJobInput): Promise<Job> {
    if (!input.idempotency_key)
      throw new Error("Explicit idempotency key required");
    return this.request("/v1/jobs", "POST", {
      ...input,
      workload_type: "synthetic-hash-v1",
    });
  }
  getJob(id: string): Promise<Job> {
    return this.request(`/v1/jobs/${encodeURIComponent(id)}`);
  }
  cancelJob(id: string): Promise<{ canceled: boolean }> {
    return this.request(`/v1/jobs/${encodeURIComponent(id)}/cancel`, "POST");
  }
  private page<T>(path: string, options: PageOptions): Promise<T> {
    const query = new URLSearchParams();
    if (options.limit !== undefined) query.set("limit", String(options.limit));
    if (options.cursor !== undefined) query.set("cursor", options.cursor);
    return this.request(path + (query.size ? "?" + query.toString() : ""));
  }
  listJobs(options: PageOptions = {}): Promise<JobPage> {
    return this.page<JobPage>("/v1/operator/jobs", options);
  }
  listContributorJobs(options: PageOptions = {}): Promise<JobPage> {
    return this.page<JobPage>("/v1/contributor/jobs", options);
  }
  listUsage(options: PageOptions = {}): Promise<UsagePage> {
    return this.page<UsagePage>("/v1/operator/usage", options);
  }
  listContributorUsage(options: PageOptions = {}): Promise<UsagePage> {
    return this.page<UsagePage>("/v1/contributor/usage", options);
  }
}
