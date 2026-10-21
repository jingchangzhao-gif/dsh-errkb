<#
.SYNOPSIS
  Preview or remove confirmed local-only generated and probe files.
  预览或删除已经确认只属于本机的生成文件与探针文件。

.DESCRIPTION
  The script is deliberately limited to direct children of this repository.
  Without -Apply it only prints the targets. With -Apply it verifies every
  resolved path is inside the repository before deleting it.

  此脚本只处理本仓库中列出的固定目标。默认只打印；传入 -Apply 后，仍会先确认
  每个规范化路径位于仓库内部，再执行删除。
#>

[CmdletBinding()]
param(
    [switch]$Apply
)

$ErrorActionPreference = 'Stop'
$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..')).TrimEnd('\')

$targets = @(
    [pscustomobject]@{ Path = 'CUsersADMINI~1AppDataLocalTempclaude-probefired.txt'; Kind = 'file' },
    [pscustomobject]@{ Path = 'CUsersADMINI~1AppDataLocalTempclaude-probefired2.txt'; Kind = 'file' },
    [pscustomobject]@{ Path = 'coverage'; Kind = 'directory' },
    [pscustomobject]@{ Path = 'dist'; Kind = 'directory' }
)

function Resolve-WorkspaceTarget([string]$RelativePath) {
    $candidate = [System.IO.Path]::GetFullPath((Join-Path $repoRoot $RelativePath))
    if (-not $candidate.StartsWith($repoRoot + '\', [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing a cleanup target outside the repository: $candidate"
    }
    return $candidate
}

Write-Output ("cleanup mode: " + $(if ($Apply) { 'APPLY' } else { 'PREVIEW' }))
foreach ($target in $targets) {
    $resolved = Resolve-WorkspaceTarget $target.Path
    if (-not (Test-Path -LiteralPath $resolved)) {
        Write-Output "absent  $resolved"
        continue
    }

    if (-not $Apply) {
        Write-Output "remove  $resolved"
        continue
    }

    if ($target.Kind -eq 'directory') {
        if (-not (Test-Path -LiteralPath $resolved -PathType Container)) {
            throw "Expected a directory but found another file type: $resolved"
        }
        Remove-Item -LiteralPath $resolved -Recurse -Force
    }
    else {
        if (-not (Test-Path -LiteralPath $resolved -PathType Leaf)) {
            throw "Expected a file but found another file type: $resolved"
        }
        Remove-Item -LiteralPath $resolved -Force
    }
    Write-Output "removed $resolved"
}
