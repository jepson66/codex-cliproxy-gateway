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
- 当前 Kimi 使用用户手工填写的 API Key；暂不包含 Kimi OAuth/订阅接入。
- CI 和 Release 覆盖 macOS、Linux、Windows 的 amd64 与 arm64。
- 新增模型前，需要单独验证流式输出、工具调用、图片、上下文和 reasoning。

## 安装预编译版本

前置条件：安装 Codex 并登录 ChatGPT，至少成功运行一次官方模型，创建 Kimi
API Key，然后完全退出 Codex。

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

## 配置 Kimi

安装后请用户自行编辑 CLIProxyAPI 配置：

- macOS/Linux：`~/.cli-proxy-api/config.yaml`
- Windows：`%USERPROFILE%\.cli-proxy-api\config.yaml`

添加 OpenAI 兼容提供商，并将占位符替换为自己的 Key：

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

不要提交此配置文件或在 Issue 中粘贴真实 Key。Kimi 提供商密钥与本地
CLIProxyAPI 访问密钥是两个不同的秘密。

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

**Codex CLI：** 重新启动 Codex，输入 `/model`，选择 `cliproxy/kimi-k3`。
官方 GPT 模型仍在同一个选择器中。

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
