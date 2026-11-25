import { chromium } from 'playwright-core';
import { readDashboardFile, startDashboardServer, authenticate, defaultFixtures } from './helpers.mjs';

const [htmlPath, chromePath] = process.argv.slice(2);
if (!htmlPath || !chromePath) {
  throw new Error('usage: node test/dashboard_analysis_fixes.mjs <dashboard-html-path> <google-chrome-path>');
}

const dashboardHTML = await readDashboardFile(htmlPath);
const GOOD_KEY = 'analysis-fixes-key';
const CLAUDE_MODEL = 'claude-write-only';

let savedPreferences = {};
const preferencePosts = [];
const groupsRequests = [];
const trendRequests = [];
let holdTrendGate = null;

function releaseHeldTrend() {
  if (holdTrendGate) {
    holdTrendGate.resolve();
    holdTrendGate = null;
  }
}

const claudePointMinute601 = {
  hour: '2026-08-23T10:01:00Z',
  model: CLAUDE_MODEL,
  requests: 2,
  failed_requests: 0,
  input_tokens: 1000,
  output_tokens: 200,
  cached_tokens: 500,
  cache_read_tokens: 0,
  cache_creation_tokens: 500,
  total_tokens: 1700,
  total_latency_ns: 12345000000,
  latency_samples: 1,
  total_ttft_ns: 500000000,
  ttft_samples: 1,
  cache_rate_numerator: 0,
  cache_rate_denominator: 1500,
};

let currentPoints = [claudePointMinute601];
let includeUntitledModel = false;

const base = defaultFixtures({ model: CLAUDE_MODEL });

const host = startDashboardServer({
  dashboardHTML,
  pluginId: 'analysis-fixes-test',
  fixtures: base,
  rawHandler: (request, response, url) => {
    const authHeader = request.headers.authorization || '';
    const key = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : '';
    if (url.pathname.startsWith('/v0/management/plugins/analysis-fixes-test/') && key !== GOOD_KEY) {
      return false;
    }

    if (url.pathname === '/v0/management/plugins/analysis-fixes-test/preferences') {
      if (request.method === 'POST') {
        let body = '';
        request.setEncoding('utf8');
        request.on('data', (c) => { body += c; });
        request.on('end', () => {
          const parsed = JSON.parse(body || '{}');
          preferencePosts.push(parsed);
          savedPreferences = parsed;
          response.writeHead(200, { 'content-type': 'application/json' });
          response.end(JSON.stringify(savedPreferences));
        });
        return true;
      }
      response.writeHead(200, { 'content-type': 'application/json' });
      response.end(JSON.stringify(savedPreferences));
      return true;
    }

    if (url.pathname === '/v0/management/plugins/analysis-fixes-test/stats/initial') {
      const scopeModel = url.searchParams.get('scope_model') || '';
      const models = [{
        model: CLAUDE_MODEL,
        provider: 'claude',
        executor_type: 'anthropic',
        requests: currentPoints.reduce((s, p) => s + p.requests, 0),
        failed_requests: 0,
        input_tokens: currentPoints.reduce((s, p) => s + p.input_tokens, 0),
        output_tokens: currentPoints.reduce((s, p) => s + p.output_tokens, 0),
        cached_tokens: currentPoints.reduce((s, p) => s + p.cached_tokens, 0),
        cache_read_tokens: 0,
        cache_creation_tokens: currentPoints.reduce((s, p) => s + p.cache_creation_tokens, 0),
        total_tokens: currentPoints.reduce((s, p) => s + p.total_tokens, 0),
        average_latency_ns: 12345000000,
        average_ttft_ns: 500000000,
        cache_rate_numerator: 0,
        cache_rate_denominator: 1500 * currentPoints.length,
      }];
      if (includeUntitledModel && !scopeModel) {
        models.push({
          model: '',
          provider: 'openai',
          requests: 3,
          input_tokens: 500,
          output_tokens: 100,
          total_tokens: 600,
          cache_rate_numerator: 0,
          cache_rate_denominator: 500,
        });
      }
      const gran = url.searchParams.get('granularity') || '';
      const bucketSeconds = gran === 'minute' ? 60 : 3600;
      response.writeHead(200, { 'content-type': 'application/json' });
      response.end(JSON.stringify({
        generated_at: '2026-08-23T12:00:00.000Z',
        last_used: '2026-08-23T14:02:00.000Z',
        bucket_seconds: bucketSeconds,
        models,
        all_models: [CLAUDE_MODEL],
        sources: ['cli'],
        series: currentPoints.map((p) => ({ ...p })),
      }));
      return true;
    }

    if (url.pathname === '/v0/management/plugins/analysis-fixes-test/stats/trends') {
      const gran = url.searchParams.get('granularity') || '';
      const bucketSeconds = gran === 'minute' ? 60 : 3600;
      trendRequests.push({
        start: url.searchParams.get('start') || '',
        end: url.searchParams.get('end') || '',
        granularity: gran,
      });
      const sendTrends = () => {
        response.writeHead(200, { 'content-type': 'application/json' });
        response.end(JSON.stringify({
          bucket_seconds: bucketSeconds,
          model_series: currentPoints.map((p) => ({ ...p })),
        }));
      };
      if (holdTrendGate) {
        holdTrendGate.promise.then(sendTrends);
      } else {
        sendTrends();
      }
      return true;
    }

    if (url.pathname === '/v0/management/plugins/analysis-fixes-test/stats/groups') {
      const scopeModel = url.searchParams.get('scope_model') || '';
      const localModel = url.searchParams.get('model') || '';
      groupsRequests.push({ scopeModel, localModel });
      let items = [{
        model: CLAUDE_MODEL,
        provider: 'claude',
        source: 'cli',
        executor_type: 'anthropic',
        auth_type: 'api_key',
        service_tier: '',
        reasoning_effort: '',
        requests: currentPoints.reduce((s, p) => s + p.requests, 0),
        failed_requests: 0,
        input_tokens: 1000,
        output_tokens: 200,
        reasoning_tokens: 0,
        cache_read_tokens: 0,
        cache_creation_tokens: 500,
        total_tokens: 1700,
        average_ttft_ns: 500000000,
        average_latency_ns: 12345000000,
        api_key_ref: 'g1:ab',
        api_key: '',
        api_key_status: 'available',
      }];
      if (includeUntitledModel) {
        items.push({
          model: '',
          provider: 'openai',
          source: 'cli',
          executor_type: 'openai',
          auth_type: 'api_key',
          service_tier: '',
          reasoning_effort: '',
          requests: 3,
          failed_requests: 0,
          input_tokens: 500,
          output_tokens: 100,
          reasoning_tokens: 0,
          cache_read_tokens: 0,
          cache_creation_tokens: 0,
          total_tokens: 600,
          average_ttft_ns: 200000000,
          average_latency_ns: 800000000,
          api_key_ref: 'g1:ab',
          api_key: '',
          api_key_status: 'available',
        });
      }
      const activeModel = scopeModel || localModel;
      if (activeModel) {
        items = items.filter((it) => (activeModel === '__untitled__' ? it.model === '' : it.model === activeModel));
      }
      response.writeHead(200, { 'content-type': 'application/json' });
      response.end(JSON.stringify({ items, total: items.length }));
      return true;
    }

    if (url.pathname === '/v0/management/plugins/analysis-fixes-test/costs') {
      const models = [{
        model: CLAUDE_MODEL,
        provider: 'claude',
        requests: 2,
        priced_requests: 2,
        unpriced_requests: 0,
        input_usd: 0.003,
        output_usd: 0.003,
        cache_read_usd: 0,
        cache_creation_usd: 0.001875,
        total_usd: 0.007875,
      }];
      if (includeUntitledModel) {
        models.push({
          model: '',
          provider: 'openai',
          requests: 3,
          priced_requests: 3,
          unpriced_requests: 0,
          input_usd: 0.001,
          output_usd: 0.001,
          cache_read_usd: 0,
          cache_creation_usd: 0,
          total_usd: 0.002,
        });
      }
      const summary = models.reduce((acc, m) => ({
        requests: acc.requests + m.requests,
        priced_requests: acc.priced_requests + m.priced_requests,
        unpriced_requests: acc.unpriced_requests + m.unpriced_requests,
        total_usd: acc.total_usd + m.total_usd,
      }), { requests: 0, priced_requests: 0, unpriced_requests: 0, total_usd: 0 });
      response.writeHead(200, { 'content-type': 'application/json' });
      response.end(JSON.stringify({
        summary,
        models,
        price_book_revision: 1,
      }));
      return true;
    }

    if (url.pathname === '/v0/management/plugins/analysis-fixes-test/prices' && request.method === 'GET') {
      response.writeHead(200, { 'content-type': 'application/json' });
      response.end(JSON.stringify({
        revision: 1,
        prices: {
          [CLAUDE_MODEL]: { input: 3, output: 15, cache_read: 0.3, cache_creation: 3.75, source: 'manual' },
          '未标记模型': { input: 2, output: 4, source: 'manual' },
        },
        sync_settings: { provider_priority: [], ignored_suffixes: [], mappings: [] },
      }));
      return true;
    }

    return false;
  },
});

const dashboardURL = await host.ready;
const browser = await chromium.launch({ executablePath: chromePath, headless: true });

try {
  const context = await browser.newContext({ locale: 'zh-CN', timezoneId: 'UTC', viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(String(error)));

  await page.clock.install({ time: new Date('2026-08-23T12:00:00.000Z') });
  await page.goto(dashboardURL, { waitUntil: 'domcontentloaded' });
  await authenticate(page, { managementBase: host.managementBase, key: GOOD_KEY, waitFor: ['stats/initial', 'stats/trends', 'requests', 'stats/groups', 'costs', 'prices'] });

  // 1440-minute dense chart: switch to minute -> single call at index 601 must draw a bar,
  // render a visible single-point symbol on line series (>36 buckets), expose non-empty hit rect,
  // and agree with Metric Card cache rate (0.00% vs 0.0%) for Claude write-only cache.
  const granButton = page.locator('.granularity-select .enhanced-select-button');
  const listboxId = await granButton.getAttribute('aria-controls');
  const granMenu = page.locator(`#${listboxId}`);
  await granButton.click();
  await granMenu.waitFor({ state: 'visible' });
  await Promise.all([
    page.waitForResponse((res) =>
      new URL(res.url()).pathname === `${host.managementBase}/stats/trends` &&
      new URL(res.url()).searchParams.get('granularity') === 'minute',
    ),
    granMenu.locator('[role="option"][data-index="0"]').click(),
  ]);

  // Turn on totalTokens so single-point line renders too.
  await page.locator('.series-key-button[data-series="totalTokens"]').click();
  await page.waitForFunction(() => Boolean(document.querySelector('#analysisHitLayer .bar-hit[data-index="601"]')) && document.querySelectorAll('#analysisPlot circle.analysis-symbol').length >= 1, undefined, { timeout: 5000 }).catch(() => {});

  const cardCacheRate = await page.locator('#cacheRate').textContent();
  if (cardCacheRate !== '0.00%') {
    throw new Error(`expected Metric Card cacheRate "0.00%", got "${cardCacheRate}"`);
  }

  const denseState = await page.evaluate(() => {
    const plot = document.getElementById('analysisPlot');
    const hitLayer = document.getElementById('analysisHitLayer');
    const bars = Array.from(plot.querySelectorAll('.analysis-bar'));
    const symbols = Array.from(plot.querySelectorAll('circle.analysis-symbol'));
    const hits = Array.from(hitLayer.querySelectorAll('.bar-hit'));
    const hit601 = hits.find((h) => Number(h.dataset.index) === 601);
    return {
      barCount: bars.length,
      symbolCount: symbols.length,
      hit601Present: Boolean(hit601),
      hit601Label: hit601 ? hit601.getAttribute('aria-label') : '',
    };
  });
  if (denseState.barCount < 1 || denseState.symbolCount < 1 || !denseState.hit601Present) {
    throw new Error(`dense 1440-minute chart dropped single bucket 601 or single-point dot: ${JSON.stringify(denseState)}`);
  }
  if (!denseState.hit601Label.includes('0.0%') || denseState.hit601Label.includes('无请求')) {
    throw new Error(`hit 601 aria-label mismatch (expected 0.0% cache rate and non-empty call): "${denseState.hit601Label}"`);
  }
  await page.locator('#analysisHitLayer .bar-hit[data-index="601"]').hover();
  const tooltipText = await page.locator('#tooltip').textContent();
  if (!tooltipText.includes('0.0%') || tooltipText.includes('无请求')) {
    throw new Error(`hovering minute 601 tooltip mismatch: "${tooltipText}"`);
  }

  // Add 4h gap + two adjacent buckets at 14:01Z & 14:02Z; enable latency (12.345 s) + percent axes ->
  // verify empty gap creates >=2 separate paths (does NOT bridge straight across 4h gap),
  // and verify right dual-Y axes align cleanly with zero overlap/clipping.
  currentPoints = [
    claudePointMinute601,
    { ...claudePointMinute601, hour: '2026-08-23T14:01:00Z' },
    { ...claudePointMinute601, hour: '2026-08-23T14:02:00Z' },
  ];
  await Promise.all([
    page.waitForResponse((res) => new URL(res.url()).pathname === `${host.managementBase}/stats/trends`),
    page.locator('#refreshButton').click(),
  ]);
  await page.locator('.series-key-button[data-series="latency"]').click();
  await page.waitForFunction(() => document.querySelectorAll('#analysisChart path.analysis-line.count').length === 2 && Boolean(document.querySelector('#analysisChart .analysis-latency-axis')), undefined, { timeout: 5000 }).catch(() => {});

  const geometry = await page.evaluate(() => {
    const svg = document.getElementById('analysisChart');
    const vb = svg.viewBox.baseVal;
    const gridLine = svg.querySelector('.chart-grid');
    const latAxis = svg.querySelector('.analysis-latency-axis');
    const pctAxis = svg.querySelector('.analysis-percent-axis');
    const latLabels = Array.from(svg.querySelectorAll('.analysis-latency-label')).map((n) => {
      const r = n.getBBox();
      return { text: n.textContent, left: r.x, right: r.x + r.width };
    });
    const pctLabels = Array.from(svg.querySelectorAll('.analysis-percent-label')).map((n) => {
      const r = n.getBBox();
      return { text: n.textContent, left: r.x, right: r.x + r.width };
    });
    const tokenPaths = Array.from(svg.querySelectorAll('path.analysis-line.count'));
    return {
      svgWidth: vb.width,
      gridX2: gridLine ? Number(gridLine.getAttribute('x2')) : null,
      latAxisX1: latAxis ? Number(latAxis.getAttribute('x1')) : null,
      pctAxisX1: pctAxis ? Number(pctAxis.getAttribute('x1')) : null,
      maxLatRight: Math.max(...latLabels.map((l) => l.right)),
      maxPctRight: Math.max(...pctLabels.map((l) => l.right)),
      tokenPathCount: tokenPaths.length,
    };
  });
  if (geometry.tokenPathCount !== 2) {
    throw new Error(`expected 2 disconnected segments across 4h gap, got ${geometry.tokenPathCount}: ${JSON.stringify(geometry)}`);
  }
  if (geometry.gridX2 !== geometry.latAxisX1) {
    throw new Error(`latency axis x1 must equal grid x2: ${JSON.stringify(geometry)}`);
  }
  if (!(geometry.maxLatRight < geometry.pctAxisX1 - 2) || !(geometry.maxPctRight <= geometry.svgWidth)) {
    throw new Error(`dual right Y-axes overlapped or clipped: ${JSON.stringify(geometry)}`);
  }
  await page.locator('.series-key-button[data-series="latency"]').click();
  await page.locator('.series-key-button[data-series="totalTokens"]').click();

  // All series hidden -> must render empty message "已隐藏全部序列".
  const pressedKeys = page.locator('.series-key-button[aria-pressed="true"]');
  while (await pressedKeys.count() > 0) {
    await pressedKeys.first().click();
  }
  const hiddenText = await page.locator('#analysisPlot').textContent();
  if (!hiddenText.includes('已隐藏全部序列')) {
    throw new Error(`expected all-hidden prompt in #analysisPlot, got "${hiddenText}"`);
  }
  await page.locator('.series-key-button[data-series="requests"]').click();
  await page.locator('.series-key-button[data-series="successRate"]').click();
  await page.locator('.series-key-button[data-series="cacheRate"]').click();

  // Single-model drill-down while /stats/trends is held on range switch -> must NOT flash "该时间段内暂无调用记录".
  await page.locator('.dimension-table .model-cell').first().click();
  let resolveTrendHold;
  holdTrendGate = { promise: new Promise((resolve) => { resolveTrendHold = resolve; }), resolve: resolveTrendHold };
  const trendCountBeforeHold = trendRequests.length;
  await page.locator('#rangeButton').click();
  await page.locator('[data-range-preset="last_5_hours"]').click();
  const fiveHourInitial = page.waitForResponse((res) => new URL(res.url()).pathname === `${host.managementBase}/stats/initial`);
  await page.locator('#confirmDateRange').click();
  await fiveHourInitial;
  while (trendRequests.length === trendCountBeforeHold) {
    await new Promise((r) => setTimeout(r, 15));
  }
  const midFlightPlotText = await page.locator('#analysisPlot').textContent();
  if (midFlightPlotText.includes('暂无调用记录')) {
    throw new Error(`single-model drill-down flashed noCalls while /stats/trends was loading: "${midFlightPlotText}"`);
  }
  const fiveHourTrend = page.waitForResponse((res) => new URL(res.url()).pathname === `${host.managementBase}/stats/trends`);
  releaseHeldTrend();
  await fiveHourTrend;

  // Rolling preset (last_5_hours) 15s tick advances #status timestamp/range.
  const statusBeforeTick = await page.locator('#status').textContent();
  const tickInitial = page.waitForResponse((res) => new URL(res.url()).pathname === `${host.managementBase}/stats/initial`);
  const tickGroups = page.waitForResponse((res) => new URL(res.url()).pathname === `${host.managementBase}/stats/groups`);
  const tickCosts = page.waitForResponse((res) => new URL(res.url()).pathname === `${host.managementBase}/costs`);
  await page.clock.fastForward(15500);
  await Promise.all([tickInitial, tickGroups, tickCosts]);
  await page.waitForFunction((prev) => document.getElementById('status')?.textContent !== prev, statusBeforeTick, { timeout: 5000 }).catch(() => {});
  const statusAfterTick = await page.locator('#status').textContent();
  if (statusAfterTick === statusBeforeTick) {
    throw new Error(`rolling preset #status froze across 15s tick: "${statusBeforeTick}"`);
  }
  await Promise.all([
    page.waitForResponse((res) => new URL(res.url()).pathname === `${host.managementBase}/stats/groups`),
    page.locator('#resetModelButton').click(),
  ]);

  // Global #modelFilter sends scope_model to /stats/groups and unnamed model shows cost & source.
  includeUntitledModel = true;
  await Promise.all([
    page.waitForResponse((res) => new URL(res.url()).pathname === `${host.managementBase}/stats/groups`),
    page.waitForResponse((res) => new URL(res.url()).pathname === `${host.managementBase}/costs`),
    page.locator('#refreshButton').click(),
  ]);
  await page.waitForFunction(() => {
    const rows = Array.from(document.querySelectorAll('#groups tr'));
    const target = rows.find((r) => r.querySelector('td[data-column="model"]')?.textContent === '未标记模型');
    const costText = target?.querySelector('td[data-column="estimated_cost"]')?.textContent || '';
    return Boolean(target && costText && costText !== '未定价');
  });
  const untitledRow = await page.evaluate(() => {
    const rows = Array.from(document.querySelectorAll('#groups tr'));
    const target = rows.find((r) => r.querySelector('td[data-column="model"]')?.textContent === '未标记模型');
    if (!target) return null;
    return {
      cost: target.querySelector('td[data-column="estimated_cost"]')?.textContent,
      source: target.querySelector('td[data-column="price_source"]')?.textContent,
    };
  });
  if (!untitledRow || untitledRow.cost === '未定价' || untitledRow.source !== 'manual') {
    throw new Error(`unnamed model row cost/source lookup failed: ${JSON.stringify(untitledRow)}`);
  }

  const modelFilterBtn = page.locator('#modelFilter').locator('..').locator('.enhanced-select-button');
  const modelFilterListId = await modelFilterBtn.getAttribute('aria-controls');
  await modelFilterBtn.click();
  const modelFilterMenu = page.locator(`#${modelFilterListId}`);
  const claudeOpt = modelFilterMenu.locator('[role="option"]').filter({ hasText: CLAUDE_MODEL });
  await Promise.all([
    page.waitForResponse((res) => {
      const u = new URL(res.url());
      return u.pathname === `${host.managementBase}/stats/groups` && u.searchParams.get('scope_model') === CLAUDE_MODEL;
    }),
    claudeOpt.click(),
  ]);
  await page.waitForFunction((expected) => {
    const table = document.querySelector('.dimension-table');
    if (table && table.classList.contains('is-loading')) return false;
    const cells = Array.from(document.querySelectorAll('#groups td[data-column="model"]')).map((n) => n.textContent);
    return cells.length === 1 && cells[0] === expected;
  }, CLAUDE_MODEL, { timeout: 5000 }).catch(() => {});
  const groupModelTexts = await page.locator('#groups td[data-column="model"]').allTextContents();
  if (groupModelTexts.length !== 1 || groupModelTexts[0] !== CLAUDE_MODEL) {
    throw new Error(`global #modelFilter failed to narrow Model details: ${JSON.stringify(groupModelTexts)}`);
  }

  // Hiding Service Health in Display Settings persists across reload.
  await page.locator('#displaySettingsButton').click();
  await Promise.all([
    page.waitForResponse((res) => new URL(res.url()).pathname === `${host.managementBase}/preferences` && res.request().method() === 'POST'),
    page.locator('#displaySectionHealth').uncheck(),
  ]);
  const postedSections = preferencePosts[preferencePosts.length - 1]?.hidden_sections || [];
  if (!postedSections.includes('health')) {
    throw new Error(`preferences POST did not include "health": ${JSON.stringify(postedSections)}`);
  }
  await Promise.all([
    page.waitForResponse((res) => new URL(res.url()).pathname === `${host.managementBase}/preferences` && res.request().method() === 'GET'),
    page.waitForResponse((res) => new URL(res.url()).pathname === `${host.managementBase}/stats/initial`),
    page.reload({ waitUntil: 'domcontentloaded' }),
  ]);
  const healthHiddenAfterReload = await page.locator('#healthPanel').evaluate((el) => el.hidden);
  if (!healthHiddenAfterReload) {
    throw new Error('#healthPanel did not remain hidden after reload');
  }

  if (pageErrors.length) {
    throw pageErrors[0];
  }
} finally {
  releaseHeldTrend();
  await browser.close();
  await host.close();
}
