import { chromium } from 'playwright-core';
import { readDashboardFile, startDashboardServer, authenticate, defaultFixtures, loadLocaleCatalogs } from './helpers.mjs';

const [htmlPath, chromePath, localesDir] = process.argv.slice(2);
if (!htmlPath || !chromePath || !localesDir) {
  throw new Error('usage: node test/dashboard_embedded.mjs <dashboard-html-path> <google-chrome-path> <locales-dir>');
}

const dashboardHTML = await readDashboardFile(htmlPath);
const GOOD_KEY = 'embedded-browser-key';
const PARENT_BACKGROUND = '#abcdef';

const catalogs = await loadLocaleCatalogs(localesDir, [
  { code: 'zh-TW', file: 'zh-TW.json' },
  { code: 'zh-CN', file: 'zh-CN.json' },
]);

const host = startDashboardServer({
  dashboardHTML,
  pluginId: 'embedded-browser-test',
  fixtures: defaultFixtures({ model: 'embedded-browser-model' }),
  rawHandler: (request, response, url) => {
    // The management-center parent page: same origin, locale and theme set on
    // the root element, the dashboard mounted in an iframe.
    if (url.pathname === '/parent') {
      const embed = url.searchParams.get('embed') ?? '';
      response.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      response.end(`<!doctype html>
<html lang="zh-TW" data-theme="white">
<head><meta charset="utf-8"><style>:root{--bg-secondary:${PARENT_BACKGROUND}}</style></head>
<body><iframe id="dash" src="${embed}" style="width:1200px;height:800px;border:0"></iframe></body>
</html>`);
      return true;
    }
    return false;
  },
});
const dashboardURL = await host.ready;
const origin = new URL(dashboardURL).origin;
const parentURL = `${origin}/parent?embed=${encodeURIComponent(dashboardURL)}`;
const browser = await chromium.launch({ executablePath: chromePath, headless: true });

try {
  // The emulated navigator locale is en-US on purpose: without the parent
  // integration the page would resolve to 'en'; the parent page must win.
  const context = await browser.newContext({ locale: 'en-US', timezoneId: 'UTC' });
  const page = await context.newPage();
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(String(error.stack || error).slice(0, 500)));
    page.on('console', (msg) => { if (msg.type() === 'error') pageErrors.push('console@' + (msg.location()?.url || '') + ':' + (msg.location()?.lineNumber ?? 0) + ' ' + msg.text().slice(0, 300)); });

  await page.clock.install({ time: new Date('2026-08-23T12:00:00.000Z') });
  await page.goto(parentURL, { waitUntil: 'domcontentloaded' });

  const dashFrame = () => page.frames().find((frame) => frame !== page.mainFrame());
  const frameEl = page.frameLocator('#dash');
  if (!dashFrame()) {
    throw new Error('dashboard iframe did not mount inside the parent page');
  }

  // Initial sync: the parent's lang/theme/background must override the
  // emulated navigator locale and the built-in dark theme.
  await frameEl.locator('#authView').waitFor({ state: 'visible' });
  const initial = await dashFrame().evaluate(() => ({
    lang: document.documentElement.lang,
    theme: document.documentElement.getAttribute('data-theme'),
    background: document.documentElement.style.backgroundColor,
    verify: document.getElementById('authSubmit').textContent,
  }));
  if (initial.lang !== 'zh-TW') {
    throw new Error(`iframe lang = ${initial.lang}, want the parent's zh-TW`);
  }
  if (initial.theme !== 'white') {
    throw new Error(`iframe theme = ${initial.theme}, want the parent's white`);
  }
  if (initial.background !== 'rgb(171, 205, 239)') {
    throw new Error(`iframe background = ${initial.background}, want the parent's --bg-secondary (rgb(171, 205, 239))`);
  }
  if (initial.verify !== catalogs['zh-TW']['gate.verify']) {
    throw new Error(`iframe auth submit = ${JSON.stringify(initial.verify)}, want the zh-TW translation`);
  }

  // Authenticate inside the iframe.
  await authenticate(page, { managementBase: host.managementBase, key: GOOD_KEY, frame: dashFrame() });

  // Dynamic follow: mutating the parent's locale and theme at runtime must be
  // picked up by the dashboard's MutationObserver (the management-center
  // language/theme switching contract).
  await page.evaluate(() => {
    document.documentElement.lang = 'zh-CN';
    document.documentElement.setAttribute('data-theme', 'dark');
  });
  await dashFrame().waitForFunction(
    () => document.documentElement.lang === 'zh-CN' && document.documentElement.getAttribute('data-theme') === 'dark',
    undefined, { timeout: 5000 },
  );
  await frameEl.locator('#pricingButton').waitFor({ state: 'visible' });
  const after = await frameEl.locator('#appView').evaluate((app) => ({
    title: app.querySelector('h1').textContent,
    pricing: app.querySelector('#pricingButton .button-label').textContent,
    theme: document.documentElement.getAttribute('data-theme'),
    lang: document.documentElement.lang,
  }));
  if (after.lang !== 'zh-CN' || after.theme !== 'dark') {
    throw new Error(`iframe did not follow the parent mutation: ${JSON.stringify(after)}`);
  }
  if (after.title !== catalogs['zh-CN']['app.title']) {
    throw new Error(`iframe title = ${JSON.stringify(after.title)}, want the zh-CN translation`);
  }
  if (after.pricing !== catalogs['zh-CN']['button.pricing']) {
    throw new Error(`iframe pricing label = ${JSON.stringify(after.pricing)}, want the zh-CN translation`);
  }

  if (pageErrors.length) {
    throw new Error(`page errors: ${pageErrors.join(' | ')}`);
  }
} finally {
  await browser.close();
  host.close();
}
