#!/bin/sh
set -eu

version="${1:-}"
output_directory="${2:-dist}"
if [ -z "$version" ]; then
  printf '%s\n' 'usage: scripts/build-release.sh <version> [output-directory]' >&2
  exit 2
fi

case "$output_directory" in
  /*) ;;
  *) output_directory="$PWD/$output_directory" ;;
esac
mkdir -p "$output_directory"
temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/codex-cliproxy-release.XXXXXX")"
cleanup() {
  rm -rf -- "$temporary_root"
}
trap cleanup EXIT HUP INT TERM

checksum_file="$output_directory/checksums.txt"
: >"$checksum_file"

for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
  goos="${target%/*}"
  goarch="${target#*/}"
  executable="codex-cliproxy-gateway"
  extension="tar.gz"
  if [ "$goos" = windows ]; then
    executable="codex-cliproxy-gateway.exe"
    extension="zip"
  fi
  build_directory="$temporary_root/${goos}-${goarch}"
  mkdir -p "$build_directory"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
    -trimpath \
    -ldflags "-s -w -X codex-cliproxy-gateway/internal/app.Version=$version" \
    -o "$build_directory/$executable" \
    ./cmd/codex-cliproxy-gateway

  asset="codex-cliproxy-gateway_${goos}_${goarch}.${extension}"
  if [ "$goos" = windows ]; then
    (cd "$build_directory" && zip -q "$output_directory/$asset" "$executable")
  else
    tar -C "$build_directory" -czf "$output_directory/$asset" "$executable"
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    digest="$(sha256sum "$output_directory/$asset" | awk '{print $1}')"
  else
    digest="$(shasum -a 256 "$output_directory/$asset" | awk '{print $1}')"
  fi
  printf '%s  %s\n' "$digest" "$asset" >>"$checksum_file"
done
