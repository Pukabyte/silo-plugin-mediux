"use strict";
const assert=require("node:assert/strict"),fs=require("node:fs"),path=require("node:path"),{chromium}=require("playwright");
const root=path.resolve(__dirname,".."),html=fs.readFileSync(process.env.ARTWORK_MANAGER_TEST_HTML||"/tmp/artwork-manager-test.html","utf8"),css=fs.readFileSync(path.join(root,"settings.html"),"utf8").match(/<style>([\s\S]*?)<\/style>/i)[1];
const origin="https://isolatedexample.test",mount="/api/v2/plugin-content/plugins/21";
const art={id:"asset-1",kind:"poster",url:"https://images.example/poster.jpg",set_id:"42",set_title:"Marvel set",creator:"Artist",title:"Marvel Collection"};
const collection={id:"c1",title:"Marvel Collection",library_ids:["7"],poster_url:"",backdrop_url:"",item_count:3};
async function setup(browser,routeName="artwork",width=390){
 const context=await browser.newContext({viewport:{width,height:844}}),page=await context.newPage(),writes=[],errors=[];let bindings={},calls=0;
 page.on("pageerror",e=>errors.push(e.message));
 await page.route("**/*",async route=>{const r=route.request(),u=new URL(r.url());const json=(body)=>route.fulfill({status:200,contentType:"application/json",body:JSON.stringify(body)});
  if(u.hostname==="images.example")return route.fulfill({status:200,contentType:"image/svg+xml",body:'<svg xmlns="http://www.w3.org/2000/svg" width="1000" height="1500"><rect width="100%" height="100%" fill="#453964"/><text x="500" y="750" text-anchor="middle" fill="white" font-size="80">Artwork</text></svg>'});
  if(u.origin!==origin)return route.abort();
  if(u.pathname==="/api/v2/theme/admin-css")return json({vars:{"settings-accent":"#b9a6ef"}});
  if(["/artwork","/sets","/collections"].some(p=>u.pathname===mount+p))return route.fulfill({status:200,contentType:"text/html",body:html});
  if(u.pathname===mount+"/ui.css")return route.fulfill({status:200,contentType:"text/css",body:css});
  if(r.method()!=="GET")writes.push({path:u.pathname,body:JSON.parse(r.postData())});
  if(u.pathname===mount+"/sets/preferences"){if(r.method()==="PUT"){const b=JSON.parse(r.postData());if(b.set_id)bindings[b.type+":"+b.id]=b.set_id;else delete bindings[b.type+":"+b.id]}return json({csrf:"csrf",bindings})}
  if(u.pathname===mount+"/settings/config")return json({csrf:"csrf",collection_connection_configured:true});
  if(u.pathname===mount+"/collections/catalog")return json({items:[collection],connected:true});
  if(u.pathname===mount+"/collections/item")return json({collection,etag:'"v1"'});
  if(u.pathname===mount+"/collection-artwork")return json({items:[art],has_more:false});
  if(u.pathname===mount+"/collections/cover"){const b=JSON.parse(r.postData());return json({collection:{...collection,[b.slot+"_url"]:b.url},etag:'"v2"'})}
  if(u.pathname===mount+"/sets/items")return json({items:[{content_id:"movie-1",type:"movie",title:"Marvel Movie",year:2020}]});
  if(u.pathname===mount+"/sets/item")return json({type:"movie",id:"550",title:"Marvel Movie"});
  if(u.pathname===mount+"/sets/catalog")return json({type:"movie",id:"550",sets:[{id:"42",title:"Marvel set",creator:"Artist",artwork:[art]}]});
  if(u.pathname===mount+"/sets/plan"){calls++;const b=JSON.parse(r.postData());return json({...b,operations:[{token:"operation-"+calls,target_id:"movie-1",kind:"poster",url:art.url,label:"Movie poster"}],skipped:0})}
  if(u.pathname===mount+"/sets/apply")return json({target_id:"movie-1",kind:"poster",stored_path:"cache/poster.webp",revision:"revision"});
  return route.fulfill({status:404,body:"not found"});
 });
 await page.goto(origin+mount+"/"+routeName);return{page,context,writes,errors};
}
async function combinedViews(browser){
 const test=await setup(browser);const{page,writes,errors}=test;
 try{
  assert.equal(await page.title(),"Artwork Manager");assert.equal(await page.getByRole("heading",{name:"Artwork Manager",exact:true}).count(),1);
  const duplicateIDs=await page.evaluate(()=>{const seen=new Set();return[...document.querySelectorAll("[id]")].map(el=>el.id).filter(id=>{if(seen.has(id))return true;seen.add(id);return false})});assert.deepEqual(duplicateIDs,[]);
  await page.locator("#media-title-query").fill("Marvel");await page.locator("#media-find").click();await page.getByRole("button",{name:/Marvel Movie/}).click();
  const mediaItem=page.locator("#media-results .art-item").first();await mediaItem.waitFor();
  await mediaItem.locator(".art").click();let preview=page.getByRole("dialog",{name:"Artwork preview"});await preview.waitFor();await preview.locator("img").evaluate(img=>img.decode());
  assert.equal(writes.length,0,"media preview mutated data");assert.equal(await preview.locator("img").getAttribute("src"),art.url);
  const enlarged=await preview.locator("img").boundingBox(),small=await mediaItem.locator(".art img").boundingBox();assert(enlarged.width>small.width);
  if(process.env.UI_SCREENSHOTS)await page.screenshot({path:"/tmp/artwork-manager-preview-mobile.png",fullPage:true});
  await page.keyboard.press("Escape");assert.equal(await mediaItem.locator(".art").evaluate(el=>el===document.activeElement),true);
  await page.getByRole("tab",{name:"Collection covers"}).click();assert.equal(await page.locator("#collections-view").isVisible(),true);assert.equal(await page.locator("#media-view").isVisible(),false);
  await page.getByRole("button",{name:/Marvel Collection/}).click();await page.locator("#collections-search").click();
  const candidate=page.locator("#collections-candidate-results .candidate-item").first();await candidate.locator(".candidate").click();preview=page.getByRole("dialog",{name:"Artwork preview"});await preview.waitFor();assert.equal(writes.length,0,"collection preview mutated data");await preview.getByRole("button",{name:"Close preview"}).click();
  const boxes=await candidate.evaluate(el=>{const p=el.querySelector('.candidate').getBoundingClientRect(),a=el.querySelector('.apply-image').getBoundingClientRect();return{bottom:p.bottom,top:a.top}});assert(boxes.top>=boxes.bottom);
  await candidate.getByRole("button",{name:"Apply poster image"}).click();await page.waitForFunction(()=>document.getElementById("collections-status").textContent.includes("Artwork applied"));assert.equal(writes.filter(x=>x.path.endsWith("/collections/cover")).length,1);
  await page.getByRole("tab",{name:"Movies & shows"}).click();assert.equal(await page.locator("#media-title-query").inputValue(),"Marvel");assert.equal(await page.locator("#media-results .set").count(),1,"switching views reset media state");
  await mediaItem.getByRole("button",{name:"Apply poster image"}).click();await page.waitForFunction(()=>document.getElementById("media-status").textContent.includes("Applied 1 image now"));assert.equal(writes.filter(x=>x.path.endsWith("/sets/apply")).length,1);
  await page.getByRole("button",{name:"Apply set",exact:true}).click();await page.waitForFunction(()=>document.getElementById("media-status").textContent.includes("Set preference saved"));
  await page.getByRole("tab",{name:"Movies & shows"}).focus();await page.keyboard.press("ArrowRight");assert.equal(await page.getByRole("tab",{name:"Collection covers"}).getAttribute("aria-selected"),"true");
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);assert.deepEqual(errors,[]);
  if(process.env.UI_SCREENSHOTS){await page.screenshot({path:"/tmp/artwork-manager-collections-mobile.png",fullPage:true});await page.setViewportSize({width:1280,height:900});await page.getByRole("tab",{name:"Movies & shows"}).click();await page.screenshot({path:"/tmp/artwork-manager-desktop.png",fullPage:true})}
 }finally{await test.context.close()}
}
(async()=>{const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}:{})});try{await combinedViews(browser);for(const route of["sets","collections"]){const t=await setup(browser,route);try{await t.page.getByRole("tab",{name:route==="sets"?"Movies & shows":"Collection covers"}).waitFor();assert.equal(await t.page.locator(route==="sets"?"#media-view":"#collections-view").isVisible(),true);assert.deepEqual(t.errors,[])}finally{await t.context.close()}}process.stdout.write("Artwork Manager UI checks passed\n")}finally{await browser.close()}})().catch(error=>{process.stderr.write("Artwork Manager UI checks failed: "+error.stack+"\n");process.exitCode=1});
