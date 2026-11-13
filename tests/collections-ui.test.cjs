"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const root = path.resolve(__dirname, "..");
const html = fs.readFileSync(path.join(root, "collections.html"), "utf8");
const settingsHTML = fs.readFileSync(path.join(root, "settings.html"), "utf8");
const sharedCSS = settingsHTML.match(/<style>([\s\S]*?)<\/style>/i)?.[1] || "";
const origin = "https://isolatedexample.test";
const mount = "/api/v2/plugin-content/plugins/21";
const catalog = [
  { id: "c1", title: "Marvel Cinematic Universe Collection", poster_url: "https://images.example/current-poster.jpg", backdrop_url: "https://images.example/current-backdrop.jpg", item_count: 34, library_ids: ["movies"] },
  { id: "c2", title: "An exceptionally long collection name that must wrap cleanly on a narrow phone viewport", poster_url: "", backdrop_url: "", item_count: 2, library_ids: ["tv"] },
];
const artwork = { id: "mediux-1", kind: "poster", url: "https://images.example/mediux-poster.jpg", set_id: "set-1", set_title: "Marvel premium set", creator: "Creator", title: "Marvel Collection", source_url: "https://mediux.example/item/1", width: 1000, height: 1500 };

async function setup(browser, options = {}) {
  const context = await browser.newContext({ viewport: options.viewport || { width: 1000, height: 800 } });
  if (options.accessibility) await context.addInitScript(() => {
    localStorage.setItem("silo-ui-cache-owner","test-owner");
    localStorage.setItem("silo-ui-text-scale:test-owner","large");
    localStorage.setItem("silo-ui-text-weight:test-owner","strong");
    localStorage.setItem("silo-ui-high-contrast:test-owner","true");
  });
  const page = await context.newPage();
  const writes = []; const preferenceWrites = []; const itemReads = []; let bindings = {}; let postStatus = options.postStatus || 200; let delayC1 = false;
  await page.route("**/*", async (route) => {
    const request = route.request(); const url = new URL(request.url());
    if (url.origin !== origin) return route.abort();
    if (url.pathname === "/api/v2/theme/admin-css") return route.fulfill({status:200,contentType:"application/json",body:JSON.stringify({vars:{"settings-accent":"#dba444"}})});
    if (url.pathname === mount + "/collections" && request.method() === "GET") return route.fulfill({ status: 200, contentType: "text/html", body: html });
    if (url.pathname === mount + "/ui.css") return route.fulfill({ status: 200, contentType: "text/css", body: sharedCSS });
    if (url.pathname === mount + "/settings/config") return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ csrf: "test-csrf", collection_connection_configured: options.connected !== false }) });
    if (url.pathname === mount + "/sets/preferences") { if (request.method() === "GET") return route.fulfill({status:200,contentType:"application/json",body:JSON.stringify({csrf:"test-csrf",bindings})}); const patch=JSON.parse(request.postData()||"{}"); preferenceWrites.push(patch); if (patch.set_id) bindings[patch.type+":"+patch.id]=patch.set_id; else delete bindings[patch.type+":"+patch.id]; return route.fulfill({status:200,contentType:"application/json",body:JSON.stringify({csrf:"test-csrf",bindings})}); }
    if (url.pathname === mount + "/collections/catalog") {
      if (options.catalogStatus) return route.fulfill({ status: options.catalogStatus, contentType: "application/json", body: JSON.stringify({ error: "connection unavailable" }) });
      return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: catalog, connected: options.connected !== false }) });
    }
    if (url.pathname === mount + "/collections/item") { const id = url.searchParams.get("id"); itemReads.push(id); if (delayC1 && id === "c1") await new Promise((resolve) => setTimeout(resolve, 150)); const item = catalog.find((value) => value.id === id); return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ collection: item, etag: `\"${id}-tag\"` }) }); }
    if (url.pathname === mount + "/collection-artwork") { if (options.searchDelay) await new Promise(resolve => setTimeout(resolve, options.searchDelay)); return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [artwork,{...artwork,id:"backdrop-1",kind:"backdrop",title:"Marvel Backdrop",url:"https://images.example/mediux-backdrop.jpg"}], has_more: false }) }); }
    if (url.pathname === mount + "/collections/cover" && request.method() === "POST") { writes.push(JSON.parse(request.postData() || "{}")); if (options.postHang) return; if (options.postDelay) await new Promise((resolve) => setTimeout(resolve, options.postDelay)); if (postStatus === 412) return route.fulfill({ status: 412, contentType: "application/json", body: JSON.stringify({ error: "stale" }) }); if (postStatus === 503) return route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: "connection unavailable" }) }); if (postStatus >= 400) return route.fulfill({status:postStatus,contentType:"application/json",body:JSON.stringify({error:"failed"})}); const submitted = writes.at(-1); const current = { ...catalog.find((value) => value.id === submitted.id) }; current[submitted.slot + "_url"] = submitted.url; return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ collection: current, etag: "\"new-tag\"" }) }); }
    return route.fulfill({ status: 404, body: "not found" });
  });
  await page.goto(origin + mount + "/collections");
  if (!options.catalogStatus) await page.waitForFunction(() => document.getElementById("collection-list").children.length > 0);
  return { context, page, writes, preferenceWrites, itemReads, setPostStatus: (status) => { postStatus = status; }, delayFirstItem: () => { delayC1 = true; } };
}

async function browseAndApply(browser) {
  const test = await setup(browser); const { page, writes, context } = test;
  try {
    assert.equal(writes.length, 0, "page wrote before a user action");
    assert.equal(await page.locator(".collection-row").first().evaluate(el=>getComputedStyle(el).whiteSpace),"normal");
    if (process.env.UI_SCREENSHOTS) await page.screenshot({path:"/tmp/mediux-desktop-fixed.png",fullPage:true});
    await page.getByRole("button", { name: /Marvel Cinematic Universe Collection/ }).click();
    await page.getByRole("button", { name: "Search Mediux" }).click();
    await page.getByRole("button", { name: /Marvel Collection.*Creator/ }).click(); await page.getByRole("dialog",{name:"Artwork preview"}).getByRole("button",{name:"Close preview"}).click();
    assert.equal(writes.length, 0, "choosing artwork wrote before Apply");
    await page.getByRole("button", { name: "Apply selected artwork" }).click();
    await page.waitForFunction(() => document.getElementById("status").textContent.includes("Artwork applied"));
    assert.deepEqual(writes, [{ csrf: "test-csrf", id: "c1", slot: "poster", url: artwork.url, etag: "\"c1-tag\"" }], "apply body did not match the native bridge contract");
    assert.equal(await page.locator("#current-covers img").first().getAttribute("src"), artwork.url, "returned collection artwork was not refreshed");
  } finally { await context.close(); }
}

async function collectionPreviewAndIndividualApply(browser) {
 const test=await setup(browser);const{page,writes}=test;
 try{
  await page.getByRole("button",{name:/Marvel Cinematic Universe Collection/}).click();await page.getByRole("button",{name:"Search Mediux"}).click();
  const item=page.locator(".candidate-item").filter({has:page.locator(".candidate:not(.backdrop)")}).first();
  await item.locator(".candidate").click();const preview=page.getByRole("dialog",{name:"Artwork preview"});await preview.waitFor();
  assert.equal(writes.length,0,"collection preview changed artwork");assert.equal(await preview.locator("img").getAttribute("src"),artwork.url);
  await preview.getByRole("button",{name:"Close preview"}).click();
  await item.getByRole("button",{name:"Apply poster image",exact:true}).click();await page.waitForFunction(()=>document.getElementById("status").textContent.includes("Artwork applied"));
  assert.equal(writes.length,1);assert.equal(writes[0].url,artwork.url);
 }finally{await test.context.close()}
}

async function staleAndSelectionSafety(browser) {
  const test = await setup(browser); const { page, writes, itemReads, context } = test;
  try {
    await page.getByRole("button", { name: /Marvel Cinematic Universe/ }).click(); await page.getByRole("button", { name: "Search Mediux" }).click(); await page.getByRole("button", { name: /Marvel Collection.*Creator/ }).click(); await page.getByRole("dialog",{name:"Artwork preview"}).getByRole("button",{name:"Close preview"}).click();
    test.setPostStatus(412); await page.getByRole("button", { name: "Apply selected artwork" }).click();
    await page.waitForFunction(() => document.getElementById("status").textContent.includes("changed elsewhere"));
    assert.equal(writes.length, 1, "stale response automatically retried a write");
    await page.waitForFunction(() => document.getElementById("apply").disabled === false);
    assert.ok(itemReads.filter((id) => id === "c1").length >= 2, "stale response did not reload the selected collection");
    await page.getByRole("button", { name: /exceptionally long collection name/ }).click();
    await page.waitForFunction(() => document.getElementById("selected-name").textContent.startsWith("An exceptionally"));
    assert.equal(await page.locator("#apply").isDisabled(), true, "old candidate could be applied to a newly selected collection");
    await page.locator("#external-url").fill("javascript:alert(1)"); await page.getByRole("button", { name: "Use external URL" }).click();
    assert.match(await page.locator("#status").textContent(), /HTTP\(S\)/, "unsafe external URL was not rejected");
  } finally { await context.close(); }
}

async function externalAndNarrow(browser) {
  const test = await setup(browser, { viewport: { width: 360, height: 800 } }); const { page, writes, context } = test;
  try {
    await page.getByRole("button", { name: /exceptionally long collection name/ }).click(); await page.waitForFunction(() => document.getElementById("selected-name").textContent.startsWith("An exceptionally"));
    await page.locator("#slot").selectOption("backdrop"); await page.locator("#external-url").fill("https://cdn.example/cover.jpg"); await page.getByRole("button", { name: "Use external URL" }).click(); await page.getByRole("button", { name: "Apply selected artwork" }).click();
    await page.waitForFunction(() => document.getElementById("status").textContent.includes("Artwork applied"));
    assert.deepEqual(writes[0], { csrf: "test-csrf", id: "c2", slot: "backdrop", url: "https://cdn.example/cover.jpg", etag: "\"c2-tag\"" }, "external URL request was incorrect");
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false, "narrow viewport overflows horizontally");
  } finally { await context.close(); }
}

async function staleSwitchDoesNotOverwrite(browser) {
  const test = await setup(browser, { postStatus: 412, postDelay: 150 }); const { page, itemReads, context } = test;
  try {
    await page.getByRole("button", { name: /Marvel Cinematic Universe/ }).click(); await page.getByRole("button", { name: "Search Mediux" }).click(); await page.getByRole("button", { name: /Marvel Collection.*Creator/ }).click(); await page.getByRole("dialog",{name:"Artwork preview"}).getByRole("button",{name:"Close preview"}).click();
    await page.getByRole("button", { name: "Apply selected artwork" }).click(); await page.getByRole("button", { name: /exceptionally long collection name/ }).click();
    await page.waitForFunction(() => document.getElementById("selected-name").textContent.startsWith("An exceptionally")); await page.waitForTimeout(250);
    assert.doesNotMatch(await page.locator("#status").textContent(), /changed elsewhere/, "stale response overwrote the newly selected collection status");
    assert.equal(itemReads.filter((id) => id === "c1").length, 1, "stale response reloaded a collection that was no longer selected");
  } finally { await context.close(); }
}

async function backdropSelectionTargetsBackdrop(browser) {
  const test=await setup(browser);
  try {
    const page=test.page;
    await page.getByRole("button",{name:/Marvel Cinematic Universe/}).click();
    await page.getByRole("button",{name:"Search Mediux"}).click();
    await page.getByRole("button",{name:/Marvel Backdrop.*Creator/}).click(); await page.getByRole("dialog",{name:"Artwork preview"}).getByRole("button",{name:"Close preview"}).click();
    await page.getByRole("button",{name:"Apply selected artwork"}).click();
    await page.waitForFunction(()=>document.getElementById("status").textContent.includes("Artwork applied"));
    assert.equal(test.writes[0].slot,"backdrop","A Mediux backdrop was applied to the poster slot");
  } finally {await test.context.close();}
}

async function themeAndAccessibilityFollowSilo(browser) {
  const test=await setup(browser,{accessibility:true});
  try {
    await test.page.waitForFunction(()=>document.documentElement.dataset.textScale==="large",null,{timeout:2000});
    const values=await test.page.evaluate(()=>({weight:document.documentElement.dataset.textWeight,contrast:document.documentElement.dataset.highContrast,accent:getComputedStyle(document.documentElement).getPropertyValue("--settings-accent").trim()}));
    assert.deepEqual(values,{weight:"strong",contrast:"true",accent:"#dba444"});
  }finally{await test.context.close();}
}

async function searchSwitchStaysUsable(browser) {
  const test = await setup(browser, { searchDelay: 180 });
  try {
    const page = test.page;
    await page.getByRole("button", { name: /Marvel Cinematic Universe/ }).click();
    await page.getByRole("button", { name: "Search Mediux" }).click();
    await page.getByRole("button", { name: /exceptionally long collection name/ }).click();
    await page.waitForFunction(() => document.getElementById("selected-name").textContent.startsWith("An exceptionally"));
    assert.equal(await page.locator("#search").isDisabled(), false, "switching during search disabled the new collection's Search");
    await page.waitForTimeout(220);
  } finally { await test.context.close(); }
}

async function delayedFailuresStayWithTheirCollection(browser) {
  for (const postStatus of [503, 500]) {
    const test = await setup(browser, {postStatus, postDelay:180});
    try {
      const page=test.page;
      await page.getByRole("button", {name:/Marvel Cinematic Universe/}).click();
      await page.getByRole("button", {name:"Search Mediux"}).click();
      await page.getByRole("button", {name:/Marvel Collection.*Creator/}).click(); await page.getByRole("dialog",{name:"Artwork preview"}).getByRole("button",{name:"Close preview"}).click();
      await page.getByRole("button", {name:"Apply selected artwork"}).click();
      await page.getByRole("button", {name:/exceptionally long collection name/}).click();
      await page.waitForFunction(() => document.getElementById("selected-name").textContent.startsWith("An exceptionally"));
      await page.waitForTimeout(230);
      assert.equal(await page.locator("#status").textContent(), "", "old save failure overwrote new collection status");
      assert.equal(await page.locator("#connection-note").isVisible(), false, "old save failure overwrote new connection state");
    } finally { await test.context.close(); }
  }
}

async function timedOutSaveIsReported(browser) {
  const test = await setup(browser, {postHang:true});
  try {
    const page=test.page;
    await page.clock.install();
    await page.getByRole("button", {name:/Marvel Cinematic Universe/}).click();
    await page.getByRole("button", {name:"Search Mediux"}).click();
    await page.getByRole("button", {name:/Marvel Collection.*Creator/}).click(); await page.getByRole("dialog",{name:"Artwork preview"}).getByRole("button",{name:"Close preview"}).click();
    await page.getByRole("button", {name:"Apply selected artwork"}).click();
    await page.clock.fastForward(20_000);
    await page.waitForFunction(() => document.getElementById("status").textContent.includes("timed out"), null, {timeout:2000});
    assert.equal(test.writes.length,1,"timed-out mutation was replayed");
  } finally { await test.context.close(); }
}

async function backReturnsToExistingSiloRoute(browser) {
  const test=await setup(browser);
  try {
    await test.page.getByRole("button",{name:"Back to Silo"}).click();
    assert.equal(new URL(test.page.url()).pathname,"/admin/plugins","Back must return to the existing native admin plugin route");
  } finally {await test.context.close();}
}

async function mobileControlGeometry(browser) {
  for (const width of [360, 393, 600]) {
    const test = await setup(browser, {viewport:{width,height:850},accessibility:true});
    try {
      const geometry = await test.page.evaluate(() => {
        const rect = selector => {const r=document.querySelector(selector).getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,bottom:r.bottom};};
        return {row:rect('.search-row'),field:rect('.search-row .field'),search:rect('#search'),external:rect('#use-external'),apply:rect('#apply'),panels:[...document.querySelectorAll('.stack > .panel')].map(el=>{const r=el.getBoundingClientRect();return {y:r.y,bottom:r.bottom};})};
      });
      assert.ok(Math.abs(geometry.field.x-geometry.row.x)<2, 'mobile search field is shifted right');
      assert.ok(Math.abs(geometry.field.width-geometry.row.width)<2, 'mobile search field must fill row');
      assert.ok(geometry.search.width>=geometry.row.width-2, 'mobile search button must fill row');
      assert.ok(geometry.apply.y>=geometry.external.bottom, 'mobile apply buttons must stack');
      assert.ok(geometry.panels[2].y-geometry.panels[1].bottom<=20, 'card margins doubled mobile spacing');
      assert.equal(await test.page.locator('#art-query').getAttribute('maxlength'),'100');
      if (width===393 && process.env.UI_SCREENSHOTS) await test.page.screenshot({path:'/tmp/mediux-mobile-fixed.png',fullPage:true});
    } finally {await test.context.close();}
  }
}

async function groupedSetsAndSavedPreference(browser) {
  const test=await setup(browser);
  try {
    const page=test.page;
    await page.getByRole("button",{name:/Marvel Cinematic Universe/}).click();
    await page.getByRole("button",{name:"Search Mediux"}).click();
    await page.getByRole("heading",{name:"Marvel premium set"}).waitFor();
    await page.getByRole("button",{name:/Marvel Collection.*Creator/}).click(); await page.getByRole("dialog",{name:"Artwork preview"}).getByRole("button",{name:"Close preview"}).click();
    await page.getByRole("button",{name:"Save preferred set"}).click();
    await page.waitForFunction(()=>document.getElementById("status").textContent.includes("Preferred set saved"));
    assert.deepEqual(test.preferenceWrites,[{csrf:"test-csrf",type:"collection",id:"c1",set_id:"set-1"}]);
    await page.getByRole("button",{name:"Remove preferred set"}).click();
    await page.waitForFunction(()=>document.getElementById("status").textContent.includes("Preferred set removed"));
    assert.equal(test.preferenceWrites.length,2);
  } finally {await test.context.close();}
}

async function connectionMessage(browser) {
  const test = await setup(browser, { connected: false, catalogStatus: 503 });
  try { await test.page.waitForFunction(() => document.getElementById("connection-note").textContent.includes("configured by an administrator")); assert.equal(test.writes.length, 0, "unconfigured connection made a write"); }
  finally { await test.context.close(); }
}

(async () => { const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH; const browser = await chromium.launch({ headless: true, ...(executablePath ? { executablePath } : {}) }); try { await browseAndApply(browser); await collectionPreviewAndIndividualApply(browser); await staleAndSelectionSafety(browser); await externalAndNarrow(browser); await staleSwitchDoesNotOverwrite(browser); await backdropSelectionTargetsBackdrop(browser); await themeAndAccessibilityFollowSilo(browser); await searchSwitchStaysUsable(browser); await delayedFailuresStayWithTheirCollection(browser); await timedOutSaveIsReported(browser); await backReturnsToExistingSiloRoute(browser); await mobileControlGeometry(browser); await groupedSetsAndSavedPreference(browser); await connectionMessage(browser); process.stdout.write("collections UI checks passed\n"); } finally { await browser.close(); } })().catch((error) => { process.stderr.write("collections UI checks failed: " + error.stack + "\n"); process.exitCode = 1; });
