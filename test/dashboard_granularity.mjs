import { chromium } from 'playwright-core';
import { readDashboardFile, startDashboardServer, authenticate, defaultFixtures } from './helpers.mjs';

const [htmlPath, chromePath] = process.argv.slice(2);
if (!htmlPath || !chromePath) {
  throw new Error('usage: node test/dashboard_granularity.mjs <dashboard-html-path> <google-chrome-path>');
}

const dashboardHTML = await readDashboardFile(htmlPath);
const GOOD_KEY = 'granularity-browser-key';
const MODEL = 'browser-test-model';

function computeBucketSeconds(startIso, endIso, granularity) {
  const startMs = Date.parse(startIso);
  const endMs = Date.parse(endIso);
  const spanMs = Math.max(1000, endMs - startMs);
  const spanSec = Math.floor(spanMs / 1000);
  const widths = [60, 300, 900, 3600, 21600, 86400, 604800];
  let defaultWidth = 604800;
  for (const w of widths) {
    if (Math.floor(spanSec / w) <= 360) {
      defaultWidth = w;
      break;
    }
  }
  const caps = {
    minute: { desired: 60, maxMs: 25 * 3600 * 1000 },
    hour: { desired: 3600, maxMs: (31 * 24 + 1) * 3600 * 1000 },
    day: { desired: 86400, maxMs: 4 * 366 * 24 * 3600 * 1000 },
  };
  const rule = caps[granularity];
  if (!rule || rule.desired >= defaultWidth) {
    return defaultWidth;
  }
  return spanMs <= rule.maxMs ? rule.desired : defaultWidth;
}

const initialRequests = [];
const trendRequests = [];
let holdInitialGate = null;
let holdTrendGate = null;
let failNextTrend = false;

function releaseHeldInitial() {
  if (holdInitialGate) {
    holdInitialGate.resolve();
    holdInitialGate = null;
  }
}

function releaseHeldTrend() {
  if (holdTrendGate) {
    holdTrendGate.resolve();
    holdTrendGate = null;
  }
}

const baseFixtures = defaultFixtures({ model: MODEL });
const host = startDashboardServer({
  dashboardHTML,
  pluginId: 'granularity-browser-test',
  fixtures: baseFixtures,
  rawHandler: (request, response, url) => {
    const authHeader = request.headers.authorization || '';
    const key = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : '';
    if (url.pathname.startsWith('/v0/management/plugins/granularity-browser-test/') && key !== GOOD_KEY) {
      return false;
    }
    if (url.pathname === '/v0/management/plugins/granularity-browser-test/stats/initial') {
      const entry = {
        start: url.searchParams.get('start') || '',
        end: url.searchParams.get('end') || '',
        granularity: url.searchParams.get('granularity') || '',
      };
      const bucketSeconds = computeBucketSeconds(entry.start, entry.end, entry.granularity);
      entry.bucketSeconds = bucketSeconds;
      initialRequests.push(entry);

      const respond = () => {
        response.writeHead(200, { 'content-type': 'application/json' });
        response.end(JSON.stringify({
          ...baseFixtures.statsInitial,
          series: [
            { hour: '2026-08-23T11:55:00Z', requests: 1, input_tokens: 5, output_tokens: 10, total_tokens: 15 },
            { hour: '2026-08-23T11:56:00Z', requests: 1, input_tokens: 5, output_tokens: 10, total_tokens: 15 },
          ],
          bucket_seconds: bucketSeconds,
        }));
      };
      if (holdInitialGate) {
        holdInitialGate.promise.then(respond);
      } else {
        respond();
      }
      return true;
    }
    if (url.pathname === '/v0/management/plugins/granularity-browser-test/stats/trends') {
      const entry = {
        start: url.searchParams.get('start') || '',
        end: url.searchParams.get('end') || '',
        granularity: url.searchParams.get('granularity') || '',
      };
      const bucketSeconds = computeBucketSeconds(entry.start, entry.end, entry.granularity);
      entry.bucketSeconds = bucketSeconds;
      trendRequests.push(entry);
      const respondTrend = () => {
        if (failNextTrend) {
          failNextTrend = false;
          response.writeHead(500, { 'content-type': 'application/json' });
          response.end(JSON.stringify({ error: 'trend failure' }));
          return;
        }
        response.writeHead(200, { 'content-type': 'application/json' });
        response.end(JSON.stringify({
          model_series: [
            { hour: '2026-08-23T11:55:00Z', model: MODEL, requests: 1, input_tokens: 5, output_tokens: 10, total_tokens: 15 },
            { hour: '2026-08-23T11:56:00Z', model: MODEL, requests: 1, input_tokens: 5, output_tokens: 10, total_tokens: 15 },
          ],
          bucket_seconds: bucketSeconds,
        }));
      };
      if (holdTrendGate) {
        holdTrendGate.promise.then(respondTrend);
      } else {
        respondTrend();
      }
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

  await authenticate(page, { managementBase: host.managementBase, key: GOOD_KEY, waitFor: ['stats/initial', 'stats/trends', 'requests', 'stats/groups'] });

  const granButton = page.locator('.granularity-select .enhanced-select-button');
  const listboxId = await granButton.getAttribute('aria-controls');
  if (!listboxId) {
    throw new Error('granularity enhanced select missing aria-controls');
  }
  const granMenu = page.locator(`#${listboxId}`);
  const granValue = () => page.evaluate(() => document.getElementById('granularity').value);

  // 1. On Today (24h), starts at hour, user selects minute -> fetches 60s trends and survives 15s background refresh.
  if (await granValue() !== 'hour') {
    throw new Error(`expected initial granularity on Today to be "hour", got "${await granValue()}"`);
  }
  await granButton.click();
  await granMenu.waitFor({ state: 'visible' });
  await Promise.all([
    page.waitForResponse((res) =>
      new URL(res.url()).pathname === `${host.managementBase}/stats/trends` &&
      new URL(res.url()).searchParams.get('granularity') === 'minute',
    ),
    granMenu.locator('[role="option"][data-index="0"]').click(),
  ]);

  if (await granValue() !== 'minute') {
    throw new Error(`selecting minute on Today must set #granularity to "minute", got "${await granValue()}"`);
  }

  const refreshInitialPromise = page.waitForResponse((res) =>
    new URL(res.url()).pathname === `${host.managementBase}/stats/initial` &&
    new URL(res.url()).searchParams.get('granularity') === 'minute',
  );
  const refreshTrendPromise = page.waitForResponse((res) =>
    new URL(res.url()).pathname === `${host.managementBase}/stats/trends` &&
    new URL(res.url()).searchParams.get('granularity') === 'minute',
  );
  await page.clock.fastForward(15500);
  await Promise.all([refreshInitialPromise, refreshTrendPromise]);

  if (await granValue() !== 'minute') {
    throw new Error(`minute granularity snapped back after 15s refresh: got "${await granValue()}"`);
  }
  const lastTrend = trendRequests[trendRequests.length - 1];
  if (!lastTrend || lastTrend.bucketSeconds !== 60) {
    throw new Error(`expected 60s bucket after minute refresh, got ${JSON.stringify(lastTrend)}`);
  }

  // 2. Switch to Last 5 hours -> defaults to minute and stays minute after 15s refresh.
  await page.locator('#rangeButton').click();
  await page.locator('[data-range-preset="last_5_hours"]').click();
  const fiveHourInitialPromise = page.waitForResponse((res) =>
    new URL(res.url()).pathname === `${host.managementBase}/stats/initial` &&
    new URL(res.url()).searchParams.get('granularity') === 'minute',
  );
  const fiveHourTrendPromise = page.waitForResponse((res) =>
    new URL(res.url()).pathname === `${host.managementBase}/stats/trends` &&
    new URL(res.url()).searchParams.get('granularity') === 'minute',
  );
  await page.locator('#confirmDateRange').click();
  await Promise.all([fiveHourInitialPromise, fiveHourTrendPromise]);

  if (await granValue() !== 'minute') {
    throw new Error(`Last 5 hours must default to "minute", got "${await granValue()}"`);
  }
  const fiveHourTickInitial = page.waitForResponse((res) =>
    new URL(res.url()).pathname === `${host.managementBase}/stats/initial` &&
    new URL(res.url()).searchParams.get('granularity') === 'minute',
  );
  await page.clock.fastForward(15500);
  await fiveHourTickInitial;
  if (await granValue() !== 'minute') {
    throw new Error(`Last 5 hours snapped back after 15s refresh: got "${await granValue()}"`);
  }

  // 3. Switch to Last 7 days -> minute disabled (native + enhanced), Home jumps to hour.
  await page.locator('#rangeButton').click();
  await page.locator('[data-range-preset="last_7_days"]').click();
  const sevenDayInitialPromise = page.waitForResponse((res) =>
    new URL(res.url()).pathname === `${host.managementBase}/stats/initial` &&
    new URL(res.url()).searchParams.get('granularity') === 'hour',
  );
  await page.locator('#confirmDateRange').click();
  await sevenDayInitialPromise;

  if (await granValue() !== 'hour') {
    throw new Error(`Last 7 days must default to "hour", got "${await granValue()}"`);
  }
  const sevenDayDisabledState = await page.evaluate((id) => {
    const nativeMinute = document.querySelector('#granularity option[value="minute"]');
    const enhancedMinute = document.querySelector(`#${id} [role="option"][data-index="0"]`);
    return {
      nativeDisabled: Boolean(nativeMinute && nativeMinute.disabled),
      enhancedDisabled: Boolean(enhancedMinute && enhancedMinute.disabled),
    };
  }, listboxId);
  if (!sevenDayDisabledState.nativeDisabled || !sevenDayDisabledState.enhancedDisabled) {
    throw new Error(`expected minute option to be disabled on Last 7 days: ${JSON.stringify(sevenDayDisabledState)}`);
  }
  await granButton.focus();
  await page.keyboard.press('Home');
  const activeDescendant = await granButton.getAttribute('aria-activedescendant');
  const expectedHourOptionId = await granMenu.locator('[role="option"][data-index="1"]').getAttribute('id');
  if (activeDescendant !== expectedHourOptionId) {
    throw new Error(`Home key on Last 7 days must skip disabled minute and highlight hour (${expectedHourOptionId}), got ${activeDescendant}`);
  }
  await page.keyboard.press('Escape');

  // 4. Switch to Last 30 days -> defaults to day, minute disabled, hour enabled; selecting hour gets 3600s and survives refresh.
  await page.locator('#rangeButton').click();
  await page.locator('[data-range-preset="last_30_days"]').click();
  const thirtyDayInitialPromise = page.waitForResponse((res) =>
    new URL(res.url()).pathname === `${host.managementBase}/stats/initial` &&
    new URL(res.url()).searchParams.get('granularity') === 'day',
  );
  await page.locator('#confirmDateRange').click();
  await thirtyDayInitialPromise;

  if (await granValue() !== 'day') {
    throw new Error(`Last 30 days must default to "day", got "${await granValue()}"`);
  }
  await granButton.click();
  await granMenu.waitFor({ state: 'visible' });
  await Promise.all([
    page.waitForResponse((res) =>
      new URL(res.url()).pathname === `${host.managementBase}/stats/trends` &&
      new URL(res.url()).searchParams.get('granularity') === 'hour',
    ),
    granMenu.locator('[role="option"][data-index="1"]').click(),
  ]);

  if (await granValue() !== 'hour') {
    throw new Error(`selecting hour on Last 30 days must set "hour", got "${await granValue()}"`);
  }
  const thirtyDayHourTick = page.waitForResponse((res) =>
    new URL(res.url()).pathname === `${host.managementBase}/stats/initial` &&
    new URL(res.url()).searchParams.get('granularity') === 'hour',
  );
  await page.clock.fastForward(15500);
  await thirtyDayHourTick;
  if (await granValue() !== 'hour') {
    throw new Error(`hour on Last 30 days snapped back after 15s refresh: got "${await granValue()}"`);
  }
  const lastTrend30d = trendRequests[trendRequests.length - 1];
  if (!lastTrend30d || lastTrend30d.bucketSeconds !== 3600) {
    throw new Error(`expected 3600s bucket for hour on Last 30 days, got ${JSON.stringify(lastTrend30d)}`);
  }

  // 5. In-flight refresh race: on Today (hour), hold /stats/initial?granularity=hour while user switches to minute, then release.
  await page.locator('#rangeButton').click();
  await page.locator('#resetDateRange').click();
  const todayResetInitial = page.waitForResponse((res) =>
    new URL(res.url()).pathname === `${host.managementBase}/stats/initial` &&
    new URL(res.url()).searchParams.get('granularity') === 'hour',
  );
  const todayResetTrend = page.waitForResponse((res) =>
    new URL(res.url()).pathname === `${host.managementBase}/stats/trends` &&
    new URL(res.url()).searchParams.get('granularity') === 'hour',
  );
  await page.locator('#confirmDateRange').click();
  await Promise.all([todayResetInitial, todayResetTrend]);
  if (await granValue() !== 'hour') {
    throw new Error(`reset to Today must select "hour", got "${await granValue()}"`);
  }

  let resolveHold;
  const holdPromise = new Promise((resolve) => { resolveHold = resolve; });
  holdInitialGate = { promise: holdPromise, resolve: resolveHold };
  const initialCountBeforeRace = initialRequests.length;
  await page.locator('#refreshButton').click();
  // Poll briefly until the held /stats/initial request reaches the mock server.
  while (initialRequests.length === initialCountBeforeRace) {
    await new Promise((r) => setTimeout(r, 15));
  }

  // Switch to minute while /stats/initial (granularity=hour, bucket_seconds=300) is still in flight.
  await granButton.click();
  await granMenu.waitFor({ state: 'visible' });
  await granMenu.locator('[role="option"][data-index="0"]').click();
  if (await granValue() !== 'minute') {
    throw new Error(`switching to minute mid-flight failed, got "${await granValue()}"`);
  }

  const raceTrendPromise = page.waitForResponse((res) =>
    new URL(res.url()).pathname === `${host.managementBase}/stats/trends` &&
    new URL(res.url()).searchParams.get('granularity') === 'minute',
  );
  releaseHeldInitial();
  await raceTrendPromise;

  if (await granValue() !== 'minute') {
    throw new Error(`minute selection must not snap back when in-flight hour initial response resolves, got "${await granValue()}"`);
  }
  const finalTrend = trendRequests[trendRequests.length - 1];
  if (!finalTrend || finalTrend.bucketSeconds !== 60) {
    throw new Error(`expected 60s bucket after race resolved, got ${JSON.stringify(finalTrend)}`);
  }

  // 6. Coarser switch (hour -> day) while /stats/trends is still in flight must NOT drop model_series.
  let resolveTrendHold;
  const trendHoldPromise = new Promise((resolve) => { resolveTrendHold = resolve; });
  holdTrendGate = { promise: trendHoldPromise, resolve: resolveTrendHold };
  const trendCountBeforeCoarseRace = trendRequests.length;
  await page.locator('#rangeButton').click();
  await page.locator('#resetDateRange').click();
  const coarseRaceInitial = page.waitForResponse((res) =>
    new URL(res.url()).pathname === `${host.managementBase}/stats/initial`,
  );
  await page.locator('#confirmDateRange').click();
  await coarseRaceInitial;
  while (trendRequests.length === trendCountBeforeCoarseRace) {
    await new Promise((r) => setTimeout(r, 15));
  }
  await granButton.click();
  await granMenu.waitFor({ state: 'visible' });
  await granMenu.locator('[role="option"][data-index="2"]').click();
  if (await granValue() !== 'day') {
    throw new Error(`switching to day mid-flight failed, got "${await granValue()}"`);
  }
  releaseHeldTrend();
  await page.waitForTimeout(80);
  await page.locator('.dimension-table .model-cell').first().click();
  const drillState = await page.evaluate(() => {
    const plot = document.getElementById('analysisPlot');
    return {
      bars: plot ? plot.querySelectorAll('.analysis-bar').length : 0,
      emptyTitles: plot ? Array.from(plot.querySelectorAll('.chart-empty-title')).map((n) => n.textContent) : [],
    };
  });
  if (drillState.bars < 1 || drillState.emptyTitles.length > 0) {
    throw new Error(`coarser granularity switch discarded model_series before drill-down: ${JSON.stringify(drillState)}`);
  }

  // 7. Switching finer (Today: hour -> minute) when /stats/trends fails must surface error and roll back #granularity to hour.
  await page.locator('#resetModelButton').click();
  await page.locator('#rangeButton').click();
  await page.locator('#resetDateRange').click();
  const todayErrorSetupInitial = page.waitForResponse((res) =>
    new URL(res.url()).pathname === `${host.managementBase}/stats/initial` &&
    new URL(res.url()).searchParams.get('granularity') === 'hour',
  );
  const todayErrorSetupTrend = page.waitForResponse((res) =>
    new URL(res.url()).pathname === `${host.managementBase}/stats/trends` &&
    new URL(res.url()).searchParams.get('granularity') === 'hour',
  );
  await page.locator('#confirmDateRange').click();
  await Promise.all([todayErrorSetupInitial, todayErrorSetupTrend]);
  if (await granValue() !== 'hour') {
    throw new Error(`expected Today setup granularity "hour", got "${await granValue()}"`);
  }
  failNextTrend = true;
  await granButton.click();
  await granMenu.waitFor({ state: 'visible' });
  await Promise.all([
    page.waitForResponse((res) =>
      new URL(res.url()).pathname === `${host.managementBase}/stats/trends` &&
      new URL(res.url()).searchParams.get('granularity') === 'minute' &&
      res.status() === 500,
    ),
    granMenu.locator('[role="option"][data-index="0"]').click(),
  ]);
  await page.waitForFunction(() => document.getElementById('granularity').value === 'hour', undefined, { timeout: 5000 });
  const rollbackState = await page.evaluate(() => ({
    value: document.getElementById('granularity').value,
    buttonText: document.querySelector('.granularity-select .enhanced-select-button')?.textContent?.trim(),
    errorText: document.getElementById('error').textContent.trim(),
  }));
  const hourOptionText = await granMenu.locator('[role="option"][data-index="1"]').textContent();
  if (rollbackState.value !== 'hour' || rollbackState.buttonText !== hourOptionText.trim() || !rollbackState.errorText.includes('trend failure')) {
    throw new Error(`failed /stats/trends must roll back granularity and show error: ${JSON.stringify({ rollbackState, hourOptionText })}`);
  }

  if (pageErrors.length) {
    throw pageErrors[0];
  }
} finally {
  releaseHeldInitial();
  releaseHeldTrend();
  await browser.close();
  await host.close();
}
