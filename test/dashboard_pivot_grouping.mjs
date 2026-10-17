// Pivot-grouping browser regression: the model-details table must split rows
// by the visible dimension columns, merge them back when a column is hidden,
// and keep request totals conserved across every toggle.
import { readFile } from 'node:fs/promises';
import { chromium } from 'playwright-core';
import { startDashboardServer, defaultFixtures } from './helpers.mjs';

const [htmlPath, chromePath] = process.argv.slice(2);
if (!htmlPath || !chromePath) {
  throw new Error('usage: node test/dashboard_pivot_grouping.mjs <dashboard-html-path> <google-chrome-path>');
}

const dashboardHTML = await readFile(htmlPath);
const resourceBase = '/v0/resource/plugins/pivot-browser-test';
const managementBase = '/v0/management/plugins/pivot-browser-test';

const records = [
  { model: 'alpha', provider: 'openai', source: 'cli', alias: '', response_model: 'alpha-002', requests: 2, failed_requests: 0, total_tokens: 20, average_latency_ns: 0, average_ttft_ns: 0 },
  { model: 'alpha', provider: 'openai', source: 'cli', alias: '', response_model: 'alpha', requests: 3, failed_requests: 0, total_tokens: 30, average_latency_ns: 0, average_ttft_ns: 0 },
  { model: 'beta', provider: 'anthropic', source: 'cli', alias: '', response_model: '', requests: 1, failed_requests: 0, total_tokens: 10, average_latency_ns: 0, average_ttft_ns: 0 },
];

// Mirrors the store's groupIdentity contract: model is always part of the
// identity, listed fields participate, and an empty response_model normalizes
// to the requested model so unreported records merge with same-name rows.
const receivedGroupBy = [];

function groupRecords(groupBy) {
  const fields = new Set((groupBy || '').split(',').map((name) => name.trim()).filter(Boolean));
  const map = new Map();
  for (const record of records) {
    // Contract parity with the store: response_model participates as
    // reported (unreported records keep an empty value and surface as their
    // own "—"-marked row); when it does not participate, rows carry an empty
    // response_model.
    const effective = fields.has('response_model') ? record.response_model : '';
    const identity = JSON.stringify([
      record.model,
      fields.has('alias') ? (record.alias || '') : '',
      effective,
      fields.has('provider') ? record.provider : '',
      fields.has('source') ? record.source : '',
    ]);
    const group = map.get(identity) || { ...record, response_model: effective, requests: 0, total_tokens: 0 };
    group.requests += record.requests;
    group.total_tokens += record.total_tokens;
    map.set(identity, group);
  }
  return [...map.values()].sort((left, right) => right.total_tokens - left.total_tokens || left.model.localeCompare(right.model));
}

const host = startDashboardServer({
  dashboardHTML,
  pluginId: 'pivot-browser-test',
  fixtures: defaultFixtures({ model: 'alpha' }),
  rawHandler: (request, response, url) => {
    if (url.pathname !== `${managementBase}/stats/groups`) {
      return false;
    }
    const groupBy = url.searchParams.get('group_by') ?? '';
    receivedGroupBy.push(groupBy);
    const items = groupRecords(groupBy);
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end(JSON.stringify({ items, total: items.length }));
    return true;
  },
});
const dashboardURL = await host.ready;
const browser = await chromium.launch({ executablePath: chromePath, headless: true });

try {
  const context = await browser.newContext({ locale: 'en-US', timezoneId: 'UTC' });
  await context.addInitScript(() => {
    sessionStorage.setItem('tokens-statistic-key', 'pivot-browser-test-key');
  });
  const page = await context.newPage();
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(String(error.stack || error).slice(0, 500)));
  page.on('console', (msg) => { if (msg.type() === 'error') pageErrors.push('console@' + (msg.location()?.url || '') + ':' + (msg.location()?.lineNumber ?? 0) + ' ' + msg.text().slice(0, 300)); });

  await page.goto(dashboardURL, { waitUntil: 'domcontentloaded' });
  await page.locator('#groups tr').first().waitFor({ state: 'visible', timeout: 10000 });

  async function dimensionSummary() {
    return page.locator('#groups tr').evaluateAll((rows) => {
      let requests = 0;
      const models = [];
      const upstream = [];
      rows.forEach((row) => {
        const requestsCell = row.querySelector('td[data-column="requests"]');
        if (requestsCell) requests += Number(requestsCell.textContent.replace(/[^0-9-]/g, '')) || 0;
        const modelCell = row.querySelector('td[data-column="model"]');
        if (modelCell) models.push(modelCell.textContent.trim());
        const upstreamCell = row.querySelector('td[data-column="response_model"]');
        if (upstreamCell) upstream.push(upstreamCell.textContent.trim());
      });
      return { requests, rowCount: rows.length, models, upstream };
    });
  }

  // Default view: every dimension column visible, so rows split by the
  // upstream model with unreported records normalized to their model.
  let summary = await dimensionSummary();
  if (summary.rowCount !== 3 || summary.requests !== 6) {
    throw new Error(`default view = ${JSON.stringify(summary)}, want 3 rows conserving 6 requests`);
  }
  if (JSON.stringify(summary.models) !== JSON.stringify(['alpha', 'alpha', 'beta']) || JSON.stringify(summary.upstream) !== JSON.stringify(['alpha', 'alpha-002', '—'])) {
    throw new Error(`default rows = ${JSON.stringify(summary)}, want upstream [alpha, alpha-002, em-dash]`);
  }
  if (!receivedGroupBy[0].startsWith('model,') || !receivedGroupBy[0].includes('response_model')) {
    throw new Error(`first group_by = ${receivedGroupBy[0]}`);
  }
  const subtitle = await page.locator('#tableSubtitle').textContent();
  if (!subtitle.includes('3 rows')) {
    throw new Error(`subtitle = ${JSON.stringify(subtitle)}, want the pivot status with 3 rows`);
  }

  // Hiding the upstream-model column must merge version rows and keep the
  // request total conserved.
  await page.locator('#dimensionColumnsButton').click();
  await page.locator('#dimensionColumnsMenu').waitFor({ state: 'visible' });
  await page.locator('#dimensionColumnsMenu').getByLabel('Upstream model').uncheck();

  await page.waitForFunction(() => document.querySelectorAll('#groups tr').length === 2, undefined, { timeout: 5000 });
  summary = await dimensionSummary();
  if (summary.requests !== 6 || JSON.stringify(summary.models) !== JSON.stringify(['alpha', 'beta'])) {
    throw new Error(`merged view = ${JSON.stringify(summary)}, want 2 rows conserving 6 requests`);
  }
  if (receivedGroupBy[receivedGroupBy.length - 1].includes('response_model')) {
    throw new Error(`hidden column must drop response_model from group_by: ${receivedGroupBy[receivedGroupBy.length - 1]}`);
  }

  // Showing it again must restore the split without losing anything.
  await page.locator('#dimensionColumnsMenu').getByLabel('Upstream model').check();
  await page.waitForFunction(() => document.querySelectorAll('#groups tr').length === 3, undefined, { timeout: 5000 });
  summary = await dimensionSummary();
  if (summary.requests !== 6 || summary.rowCount !== 3) {
    throw new Error(`restored view = ${JSON.stringify(summary)}`);
  }

  // Measure columns are display-only: hiding one changes neither rows nor sums.
  await page.locator('#dimensionColumnsMenu').getByLabel('Failed').uncheck();
  await page.waitForFunction(() => !document.querySelector('#groups td[data-column="failed_requests"]'), undefined, { timeout: 5000 });
  summary = await dimensionSummary();
  if (summary.rowCount !== 3 || summary.requests !== 6) {
    throw new Error(`measure-only toggle = ${JSON.stringify(summary)}`);
  }
  await page.locator('#dimensionColumnsMenu').getByLabel('Failed').check();

  if (pageErrors.length) {
    throw new Error(`page errors: ${pageErrors.join(' | ')}`);
  }
} finally {
  await browser.close();
  // server.close can wait on lingering keep-alive sockets; bound it so a real
  // assertion error from the try block still surfaces.
  await Promise.race([
    new Promise((resolve, reject) => host.close((error) => error ? reject(error) : resolve())),
    new Promise((resolve) => setTimeout(resolve, 2000)),
  ]);
}
