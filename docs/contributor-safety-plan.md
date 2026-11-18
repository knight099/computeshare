# Local contributor safety controls

Scope: optional AC-only, current desktop-session idle minimum, and CPU sensor ceiling. These only restrict existing timed CPU/RAM consent. They stay on the device; company requests cannot set or relax them. No automatic resumption after a safety stop. Resource-policy and server protocol remain unchanged.

Implementation choice: stop the existing session through its single cancellation path when a required sensor is unsafe, missing, invalid or stale. Starting and dispatch require a fresh eligible observation. A bounded single-flight sampler runs outside the session mutex; a stuck sensor cannot block local Stop or create an unbounded goroutine queue. Poll cache at 500ms, expire observations after2s, bound startup wait to1s.

Windows uses documented power status and last-input APIs for a active console, non-remote interactive session; unavailable CPU temperature is reported honestly. Linux uses bounded sysfs power/CPU sensor reads and a bounded loginctl query for the actual caller’s active, seated, local, same-user graphical session (`loginctl self`; unsupported logind versions report unavailable). No keystrokes, window names, process lists or sensor serials are collected. Unknown is never interpreted as safe for an enabled guard.

Guarded ACTIVE state is stored with a versioned local status marker that old clients cannot treat as ACTIVE. A new agent also stops rather than resumes that record. All session persistence must use one owner. The local dashboard shows sensor availability and opt-in controls, and explains that a safety stop needs another explicit start. Existing unguarded consent semantics and the Windows public execution gate remain unchanged.

Sequence: tests for policy evaluation, sampler bounds/staleness, cancellation and persistence; shared engine and platform readers; session/start/dispatch/watch integration; local dashboard; native/source/full-stack verification and one final review. Verify real client sensor/desktop/suspend behavior separately before production claims.

Idle eligibility is checked at Start, not armed for later automatic execution. Interacting with the local dashboard normally resets desktop idle, so the idle option is a strict control API primitive pending an explicit, cancelable “wait until idle” client flow and real desktop acceptance; do not advertise that UI flow as complete. No background timer may silently create or renew consent.

Verification: a3ed1cc passed all three CI44380369106 jobs: native Windows, source/integration, and full Linux Compose acceptance. The power-loss protocol fixture uses the actual protected Windows worker, a synthetic sensor source and blocked company ACK; it proves cancellation wiring, not physical battery/desktop transitions. See the engineering tracker for measured results and remaining client gates.
