import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { chromium } from 'playwright-core';

const [htmlA, htmlB, chromePath, outDir] = process.argv.slice(2);

const model = (name, tokens, requests, failed) => ({
  model: name, provider: 'openai', requests, failed_requests: failed,
  input_tokens: tokens, output_tokens: Math.round(tokens * 0.6), reasoning_tokens: 0,
  cache_read_tokens: Math.round(tokens * 0.4), cache_creation_tokens: 0, total_tokens: Math.round(tokens * 1.6),
  total_latency_ns: requests * 11343000000, ttft_samples: requests, total_ttft_ns: requests * 11319000000,
  latency_samples: requests,
});

const initial = {
  generated_at: '2026-10-04T12:00:00.000Z', last_used: '2026-10-04T12:00:00.000Z',
  models: [
    model('gpt-5.6-sol-fast', 32000000, 10, 3),
    model('claude-4.6-thinking', 21000000, 7, 1),
    model('gemini-3-pro-preview', 9000000, 3, 0),
  ],
  sources: [], bucket_seconds: 86400,
};

const server = createServer((request, response) => {
  const url = new URL(request.url, 'http://127.0.0.1');
  const send = (v) => { response.writeHead(200, { 'content-type': 'application/json' }); response.end(JSON.stringify(v)); };
  if (url.pathname.endsWith('/dashboard')) {
    response.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
    response.end(currentHTML);
    return;
  }
  if (url.pathname.endsWith('/api-key-info')) return send({ api_key_tracking_enabled: true, api_key_uses_default_secret: false, api_key_labels: {} });
  if (url.pathname.endsWith('/preferences')) return send({});
  if (url.pathname.endsWith('/stats/initial')) return send(initial);
  if (url.pathname.endsWith('/stats/trends')) return send({ model_series: [], bucket_seconds: 86400 });
  if (url.pathname.endsWith('/stats/groups') || url.pathname.endsWith('/requests')) return send({ items: [], total: 0 });
  if (url.pathname.endsWith('/costs')) return send({ summary: { requests: 20, priced_requests: 20, unpriced_requests: 0, input_usd: 1.2, output_usd: 3.4, cache_read_usd: 0.4, cache_creation_usd: 0, total_usd: 5.0 }, models: [], price_book_revision: 35 });
  if (url.pathname.endsWith('/prices')) return send({ prices: {}, revision: 35, sync_settings: {} });
  send({});
});

let currentHTML = '';
await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
const port = server.address().port;

const browser = await chromium.launch({ executablePath: chromePath, headless: true });
try {
  for (const [name, file] of [['variant_d8', htmlA], ['variant_d6', htmlB]]) {
    for (const theme of ['dark', 'white']) {
      currentHTML = await readFile(file, 'utf-8');
      const context = await browser.newContext({ locale: 'zh-CN', timezoneId: 'UTC', viewport: { width: 1560, height: 760 }, deviceScaleFactor: 2 });
      await context.addInitScript(() => sessionStorage.setItem('tokens-statistic-key', 'preview-key'));
      const page = await context.newPage();
      await page.goto(`http://127.0.0.1:${port}/v0/resource/plugins/preview/dashboard`, { waitUntil: 'domcontentloaded' });
      await page.waitForResponse((r) => new URL(r.url()).pathname.endsWith('/stats/initial'));
      await page.waitForTimeout(600);
      await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), theme);
      await page.waitForTimeout(150);
      await page.locator('.cards').screenshot({ path: `${outDir}/${name}_${theme}.png` });
      await context.close();
    }
  }
  console.log('previews done');
} finally {
  await browser.close();
  server.close();
}
