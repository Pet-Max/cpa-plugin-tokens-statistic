import { chromium } from 'playwright-core';
import { readDashboardFile, startDashboardServer, authenticate, defaultFixtures } from './helpers.mjs';

const [htmlPath, chromePath] = process.argv.slice(2);
if (!htmlPath || !chromePath) {
  throw new Error('usage: node test/dashboard_pricing.mjs <dashboard-html-path> <google-chrome-path>');
}

const dashboardHTML = await readDashboardFile(htmlPath);
const resourceBase = '/v0/resource/plugins/pricing-browser-test';
const managementBase = '/v0/management/plugins/pricing-browser-test';
const GOOD_KEY = 'pricing-browser-key';
const MANUAL_MODEL = 'openai/gpt-test';
const REFERENCE_MODEL = 'anthropic/claude-ref';

const baseBook = {
  prices: {
    [MANUAL_MODEL]: { input: 0.5, output: 1.5, cache_read: 0.25, cache_creation: 1.25, source: 'manual' },
    [REFERENCE_MODEL]: { input: 1, output: 2, cache_read: 0.5, cache_creation: 2, source: 'models.dev', catalog_provider: 'anthropic', catalog_model: 'claude-ref' },
  },
  revision: 3,
  sync_settings: { provider_priority: [], ignored_suffixes: [], mappings: [] },
  last_sync: null,
};

const capturedPut = [];
const capturedSync = [];

const host = startDashboardServer({
  dashboardHTML,
  pluginId: 'pricing-browser-test',
  fixtures: {
    ...defaultFixtures({ model: 'pricing-browser-model' }),
    priceBook: {
      prices: {
        [MANUAL_MODEL]: { input: 0.5, output: 1.5, cache_read: 0.25, cache_creation: 1.25, source: 'manual' },
        [REFERENCE_MODEL]: { input: 1, output: 2, cache_read: 0.5, cache_creation: 2, source: 'models.dev', catalog_provider: 'anthropic', catalog_model: 'claude-ref' },
      },
      revision: 3,
      sync_settings: { provider_priority: [], ignored_suffixes: [], mappings: [] },
      last_sync: null,
    },
    exchangeRate: { schema_version: 1, base: 'USD', quote: 'CNY', rate: 7.5, effective_at: '2026-08-23T12:00:00.000Z', fetched_at: '2026-08-23T12:00:00.000Z', source: 'test', stale: false },
    modelsCatalog: { object: 'list', data: [{ id: MANUAL_MODEL }, { id: REFERENCE_MODEL }] },
  },
  capture: { putPrices: capturedPut, postSync: capturedSync },
});
const dashboardURL = await host.ready;
const browser = await chromium.launch({ executablePath: chromePath, headless: true });

try {
  const context = await browser.newContext({ locale: 'zh-CN', timezoneId: 'UTC' });
  const page = await context.newPage();
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(String(error)));

  await page.clock.install({ time: new Date('2026-08-23T12:00:00.000Z') });
  await page.goto(dashboardURL, { waitUntil: 'domcontentloaded' });

  await authenticate(page, { managementBase: host.managementBase, key: GOOD_KEY });

  // Open the pricing dialog straight from the toolbar button.
  await page.locator('#pricingButton').click();
  await page.waitForFunction(() => document.getElementById('pricingDialog') && document.getElementById('pricingDialog').open);
  const pricingOpen = await page.evaluate(() => document.getElementById('pricingDialog').open);
  if (!pricingOpen) {
    throw new Error('pricing dialog must open from the toolbar button');
  }

  // Both price rows render with the correct provenance tags.
  await page.waitForFunction(() => document.querySelectorAll('#priceList tr[data-model]').length >= 2);
  const tags = await page.evaluate(() => {
    const read = (model) => {
      const row = document.querySelector(`#priceList tr[data-model="${model}"]`);
      return row ? row.querySelector('.tag').textContent : null;
    };
    return { manual: read('openai/gpt-test'), reference: read('anthropic/claude-ref') };
  });
  if (tags.manual === null || tags.reference === null) {
    throw new Error(`price rows missing: ${JSON.stringify(tags)}`);
  }
  if (tags.manual === tags.reference) {
    throw new Error(`manual and models.dev entries must show different provenance tags: ${JSON.stringify(tags)}`);
  }

  // The models.dev reference row must lock removal; the manual row stays editable.
  const lockState = await page.evaluate(() => ({
    manualRemoveDisabled: document.querySelector('#priceList tr[data-model="openai/gpt-test"] .remove-model-price').disabled,
    referenceRemoveDisabled: document.querySelector('#priceList tr[data-model="anthropic/claude-ref"] .remove-model-price').disabled,
  }));
  if (lockState.manualRemoveDisabled || !lockState.referenceRemoveDisabled) {
    throw new Error(`reference rows must lock removal, manual rows must allow it: ${JSON.stringify(lockState)}`);
  }

  // Search narrows the table to the matching model only.
  await page.locator('#priceSearch').fill('claude');
  const searchState = await page.evaluate(() => ({
    visible: Array.from(document.querySelectorAll('#priceList tr[data-model]'))
      .filter((row) => row.style.display !== 'none')
      .map((row) => row.dataset.model),
  }));
  if (searchState.visible.length !== 1 || searchState.visible[0] !== REFERENCE_MODEL) {
    throw new Error(`search "claude" must leave only the reference row: ${JSON.stringify(searchState)}`);
  }
  await page.locator('#priceSearch').fill('');

  // Edit the manual entry: the dialog pre-fills persisted values, saving PUTs
  // the whole book with the edited numbers and preserved cache prices.
  await page.locator('#priceList tr[data-model="openai/gpt-test"] .price-edit-link').click();
  await page.waitForFunction(() => document.getElementById('priceEditDialog') && document.getElementById('priceEditDialog').open);
  const prefill = await page.evaluate(() => ({
    model: document.getElementById('priceEditModel').value,
    input: document.getElementById('priceEditInput').value,
    output: document.getElementById('priceEditOutput').value,
    cacheRead: document.getElementById('priceEditCacheRead').value,
    cacheWrite: document.getElementById('priceEditCacheWrite').value,
  }));
  if (prefill.model !== MANUAL_MODEL || prefill.input !== '0.5' || prefill.output !== '1.5' || prefill.cacheRead !== '0.25' || prefill.cacheWrite !== '1.25') {
    throw new Error(`price edit dialog prefill mismatch: ${JSON.stringify(prefill)}`);
  }

  await page.locator('#priceEditInput').fill('0.9');
  await page.locator('#priceEditOutput').fill('2.5');
  await page.locator('#priceEditSave').click();
  await page.waitForFunction(() => document.getElementById('priceRevision').textContent.indexOf('4') >= 0, undefined, { timeout: 5000 });

  if (!capturedPut.length || !capturedPut[0].prices || !capturedPut[0].prices[MANUAL_MODEL]) {
    throw new Error(`save must PUT the price book, got ${JSON.stringify(capturedPut)}`);
  }
  const savedEntry = capturedPut[0].prices[MANUAL_MODEL];
  if (savedEntry.input !== 0.9 || savedEntry.output !== 2.5) {
    throw new Error(`edited prices missing from PUT body: ${JSON.stringify(savedEntry)}`);
  }
  if (savedEntry.cache_read !== 0.25 || savedEntry.cache_creation !== 1.25) {
    throw new Error(`untouched cache prices must be preserved: ${JSON.stringify(savedEntry)}`);
  }
  if (capturedPut[0].prices[REFERENCE_MODEL].source !== 'models.dev') {
    throw new Error(`sibling entries must survive the save: ${JSON.stringify(Object.keys(capturedPut.prices))}`);
  }

  // After the save the edit dialog closes, the book reloads at the new
  // revision, and the row shows the new unit price.
  await page.waitForFunction(() => !document.getElementById('priceEditDialog').open);
  await page.waitForFunction(() => document.getElementById('priceRevision').textContent.indexOf('4') >= 0, undefined, { timeout: 5000 });
  await page.waitForFunction(() => {
    const row = document.querySelector('#priceList tr[data-model="openai/gpt-test"]');
    return row && row.textContent.indexOf('$0.9') >= 0;
  }, undefined, { timeout: 5000 });

  // Sync button must POST a models.dev request and bump the revision again.
  await page.locator('#cliModelsKeyInput').fill('catalog-browser-key');
  await page.locator('#syncPrices').click();
  await page.waitForFunction(() => document.getElementById('priceRevision').textContent.indexOf('5') >= 0, undefined, { timeout: 5000 });
  if (!capturedSync.length || capturedSync[0].source !== 'models.dev' || !capturedSync[0].sync_settings) {
    throw new Error(`sync must POST a models.dev request with sync settings, got ${JSON.stringify(capturedSync)}`);
  }
  await page.waitForFunction(() => document.getElementById('priceRevision').textContent.indexOf('5') >= 0, undefined, { timeout: 5000 });

  if (pageErrors.length) {
    throw new Error(`page errors: ${pageErrors.join(' | ')}`);
  }
} finally {
  await browser.close();
  host.close();
}
