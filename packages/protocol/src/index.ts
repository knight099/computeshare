import { z } from "zod";

export const capabilitiesSchema = z
  .object({
    os: z.enum(["linux", "windows", "darwin"]),
    arch: z.string().min(1).max(32),
    cpu_cores: z.number().int().min(1).max(4096),
    memory_mb: z.number().int().min(1).max(16777216),
    agent_version: z.literal("0.1.0"),
    runtimes: z.array(z.enum(["synthetic-hash-v1"])).max(1),
  })
  .strict();

export const controlMessageSchema = z
  .object({
    protocol_version: z.literal(1),
    type: z.enum([
      "HELLO",
      "HEARTBEAT",
      "WORK_REQUEST",
      "JOB_ACCEPT",
      "JOB_START",
      "JOB_COMPLETE",
      "JOB_FAIL",
    ]),
    device_id: z.string().uuid(),
    sequence: z.number().int().nonnegative().safe(),
    trace_id: z.string().uuid(),
    capabilities: capabilitiesSchema.optional(),
    session: z
      .object({
        id: z.string().uuid(),
        status: z.enum(["ACTIVE", "STOPPED", "EXPIRED"]),
      })
      .strict()
      .optional(),
    attempt: z
      .object({
        id: z.string().uuid(),
        epoch: z.number().int().positive(),
        result: z
          .string()
          .regex(/^[a-f0-9]{64}$/)
          .optional(),
      })
      .strict()
      .optional(),
  })
  .strict();

export type Capabilities = z.infer<typeof capabilitiesSchema>;
export type ControlMessage = z.infer<typeof controlMessageSchema>;
export const CONSENT_VERSION = "2026-11-14";
