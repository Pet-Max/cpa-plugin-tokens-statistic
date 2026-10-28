// Particle-density regression for the health panel's big capsule: the flowing
// bubbles must reach the right edge of the fill, not die out ~11% short of it.
// Guards the fix that moved the fade-in into the clipped zone (keyframes birth
// offset) and shifted the front anchors (nth-of-type 1/2/3/21/22/23) outward.
//
// Metric: across 12 samples 300ms apart, the average distance from the fill's
// right edge to the nearest visible bubble (computed opacity >= 0.3) must stay
// under 7% of the fill width. The pre-fix build measured ~11.3% on a 1041px
// fill (118px); the fixed build measures 1.8-4.1% across runs (~19-43px).
import { chromium } from 'playwright-core';
import { mkdirSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'url';
import { dirname, join } from 'node:path';
import { startDashboardServer } from './helpers.mjs';

const [htmlPath, chromePath, outDir] = process.argv.slice(2);
if (!htmlPath || !chromePath) {
  throw new Error('usage: node test/health-particle-density.mjs <dashboard-html-path> <google-chrome-path> [screenshot-dir]');
}

// The raw template carries placeholders the Go plugin fills at startup
// (internal/plugin/dashboard.go init()); bake the shipped locale catalog and a
// stub logo in the same way, or the page script dies on a syntax error.
const repoRoot = join(dirname(fileURLToPath(import.meta.url)), '..');
const localeData = {};
for (const code of ['en', 'zh-CN', 'zh-TW', 'ru']) {
  localeData[code] = JSON.parse(await readFile(join(repoRoot, 'internal', 'plugin', 'web', 'locales', code + '.json'), 'utf-8'));
}
const dashboardHTML = (await readFile(htmlPath, 'utf-8'))
  .replace('/*LOCALE_PLACEHOLDER*/', JSON.stringify(localeData))
  .replaceAll('__CARD_LOGO_DATA_URI__', 'data:image/gif;base64,R0lGODlhAQABAIAAAP///wAAACH5BAEAAAAALAAAAAABAAEAAAICRAEAOw==');

const GOOD_KEY = 'health-particle-browser-key';
const host = startDashboardServer({ dashboardHTML, pluginId: 'health-particle-browser-test' });
const dashboardURL = await host.ready;
const browser = await chromium.launch({ executablePath: chromePath, headless: true });

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

  // The fill's width transitions on first paint; wait for it to settle so the
  // bubble offsets are measured against the final geometry.
  await page.waitForFunction(() => {
    const fill = document.getElementById('healthFill');
    const track = document.querySelector('.health-track');
    return fill && track && /is-(good|warn|bad)/.test(fill.className)
      && Math.abs(fill.getBoundingClientRect().width - track.getBoundingClientRect().width) < 1;
  }, undefined, { timeout: 5000 });
  await page.waitForTimeout(1000);

  const bubbles = await page.evaluate(() => {
    const list = document.querySelectorAll('#healthFill b');
    return {
      count: list.length,
      animated: Array.from(list).filter((el) => getComputedStyle(el).animationName === 'hp-flow').length,
    };
  });
  check('capsule bubble count unchanged (30)', bubbles.count === 30, String(bubbles.count));
  check('all bubbles keep the flow animation', bubbles.animated === 30, String(bubbles.animated));

  // Sample the leading edge: nearest visible bubble to the fill's right edge.
  const samples = [];
  for (let i = 0; i < 12; i++) {
    samples.push(await page.evaluate(() => {
      const fill = document.getElementById('healthFill');
      const fr = fill.getBoundingClientRect();
      let nearest = Infinity;
      for (const b of fill.querySelectorAll('b')) {
        if (Number(getComputedStyle(b).opacity) < 0.3) continue;
        nearest = Math.min(nearest, fr.right - b.getBoundingClientRect().right);
      }
      return { fillWidth: fr.width, leading: isFinite(nearest) ? nearest : fr.width };
    }));
    await page.waitForTimeout(300);
  }
  const fillWidth = samples[0].fillWidth;
  const avgLeading = samples.reduce((sum, s) => sum + s.leading, 0) / samples.length;
  const ratio = avgLeading / fillWidth;
  console.log(`info: leading gap avg ${avgLeading.toFixed(0)}px of ${fillWidth.toFixed(0)}px fill (${(ratio * 100).toFixed(1)}%), pre-fix baseline was 118px / 11.3%`);
  check('leading gap stays under 7% of the fill width',
    ratio < 0.05,
    `avg ${avgLeading.toFixed(0)}px of ${fillWidth.toFixed(0)}px (${(ratio * 100).toFixed(1)}%)`);

  if (outDir) {
    mkdirSync(outDir, { recursive: true });
    const shot = join(outDir, 'health-particle-density.png');
    await page.locator('#healthPanel .health-scrub').screenshot({ path: shot });
    console.log('screenshot: ' + shot);
  }

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
