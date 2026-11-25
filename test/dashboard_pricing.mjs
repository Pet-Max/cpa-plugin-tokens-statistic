import { chromium } from 'playwright-core';
import { readDashboardFile, startDashboardServer, authenticate, defaultFixtures } from './helpers.mjs';

const [htmlPath, chromePath] = process.argv.slice(2);
if (!htmlPath || !chromePath) {
  throw new Error('usage: node test/dashboard_pricing.mjs <dashboard-html-path> <google-chrome-path>');
}

const dashboardHTML = await readDashboardFile(htmlPath);
const managementBase = '/v0/management/plugins/pricing-browser-test';
const GOOD_KEY = 'pricing-browser-key';
const MANUAL_MODEL = 'openai/gpt-test';
const REFERENCE_MODEL = 'anthropic/claude-ref';

const capturedPut = [];
const capturedSync = [];
let failNextSync = false;
let holdNextGetPricesGate = null;
let holdGetPricesRequested = false;

const host = startDashboardServer({
  dashboardHTML,
  pluginId: 'pricing-browser-test',
  fixtures: {
    ...defaultFixtures({ model: 'pricing-browser-model' }),
    priceBook: {
      prices: {
        [MANUAL_MODEL]: { input: 0.5, output: 1.5, cache_read: 0.25, cache_creation: 1.25, source: 'manual' },
        [REFERENCE_MODEL]: { input: 1, output: 2, cache_read: 0.5, cache_creation: 2, source: 'models.dev', catalog_provider: 'anthropic', catalog_model: 'claude-ref' },
        '未标记模型': {
          input: 1.2,
          output: 2.4,
          context_tiers: [{ threshold: 200000, input: 3, output: 6, cache_read: 0.3, cache_creation: 3 }],
          source: 'manual',
        },
      },
      revision: 3,
      sync_settings: { provider_priority: [], ignored_suffixes: [], mappings: [] },
      last_sync: null,
    },
    exchangeRate: { schema_version: 1, base: 'USD', quote: 'CNY', rate: 7.5, effective_at: '2026-08-23T12:00:00.000Z', fetched_at: '2026-08-23T12:00:00.000Z', source: 'test', stale: false },
    modelsCatalog: { object: 'list', data: [{ id: MANUAL_MODEL }, { id: REFERENCE_MODEL }, { id: 'pricing-browser-model' }] },
  },
  capture: { putPrices: capturedPut, postSync: capturedSync, postPreferences: [] },
  rawHandler: (request, response, url) => {
    if (url.pathname === `${managementBase}/prices/sync` && request.method === 'POST' && failNextSync) {
      failNextSync = false;
      response.writeHead(500, { 'content-type': 'application/json' });
      response.end(JSON.stringify({ error: 'sync failed' }));
      return true;
    }
    if (url.pathname === `${managementBase}/prices` && request.method === 'GET' && holdNextGetPricesGate) {
      const gate = holdNextGetPricesGate;
      holdNextGetPricesGate = null;
      holdGetPricesRequested = true;
      const latestPut = capturedPut[capturedPut.length - 1];
      gate.promise.then(() => {
        response.writeHead(200, { 'content-type': 'application/json' });
        response.end(JSON.stringify({
          prices: (latestPut && latestPut.prices) || {},
          revision: 9,
          sync_settings: { provider_priority: [], ignored_suffixes: [], mappings: [] },
          last_sync: null,
        }));
      });
      return true;
    }
    if (url.pathname === `${managementBase}/costs`) {
      const priced = capturedPut.some((p) => p.prices && p.prices['pricing-browser-model']);
      response.writeHead(200, { 'content-type': 'application/json' });
      response.end(JSON.stringify(priced
        ? {
            summary: { requests: 2, priced_requests: 2, unpriced_requests: 0, total_usd: 1.23 },
            models: [{ model: 'pricing-browser-model', provider: 'openai', requests: 2, priced_requests: 2, unpriced_requests: 0, input_usd: 0.5, output_usd: 0.73, cache_read_usd: 0, cache_creation_usd: 0, total_usd: 1.23 }],
            price_book_revision: 8,
          }
        : { summary: { requests: 2, priced_requests: 0, unpriced_requests: 2 }, models: [], price_book_revision: 3 }));
      return true;
    }
    return false;
  },
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

  // Desktop pricing dialog reserves top safe clearance (>= 80px) with flush toolbar actions.
  const desktopGeometry = await page.evaluate(() => {
    const dlg = document.getElementById('pricingDialog').getBoundingClientRect();
    const actionsStyle = getComputedStyle(document.querySelector('#pricingDialog .pricing-toolbar-actions'));
    return { top: dlg.top, marginRight: actionsStyle.marginRight };
  });
  if (desktopGeometry.top < 80) {
    throw new Error(`desktop pricing dialog must reserve top safe offset (>= 80px), got ${JSON.stringify(desktopGeometry)}`);
  }
  if (desktopGeometry.marginRight !== '0px') {
    throw new Error(`pricing toolbar actions must not retain dead right margin, got ${desktopGeometry.marginRight}`);
  }

  // Default tab is Price list; switching to Sync settings and back works.
  if (!(await page.locator('#pricingTabList').isVisible()) || (await page.locator('#pricingTabSync').isVisible())) {
    throw new Error('pricing dialog must default to the Price list tab');
  }
  await page.locator('#pricingTabSyncBtn').click();
  if ((await page.locator('#pricingTabList').isVisible()) || !(await page.locator('#pricingTabSync').isVisible())) {
    throw new Error('clicking Sync settings tab must reveal sync panel and hide price list');
  }
  await page.locator('#pricingTabListBtn').click();
  if (!(await page.locator('#pricingTabList').isVisible()) || (await page.locator('#pricingTabSync').isVisible())) {
    throw new Error('clicking Price list tab must restore price list panel');
  }
  // Arrow keys navigate across tabs and move focus to the activated tab button.
  await page.locator('#pricingTabListBtn').focus();
  await page.keyboard.press('ArrowRight');
  const tabKeyForward = await page.evaluate(() => ({
    activeId: document.activeElement?.id,
    syncSelected: document.getElementById('pricingTabSyncBtn').getAttribute('aria-selected'),
  }));
  if (tabKeyForward.activeId !== 'pricingTabSyncBtn' || tabKeyForward.syncSelected !== 'true') {
    throw new Error(`ArrowRight on pricing tablist must focus and activate Sync settings tab: ${JSON.stringify(tabKeyForward)}`);
  }
  await page.keyboard.press('ArrowLeft');
  const tabKeyBack = await page.evaluate(() => ({
    activeId: document.activeElement?.id,
    listSelected: document.getElementById('pricingTabListBtn').getAttribute('aria-selected'),
  }));
  if (tabKeyBack.activeId !== 'pricingTabListBtn' || tabKeyBack.listSelected !== 'true') {
    throw new Error(`ArrowLeft on pricing tablist must focus and restore Price list tab: ${JSON.stringify(tabKeyBack)}`);
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

  // The models.dev reference row must lock removal and restore; the manual row stays editable and restorable.
  const lockState = await page.evaluate(() => ({
    manualRemoveDisabled: document.querySelector('#priceList tr[data-model="openai/gpt-test"] .remove-model-price').disabled,
    referenceRemoveDisabled: document.querySelector('#priceList tr[data-model="anthropic/claude-ref"] .remove-model-price').disabled,
    manualRestoreDisabled: document.querySelector('#priceList tr[data-model="openai/gpt-test"] .restore-price').disabled,
    referenceRestoreDisabled: document.querySelector('#priceList tr[data-model="anthropic/claude-ref"] .restore-price').disabled,
  }));
  if (lockState.manualRemoveDisabled || !lockState.referenceRemoveDisabled || lockState.manualRestoreDisabled || !lockState.referenceRestoreDisabled) {
    throw new Error(`reference rows must lock removal/restore, manual rows must allow both: ${JSON.stringify(lockState)}`);
  }

  // Inline validation errors must show inside #priceEditDialog (not behind it in #pricingDialog).
  // Adding a new price must NOT inherit context_tiers from "未标记模型".
  await page.locator('#addPriceEntry').click();
  await page.waitForFunction(() => document.getElementById('priceEditDialog') && document.getElementById('priceEditDialog').open);
  const addTierLeak = await page.evaluate(() => ({
    checked: document.getElementById('priceEditTiersEnabled').checked,
    tierRows: document.querySelectorAll('#priceEditTierRows .context-tier-row').length,
  }));
  if (addTierLeak.checked || addTierLeak.tierRows !== 0) {
    throw new Error(`adding a new price must not inherit context_tiers from untitled model: ${JSON.stringify(addTierLeak)}`);
  }
  await page.locator('#priceEditSave').click();
  const editErrorText = (await page.locator('#priceEditError').textContent()).trim();
  if (!editErrorText) {
    throw new Error('priceEditSave with empty model ID must surface validation error inside #priceEditDialog');
  }
  await page.locator('#priceEditCancel').click();
  await page.waitForFunction(() => !document.getElementById('priceEditDialog').open);

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
    throw new Error(`sibling entries must survive the save: ${JSON.stringify(Object.keys(capturedPut[0].prices))}`);
  }

  // After the save the edit dialog closes, the book reloads at the new
  // revision, and the row shows the new unit price.
  await page.waitForFunction(() => !document.getElementById('priceEditDialog').open);
  await page.waitForFunction(() => document.getElementById('priceRevision').textContent.indexOf('4') >= 0, undefined, { timeout: 5000 });
  await page.waitForFunction(() => {
    const row = document.querySelector('#priceList tr[data-model="openai/gpt-test"]');
    return row && row.textContent.indexOf('$0.9') >= 0;
  }, undefined, { timeout: 5000 });

  // Clicking Sync without an API key must switch to Sync settings, focus the key input, and preserve guidance without stamping sync failure.
  await page.locator('#syncPrices').click();
  const emptyKeyState = await page.evaluate(() => ({
    syncVisible: !document.getElementById('pricingTabSync').hidden,
    activeId: document.activeElement?.id,
    priceError: document.getElementById('priceError').textContent.trim(),
    lastSync: document.getElementById('lastSyncStatus').textContent,
  }));
  if (!emptyKeyState.syncVisible || emptyKeyState.activeId !== 'cliModelsKeyInput' || !emptyKeyState.priceError || emptyKeyState.lastSync.includes('同步失败')) {
    throw new Error(`sync without API key must focus key input and show prompt: ${JSON.stringify(emptyKeyState)}`);
  }

  // Sync button must POST a models.dev request and bump the revision again.
  await page.locator('#cliModelsKeyInput').fill('catalog-browser-key');
  await page.locator('#syncPrices').click();
  await page.waitForFunction(() => document.getElementById('priceRevision').textContent.indexOf('5') >= 0, undefined, { timeout: 5000 });
  if (!capturedSync.length || capturedSync[0].source !== 'models.dev' || !capturedSync[0].sync_settings) {
    throw new Error(`sync must POST a models.dev request with sync settings, got ${JSON.stringify(capturedSync)}`);
  }
  await page.waitForFunction(() => document.getElementById('priceRevision').textContent.indexOf('5') >= 0, undefined, { timeout: 5000 });

  // Switch back to Price list and verify custom confirm modal on Restore (both cancel and confirm).
  await page.locator('#pricingTabListBtn').click();
  await page.locator('#priceList tr[data-model="openai/gpt-test"] .restore-price').click();
  await page.waitForFunction(() => document.getElementById('priceConfirmDialog') && document.getElementById('priceConfirmDialog').open);
  await page.locator('#priceConfirmCancel').click();
  await page.waitForFunction(() => !document.getElementById('priceConfirmDialog').open);

  // Restore must validate sync settings BEFORE deleting the custom entry via PUT.
  await page.locator('#pricingTabSyncBtn').click();
  await page.locator('#syncMappings').fill('bad-mapping-line');
  await page.locator('#pricingTabListBtn').click();
  const putCountBeforeInvalid = capturedPut.length;
  const syncCountBeforeInvalid = capturedSync.length;
  await page.locator('#priceList tr[data-model="openai/gpt-test"] .restore-price').click();
  await page.waitForFunction(() => document.getElementById('priceConfirmDialog') && document.getElementById('priceConfirmDialog').open);
  await page.locator('#priceConfirmOk').click();
  await page.waitForFunction(() => !document.getElementById('priceConfirmDialog').open);
  await page.waitForTimeout(150);
  const invalidRestoreState = await page.evaluate(() => ({
    syncVisible: !document.getElementById('pricingTabSync').hidden,
    priceError: document.getElementById('priceError').textContent.trim(),
  }));
  if (capturedPut.length !== putCountBeforeInvalid || capturedSync.length !== syncCountBeforeInvalid || !invalidRestoreState.syncVisible || !invalidRestoreState.priceError) {
    throw new Error(`restore with invalid sync mappings must fail before PUT and focus Sync tab: ${JSON.stringify({ putDiff: capturedPut.length - putCountBeforeInvalid, syncDiff: capturedSync.length - syncCountBeforeInvalid, ...invalidRestoreState })}`);
  }
  await page.locator('#syncMappings').fill('');
  await page.locator('#pricingTabListBtn').click();

  const putCountBeforeRestore = capturedPut.length;
  const syncCountBeforeRestore = capturedSync.length;
  await page.locator('#priceList tr[data-model="openai/gpt-test"] .restore-price').click();
  await page.waitForFunction(() => document.getElementById('priceConfirmDialog') && document.getElementById('priceConfirmDialog').open);
  await page.locator('#priceConfirmOk').click();
  await page.waitForFunction(() => !document.getElementById('priceConfirmDialog').open);
  await page.waitForFunction(() => document.getElementById('priceRevision').textContent.indexOf('7') >= 0, undefined, { timeout: 5000 });
  if (capturedPut.length <= putCountBeforeRestore || capturedSync.length <= syncCountBeforeRestore) {
    throw new Error('confirming restore must send PUT without the custom model and POST sync');
  }

  // Adding a price while on Sync settings tab must auto-switch back to Price list on save.
  await page.locator('#pricingTabSyncBtn').click();
  await page.locator('#addPriceEntry').click();
  await page.waitForFunction(() => document.getElementById('priceEditDialog') && document.getElementById('priceEditDialog').open);
  await page.locator('#priceEditModel').fill('openai/gpt-added');
  await page.locator('#priceEditInput').fill('1');
  await page.locator('#priceEditOutput').fill('2');
  await page.locator('#priceEditCacheRead').fill('0');
  await page.locator('#priceEditCacheWrite').fill('0');
  await page.locator('#priceEditSave').click();
  await page.waitForFunction(() => !document.getElementById('priceEditDialog').open, undefined, { timeout: 5000 });
  await page.waitForFunction(() => !document.getElementById('pricingTabList').hidden && document.getElementById('pricingTabSync').hidden, undefined, { timeout: 5000 });

  // Unpriced catalog rows must disable remove/restore without claiming reference-price tooltips.
  const unpricedHints = await page.evaluate(() => {
    const row = document.querySelector('#priceList tr[data-model="pricing-browser-model"]');
    return row
      ? {
          removeDisabled: row.querySelector('.remove-model-price')?.disabled,
          removeTitle: row.querySelector('.remove-model-price')?.title,
          restoreDisabled: row.querySelector('.restore-price')?.disabled,
          restoreTitle: row.querySelector('.restore-price')?.title,
        }
      : null;
  });
  if (!unpricedHints || !unpricedHints.removeDisabled || !unpricedHints.restoreDisabled || unpricedHints.removeTitle !== '' || unpricedHints.restoreTitle !== '') {
    throw new Error(`unpriced row buttons must be disabled with empty titles: ${JSON.stringify(unpricedHints)}`);
  }

  // Saving a price for a model shown in #groups must refresh its cost cell immediately without waiting for the 15s tick.
  await page.locator('#priceList tr[data-model="pricing-browser-model"] .price-edit-link').click();
  await page.waitForFunction(() => document.getElementById('priceEditDialog') && document.getElementById('priceEditDialog').open);
  await page.locator('#priceEditInput').fill('2');
  await page.locator('#priceEditOutput').fill('4');
  await page.locator('#priceEditSave').click();
  await page.waitForFunction(() => !document.getElementById('priceEditDialog').open, undefined, { timeout: 5000 });
  const groupCostAfterSave = await page.evaluate(() => {
    const row = Array.from(document.querySelectorAll('#groups tr')).find((tr) => tr.querySelector('td[data-column="model"]')?.textContent === 'pricing-browser-model');
    return row ? row.querySelector('td[data-column="estimated_cost"]')?.textContent : null;
  });
  if (!groupCostAfterSave || groupCostAfterSave.indexOf('1.23') < 0) {
    throw new Error(`saving price edit must refresh #groups cost cell immediately, got ${JSON.stringify(groupCostAfterSave)}`);
  }

  // If restore's /prices/sync fails after PUT deletion, #pricingDialog must stay busy (and block Escape) until recovery GET /prices finishes.
  let resolveRecoveryPrices;
  holdNextGetPricesGate = { promise: new Promise((resolve) => { resolveRecoveryPrices = resolve; }), resolve: resolveRecoveryPrices };
  holdGetPricesRequested = false;
  failNextSync = true;
  await page.locator('#priceList tr[data-model="openai/gpt-added"] .restore-price').click();
  await page.waitForFunction(() => document.getElementById('priceConfirmDialog') && document.getElementById('priceConfirmDialog').open);
  await page.locator('#priceConfirmOk').click();
  await page.waitForFunction(() => !document.getElementById('priceConfirmDialog').open);
  while (!holdGetPricesRequested) {
    await new Promise((r) => setTimeout(r, 15));
  }
  await page.waitForTimeout(30);
  await page.keyboard.press('Escape');
  await page.waitForTimeout(40);
  const busyRecoveryState = await page.evaluate(() => ({
    cancelDisabled: document.getElementById('cancelPricing').disabled,
    dialogOpen: document.getElementById('pricingDialog').open,
    dialogClosing: document.getElementById('pricingDialog').classList.contains('is-closing'),
    priceError: document.getElementById('priceError').textContent.trim(),
  }));
  if (!busyRecoveryState.cancelDisabled || !busyRecoveryState.dialogOpen || busyRecoveryState.dialogClosing || !busyRecoveryState.priceError.includes('sync failed')) {
    resolveRecoveryPrices();
    throw new Error(`restore sync failure must keep dialog busy and block Escape until recovery completes: ${JSON.stringify(busyRecoveryState)}`);
  }
  resolveRecoveryPrices();
  await page.waitForFunction(() => !document.getElementById('cancelPricing').disabled && !document.querySelector('#priceList tr[data-model="openai/gpt-added"]'), undefined, { timeout: 5000 });

  // Close pricing dialog and verify Tokens Analysis (1:1 bars + axis units + tooltip) on desktop & mobile.
  await page.locator('#cancelPricing').click();
  await page.waitForFunction(() => !document.getElementById('pricingDialog').open);

  const desktopAnalysis = await page.evaluate(() => {
    const svg = document.getElementById('analysisChart');
    const bars = Array.from(svg.querySelectorAll('.analysis-bar')).map((el) => Number(el.getAttribute('height') || 0));
    const units = Array.from(svg.querySelectorAll('.axis-unit')).map((el) => el.textContent);
    const svgRect = svg.getBoundingClientRect();
    return { barCount: bars.length, maxBar: bars.length ? Math.max(...bars) : 0, units, svgHeight: svgRect.height };
  });
  if (desktopAnalysis.barCount < 1 || desktopAnalysis.maxBar <= desktopAnalysis.svgHeight * 0.5) {
    throw new Error(`desktop analysis bars must scale against full plot height, got ${JSON.stringify(desktopAnalysis)}`);
  }
  if (!desktopAnalysis.units.includes('次') || !desktopAnalysis.units.includes('%')) {
    throw new Error(`desktop analysis missing axis unit labels, got ${JSON.stringify(desktopAnalysis)}`);
  }

  // Combining requests with token series scales bars down as secondary backdrop and paints them before lines.
  await page.locator('.series-key-button[data-series="totalTokens"]').click();
  const dualScaleCheck = await page.evaluate(() => {
    const plot = document.getElementById('analysisPlot');
    const clipped = plot.querySelector('g[clip-path]');
    const children = clipped ? Array.from(clipped.children) : [];
    const barIndexes = children.map((el, i) => el.classList.contains('analysis-bar') ? i : -1).filter((i) => i >= 0);
    const lineIndexes = children.map((el, i) => (el.tagName === 'path' && el.classList.contains('analysis-line')) ? i : -1).filter((i) => i >= 0);
    const bars = Array.from(plot.querySelectorAll('.analysis-bar'));
    const maxH = bars.length ? Math.max(...bars.map((el) => Number(el.getAttribute('height') || 0))) : 0;
    const svgH = document.getElementById('analysisChart').getBoundingClientRect().height;
    return {
      secondaryClass: bars.every((el) => el.classList.contains('is-secondary')),
      barsBeforeLines: barIndexes.length > 0 && lineIndexes.length > 0 && Math.max(...barIndexes) < Math.min(...lineIndexes),
      maxH,
      svgH,
      units: Array.from(plot.querySelectorAll('.axis-unit')).map((el) => el.textContent),
    };
  });
  if (!dualScaleCheck.secondaryClass || !dualScaleCheck.barsBeforeLines || dualScaleCheck.maxH >= dualScaleCheck.svgH * 0.5 || !dualScaleCheck.units.includes('Tokens')) {
    throw new Error(`dual-scale analysis chart must scale secondary bars below lines: ${JSON.stringify(dualScaleCheck)}`);
  }
  await page.locator('.series-key-button[data-series="totalTokens"]').click();

  // Switch to mobile viewport (390x844): verify mobile analysis legend fade, full-scale bars, tooltip,
  // and mobile pricing card sheet with 74px top safe offset and single-row buttons.
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(150);

  const mobileAnalysis = await page.evaluate(() => {
    const fade = document.querySelector('.series-key-fade');
    const svg = document.getElementById('analysisChart');
    const bars = Array.from(svg.querySelectorAll('.analysis-bar')).map((el) => Number(el.getAttribute('height') || 0));
    const svgRect = svg.getBoundingClientRect();
    return {
      fadeDisplay: fade ? getComputedStyle(fade).display : 'missing',
      barCount: bars.length,
      maxBar: bars.length ? Math.max(...bars) : 0,
      svgHeight: svgRect.height,
    };
  });
  if (mobileAnalysis.fadeDisplay === 'none' || mobileAnalysis.fadeDisplay === 'missing') {
    throw new Error(`mobile legend fade indicator must be rendered, got ${JSON.stringify(mobileAnalysis)}`);
  }
  if (mobileAnalysis.barCount < 1 || mobileAnalysis.maxBar <= mobileAnalysis.svgHeight * 0.5) {
    throw new Error(`mobile analysis bars must use 1:1 full scale, got ${JSON.stringify(mobileAnalysis)}`);
  }

  // Toggling Display Settings off/on (with a resize while hidden) must restore mobile legend fade.
  await page.locator('#displaySettingsButton').click();
  await page.locator('#displaySettingsMenu').waitFor({ state: 'visible' });
  await page.locator('#displaySectionAnalysis').uncheck();
  await page.evaluate(() => window.dispatchEvent(new Event('resize')));
  await page.locator('#displaySectionAnalysis').check();
  await page.locator('#displaySettingsButton').click();
  await page.waitForFunction(() => document.getElementById('displaySettingsMenu').hidden);
  await page.waitForTimeout(150);
  const fadeAfterToggle = await page.evaluate(() => {
    const wrap = document.querySelector('.series-key-wrap');
    const fade = wrap.querySelector('.series-key-fade');
    return {
      isScrolledEnd: wrap.classList.contains('is-scrolled-end'),
      opacity: getComputedStyle(fade).opacity,
    };
  });
  if (fadeAfterToggle.isScrolledEnd || fadeAfterToggle.opacity !== '1') {
    throw new Error(`mobile legend fade must recover after re-enabling Tokens analysis: ${JSON.stringify(fadeAfterToggle)}`);
  }

  await page.locator('#analysisHitLayer .bar-hit').first().focus();
  await page.waitForFunction(() => document.getElementById('tooltip').classList.contains('visible'));

  await page.locator('#pricingButton').click();
  await page.waitForFunction(() => document.getElementById('pricingDialog') && document.getElementById('pricingDialog').open);
  const mobilePricing = await page.evaluate(() => {
    const dlg = document.getElementById('pricingDialog').getBoundingClientRect();
    const tableScroll = document.querySelector('#pricingDialog .price-table-scroll');
    const syncBtn = document.getElementById('syncPrices').getBoundingClientRect();
    const addBtn = document.getElementById('addPriceEntry').getBoundingClientRect();
    const cards = Array.from(document.querySelectorAll('#priceCardList .price-card'));
    return {
      dialogTop: dlg.top,
      tableVisible: tableScroll ? getComputedStyle(tableScroll).display !== 'none' : true,
      cardCount: cards.length,
      cardActionsPresent: cards.every((c) => c.querySelector('.price-edit-link') && c.querySelector('.restore-price') && c.querySelector('.remove-model-price')),
      buttonsSameRow: Math.abs(syncBtn.top - addBtn.top) <= 2,
      addRight: addBtn.right,
    };
  });
  if (mobilePricing.dialogTop < 72 || mobilePricing.tableVisible || mobilePricing.cardCount < 1 || !mobilePricing.cardActionsPresent) {
    throw new Error(`mobile pricing card sheet regression: ${JSON.stringify(mobilePricing)}`);
  }
  if (!mobilePricing.buttonsSameRow || mobilePricing.addRight > 390) {
    throw new Error(`mobile pricing toolbar actions wrapped or overflowed: ${JSON.stringify(mobilePricing)}`);
  }

  // Mobile tiered pricing fields in #priceEditDialog must scroll horizontally without expanding the dialog.
  await page.locator('#addPriceEntry').click();
  await page.waitForFunction(() => document.getElementById('priceEditDialog') && document.getElementById('priceEditDialog').open);
  await page.locator('#priceEditTiersEnabled').check();
  await page.locator('#priceEditAddTier').click();
  const mobileTierScroll = await page.evaluate(() => {
    const dlg = document.getElementById('priceEditDialog');
    const fields = document.getElementById('priceEditTierFields');
    return {
      dlgScrollW: dlg.scrollWidth,
      dlgClientW: dlg.clientWidth,
      overflowX: getComputedStyle(fields).overflowX,
      fieldsScrollW: fields.scrollWidth,
      fieldsClientW: fields.clientWidth,
    };
  });
  if (mobileTierScroll.dlgScrollW > mobileTierScroll.dlgClientW + 1 || mobileTierScroll.overflowX !== 'auto' || mobileTierScroll.fieldsScrollW <= mobileTierScroll.fieldsClientW) {
    throw new Error(`mobile tiered pricing fields must scroll horizontally inside #priceEditDialog: ${JSON.stringify(mobileTierScroll)}`);
  }
  await page.locator('#priceEditCancel').click();
  await page.waitForFunction(() => !document.getElementById('priceEditDialog').open);

  if (pageErrors.length) {
    throw new Error(`page errors: ${pageErrors.join(' | ')}`);
  }
} finally {
  if (holdNextGetPricesGate) {
    holdNextGetPricesGate.resolve();
    holdNextGetPricesGate = null;
  }
  await browser.close();
  host.close();
}
