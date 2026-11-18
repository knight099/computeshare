# Windows and Linux host support

First production targets: Windows and Linux, owner decision 2026-11-17. macOS is deferred. This is a release gate matrix, not an assertion that all targets already work.

| Capability                         | Linux                                                | Windows                                                                                                                                           |
| ---------------------------------- | ---------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| Current native test target         | Ubuntu 24.04 CI, cgroup v2                           | Windows Server 2022 CI; Windows 10/11 x64 client acceptance still required                                                                        |
| Shared enrollment / device proof   | Verified against real Keycloak/API                   | Native enrollment/device-proof fixture tests passed in CI 44221909237; real company identity/browser acceptance pending                           |
| State protection                   | Private regular files, no final symlinks, 0600/0700  | User-bound DPAPI ciphertext; user/SYSTEM protected DACLs, explicit owner on creation, final reparse points rejected; native CI 44221909237 passed |
| Session locking                    | flock                                                | LockFileEx; native CI 44221909237 passed                                                                                                          |
| Local lifetime / expiry            | Kernel boot ID and uptime, UTC deadline              | GetTickCount64 and process-lifetime ID plus UTC deadline; restarting cannot resume prior consent                                                  |
| Bounded built-in execution         | Delegated CPU/RAM/swap/pids cgroups required; tested | Disabled: no executable runtime advertised, local sharing rejected                                                                                |
| Arbitrary company workloads / GPUs | Disabled pending reviewed runtime                    | Disabled pending reviewed runtime                                                                                                                 |
| Production installers / updates    | Signing/release acceptance pending                   | Signing/release acceptance pending                                                                                                                |

The Windows baseline is an enrollment preview until native storage, identity and connection acceptance passes. Cross-compilation and CI on a server OS do not replace tests on supported consumer Windows versions. ARM and other distributions require explicit compatibility evidence before support claims.

## Platform boundaries

One Go agent shares protocol, consent/session state machine, outbound connection, workload validation and local dashboard. Build-tagged functions own file protection/locking, inventory/clock and execution. The Linux adapter preserves existing cgroup enforcement and supervisor-death behavior. Windows never substitutes an ordinary process for enforced compute.

Use a private local user-profile directory with trusted parent directories. UNC/device paths and junction/reparse path components are rejected. Existing directory/file permissions are validated by handle and never rewritten; overly broad ACLs are rejected. Creation assigns private ACLs and ownership immediately. Like ordinary path-based file APIs, this assumes ancestors cannot be maliciously replaced concurrently by an untrusted principal.

Windows files use a versioned DPAPI envelope and refuse plaintext fallback or corrupt ciphertext. Protecting with the current user's keys binds the files to that account/machine context; copying a Linux state directory into Windows is not migration. Enroll a fresh device identity through the chosen company. Device-owner/admin compromise remains outside confidentiality guarantees on an untrusted host.

## Next Windows acceptance

1. Native DPAPI/DACL/reparse-point, file-lock, clock and enrollment/device-proof tests.
2. Real company OIDC browser login, enrollment, heartbeat/reconnect, revocation and local controls on Windows 10/11 x64.
3. Enforced Job Object CPU/memory/process accounting, atomic assignment before execution, parent-death kill, local Stop and suspended/resumed expiry tests before enabling built-in work.
4. General-workload isolation in a reviewed VM boundary, denied host filesystem/credentials and default-denied network. Job Objects alone are resource controls, not a sandbox for arbitrary code.
5. Company-bound signed installers/update manifests, tamper/revocation/downgrade checks and reproducible release artifacts. No fabricated signing certificate or security-review evidence.

## Latest native evidence (2026-11-18)

Windows CI 44221909237 passed storage encryption/tamper/plaintext rejection, existing ACL preservation, broad file/lock ACL rejection, lock serialization, intermediate-junction rejection, inventory/lifetime clock, enrollment/device proof, and rejection of direct worker execution. The open-reader replacement test first failed in CI 44189215468; the fix uses FileRenameInfoEx replace/POSIX semantics and verifies both old-reader snapshots and preservation of prior state when deletion is explicitly denied. The full Linux Compose regression also passed on this commit. These results do not enable Windows compute or certify Windows 10/11 client devices.

Implementation sequence and required failure cases: [Windows bounded execution acceptance plan](windows-execution-plan.md).

The next Job Object resource-configuration helpers are under native acceptance. Querying configured limits is not proof of worker enforcement; no Windows runtime is enabled until atomic process assignment, limit-pressure and lifetime tests pass.
