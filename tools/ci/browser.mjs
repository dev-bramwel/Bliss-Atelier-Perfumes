import {chromium} from 'playwright';
import assert from 'node:assert/strict';
import {mkdirSync, writeFileSync} from 'node:fs';

const origin = process.env.SMOKE_ORIGIN || 'http://frontend:8080';
const output = process.env.REPORT_DIR || '/reports';
mkdirSync(output, {recursive:true});
const browser = await chromium.launch({headless:true});
try {
  const context = await browser.newContext({viewport:{width:390,height:844}});
  await context.route('**/*', route => new URL(route.request().url()).origin === new URL(origin).origin ? route.continue() : route.abort());
  await context.addInitScript(() => localStorage.setItem('cart', JSON.stringify({p1:2})));
  const page = await context.newPage();
  const errors=[];
  page.on('pageerror', error => errors.push(error.message));
  assert.equal((await page.goto(origin+'/index.html')).status(),200);
  assert.equal((await page.goto(origin+'/cart.html')).status(),200);
  assert.equal((await page.goto(origin+'/checkout.html')).status(),200);
  assert(await page.locator('#checkoutForm').isVisible());
  assert.match(await page.locator('#checkoutItems').innerText(),/Arabian Wood/);
  assert.match(await page.locator('#checkoutTotal').innerText(),/4[,.]?000/);
  assert.equal(await page.locator('meta[name="api-base-url"]').getAttribute('content'),'');
  for (const path of ['/api/health/live','/api/health/ready']) {
    assert.equal(await page.evaluate(async path => (await fetch(path)).status,path),200);
  }
  assert.equal(await page.evaluate(() => getComputedStyle(document.body).overflowX),'hidden');
  assert.deepEqual(errors,[]);
  await page.screenshot({path:output+'/checkout-mobile.png',fullPage:true});
  writeFileSync(output+'/browser.json',JSON.stringify({status:'passed',viewport:'390x844',scope:'static cart/checkout and same-origin foundation health; no payment or order submission'},null,2));
} finally {await browser.close();}
