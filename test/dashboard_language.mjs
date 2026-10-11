import { chromium } from 'playwright-core';
import { readDashboardFile, startDashboardServer, authenticate, defaultFixtures, loadLocaleCatalogs } from './helpers.mjs';

const [htmlPath, chromePath, localesDir] = process.argv.slice(2);
if (!htmlPath || !chromePath || !localesDir) {
  throw new Error('usage: node test/dashboard_language.mjs <dashboard-html-path> <google-chrome-path> <locales-dir>');
}

const dashboardHTML = await readDashboardFile(htmlPath);
const GOOD_KEY = 'language-browser-key';

// Expected UI strings come straight from the shipped locale catalogs, so the
// assertions track the real translations instead of duplicated literals.
const localeSpecs = [
  { contextLocale: 'en-US', code: 'en', file: 'en.json' },
  { contextLocale: 'zh-CN', code: 'zh-CN', file: 'zh-CN.json' },
  { contextLocale: 'zh-TW', code: 'zh-TW', file: 'zh-TW.json' },
  { contextLocale: 'ru-RU', code: 'ru', file: 'ru.json' },
];
const catalogs = await loadLocaleCatalogs(localesDir, localeSpecs);

const host = startDashboardServer({
  dashboardHTML,
  pluginId: 'language-browser-test',
  fixtures: defaultFixtures({ model: 'language-browser-model' }),
});
const dashboardURL = await host.ready;
const browser = await chromium.launch({ executablePath: chromePath, headless: true });

try {
  for (const spec of localeSpecs) {
    const catalog = catalogs[spec.code];
    const context = await browser.newContext({ locale: spec.contextLocale, timezoneId: 'UTC' });
    const page = await context.newPage();
    const pageErrors = [];
    page.on('pageerror', (error) => pageErrors.push(String(error)));

    await page.clock.install({ time: new Date('2026-08-23T12:00:00.000Z') });
    await page.goto(dashboardURL, { waitUntil: 'domcontentloaded' });

    // Pre-auth: the locale picked from the emulated browser languages must
    // translate the auth card, and document.lang must name the locale.
    const preAuth = await page.evaluate(() => ({
      lang: document.documentElement.lang,
      verify: document.getElementById('authSubmit').textContent,
      heading: document.querySelector('#authView h2').textContent,
    }));
    if (preAuth.lang !== spec.code) {
      throw new Error(`[${spec.code}] document.lang = ${preAuth.lang}, want ${spec.code}`);
    }
    if (preAuth.verify !== catalog['gate.verify']) {
      throw new Error(`[${spec.code}] auth submit = ${JSON.stringify(preAuth.verify)}, want ${JSON.stringify(catalog['gate.verify'])}`);
    }
    if (preAuth.heading !== catalog['gate.heading']) {
      throw new Error(`[${spec.code}] auth heading = ${JSON.stringify(preAuth.heading)}, want ${JSON.stringify(catalog['gate.heading'])}`);
    }

    await authenticate(page, { managementBase: host.managementBase, key: GOOD_KEY });

    // Post-auth: static chrome, control labels, card labels and the dimension
    // table headers must all follow the same locale (the 0.1.2 re-render
    // regression guard).
    await page.waitForFunction(() => {
      const button = document.getElementById('pricingButton');
      return button && button.querySelector('.button-label') && button.querySelector('.button-label').textContent.trim() !== '';
    });
    const postAuth = await page.evaluate(() => {
      const headers = Array.from(document.querySelectorAll('#dimensionHeaders th')).map((node) => node.textContent);
      return {
        title: document.querySelector('#appView h1').textContent,
        pricing: document.querySelector('#pricingButton .button-label').textContent,
        apiKeyAll: document.getElementById('apiKeyFilterValue').textContent,
        totalCalls: document.querySelector('[data-i18n="card.totalCalls"]').textContent,
        headers,
      };
    });
    if (postAuth.title !== catalog['app.title']) {
      throw new Error(`[${spec.code}] app title = ${JSON.stringify(postAuth.title)}, want ${JSON.stringify(catalog['app.title'])}`);
    }
    if (postAuth.pricing !== catalog['button.pricing']) {
      throw new Error(`[${spec.code}] pricing label = ${JSON.stringify(postAuth.pricing)}, want ${JSON.stringify(catalog['button.pricing'])}`);
    }
    if (postAuth.apiKeyAll !== catalog['apiKey.all']) {
      throw new Error(`[${spec.code}] api key filter = ${JSON.stringify(postAuth.apiKeyAll)}, want ${JSON.stringify(catalog['apiKey.all'])}`);
    }
    if (postAuth.totalCalls !== catalog['card.totalCalls']) {
      throw new Error(`[${spec.code}] total calls label = ${JSON.stringify(postAuth.totalCalls)}, want ${JSON.stringify(catalog['card.totalCalls'])}`);
    }
    const wantModelHeader = catalog['table.model'];
    if (!postAuth.headers.includes(wantModelHeader)) {
      throw new Error(`[${spec.code}] dimension headers ${JSON.stringify(postAuth.headers)} must include ${JSON.stringify(wantModelHeader)}`);
    }
    if (pageErrors.length) {
      throw new Error(`[${spec.code}] page errors: ${pageErrors.join(' | ')}`);
    }

    await context.close();
  }
} finally {
  await browser.close();
  host.close();
}
