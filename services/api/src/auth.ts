import { createRemoteJWKSet, jwtVerify } from "jose";
import type { FastifyRequest } from "fastify";
import type { Config } from "../../../packages/config/src/index.ts";

export class HttpError extends Error {
  constructor(
    public statusCode: number,
    message: string,
  ) {
    super(message);
  }
}
export type Principal = { subject: string; roles: string[] };
export function oidcVerifier(config: Config) {
  const jwks = createRemoteJWKSet(
    new URL(
      config.OIDC_JWKS_URL ??
        `${config.OIDC_ISSUER}/protocol/openid-connect/certs`,
    ),
  );
  return async (request: FastifyRequest): Promise<Principal> => {
    const authorization = request.headers.authorization;
    if (!authorization?.startsWith("Bearer "))
      throw new HttpError(401, "Authentication required");
    try {
      const { payload } = await jwtVerify(authorization.slice(7), jwks, {
        issuer: config.OIDC_ISSUER,
        audience: config.OIDC_AUDIENCE,
        algorithms: ["RS256", "ES256"],
        requiredClaims: ["sub", "exp", "iat"],
      });
      if (!payload.sub || payload.sub.length > 255)
        throw new Error("Invalid subject");
      const access = payload.realm_access as { roles?: unknown } | undefined;
      const roles = Array.isArray(access?.roles)
        ? access.roles.filter((r): r is string => typeof r === "string")
        : [];
      return { subject: payload.sub, roles };
    } catch {
      throw new HttpError(401, "Invalid or expired access token");
    }
  };
}
export function requireRole(principal: Principal, roles: string[]) {
  if (!roles.some((role) => principal.roles.includes(role)))
    throw new HttpError(403, "Insufficient permissions");
}
