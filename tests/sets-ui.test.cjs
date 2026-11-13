"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const {chromium} = require("playwright");
const root = path.resolve(__dirname, "..");
const html = fs.readFileSync(path.join(root, "sets.html"), "utf8");
const sharedCSS = fs.readFileSync(path.join(root, "settings.html"), "utf8").match(/<style>([\s\S]*?)<\/style>/i)[1];
const origin = "https://isolatedexample.test", mount = "/api/v2/plugin-content/plugins/21";
const catalog = {type:"movie", id:"550", sets:[{id:"42",title:"Fight Club set",creator:"Artist",counts:{poster:1,backdrop:1,logo:1},artwork_count:3,artwork:[
  {id:"a",kind:"poster",url:"https://images.example/poster.jpg",set_id:"42",language:"en"},
  {id:"b",kind:"backdrop",url:"https://images.example/backdrop.jpg",set_id:"42"},
  {id:"c",kind:"logo",url:"https://images.example/logo.png",set_id:"42"}
]}]};
async function setup(browser, options={}) {
  const context=await browser.newContext({viewport:{width:390,height:844}}), page=await context.newPage();
  let bindings={...(options.bindings||{})};
  const writes=[],plans=[],applies=[],errors=[],tokens=new Map();
  page.on("pageerror",error=>errors.push(error.message));
  await page.route("**/*",async route=>{
    const r=route.request(),u=new URL(r.url());
    const json=(body,status=200)=>route.fulfill({status,contentType:"application/json",body:JSON.stringify(body)});
    if(u.hostname==="images.example"){const wide=u.pathname.includes("logo")?[900,300]:u.pathname.includes("backdrop")?[1600,900]:[1000,1500];return route.fulfill({status:200,contentType:"image/svg+xml",body:`<svg xmlns="http://www.w3.org/2000/svg" width="${wide[0]}" height="${wide[1]}" viewBox="0 0 ${wide[0]} ${wide[1]}"><rect width="100%" height="100%" fill="#3d3559"/><text x="50%" y="50%" text-anchor="middle" fill="#fff" font-size="50">Artwork</text></svg>`})}if(u.origin!==origin)return route.abort();
    if(u.pathname==="/api/v2/theme/admin-css")return json({});
    if(u.pathname===mount+"/sets")return route.fulfill({status:200,contentType:"text/html",body:html});
    if(u.pathname===mount+"/ui.css")return route.fulfill({status:200,contentType:"text/css",body:sharedCSS});
    if(u.pathname===mount+"/sets/preferences") {
      if(r.method()==="GET")return json({csrf:"csrf",bindings});
      const value=JSON.parse(r.postData());writes.push(value);
      if(options.preferenceFailure)return json({error:"Could not save preference"},500);
      if(value.set_id)bindings[value.type+":"+value.id]=value.set_id;else delete bindings[value.type+":"+value.id];
      return json({csrf:"csrf",bindings});
    }
    if(u.pathname===mount+"/sets/items")return json({items:[{content_id:"content-1",type:"movie",title:"Fight Club",year:1999}]});
    if(u.pathname===mount+"/sets/item")return json({type:"movie",id:"550",title:"Fight Club"});
    if(u.pathname===mount+"/sets/catalog")return json({...catalog,...options.catalog,type:u.searchParams.get("type"),id:u.searchParams.get("id")});
    if(u.pathname===mount+"/sets/plan") {
      const input=JSON.parse(r.postData());plans.push(input);
      const art=input.asset_id?catalog.sets[0].artwork.filter(a=>a.id===input.asset_id):catalog.sets[0].artwork;
      const operations=art.map(a=>{const operation={token:input.id+":"+a.id,target_id:input.content_id||"content-1",kind:a.kind,url:a.url,label:a.kind};tokens.set(operation.token,operation);return operation});
      return json({...input,content_id:input.content_id||"content-1",operations,skipped:0});
    }
    if(u.pathname===mount+"/sets/apply") {
      const input=JSON.parse(r.postData());applies.push(input);
      if(options.applyGate)await options.applyGate;
      if(options.networkFailure)return route.abort();
      if(options.applyFailureAt===applies.length)return json({error:"Native image application failed"},502);
      const operation=tokens.get(input.token);
      return json({...operation,stored_path:"cache/"+input.token+".jpg",image_url:operation.url,revision:"revision-1"});
    }
    return route.fulfill({status:404,body:"not found"});
  });
  await page.goto(origin+mount+"/sets");
  return {context,page,writes,plans,applies,errors};
}
async function browse(test) {
  await test.page.locator("#title-query").fill("Fight");
  await test.page.getByRole("button",{name:"Find titles"}).click();
  await test.page.getByRole("button",{name:/Fight Club/}).click();
  await test.page.getByRole("heading",{name:"Fight Club set"}).waitFor();
}
async function individualAndSetApply(browser) {
  const test=await setup(browser),{page}=test;
  try {
    await browse(test);
    assert.equal(test.applies.length,0,"browsing applied artwork");
    assert.equal(await page.locator(".art").first().evaluate(el=>getComputedStyle(el).display),"block");
    const logo=page.locator(".art-item").filter({has:page.locator(".art.logo")});
    const thumbnail=logo.getByRole("button",{name:"Preview logo",exact:true});
    await thumbnail.click();
    const preview=page.getByRole("dialog",{name:"Artwork preview"});await preview.waitFor();
    assert.equal(await preview.locator("img").getAttribute("src"),catalog.sets[0].artwork[2].url);
    await preview.locator("img").evaluate(image=>image.decode());const enlarged=await preview.locator("img").boundingBox(),small=await thumbnail.locator("img").boundingBox();assert(enlarged.width>small.width,"preview did not enlarge the image");
    assert.equal(test.plans.length,0,"opening a preview planned a write");assert.equal(test.applies.length,0,"opening a preview changed artwork");assert.equal(test.writes.length,0,"opening a preview saved a preference");
    await page.keyboard.press("Escape");assert.equal(await preview.isVisible(),false);assert.equal(await thumbnail.evaluate(el=>document.activeElement===el),true,"preview did not restore thumbnail focus");
    await thumbnail.click();await preview.getByRole("button",{name:"Close preview"}).click();
    const applyImage=logo.getByRole("button",{name:"Apply logo image",exact:true});
    const rectangles=await logo.evaluate(el=>{const image=el.querySelector(".art").getBoundingClientRect(),apply=el.querySelector(".apply-image").getBoundingClientRect();return {imageBottom:image.bottom,applyTop:apply.top}});
    assert(rectangles.applyTop>=rectangles.imageBottom,"individual Apply must sit beneath preview");
    await applyImage.click();
    await page.waitForFunction(()=>document.getElementById("status").textContent.includes("Applied 1 image now"));
    assert.equal(test.applies.length,1,"individual Apply did not publish immediately");
    assert.equal(test.plans[0].asset_id,"c");assert.equal(test.plans[0].content_id,"content-1");
    assert.equal(test.writes.length,0,"individual selection pinned the entire set");
    await page.getByRole("button",{name:"Apply set"}).click();
    await page.waitForFunction(()=>document.getElementById("status").textContent.includes("Applied 3 images now"));
    assert.equal(test.applies.length,4);
    assert.deepEqual(test.writes[0],{csrf:"csrf",type:"movie",id:"550",set_id:"42"});
    await page.locator("#saved-bindings").getByRole("button",{name:"Remove",exact:true}).click();
    await page.waitForFunction(()=>document.getElementById("status").textContent.includes("Preferred set removed"));
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
    assert.deepEqual(test.errors,[]);
    if(process.env.UI_SCREENSHOTS){await page.screenshot({path:"/tmp/mediux-immediate-mobile.png",fullPage:true});await page.setViewportSize({width:1200,height:900});await page.screenshot({path:"/tmp/mediux-immediate-desktop.png",fullPage:true})}
  }finally{await test.context.close()}
}
async function failuresStopWithoutRetry(browser) {
  for(const options of [{applyFailureAt:2},{networkFailure:true},{preferenceFailure:true}]) {
    const test=await setup(browser,options);
    try {
      await browse(test);await test.page.getByRole("button",{name:"Apply set"}).click();
      await test.page.waitForFunction(()=>document.getElementById("status").classList.contains("error"));
      if(options.applyFailureAt){assert.equal(test.applies.length,2);assert.equal(test.writes.length,0);assert.match(await test.page.locator("#status").textContent(),/Stopped after 1 of 3/)}
      if(options.networkFailure){assert.equal(test.applies.length,1);assert.equal(test.writes.length,0);assert.match(await test.page.locator("#status").textContent(),/may have changed/)}
      if(options.preferenceFailure){assert.equal(test.applies.length,3);assert.match(await test.page.locator("#status").textContent(),/Applied 3 images now, but could not save/)}
      assert.deepEqual(test.errors,[]);
    }finally{await test.context.close()}
  }
}
async function stopAfterCurrentImage(browser) {
  let release;const applyGate=new Promise(resolve=>release=resolve);
  const test=await setup(browser,{applyGate});
  try {
    await browse(test);const started=test.page.waitForRequest(r=>r.url().endsWith("/sets/apply"));
    await test.page.getByRole("button",{name:"Apply set"}).click();await started;
    assert.equal(await test.page.locator("#type").isDisabled(),true);
    await test.page.getByRole("button",{name:"Stop after current image"}).click();release();
    await test.page.waitForFunction(()=>document.getElementById("status").textContent.includes("Stopped after 1 of 3"));
    assert.equal(test.applies.length,1);assert.equal(test.writes.length,0);
  }finally{release();await test.context.close()}
}
async function staleBrowseAndSavedRemoval(browser) {
  const test=await setup(browser);let release;
  try {
    const page=test.page;await page.locator("details").evaluate(el=>el.open=true);
    const gate=new Promise(resolve=>release=resolve);
    await page.route("**/sets/catalog?**",async route=>{const u=new URL(route.request().url()),id=u.searchParams.get("id");if(id==="550")await gate;try{await route.fulfill({status:200,contentType:"application/json",body:JSON.stringify({...catalog,type:u.searchParams.get("type"),id,sets:[{...catalog.sets[0],title:id==="1399"?"Current series":"Stale movie"}]})})}catch(_) {}});
    await page.locator("#tmdb").fill("550");const started=page.waitForRequest(r=>r.url().includes("id=550"));await page.locator("#browse").click();await started;
    await page.locator("#type").selectOption("series");assert.equal(await page.locator("#browse").isEnabled(),true);
    await page.locator("#tmdb").fill("1399");await page.locator("#browse").click();await page.getByRole("heading",{name:"Current series"}).waitFor();release();
    assert.equal(await page.getByRole("heading",{name:"Stale movie"}).count(),0);
  }finally{if(release)release();await test.context.close()}
  const saved=await setup(browser,{bindings:{"collection:local-id":"42"}});
  try{await saved.page.getByRole("button",{name:"Remove",exact:true}).click();await saved.page.waitForFunction(()=>document.getElementById("status").textContent.includes("Preferred set removed"));assert.equal(saved.writes[0].id,"local-id")}finally{await saved.context.close()}
}

async function seasonRows(browser) {
 const artwork=Array.from({length:61},(_,i)=>({id:"episode-"+i,kind:"still",season:i%3+1,episode:Math.floor(i/3)+1,url:"https://images.example/episode-"+i+".jpg"})).reverse();
 const test=await setup(browser,{catalog:{sets:[{id:"42",title:"Fight Club set",artwork_count:64,artwork:[...artwork,...catalog.sets[0].artwork]}]}});
 try {const {page}=test;
 await page.route('**/sets/catalog?**',route=>{const u=new URL(route.request().url()),offset=Number(u.searchParams.get('offset'))||0,all=[...artwork,...catalog.sets[0].artwork];return route.fulfill({status:200,contentType:'application/json',body:JSON.stringify({type:u.searchParams.get('type'),id:u.searchParams.get('id'),sets:[{id:'42',title:'Fight Club set',artwork_count:64,artwork:all.slice(offset,offset?undefined:30)}],next_offset:offset?0:30})})});
 await browse(test);await page.waitForFunction(()=>document.querySelectorAll('.art-item').length===64);assert.equal(await page.locator('.art-item').count(),64);
 assert.equal(await page.locator('.artwork-group').count(),6);for(const label of ['Posters','Backdrops','Logos','Season 1 · Title cards','Season 2 · Title cards','Season 3 · Title cards'])assert.equal(await page.getByRole('heading',{name:new RegExp('^'+label+' ·')}).count(),1);
 const strip=page.getByRole('region',{name:'Season 3 · Title cards images'});assert.equal(await strip.locator('.art-item').count(),20);
 const episodes=await strip.locator('.label').allTextContents();assert(episodes[0].includes('S3E1'));assert(episodes.at(-1).includes('S3E20'));
 assert(await strip.evaluate(el=>el.scrollWidth>el.clientWidth));await page.getByRole('button',{name:'Scroll Season 3 · Title cards right',exact:true}).click();await page.waitForFunction(()=>document.querySelectorAll('.episode-strip')[2].scrollLeft>0);
 const last=strip.locator('.art').last();await last.click();await page.getByRole('dialog',{name:'Artwork preview'}).waitFor();assert.equal(test.plans.length,0);await page.keyboard.press('Escape');
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);await page.setViewportSize({width:1280,height:900});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);assert.deepEqual(test.errors,[]);
 }finally{await test.context.close()}
}

(async()=>{const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}:{})});try{await seasonRows(browser);await individualAndSetApply(browser);await failuresStopWithoutRetry(browser);await stopAfterCurrentImage(browser);await staleBrowseAndSavedRemoval(browser);process.stdout.write("sets UI checks passed\n")}finally{await browser.close()}})().catch(error=>{process.stderr.write("sets UI checks failed: "+error.stack+"\n");process.exitCode=1});
