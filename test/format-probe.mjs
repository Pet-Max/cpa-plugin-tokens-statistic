#!/usr/bin/env node
// format-probe.mjs — dashboard 时间显示格式探测脚本
//
// 目的：把 dashboard.html 各时间显示点在不同语言下的真实输出打成一张表，作为
//       "某个语言界面里时间显示成什么"的唯一事实来源（规约见工作区根目录 AGENTS.md）。
//       格式化函数直接从 dashboard.html 源码按行提取——源码怎么改，探测就怎么跑，不会漂移；
//       每个显示点还带源码锚点校验：锚点数量对不上，说明 dashboard 相关代码被改动，
//       本表需要人工核对后更新探针。
//
// 运行：node test/format-probe.mjs [locale ...]    默认 zh-CN en-US ru-RU
// 退出码：0 = 锚点全部匹配且无单元格错误；1 = 有锚点漂移、函数缺失或单元格错误（表格仍会打印）。

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const htmlPath = path.resolve(here, '../internal/plugin/web/dashboard.html');
const html = fs.readFileSync(htmlPath, 'utf8');

const locales = process.argv.slice(2);
if (!locales.length) locales.push('zh-CN', 'en-US', 'ru-RU');

// —— 从 dashboard.html 提取单行函数（这些工具函数在源码里都是一行一个）——
const FN_NAMES = ['pad2', 'md', 'hm', 'dateKey', 'timeInputValue', 'localeDate', 'requestTime',
  'bucketInfo', 'healthTimeParts', 'healthCallTime', 'healthBucketTitle'];
function extractFn(name) {
  const at = html.search(new RegExp('^function ' + name + '\\(', 'm'));
  if (at < 0) return null;
  const end = html.indexOf('\n', at);
  return html.slice(at, end < 0 ? undefined : end);
}
const extracted = Object.fromEntries(FN_NAMES.map(n => [n, extractFn(n)]));
const missing = FN_NAMES.filter(n => !extracted[n]);
const sandboxSrc = FN_NAMES.map(n => extracted[n] || '').join('\n');

// 词条桩：不伪造翻译，键名原样出现，避免探针自己编造事实
const stubT = k => '[' + k + ']';

let makeApi;
try {
  makeApi = new Function('formatterLocale', 't', sandboxSrc +
    '\nreturn {localeDate,requestTime,bucketInfo,healthTimeParts,healthCallTime,healthBucketTitle};');
} catch (error) {
  console.error('探针沙箱编译失败（dashboard 源码有语法问题？）: ' + error.message);
  process.exitCode = 1;
  process.exit(1);
}
const apis = Object.fromEntries(locales.map(loc => [loc, makeApi(loc, stubT)]));

// —— 显示点清单：label + 源码锚点（正则, 期望出现次数） + 与 dashboard 等价的取值代码 ——
const hits = pattern => (html.match(new RegExp(pattern, 'g')) || []).length;
const SITES = [
  { label: '提示类·数据更新/目录加载/保存/加载', anchor: ['toLocaleTimeString\\(formatterLocale\\)', 4], run: (api, d, loc) => d.toLocaleTimeString(loc) },
  { label: 'API Key·最近使用（无 options）', anchor: ['localeDate\\(new Date\\(data\\.last_used\\)\\)', 1], run: (api, d) => api.localeDate(d) },
  { label: '价格同步·完成时间（无 options）', anchor: ['localeDate\\(new Date\\(lastSync\\.completed_at\\)\\)', 1], run: (api, d) => api.localeDate(d) },
  { label: 'PNG 导出·图片时间戳', anchor: ['toLocaleString\\(formatterLocale\\)', 1], run: (api, d, loc) => d.toLocaleString(loc) },
  { label: '价格表·最后保存（month:numeric）', anchor: ["month:'numeric'", 1], run: (api, d) => api.localeDate(d, { year: 'numeric', month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' }) },
  { label: '请求明细·时间列 requestTime', anchor: ['function requestTime\\(value\\)', 1], run: (api, d) => api.requestTime(d) },
  { label: '趋势轴·分钟桶', anchor: ['function bucketInfo\\(', 1], run: (api, d) => api.bucketInfo(d, 'minute').label },
  { label: '趋势轴·小时桶', anchor: ['function bucketInfo\\(', 1], run: (api, d) => api.bucketInfo(d, 'hour').label },
  { label: '趋势轴·天桶', anchor: ['function bucketInfo\\(', 1], run: (api, d) => api.bucketInfo(d, 'day').label },
  { label: '趋势轴·周桶', anchor: ['function bucketInfo\\(', 1], run: (api, d) => api.bucketInfo(d, 'week').label },
  { label: '趋势轴·月桶', anchor: ['function bucketInfo\\(', 1], run: (api, d) => api.bucketInfo(d, 'month').label },
  { label: '健康·时段标题 healthTimeParts', anchor: ['function healthTimeParts\\(', 1], run: (api, d) => api.healthTimeParts(d) },
  { label: '健康·实时单次 healthCallTime', anchor: ['function healthCallTime\\(', 1], run: (api, d) => api.healthCallTime(d.toISOString()) },
  { label: '健康·小时窗标题', anchor: ['function healthBucketTitle\\(', 1], run: (api, d) => api.healthBucketTitle('day', { bucket_start: d.toISOString(), bucket_end: new Date(d.getTime() + 3600000).toISOString() }) },
  { label: '健康·多日部分窗标题', anchor: ['function healthBucketTitle\\(', 1], run: (api, d) => { const s = new Date(d); s.setDate(s.getDate() - 4); return api.healthBucketTitle('days', { bucket_start: s.toISOString(), bucket_end: d.toISOString(), partial: true }); } },
];

// —— 样本时刻：2026-09-05 14:30:05（月/日/时均可区分 12/24 小时制与补零差异）——
const SAMPLE = new Date(2026, 8, 5, 14, 30, 5);

const anchorIssues = [];
for (const site of SITES) {
  const [pattern, expected] = site.anchor;
  const found = hits(pattern);
  if (found !== expected) anchorIssues.push(site.label + '：锚点 /' + pattern + '/ 期望 ' + expected + ' 次，实际 ' + found + ' 次');
}

const rows = SITES.map(site => {
  const row = { 场景: site.label };
  for (const loc of locales) {
    try {
      row[loc] = String(site.run(apis[loc], SAMPLE, loc));
    } catch (error) {
      row[loc] = '⚠ ' + String(error && error.message ? error.message.split('\n')[0] : error);
    }
  }
  return row;
});

console.log('== format-probe ==');
console.log('源文件: ' + htmlPath);
console.log('样本时刻: ' + SAMPLE.toLocaleString() + '  (' + SAMPLE.toISOString() + ')');
console.log('时区: ' + (Intl.DateTimeFormat().resolvedOptions().timeZone || '未知'));
if (missing.length) console.log('⚠ 函数提取缺失: ' + missing.join(', '));
if (anchorIssues.length) {
  console.log('⚠ 源码锚点漂移（dashboard 已改动，下表需人工核对后更新探针）:');
  anchorIssues.forEach(line => console.log('  - ' + line));
} else {
  console.log('锚点检查: ' + SITES.length + '/' + SITES.length + ' 匹配');
}
console.table(rows);

const cellErrors = rows.some(row => locales.some(loc => String(row[loc]).startsWith('⚠')));
process.exitCode = anchorIssues.length || missing.length || cellErrors ? 1 : 0;
