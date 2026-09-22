# codex-cliproxy-gateway

Experimental local gateway that keeps Codex's official GPT model IDs on the
ChatGPT OAuth path and routes only `cliproxy/*` model IDs to CLIProxyAPI.

```text
Codex CLI / Desktop /model
            |
            v
codex-cliproxy-gateway
  |                       |
  | gpt-*                 | cliproxy/*
  v                       v
ChatGPT OAuth         CLIProxyAPI
                      Kimi / Claude / Gemini
```

## Current status

This is a proof of concept. Codex CLI 0.153.2 on macOS has been verified with
both an official GPT model and `cliproxy/kimi-k3`, including Codex's zstd
request compression. It has routing tests, reversible Codex configuration
installation, a merged model catalog, and a per-user LaunchAgent. Desktop uses
the same user-level Codex configuration, but each Desktop release still needs
a real UI acceptance check before production use.

## Security boundary

The gateway listens on loopback by default. Official requests retain the
incoming ChatGPT OAuth headers. For `cliproxy/*` requests it removes ChatGPT
OAuth, account, cookie, organization, project, and actor-authorization headers,
then uses the local CLIProxyAPI access key for that hop. The key is loaded from
`CLIPROXY_API_KEY` when set, otherwise from a mode-0600 file under
`~/.codex/codex-cliproxy-gateway/`. It is not the Kimi provider key. Secrets are
never written to the gateway JSON config or request logs.

Non-loopback listeners are rejected unless `allow_non_loopback` is explicitly
enabled. Third-party model ids are allowlisted from the Gateway configuration;
an unknown `cliproxy/*` id fails closed instead of being forwarded.

Codex compresses some OpenAI-authenticated requests with zstd. The gateway
keeps compressed official requests byte-for-byte intact and uses the local
`zstd` command only to inspect routing; prefixed third-party requests are sent
to CLIProxyAPI as ordinary JSON. `doctor` verifies that `zstd` is available.

## Build and test

```sh
go test ./...
go build ./cmd/codex-cliproxy-gateway
```

## Configuration

Create the default config under `~/.codex`:

```sh
./codex-cliproxy-gateway init
./codex-cliproxy-gateway import-cliproxy-key
```

The import command copies the first local access key from
`~/.cli-proxy-api/config.yaml` into the private gateway key file without
printing it.

Edit `~/.codex/codex-cliproxy-gateway.json` to add prefixed models. Model IDs in
that file do not include `cliproxy/`; the catalog generator adds the prefix.
Configuration schema version 1 requires each new model to declare its route,
upstream model id, wire API, modalities, and capabilities explicitly. Legacy
PoC configuration without `schema_version` is migrated in memory with
behavior-preserving defaults; saving it writes the current schema.

The gateway reads the current official models from
`~/.codex/models_cache.json`, preserving the exact GPT entries that Codex
already fetched. It writes a merged catalog to
`~/.codex/model-catalogs/codex-cliproxy-gateway.json`.

Third-party catalog entries keep the host's tool, permission, and collaboration
instructions, but replace the official GPT identity paragraph with neutral
guidance. The gateway never rewrites model responses. A third-party model is
instructed to identify its underlying model/provider truthfully and to
distinguish that identity from the Codex host.

## Install and run

`install` backs up `~/.codex/config.toml`, generates the merged catalog, and
sets one custom provider with `requires_openai_auth = true`:

```sh
./codex-cliproxy-gateway install
./codex-cliproxy-gateway service-install
```

`install` does not replace `config.toml` with a template. It preserves
unrelated settings and manages only the top-level `model_provider` and
`model_catalog_json` keys, the
`[model_providers.codex-cliproxy-gateway]` table, configured legacy provider
`base_url` values, and an unprefixed selected third-party model ID when one is
present. It prints this scope, the original backup path, and maintenance
commands after installation. If the file contains other post-install edits,
the installer refuses to overwrite it; use `catalog` when only the generated
model list needs updating.

Maintenance commands:

```sh
# After editing the gateway model list:
./codex-cliproxy-gateway catalog
# Restart Codex after catalog changes.

# After rebuilding or updating the Gateway binary:
./codex-cliproxy-gateway service-install

# Restore the original pre-install Codex config (only when the safety check
# confirms no unrelated post-install edits would be lost):
./codex-cliproxy-gateway uninstall
```

If the Gateway was installed before legacy Codex sessions were supported, run:

```sh
./codex-cliproxy-gateway repair-legacy-providers
```

This creates a fresh backup and changes only the `base_url` of provider IDs in
`legacy_provider_ids` (by default, `openai-http`). Project trust entries,
plugins, environment variables, model selection, and all other Codex settings
are preserved.

On macOS, `service-install` copies the current binary to `~/.local/bin`, writes
a per-user LaunchAgent, and starts it. For foreground debugging, use `serve`
instead. `service-uninstall` removes only the LaunchAgent; it preserves the
binary and configuration.

The installer is reversible while the installed Codex config has not been
edited afterward:

```sh
./codex-cliproxy-gateway uninstall
```

Use `doctor` before starting:

```sh
./codex-cliproxy-gateway doctor
```

`doctor` authenticates against CLIProxyAPI's models endpoint without invoking a
model. An optional end-to-end check makes one small, potentially billable model
request and therefore must be requested explicitly:

```sh
./codex-cliproxy-gateway doctor --e2e --model cliproxy/kimi-k3
```

Runtime health endpoints are split by purpose: `/livez` checks only the
Gateway process, while `/readyz` verifies that the CLIProxyAPI key and models
endpoint are usable. `/healthz` remains a compatibility alias for liveness.

Restart Codex after catalog changes because Codex loads
`model_catalog_json` at startup.

After restart, `/model` should show the unchanged official model IDs plus
`cliproxy/kimi-k3`. Kimi exposes only the `none` reasoning option because its
upstream manages reasoning automatically.

Gateway logs are written to
`~/.codex/codex-cliproxy-gateway/gateway.err.log`. They include only the route
and model id, never request headers or API keys.

## Deliberate PoC limitations

- WebSocket transport is disabled; Codex uses HTTP/SSE for both branches.
- The official branch assumes the current ChatGPT Codex endpoint remains
  `https://chatgpt.com/backend-api/codex`.
- The model catalog is refreshed by rerunning `catalog` or `install`.
- Desktop behavior still needs a real end-to-end validation. If Desktop rejects
  a prefixed model before any gateway request is logged, a gateway alone cannot
  satisfy that Desktop version.

## Trust and compatibility notes

- Switching providers inside one thread can send that thread's prior context to
  the newly selected provider. Start a new thread before switching when the
  existing conversation contains provider-sensitive data.
- Loopback limits network exposure, but processes running as the same OS user
  remain inside the trust boundary and can potentially use or inspect local
  credentials.
- Each added model needs its own Responses API, streaming, tool-call, image,
  context-window, and reasoning-setting acceptance tests. Do not infer one
  model's capabilities from another model or from the official GPT template.
- Codex, its model-catalog schema, and CLIProxyAPI are independently versioned.
  Regenerate the catalog and repeat CLI and Desktop acceptance tests after any
  upgrade.
- Provider credentials, prompts, repository context, tool output, retention,
  quotas, and billing are governed by the selected upstream provider, not by
  ChatGPT OAuth.
