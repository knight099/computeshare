import { z } from "zod";
import { HttpError } from "./auth.ts";
const cursorSchema = z
  .object({ time: z.iso.datetime({ precision: 6 }), id: z.string().uuid() })
  .strict();
export const historyPageSchema = z
  .object({
    limit: z.coerce.number().int().min(1).max(200).default(200),
    cursor: z
      .string()
      .max(512)
      .regex(/^[A-Za-z0-9_-]+$/)
      .optional(),
  })
  .strict();
export function decodeHistoryCursor(value?: string) {
  if (!value) return undefined;
  try {
    return cursorSchema.parse(
      JSON.parse(Buffer.from(value, "base64url").toString("utf8")),
    );
  } catch {
    throw new HttpError(400, "Invalid history cursor");
  }
}
export function encodeHistoryCursor(time: string, id: string) {
  return Buffer.from(JSON.stringify({ time, id })).toString("base64url");
}
