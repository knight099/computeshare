# Verification evidence — enrollment preview

Date: 2026-11-14. This records checks executed locally and in GitHub Actions, not future milestone acceptance. CI evidence: [implementation commit a4e9028](https://github.com/knight099/computeshare/actions/runs/43633202994).

| Check                                                          | Observed result                                                                                       |
| -------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `pnpm lint` (same package script executed through npm locally) | ESLint and Prettier passed                                                                            |
| `pnpm typecheck`                                               | Strict TypeScript passed                                                                              |
| `pnpm test`                                                    | 4 named tests passed: config bounds/redaction, protocol validation, bounded probe cancellation        |
| `TEST_DATABASE_URL=… pnpm test:integration`                    | 11 named tests passed against real PostgreSQL 17.6                                                    |
| `go test -race ./...` in `agents/node-agent`                   | 4 tests passed, race detector enabled                                                                 |
| `go vet ./...` and `gofmt` check                               | Passed                                                                                                |
| `go build`                                                     | Linux agent binary built successfully                                                                 |
| `pnpm build`                                                   | Strict typecheck and Next.js production build passed                                                  |
| Compose dependencies                                           | PostgreSQL, Redis and NATS started locally                                                            |
| `make dev` full stack                                          | Not passed locally: remaining image/container creation exhausted the 8 GB filesystem using Docker VFS |
| Real Keycloak/CLI/console E2E                                  | Script and CI job added; not passed locally because the full stack could not start                    |
| Browser console sign-in and reconnect                          | Not verified; console HTML smoke is not a browser-flow test                                           |

Integration tests use an isolated disposable PostgreSQL schema and a signed RSA/JWKS identity fixture. They exercise real HTTP/WebSocket server behavior and real database transactions, but the fixture is not evidence that the real Keycloak flow passed. Tests cover wrong issuer/audience/expiry, explicit consent and proof, cross-user IDOR/revocation, challenge replay, credential hashing/expiry, gateway identity and sequence replay, revocation disconnect, audit records, migration binding, idle database failure/reconnection, bounded stalled queries, and public traffic not exhausting authenticated users through a shared console proxy.

## Review findings and regression checks

A separate read-only reviewer inspected the foundation/enrollment changes. Three Important findings were addressed: handled idle PostgreSQL pool errors, bounded dependency probes, and separate verified-principal API rate limits. Regression tests reproduced the shared-proxy limit failure before the fix and verified the corrected behavior; database/probe tests cover lost idle connections and stalled operations. The full suite passed after fixes. The UI command was corrected to include explicit enrollment consent.

## Environment failures and retained gates

The initially selected MinIO images were unavailable from the tested registries. ADR-009 records the switch to SeaweedFS S3; no mock storage was substituted. Attempts to instantiate the complete identity/storage stack then failed due to disk exhaustion under Docker VFS. A realm-file permission failure observed in an earlier custom Keycloak image was corrected by using the official image and a read-only, checkout-readable realm file.

Automatic review rejected broad Docker pruning because it could remove unrelated resources. Cleanup used specifically identified task-owned images/cache records instead. Active data services and persistent volumes were preserved.

M0 foundation acceptance passed in CI. The initial M1 native enrollment flow also passed. The complete M1 gate remains open for browser console PKCE and reconnect verification, plus the follow-ups in the implementation checklist. M2–M6 implementation has not begun, following the master specification’s first-delivery work order and sequential acceptance rule. No production, compute-execution, credit, signing or sandbox-readiness claim is made.
