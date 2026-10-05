import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { chromium } from 'playwright-core';

const [htmlPath, chromePath, scenario = 'exclusive'] = process.argv.slice(2);
if (!htmlPath || !chromePath) {
  throw new Error('usage: node test/dashboard_date_range.mjs <dashboard-html-path> <google-chrome-path> [exclusive|end-time|end-time-reset|quick-preset|reverse|los-angeles-dst|token-unit|single-day|time-wrap|sort-feedback|cpa-auth-reuse|pricing-save-stamp]');
}
if (!['exclusive', 'end-time', 'end-time-reset', 'quick-preset', 'reverse', 'los-angeles-dst', 'token-unit', 'single-day', 'time-wrap', 'sort-feedback', 'cpa-auth-reuse', 'pricing-save-stamp'].includes(scenario)) {
  throw new Error(`unknown dashboard date-range browser scenario: ${scenario}`);
}

const dashboardHTML = await readFile(htmlPath);
const resourceBase = '/v0/resource/plugins/calendar-browser-test';
const managementBase = '/v0/management/plugins/calendar-browser-test';
const timezoneId = scenario === 'los-angeles-dst' ? 'America/Los_Angeles' : 'UTC';
const initialRange = scenario === 'los-angeles-dst'
  ? { start: '2026-08-23T07:00:00.000Z', end: '2026-08-24T07:00:00.000Z' }
  : { start: '2026-08-23T00:00:00.000Z', end: '2026-08-24T00:00:00.000Z' };
const emptyInitial = {
  generated_at: '2026-08-23T00:00:00.000Z',
  last_used: '0001-01-01T00:00:00.000Z',
  models: [],
  sources: [],
  bucket_seconds: 86400,
};
const tokenUnitInitial = {
  generated_at: '2026-08-23T00:00:00.000Z',
  last_used: '2026-08-23T12:00:00.000Z',
  models: [{ model: 'browser-test', requests: 1, input_tokens: 1000000000, output_tokens: 230000000, reasoning_tokens: 0, cache_read_tokens: 0, cache_creation_tokens: 0, total_tokens: 1230000000 }],
  sources: [],
  bucket_seconds: 86400,
};
const initialPayload = scenario === 'token-unit' ? tokenUnitInitial : emptyInitial;
const savedTokenDisplayModes = [];
// Server-side price-book state for the pricing-save-stamp scenario: the saved_at
// stamp lives in the mock store (like the real bolt metadata), so it survives a
// page reload and the chip must pick it up from the GET response.
const savedPriceBookState = { savedAt: '' };
let initialAuthHeader = '';

async function setTimePickerValue(page, boundary, values) {
  for (const [part, value] of Object.entries(values)) {
    await page.locator(`#${boundary}TimePicker [data-time-part="${part}"]`).evaluate((field, nextValue) => {
      field.value = nextValue;
      field.dispatchEvent(new Event('input', { bubbles: true }));
    }, value);
  }
}

const server = createServer(async (request, response) => {
  const url = new URL(request.url, 'http://127.0.0.1');
  const sendJSON = (value) => {
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end(JSON.stringify(value));
  };

  if (url.pathname === `${resourceBase}/dashboard`) {
    response.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
    response.end(dashboardHTML);
    return;
  }
  if (url.pathname === `${managementBase}/preferences`) {
    if (request.method === 'POST') {
      let body = '';
      request.setEncoding('utf8');
      for await (const chunk of request) body += chunk;
      let posted = {};
      try {
        posted = JSON.parse(body || '{}');
      } catch {
        posted = {};
      }
      savedTokenDisplayModes.push(posted.token_display_mode);
      sendJSON({
        request_page_size: posted.request_page_size ?? 100,
        dimension_page_size: posted.dimension_page_size ?? 100,
        hidden_request_columns: posted.hidden_request_columns ?? [],
        hidden_dimension_columns: posted.hidden_dimension_columns ?? [],
        time_range_mode: posted.time_range_mode ?? 'custom',
        token_display_mode: posted.token_display_mode ?? 'full',
      });
      return;
    }
    sendJSON({
      time_range_mode: 'custom',
      time_range_start: initialRange.start,
      time_range_end: initialRange.end,
      token_display_mode: scenario === 'token-unit' ? 'B' : 'full',
    });
    return;
  }
  if (url.pathname === `${managementBase}/stats/initial`) {
    initialAuthHeader = request.headers.authorization ?? '';
    sendJSON(initialPayload);
    return;
  }
  if (url.pathname === `${managementBase}/stats/trends`) {
    sendJSON({ model_series: [], bucket_seconds: 86400 });
    return;
  }
  if (url.pathname === `${managementBase}/stats/groups`) {
    sendJSON({ items: [{ model: 'browser-test', provider: 'openai', requests: 1, failed_requests: 0, input_tokens: 500000000, output_tokens: 300000000, reasoning_tokens: 0, cache_read_tokens: 230000000, cache_creation_tokens: 0, total_tokens: 1230000000, average_latency_ns: 0, average_ttft_ns: 0 }], total: 1 });
    return;
  }
  if (url.pathname === `${managementBase}/requests`) {
    sendJSON({ items: [], total: 0 });
    return;
  }
  if (url.pathname === `${managementBase}/costs`) {
    sendJSON({ summary: { requests: 0, priced_requests: 0, unpriced_requests: 0 }, models: [], price_book_revision: 0 });
    return;
  }
  if (url.pathname === `${managementBase}/prices`) {
    if (request.method === 'PUT') {
      let body = '';
      request.setEncoding('utf8');
      for await (const chunk of request) body += chunk;
      try {
        JSON.parse(body || '{}');
      } catch {
        response.writeHead(400, { 'content-type': 'application/json' });
        response.end(JSON.stringify({ error: 'invalid price book body' }));
        return;
      }
      savedPriceBookState.savedAt = '2026-08-23T12:00:00.000Z';
      response.writeHead(200, { 'content-type': 'application/json' });
      response.end(JSON.stringify({ prices: {}, revision: 1, sync_settings: { provider_priority: [], ignored_suffixes: [], mappings: [] }, saved_at: savedPriceBookState.savedAt }));
      return;
    }
    sendJSON({
      prices: {},
      revision: savedPriceBookState.savedAt ? 1 : 0,
      sync_settings: { provider_priority: [], ignored_suffixes: [], mappings: [] },
      ...(savedPriceBookState.savedAt ? { saved_at: savedPriceBookState.savedAt } : {}),
    });
    return;
  }
  sendJSON({});
});

await new Promise((resolve, reject) => {
  server.once('error', reject);
  server.listen(0, '127.0.0.1', resolve);
});

const address = server.address();
const dashboardURL = `http://127.0.0.1:${address.port}${resourceBase}/dashboard`;
const browser = await chromium.launch({ executablePath: chromePath, headless: true });

try {
  const context = await browser.newContext({ locale: 'zh-CN', timezoneId });
  // The dashboard resolves its management key before the first data fetch. Regular
  // scenarios seed the per-tab gate storage so the auth page never appears; the
  // cpa-auth-reuse scenario instead plants the management center's persisted
  // credentials (same envelope and obfuscation as CPA) to exercise that fallback.
  await context.addInitScript((reuse) => {
    window.__cpaAuthObfuscate = (plaintext) => {
      const secret = new TextEncoder().encode('cli-proxy-api-webui::secure-storage|' + window.location.host + '|' + navigator.userAgent);
      const bytes = new TextEncoder().encode(plaintext);
      let binary = '';
      for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i] ^ secret[i % secret.length]);
      return 'enc::v1::' + btoa(binary);
    };
    if (!reuse) {
      sessionStorage.setItem('tokens-statistic-key', 'calendar-browser-test-key');
      return;
    }
    if (!localStorage.getItem('cli-proxy-auth')) {
      localStorage.setItem('cli-proxy-auth', window.__cpaAuthObfuscate(JSON.stringify({
        state: { apiBase: window.location.origin, managementKey: 'cpa-stored-management-key', rememberPassword: true, serverVersion: null, serverBuildDate: null },
        version: 0,
      })));
      localStorage.setItem('isLoggedIn', 'true');
    }
  }, scenario === 'cpa-auth-reuse');
  const page = await context.newPage();
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(error));

  await page.clock.install({ time: new Date('2026-08-23T12:00:00.000Z') });
  await page.goto(dashboardURL, { waitUntil: 'domcontentloaded' });
  await page.waitForResponse((response) => new URL(response.url()).pathname === `${managementBase}/stats/initial`);

  if (scenario === 'cpa-auth-reuse') {
    await page.waitForFunction(() => document.querySelectorAll('#groups tr').length > 0);
    if (initialAuthHeader !== 'Bearer cpa-stored-management-key') {
      throw new Error(`dashboard must reuse the management key stored in the management center's cli-proxy-auth envelope, got authorization header "${initialAuthHeader}"`);
    }
    if (await page.evaluate(() => !document.getElementById('authView').hidden)) {
      throw new Error('auth page must stay closed when the stored management center credentials can be reused');
    }
    // Negative control: the same envelope without a stored key must still park
    // the dashboard on the auth page.
    await page.evaluate(() => {
      localStorage.setItem('cli-proxy-auth', window.__cpaAuthObfuscate(JSON.stringify({
        state: { apiBase: window.location.origin, rememberPassword: true, serverVersion: null, serverBuildDate: null },
        version: 0,
      })));
      sessionStorage.clear();
    });
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.waitForFunction(() => !document.getElementById('authView')?.hidden && document.getElementById('appView').classList.contains('hidden'));
  } else if (scenario === 'token-unit') {
    const tokenButton = page.locator('#tokenUnitButton');
    const totalTokens = page.locator('#totalTokens');
    const expected = [
      ['B', '1.23B', 'full'],
      ['完整', '1,230,000,000', 'k'],
      ['K', '1,230,000K', 'm'],
      ['M', '1,230M', 'B'],
    ];
    for (const [buttonText, totalText, savedMode] of expected) {
      if (await tokenButton.textContent() !== buttonText) {
        throw new Error(`expected token unit button ${buttonText}, got ${await tokenButton.textContent()}`);
      }
      if (await totalTokens.textContent() !== totalText) {
        throw new Error(`expected total tokens ${totalText}, got ${await totalTokens.textContent()}`);
      }
      await Promise.all([
        page.waitForResponse((response) => {
          const savedURL = new URL(response.url());
          let posted = {};
          try {
            posted = JSON.parse(response.request().postData() || '{}');
          } catch {
            posted = {};
          }
          return savedURL.pathname === `${managementBase}/preferences`
            && response.request().method() === 'POST'
            && posted.token_display_mode === savedMode;
        }),
        tokenButton.click(),
      ]);
      const dimensionText = await page.locator('#groups').textContent();
      const dimensionExpected = { full: '1,230,000,000', k: '1,230,000K', m: '1,230M', B: '1.23B' }[savedMode];
      if (!dimensionText.includes(dimensionExpected)) {
        throw new Error(`dimension table did not switch to ${dimensionExpected}: ${dimensionText.slice(0, 120)}`);
      }
    }
    if (JSON.stringify(savedTokenDisplayModes) !== JSON.stringify(['full', 'k', 'm', 'B'])) {
      throw new Error(`expected saved token display modes full,k,m,B, got ${savedTokenDisplayModes.join(',')}`);
    }
    if (await tokenButton.textContent() !== 'B' || await totalTokens.textContent() !== '1.23B') {
      throw new Error(`expected token unit cycle to return to B, got ${await tokenButton.textContent()} / ${await totalTokens.textContent()}`);
    }
  }

  if (scenario === 'sort-feedback') {
    // The dimension table sorts server-side, so the sort click must acknowledge itself
    // instantly (arrow + loading state) instead of waiting for the groups round trip.
    await page.waitForFunction(() => document.querySelectorAll('#groups tr').length > 0);
    if (await page.locator('#groups tr').count() !== 1) {
      throw new Error(`expected one dimension row after initial load, got ${await page.locator('#groups tr').count()}`);
    }
    await page.route((url) => url.pathname === `${managementBase}/stats/groups`, async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 800));
      await route.continue();
    });
    let sortedGroupsURL = '';
    await page.route((url) => url.pathname === `${managementBase}/stats/groups`, async (route) => {
      sortedGroupsURL = route.request().url();
      await new Promise((resolve) => setTimeout(resolve, 800));
      await route.continue();
    });
    const requestsHeader = page.locator('#dimensionHeaders th[data-column="requests"]');
    if (await requestsHeader.getAttribute('aria-sort') !== 'none') {
      throw new Error(`requests header must start unsorted, got ${await requestsHeader.getAttribute('aria-sort')}`);
    }
    await page.locator('#dimensionHeaders [data-dimension-sort="requests"]').click();
    if (await requestsHeader.getAttribute('aria-sort') !== 'descending') {
      throw new Error(`sort header must update instantly on click before the server responds, got ${await requestsHeader.getAttribute('aria-sort')}`);
    }
    const tableState = await page.locator('.dimension-table').evaluate((node) => ({ busy: node.getAttribute('aria-busy'), loading: node.classList.contains('is-loading') }));
    if (tableState.busy !== 'true' || !tableState.loading) {
      throw new Error(`dimension table must be in the loading state while the groups request is in flight, got ${JSON.stringify(tableState)}`);
    }
    await page.waitForFunction(() => {
      const node = document.querySelector('.dimension-table');
      return Boolean(node) && !node.classList.contains('is-loading') && !node.hasAttribute('aria-busy');
    });
    const sortedQuery = new URL(sortedGroupsURL).searchParams;
    if (sortedQuery.get('sort') !== 'requests' || sortedQuery.get('direction') !== 'desc') {
      throw new Error(`sort click must request sort=requests&direction=desc, got ${sortedGroupsURL}`);
    }
    if (await page.locator('#groups tr').count() !== 1) {
      throw new Error('dimension table must re-render its rows after the sort response');
    }
  } else if (scenario !== 'cpa-auth-reuse' && scenario !== 'pricing-save-stamp') {
  await page.locator('#rangeButton').click();
  if (scenario === 'quick-preset') {
    await page.locator('[data-range-preset="last_30_days"]').click();

    for (const boundary of ['start', 'end']) {
      const button = page.locator(`#${boundary}TimeButton`);
      if (await button.isDisabled()) {
        throw new Error(`${boundary} time must remain editable after choosing a quick range`);
      }
      const expected = boundary === 'start' ? '00:00:00' : '00:00:00';
      if (await button.textContent() !== expected) {
        throw new Error(`last 30 days ${boundary} must display ${expected}, got ${await button.textContent()}`);
      }
    }

    await page.locator('#startTimeButton').click();
    await setTimePickerValue(page, 'start', { hour: '01', minute: '02', second: '03' });
    const confirmedResponse = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return url.pathname === `${managementBase}/stats/initial`
        && url.searchParams.get('start') === '2026-07-25T01:02:03.000Z';
    });
    await page.locator('#confirmDateRange').click();
    const confirmed = new URL((await confirmedResponse).url());
    if (confirmed.searchParams.get('end') !== '2026-08-24T00:00:00.000Z') {
      throw new Error(`expected manually edited quick range end=2026-08-24T00:00:00.000Z, got ${confirmed.searchParams.get('end')}`);
    }
  } else if (scenario === 'single-day') {
    await page.locator('[data-date="2026-08-20"]').click();
  } else if (scenario === 'time-wrap') {
    await page.locator('[data-date="2026-08-21"]').click();
    await page.locator('[data-date="2026-08-23"]').click();
    const endButton = page.locator('#endTimeButton');
    if (await endButton.textContent() !== '00:00:00') {
      throw new Error(`exclusive midnight end must display as 00:00:00, got ${await endButton.textContent()}`);
    }
    const startButton = page.locator('#startTimeButton');
    await startButton.click();
    await setTimePickerValue(page, 'start', { hour: '23' });
    if (await startButton.textContent() !== '23:00:00') {
      throw new Error(`start hour must reach 23, got ${await startButton.textContent()}`);
    }
    await setTimePickerValue(page, 'start', { hour: '24' });
    if (await startButton.textContent() !== '00:00:00') {
      throw new Error(`start hour 24 must wrap to 00, got ${await startButton.textContent()}`);
    }
    await setTimePickerValue(page, 'start', { hour: '-1' });
    if (await startButton.textContent() !== '23:00:00') {
      throw new Error(`start hour -1 must wrap to 23, got ${await startButton.textContent()}`);
    }
    await setTimePickerValue(page, 'start', { minute: '60' });
    if (await startButton.textContent() !== '23:00:00') {
      throw new Error(`start minute 60 must wrap to 00, got ${await startButton.textContent()}`);
    }
    const confirmedResponse = page.waitForResponse((response) => response.url().includes('/stats/initial'));
    await page.locator('#confirmDateRange').click();
    const confirmed = new URL((await confirmedResponse).url());
    if (confirmed.searchParams.get('start') !== '2026-08-21T23:00:00.000Z' || confirmed.searchParams.get('end') !== '2026-08-24T00:00:00.000Z') {
      throw new Error(`time-wrap confirmed ${confirmed.searchParams.get('start')} .. ${confirmed.searchParams.get('end')}`);
    }
  } else if (scenario === 'reverse') {
    await page.locator('[data-date="2026-08-23"]').click();
    await page.locator('[data-date="2026-08-21"]').click();
  } else {
    await page.locator('[data-date="2026-08-21"]').click();
    await page.locator('[data-date="2026-08-23"]').click();
  }

  if (scenario === 'quick-preset') {
    // Verified above.
  } else if (scenario === 'time-wrap') {
    // Verified inline above.
  } else if (scenario === 'exclusive') {
    const selectedEnd = page.locator('[data-date="2026-08-23"].range-end');
    if (await selectedEnd.count() !== 1) {
      throw new Error('selecting 2026-08-21 through 2026-08-23 must mark 2026-08-23 as .range-end');
    }

    const confirmedResponse = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return url.pathname === `${managementBase}/stats/initial`
        && url.searchParams.get('start') === '2026-08-21T00:00:00.000Z';
    });
    await page.locator('#confirmDateRange').click();
    const confirmed = new URL((await confirmedResponse).url());
    if (confirmed.searchParams.get('start') !== '2026-08-21T00:00:00.000Z') {
      throw new Error(`expected confirmed start=2026-08-21T00:00:00.000Z, got ${confirmed.searchParams.get('start')}`);
    }
    if (confirmed.searchParams.get('end') !== '2026-08-24T00:00:00.000Z') {
      throw new Error(`expected confirmed end=2026-08-24T00:00:00.000Z, got ${confirmed.searchParams.get('end')}`);
    }
  } else if (scenario === 'single-day') {
    const confirmedResponse = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return url.pathname === `${managementBase}/stats/initial`
        && url.searchParams.get('start') === '2026-08-20T00:00:00.000Z';
    });
    await page.locator('#confirmDateRange').click();
    const confirmed = new URL((await confirmedResponse).url());
    if (confirmed.searchParams.get('end') !== '2026-08-21T00:00:00.000Z') {
      throw new Error(`expected single-day end=2026-08-21T00:00:00.000Z, got ${confirmed.searchParams.get('end')}`);
    }
  } else if (scenario === 'los-angeles-dst') {
    const selectedEnd = page.locator('[data-date="2026-08-23"].range-end');
    if (await selectedEnd.count() !== 1) {
      throw new Error('America/Los_Angeles selection must mark 2026-08-23 as .range-end');
    }

    const confirmedResponse = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return url.pathname === `${managementBase}/stats/initial`
        && url.searchParams.get('start') === '2026-08-21T07:00:00.000Z';
    });
    await page.locator('#confirmDateRange').click();
    const confirmed = new URL((await confirmedResponse).url());
    if (confirmed.searchParams.get('start') !== '2026-08-21T07:00:00.000Z') {
      throw new Error(`expected America/Los_Angeles start=2026-08-21T07:00:00.000Z, got ${confirmed.searchParams.get('start')}`);
    }
    if (confirmed.searchParams.get('end') !== '2026-08-24T07:00:00.000Z') {
      throw new Error(`expected America/Los_Angeles end=2026-08-24T07:00:00.000Z, got ${confirmed.searchParams.get('end')}`);
    }
  } else if (scenario === 'end-time' || scenario === 'end-time-reset') {
    await page.locator('#endTimeButton').click();
    await setTimePickerValue(page, 'end', { hour: '12', minute: '00', second: '00' });

    if (scenario === 'end-time-reset') {
      await setTimePickerValue(page, 'end', { hour: '00', minute: '00', second: '00' });
    }

    const selectedEnd = page.locator('[data-date="2026-08-23"].range-end');
    if (await selectedEnd.count() !== 1) {
      throw new Error(scenario === 'end-time'
        ? 'setting end time to 12:00:00 must keep 2026-08-23 as .range-end'
        : 'resetting end time from 12:00:00 to 00:00:00 must keep 2026-08-23 as .range-end');
    }

    const confirmedResponse = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return url.pathname === `${managementBase}/stats/initial`
        && url.searchParams.get('start') === '2026-08-21T00:00:00.000Z';
    });
    await page.locator('#confirmDateRange').click();
    const confirmed = new URL((await confirmedResponse).url());
    const expectedEnd = scenario === 'end-time' ? '2026-08-23T12:00:00.000Z' : '2026-08-24T00:00:00.000Z';
    if (confirmed.searchParams.get('end') !== expectedEnd) {
      throw new Error(`expected ${scenario} end=${expectedEnd}, got ${confirmed.searchParams.get('end')}`);
    }
  } else {
    const selectedStart = page.locator('[data-date="2026-08-21"].range-start');
    if (await selectedStart.count() !== 1) {
      throw new Error('reverse selection 2026-08-23 through 2026-08-21 must mark 2026-08-21 as .range-start');
    }
    const selectedEnd = page.locator('[data-date="2026-08-23"].range-end');
    if (await selectedEnd.count() !== 1) {
      throw new Error('reverse selection 2026-08-23 through 2026-08-21 must mark 2026-08-23 as .range-end');
    }

    const confirmedResponse = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return url.pathname === `${managementBase}/stats/initial`
        && url.searchParams.get('start') === '2026-08-21T00:00:00.000Z';
    });
    await page.locator('#confirmDateRange').click();
    const confirmed = new URL((await confirmedResponse).url());
    if (confirmed.searchParams.get('end') !== '2026-08-24T00:00:00.000Z') {
      throw new Error(`expected reverse-selection end=2026-08-24T00:00:00.000Z, got ${confirmed.searchParams.get('end')}`);
    }
  }
  }
  if (scenario === 'pricing-save-stamp') {
    // The "last saved" chip is fed by the server-side saved_at field: a fresh
    // price book shows the never-saved placeholder, a successful save flips it
    // (and drops the confirmation toast 64px below the viewport top), and the
    // stamp survives a reload because it comes from the GET response, not page
    // memory.
    const pricesLoaded = () => page.waitForResponse((response) => new URL(response.url()).pathname === `${managementBase}/prices`);
    const chip = page.locator('#lastSaveStatus');
    const openDialog = async () => {
      await page.locator('#pricingButton').click();
      await page.waitForFunction(() => document.getElementById('pricingDialog').open);
    };
    await pricesLoaded();
    await openDialog();
    if (await chip.textContent() !== '尚未保存') {
      throw new Error(`fresh price book must show the never-saved chip, got "${await chip.textContent()}"`);
    }
    const saveResponse = page.waitForResponse((response) => new URL(response.url()).pathname === `${managementBase}/prices` && response.request().method() === 'PUT');
    await page.locator('#saveSyncSettings').click();
    await saveResponse;
    // Wait until the slide-in transition has settled (hidden state sits at
    // translateY(-8px); the visible state must rest at translateY(0)).
    await page.waitForFunction(() => {
      const toast = document.getElementById('syncSettingsToast');
      if (!toast.classList.contains('is-visible')) return false;
      const match = /matrix\([^,]+, [^,]+, [^,]+, [^,]+, [^,]+, (-?\d+(?:\.\d+)?)\)/.exec(getComputedStyle(toast).transform);
      return Boolean(match) && Math.abs(parseFloat(match[1])) < 0.5;
    });
    const toastBox = await page.locator('#syncSettingsToast').boundingBox();
    if (!toastBox || toastBox.y < 60 || toastBox.y > 68) {
      throw new Error(`save toast must sit exactly 64px below the viewport top, got ${JSON.stringify(toastBox)}`);
    }
    if (Math.abs((toastBox.x + toastBox.width / 2) - page.viewportSize().width / 2) > 2) {
      throw new Error(`save toast must be horizontally centered, got ${JSON.stringify(toastBox)}`);
    }
    // The dialog entrance animation must live on .dialog-body, never on the
    // dialog itself: a transformed dialog would become the toast's containing
    // block (offsetParent) and re-anchor "viewport top + 64px" to the dialog.
    const toastAnchoredToViewport = await page.evaluate(() => document.getElementById('syncSettingsToast').offsetParent === null);
    if (!toastAnchoredToViewport) {
      throw new Error('save toast must be anchored to the viewport (offsetParent null), not to the dialog');
    }
    if (await chip.textContent() !== '上次保存 2026/8/23 12:00:00') {
      throw new Error(`saving must stamp the last-saved chip from the server response, got "${await chip.textContent()}"`);
    }
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.waitForResponse((response) => new URL(response.url()).pathname === `${managementBase}/stats/initial`);
    await pricesLoaded();
    await openDialog();
    if (await chip.textContent() !== '上次保存 2026/8/23 12:00:00') {
      throw new Error(`last-saved chip must survive a reload (server truth), got "${await chip.textContent()}"`);
    }
  }
  if (pageErrors.length) {
    throw pageErrors[0];
  }
} finally {
  await browser.close();
  await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
}
