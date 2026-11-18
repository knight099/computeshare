# Cancelable idle wait implementation plan

Goal: explicit timed consent may wait for the device to become idle, with no work or company session before eligibility. The original click starts the expiry clock. Stop from any tab, CLI Stop, agent shutdown/restart, expiry, unavailable required sensors or unsafe power/temperature cancel the wait. Eligibility activates at most once; a later safety stop never rearms it.

Approach: the agent owns the pending STARTING generation and cancellation context. A browser-only timer would miss Stop from other tabs and is rejected. Reuse existing session preparation, safety sampler, generation/adoption fence and company start exchange. Add `wait_for_idle` to local Start input and `waiting_idle` to local diagnostics; an optional original `consent_expires_at` on session creation binds company availability to the same deadline. Older company APIs fail closed and must be upgraded. Persist STARTING so older agents also refuse restart resumption. Bound company exchange to5s after idle; no network under the session mutex. Selected duration includes waiting.

- [x] Add failing tests for local wait without network, Stop/CLI/shutdown/restart cancellation, invalid guards, unsafe/missing readings, expiry and one-shot activation through the existing protocol fixture.
- [x] Extract prepared-session completion from start; implement agent-owned wait using the existing sampler and immutable boot/wall deadline. Keep generation and context ownership in the session manager.
- [x] Wire dashboard opt-in idle wait, visible waiting/remaining time and cancellation; snapshot submitted settings, prevent duplicate submits, and use the existing local API protections.
- [x] Run focused tests, whole-agent race, Windows cross-build/vet, native CI and Linux Compose acceptance; obtain one final review and record evidence in the engineering tracker.

Actual consumer desktop/suspend certification and signed releases remain separate. Public Windows compute stays disabled. No new runtime, scheduler or financial subsystem.

Review corrections: preserve CLI Stop when persisting the armed marker; separate pending-start cancellation from worker lifetime; Stop returns the captured prior session atomically and only contacts the company for an ACTIVE session; recheck boot/wall deadlines after sensing and bound authentication/creation by remaining consent. The API accepts an optional UTC deadline and caps it by the duration instead of granting a fresh full-duration window after waiting. This additive field is a necessary plan correction: local-only expiry would leave company availability overstated. No safety sensor readings leave the device.

Verification: e6dc654 passed all3 CI44419447561 jobs, including native Server2022 idle activation/cancellation, real PostgreSQL deadline/rollback integration, and full Linux Compose acceptance. Local whole-agent race passed33.831s and Go vet passed. Consumer desktop physical transitions and signed distribution are not certified by these fixtures.
