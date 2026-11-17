# Contributor enrollment and bounded compute preview

This release registers a Linux device and reports its connectivity. Enrollment alone **never starts compute**. Bounded built-in CPU work requires a separate explicitly consented sharing session with local time/resource limits and Stop controls.

By enrolling, you explicitly allow your chosen operator to store your account identifier, device name, device public key, operating system, architecture, CPU count, total RAM, agent version, registration time, and heartbeat timestamps. The operator records consent and security events. No personal files, browser data, clipboard, camera, microphone, or workload execution permission is requested.

Build with `make build`, then:

```sh
bin/opencompute-agent login --gateway http://localhost:8080
bin/opencompute-agent enroll --gateway http://localhost:8080 --name my-linux-pc --accept-consent
bin/opencompute-agent run
```

Only pass `--accept-consent` after reviewing this disclosure and your operator's legal/privacy terms. For remote deployments use HTTPS. Login opens the company identity provider in your system browser; if opening fails, use the printed sign-in URL. No password is collected by the agent.

The default state directory is `$XDG_CONFIG_HOME/opencompute` or `~/.config/opencompute`; it must be private (0700). The device key is a 0600 file. Never share this file or diagnostic bundles containing it. Enrollment refuses to silently replace an existing device/operator binding.

Ctrl+C or SIGTERM stops the agent locally, including during reconnect backoff. Your operator sees offline status after the heartbeat grace period. To revoke the device, use the owner-authenticated API described in `docs/api.md`; revocation prevents reconnection and closes the current connection. Ask the operator about retained data and deletion; local key deletion alone does not remove company records.

Timed availability and CPU/RAM controls are available in the local dashboard. Broader policy controls, diagnostic exports, desktop packaging and OS-backed credential storage remain later stages. Enrollment is not consent to those capabilities. Credits, payments and business management are outside framework scope.

## M2 synthetic sharing preview

The new local contributor dashboard supports explicit 1/2/4/6/8/12-hour availability, worker CPU/RAM limits, current/last job, and observed worker CPU time/peak RAM. It runs on loopback so **Stop sharing now works even when the company API is offline**. Ctrl+C and `opencompute-agent stop` also cancel sharing locally. Availability never renews automatically; reboot expires it, and the local boot clock prevents wall-clock rollback from extending it.

Execution requires Linux cgroup v2 delegation for CPU, memory, swap and processes. On a Linux desktop with a running user systemd manager and those controllers delegated, run:

```sh
make build
systemd-run --user --scope --unit=opencompute-contributor --property=Delegate=yes \
  "$PWD/scripts/contributor-cgroup.sh" "$PWD/bin/opencompute-agent" run
```

Open the printed `http://127.0.0.1:8788` contributor dashboard. Choose duration/limits, read the sharing disclosure, explicitly check consent, and click **Start sharing**. If delegation is unavailable, execution stays disabled; do not run the agent as root or remove limit checks as a workaround. Your operator may need to configure systemd controller delegation for your platform. The helper refuses to operate outside its dedicated `opencompute-contributor.scope`.

Only built-in public deterministic hashes run. Workload inputs contain no paths, scripts, images, credentials or network destinations. CPU limit is a percentage of one worker core; RAM is the worker's hard cgroup limit, with swap disabled and process count bounded. The agent/dashboard itself has separate small overhead. Jobs are canceled on connection loss, Stop, expiry, or timeout. The framework records technical activity and explicitly untrusted CPU/memory reports; it does not manage earnings, credit balances or payouts. Company-controlled retries may run elsewhere after local interruption, but never restart your stopped session.

GPU, idle-only mode, custom disk/network workloads, battery/thermal policies and desktop installers are not enabled in this slice. Those options cannot be requested as enforced policies. Operators must not advertise this preview as a complete production resource-management or arbitrary-workload runtime.
