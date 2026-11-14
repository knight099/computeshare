# Troubleshooting

- **Stack not ready:** run `docker compose ps -a`, `docker compose logs --tail=50 api identity migrate storage`, and inspect `/readyz`. Liveness is intentionally independent. Redact credentials before sharing logs.
- **Identity startup fails:** the realm JSON must be readable (0644). It contains no secrets. Realm import happens on first startup; edit existing clients via the admin console rather than deleting identity data.
- **No space left:** Docker VFS duplicates image/container files. Reserve at least 12 GB free disk; more for VFS. Remove only disposable task caches or stopped task containers. Do not delete database/artifact volumes as a workaround.
- **Registry/network unavailable:** confirm the pinned images can be pulled. No unsigned fallback or mock service is substituted. Configure an operator-approved mirror if needed.
- **Migration operator mismatch:** restore the original `OPERATOR_ID`. Provision a different database for a different operator.
- **401/403:** check issuer/audience, user role, token expiry and explicit current consent. Device credentials expire after five minutes; the agent reconnects automatically. Do not disable validation.
- **Agent won't enroll:** sign in again, explicitly accept consent, and ensure no existing device state is being replaced. Private state must be 0700/0600 without symlinks. Remote gateways require HTTPS.
- **Offline after stopping:** presence uses a 45-second heartbeat grace period. Inventory does not imply an active availability session.
- **Browser login callback unavailable:** port 8787 must be free on the contributor's loopback interface. No public inbound rule or router change is required.
- **Unit tests report only file names:** use the provided Node 24 `--test-isolation=none` scripts; named assertions must appear. An empty file-level pass is not accepted as test evidence.
