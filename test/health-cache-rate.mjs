// Cache-rate consistency probe: the health panel's tooltips must show the
// same accounting-aware cache rate as the request table for the very same
// call/bucket. Guards the regression where the blended health formula
// double-counted CLIProxyAPI's CachedTokens/CacheReadTokens dual-fill.
//
// Fixture math (see helpers.mjs healthFixtures):
//   recent slot 21: read=cached=400, input=1000 → accounting-aware 40.00%
//                   (old blended formula showed 44.4%)
//   recent slot 20: no cache, input=1900 → 0.00%
//   day slot 10 (overridden): numerator=250, denominator=1000 → 25.0%
//                   (old blended formula showed 33.3%)
import { chromium } from 'playwright-core';
import { mkdirSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'url';
import { dirname, join } from 'node:path';
import { startDashboardServer, defaultFixtures } from './helpers.mjs';

const [htmlPath, chromePath, outDir] = process.argv.slice(2);
if (!htmlPath || !chromePath) {
  throw new Error('usage: node test/health-cache-rate.mjs <dashboard-html-path> <google-chrome-path> [screenshot-dir]');
}

// The raw template carries placeholders the Go plugin fills at startup
// (internal/plugin/dashboard.go init()); bake the shipped locale catalog and a
// stub logo in the same way, or the page script dies on a syntax error.
const repoRoot = join(dirname(fileURLToPath(import.meta.url)), '..');
const localeData = {};
for (const code of ['en', 'zh-CN', 'zh-TW', 'ru']) {
  localeData[code] = JSON.parse(await readFile(join(repoRoot, 'internal', 'plugin', 'web', 'locales', code + '.json'), 'utf-8'));
}
const rawTemplate = await readFile(htmlPath, 'utf-8');
const dashboardHTML = rawTemplate
  .replace('/*LOCALE_PLACEHOLDER*/', JSON.stringify(localeData))
  .replaceAll('__CARD_LOGO_DATA_URI__', 'data:image/gif;base64,R0lGODlhAQABAIAAAP///wAAACH5BAEAAAAALAAAAAABAAEAAAICRAEAOw==');

const GOOD_KEY = 'health-cache-browser-key';
const fixtures = defaultFixtures();
// One bucket with cache activity so the bucket tooltip assertion can tell the
// accounting-aware value apart from the old blended one.
fixtures.health.day[10] = {
  ...fixtures.health.day[10],
  cache_read_tokens: 250,
  cached_tokens: 250,
  cache_rate_numerator: 250,
  cache_rate_denominator: 1000,
};

const host = startDashboardServer({
  dashboardHTML,
  pluginId: 'health-cache-browser-test',
  fixtures,
});
const dashboardURL = await host.ready;
const browser = await chromium.launch({ executablePath: chromePath, headless: true });

// The old blended health formula, for the record only.
const blended = (point) => {
  const read = point.cache_read_tokens || 0, cached = point.cached_tokens || 0;
  const creation = point.cache_creation_tokens || 0, input = point.input_tokens || 0;
  const denominator = input + creation + read + cached;
  return denominator > 0 ? Math.min(100, (read + cached) / denominator * 100) : null;
};
// Mirror of the request-table formula for openai-compatibility (subset):
// numerator = read>0?read:cached, denominator = input.
const calls = fixtures.health.calls;
const expectedRecent = (call) => {
  const read = call.cache_read_tokens || 0, cached = call.cached_tokens || 0;
  const numerator = read > 0 ? read : cached;
  return (numerator / (call.input_tokens || 0) * 100).toFixed(2) + '%';
};

const results = [];
const check = (name, ok, detail = '') => results.push((ok ? 'PASS ' : 'FAIL ') + name + (ok ? '' : ' — ' + detail));

try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 1150 }, deviceScaleFactor: 2, locale: 'zh-CN', timezoneId: 'UTC' });
  await context.addInitScript((key) => { sessionStorage.setItem('tokens-statistic-key', key); }, GOOD_KEY);
  const page = await context.newPage();
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(String(error.stack || error).slice(0, 500)));

  await page.goto(dashboardURL, { waitUntil: 'domcontentloaded' });
  await page.waitForResponse((response) => new URL(response.url()).pathname.endsWith('/stats/health'));
  await page.waitForFunction(() => document.querySelectorAll('.health-dot').length === 72);

  // recent dots carry data-slot; day/days dots carry data-index.
  const hoverCacheCell = async (win, attr, slotIndex) => {
    await page.hover(`.health-dots[data-window="${win}"] .health-dot[data-${attr}="${slotIndex}"]`);
    await page.waitForFunction(() => document.getElementById('healthTip').classList.contains('is-on'));
    return page.evaluate(() => document.getElementById('healthTipCache').textContent);
  };
  const shoot = async (name) => {
    if (!outDir) return;
    mkdirSync(outDir, { recursive: true });
    const shot = join(outDir, name);
    await page.screenshot({ path: shot });
    console.log('screenshot: ' + shot);
  };

  const hit = calls[21];
  const shownHit = await hoverCacheCell("recent", "slot", 21);
  const wantHit = expectedRecent(hit);
  check(`recent slot 21 cache rate = ${wantHit}`, shownHit === wantHit, `shown ${shownHit}`);
  const oldHit = blended(hit);
  check('recent value differs from the old blended formula', wantHit !== oldHit.toFixed(1) + '%', `blended ${oldHit.toFixed(1)}%`);
  console.log(`info: slot 21 accounting-aware ${wantHit} vs old blended ${oldHit.toFixed(1)}% (input=${hit.input_tokens} read=${hit.cache_read_tokens} cached=${hit.cached_tokens})`);
  await shoot('health-recent-cache-rate.png');

  const shownZero = await hoverCacheCell("recent", "slot", 20);
  const wantZero = expectedRecent(calls[20]);
  check(`recent slot 20 cache rate = ${wantZero}`, shownZero === wantZero, `shown ${shownZero}`);

  const bucket = fixtures.health.day[10];
  const shownBucket = await hoverCacheCell('day', 'index', 10);
  check('day slot 10 bucket cache rate = 25.00%', shownBucket === '25.00%', `shown ${shownBucket}`);
  const oldBucket = blended(bucket);
  check('bucket value differs from the old blended formula', shownBucket !== oldBucket.toFixed(1) + '%', `blended ${oldBucket.toFixed(1)}%`);
  console.log(`info: day slot 10 accounting-aware 25.00% vs old blended ${oldBucket.toFixed(1)}% (numerator=${bucket.cache_rate_numerator} denominator=${bucket.cache_rate_denominator})`);
  await shoot('health-bucket-cache-rate.png');

  // Capsule decorations (same panel): every bubble flows, and the fill covers
  // the full track so no gray tail remnant shows at the right end. The fill's
  // width has a 0.6s transition on first paint, so wait for it to settle.
  await page.waitForFunction(() => {
    const fill = document.getElementById('healthFill');
    const track = document.querySelector('.health-track');
    return fill && track && Math.abs(fill.getBoundingClientRect().width - track.getBoundingClientRect().width) < 1;
  }, undefined, { timeout: 5000 });
  const bubbles = await page.evaluate(() => {
    const list = document.querySelectorAll('#healthFill b');
    const fill = document.getElementById('healthFill');
    const track = document.querySelector('.health-track');
    return {
      count: list.length,
      animated: Array.from(list).filter((el) => getComputedStyle(el).animationName === 'hp-flow').length,
      fillWidth: fill ? fill.getBoundingClientRect().width : -1,
      trackWidth: track ? track.getBoundingClientRect().width : -2,
    };
  });
  check('capsule bubble count unchanged (20)', bubbles.count === 20, String(bubbles.count));
  check('all bubbles keep the flow animation', bubbles.animated === 20, String(bubbles.animated));
  check('fill covers the full track (no gray tail)', Math.abs(bubbles.fillWidth - bubbles.trackWidth) < 1, `fill ${bubbles.fillWidth} vs track ${bubbles.trackWidth}`);
  await shoot('health-capsule-dot.png');

  check('no page errors', pageErrors.length === 0, pageErrors.join(' | '));

  await browser.close();
  host.close();

  let failed = 0;
  for (const line of results) {
    console.log(line);
    if (line.startsWith('FAIL')) failed++;
  }
  if (failed > 0) process.exit(1);
} catch (error) {
  console.error('FAIL ' + (error && error.message || error));
  await browser.close();
  host.close();
  process.exit(1);
}
