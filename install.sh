#!/bin/sh
set -eu

repository="jepson66/codex-cliproxy-gateway"
release_base="https://github.com/${repository}/releases/latest/download"
assume_yes=false

for argument in "$@"; do
  case "$argument" in
    --yes) assume_yes=true ;;
    *) printf 'Unknown argument: %s\n' "$argument" >&2; exit 2 ;;
  esac
done

codex_cache="${HOME}/.codex/models_cache.json"
if [ ! -f "$codex_cache" ]; then
  printf '%s\n' "Codex's official model cache was not found at $codex_cache." >&2
  printf '%s\n' 'Install Codex, sign in with ChatGPT, run it successfully once, exit Codex, and rerun this installer.' >&2
  printf '%s\n' 'Official install command: curl -fsSL https://chatgpt.com/codex/install.sh | sh' >&2
  exit 1
fi

case "$(uname -s)" in
  Darwin) operating_system="darwin" ;;
  Linux) operating_system="linux" ;;
  *) printf 'Unsupported operating system: %s\n' "$(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) architecture="amd64" ;;
  arm64|aarch64) architecture="arm64" ;;
  *) printf 'Unsupported architecture: %s\n' "$(uname -m)" >&2; exit 1 ;;
esac

asset="codex-cliproxy-gateway_${operating_system}_${architecture}.tar.gz"
temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/codex-cliproxy-gateway.XXXXXX")"
cleanup() {
  rm -rf -- "$temporary_root"
}
trap cleanup EXIT HUP INT TERM

curl -fL --retry 3 --proto '=https' --tlsv1.2 -o "$temporary_root/$asset" "$release_base/$asset"
curl -fL --retry 3 --proto '=https' --tlsv1.2 -o "$temporary_root/checksums.txt" "$release_base/checksums.txt"

expected="$(awk -v asset="$asset" '$2 == asset { print $1; exit }' "$temporary_root/checksums.txt")"
if [ -z "$expected" ]; then
  printf 'Release checksum for %s is missing.\n' "$asset" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$temporary_root/$asset" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "$temporary_root/$asset" | awk '{print $1}')"
fi
if [ "$actual" != "$expected" ]; then
  printf 'SHA-256 mismatch for %s.\n' "$asset" >&2
  exit 1
fi

mkdir "$temporary_root/extract"
tar -xzf "$temporary_root/$asset" -C "$temporary_root/extract"
gateway="$temporary_root/extract/codex-cliproxy-gateway"
if [ ! -x "$gateway" ]; then
  printf '%s\n' 'Release archive does not contain an executable Gateway.' >&2
  exit 1
fi

"$gateway" plan --cliproxy-mode managed
if [ "$assume_yes" != true ]; then
  if [ ! -r /dev/tty ]; then
    printf '%s\n' 'Interactive confirmation requires a terminal; download the script and run it directly, or pass --yes after reviewing it.' >&2
    exit 1
  fi
  printf 'Apply this plan and install the Gateway plus CLIProxyAPI? [y/N] ' >/dev/tty
  IFS= read -r answer </dev/tty
  case "$answer" in y|Y|yes|YES|Yes) ;; *) printf '%s\n' 'Installation cancelled.' >&2; exit 1 ;; esac
fi

"$gateway" bootstrap --cliproxy-mode managed --experimental-managed --yes

installed_gateway="${HOME}/.local/bin/codex-cliproxy-gateway"
"$installed_gateway" status
printf '\n%s\n' 'Installation finished. Run:'
printf '  %s login kimi-code\n' "$installed_gateway"
printf '%s\n' '  Or configure your own provider API key manually in ~/.cli-proxy-api/config.yaml.'
printf '%s\n' '  OAuth tokens are saved privately in CLIProxyAPI auth-dir; the installer never asks for or prints tokens or API keys.'
printf '  %s doctor\n' "$installed_gateway"
printf '%s\n' 'For the optional billable Kimi check:'
printf '  %s doctor --e2e --model cliproxy/kimi-k3\n' "$installed_gateway"
printf '%s\n' 'Desktop: fully restart Codex, then choose cliproxy/kimi-k3 from the model control beneath the composer.'
printf '%s\n' 'CLI: restart Codex, enter /model, then choose cliproxy/kimi-k3.'
