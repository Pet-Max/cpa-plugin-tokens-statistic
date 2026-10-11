import { chromium } from 'playwright-core';
import { readDashboardFile, startDashboardServer, authenticate, defaultFixtures, installExportCapture, waitForExports, readCapturedExports } from './helpers.mjs';

const [htmlPath, chromePath] = process.argv.slice(2);
if (!htmlPath || !chromePath) {
  throw new Error('usage: node test/dashboard_export.mjs <dashboard-html-path> <google-chrome-path>');
}

const dashboardHTML = await readDashboardFile(htmlPath);
const resourceBase = '/v0/resource/plugins/export-browser-test';
const managementBase = '/v0/management/plugins/export-browser-test';
const GOOD_KEY = 'export-browser-key';
const EXPORT_MODEL = 'export-browser-model';

const groupRow = {
  model: EXPORT_MODEL,
  provider: 'openai',
  source: 'cli',
  executor_type: 'openai',
  auth_type: 'api_key',
  service_tier: '',
  reasoning_effort: '',
  requests: 2,
  failed_requests: 0,
  input_tokens: 10,
  output_tokens: 20,
  reasoning_tokens: 0,
  cache_read_tokens: 0,
  cache_creation_tokens: 0,
  total_tokens: 30,
  average_ttft_ns: 200000,
  average_latency_ns: 1000000,
  api_key_ref: 'g1:ab',
  api_key: '',
  api_key_status: 'available',
  price_source: 'models.dev',
};
const requestRow = {
  sequence: 1,
  time: '2026-08-23T11:59:00.000Z',
  model: EXPORT_MODEL,
  provider: 'openai',
  source: 'cli',
  result: 'success',
  input_tokens: 10,
  output_tokens: 20,
  reasoning_tokens: 0,
  cache_read_tokens: 0,
  cache_creation_tokens: 0,
  total_tokens: 30,
  latency_ns: 1000000,
  ttft_ns: 200000,
  generation_ns: 800000,
  tps: 25,
  cache_hit: false,
  estimated_cost: { priced: true, input_usd: 1, output_usd: 2, cache_read_usd: 0, cache_creation_usd: 0, total_usd: 3, source: 'models.dev', accounting_mode: 'token', tier_threshold: 0 },
};

const host = startDashboardServer({
  dashboardHTML,
  pluginId: 'export-browser-test',
  fixtures: {
    ...defaultFixtures({ model: EXPORT_MODEL }),
    requests: {
      generated_at: '2026-08-23T12:00:00.000Z',
      range: '24h',
      total: 1,
      offset: 0,
      limit: 100,
      items: [{
        sequence: 1,
        time: '2026-08-23T11:59:00.000Z',
        model: EXPORT_MODEL,
        provider: 'openai',
        source: 'cli',
        result: 'success',
        input_tokens: 10,
        output_tokens: 20,
        reasoning_tokens: 0,
        cache_read_tokens: 0,
        cache_creation_tokens: 0,
        total_tokens: 30,
        latency_ns: 1000000,
        ttft_ns: 200000,
        generation_ns: 800000,
        tps: 25,
        cache_hit: false,
        estimated_cost: { priced: true, input_usd: 1, output_usd: 2, cache_read_usd: 0, cache_creation_usd: 0, total_usd: 3, source: 'models.dev', accounting_mode: 'token', tier_threshold: 0 },
      }],
    },
    costs: {
      summary: { requests: 1, priced_requests: 1, unpriced_requests: 0 },
      models: [{ model: EXPORT_MODEL, input_usd: 1, output_usd: 2, cache_read_usd: 0, cache_creation_usd: 0, total_usd: 3, priced_requests: 1 }],
      price_book_revision: 3,
      missing_prices: [],
    },
    priceBook: { prices: { [EXPORT_MODEL]: { input: 0.5, output: 1.5, source: 'models.dev' } }, revision: 3, sync_settings: { provider_priority: [], ignored_suffixes: [], mappings: [] } },
    exchangeRate: { schema_version: 1, base: 'USD', quote: 'CNY', rate: 7.5, effective_at: '2026-08-23T12:00:00.000Z', fetched_at: '2026-08-23T12:00:00.000Z', source: 'test', stale: false },
  },
});
const dashboardURL = await host.ready;
const browser = await chromium.launch({ executablePath: chromePath, headless: true });

try {
  const context = await browser.newContext({ locale: 'zh-CN', timezoneId: 'UTC' });
  const page = await context.newPage();
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(String(error)));

  installExportCapture(context);

  await page.clock.install({ time: new Date('2026-08-23T12:00:00.000Z') });
  await page.goto(dashboardURL, { waitUntil: 'domcontentloaded' });

  await authenticate(page, { managementBase: host.managementBase, key: GOOD_KEY });

  // The export menu opens from the toolbar and lists the three exports.
  await page.locator('#exportButton').click();
  await page.locator('#exportMenu').waitFor({ state: 'visible' });
  const menuItems = await page.evaluate(() =>
    ['exportCSV', 'exportPNG', 'exportBackup'].map((id) => ({ id, hidden: document.getElementById(id).hidden })),
  );
  if (menuItems.some((item) => item.hidden)) {
    throw new Error(`export menu items must be visible: ${JSON.stringify(menuItems)}`);
  }

  // CSV export builds both detail files in one action: BOM-prefixed content,
  // the documented filenames, and the exported model inside.
  await page.locator('#exportCSV').click();
  await waitForExports(page, 2);
  const usdExports = await readCapturedExports(page);
  if (usdExports.length !== 2) {
    throw new Error(`one CSV click must export exactly two files, got ${usdExports.length}`);
  }
  const requestCsv = usdExports.find((item) => /^request-details-/.test(item.name || ''));
  const modelCsv = usdExports.find((item) => /^model-details-/.test(item.name || ''));
  if (!requestCsv || !modelCsv) {
    throw new Error(`CSV exports missing: ${JSON.stringify(usdExports.map((item) => item.name))}`);
  }
  const bom = [0xef, 0xbb, 0xbf];
  if (requestCsv.head.length !== 3 || bom.some((byte, index) => requestCsv.head[index] !== byte)) {
    throw new Error(`request CSV must start with a UTF-8 BOM, got ${JSON.stringify(requestCsv.head)}`);
  }
  if (!modelCsv.text.includes(EXPORT_MODEL)) {
    throw new Error(`request CSV must contain the exported model: ${modelCsv.text.slice(0, 400)}`);
  }
  if (!modelCsv.text.includes(EXPORT_MODEL)) {
    throw new Error(`model CSV must contain the exported model: ${modelCsv.text.slice(0, 400)}`);
  }
  if (!modelCsv.text.includes('USD')) {
    throw new Error(`model CSV cost headers must be USD before the currency switch: ${modelCsv.text.slice(0, 400)}`);
  }

  // Switching the cost card to ￥ fetches and caches the exchange rate, and
  // the next model export must replace USD with CNY in the cost headers.
  await page.locator('#costCurrencyButton').click();
  await page.waitForFunction(() => document.getElementById('costCurrencyButton').textContent === '￥', undefined, { timeout: 5000 });
  if (!host.exchangeRateRequested()) {
    throw new Error('switching to CNY must fetch the /exchange-rate endpoint');
  }
  await page.locator('#exportButton').click();
  await page.locator('#exportMenu').waitFor({ state: 'visible' });
  await page.locator('#exportCSV').click();
  await waitForExports(page, 4);
  const cnyExports = (await readCapturedExports(page)).slice(2);
  const cnyModelCsv = cnyExports.find((item) => /^model-details-/.test(item.name || ''));
  if (!cnyModelCsv) {
    throw new Error(`CNY run must export the model details: ${JSON.stringify(cnyExports.map((item) => item.name))}`);
  }
  if (!cnyModelCsv.text.includes('CNY')) {
    throw new Error(`CNY export must replace USD headers: ${cnyModelCsv.text.slice(0, 400)}`);
  }

  // PNG export renders the analysis canvas and exports a named PNG blob.
  await page.locator('#exportButton').click();
  await page.locator('#exportMenu').waitFor({ state: 'visible' });
  await page.locator('#exportPNG').click();
  await page.waitForFunction(() => window.__exports.length >= 5 && window.__exports[4].name !== null, undefined, { timeout: 10000 });
  const pngExport = await page.evaluate(() => {
    const item = window.__exports[4];
    return { name: item.name, type: item.type, size: item.size };
  });
  if (!/^tokens-analysis-.*\.png$/.test(pngExport.name || '')) {
    throw new Error(`PNG export filename mismatch: ${JSON.stringify(pngExport)}`);
  }
  if (pngExport.type !== 'image/png' || pngExport.size === 0) {
    throw new Error(`PNG export must carry a non-empty image: ${JSON.stringify(pngExport)}`);
  }

  if (pageErrors.length) {
    throw new Error(`page errors: ${pageErrors.join(' | ')}`);
  }
} finally {
  await browser.close();
  host.close();
}
