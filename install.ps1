[CmdletBinding()]
param(
    [switch]$Yes
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$Repository = "jepson66/codex-cliproxy-gateway"
$ReleaseBase = "https://github.com/$Repository/releases/latest/download"
$CodexCache = Join-Path $HOME ".codex\models_cache.json"
$CLIProxyConfig = Join-Path $HOME ".cli-proxy-api\config.yaml"

function Stop-WithMessage([string]$Message) {
    throw "codex-cliproxy-gateway installer: $Message"
}

if (-not (Test-Path -LiteralPath $CodexCache -PathType Leaf)) {
    Stop-WithMessage @"
Codex's official model cache was not found at $CodexCache.
Install Codex, sign in with ChatGPT, run it successfully once, exit Codex, and rerun this installer.
Official install command:
powershell -ExecutionPolicy ByPass -c "irm https://chatgpt.com/codex/install.ps1 | iex"
"@
}

$Architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
switch ($Architecture) {
    "X64"   { $Arch = "amd64" }
    "Arm64" { $Arch = "arm64" }
    default { Stop-WithMessage "unsupported Windows architecture: $Architecture" }
}

$Asset = "codex-cliproxy-gateway_windows_$Arch.zip"
$TemporaryRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("codex-cliproxy-gateway-" + [guid]::NewGuid().ToString("N"))
$ArchivePath = Join-Path $TemporaryRoot $Asset
$ChecksumsPath = Join-Path $TemporaryRoot "checksums.txt"
$ExtractPath = Join-Path $TemporaryRoot "extract"

try {
    New-Item -ItemType Directory -Path $TemporaryRoot | Out-Null
    Invoke-WebRequest -UseBasicParsing -Uri "$ReleaseBase/$Asset" -OutFile $ArchivePath
    Invoke-WebRequest -UseBasicParsing -Uri "$ReleaseBase/checksums.txt" -OutFile $ChecksumsPath

    $ChecksumLine = Get-Content -LiteralPath $ChecksumsPath | Where-Object { $_ -match "\s$([regex]::Escape($Asset))$" } | Select-Object -First 1
    if (-not $ChecksumLine) {
        Stop-WithMessage "release checksum for $Asset is missing"
    }
    $Expected = ($ChecksumLine -split "\s+")[0].ToLowerInvariant()
    $Actual = (Get-FileHash -LiteralPath $ArchivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($Actual -ne $Expected) {
        Stop-WithMessage "SHA-256 mismatch for $Asset"
    }

    Expand-Archive -LiteralPath $ArchivePath -DestinationPath $ExtractPath
    $Gateway = Join-Path $ExtractPath "codex-cliproxy-gateway.exe"
    if (-not (Test-Path -LiteralPath $Gateway -PathType Leaf)) {
        Stop-WithMessage "release archive does not contain codex-cliproxy-gateway.exe"
    }

    & $Gateway plan --cliproxy-mode managed
    if ($LASTEXITCODE -ne 0) {
        Stop-WithMessage "installation plan failed"
    }

    if (-not $Yes) {
        $Answer = Read-Host "Apply this plan and install the Gateway plus CLIProxyAPI? [y/N]"
        if ($Answer -notmatch "^(?i:y|yes)$") {
            Stop-WithMessage "installation cancelled"
        }
    }

    & $Gateway bootstrap --cliproxy-mode managed --experimental-managed --yes
    if ($LASTEXITCODE -ne 0) {
        Stop-WithMessage "bootstrap failed"
    }

    $InstalledGateway = Join-Path $env:LOCALAPPDATA "codex-cliproxy-gateway\bin\codex-cliproxy-gateway.exe"
    & $InstalledGateway status
    Write-Host ""
    Write-Host "Installation finished. Run the following before opening Codex:"
    Write-Host "  Configure your own provider key manually in: $CLIProxyConfig"
    Write-Host "  The installer never reads, accepts, or writes provider API keys."
    Write-Host "  & `"$InstalledGateway`" doctor"
    Write-Host "For the optional billable Kimi check:"
    Write-Host "  & `"$InstalledGateway`" doctor --e2e --model cliproxy/kimi-k3"
    Write-Host "Desktop: fully restart Codex, then choose cliproxy/kimi-k3 from the model control beneath the composer."
    Write-Host "CLI: restart Codex, enter /model, then choose cliproxy/kimi-k3."
}
finally {
    if (Test-Path -LiteralPath $TemporaryRoot) {
        Remove-Item -LiteralPath $TemporaryRoot -Recurse -Force
    }
}
