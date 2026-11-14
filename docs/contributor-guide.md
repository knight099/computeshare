# Contributor enrollment preview

This release registers a Linux device and reports its connectivity. It **does not execute compute**, consume a sharing session, or award credits.

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

Availability, resource controls, credits, device diagnostics exports, desktop packaging and OS credential storage arrive in later stages. Enrollment is not consent to these future capabilities.
