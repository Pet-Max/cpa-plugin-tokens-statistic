// Unnamed-model drill-down probe: clicking the localized "unnamed model"
// dimension row must send the locale-neutral __untitled__ filter to every
// server query (groups / requests / scope_model / CSV exports), and the reset
// button must clear the drill-down completely.
//
// Regression guard: before the fix the dashboard sent the localized label
// (e.g. "Unnamed model"), which ModelFilterMatches could never match in
// non-zh-CN locales, leaving the request table permanently empty.
import { chromium } from 'playwright-core';
import { mkdirSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'url';
import { dirname, join } from 'node:path';
import { startDashboardServer, defaultFixtures } from './helpers.mjs';

const [htmlPath, chromePath, outDir] = process.argv.slice(2);
if (!htmlPath || !chromePath) {
  throw new Error('usage: node test/model-drilldown.mjs <dashboard-html-path> <google-chrome-path> [screenshot-dir]');
}

// Bake the placeholders exactly like the Go plugin does at startup.
const repoRoot = join(dirname(fileURLToPath(import.meta.url)), '..');
const localeData = {};
for (const code of ['en', 'zh-CN', 'zh-TW', 'ru']) {
  localeData[code] = JSON.parse(await readFile(join(repoRoot, 'internal', 'plugin', 'web', 'locales', code + '.json'), 'utf-8'));
}
const raw = await readFile(htmlPath, 'utf-8');
const dashboardHTML = raw
  .replace('/*LOCALE_PLACEHOLDER*/', JSON.stringify(localeData))
  .replaceAll('__CARD_LOGO_DATA_URI__', 'data:image/gif;base64,R0lGODlhAQABAIAAAP///wAAACH5BAEAAAAALAAAAAABAAEAAAICRAEAOw==');

const untitledLabel = localeData.en['model.untitled'];
if (!untitledLabel) throw new Error('en locale is missing model.untitled');

const fixtures = defaultFixtures();
const baseItem = fixtures.requests.items[0];
const unnamedGroup = {
  model: '', provider: 'openai-compatibility', executor_type: 'openaicompatexecutor', source: 'cli',
  auth_type: 'api_key', service_tier: '', reasoning_effort: '', requests: 3, failed_requests: 1,
  input_tokens: 3000, output_tokens: 1500, reasoning_tokens: 0, cache_read_tokens: 0,
  cache_creation_tokens: 0, total_tokens: 4500, average_ttft_ns: 1e6, average_latency_ns: 2e6,
  api_key_ref: '', api_key: '', api_key_status: 'available', price_source: 'unpriced',
};
fixtures.statsGroups.items = [unnamedGroup, ...fixtures.statsGroups.items];
fixtures.statsInitial.models = [
  { model: '', provider: 'openai-compatibility', requests: 3, input_tokens: 3000, output_tokens: 1500, cache_read_tokens: 0, cache_creation_tokens: 0, total_tokens: 4500 },
  ...fixtures.statsInitial.models,
];
fixtures.requests.items = [{ ...baseItem, model: '' }, ...fixtures.requests.items];
fixtures.requests.total = fixtures.requests.items.length;

const host = startDashboardServer({ dashboardHTML, pluginId: 'model-drilldown-browser-test', fixtures });
const dashboardURL = await host.ready;
const browser = await chromium.launch({ executablePath: chromePath, headless: true });

const results = [];
const check = (name, ok, detail = '') => results.push((ok ? 'PASS ' : 'FAIL ') + name + (ok ? '' : ' — ' + detail));

try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 950 }, deviceScaleFactor: 2, locale: 'en-US', timezoneId: 'UTC' });
  await context.addInitScript((key) => { sessionStorage.setItem('tokens-statistic-key', key); }, 'model-drilldown-key');
  const page = await context.newPage();
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(String(error.stack || error).slice(0, 500)));

  const sentQueries = [];
  const allRequests = [];
  page.on('request', (r) => {
    const u = new URL(r.url());
    const model = u.searchParams.get('model');
    const endpoint = u.pathname.slice(u.pathname.lastIndexOf('/'));
    if (u.pathname.endsWith('/requests') || u.pathname.endsWith('/groups')) {
      sentQueries.push({ endpoint, model });
    }
    allRequests.push({ endpoint, model });
  });

  await page.goto(dashboardURL, { waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => document.querySelectorAll('.health-dot').length === 72);
  // The dimension table only renders once the stats payload has landed; wait
  // for the unnamed-model cell instead of racing the initial load.
  await page.waitForFunction(() => document.querySelectorAll('.model-cell').length > 0, undefined, { timeout: 10000 });

  // Poll captured server queries instead of racing playwright events: the
  // drill-down may legitimately skip loadGroups when stats data is already
  // current, and per-request timing proved flaky under the Go runner.
  const waitSent = async (pred, since, timeout = 8000) => {
    const end = Date.now() + timeout;
    while (Date.now() < end) {
      if (sentQueries.slice(since).some(pred)) return true;
      await page.waitForTimeout(150);
    }
    return false;
  };
  const isUntitled = (q) => q.model === '__untitled__';

  const beforeDrill = sentQueries.length;
  const beforeClick = allRequests.length;
  await page.locator('.model-cell', { hasText: untitledLabel }).first().click();
  const drilledGroups = await waitSent((q) => q.endpoint === '/groups' && isUntitled(q), beforeDrill);
  const drilledRequests = await waitSent((q) => q.endpoint === '/requests' && isUntitled(q), beforeDrill);
  check('drill-down sends __untitled__ to groups', drilledGroups, JSON.stringify(sentQueries.slice(beforeDrill)));
  check('drill-down sends __untitled__ to requests', drilledRequests, JSON.stringify({ captured: sentQueries.slice(beforeDrill), allAfterClick: allRequests.slice(beforeClick) }));
  check('drill-down does not send the localized label', !sentQueries.some((q) => q.model === untitledLabel), JSON.stringify(sentQueries));

  const subtitle = await page.locator('#analysisSubtitle').textContent();
  check('subtitle shows the drill-down label', subtitle.includes(untitledLabel), subtitle);
  check('reset button visible', await page.locator('#resetModelButton').isVisible());

  if (outDir) {
    mkdirSync(outDir, { recursive: true });
    await page.screenshot({ path: join(outDir, 'model-drilldown-untitled.png') });
    console.log('screenshot: ' + join(outDir, 'model-drilldown-untitled.png'));
  }

  // Reset must clear everything and reload unfiltered.
  const beforeReset = sentQueries.length;
  await page.click('#resetModelButton');
  const resetCleared = await waitSent((q) => q.model === null, beforeReset);
  check('reset reloads groups and requests without a model filter', resetCleared, JSON.stringify(sentQueries.slice(beforeReset)));
  check('reset hides the button', await page.locator('#resetModelButton').isHidden());
  check('subtitle restored', !(await page.locator('#analysisSubtitle').textContent()).includes(untitledLabel));

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
