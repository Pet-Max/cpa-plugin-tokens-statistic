import { chromium } from 'playwright-core';
import { readDashboardFile, startDashboardServer, defaultFixtures } from './helpers.mjs';

const [htmlPath, chromePath] = process.argv.slice(2);
if (!htmlPath || !chromePath) {
  throw new Error('usage: node test/dashboard_key_gate.mjs <dashboard-html-path> <google-chrome-path>');
}

const dashboardHTML = await readDashboardFile(htmlPath);
const resourceBase = '/v0/resource/plugins/key-gate-browser-test';
const managementBase = '/v0/management/plugins/key-gate-browser-test';
const GOOD_KEY = 'gate-browser-key';
// Keys the mock rejects, mirroring a host whose management key does not match.
const DENY_KEYS = new Set(['wrong-key', 'bad-stored-key']);
const EMPTY_MESSAGE = '请输入管理密钥。';
const INVALID_MESSAGE = '管理密钥无效或与当前配置不一致，请重新输入。';
const CENTER_INVALID_MESSAGE = '管理中心记住的密钥已失效。请到管理中心重新登录，或在本页输入当前密钥。';

const requestRow = {
  sequence: 1,
  time: '2026-08-23T11:59:00.000Z',
  model: 'gate-browser-model',
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
};
const groupRow = {
  model: 'gate-browser-model',
  provider: 'openai',
  source: 'cli',
  requests: 2,
  input_tokens: 10,
  output_tokens: 20,
  total_tokens: 30,
};


// Auth-gate semantics: empty/absent keys and deny-listed keys are rejected
// with 401 before any endpoint runs; /api-key-info arrives late (900ms) to
// reproduce the window where tracking state has not landed yet.
const host = startDashboardServer({
  dashboardHTML,
  pluginId: 'key-gate-browser-test',
  fixtures: defaultFixtures({ model: 'gate-browser-model' }),
  denyKeys: ['wrong-key', 'bad-stored-key'],
  apiKeyInfoDelayMs: 900,
});
const dashboardURL = await host.ready;
const browser = await chromium.launch({ executablePath: chromePath, headless: true });

try {
  const context = await browser.newContext({ locale: 'zh-CN', timezoneId: 'UTC' });
  // Instrument before any page script runs: count management fetches fired and
  // expose the same obfuscation the plugin uses, so the test can plant stored
  // credentials without duplicating the crypto inline.
  await context.addInitScript(() => {
    window.__mgmtFetches = 0;
    window.__of = window.fetch;
    window.fetch = function (u, o) {
      if (String(u).indexOf('/v0/management/plugins/key-gate-browser-test/') === 0) window.__mgmtFetches += 1;
      return window.__of.apply(this, arguments);
    };
    window.__obf = (plaintext) => {
      const secret = new TextEncoder().encode('cli-proxy-api-webui::secure-storage|' + window.location.host + '|' + navigator.userAgent);
      const bytes = new TextEncoder().encode(plaintext);
      let binary = '';
      for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i] ^ secret[i % secret.length]);
      return 'enc::v1::' + btoa(binary);
    };
  });
  const page = await context.newPage();
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(String(error)));

  await page.clock.install({ time: new Date('2026-08-23T12:00:00.000Z') });
  await page.goto(dashboardURL, { waitUntil: 'domcontentloaded' });

  // Phase A: with no stored credentials the auth page parks the dashboard and
  // fires zero management requests, no matter how long the visitor waits or how
  // often a stale refresh wave lands.
  const authView = page.locator('#authView');
  await authView.waitFor({ state: 'visible' });
  if (await page.locator('#appView').evaluate((node) => !node.classList.contains('hidden'))) {
    throw new Error('main dashboard must stay hidden while the auth page is up');
  }
  await page.clock.fastForward(11000);
  await page.evaluate(() => document.getElementById('refreshButton').click());
  await page.waitForTimeout(200);

  // Empty submit: prompt inside the auth card, still zero requests.
  await page.locator('#authSubmit').click();
  const emptyState = await page.evaluate(() => ({
    text: document.getElementById('authMessage').textContent,
    error: document.getElementById('authMessage').classList.contains('error'),
  }));
  if (emptyState.text !== EMPTY_MESSAGE || !emptyState.error) {
    throw new Error(`empty submit must prompt inside the auth card, got ${JSON.stringify(emptyState)}`);
  }

  // Wrong key (submitted via Enter): 401 must surface inside the auth card and
  // never paint the main-page error banner; the typed key must not persist.
  await page.locator('#authKeyInput').fill('wrong-key');
  await page.locator('#authKeyInput').press('Enter');
  await page.waitForTimeout(300);
  const wrongState = await page.evaluate(() => ({
    text: document.getElementById('authMessage').textContent,
    error: document.getElementById('authMessage').classList.contains('error'),
    viewHidden: document.getElementById('authView').hidden,
  }));
  if (wrongState.text !== INVALID_MESSAGE || !wrongState.error || wrongState.viewHidden) {
    throw new Error(`wrong key must show the invalid-key message inside the auth card, got ${JSON.stringify(wrongState)}`);
  }
  if (await page.evaluate(() => Boolean(sessionStorage.getItem('tokens-statistic-key')))) {
    throw new Error('a rejected key must not persist in per-tab storage');
  }

  // Correct key + remember credentials: verification runs first, then the
  // dashboard loads with the very key that was entered.
  await page.locator('#authRemember').check();
  const settled = Promise.all([
    page.waitForResponse((response) => new URL(response.url()).pathname === `${managementBase}/stats/initial`),
    page.waitForResponse((response) => new URL(response.url()).pathname === `${managementBase}/requests`),
    page.waitForResponse((response) => new URL(response.url()).pathname === `${managementBase}/stats/groups`),
  ]);
  await page.locator('#authKeyInput').fill(GOOD_KEY);
  await page.locator('#authSubmit').click();
  await settled;
  if (await authView.evaluate((node) => !node.hidden)) {
    throw new Error('auth page must disappear after successful verification');
  }
  if (await page.locator('#appView').evaluate((node) => node.classList.contains('hidden'))) {
    throw new Error('main dashboard must be revealed after successful verification');
  }
  const requestRows = await page.locator('#requestRows tr').count();
  if (requestRows < 1) {
    throw new Error(`request detail table must render a row after verification, got ${requestRows}`);
  }
  if (!(await page.locator('#requestRows').textContent()).includes('gate-browser-model')) {
    throw new Error('request detail table does not show the served request');
  }
  const errorText = (await page.locator('#error').textContent() || '').trim();
  if (errorText) {
    throw new Error(`dashboard reported an error after verification: ${errorText}`);
  }
  const remembered = await page.evaluate(() => localStorage.getItem('tokens-statistic-remembered-key') || '');
  if (!remembered.startsWith('enc::v1::')) {
    throw new Error('ticking remember credentials must store an obfuscated key, got empty or plaintext entry');
  }

  // While /api-key-info is still in flight the tracking state is unknown:
  // neither the disabled notice nor the filter may flash into view.
  const quietState = await page.evaluate(() => ({
    noticeHidden: document.getElementById('apiKeyTrackingState').hidden,
    filterHidden: document.getElementById('apiKeyFilter').hidden,
  }));
  if (!quietState.noticeHidden || !quietState.filterHidden) {
    throw new Error(`unknown tracking state must not render the disabled notice or the filter, got ${JSON.stringify(quietState)}`);
  }
  await page.waitForFunction(() => !document.getElementById('apiKeyFilter').hidden, null, { timeout: 5000 });
  const enabledState = await page.evaluate(() => ({
    noticeHidden: document.getElementById('apiKeyTrackingState').hidden,
    filterHidden: document.getElementById('apiKeyFilter').hidden,
  }));
  if (!enabledState.noticeHidden || enabledState.filterHidden) {
    throw new Error(`enabled tracking must show the filter and keep the notice hidden, got ${JSON.stringify(enabledState)}`);
  }

  // The filter caret is the CSS-drawn chevron shared with the enhanced selects:
  // 7px box at rest, flipped upward (rotate 225deg => negative cos) while the
  // menu is open.
  await page.locator('#apiKeyFilterButton').click();
  await page.waitForFunction(() => document.getElementById('apiKeyFilterButton').getAttribute('aria-expanded') === 'true', null, { timeout: 5000 });
  const caret = await page.evaluate(() => {
    const node = document.querySelector('.apikey-filter-caret');
    return { width: getComputedStyle(node).width, transform: getComputedStyle(node).transform };
  });
  if (caret.width !== '7px' || !caret.transform.includes('-0.7071')) {
    throw new Error(`open-menu caret must be the 7px CSS chevron flipped upward, got ${JSON.stringify(caret)}`);
  }
  await page.locator('#apiKeyFilterButton').click();
  await page.waitForFunction(() => document.getElementById('apiKeyFilterButton').getAttribute('aria-expanded') === 'false', null, { timeout: 5000 });

  // Phase B: remembered credentials survive a tab restart with no sessionStorage.
  await page.evaluate(() => sessionStorage.clear());
  host.resetLastAuthHeader();
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForResponse((response) => new URL(response.url()).pathname === `${managementBase}/stats/initial`);
  if (await page.locator('#authView').evaluate((node) => !node.hidden)) {
    throw new Error('remembered credentials must skip the auth page on the next visit');
  }
  if (host.lastAuthHeader() !== `Bearer ${GOOD_KEY}`) {
    throw new Error(`remembered key must be replayed verbatim, got authorization header "${host.lastAuthHeader()}"`);
  }

  // Phase C: a remembered key the host now rejects sends the visitor back to
  // the auth page with the invalid-key message and wipes the stored copy.
  await page.evaluate(() => {
    localStorage.setItem('tokens-statistic-remembered-key', window.__obf(JSON.stringify({ key: 'bad-stored-key' })));
    sessionStorage.clear();
  });
  host.resetLastAuthHeader();
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => {
    const view = document.getElementById('authView');
    return view && !view.hidden && document.getElementById('authMessage').classList.contains('error');
  });
  if (await page.locator('#appView').evaluate((node) => !node.classList.contains('hidden'))) {
    throw new Error('main dashboard must stay hidden after a rejected remembered key');
  }
  const invalidText = await page.evaluate(() => document.getElementById('authMessage').textContent);
  if (invalidText !== INVALID_MESSAGE) {
    throw new Error(`rejected remembered key must show the invalid-key message, got ${invalidText}`);
  }
  if (host.lastAuthHeader() !== 'Bearer bad-stored-key') {
    throw new Error(`the stored key must be tried against the host, got authorization header "${host.lastAuthHeader()}"`);
  }
  const leftover = await page.evaluate(() => localStorage.getItem('tokens-statistic-remembered-key'));
  if (leftover) {
    throw new Error('a rejected remembered key must be wiped from storage');
  }

  // Phase D: a stale key remembered by the CPA management center is rejected
  // with the dedicated center-credentials guidance; the rejection is also
  // recorded so later boots in this tab skip the center fallback.
  await page.evaluate(() => {
    localStorage.setItem('cli-proxy-auth', window.__obf(JSON.stringify({ managementKey: 'wrong-key' })));
    sessionStorage.clear();
  });
  host.resetLastAuthHeader();
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => {
    const view = document.getElementById('authView');
    return view && !view.hidden && document.getElementById('authMessage').classList.contains('error');
  });
  if (host.lastAuthHeader() !== 'Bearer wrong-key') {
    throw new Error(`the center-remembered key must be tried against the host, got authorization header "${host.lastAuthHeader()}"`);
  }
  const centerInvalidText = await page.evaluate(() => document.getElementById('authMessage').textContent);
  if (centerInvalidText !== CENTER_INVALID_MESSAGE) {
    throw new Error(`rejected center-remembered key must show the center-credentials guidance, got ${centerInvalidText}`);
  }

  if (pageErrors.length > 0) {
    throw new Error(`page errors: ${pageErrors.join('\n')}`);
  }
} finally {
  await browser.close();
  host.close();
}
