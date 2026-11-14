# Initial threat model

## Boundaries

The operator controls the API, identity provider and data stores. Device owners control their own machines. Devices, capability claims, agent clocks and future results are untrusted. No proprietary workload confidentiality is promised on consumer machines. Separate deployments are separate trust domains; IP addresses are never device identities.

## Enrollment controls

OIDC verifies issuer, audience, allowed algorithms, expiry and subject. Roles come only from that trusted issuer's `realm_access.roles`. Registration requires current explicit consent and an Ed25519 signature. Auth signatures include the operator UUID; challenges are random, short-lived and consumed transactionally. Device credentials are hashed at rest, expire within five minutes, and are checked on every WebSocket message. Revocation serializes with issuance and invalidates credentials. Owner queries include owner IDs. Logs redact authorization/cookies and avoid exception strings containing connection secrets.

The gateway rejects oversized messages, unknown fields/versions, forged device IDs, non-increasing sequences, repeated HELLO and missing initial capabilities. Connections time out after 45 seconds without messages. Agent state uses private directories/files, rejects symlink files, disables HTTP redirects, and requires HTTPS except loopback development. Local stop interrupts connection retries.

## Known gaps and deployment limits

- Development Compose exposes services on loopback and uses HTTP internally. Do not expose it publicly. Production needs a TLS 1.3 gateway/reverse proxy, authenticated internal services, network isolation, hardened infrastructure and operator-owned keys.
- Initial gateway presence/rate limits/revocation routing are single-process. HA and distributed rate limits are M6. Device-auth IP rate limits do not authenticate devices and may affect shared NATs. Human API quotas use verified OIDC subjects so console users do not share a proxy bucket.
- Devices can lie about hardware. Inventory is not verified compute capacity and earns no credits.
- Private file permissions are not a replacement for OS keychain storage. Agent/installer/config signing, rotation and downgrade checks are pending M4; binaries must not be called signed releases yet.
- No runtime, scheduler, availability session, metering, credit settlement or artifact execution path is enabled. Enrollment consent cannot authorize those later features.
- Consent/legal text is a development disclosure; operators must provide their own legal terms, retention and deletion workflow before production.
- Audit rows are application append-only, but a database administrator can alter them. Hash chaining, retention and restricted production database roles are later work.
- Dependency/infra licenses and security advisories require operator review. Compose pins versions; production use additionally requires immutable digests and an upgrade policy.

## Required future adversarial tests

M2/M3: offline Stop/expiry, forged usage, runaway workloads, stale leases, duplicate events, corrupt artifacts and zero duplicate settlements. M4: malicious configuration, retargeting, revoked releases and downgrade attacks. M5: filesystem/network escape, host credentials, VM isolation and hostile GPU workloads. M6: unauthorized peers, cache poisoning, relay abuse, backup restoration and partial outages.
