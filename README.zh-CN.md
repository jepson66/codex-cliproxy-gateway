# codex-cliproxy-gateway

[English](README.md) · 简体中文

[![CI](https://github.com/jepson66/codex-cliproxy-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/jepson66/codex-cliproxy-gateway/actions/workflows/ci.yml)
[![Platforms](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](#当前状态)

一个实验性的本地网关：在 Codex 的同一个模型目录中保留官方 GPT，并加入
CLIProxyAPI 提供的模型。Codex Desktop 使用输入区下方的模型控件，Codex CLI
使用 `/model`。官方请求继续使用 ChatGPT OAuth；带 `cliproxy/*` 命名空间的
请求转发到本机 CLIProxyAPI。

```text
Codex Desktop / CLI
        │ Desktop：输入区下方的模型控件
        │ CLI：/model
        ▼
codex-cliproxy-gateway（127.0.0.1:8765）
        ├── 官方模型 ───────► ChatGPT Codex（OAuth）
        └── cliproxy/* ─────► CLIProxyAPI（127.0.0.1:8317）
                                └── Kimi / 其他提供商
```

## 当前状态

- 概念验证项目；managed CLIProxyAPI 模式明确标记为实验性。
- Kimi Code 支持设备 OAuth；原有的订阅 API Key 手工配置方式继续保留。
- CI 和 Release 覆盖 macOS、Linux、Windows 的 amd64 与 arm64。
- 新增模型前，需要单独验证流式输出、工具调用、图片、上下文和 reasoning。

## 安装预编译版本

前置条件：安装 Codex 并登录 ChatGPT，至少成功运行一次官方模型；安装前完全
退出 Codex。

Gateway 使用 Go 编写，但 Release 已包含编译完成的独立可执行文件。安装器不会
检测、安装、升级或删除 Go；电脑中已有的 Go 环境不会被修改。只有从源码构建
项目时才需要 Go。

### Windows

建议先检查脚本：

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/jepson66/codex-cliproxy-gateway/main/install.ps1 -OutFile .\install.ps1
notepad .\install.ps1
.\install.ps1
```

### macOS / Linux

```sh
curl -fL https://raw.githubusercontent.com/jepson66/codex-cliproxy-gateway/main/install.sh -o install.sh
less install.sh
sh install.sh
```

安装器下载预编译 Gateway、校验 SHA-256，并将固定版本 CLIProxyAPI 安装为当前
用户服务。安装器不会读取、接收或写入供应商密钥，也不会覆盖已有 CLIProxyAPI
配置。

## 登录 Kimi

推荐在安装后授权本机使用 Kimi Code：

```sh
codex-cliproxy-gateway login kimi-code
```

该命令会打开一次性 Kimi 授权地址、等待用户确认，并把 OAuth 凭据保存到配置的
CLIProxyAPI 认证目录。它不会调用或依赖 Kimi CLI，也不会输出 access token 或
refresh token；运行中的 CLIProxyAPI 会热加载新凭据。远程或无桌面环境使用
`codex-cliproxy-gateway login --no-browser kimi-code`，再手工打开命令显示的地址。

下面的命令可检查 CLIProxyAPI 是否已加载 OAuth 凭据或 API Key：

```sh
codex-cliproxy-gateway auth-status kimi-code
```

Codex 只切换模型时不会通知自定义 Provider，因此缺少登录的提示会在发送第一条
消息后出现，而不是点击模型选项时立即出现。

### 可选：API Key 备用认证

原有 API Key 方式继续支持。请用户自行编辑 CLIProxyAPI 配置：

- macOS/Linux：`~/.cli-proxy-api/config.yaml`
- Windows：`%USERPROFILE%\.cli-proxy-api\config.yaml`

添加以下 OpenAI 兼容提供商，并将占位符替换为自己的 Kimi Code Key。该 Key
消耗 Kimi Code 会员订阅额度，不是 Moonshot 开放平台按量计费 Key。请保留
`priority: 0`，并与 OAuth 共用 `kimi-k3` alias。

```yaml
openai-compatibility:
  - name: "kimi-code"
    priority: 0
    base-url: "https://api.kimi.com/coding/v1"
    api-key-entries:
      - api-key: "<KIMI_CODE_KEY>"
    models:
      - name: "k3"
        alias: "kimi-k3"
        max-context-length: 1048576
        input-modalities: [text, image]
        output-modalities: [text]
```

不要提交此配置文件或在 Issue 中粘贴真实 Key。Kimi 提供商密钥与本地
CLIProxyAPI 访问密钥是两个不同的秘密。

Codex 中始终只显示一个模型名：`cliproxy/kimi-k3`。Kimi Code OAuth 凭据使用
优先级 `100`，上述 API Key 提供商使用优先级 `0`。因此 OAuth 可用时
CLIProxyAPI 只使用 OAuth；OAuth 不存在或不可用时才回退到 API Key。同一个
`kimi-k3` alias 最多配置一个优先级为 `0` 的 API Key 提供商，不要同时配置
Kimi Code Key 和 Moonshot 开放平台 Key。Gateway 启动时会安全迁移旧版生成的
OAuth 文件。

未配置凭据时，Kimi 模型仍会保留在 Codex 模型选择器中。发送第一条请求时，
Gateway 会通过 CLIProxyAPI 检查状态，但不会读取提供商秘密。如果 Kimi 缺少或
拒绝凭据，Codex CLI 和 Desktop 会显示 `login kimi-code` 命令及 API Key 备用
配置路径。

## 选择模型

先检查链路：

```sh
codex-cliproxy-gateway status
codex-cliproxy-gateway doctor
```

可选的端到端检查会调用一次 Kimi，可能产生费用：

```sh
codex-cliproxy-gateway doctor --e2e --model cliproxy/kimi-k3
```

**Codex Desktop：** 完全退出并重新打开应用，新建会话，然后点击输入区下方的
模型与推理控件，选择 `cliproxy/kimi-k3`。也可以按 `Ctrl+Shift+M` 打开模型
选择器。

**Codex CLI：** 重新启动 Codex，输入 `/model`，选择
`cliproxy/kimi-k3`。官方 GPT 模型仍在同一个选择器中。OAuth 路径已用这个 1M
上下文模型验证，需要对应 Kimi 会员档位。默认不再提供 256K 别名，因为 Codex
加入工具 schema 后，正常请求也可能超过该模型上限。Kimi 不提供 Codex 的
reasoning 档位；Gateway 使用 Kimi 的非思考模式，不发送 `effort` 等级。为避免
无关 schema 消耗数十万 token，Kimi 请求默认排除 Codex Connected Apps，保留
核心编码、浏览器、MCP、图片和多 Agent 工具。

Desktop 当前入口可参考
[OpenAI 模型选择文档](https://learn.chatgpt.com/docs/models#choose-a-model)。

如果模型没有出现，先运行一次官方模型，再执行 `catalog` 并重启 Codex。
如果出现 ChatGPT account 不支持该模型的错误，说明请求绕过了网关或使用了
旧配置；确认 `model_provider = "codex-cliproxy-gateway"` 后启动新会话。

## 常用命令

| 命令 | 作用 |
| --- | --- |
| `plan` | 预览文件、依赖、配置和服务操作。 |
| `bootstrap` | 应用已审核的安装计划。 |
| `catalog` | 重新生成合并模型目录。 |
| `status` / `doctor` | 检查服务和提供商是否就绪。 |
| `login` / `auth-status` | 授权 Kimi 或检查 CLIProxyAPI 是否已加载凭据。 |
| `serve` | 在前台运行网关。 |
| `uninstall` | 通过安全检查后恢复 Codex 配置备份。 |

## 开发

开发者需要 Go `1.26` 或更高版本：

```sh
go test -race ./...
go vet ./...
go build ./cmd/codex-cliproxy-gateway
sh scripts/check-secrets.sh
```

## 安全

网关默认只监听 loopback，仅允许配置过的 `cliproxy/*` 模型，转发第三方请求前
会移除 ChatGPT 凭据，日志不记录请求头、请求体或 API Key。第三方的数据保留、
计费、配额、提示词和工具输出由对应提供商负责。
