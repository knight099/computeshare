import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createContext, runInContext } from "node:vm";

function dashboard() {
  type Element = {
    value: string;
    checked: boolean;
    disabled: boolean;
    textContent: string;
    handlers: Record<
      string,
      (event: { preventDefault(): void }) => Promise<void>
    >;
    addEventListener(name: string, handler: Element["handlers"][string]): void;
  };
  const elements = new Map<string, Element>();
  const element = (id: string): Element => {
    if (!elements.has(id))
      elements.set(id, {
        value: "0",
        checked: false,
        disabled: false,
        textContent: "",
        handlers: {},
        addEventListener(name, handler) {
          this.handlers[name] = handler;
        },
      });
    return elements.get(id)!;
  };
  const state = {
    connected: true,
    execution_available: true,
    session: {
      status: "STOPPED",
      waiting_idle: false,
      expires_at: new Date(Date.now() + 3600000).toISOString(),
    },
    last_result: {},
    csrf: "local",
    host: { ac_online: true, idle_seconds: 0, cpu_millic: null },
  };
  const requests: { path: string; body: Record<string, unknown> }[] = [];
  const context = createContext({
    document: { getElementById: element },
    Date,
    Math,
    Number,
    String,
    Error,
    JSON,
    setInterval() {},
    fetch: async (path: string, options?: { body: string }) => {
      if (options) requests.push({ path, body: JSON.parse(options.body) });
      return { ok: true, json: async () => state };
    },
  });
  const html = readFileSync("agents/node-agent/dashboard.html", "utf8");
  runInContext(
    html.match(/<script nonce="__NONCE__">([\s\S]*?)<\/script>/)![1],
    context,
  );
  return {
    element,
    state,
    requests,
    refresh: () => runInContext("refresh()", context) as Promise<void>,
  };
}

test("contributor idle submission arms an agent-owned wait with a settings snapshot", async () => {
  const ui = dashboard();
  await ui.refresh();
  ui.element("idle").value = "60";
  ui.element("cpu").value = "25";
  ui.element("memory").value = "128";
  ui.element("duration").value = "3600";
  ui.element("consent").checked = true;
  await ui.element("sharing").handlers.submit({ preventDefault() {} });
  assert.equal(ui.requests[0].body.wait_for_idle, true);
  assert.deepEqual(ui.requests[0].body.safety, {
    ac_only: false,
    idle_seconds: 60,
    max_cpu_millic: 0,
  });
  ui.element("idle").value = "600";
  assert.equal(
    (ui.requests[0].body.safety as { idle_seconds: number }).idle_seconds,
    60,
  );
  await ui.element("stop").handlers.click({ preventDefault() {} });
  assert.equal(ui.requests[1].path, "/stop");
});

test("contributor dashboard displays pending consent and prevents a duplicate start", async () => {
  const ui = dashboard();
  ui.state.session.status = "STARTING";
  ui.state.session.waiting_idle = true;
  ui.element("consent").checked = true;
  await ui.refresh();
  assert.equal(ui.element("status").textContent, "WAITING FOR IDLE");
  assert.equal(ui.element("start").disabled, true);
  assert.notEqual(ui.element("remaining").textContent, "—");
});
