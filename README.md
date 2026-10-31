<p align="center"><img src="images/logo.svg" width="120" alt="Tokens Statistic"></p>

<h1 align="center">Tokens Statistic</h1>

<p align="center">简体中文 | <a href="README_EN.md">English</a></p>

<p align="center">
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/License-MIT-blue"></a>
  <a href="https://github.com/Pet-Max/cpa-plugin-tokens-statistic/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/Pet-Max/cpa-plugin-tokens-statistic?logo=github&label=Release"></a>
  <a href="https://go.dev"><img alt="Go" src="https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoSize=auto&label=%E8%AF%AD%E8%A8%80"></a>
  <a href="https://github.com/Pet-Max/cpa-plugin-tokens-statistic/releases/latest"><img alt="Platform" src="https://img.shields.io/badge/Platform-Linux%20%7C%20MacOS%20%7C%20Windows-lightgrey"></a>
</p>


Tokens Statistic 是一个 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 用量统计插件。安装后，管理中心会新增「Tokens Statistic」页面，每次模型调用的模型、Token 数、延迟、成败与缓存命中都会被记录，并以趋势图和明细表呈现，支持任意时间范围查询。所有数据保存在本地数据库，不上传。

<p align="center"><img src="images/desktop/cn/dashboard-cards.png" width="720" alt="指标卡片"></p>
<p align="center">📸 <a href="preview.md">界面预览</a>（指标卡片 · 服务健康 · Tokens 分析 · 请求明细 · 模型明细 · 价格同步）</p>

## 主要功能

- 记录每次调用的模型、输入 / 输出 / 推理 / 缓存 Token、延迟、TTFT、TPS、成败与缓存命中
- 按模型、提供商、执行器、来源、认证类型、服务层级、推理强度、失败状态多维分组统计
- 概览卡片、Tokens 分析趋势图、维度统计表与逐请求明细表
- 时间范围支持今天、最近 5 小时 / 7 天 / 30 天、本月及自定义区间
- 趋势图支持分钟至月聚合，滚轮缩放与平移
- 表格分页、排序与列显示偏好，Token 单位与货币切换均可持久化
- 按多个 API Key 并集筛选与标签管理
- 跟随管理中心主题与浏览器语言，内置简体中文、繁体中文、英文、俄文
- 单文件插件，支持 Linux、Mac 与 Windows

## 部署

**方法 1（推荐）**：在 CPA 插件管理商店内一键安装。

**方法 2**：

1. 从 [Releases](https://github.com/Pet-Max/cpa-plugin-tokens-statistic/releases) 下载对应平台的 zip 并解压，得到一个动态库文件。
2. 将动态库文件放入 CLIProxyAPI 目录下的 `plugins/<系统>/<架构>/`。
3. 在 CLIProxyAPI 的 config.yaml 中配置（`enabled` 必填，宿主默认不加载未显式启用的插件）：

```yaml
plugins:
  enabled: true
  configs:
    tokens-statistic:
      enabled: true
      db: data/tokens-statistic.db   # 数据库路径，默认 <CLIProxyAPI 目录>/data/tokens-statistic.db
      retention: 365                 # 保留天数（1–3650），默认 365
      flush: 5s                      # 批量写盘间隔（1s–1h），省略 = 每条立即落库
      secret: "123456"               # API Key 加密密钥，公开部署须改为 ≥32 字节随机串
      # health_warn: 95              # 降级阈值，默认 95
      # health_crit: 80              # 异常阈值，默认 80
```

4. 字段说明：

| 字段 | 默认值 | 说明 |
|---|---|---|
| `db` | `<CLIProxyAPI 目录>/data/tokens-statistic.db` | bbolt 数据库路径。留空使用默认位置；相对路径相对于 CLIProxyAPI 工作目录解析 |
| `retention` | `365` | 逐分钟聚合与请求明细的保留天数（1–3650），过期自动清理 |
| `flush` | 空 | 留空每条请求立即落库；填写后按间隔批量写盘（1s–1h），小主机建议 5s，突发流量由内部 100 条/批上限摊平 |
| `secret` | `123456` | API Key 加密与指纹密钥，自定义须不少于 32 字节。公开部署必须修改；留空则完全禁用 API Key 追踪 |
| `health_warn` / `health_crit` | `95` / `80` | 服务健康面板的成功率阈值：低于 health_warn 标黄（降级）、低于 health_crit 标红（异常）。均为 0–100 且 health_warn 必须大于 health_crit |

## 构建

本地构建跑 `scripts/` 里的脚本（4 平台，产物留在本地 `dist/`，不进 git）。发布由仓库维护者操作：推送 `v*` 标签后 CI 构建 6 平台（含 macOS）并自动发布稳定版 Release。环境要求：Go 1.26+ 与 `CGO_ENABLED=1`，交叉编译推荐 [zig](https://zig.dev)（Windows 也可用 MinGW-w64，Linux arm64 也可用 aarch64-linux-gnu-gcc）。

| 平台 | 命令 |
|:--|:--|
| Linux amd64 | `bash scripts/build-linux-amd64.sh` |
| Linux arm64 | `bash scripts/build-linux-arm64.sh` |
| Windows amd64 / arm64 | `powershell -ExecutionPolicy Bypass -File scripts/build_dll.ps1` |

- 产物：输出到 `dist/release/v<版本>/`（`tokens-statistic_<版本>_<平台>.zip` + `checksums.txt`，与 CI Release 资产同规格）；版本可用 `VERSION` 覆盖。
- 测试：`gofmt -w *.go && go vet ./... && go test -count=1 ./...`；可选的浏览器回归测试需要 Node、Chrome/Edge 与 `npm ci` 安装的 playwright-core 并设置 `CHROME_PATH`，条件不满足时自动跳过。

## 隐私与安全

- 不存储 prompt、请求正文与响应正文，只保留统计所需的元数据与计数
- API Key 以密文加指纹形式存储，明文仅在通过管理密钥验证的管理接口中按需返回，不写入页面静态内容、日志或浏览器存储
- 仪表盘数据与全部管理操作都受 CLIProxyAPI 管理密钥保护：管理中心内自动复用已登录身份，独立访问时需输入一次管理密钥（仅保存在当前标签页，关闭即失效）
- 默认 `secret`（123456）仅适用于本机测试，公开部署请设置不少于 32 字节的随机值；更换后历史密文保留但无法解密显示，留空则完全关闭 API Key 追踪

## 鸣谢

[AITNR](https://github.com/AITNR/cap-token-usage-tracker)

## License

[MIT](LICENSE)
