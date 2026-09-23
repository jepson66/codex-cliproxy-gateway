# codex-cliproxy-gateway

[English](README.md) · [简体中文](README.zh-CN.md)

[![CI](https://github.com/jepson66/codex-cliproxy-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/jepson66/codex-cliproxy-gateway/actions/workflows/ci.yml)
[![Platforms](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](#status)

An experimental local gateway that keeps Codex's official GPT models and adds
CLIProxyAPI models to the same `/model` picker. Official requests keep ChatGPT
OAuth; namespaced `cliproxy/*` requests go to a local CLIProxyAPI instance.

```text
Codex CLI / Desktop
        │ /model
        ▼
codex-cliproxy-gateway (127.0.0.1:8765)
        ├── official models ──► ChatGPT Codex (OAuth)
        └── cliproxy/* ───────► CLIProxyAPI (127.0.0.1:8317)
                                   └── Kimi / other providers
```

## Status

- Proof of concept; managed CLIProxyAPI mode is explicitly experimental.
- Kimi currently uses a manually entered API key. Kimi OAuth/subscription is
  not included.
- CI and release builds cover macOS, Linux, and Windows on amd64 and arm64.
- New providers need their own streaming, tool-call, image, context, and
  reasoning tests.

## Install a prebuilt release

Prerequisites: install Codex, sign in with ChatGPT, run one official model once,
create a Kimi API key, and fully exit Codex.

The Gateway is written in Go, but release archives contain a compiled,
standalone executable. The installer does not check, install, upgrade, or
remove Go. An existing Go installation is left untouched. Go is required only
when building the project from source.

### Windows

Inspect the script first (recommended):

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

The installer downloads the prebuilt Gateway, verifies SHA-256, and installs the
pinned CLIProxyAPI release as a per-user service. It never reads, accepts, or
writes provider keys. Existing CLIProxyAPI configuration is preserved.

## Configure Kimi

After installation, edit the local CLIProxyAPI file yourself:

- macOS/Linux: `~/.cli-proxy-api/config.yaml`
- Windows: `%USERPROFILE%\.cli-proxy-api\config.yaml`

Add an OpenAI-compatible provider and replace the placeholder with your own key:

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

Never commit this file or paste a real key into an issue. The Kimi provider key
and the local CLIProxyAPI access key are different secrets.

## Use `/model`

Check the local chain:

```sh
codex-cliproxy-gateway status
codex-cliproxy-gateway doctor
```

Optional end-to-end check (may incur a Kimi charge):

```sh
codex-cliproxy-gateway doctor --e2e --model cliproxy/kimi-k3
```

Fully exit Codex, start a new session, open `/model`, and choose
`cliproxy/kimi-k3`. Official GPT models remain in the same picker.

If the model is missing, run the official model once, run `catalog`, and restart
Codex. If Codex says the model is unsupported for a ChatGPT account, the request
bypassed the Gateway or used stale configuration; verify
`model_provider = "codex-cliproxy-gateway"` and start a new session.

## Useful commands

| Command | Purpose |
| --- | --- |
| `plan` | Preview files, dependencies, config, and service actions. |
| `bootstrap` | Apply a reviewed installation plan. |
| `catalog` | Regenerate the merged model catalog. |
| `status` / `doctor` | Check service and provider readiness. |
| `serve` | Run the Gateway in the foreground. |
| `uninstall` | Restore the recorded Codex configuration backup safely. |

## Development

Developers need Go `1.26` or newer:

```sh
go test -race ./...
go vet ./...
go build ./cmd/codex-cliproxy-gateway
sh scripts/check-secrets.sh
```

## Security and license

The Gateway listens on loopback by default, allowlists `cliproxy/*` model IDs,
strips ChatGPT credentials before third-party forwarding, and logs no headers,
bodies, or API keys. Third-party retention, billing, quotas, prompts, and tool
output remain governed by that provider.

No license has been selected yet. Public visibility does not grant reuse or
redistribution rights until a license is added.
