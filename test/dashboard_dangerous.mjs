import { chromium } from 'playwright-core';
import { readDashboardFile, startDashboardServer, authenticate, defaultFixtures, installExportCapture, waitForExports, readCapturedExports } from './helpers.mjs';

const [htmlPath, chromePath] = process.argv.slice(2);
if (!htmlPath || !chromePath) {
  throw new Error('usage: node test/dashboard_dangerous.mjs <dashboard-html-path> <google-chrome-path>');
}

const dashboardHTML = await readDashboardFile(htmlPath);
const GOOD_KEY = 'dangerous-browser-key';
const FAKE_DB = Buffer.from('BOLTDB-FAKE-BACKUP-BYTES-FOR-BROWSER-TEST');

const populatedFixtures = defaultFixtures({ model: 'dangerous-browser-model' });
const emptyFixtures = {
  statsInitial: { generated_at: '2026-08-23T12:00:00.000Z', last_used: '0001-01-01T00:00:00.000Z', models: [], sources: [], bucket_seconds: 86400 },
  statsTrends: { model_series: [], bucket_seconds: 86400 },
  statsGroups: { items: [], total: 0 },
  requests: { generated_at: '2026-08-23T12:00:00.000Z', range: '24h', total: 0, offset: 0, limit: 100, items: [] },
  costs: { summary: { requests: 0, priced_requests: 0, unpriced_requests: 0 }, models: [], price_book_revision: 0 },
};

const captured = { backupAuth: '', resetBody: null, restoreBody: null, restoreConfirmHeader: '' };
let serveEmpty = false;

const host = startDashboardServer({
  dashboardHTML,
  pluginId: 'dangerous-browser-test',
  fixtures: populatedFixtures,
  rawHandler: (request, response, url) => {
    // GET /backup: binary download carrying the Content-Disposition filename.
    if (url.pathname === `${host.managementBase}/backup` && request.method === 'GET') {
      captured.backupAuth = request.headers.authorization || '';
      response.writeHead(200, {
        'content-type': 'application/octet-stream',
        'content-disposition': 'attachment; filename="tokens-statistic-20260823.db"',
      });
      response.end(FAKE_DB);
      return true;
    }
    // POST /reset: the dashboard must send {"confirm":"reset"} in the body.
    if (url.pathname === `${host.managementBase}/reset` && request.method === 'POST') {
      let body = '';
      request.setEncoding('utf8');
      request.on('data', (chunk) => { body += chunk; });
      request.on('end', () => {
        captured.resetBody = body;
        serveEmpty = true;
        host.updateFixtures(emptyFixtures);
        sendJSONValue(response, { reset: true, reset_at: '2026-08-23T12:00:00.000Z' });
      });
      return true;
    }
    // POST /restore: the uploaded bytes must come back with the confirm header.
    if (url.pathname === `${host.managementBase}/restore` && request.method === 'POST') {
      captured.restoreConfirmHeader = request.headers['x-confirm-restore'] || '';
      const chunks = [];
      request.on('data', (chunk) => chunks.push(Buffer.from(chunk)));
      request.on('end', () => {
        captured.restoreBody = Buffer.concat(chunks).toString('utf8');
        serveEmpty = false;
        host.updateFixtures(populatedFixtures);
        sendJSONValue(response, { restored: true, restored_at: '2026-08-23T12:00:00.000Z' });
      });
      return true;
    }
    return false;
  },
});

const sendJSONValue = (response, value) => {
  response.writeHead(200, { 'content-type': 'application/json' });
  response.end(JSON.stringify(value));
};

const dashboardURL = await host.ready;
const browser = await chromium.launch({ executablePath: chromePath, headless: true });

try {
  const context = await browser.newContext({ locale: 'zh-CN', timezoneId: 'UTC' });
  const page = await context.newPage();
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(String(error)));
  // The three flows raise native confirm()/prompt() dialogs; answer them the
  // way a user confirming a dangerous operation would.
  page.on('dialog', (dialog) => {
    if (dialog.type() === 'prompt') {
      void dialog.accept('reset');
    } else {
      void dialog.accept();
    }
  });

  installExportCapture(context);
  await page.clock.install({ time: new Date('2026-08-23T12:00:00.000Z') });
  await page.goto(dashboardURL, { waitUntil: 'domcontentloaded' });

  await authenticate(page, { managementBase: host.managementBase, key: GOOD_KEY });
  await page.waitForFunction(() => {
    const node = document.getElementById('totalCalls');
    return node && node.textContent !== '—' && node.textContent !== '0';
  }, undefined, { timeout: 5000 });

  // ── Backup: the export menu's backup item must download the .db blob the
  // host produced, named from the Content-Disposition header.
  await page.locator('#exportButton').click();
  await page.locator('#exportMenu').waitFor({ state: 'visible' });
  await page.locator('#exportBackup').click();
  await waitForExports(page, 1);
  const backupExports = await readCapturedExports(page);
  if (backupExports.length !== 1) {
    throw new Error(`backup must export exactly one file, got ${backupExports.length}`);
  }
  if (!/^tokens-statistic-.*\.db$/.test(backupExports[0].name || '')) {
    throw new Error(`backup filename mismatch: ${JSON.stringify(backupExports[0])}`);
  }
  if (backupExports[0].size !== FAKE_DB.length) {
    throw new Error(`backup blob size mismatch: ${backupExports[0].size} vs ${FAKE_DB.length}`);
  }
  if (!captured.backupAuth.startsWith('Bearer ')) {
    throw new Error(`backup must send the management key: ${JSON.stringify(captured.backupAuth)}`);
  }

  // ── Reset: confirm() + prompt('reset') must POST {"confirm":"reset"} and the
  // dashboard must repaint with the emptied statistics.
  const resetRequested = page.waitForResponse((response) => new URL(response.url()).pathname === `${host.managementBase}/reset`);
  await page.locator('#resetButton').click();
  await resetRequested;
  if (captured.resetBody === null || JSON.parse(captured.resetBody).confirm !== 'reset') {
    throw new Error(`reset body mismatch: ${JSON.stringify(captured.resetBody)}`);
  }
  await page.waitForFunction(() => document.getElementById('totalCalls').textContent === '0', undefined, { timeout: 5000 });

  // ── Restore: uploading the previously captured backup must POST it with the
  // confirm header and bring the statistics back.
  await page.locator('#exportButton').click();
  await page.locator('#exportMenu').waitFor({ state: 'visible' });
  const restoreRequested = page.waitForResponse((response) => new URL(response.url()).pathname === `${host.managementBase}/restore`);
  const fileChooserPromise = page.waitForEvent('filechooser');
  await page.locator('#restoreBackup').click();
  const chooser = await fileChooserPromise;
  await chooser.setFiles({ name: 'restore-test.db', mimeType: 'application/octet-stream', buffer: FAKE_DB });
  await restoreRequested;
  if (captured.restoreConfirmHeader !== 'replace') {
    throw new Error(`restore must send X-Confirm-Restore: replace, got ${JSON.stringify(captured.restoreConfirmHeader)}`);
  }
  if (captured.restoreBody !== FAKE_DB.toString('utf8')) {
    throw new Error(`restore body mismatch: ${JSON.stringify(captured.restoreBody.slice(0, 120))}`);
  }
  await page.waitForFunction(() => {
    const node = document.getElementById('totalCalls');
    return node && node.textContent !== '—' && node.textContent !== '0';
  }, undefined, { timeout: 5000 });

  if (pageErrors.length) {
    throw new Error(`page errors: ${pageErrors.join(' | ')}`);
  }
} finally {
  await browser.close();
  host.close();
}
