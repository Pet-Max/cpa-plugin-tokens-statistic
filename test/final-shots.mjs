import { createRequire } from 'module';
import { pathToFileURL } from 'url';
import { execSync } from 'child_process';
const require = createRequire(import.meta.url);
const { chromium } = require('playwright-core');

const helpersURL = pathToFileURL('D:/Claude/Tokens 用量统计/cpa-plugin-tokens-statistic/tokens-statistic-v0.2.3/test/helpers.mjs').href;
const { readDashboardFile, startDashboardServer } = await import(helpersURL);

const htmlPath = 'D:/Claude/Tokens 用量统计/cpa-plugin-tokens-statistic/tokens-statistic-v0.2.3/internal/plugin/web/dashboard.html';
const outDir = 'D:/Claude/Tokens 用量统计/cpa-plugin-tokens-statistic/tokens-statistic-v0.2.3/design-preview/final';
const chrome = 'C:/Program Files/Google/Chrome/Application/chrome.exe';

const html = (await readDashboardFile(htmlPath)).toString().replace('/*LOCALE_PLACEHOLDER*/', JSON.stringify({
  'zh-CN': JSON.parse((await import('fs')).readFileSync('D:/Claude/Tokens 用量统计/cpa-plugin-tokens-statistic/tokens-statistic-v0.2.3/internal/plugin/web/locales/zh-CN.json', 'utf-8')),
  'zh-TW': JSON.parse((await import('fs')).readFileSync('D:/Claude/Tokens 用量统计/cpa-plugin-tokens-statistic/tokens-statistic-v0.2.3/internal/plugin/web/locales/zh-TW.json', 'utf-8')),
  en: JSON.parse((await import('fs')).readFileSync('D:/Claude/Tokens 用量统计/cpa-plugin-tokens-statistic/tokens-statistic-v0.2.3/internal/plugin/web/locales/en.json', 'utf-8')),
  ru: JSON.parse((await import('fs')).readFileSync('D:/Claude/Tokens 用量统计/cpa-plugin-tokens-statistic/tokens-statistic-v0.2.3/internal/plugin/web/locales/ru.json', 'utf-8')),
}));

const server = startDashboardServer({ dashboardHTML: html, pluginId: 'test' });
const dashboardURL = await server.ready;
const browser = await chromium.launch({ executablePath: chrome, headless: true });
const context = await browser.newContext({ locale: 'zh-CN', viewport: { width: 1280, height: 950 }, deviceScaleFactor: 2 });
await context.addInitScript(() => { sessionStorage.setItem('tokens-statistic-key', 'final-key'); });
const page = await context.newPage();
const pageErrors = [];
page.on('pageerror', (error) => pageErrors.push(String(error)));

await page.goto(dashboardURL, { waitUntil: 'domcontentloaded' });
await page.waitForFunction(() => document.querySelectorAll('.health-dot').length === 72);
await page.waitForTimeout(700);

const panel = page.locator('#healthPanel');

// 1. light overview + panel
await page.screenshot({ path: outDir + '/f1-light-full.png', fullPage: true });
await panel.screenshot({ path: outDir + '/f2-light-panel.png' });

// 2. hover failed call: tooltip below the dot
await page.hover('.health-dots[data-window="recent"] .health-dot[data-slot="6"]');
await page.waitForTimeout(350);
await panel.screenshot({ path: outDir + '/f3-tooltip-below.png' });
await page.mouse.move(20, 20);
await page.waitForTimeout(250);

// 3. hover a 24d row dot (bottom row): tooltip below, floating over next section
await page.hover('.health-dots[data-window="days"] .health-dot[data-index="10"]');
await page.waitForTimeout(350);
await page.screenshot({ path: outDir + '/f4-tooltip-24d.png', clip: await page.locator('#healthPanel').boundingBox().then((b) => ({ x: b.x, y: b.y, width: b.width, height: Math.min(b.height + 240, 700) })) });
await page.mouse.move(20, 20);
await page.waitForTimeout(250);

// 4. dark theme
await page.evaluate(() => {
  const root = document.documentElement;
  root.setAttribute('data-theme', 'dark');
  root.style.backgroundColor = '#151412';
  root.style.colorScheme = 'dark';
});
await page.waitForTimeout(300);
await panel.screenshot({ path: outDir + '/f5-dark-panel.png' });
await page.evaluate(() => {
  const root = document.documentElement;
  root.removeAttribute('data-theme');
  root.style.backgroundColor = '#faf9f5';
  root.style.colorScheme = 'light';
});

// 5. bubble GIF on the hero bar (light)
const scrub = await page.locator('.health-scrub').boundingBox();
for (let i = 0; i < 16; i++) {
  await page.screenshot({ path: `C:/Users/LEGION/AppData/Local/Temp/vid/fb${String(i).padStart(2, '0')}.png`, clip: scrub });
  await page.waitForTimeout(220);
}
execSync('ffmpeg -y -loglevel error -framerate 5 -i C:/Users/LEGION/AppData/Local/Temp/vid/fb%02d.png -vf "split[a][b];[a]palettegen[p];[b][p]paletteuse" -loop 0 "D:/Claude/Tokens 用量统计/cpa-plugin-tokens-statistic/tokens-statistic-v0.2.3/design-preview/final/bubbles15.gif"', { stdio: 'inherit' });

// 6. narrow 600
await page.setViewportSize({ width: 600, height: 950 });
await page.waitForTimeout(400);
await panel.screenshot({ path: outDir + '/f6-narrow600.png' });

await browser.close();
await server.close();
console.log('FINAL SHOTS DONE, pageErrors:', pageErrors.length ? pageErrors.join(' | ') : 'none');
