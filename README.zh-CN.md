# codex-cliproxy-gateway

[English](README.md) · 简体中文

[![CI](https://github.com/jepson66/codex-cliproxy-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/jepson66/codex-cliproxy-gateway/actions/workflows/ci.yml)
[![Platforms](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](#平台支持)

一个实验性的本地网关：在 Codex 的同一个 `/model` 选择器中保留官方 GPT
模型，同时加入由 CLIProxyAPI 提供的 Kimi 等第三方模型。

```text
Codex CLI / Desktop
        │
        ▼
codex-cliproxy-gateway（127.0.0.1:8765）
        │
        ├── 官方模型 ID ───────► ChatGPT Codex 接口
        │                         （原有 ChatGPT OAuth）
        │
        └── cliproxy/* ───────► CLIProxyAPI（127.0.0.1:8317）
                                  └── Kimi / 未来的其他提供商
```

## 为什么需要

如果让 Codex 直接指向第三方提供商，可能会替换原有的 ChatGPT 路由。本网关
为 Codex 提供一个合并后的模型目录，再按模型 ID 路由请求：

- 官方 GPT 条目继续使用 ChatGPT OAuth。
- `cliproxy/*` 条目使用本机 CLIProxyAPI 访问密钥。
- 模型通过 `/model` 切换，不需要 profile，也不需要用另一个模型替换官方模型。

## 当前状态

这是概念验证项目，不是生产支持承诺。

- 已在 macOS、Codex CLI `0.153.2`、CLIProxyAPI `7.3.11` 下验证官方 GPT
  与 Kimi API Key 路由。
- CI 在 macOS、Linux、Windows 上运行单元测试、竞态测试、vet、敏感信息扫描
  和构建检查。
- Release 覆盖 Darwin、Linux、Windows 的 amd64 与 arm64。
- 已下载并校验官方 Windows CLIProxyAPI 归档的 SHA-256，并确认解压结果为
  PE 可执行文件。
- Linux 和 Windows 服务代码已测试并交叉构建，但仍需在干净系统或虚拟机上
  完成端到端验收。
- Codex Desktop 与 CLI 共用用户级配置，但每个 Desktop 版本仍需实际 UI 验收。

不要因为 Kimi 的结果就推断其他模型也兼容。每个模型都需要单独验证流式输出、
工具调用、图片、上下文窗口和 reasoning 选项。

## 特性

- 保留官方模型 ID 和 ChatGPT OAuth 路由。
- 在 `/model` 中加入带命名空间的第三方模型。
- 从第三方请求中移除 ChatGPT OAuth 和账户相关请求头。
- 拒绝未知的 `cliproxy/*` 模型 ID，不会盲目转发。
- 内置 zstd 解码器，用户不需要另行安装 zstd 或 Go。
- 默认只监听 loopback；只有显式开启时才允许非本机访问。
- 只修改归网关管理的 Codex 配置字段，并创建私有备份。
- 支持外部 CLIProxyAPI 和受实验开关保护的 managed CLIProxyAPI 模式。
- 使用 launchd、systemd 或 Task Scheduler 安装当前用户服务。
- 提供 plan、status、health、doctor 和可恢复的卸载命令。

## 前置条件

1. 安装 Codex，使用 ChatGPT 登录，并至少成功运行一次官方模型。这样会生成
   合并模型目录需要的官方模型缓存。
2. 在 <https://platform.kimi.com/console/api-keys> 创建 Kimi API Key。
3. 安装网关前完全退出 Codex。

## 无需 Go 的安装方式

Release 安装器会识别操作系统和架构，下载预编译 Gateway，校验 SHA-256，
展示完整安装计划，并将 Gateway 与固定版本 CLIProxyAPI `7.3.11` 安装为当前
用户服务。

### Windows PowerShell

最快方式：

```powershell
irm https://raw.githubusercontent.com/jepson66/codex-cliproxy-gateway/main/install.ps1 | iex
```

建议先下载并检查脚本：

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/jepson66/codex-cliproxy-gateway/main/install.ps1 -OutFile .\install.ps1
notepad .\install.ps1
.\install.ps1
```

### macOS / Linux

建议先检查脚本，再执行：

```sh
curl -fL https://raw.githubusercontent.com/jepson66/codex-cliproxy-gateway/main/install.sh -o install.sh
less install.sh
sh install.sh
```

安装器绝不会读取、接收或写入供应商 API Key。首次安装 CLIProxyAPI 时，它只会
创建空的提供商映射，然后告诉用户需要手工编辑的私有文件路径；已有的
CLIProxyAPI 配置会被保留，绝不会覆盖。

在每个操作系统的干净机器验收完成前，managed 模式都明确标记为实验性。安装器
会显示这一点，不会隐藏。

## 从源码构建

开发者需要 Go `1.26` 或更高版本：

```sh
git clone https://github.com/jepson66/codex-cliproxy-gateway.git
cd codex-cliproxy-gateway
go build -o ./bin/codex-cliproxy-gateway ./cmd/codex-cliproxy-gateway
```

Windows：

```powershell
go build -o .\bin\codex-cliproxy-gateway.exe .\cmd\codex-cliproxy-gateway
```

## 生命周期模式

### Managed CLIProxyAPI

一键安装器使用 managed 模式。手工执行等价于：

```sh
codex-cliproxy-gateway plan --cliproxy-mode managed
codex-cliproxy-gateway bootstrap --cliproxy-mode managed --experimental-managed
```

managed 模式会下载固定版本的上游 Release，在解压前校验平台对应的 SHA-256，
创建独立的当前用户服务，并保留已有 CLIProxyAPI 配置。它会生成随机的本地访问
密钥，但不会生成、覆盖或代填供应商凭据。

### External CLIProxyAPI

如果 CLIProxyAPI 已经由其他方式安装并维护，请使用 external 模式：

```sh
codex-cliproxy-gateway plan --cliproxy-mode external
codex-cliproxy-gateway bootstrap --cliproxy-mode external
```

external 模式不会替换 CLIProxyAPI 的二进制、配置或服务。其配置必须包含
本机 `api-keys` 条目。Bootstrap 会将第一个本机访问密钥导入网关私有文件，
但不会打印该密钥。

## Kimi API Key 配置

安装完成后，请由用户自行配置提供商。在
`~/.cli-proxy-api/config.yaml` 中保留已有的 `host`、`port`、`auth-dir` 和
`api-keys`，并添加：

```yaml
openai-compatibility:
  - name: "kimi"
    base-url: "https://api.moonshot.cn/v1"
    api-key-entries:
      - api-key: "<YOUR_KIMI_API_KEY>"
    models:
      - name: "kimi-k3"
        alias: "kimi-k3"
        max-context-length: 1048576
        input-modalities: [text, image]
        output-modalities: [text]
        thinking:
          levels: ["none"]
```

绝不要提交这个文件。它包含明文供应商凭据。Kimi 提供商密钥与 CLIProxyAPI
随机本地访问密钥是两个不同的秘密。

网关默认模型声明在
[`examples/config.example.json`](examples/config.example.json)。新增提供商或
模型时，两个配置必须使用一致的客户端可见别名，并且网关只能声明已经验证过的
能力。

修改网关模型列表后：

```sh
codex-cliproxy-gateway catalog
```

模型目录在启动时加载，因此还需要重启 Codex。

## 使用 `/model`

安装完成后：

```sh
codex-cliproxy-gateway status
codex-cliproxy-gateway doctor
```

可选的端到端检查会发送一次可能计费的 Kimi 请求：

```sh
codex-cliproxy-gateway doctor --e2e --model cliproxy/kimi-k3
```

完全退出 Codex CLI/Desktop，启动新会话，打开 `/model`，选择：

```text
cliproxy/kimi-k3
```

官方 GPT 条目仍会出现在同一个选择器中。当前 Kimi 只暴露 `none` reasoning
选项，因为推理行为由上游自动管理。

## 命令

| 命令 | 作用 |
| --- | --- |
| `init` | 若不存在则创建默认 Gateway JSON 配置。 |
| `plan` | 预览所有文件、依赖、配置和服务操作。 |
| `bootstrap` | 事务性应用已审核的生命周期计划。 |
| `import-cliproxy-key` | 导入本机 CLIProxyAPI 访问密钥，但不打印。 |
| `catalog` | 重新生成合并后的模型目录。 |
| `install` | 修改归网关管理的 Codex 配置字段并创建备份。 |
| `repair-legacy-providers` | 将旧的持久化 provider 重定向到网关。 |
| `serve` | 在前台运行网关。 |
| `service-install` | 安装或更新当前用户的原生网关服务。 |
| `service-uninstall` | 移除服务定义，保留配置和二进制。 |
| `status` | 查看网关和 managed CLIProxyAPI 服务状态。 |
| `doctor` | 检查配置、模型缓存、密钥和 CLIProxyAPI 就绪状态。 |
| `uninstall` | 安全检查通过后恢复原始 Codex 配置。 |
| `version` | 输出网关版本。 |

## Codex 配置所有权

安装不会替换整个 `~/.codex/config.toml`。它只会打印备份路径，并管理：

- 顶层 `model_provider`；
- 顶层 `model_catalog_json`；
- `[model_providers.codex-cliproxy-gateway]`；
- 已配置旧 provider ID 的 `base_url`；
- 需要命名空间时，未加前缀的已选第三方模型 ID。

项目 trust 条目、插件、MCP server、环境变量和其他无关设置都会保留。如果安装
后文件在这块所有权范围之外被修改，自动替换或恢复会安全失败，不会覆盖无关编辑。

## 服务位置

| 平台 | Gateway 二进制 | 服务定义 |
| --- | --- | --- |
| macOS | `~/.local/bin/codex-cliproxy-gateway` | `~/Library/LaunchAgents/com.codex-cliproxy-gateway.plist` |
| Linux | `~/.local/bin/codex-cliproxy-gateway` | `~/.config/systemd/user/com.codex-cliproxy-gateway.service` |
| Windows | `%LOCALAPPDATA%\codex-cliproxy-gateway\bin\codex-cliproxy-gateway.exe` | `%LOCALAPPDATA%\codex-cliproxy-gateway\services\gateway-task.xml` |

Windows 使用最低权限的当前用户 Task Scheduler 登录任务，安装网关服务不需要
管理员权限。

## 健康检查与排障

Loopback 健康端点：

- `GET /livez`：网关进程存活状态。
- `GET /readyz`：经过认证的 CLIProxyAPI 模型端点就绪状态。
- `GET /healthz`：兼容性的存活状态别名。

每次排查都从 `status` 和 `doctor` 开始。

- **`/model` 中没有第三方模型：** 先运行一次官方 Codex，重新执行 `catalog`，
  然后完全重启 Codex。
- **“model is not supported when using Codex with a ChatGPT account”：** 请求绕过
  了网关，或使用了旧的 provider/session。确认 `model_provider =
  "codex-cliproxy-gateway"`，检查服务健康状态，完全退出 Codex 后启动新会话。
  较早安装可能还需要 `repair-legacy-providers`。
- **`401 Unauthorized`：** 区分网关到 CLIProxyAPI 的本地访问密钥和 CLIProxyAPI
  内部的 Kimi 提供商密钥。
- **`404 /v1/responses`：** 确认提供商 base URL 包含 `/v1`，并且支持
  Responses API。
- **配置修改似乎没有生效：** 修改 YAML 后重启 CLIProxyAPI；修改模型目录或
  `config.toml` 后重启 Codex。

前台调试时，先停止已安装的网关服务，再运行
`codex-cliproxy-gateway serve`。

## 安全模型

官方请求保留传入的 ChatGPT 认证和压缩请求体字节。对于 `cliproxy/*` 请求，
网关会：

1. 将模型与本地 allowlist 比对；
2. 从模型 ID 中移除命名空间；
3. 移除 ChatGPT OAuth、cookie、账户、组织、项目、actor 和 authorization
   请求头；
4. 只加入本地 CLIProxyAPI 访问密钥；
5. 将普通 JSON 转发到 loopback CLIProxyAPI。

重要边界：

- Loopback 会阻止远程网络访问，但同一用户的本地进程仍在信任边界内。
- 在同一线程切换 provider 可能会将之前的上下文披露给新 provider。上下文敏感时，
  请启动新线程。
- 第三方的数据保留、配额、计费、提示词、仓库上下文和工具输出由对应提供商
  管理，不由 ChatGPT OAuth 管理。
- 未经过认证和网络边界审查，不要开启 `allow_non_loopback`。
- 分享日志和诊断信息前先审查。不要在 Issue 中附加凭据、私有配置或 Codex
  会话记录。

网关日志只包含路由、模型 ID、状态、耗时和关联 ID，不包含请求头、请求体或 API Key。

## 平台支持

| 能力 | macOS | Linux | Windows |
| --- | --- | --- | --- |
| CI 测试/构建 | 是 | 是 | 是 |
| amd64 / arm64 Release | 是 | 是 | 是 |
| 当前用户服务 | LaunchAgent | systemd user | Task Scheduler |
| Managed CLIProxyAPI 验证 | 是 | 是 | 是 |
| 干净机器运行验收 | 已验证 | 待完成 | 待完成 |

## 开发

```sh
go test -race ./...
go vet ./...
go build ./cmd/codex-cliproxy-gateway
sh scripts/check-secrets.sh
sh scripts/build-release.sh v0.1.0-test .release-test
```

请将提供商行为放在显式配置后，为路由和凭据边界补充回归测试，并只记录实际
验证过的行为。

## 卸载

```sh
codex-cliproxy-gateway service-uninstall --cliproxy-mode managed
codex-cliproxy-gateway uninstall
```

`service-uninstall` 会保留二进制和配置。`uninstall` 只有在安全检查确认不会覆盖
安装后的无关编辑时，才会恢复记录的备份。

## 许可证

项目目前尚未选择许可证。仓库可以公开阅读，但在添加许可证之前，版权法仍保留
再使用、修改和再分发权。若要正式宣称项目是完整的开源软件，请先加入 OSI
批准的许可证。

