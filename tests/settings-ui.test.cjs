"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const root = path.resolve(__dirname, "..");
const html = fs.readFileSync(path.join(root, "settings.html"), "utf8");
const beforeHTML = process.env.MEDIUX_BEFORE_HTML ? fs.readFileSync(process.env.MEDIUX_BEFORE_HTML, "utf8") : null;
const screenshotDirectory = process.env.MEDIUX_SCREENSHOT_DIR || path.join(root, "test-artifacts");
const origin = "https://isolatedexample.test";

const baseConfig = () => ({
  priority_creators: "Alpha",
  excluded_creators: "excluded",
  only_priority_creators: false,
  season_consistency: "MostComplete",
  titlecard_consistency: "MostComplete",
  missing_artwork: "Fallback",
  image_selection: "BestOnly",
  quality: "optimized",
  cache_hours: 24,
  csrf: "test-csrf",
  token_configured: true,
});

function ensure(value, message) {
  assert.equal(Boolean(value), true, message);
}

async function setupPage(browser, options = {}) {
  const context = await browser.newContext({
    colorScheme: options.colorScheme || "light",
    viewport: options.viewport || { width: 1000, height: 800 },
  });
  await context.addCookies([{ name: "silo-session", value: "private", url: origin }]);
  const page = await context.newPage();
  const writes = [];
  const configReads = [];
  let configStatus = options.configStatus || 200;
  let saveStatus = options.saveStatus || 200;
  let saveBody = options.saveBody || baseConfig();
  let config = baseConfig();
  await page.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== origin) return route.abort();
    if (url.pathname === "/settings" && request.method() === "GET") {
      return route.fulfill({ status: 200, contentType: "text/html", body: options.html || html });
    }
    if (url.pathname === "/brand/mediux.svg" || url.pathname === "/brand/baloo-2.woff2") {
      return route.fulfill({ path: path.join(root, url.pathname.slice(1)) });
    }
    if (url.pathname === "/settings/config" && request.method() === "GET") {
      configReads.push(config.csrf);
      return route.fulfill({ status: configStatus, contentType: "application/json", body: JSON.stringify(config) });
    }
    if (url.pathname === "/settings/config" && request.method() === "PUT") {
      const submitted = JSON.parse(request.postData() || "{}");
      writes.push(submitted);
      if (saveStatus >= 200 && saveStatus < 300) {
        config = { ...config, ...submitted, csrf: "new-test-csrf" };
        delete config.api_token;
      }
      return route.fulfill({ status: saveStatus, contentType: "application/json", body: JSON.stringify(saveBody) });
    }
    if (url.pathname === "/creators" && request.method() === "GET") {
      return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [{ value: "WillTong93" }] }) });
    }
    return route.fulfill({ status: 404, contentType: "text/plain", body: "not found" });
  });
  await page.goto(origin + "/settings");
  return { context, page, writes, configReads, setConfigStatus: (value) => { configStatus = value; }, setSaveStatus: (value) => { saveStatus = value; }, setSaveBody: (value) => { saveBody = value; } };
}

async function normalFlow(browser) {
  const test = await setupPage(browser);
  const { page, writes, configReads, context } = test;
  try {
    await page.waitForFunction(() => document.getElementById("status").textContent === "");
    ensure(await page.locator("#token-state").textContent().then((text) => text.includes("already configured")), "configured-token note missing");
    await page.locator("#creator-search").fill("wi");
    await page.waitForTimeout(425);
    await page.getByRole("button", { name: "Add WillTong93" }).click();
    await page.locator("#creator-search").fill("WillTong93");
    await page.getByRole("button", { name: "Add username" }).click();
    ensure((await page.locator("#priority-list li").count()) === 2, "case-insensitive duplicate was added");
    await page.locator("#creator-search").fill("Beta");
    await page.getByRole("button", { name: "Add username" }).click();
    await page.getByRole("button", { name: "Move Beta up" }).focus();
    await page.keyboard.press("Enter");
    await page.getByRole("button", { name: "Remove Alpha" }).focus();
    await page.keyboard.press("Space");
    ensure((await page.locator("#priority-list li").count()) === 2, "remove did not update priority list");
    await page.locator("#only-priority").check();
    await page.locator("#season-consistency").selectOption("CreatorPriority");
    await page.locator("#titlecard-consistency").selectOption("CreatorPriority");
    await page.locator("#cache-hours").fill("48");
    await page.getByRole("button", { name: "Save settings" }).click();
    await page.waitForFunction(() => document.getElementById("status").textContent === "Settings saved.");
    ensure(writes.length === 1, "settings save was not sent");
    const submitted = writes[0];
    assert.equal(submitted.api_token, undefined, "save exposed api_token");
    assert.equal(submitted.priority_creators, "Beta,WillTong93", "priority order was not persisted");
    assert.equal(submitted.only_priority_creators, true, "only-priority setting missing");
    assert.equal(submitted.cache_hours, 48, "cache hours missing");
    assert.equal(submitted.season_consistency, "CreatorPriority", "season creator-first mode missing");
    assert.equal(submitted.titlecard_consistency, "CreatorPriority", "title card creator-first mode missing");
    assert.equal(submitted.csrf, "test-csrf", "csrf missing");
    ensure(configReads.includes("new-test-csrf"), "save did not refresh config with the new CSRF");
    const secondPut = page.waitForResponse((response) => response.url() === origin + "/settings/config" && response.request().method() === "PUT");
    await page.getByRole("button", { name: "Save settings" }).click();
    await secondPut;
    ensure(writes.length === 2, "second settings save was not sent");
    assert.equal(writes[1].csrf, "new-test-csrf", "second save did not use refreshed CSRF");
  } finally { await context.close(); }
}

function normalizedCustomProperty(value) {
  return value.trim().toLowerCase().replaceAll(" ", "");
}

async function mediuxBranding(browser) {
  const defaults = await setupPage(browser, { colorScheme: "light" });
  try {
    await defaults.page.waitForFunction(() => document.querySelector(".mediux-mark img")?.naturalWidth > 0);
    const styles = await defaults.page.evaluate(async () => {
      await document.fonts.ready;
      const root = getComputedStyle(document.documentElement);
      return {
        background: root.getPropertyValue("--background"),
        accent: root.getPropertyValue("--settings-accent"),
        bodyFont: getComputedStyle(document.body).fontFamily,
        fontStatus: [...document.fonts].find(face => face.family.includes("Baloo 2"))?.status,
      };
    });
    assert.equal(normalizedCustomProperty(styles.background), "hsl(22471.4%4.1%)");
    assert.equal(normalizedCustomProperty(styles.accent), "#945af2");
    assert.match(styles.bodyFont, /Baloo 2/);
    assert.equal(styles.fontStatus, "loaded");
  } finally { await defaults.context.close(); }

  const readability = await setupPage(browser);
  try {
    await readability.page.evaluate(() => {
      localStorage.setItem("silo-ui-cache-owner", "profile-20");
      localStorage.setItem("silo-ui-text-scale:profile-20", "x-large");
      localStorage.setItem("silo-ui-text-weight:profile-20", "strong");
      localStorage.setItem("silo-ui-high-contrast:profile-20", "true");
    });
    await readability.page.reload();
    await readability.page.waitForFunction(() => document.getElementById("status").textContent === "");
    const state = await readability.page.evaluate(() => ({
      scale: document.documentElement.dataset.textScale,
      weight: document.documentElement.dataset.textWeight,
      contrast: document.documentElement.dataset.highContrast,
      owner: localStorage.getItem("silo-ui-cache-owner"),
    }));
    assert.deepEqual(state, { scale: "x-large", weight: "strong", contrast: "true", owner: "profile-20" }, "Silo readability cache namespace was not applied");
  } finally { await readability.context.close(); }
}

async function focusVisibility(browser) {
  const test = await setupPage(browser);
  try {
    await test.page.waitForFunction(() => document.getElementById("status").textContent === "");
    const save = test.page.getByRole("button", { name: "Save settings" });
    await save.focus();
    const focus = await save.evaluate((element) => {
      const style = getComputedStyle(element);
      return { focused: document.activeElement === element, outlineStyle: style.outlineStyle, outlineWidth: style.outlineWidth, boxShadow: style.boxShadow };
    });
    ensure(focus.focused, "save button did not receive focus");
    ensure((focus.outlineStyle !== "none" && parseFloat(focus.outlineWidth) > 0) || focus.boxShadow !== "none", "focused control has no visible focus indicator");
  } finally { await test.context.close(); }
}

async function screenshots(browser) {
  fs.mkdirSync(screenshotDirectory, { recursive: true });
  const documents = beforeHTML ? [["before", beforeHTML], ["after", html]] : [["after", html]];
  for (const [label, htmlSource] of documents) {
    for (const [device, viewport] of [["desktop", { width: 1280, height: 1000 }], ["mobile", { width: 375, height: 900 }]]) {
      const test = await setupPage(browser, { html: htmlSource, viewport });
      try {
        await test.page.waitForFunction(() => document.getElementById("status").textContent === "");
        await test.page.screenshot({ path: path.join(screenshotDirectory, `${label}-${device}.png`), fullPage: true });
      } finally { await test.context.close(); }
    }
  }
}

async function authErrors(browser) {
  const load = await setupPage(browser, { configStatus: 401 });
  try {
    await load.page.waitForFunction(() => document.getElementById("status").classList.contains("error"));
    ensure((await load.page.locator("#status").textContent()).includes("sign-in may have expired"), "401 load error was not visible");
  } finally { await load.context.close(); }
  const save = await setupPage(browser, { saveStatus: 403 });
  try {
    await save.page.waitForFunction(() => document.getElementById("status").textContent === "");
    await save.page.getByRole("button", { name: "Save settings" }).click();
    await save.page.waitForFunction(() => document.getElementById("status").classList.contains("error"));
    ensure((await save.page.locator("#status").textContent()).includes("sign-in may have expired"), "403 save error was not visible");
  } finally { await save.context.close(); }
  const server = await setupPage(browser, { saveStatus: 500, saveBody: { error: "Mediux settings storage is temporarily unavailable." } });
  try {
    await server.page.waitForFunction(() => document.getElementById("status").textContent === "");
    await server.page.getByRole("button", { name: "Save settings" }).click();
    await server.page.waitForFunction(() => document.getElementById("status").classList.contains("error"));
    assert.equal(await server.page.locator("#status").textContent(), "Mediux settings storage is temporarily unavailable.", "500 server error was not shown");
  } finally { await server.context.close(); }
}

async function mobile(browser) {
  const test = await setupPage(browser, { viewport: { width: 375, height: 900 } });
  try {
    await test.page.waitForFunction(() => document.getElementById("status").textContent === "");
    const overflow = await test.page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
    assert.equal(overflow, false, "mobile layout has horizontal overflow");
  } finally { await test.context.close(); }
}

(async () => {
  const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH;
  const browser = await chromium.launch({ headless: true, ...(executablePath ? { executablePath } : {}) });
  try {
    await normalFlow(browser);
    await authErrors(browser);
    await mediuxBranding(browser);
    await focusVisibility(browser);
    await mobile(browser);
    await screenshots(browser);
    process.stdout.write("settings UI checks passed\n");
  } finally {
    await browser.close();
  }
})().catch((error) => {
  process.stderr.write("settings UI checks failed: " + error.message + "\n");
  process.exitCode = 1;
});
