<#
.SYNOPSIS
  Builds the Windows release binary with an embedded resource block and version
  info, then records its checksum.

.DESCRIPTION
  Produces:
    dist\errkb.exe              the release binary
    dist\errkb.exe.sha256       sha256sum-format checksum line
    dist\versioninfo.json       the version document that was embedded

  Reproducibility
  ---------------
  The same source, the same parameters and the same toolchain produce the same
  bytes. The script keeps that promise with three choices:

    * build time is the commit timestamp (git log -1 --format=%cI), never the
      wall clock;
    * the binary is built with -trimpath -buildvcs=false and an empty linker
      build id, so neither the checkout path nor VCS metadata leaks in;
    * resources are regenerated from packaging\windows\versioninfo.json instead
      of being copied from an old binary.

  CGO_ENABLED=0: errkb is pure Go (the Named Pipe client, the PostgreSQL driver
  and the Win32 calls are all pure Go), so the release binary carries no C
  runtime. It still needs PostgreSQL and the connectors described in
  docs\implementation-plan.md.

  Toolchain: Go (the version pinned by go.mod) and go-winres
  (go install github.com/tc-hib/go-winres@latest). Both are build-time tools;
  the built errkb.exe runs on a clean Windows machine without Go installed.

.PARAMETER Version
  Release version, for example 0.3.0. Defaults to the nearest git tag, and to
  "dev" when no tag describes HEAD.

.PARAMETER Commit
  Commit stamp. Defaults to the short HEAD hash.

.PARAMETER BuildTime
  RFC 3339 build stamp. Defaults to the committer timestamp of HEAD.

.PARAMETER Output
  Directory for the artifacts. Defaults to dist under the repository root.

.EXAMPLE
  pwsh -File packaging\windows\build.ps1 -Version 1.0.0
#>
param(
    [string]$Version = "",
    [string]$Commit = "",
    [string]$BuildTime = "",
    [string]$Output = ""
)

$ErrorActionPreference = "Stop"

$root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
if (-not $Output) { $Output = Join-Path $root "dist" }
$metadataPath = Join-Path $PSScriptRoot "versioninfo.json"

function Invoke-Checked {
    param([string]$Program, [string[]]$Arguments, [string]$What)
    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$What failed with exit code $LASTEXITCODE"
    }
}

# 1. Resolve the stamps. Everything embedded in the binary comes from here, so a
#    release build only needs -Version; commit and build time are derivable.
Push-Location $root
try {
    if (-not $Version) {
        $tag = $null
        try { $tag = (& git describe --tags --abbrev=0 2>$null) } catch { $tag = $null }
        if ($LASTEXITCODE -ne 0 -or -not $tag) { $tag = "dev" }
        $Version = ($tag -join "").Trim()
    }
    if (-not $Commit) {
        $Commit = ((& git rev-parse --short HEAD) -join "").Trim()
        if ($LASTEXITCODE -ne 0) { throw "git rev-parse failed" }
    }
    if (-not $BuildTime) {
        $BuildTime = ((& git log -1 --format=%cI) -join "").Trim()
        if ($LASTEXITCODE -ne 0) { throw "git log failed" }
    }
} finally {
    Pop-Location
}

# 2. Locate the build tools.
$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) { throw "go is not on PATH; install the Go toolchain (see docs/implementation-plan.md section 7)" }
$winres = Get-Command go-winres -ErrorAction SilentlyContinue
if (-not $winres) {
    $candidate = Join-Path (go env GOPATH) "bin\go-winres.exe"
    if (Test-Path $candidate) {
        $winres = Get-Item $candidate
    } else {
        throw "go-winres is not installed; run: go install github.com/tc-hib/go-winres@latest"
    }
}
$winresPath = if ($winres -is [System.Management.Automation.ApplicationInfo]) { $winres.Source } else { $winres.FullName }

$metadata = Get-Content -Raw -Path $metadataPath | ConvertFrom-Json
$versionInfo = $metadata.RT_VERSION.'#1'.'0000'
if (-not $versionInfo -or -not $versionInfo.info.'0409') {
    throw "versioninfo.json is missing RT_VERSION/#1/0000/info/0409"
}
$strings = $versionInfo.info.'0409'

function ConvertTo-FixedVersion {
    param([string]$Value)
    if ($Value -notmatch '^[0-9]+(\.[0-9]+){0,3}(-[0-9A-Za-z.-]+)?$') {
        # "dev" and other non-numeric stamps cannot be written into the fixed
        # version fields; the string fields still carry the readable value.
        return "0.0.0.0"
    }
    $core = ($Value -split "-")[0]
    $parts = @($core.Split("."))
    $fixed = New-Object System.Collections.Generic.List[string]
    foreach ($part in $parts) {
        if ($fixed.Count -eq 4) { break }
        $fixed.Add($part)
    }
    while ($fixed.Count -lt 4) { $fixed.Add("0") }
    return ($fixed -join ".")
}

# Reject stamps that would silently produce a misleading resource block. "dev"
# is the documented local build, and a suffix such as 0.3.0-rc.1 is allowed.
if ($Version -ne "dev" -and $Version -notmatch '^[0-9]+(\.[0-9]+){0,3}(-[0-9A-Za-z.-]+)?$') {
    throw "invalid version '$Version': use digits and dots, optionally a suffix (for example 0.3.0 or 0.3.0-rc.1)"
}

# Fill the version into the resource template. The strings and the fixed version
# both come from the same -Version value, so the file properties and the binary
# can never disagree.
$fixedVersion = ConvertTo-FixedVersion $Version
$versionInfo.fixed.file_version = $fixedVersion
$versionInfo.fixed.product_version = $fixedVersion
$strings.FileVersion = $Version
$strings.ProductVersion = $Version

New-Item -ItemType Directory -Force -Path $Output | Out-Null
$binary = Join-Path $Output "errkb.exe"
$checksumPath = Join-Path $Output "errkb.exe.sha256"
$resolvedVersionPath = Join-Path $Output "versioninfo.json"
$resolvedWinresPath = Join-Path $Output "winres.json"
$sysoPrefix = Join-Path $root "cmd\errkb\versioninfo"

try {
    # 3. Generate the resource block (version info + manifest) inside the command
    #    package so `go build` picks it up, then remove it after the build: the
    #    repository keeps the source of truth, not a generated binary object.
    #    Write UTF-8 without a BOM: Windows PowerShell 5.1's `-Encoding utf8`
    #    adds one, and go-winres refuses a BOM-prefixed JSON document.
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($resolvedWinresPath, ($metadata | ConvertTo-Json -Depth 8), $utf8NoBom)
    Invoke-Checked $winresPath @(
        "make",
        "--arch", "amd64",
        "--in", $resolvedWinresPath,
        "--out", $sysoPrefix
    ) "go-winres make"

    # 4. Build.
    $ldflags = "-s -w -buildid= -X main.version=$Version -X main.commit=$Commit -X main.buildTime=$BuildTime"
    $saved = @{
        CGO_ENABLED = $env:CGO_ENABLED
        GOOS        = $env:GOOS
        GOARCH      = $env:GOARCH
    }
    $env:CGO_ENABLED = "0"
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    try {
        Invoke-Checked $go.Source @(
            "build",
            "-trimpath",
            "-buildvcs=false",
            "-o", $binary,
            "-ldflags", $ldflags,
            "./cmd/errkb"
        ) "go build"
    } finally {
        $env:CGO_ENABLED = $saved.CGO_ENABLED
        $env:GOOS = $saved.GOOS
        $env:GOARCH = $saved.GOARCH
    }

    # 5. Checksum and the version document that matches the binary.
    $hash = (Get-FileHash -Algorithm SHA256 -Path $binary).Hash.ToLowerInvariant()
    Set-Content -Path $checksumPath -Value ("$hash  errkb.exe") -NoNewline -Encoding ascii
    $document = [ordered]@{
        product     = $strings.ProductName
        company     = $strings.CompanyName
        description = $strings.FileDescription
        copyright   = $strings.LegalCopyright
        version     = $Version
        commit      = $Commit
        build_time  = $BuildTime
    }
    [System.IO.File]::WriteAllText($resolvedVersionPath, ($document | ConvertTo-Json), $utf8NoBom)

    $size = (Get-Item $binary).Length
    Write-Output ""
    Write-Output ("built  {0}  ({1:N0} bytes)" -f $binary, $size)
    Write-Output ("sha256 {0}" -f $hash)
    Write-Output ("docs   {0}" -f $resolvedVersionPath)
} catch {
    Write-Error $_
    exit 1
} finally {
    Get-ChildItem -Path (Join-Path $root "cmd\errkb") -Filter "versioninfo*.syso" -ErrorAction SilentlyContinue |
        Remove-Item -Force -ErrorAction SilentlyContinue
}
