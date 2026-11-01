#!/usr/bin/env node
"use strict";

// 对拍验证：管理中心两代 localStorage 混淆格式 × 插件 decodeStorageValue/encodeStorageValue。
// 被测函数按行从 internal/plugin/web/dashboard.html 实时抽取，保证与随包发布的代码一致。
//
// 格式依据（源码核对）：
//   管理中心 <=1.26.x  enc::v1::  key = cli-proxy-api-webui::secure-storage|<host>|<userAgent>
//   管理中心  1.27.0   enc::v2::  key = cli-proxy-api-webui::secure-storage|<host>
//   其他面板 v2 变体    enc::v2::  key = cli-proxy-api-webui::secure-storage|v2|<host>
//
// 用法：node scripts/verify-storage-format.mjs   （零依赖，失败退出码 1）

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const html = readFileSync(join(root, "internal/plugin/web/dashboard.html"), "utf8");

function extractFunctionLine(name) {
  const match = html.match(new RegExp("^function " + name + "\\(.+$", "m"));
  if (!match) throw new Error("dashboard.html 中未找到 function " + name);
  return match[0];
}

const HOST = "127.0.0.1:8317";
const UA = "Mozilla/5.0 (probe) TestUA/1.0";
const SALT = "cli-proxy-api-webui::secure-storage";

const load = new Function(
  "window",
  "navigator",
  "btoa",
  "atob",
  "TextEncoder",
  "TextDecoder",
  extractFunctionLine("encodeStorageValue") +
    "\n" +
    extractFunctionLine("decodeStorageValue") +
    "\nreturn { encodeStorageValue, decodeStorageValue };"
);

const { encodeStorageValue, decodeStorageValue } = load(
  { location: { host: HOST } },
  { userAgent: UA },
  (data) => Buffer.from(data, "binary").toString("base64"),
  (data) => Buffer.from(data, "base64").toString("binary"),
  TextEncoder,
  TextDecoder
);

// 按管理中心 encryption.ts 的算法构造密文
function centerWrite(value, version, scope) {
  const bytes = Buffer.from(value, "utf8");
  const key = Buffer.from(SALT + "|" + scope, "utf8");
  const out = Buffer.alloc(bytes.length);
  for (let i = 0; i < bytes.length; i++) out[i] = bytes[i] ^ key[i % key.length];
  return "enc::v" + version + "::" + out.toString("base64");
}

const envelope = JSON.stringify({
  state: {
    apiBase: "http://" + HOST,
    managementKey: "my-management-key-123",
    rememberPassword: true,
    serverVersion: "8.0.48",
    serverBuildDate: null,
  },
  version: 0,
});

function readKey(value) {
  const parsed = decodeStorageValue(value);
  if (!parsed || typeof parsed !== "object") return null;
  return parsed.managementKey || (parsed.state && parsed.state.managementKey) || null;
}

let failures = 0;
function check(label, actual, expected) {
  const ok = actual === expected;
  if (!ok) failures++;
  console.log(
    (ok ? "PASS" : "FAIL") + "  " + label + (ok ? "" : "  | 实际=" + String(actual) + " 期望=" + String(expected))
  );
}

check(
  "encode→decode 往返保真（插件自身记住凭据）",
  (decodeStorageValue(encodeStorageValue(JSON.stringify({ key: "remembered-key-1" }))) || {}).key,
  "remembered-key-1"
);
check("encode 新写入前缀为 enc::v2::", encodeStorageValue("x").slice(0, 9), "enc::v2::");
check("管理中心 v1 信封（<=1.26.x，盐|host|UA）解出管理密钥", readKey(centerWrite(envelope, 1, HOST + "|" + UA)), "my-management-key-123");
check("管理中心 v2 信封（1.27.0，盐|host）解出管理密钥", readKey(centerWrite(envelope, 2, HOST)), "my-management-key-123");
check("管理中心 v2 信封（v2|host 作用域变体，盐|v2|host）解出管理密钥", readKey(centerWrite(envelope, 2, "v2|" + HOST)), "my-management-key-123");
check(
  "插件旧 v1 记住凭据仍可读（回归）",
  (decodeStorageValue(centerWrite(JSON.stringify({ key: "legacy-remembered" }), 1, HOST + "|" + UA)) || {}).key,
  "legacy-remembered"
);
check("enc::v9:: 未知前缀垃圾值返回 null", decodeStorageValue("enc::v9::AAAA"), null);
check("无前缀明文 JSON 照旧解析", (decodeStorageValue('{"key":"plain"}') || {}).key, "plain");
check("v2 密钥全部不匹配返回 null", decodeStorageValue(centerWrite(envelope, 2, "wrong|scope")), null);

console.log(failures ? "\n结果：FAIL（" + failures + " 项失败）" : "\n结果：全部通过");
process.exit(failures ? 1 : 0);
