# codex-cliproxy-gateway

[English](README.md) · [简体中文](README.zh-CN.md)

[![CI](https://github.com/jepson66/codex-cliproxy-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/jepson66/codex-cliproxy-gateway/actions/workflows/ci.yml)
[![Platforms](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](#status)

An experimental local gateway that keeps Codex's official GPT models and adds
CLIProxyAPI models to the same model catalog. Codex Desktop uses the model
control beneath the composer; Codex CLI uses `/model`. Official requests keep
ChatGPT OAuth; namespaced `cliproxy/*` requests go to local CLIProxyAPI.

```text
Codex Desktop / CLI
        │ Desktop: model control beneath the composer
        │ CLI: /model
        ▼
codex-cliproxy-gateway (127.0.0.1:8765)
        ├── official models ──► ChatGPT Codex (OAuth)
        └── cliproxy/* ───────► CLIProxyAPI (127.0.0.1:8317)
                                   └── Kimi / other providers
```

## Status

- Proof of concept; managed CLIProxyAPI mode is explicitly experimental.
- Kimi Code supports device OAuth; manually configured subscription API keys
  remain supported.
- CI and release builds cover macOS, Linux, and Windows on amd64 and arm64.
- New providers need their own streaming, tool-call, image, context, and
  reasoning tests.

## Install a prebuilt release

Prerequisites: install Codex, sign in with ChatGPT, run one official model once,
and fully exit Codex before installation.

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

## Sign in to Kimi

Recommended: authorize this device with Kimi Code after installation:

```sh
codex-cliproxy-gateway login kimi-code
```

The command opens a one-time Kimi authorization URL, waits for approval, and
saves an OAuth credential in the configured CLIProxyAPI auth directory. It does
not invoke or depend on Kimi CLI, and never prints the access or refresh token.
CLIProxyAPI hot-loads the new credential. On a remote or headless machine, use
`codex-cliproxy-gateway login --no-browser kimi-code` and open the printed URL
yourself.

To verify that CLIProxyAPI loaded either an OAuth credential or an API key:

```sh
codex-cliproxy-gateway auth-status kimi-code
```

Codex does not notify custom providers when a model is merely selected, so a
missing-login hint appears after the first prompt is sent, not when the picker
item is clicked.

### Optional API-key fallback

The previous API key method remains supported. Edit the local CLIProxyAPI file
yourself:

- macOS/Linux: `~/.cli-proxy-api/config.yaml`
- Windows: `%USERPROFILE%\.cli-proxy-api\config.yaml`

Add this OpenAI-compatible provider and replace the placeholder with your own
Kimi Code key. This key uses Kimi Code membership quota; it is not a Moonshot
Open Platform pay-as-you-go key. Keep `priority: 0` and the same `kimi-k3`
alias.

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
        thinking:
          levels: [low, high, max]
```

Never commit this file or paste a real key into an issue. The Kimi provider key
and the local CLIProxyAPI access key are different secrets.

Codex always shows one model name: `cliproxy/kimi-k3`. Kimi Code OAuth
credentials use priority `100`; the API-key provider above uses priority `0`.
CLIProxyAPI therefore uses OAuth exclusively while it is available and falls
back to the API key only when OAuth is missing or unavailable. Configure at
most one priority-`0` API-key provider for the `kimi-k3` alias; do not enable a
Kimi Code key and a Moonshot Open Platform key under that alias at the same
time. Existing OAuth files are upgraded safely when the Gateway starts.

Kimi models stay visible in the Codex picker before credentials are configured.
On the first request, the Gateway checks CLIProxyAPI without reading provider
secrets. If Kimi is missing or rejects the credential, Codex CLI and Desktop
show the `login kimi-code` command plus the API key fallback path.

## Select a model

Check the local chain:

```sh
codex-cliproxy-gateway status
codex-cliproxy-gateway doctor
```

Optional end-to-end check (may incur a Kimi charge):

```sh
codex-cliproxy-gateway doctor --e2e --model cliproxy/kimi-k3
```

**Codex Desktop:** fully quit and reopen the app, start a new chat, then use the
model and reasoning control beneath the composer to choose
`cliproxy/kimi-k3`. `Ctrl+Shift+M` opens the model picker.

**Codex CLI:** restart Codex, enter `/model`, and choose
`cliproxy/kimi-k3`. Official GPT models remain in the same picker. The OAuth
path has been verified with this 1M-context model and requires an eligible Kimi
membership. The 256K alias is intentionally omitted: a normal Codex request can
exceed that limit after tool schemas are included. Kimi reasoning levels are
translated to its native `thinking` protocol.

See [OpenAI's model selection documentation](https://learn.chatgpt.com/docs/models#choose-a-model)
for the current Desktop control.

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
| `login` / `auth-status` | Authorize Kimi or check whether CLIProxyAPI loaded a credential. |
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

## Security

The Gateway listens on loopback by default, allowlists `cliproxy/*` model IDs,
strips ChatGPT credentials before third-party forwarding, and logs no headers,
bodies, or API keys. Third-party retention, billing, quotas, prompts, and tool
output remain governed by that provider.
