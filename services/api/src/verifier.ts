import { Worker } from "node:worker_threads";
import { HttpError } from "./auth.ts";
type Task = {
  seed: string;
  iterations: number;
  resolve: (v: string) => void;
  reject: (e: Error) => void;
  timer: ReturnType<typeof setTimeout>;
  worker?: Worker;
  result?: string;
  error?: Error;
  done: boolean;
};

export class HashVerifier {
  private queue: Task[] = [];
  private active = new Map<Worker, Task>();
  private closed = false;
  constructor(
    private concurrency = 2,
    private queued = 32,
    private timeout = 10000,
  ) {}
  async compute(seed: string, iterations: number): Promise<string> {
    if (this.closed) throw new HttpError(503, "Verifier closed");
    if (
      this.active.size >= this.concurrency &&
      this.queue.length >= this.queued
    )
      throw new HttpError(503, "Verification capacity unavailable");
    return new Promise((resolve, reject) => {
      const task: Task = {
        seed,
        iterations,
        resolve,
        reject,
        done: false,
        timer: setTimeout(() => {
          task.error = new HttpError(503, "Verification timed out");
          if (task.worker) void task.worker.terminate();
          else {
            this.queue = this.queue.filter((t) => t !== task);
            task.done = true;
            reject(task.error);
          }
        }, this.timeout),
      };
      this.queue.push(task);
      this.pump();
    });
  }
  private pump() {
    while (
      !this.closed &&
      this.active.size < this.concurrency &&
      this.queue.length
    ) {
      const task = this.queue.shift()!;
      const worker = new Worker(new URL("./hash-worker.mjs", import.meta.url), {
        workerData: { seed: task.seed, iterations: task.iterations },
        execArgv: [],
      });
      task.worker = worker;
      this.active.set(worker, task);
      worker.once("message", (value: unknown) => {
        if (typeof value === "string" && /^[a-f0-9]{64}$/.test(value))
          task.result = value;
        else task.error = new HttpError(503, "Verifier failed");
      });
      worker.once("error", () => {
        task.error = new HttpError(503, "Verifier failed");
      });
      worker.once("exit", (code) => {
        this.active.delete(worker);
        clearTimeout(task.timer);
        if (!task.done) {
          task.done = true;
          if (code === 0 && task.result && !task.error)
            task.resolve(task.result);
          else task.reject(task.error ?? new HttpError(503, "Verifier failed"));
        }
        this.pump();
      });
    }
  }
  async close() {
    this.closed = true;
    for (const task of this.queue) {
      clearTimeout(task.timer);
      task.done = true;
      task.reject(new HttpError(503, "Verifier closed"));
    }
    this.queue = [];
    await Promise.all(
      [...this.active].map(([worker, task]) => {
        task.error = new HttpError(503, "Verifier closed");
        return worker.terminate();
      }),
    );
  }
}
