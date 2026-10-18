<#
.SYNOPSIS
  Builds dist\errkb.exe for Windows.

.DESCRIPTION
  EKB-005 leaves this a shell: it only packages the Go binary that already
  exists. Code signing, installer assembly and the release layout belong to the
  packaging cards.

.PARAMETER Version
  Version stamped into the binary; "dev" is a local build.
#>
[CmdletBinding()]
param(
    [string]$Version = "dev"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$out = Join-Path $root "dist"
New-Item -ItemType Directory -Force -Path $out | Out-Null

go build -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $out "errkb.exe") ./cmd/errkb
Write-Host "built $(Join-Path $out 'errkb.exe') ($Version)"
