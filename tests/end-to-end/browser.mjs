/* global sessionStorage */
import assert from "node:assert/strict";
import { chromium } from "playwright";

// Runs against real Keycloak and the actual Next.js console, with no token or API mocks.
export async function verifyBrowserLogin({
  consoleURL,
  identity,
  username,
  password,
  deviceName,
}) {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext();
    const page = await context.newPage();
    page.setDefaultTimeout(20000);
    await page.goto(consoleURL);
    await page
      .getByRole("button", { name: "Sign in to your organization →" })
      .click();
    await page.locator("#username").fill(username);
    await page.locator("#password").fill(password);
    const exchanged = page.waitForResponse(
      (r) =>
        r.url() ===
        `${identity}/realms/opencompute/protocol/openid-connect/token`,
    );
    await page.locator("#kc-login").click();
    assert.equal((await exchanged).status(), 200);
    await page
      .getByRole("heading", { name: "Device fleet", exact: true })
      .waitFor();
    await page.getByText(deviceName, { exact: true }).waitFor();
    await page.getByRole("button", { name: /Jobs/ }).click();
    await page
      .getByRole("heading", { name: "Submit a safe hash job" })
      .waitFor();
    await page.getByLabel("Public seed").fill("public-browser-job");
    await page.getByLabel("Hash iterations").fill("1000");
    const submitted = page.waitForResponse(
      (r) => r.url().endsWith("/v1/jobs") && r.request().method() === "POST",
    );
    await page.getByRole("button", { name: "Submit synthetic job" }).click();
    assert.equal((await submitted).status(), 201);
    assert.equal(
      new URL(page.url()).search,
      "",
      "callback code must be removed from browser history",
    );
    assert.equal(
      await page.evaluate(() => sessionStorage.getItem("oidc_verifier")),
      null,
    );
    assert.equal(
      await page.evaluate(() => sessionStorage.getItem("oidc_state")),
      null,
    );
    // A blocked cross-origin token exchange must fail closed, even with valid IdP SSO.
    await page.getByRole("button", { name: "Sign out", exact: true }).click();
    await page.route("**/protocol/openid-connect/token", (route) =>
      route.abort("failed"),
    );
    await page
      .getByRole("button", { name: "Sign in to your organization →" })
      .click();
    await page.locator(".error[role=alert]").waitFor();
    assert.equal(
      await page.getByRole("button", { name: "Sign out", exact: true }).count(),
      0,
    );
    await context.close();

    const tampered = await browser.newContext();
    const invalid = await tampered.newPage();
    let tokenRequests = 0;
    invalid.on("request", (r) => {
      if (r.url().endsWith("/protocol/openid-connect/token")) tokenRequests++;
    });
    await invalid.goto(consoleURL + "/?code=forged&state=wrong");
    await invalid.locator(".error[role=alert]").waitFor();
    assert.match(
      await invalid.locator(".error[role=alert]").innerText(),
      /state mismatch/,
    );
    assert.equal(
      tokenRequests,
      0,
      "mismatched state must not reach the token endpoint",
    );
    await tampered.close();
    console.log(
      "PASS: Chromium console PKCE/CORS, authenticated fleet, callback cleanup, tampered state and failed token exchange",
    );
  } finally {
    await browser.close();
  }
}
