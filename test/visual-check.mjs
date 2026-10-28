import { createRequire } from 'module';
import { pathToFileURL } from 'url';
const require = createRequire(import.meta.url);
const { chromium } = require('playwright-core');

const helpersURL = pathToFileURL('D:/Claude/Tokens 用量统计/cpa-plugin-tokens-statistic/tokens-statistic-v0.3.4/test/helpers.mjs').href;
const { readDashboardFile, startDashboardServer, healthFixtures } = await import(helpersURL);

const htmlPath = 'D:/Claude/Tokens 用量统计/cpa-plugin-tokens-statistic/tokens-statistic-v0.3.4/internal/plugin/web/dashboard.html';
const outDir = 'C:/Users/LEGION/AppData/Local/Temp/hp-preview/shots';
const chrome = 'C:/Program Files/Google/Chrome/Application/chrome.exe';

const html = (await readDashboardFile(htmlPath)).toString().replace('/*LOCALE_PLACEHOLDER*/', JSON.stringify({
  'zh-CN': JSON.parse((await import('fs')).readFileSync('D:/Claude/Tokens 用量统计/cpa-plugin-tokens-statistic/tokens-statistic-v0.3.4/internal/plugin/web/locales/zh-CN.json', 'utf-8')),
  'zh-TW': JSON.parse((await import('fs')).readFileSync('D:/Claude/Tokens 用量统计/cpa-plugin-tokens-statistic/tokens-statistic-v0.3.4/internal/plugin/web/locales/zh-TW.json', 'utf-8')),
  en: JSON.parse((await import('fs')).readFileSync('D:/Claude/Tokens 用量统计/cpa-plugin-tokens-statistic/tokens-statistic-v0.3.4/internal/plugin/web/locales/en.json', 'utf-8')),
  ru: JSON.parse((await import('fs')).readFileSync('D:/Claude/Tokens 用量统计/cpa-plugin-tokens-statistic/tokens-statistic-v0.3.4/internal/plugin/web/locales/ru.json', 'utf-8')),
}));

const server = startDashboardServer({ dashboardHTML: html, pluginId: 'test' });
const dashboardURL = await server.ready;
const browser = await chromium.launch({ executablePath: chrome, headless: true });
const results = [];
const check = (name, ok) => results.push((ok ? 'PASS ' : 'FAIL ') + name);

const context = await browser.newContext({ locale: 'zh-CN', viewport: { width: 1280, height: 950 }, deviceScaleFactor: 2 });
await context.addInitScript(() => { sessionStorage.setItem('tokens-statistic-key', 'visual-check-key'); });
const page = await context.newPage();
const pageErrors = [];
page.on('pageerror', (error) => pageErrors.push(String(error)));

await page.goto(dashboardURL, { waitUntil: 'domcontentloaded' });
const healthRequestPromise = page.waitForRequest((r) => new URL(r.url()).pathname.endsWith('/stats/health'));
await page.waitForResponse((r) => new URL(r.url()).pathname.endsWith('/stats/health'));
await page.waitForFunction(() => document.querySelectorAll('.health-dot').length === 72);
await page.waitForTimeout(600);

check('no page errors on load', pageErrors.length === 0);
const healthRequestUrl = new URL((await healthRequestPromise).url());
check('health request carries tz offset', healthRequestUrl.searchParams.has('tz') && Number(healthRequestUrl.searchParams.get('tz')) === -new Date().getTimezoneOffset());
check('72 dots rendered (24 calls + 24h + 24d)', await page.locator('.health-dot').count() === 72);
const rowLabels = await page.locator('#healthRows .health-row-label').allTextContents();
check('row labels Live/24h/24d', JSON.stringify(rowLabels) === JSON.stringify(['实时', '24h', '24d']));
const latestLabel = await page.locator('.health-scrub .health-row-label').textContent();
check('latest row label present', latestLabel === '最近');
check('hero rate 95.8% in pct column', (await page.locator('#healthHeroRate').textContent()) === '95.8%' && (await page.locator('#healthHeroRate.health-pct.is-good').count()) === 1);
check('hero fill green', await page.locator('#healthFill.is-good').count() === 1);
check('failed call dot red at slot 6', await page.locator('.health-dots[data-window="recent"] .health-dot[data-slot="6"].is-bad').count() === 1);
check('no hollow slots with full history', await page.locator('.health-dots[data-window="recent"] .health-dot.is-empty').count() === 0);
check('today dot translucent', await page.locator('.health-dots[data-window="days"] .health-dot[data-index="23"].is-partial').count() === 1);
check('degraded day bucket dot present', await page.locator('.health-dots[data-window="day"] .health-dot.is-warn').count() >= 1);
check('two-line subtitle without filters', !(await page.locator('#healthScope').isHidden()) && (await page.locator('#healthScope').textContent()).includes('24') && (await page.locator('#healthScope').textContent()).includes('时间范围'));
check('bottom range note removed', (await page.locator('#healthRangeNote').count()) === 0);
check('legacy scrubber chrome removed', await page.evaluate(() => !document.getElementById('healthRange') && !document.getElementById('healthRead') && !document.getElementById('healthLatestButton')));
check('hero bubbles = 30', await page.locator('#healthFill b').count() === 30);
const dotWidths = await page.evaluate(() => Array.from(document.querySelectorAll('.health-dots[data-window="recent"] .health-dot')).map((el) => el.getBoundingClientRect().width));
check('capsules stretch evenly (>=30px @1280)', dotWidths.every((w) => w >= 30) && Math.max(...dotWidths) - Math.min(...dotWidths) < 1.5);
const alignCheck = await page.evaluate(() => {
  const lane = document.querySelector('.health-dots[data-window="recent"]').getBoundingClientRect();
  const track = document.querySelector('.health-track').getBoundingClientRect();
  return { laneLeft: +lane.left.toFixed(1), trackLeft: +track.left.toFixed(1), laneRight: +lane.right.toFixed(1), trackRight: +track.right.toFixed(1) };
});
check('hero bar aligns with capsule lane', Math.abs(alignCheck.laneLeft - alignCheck.trackLeft) < 2 && Math.abs(alignCheck.laneRight - alignCheck.trackRight) < 2);

await page.screenshot({ path: outDir + '/real-1-overview.png', fullPage: true });
await page.locator('#healthPanel').screenshot({ path: outDir + '/real-2-panel.png' });

// hover the failed call: per-call tooltip
await page.hover('.health-dots[data-window="recent"] .health-dot[data-slot="6"]');
await page.waitForTimeout(350);
check('per-call tooltip visible', await page.locator('#healthTip.is-on').count() === 1);
const tipPos = await page.evaluate(() => {
  const tip = document.getElementById('healthTip').getBoundingClientRect();
  const dot = document.querySelector('.health-dots[data-window="recent"] .health-dot[data-slot="6"]').getBoundingClientRect();
  return { tipTop: +tip.top.toFixed(1), dotBottom: +dot.bottom.toFixed(1), dotTop: +dot.top.toFixed(1), tipBottom: +tip.bottom.toFixed(1) };
});
check('tooltip sits beside the hovered dot (below or above)', tipPos.tipTop >= tipPos.dotBottom - 3 || tipPos.tipBottom <= tipPos.dotTop + 3);
const callTip = await page.locator('#healthTip').textContent();
const callOrder = ['模型', '结果', '时间', 'Tokens', '延迟', '首字', '缓存率'].map((s) => callTip.indexOf(s));
check('per-call tooltip fields in order with cache rate', callOrder.every((i) => i >= 0) && callOrder.every((i, k) => k === 0 || i > callOrder[k - 1]) && callTip.includes('失败'));
check('one-line subtitle', (await page.locator('#healthScope').textContent()).includes('·'));
await page.screenshot({ path: outDir + '/real-3-call-tooltip.png', clip: await page.locator('#healthPanel').boundingBox() });
await page.mouse.move(20, 20);
await page.waitForTimeout(250);
check('tooltip hides on leave', await page.locator('#healthTip.is-on').count() === 0);

// hover a 24h bucket dot: bucket tooltip must NOT show the model row visually
await page.hover('.health-dots[data-window="day"] .health-dot[data-index="8"]');
await page.waitForTimeout(350);
const bucketTip = await page.locator('#healthTip').textContent();
check('bucket tooltip shows success rate', bucketTip.includes('成功率') && bucketTip.includes('总调用'));
const bucketModelRow = await page.evaluate(() => {
  const row = document.getElementById('healthTipModelRow');
  return { hidden: row.hidden, display: getComputedStyle(row).display, visible: row.offsetParent !== null };
});
check('model row truly hidden on bucket tooltip', bucketModelRow.hidden && bucketModelRow.display === 'none' && !bucketModelRow.visible);
await page.hover('.health-dots[data-window="recent"] .health-dot[data-slot="6"]');
await page.waitForTimeout(300);
const recentModelRow = await page.evaluate(() => {
  const row = document.getElementById('healthTipModelRow');
  return row ? { hidden: row.hidden, display: getComputedStyle(row).display, visible: row.offsetParent !== null } : null;
});
check('model row restored for per-call tooltip', recentModelRow && !recentModelRow.hidden && recentModelRow.display === 'flex' && recentModelRow.visible);
await page.mouse.move(20, 20);
await page.waitForTimeout(250);

const noteHiddenDefault = await page.locator('#healthRangeNote').isHidden();


// empty-hour state: swap fixtures to zero calls, reload, verify the stale-free empty state
const emptyHealth = { ...healthFixtures(), calls: [] };
await server.updateFixtures({ health: emptyHealth });
await page.reload({ waitUntil: 'domcontentloaded' });
await page.waitForFunction(() => document.querySelectorAll('.health-dot').length === 72);
await page.waitForTimeout(400);
check('empty hour: hero rate dash', (await page.locator('#healthHeroRate').textContent()) === '—');
check('empty hour: note visible', await page.locator('#healthEmptyNote').isVisible() && (await page.locator('#healthEmptyNote').textContent()).includes('1'));
check('empty hour: fill empty class', await page.locator('#healthFill.is-empty').count() === 1);
check('empty hour: recent row all hollow', await page.locator('.health-dots[data-window="recent"] .health-dot.is-empty').count() === 24);
await page.hover('.health-dots[data-window="recent"] .health-dot[data-slot="0"]');
await page.waitForTimeout(300);
check('empty slot tooltip shows no-call card', await page.locator('#healthTip.is-on').count() === 1 && (await page.locator('#healthTip').textContent()).includes('无调用'));
await page.screenshot({ path: outDir + '/real-7-empty-slot-tooltip.png', clip: await page.locator('#healthPanel').boundingBox() });
await page.mouse.move(20, 20);
await page.waitForTimeout(250);
await page.hover('.health-dots[data-window="day"] .health-dot[data-index="8"]');
await page.waitForTimeout(300);
const bucketAfterEmpty = await page.evaluate(() => {
  const rows = Array.from(document.querySelectorAll('#healthTip .health-tip-row'));
  return { visible: rows.filter((r) => !r.hidden).length, modelHidden: rows[0].hidden, bare: document.getElementById('healthTip').classList.contains('is-bare') };
});
check('rows restored after empty-slot hover', bucketAfterEmpty.visible === 6 && bucketAfterEmpty.modelHidden && !bucketAfterEmpty.bare);
await page.mouse.move(20, 20);
await page.waitForTimeout(250);
await page.screenshot({ path: outDir + '/real-6-empty-hour.png', fullPage: true });
await server.updateFixtures({ health: healthFixtures() });
await page.reload({ waitUntil: 'domcontentloaded' });
await page.waitForFunction(() => document.querySelectorAll('.health-dot').length === 72);
await page.waitForTimeout(400);

// display settings: health section can be hidden and restored
await page.click('#displaySettingsButton');
await page.waitForTimeout(300);
const healthBox = page.locator('#displaySectionHealth');
check('display settings lists Service health', await healthBox.count() === 1 && await healthBox.isChecked());
await healthBox.uncheck();
await page.waitForTimeout(300);
check('unchecking hides the health panel', await page.locator('#healthPanel').isHidden());
await healthBox.check();
await page.waitForTimeout(300);
check('rechecking restores the health panel', await page.locator('#healthPanel').isVisible());
await page.keyboard.press('Escape');
await page.waitForTimeout(200);

for (const width of [900, 600]) {
  await page.setViewportSize({ width, height: 950 });
  await page.waitForTimeout(400);
  check(`no horizontal overflow at ${width}`, await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 2));
  await page.screenshot({ path: `${outDir}/real-5-width-${width}.png`, fullPage: true });
}

await browser.close();
await server.close();
console.log(results.join('\n'));
if (pageErrors.length) console.log('PAGE ERRORS:', pageErrors.join(' | '));
console.log(results.every((r) => r.startsWith('PASS')) ? 'VISUAL CHECK ALL PASS' : 'VISUAL CHECK FAILURES');
