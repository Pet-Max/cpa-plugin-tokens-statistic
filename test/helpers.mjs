// Shared browser-test infrastructure. Every dashboard scenario file drives the
// same assembled page against this mock host instead of re-implementing the
// management API surface, so a change to an endpoint shape lands in one place.
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';

export async function readDashboardFile(htmlPath) {
  return readFile(htmlPath);
}

// One consistent usage record per scenario; override rows via the fixtures
// argument rather than editing these literals.
export function defaultFixtures({ model = 'browser-test-model' } = {}) {
  return {
    apiKeyInfo: { api_key_tracking_enabled: true, api_key_uses_default_secret: false, api_key_labels: {} },
    preferences: {},
    preferencesPost: null,
    statsInitial: {
      generated_at: '2026-08-23T12:00:00.000Z',
      last_used: '2026-08-23T12:00:00.000Z',
      models: [{ model, provider: 'openai', requests: 2, input_tokens: 10, output_tokens: 20, total_tokens: 30 }],
      sources: [],
      bucket_seconds: 86400,
    },
    statsTrends: {
      model_series: [{ hour: '2026-08-23T11:00:00Z', model, requests: 1, input_tokens: 10, output_tokens: 20, total_tokens: 30 }],
      bucket_seconds: 86400,
    },
    statsGroups: {
      items: [{
        model,
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
        price_source: 'unpriced',
      }],
      total: 1,
    },
    requests: {
      generated_at: '2026-08-23T12:00:00.000Z',
      range: '24h',
      total: 1,
      offset: 0,
      limit: 100,
      items: [{
        sequence: 1,
        time: '2026-08-23T11:59:00.000Z',
        model,
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
      }],
    },
    costs: { summary: { requests: 1, priced_requests: 0, unpriced_requests: 1 }, models: [], price_book_revision: 0 },
    priceBook: { prices: {}, revision: 0, sync_settings: { provider_priority: [], ignored_suffixes: [], mappings: [] } },
    exchangeRate: null,
    modelsCatalog: null,
  };
}

// Starts the mock host for one scenario. Behaviour knobs:
//   denyKeys            Bearer keys rejected with 401 (auth-gate scenarios).
//   apiKeyInfoDelayMs   Delays /api-key-info to reproduce late tracking state.
//   capture             Optional object; the helper pushes parsed bodies into
//                       capture.putPrices / capture.postSync / capture.postPreferences.
//   rawHandler           Optional (request, response, url) => bool; runs before
//                       the standard endpoints and returns true when handled —
//                       for binary or destructive-operation endpoints (backup,
//                       restore, reset). Combine with the returned
//                       updateFixtures(patch) to swap served data mid-scenario.
//   liveBook            PUT /prices mutates the served price book (revision +1),
//                       POST /prices/sync bumps the revision again.
// Returns { url, resourceBase, managementBase, close, lastAuthHeader }.
export function startDashboardServer({ dashboardHTML, pluginId, fixtures = defaultFixtures(), denyKeys = [], apiKeyInfoDelayMs = 0, capture = null, rawHandler = null }) {
  const fx = { ...defaultFixtures(), ...fixtures };
  const resourceBase = `/v0/resource/plugins/${pluginId}`;
  const managementBase = `/v0/management/plugins/${pluginId}`;
  let liveBook = { ...fx.priceBook };
  let exchangeRateRequested = false;
  let lastAuthHeader = '';

  const server = createServer((request, response) => {
    const url = new URL(request.url, 'http://127.0.0.1');
    const sendJSON = (value) => {
      response.writeHead(200, { 'content-type': 'application/json' });
      response.end(JSON.stringify(value));
    };
    const rejectUnauthorized = () => {
      response.writeHead(401, { 'content-type': 'application/json' });
      response.end(JSON.stringify({ error: 'invalid management key' }));
    };
    const readBody = async () => {
      let body = '';
      request.setEncoding('utf8');
      for await (const chunk of request) body += chunk;
      try {
        return JSON.parse(body || '{}');
      } catch {
        return {};
      }
    };

    if (rawHandler && rawHandler(request, response, url) === true) {
      return;
    }
    if (url.pathname === `${resourceBase}/dashboard`) {
      response.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      response.end(dashboardHTML);
      return;
    }
    if (url.pathname.startsWith(`${managementBase}/`)) {
      lastAuthHeader = request.headers.authorization || '';
      const key = lastAuthHeader.startsWith('Bearer ') ? lastAuthHeader.slice(7) : '';
      if (!key || denyKeys.includes(key)) {
        rejectUnauthorized();
        return;
      }
    }
    if (url.pathname === `${managementBase}/api-key-info`) {
      if (fx.apiKeyInfo === 'no-response') return;
      if (apiKeyInfoDelayMs > 0) {
        setTimeout(() => sendJSON(fx.apiKeyInfo), apiKeyInfoDelayMs);
      } else {
        sendJSON(fx.apiKeyInfo);
      }
      return;
    }
    if (url.pathname === `${managementBase}/preferences`) {
      if (request.method === 'POST') {
        readBody().then((posted) => {
          if (capture) capture.postPreferences.push(posted);
          if (typeof fx.preferencesPost === 'function') {
            sendJSON(fx.preferencesPost(posted));
            return;
          }
          sendJSON(posted);
        });
        return;
      }
      sendJSON(fx.preferences);
      return;
    }
    if (url.pathname === `${managementBase}/stats/initial`) {
      if (fx.statsInitial === 'no-response') return;
      sendJSON(fx.statsInitial);
      return;
    }
    if (url.pathname === `${managementBase}/stats/trends`) {
      if (fx.statsTrends === 'no-response') return;
      sendJSON(fx.statsTrends);
      return;
    }
    if (url.pathname === `${managementBase}/stats/groups`) {
      if (fx.statsGroups === 'no-response') return;
      sendJSON(fx.statsGroups);
      return;
    }
    if (url.pathname === `${managementBase}/requests`) {
      if (fx.requests === 'no-response') return;
      sendJSON(fx.requests);
      return;
    }
    if (url.pathname === `${managementBase}/costs`) {
      if (fx.costs === 'no-response') return;
      sendJSON(fx.costs);
      return;
    }
    if (url.pathname === `${managementBase}/prices` && request.method === 'PUT') {
      readBody().then((posted) => {
        if (capture) capture.putPrices.push(posted);
        liveBook = { prices: posted.prices ?? {}, revision: Number(liveBook.revision || 0) + 1, sync_settings: liveBook.sync_settings, last_sync: liveBook.last_sync };
        sendJSON(liveBook);
      });
      return;
    }
    if (url.pathname === `${managementBase}/prices/sync` && request.method === 'POST') {
      readBody().then((posted) => {
        if (capture) capture.postSync.push(posted);
        liveBook = { ...liveBook, revision: Number(liveBook.revision || 0) + 1 };
        sendJSON(liveBook);
      });
      return;
    }
    if (url.pathname === `${managementBase}/prices`) {
      if (fx.priceBook === 'no-response') return;
      sendJSON(liveBook);
      return;
    }
    if (url.pathname === `${managementBase}/exchange-rate`) {
      exchangeRateRequested = true;
      sendJSON(fx.exchangeRate ?? {});
      return;
    }
    if (url.pathname === '/v1/models') {
      sendJSON(fx.modelsCatalog ?? {});
      return;
    }
    sendJSON({});
  });

  const listening = new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });

  const ready = listening.then(() => {
    const address = server.address();
    return `http://127.0.0.1:${address.port}${resourceBase}/dashboard`;
  });

  return {
    ready,
    resourceBase,
    managementBase,
    close: () => server.close(),
    lastAuthHeader: () => lastAuthHeader,
    resetLastAuthHeader: () => { lastAuthHeader = ''; },
    exchangeRateRequested: () => exchangeRateRequested,
    updateFixtures: (patch) => { Object.assign(fx, patch); },
  };
}

// Standard success-path authentication: fill the key, submit, wait for the
// first data wave, and require the auth page to disappear. Pass `frame` (a
// playwright Frame) when the dashboard runs inside an iframe of a parent page.
export async function authenticate(page, { managementBase, key, waitFor = ['stats/initial', 'requests', 'stats/groups'], frame = null }) {
  const scope = frame ?? page;
  const authView = scope.locator('#authView');
  await authView.waitFor({ state: 'visible' });
  const settled = Promise.all(waitFor.map((endpoint) =>
    page.waitForResponse((response) => new URL(response.url()).pathname === `${managementBase}/${endpoint}`),
  ));
  await scope.locator('#authKeyInput').fill(key);
  await scope.locator('#authSubmit').click();
  await settled;
  if (await authView.evaluate((node) => !node.hidden)) {
    throw new Error('auth page must disappear after successful verification');
  }
}

// Installs the in-page export capture (blob + suggested filename pairs) into a
// browser context and returns a reader. Chrome blocks silent multi-file
// downloads in headless mode, so observing the blob/anchor pair is the stable
// seam for export assertions.
export function installExportCapture(context) {
  void context.addInitScript(() => {
    window.__exports = [];
    const originalCreateObjectURL = URL.createObjectURL;
    URL.createObjectURL = function (blob) {
      window.__exports.push({ name: null, blob, type: blob.type, size: blob.size });
      return originalCreateObjectURL.call(this, blob);
    };
    const originalAnchorClick = HTMLAnchorElement.prototype.click;
    HTMLAnchorElement.prototype.click = function () {
      if (this.download) {
        const pending = window.__exports.length ? window.__exports[window.__exports.length - 1] : null;
        if (pending && pending.name === null) pending.name = this.download;
      }
      return originalAnchorClick.call(this);
    };
  });
}

// Waits until at least `count` exports have been captured in the page.
export async function waitForExports(page, count, timeout = 10000) {
  await page.waitForFunction((expected) => window.__exports.length >= expected, count, { timeout });
}

// Reads every captured export (name, media type, BOM bytes and text body).
export async function readCapturedExports(page) {
  return page.evaluate(async () => {
    const out = [];
    for (const item of window.__exports) {
      const head = Array.from(new Uint8Array(await item.blob.slice(0, 3).arrayBuffer()));
      out.push({ name: item.name, type: item.type, size: item.size, head, text: await item.blob.text() });
    }
    return out;
  });
}

// Loads the shipped locale catalogs so assertions compare against the real
// translations instead of duplicated literals.
export async function loadLocaleCatalogs(localesDir, specs) {
  const catalogs = {};
  for (const spec of specs) {
    catalogs[spec.code] = JSON.parse(await readFile(`${localesDir}/${spec.file}`, 'utf8'));
  }
  return catalogs;
}
