# codex-cliproxy-gateway / Codex 第三方模型网关

## 中文说明

`codex-cliproxy-gateway` 是一个实验性的本地路由网关，让 Codex 在同一个
`/model` 选择器中同时保留官方 GPT，并加入 CLIProxyAPI 提供的 Kimi 等第三方
模型。

### 工作方式

```text
Codex CLI / Desktop
        │ /model
        ▼
codex-cliproxy-gateway（127.0.0.1:8765）
        ├── 官方 GPT ─────────► ChatGPT Codex（保留 OAuth）
        └── cliproxy/* ───────► CLIProxyAPI（127.0.0.1:8317）
                                   └── Kimi / 其他第三方模型
```

官方 GPT 请求继续使用 ChatGPT OAuth；只有 `cliproxy/*` 请求转发到本机
CLIProxyAPI。切换模型使用 Codex 的 `/model`，不需要 profile，也不需要用另一
个模型替换官方模型。

### Windows 一键安装（无需 Go）

安装前先安装并登录 Codex，成功运行一次官方模型，然后完全退出 Codex。安装器
会自动下载对应架构的预编译 Gateway，校验 SHA-256，安装固定版本 CLIProxyAPI，
并创建当前用户的 Task Scheduler 服务。

建议先下载检查脚本，再执行：

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/jepson66/codex-cliproxy-gateway/main/install.ps1 -OutFile .\install.ps1
notepad .\install.ps1
.\install.ps1
```

安装器不会读取、接收或写入任何供应商 API Key。安装完成后，用户自己打开：

```text
%USERPROFILE%\.cli-proxy-api\config.yaml
```

再手工填写 Kimi 的 `openai-compatibility` 配置。维护者本地另有完整的 Windows
教程；公开仓库只提交脱敏后的 README 和示例配置。真实 Key 不要提交 Git、粘贴
到 Issue 或上传到飞书文档。

### 快速使用

```powershell
$gateway = "$env:LOCALAPPDATA\codex-cliproxy-gateway\bin\codex-cliproxy-gateway.exe"
& $gateway status
& $gateway doctor
& $gateway doctor --e2e --model cliproxy/kimi-k3
```

`--e2e` 会真实调用 Kimi，可能产生费用。通过后完全退出 Codex，启动新会话，
输入 `/model`，选择 `cliproxy/kimi-k3`。官方 GPT 模型仍在同一个列表中。

### 当前边界

- 当前版本使用 Kimi API Key；Kimi OAuth/订阅接入属于后续评估范围。
- Windows 和 Linux 的服务代码已测试并交叉构建，但仍需干净系统端到端验收。
- Codex Desktop 与 CLI 共用用户级配置，但 Desktop 每个版本都需要实际 UI 验证。
- 当前仓库没有预先选择开源许可证；公开可访问不等于已经授予再分发权限。

完整的中文 Windows 教程保存在维护者本地：
`docs/windows-zero-to-one.md`。英文文档和开发说明见下文。

## English

[![CI](https://github.com/jepson66/codex-cliproxy-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/jepson66/codex-cliproxy-gateway/actions/workflows/ci.yml)
[![Platforms](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](#platform-support)

An experimental local gateway that keeps Codex's official GPT models and adds
allowlisted CLIProxyAPI models to the same `/model` picker.

```text
Codex CLI / Desktop
        │
        ▼
codex-cliproxy-gateway (127.0.0.1:8765)
        │
        ├── official model IDs ──► ChatGPT Codex endpoint
        │                          (original ChatGPT OAuth)
        │
        └── cliproxy/* ──────────► CLIProxyAPI (127.0.0.1:8317)
                                   └── Kimi / future providers
```

## Why

Pointing Codex directly at a third-party provider can replace the official
ChatGPT route. This gateway gives Codex one provider and one merged catalog,
then routes each request by model ID:

- Official GPT entries continue to use ChatGPT OAuth.
- `cliproxy/*` entries use a local CLIProxyAPI access key.
- Model switching happens through `/model`, without profiles or separate Codex
  launch commands.

## Status

This is a proof of concept, not a production support boundary.

- Verified on macOS with Codex CLI `0.153.2`, CLIProxyAPI `7.3.11`, an official
  GPT model, and `cliproxy/kimi-k3` through a Kimi API key.
- CI runs unit, race, vet, secret-scan, and build checks on macOS, Linux, and
  Windows.
- Release packaging covers Darwin, Linux, and Windows on amd64 and arm64.
- Official Windows CLIProxyAPI archives have been downloaded, SHA-256 verified,
  safely extracted, and checked as PE executables.
- Linux and Windows service code is tested and cross-built, but still requires
  a clean-machine/VM end-to-end acceptance run.
- Codex Desktop shares the user configuration, but every Desktop release still
  needs real UI acceptance testing.

Do not infer compatibility for a new model from the Kimi result. Each model
needs explicit streaming, tool-call, image, context-window, and reasoning-option
tests.

## Features

- Preserves official model IDs and ChatGPT OAuth routing.
- Adds namespaced third-party models to `/model`.
- Strips ChatGPT OAuth and account headers from third-party requests.
- Rejects unknown `cliproxy/*` model IDs instead of forwarding them.
- Uses an embedded zstd decoder; users do not install zstd or Go.
- Listens on loopback unless non-loopback access is explicitly enabled.
- Patches only owned Codex config fields and creates a private backup.
- Supports external and experiment-gated managed CLIProxyAPI modes.
- Installs per-user services with launchd, systemd, or Task Scheduler.
- Provides plan, status, health, diagnostic, and reversible uninstall commands.

## Prerequisites

1. Install Codex, sign in with ChatGPT, and run it successfully at least once.
   This creates the official model cache used by the merged catalog.
2. Create a Kimi API key at <https://platform.kimi.com/console/api-keys>.
3. Fully exit Codex before installing the Gateway.

## Install without Go

Release installers detect the operating system and architecture, download a
prebuilt Gateway archive, verify its SHA-256, show the complete installation
plan, and install both the Gateway and pinned CLIProxyAPI `7.3.11` as per-user
services.

### Windows PowerShell

For the shortest path:

```powershell
irm https://raw.githubusercontent.com/jepson66/codex-cliproxy-gateway/main/install.ps1 | iex
```

The safer inspect-first path is recommended:

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/jepson66/codex-cliproxy-gateway/main/install.ps1 -OutFile .\install.ps1
notepad .\install.ps1
.\install.ps1
```

### macOS / Linux

Inspect first, then run:

```sh
curl -fL https://raw.githubusercontent.com/jepson66/codex-cliproxy-gateway/main/install.sh -o install.sh
less install.sh
sh install.sh
```

The installer never reads, accepts, or writes provider API keys. It creates an
empty provider mapping on a new CLIProxyAPI installation, then tells the user
which private local file to edit manually. Existing CLIProxyAPI configuration
is preserved and never overwritten.

Managed mode remains explicitly experiment-gated until clean-machine admission
passes on every OS. The installer displays this status; it does not hide it.

## Build from source

Developers need Go `1.26` or newer:

```sh
git clone https://github.com/jepson66/codex-cliproxy-gateway.git
cd codex-cliproxy-gateway
go build -o ./bin/codex-cliproxy-gateway ./cmd/codex-cliproxy-gateway
```

On Windows:

```powershell
go build -o .\bin\codex-cliproxy-gateway.exe .\cmd\codex-cliproxy-gateway
```

## Lifecycle modes

### Managed CLIProxyAPI

The one-click installer uses managed mode. Manual equivalent:

```sh
codex-cliproxy-gateway plan --cliproxy-mode managed
codex-cliproxy-gateway bootstrap --cliproxy-mode managed --experimental-managed
```

Managed mode downloads a pinned upstream release, verifies the platform-specific
SHA-256 before extraction, creates a separate per-user service, and preserves
any existing CLIProxyAPI config. It creates a random local access key but does
not invent or overwrite provider credentials.

### External CLIProxyAPI

Use this when CLIProxyAPI is already installed and managed separately:

```sh
codex-cliproxy-gateway plan --cliproxy-mode external
codex-cliproxy-gateway bootstrap --cliproxy-mode external
```

External mode never replaces the CLIProxyAPI binary, config, or service. Its
config must contain a local `api-keys` entry. Bootstrap imports the first local
access key into a private Gateway file without printing it.

## Kimi API-key configuration

After installation, configure the provider yourself. Preserve the existing
`host`, `port`, `auth-dir`, and
`api-keys` values in `~/.cli-proxy-api/config.yaml`, and add:

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

Never commit this file. It contains plaintext provider credentials. The Kimi
provider key and the random local CLIProxyAPI access key are different secrets.

The Gateway's default model declaration is in
[`examples/config.example.json`](examples/config.example.json). When adding a
provider or model, both configurations must agree on the client-visible alias,
and the Gateway must declare only tested capabilities.

After changing the Gateway model list:

```sh
codex-cliproxy-gateway catalog
```

Restart Codex after catalog changes because it loads `model_catalog_json` at
startup.

## Use `/model`

After installation:

```sh
codex-cliproxy-gateway status
codex-cliproxy-gateway doctor
```

The optional end-to-end check sends one small, potentially billable Kimi
request:

```sh
codex-cliproxy-gateway doctor --e2e --model cliproxy/kimi-k3
```

Fully exit all Codex CLI/Desktop processes, start a new session, open `/model`,
and select:

```text
cliproxy/kimi-k3
```

Official GPT entries remain unchanged in the same picker. Kimi currently
exposes only `none` as its reasoning option because the upstream manages
reasoning behavior automatically.

## Commands

| Command | Purpose |
| --- | --- |
| `init` | Create the default Gateway JSON config if absent. |
| `plan` | Preview every file, dependency, config, and service action. |
| `bootstrap` | Apply the reviewed lifecycle plan transactionally. |
| `import-cliproxy-key` | Import the local CLIProxyAPI access key without printing it. |
| `catalog` | Regenerate the merged model catalog. |
| `install` | Patch owned Codex config fields and create a backup. |
| `repair-legacy-providers` | Redirect legacy persisted providers through the Gateway. |
| `serve` | Run the Gateway in the foreground. |
| `service-install` | Install or update the native per-user Gateway service. |
| `service-uninstall` | Remove service definitions; preserve configs and binaries. |
| `status` | Inspect Gateway and managed CLIProxyAPI service state. |
| `doctor` | Validate config, model cache, key, and CLIProxyAPI readiness. |
| `uninstall` | Restore the original Codex config when the safety check passes. |
| `version` | Print the Gateway version. |

## Codex configuration ownership

Installation does **not** replace all of `~/.codex/config.toml`. It prints the
backup path and manages only:

- top-level `model_provider`;
- top-level `model_catalog_json`;
- `[model_providers.codex-cliproxy-gateway]`;
- `base_url` for configured legacy provider IDs;
- an unprefixed selected third-party model ID when it needs the namespace.

Project trust entries, plugins, MCP servers, environment variables, and all
unrelated settings remain intact. If the file is changed outside this owned
surface after installation, automatic replacement or restoration fails closed
instead of overwriting unrelated edits.

## Service locations

| Platform | Gateway binary | Service definition |
| --- | --- | --- |
| macOS | `~/.local/bin/codex-cliproxy-gateway` | `~/Library/LaunchAgents/com.codex-cliproxy-gateway.plist` |
| Linux | `~/.local/bin/codex-cliproxy-gateway` | `~/.config/systemd/user/com.codex-cliproxy-gateway.service` |
| Windows | `%LOCALAPPDATA%\codex-cliproxy-gateway\bin\codex-cliproxy-gateway.exe` | `%LOCALAPPDATA%\codex-cliproxy-gateway\services\gateway-task.xml` |

Windows uses least-privilege per-user Task Scheduler logon tasks; administrator
access is not required for Gateway service installation.

## Health and troubleshooting

Loopback health endpoints:

- `GET /livez`: Gateway process liveness.
- `GET /readyz`: authenticated CLIProxyAPI models endpoint readiness.
- `GET /healthz`: compatibility alias for liveness.

Start every investigation with `status` and `doctor`.

- **Third-party model is absent from `/model`:** run official Codex once, rerun
  `catalog`, then fully restart Codex.
- **“model is not supported when using Codex with a ChatGPT account”:** the
  request bypassed the Gateway or used a stale provider/session. Confirm
  `model_provider = "codex-cliproxy-gateway"`, check service health, fully exit
  Codex, and start a new session. Older installations may need
  `repair-legacy-providers`.
- **`401 Unauthorized`:** distinguish the local Gateway-to-CLIProxyAPI key from
  the Kimi provider key inside CLIProxyAPI.
- **`404 /v1/responses`:** verify the provider base URL includes `/v1` and
  supports the Responses API.
- **Config changes appear ignored:** restart CLIProxyAPI after YAML changes and
  restart Codex after catalog or `config.toml` changes.

For foreground debugging, stop the installed Gateway service and run
`codex-cliproxy-gateway serve`.

## Security model

Official requests preserve incoming ChatGPT authentication and compressed body
bytes. For `cliproxy/*` requests, the Gateway:

1. checks the model against the local allowlist;
2. removes the namespace from the model ID;
3. strips ChatGPT OAuth, cookie, account, organization, project, and actor
   authorization headers;
4. adds only the local CLIProxyAPI access key;
5. forwards ordinary JSON to loopback CLIProxyAPI.

Important boundaries:

- Loopback blocks remote network access, but same-user local processes remain
  inside the trust boundary.
- Switching providers in one thread may disclose previous context to the newly
  selected provider. Start a new thread when the context is provider-sensitive.
- Third-party retention, quotas, billing, prompts, repository context, and tool
  output are governed by that provider, not by ChatGPT OAuth.
- Do not enable `allow_non_loopback` without a reviewed authentication and
  network boundary.
- Review logs and diagnostics before sharing them. Never attach credentials,
  private configs, or Codex session transcripts to an issue.

Gateway logs intentionally contain only route, model ID, status, duration, and
a correlation ID—not headers, bodies, or API keys.

## Platform support

| Capability | macOS | Linux | Windows |
| --- | --- | --- | --- |
| CI test/build | Yes | Yes | Yes |
| amd64 / arm64 release | Yes | Yes | Yes |
| Per-user service | LaunchAgent | systemd user | Task Scheduler |
| Managed CLIProxyAPI verification | Yes | Yes | Yes |
| Clean-machine runtime acceptance | Verified | Pending | Pending |

## Development

```sh
go test -race ./...
go vet ./...
go build ./cmd/codex-cliproxy-gateway
sh scripts/check-secrets.sh
sh scripts/build-release.sh v0.1.0-test .release-test
```

Keep provider behavior behind explicit configuration, add regression tests for
routing and credential boundaries, and document only behavior that was actually
verified.

## Uninstall

```sh
codex-cliproxy-gateway service-uninstall --cliproxy-mode managed
codex-cliproxy-gateway uninstall
```

`service-uninstall` preserves binaries and configs. `uninstall` restores the
recorded backup only when its safety check proves unrelated post-install edits
will not be overwritten.

## License

No license has been selected yet. The repository can be publicly readable, but
until a license is added, copyright law reserves reuse, modification, and
redistribution rights. Add an OSI-approved license before describing the
project as fully open source.
