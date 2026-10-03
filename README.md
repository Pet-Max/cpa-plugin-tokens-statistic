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

## 界面预览

**仪表盘总览（普通模式）**

![仪表盘总览：概览卡片与 Tokens 分析趋势](images/dashboard-overview.png)

| 模型明细 | 请求明细 |
|:-:|:-:|
| ![模型明细](images/dashboard-dimensions.png) | ![请求明细](images/dashboard-requests.png) |

**模型价格与同步（完整模式）**

![模型价格与同步](images/full-mode-pricing.png)

## 主要功能

- 记录每次调用的模型、输入 / 输出 / 推理 / 缓存 Token、延迟、TTFT、TPS、成败与缓存命中
- 按模型、提供商、执行器、来源、认证类型、服务层级、推理强度、失败状态多维分组统计
- 概览卡片、Tokens 分析趋势图、维度统计表与逐请求明细表
- 时间范围支持今天、最近 5 小时 / 7 天 / 30 天、本月及自定义区间
- 趋势图支持分钟至月聚合，滚轮缩放与平移
- 表格分页、排序、列显示偏好持久化
- 完整模式支持按 API Key 多选筛选（并集）与标签管理
- Token 单位完整值 / K / M / B 切换并持久化
- 跟随管理中心主题与浏览器语言，内置简体中文、繁体中文、英文、俄文
- 单文件插件，支持Linux、Mac与Windows

## 部署

### 方法1（推荐）：

可以直接在 CPA 插件管理商店内一键安装。

### 方法2：

1. 从 [Releases](https://github.com/Pet-Max/cpa-plugin-tokens-statistic/releases) 下载对应平台的 zip 并解压，得到一个动态库文件。
2. 将动态库文件放入 CLIProxyAPI 目录下的 `plugins/<系统>/<架构>/`。
3. 在 CLIProxyAPI 的 config.yaml 中配置：

```yaml
plugins:
  enabled: true
  configs:
    tokens-statistic:
      enabled: true     # 必填：宿主默认不加载未显式启用的插件
      db: data/tokens-statistic.db   # 数据库路径（相对 CLIProxyAPI 工作目录）；省略时默认 <CLIProxyAPI 目录>/data/tokens-statistic.db
      retention: 365    # 逐分钟聚合与请求明细的保留天数（1–3650），默认 365
      flush: 5s         # 批量写盘间隔（1s–1h）。小主机（NAS/SD 卡等写入敏感设备）推荐值；省略此行 = 每条请求立即落库（默认）
      secret: "123456"  # API Key 加密密钥。123456 即默认值，仅建议本机测试；公开部署须改为不少于 32 字节的随机串
      session_ttl: 15   # 完整模式会话有效期（分钟，1–1440），默认 15
```

4. 字段说明：

| 字段 | 默认值 | 说明 |
|---|---|---|
| `db` | `<CLIProxyAPI 目录>/data/tokens-statistic.db` | bbolt 数据库路径。留空使用默认位置；填相对路径时相对于 CLIProxyAPI 工作目录解析 |
| `retention` | `365` | 逐分钟聚合与请求明细的保留天数（1–3650），过期自动清理 |
| `flush` | 空 | 留空表示每条请求立即落库；填写后按间隔批量写盘（1s–1h）。小主机或高频调用建议 5s；突发流量由内部 100 条/批上限自动摊平 |
| `secret` | `123456` | API Key 加密与指纹密钥，自定义须不少于 32 字节。公开部署必须修改；留空则完全禁用 API Key 追踪 |
| `session_ttl` | `15` | 完整模式会话有效期，单位分钟（1–1440） |

## 构建（开发）

环境要求：Go 1.26+、`CGO_ENABLED=1`；交叉编译推荐 [zig](https://zig.dev)，Windows 也可用 MinGW-w64，Linux arm64 也可用 aarch64-linux-gnu-gcc。

| 平台 | 命令 |
|:--|:--|
| Linux amd64 | `bash scripts/build-linux-amd64.sh` |
| Linux arm64 | `bash scripts/build-linux-arm64.sh` |
| Windows amd64 / arm64 | `powershell -ExecutionPolicy Bypass -File scripts/build_dll.ps1` |

也可以直接用 go build（以 Linux amd64 为例）：

```bash
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
  CC="zig cc -target x86_64-linux-gnu" \
  go build -buildmode=c-shared -trimpath -buildvcs=false \
  -ldflags="-s -w -X github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin.version=v0.1.0" \
  -o tokens-statistic.so .
```

产物位置：

- 原始动态库输出到 `dist/`；
- 商店规格发布包（`tokens-statistic_<版本>_<平台>.zip` + `checksums.txt`）统一输出到 `dist/release/v<版本>/`，与 CI 发行的 Release 资产同规格；
- 版本通过 `VERSION` 覆盖，例如 `VERSION=v0.2.0 bash scripts/build-linux-amd64.sh`。

测试：

```bash
gofmt -w *.go
go vet ./...
go test -count=1 ./...
```

浏览器回归测试（可选）：需要 Node、Chrome/Edge 与 `npm ci` 安装的 playwright-core，并设置 `CHROME_PATH` 指向浏览器；条件不满足时相关用例自动跳过。

### CI 自动构建

推送 `v*` 标签后，CI 会自动在 GitHub 的构建服务器上完成全部六个平台的编译并发布 Release（附 checksums.txt）。其中 macOS 的 amd64 与 arm64 两个包在 GitHub 的 macOS 服务器上构建，并在同一环境完成加载冒烟验证；Windows arm64 包在 Windows 服务器上用 zig 交叉编译——本机无需安装 macOS 工具链，也不用单独准备 arm64 工具链。仓库另有工作流自动执行代码质量检查与 CLIProxyAPI 新版本兼容性验证。

## 隐私与安全

- 不存储 prompt、请求正文与响应正文，只保留统计所需的元数据与计数
- API Key 以密文加指纹形式存储，明文仅在完整模式内按需返回，不写入页面静态内容、日志或浏览器存储
- 默认 `secret`（123456）仅适用于本机测试；公开部署请设置不少于 32 字节的随机值。更换 `secret` 后历史密文保留但无法解密显示
- 留空 `secret` 可完全关闭 API Key 追踪

## 鸣谢

[AITNR](https://github.com/AITNR/cap-token-usage-tracker)

## License

[MIT](LICENSE)
