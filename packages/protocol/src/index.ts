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
    type: z.enum(["HELLO", "HEARTBEAT"]),
    device_id: z.string().uuid(),
    sequence: z.number().int().nonnegative().safe(),
    trace_id: z.string().uuid(),
    capabilities: capabilitiesSchema.optional(),
  })
  .strict();

export type Capabilities = z.infer<typeof capabilitiesSchema>;
export type ControlMessage = z.infer<typeof controlMessageSchema>;
export const CONSENT_VERSION = "2026-11-14";
