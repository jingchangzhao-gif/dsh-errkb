<#
.SYNOPSIS
  Regression tests for the release script's Git tag and worktree gates.

.DESCRIPTION
  Checks the Windows installer silent-mode contract, then creates an isolated
  temporary Git repository and proves four release-gate cases: a missing tag
  is rejected, a tag on another commit is rejected, a dirty worktree is
  rejected, and a clean HEAD carrying the expected tag passes. No tag or file
  in the caller's repository is changed.
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

$sourceScript = Join-Path $PSScriptRoot 'release.ps1'
$installerScript = Join-Path $PSScriptRoot 'installer.iss'
$vmAcceptanceScript = Join-Path $PSScriptRoot 'vm-acceptance.ps1'
$shell = Get-Command pwsh -ErrorAction Stop
$tempBase = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath()).TrimEnd('\') + '\'
$testRoot = Join-Path $tempBase ('errkb-release-test-' + [guid]::NewGuid().ToString('N'))
$version = '9.8.7'
$tag = "v$version"

function Invoke-TestGit([string[]]$GitArguments) {
    & git -C $testRoot @GitArguments | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "test setup git command failed: git $($GitArguments -join ' ')"
    }
}

function Write-TestFile([string]$RelativePath, [string]$Content) {
    $path = Join-Path $testRoot $RelativePath
    $parent = Split-Path -Parent $path
    if ($parent) {
        New-Item -ItemType Directory -Path $parent -Force | Out-Null
    }
    [System.IO.File]::WriteAllText($path, $Content, [System.Text.UTF8Encoding]::new($false))
}

function Assert-ReleaseCheck([string]$Name, [bool]$ShouldPass, [string]$ExpectedPattern) {
    $output = @(& $shell.Source -NoProfile -File (Join-Path $testRoot 'packaging\windows\release.ps1') `
            -Mode check -Version $version -Tag $tag -VmAcceptanceVersion $version 2>&1)
    $exitCode = $LASTEXITCODE
    $text = $output -join "`n"

    if ($ShouldPass -and $exitCode -ne 0) {
        throw "$Name failed unexpectedly (exit $exitCode):`n$text"
    }
    if (-not $ShouldPass -and $exitCode -eq 0) {
        throw "$Name passed unexpectedly:`n$text"
    }
    if ($text -notmatch $ExpectedPattern) {
        throw "$Name did not report /$ExpectedPattern/:`n$text"
    }
    Write-Output "PASS $Name"
}

function Assert-MismatchedBuildCommitRejected {
    $output = @(& $shell.Source -NoProfile -File (Join-Path $testRoot 'packaging\windows\release.ps1') `
            -Mode package -Version $version -Tag $tag -Commit 'deadbeef' -VmAcceptanceVersion $version 2>&1)
    $exitCode = $LASTEXITCODE
    $text = $output -join "`n"
    if ($exitCode -eq 0 -or $text -notmatch "does not match Git tag") {
        throw "mismatched explicit build commit was not rejected correctly (exit $exitCode):`n$text"
    }
    Write-Output 'PASS mismatched explicit build commit rejected'
}

function Assert-SilentInstallDoesNotLaunchConsole {
    $versionRun = Select-String -LiteralPath $installerScript `
        -Pattern '^Filename: "\{app\}\\errkb\.exe"; Parameters: "version";' |
        Select-Object -First 1
    if (-not $versionRun -or $versionRun.Line -notmatch '\bskipifsilent\b') {
        throw 'installer version command must include skipifsilent so unattended setup cannot hang on Windows Terminal'
    }
    Write-Output 'PASS silent install skips the interactive version command'
}

function Assert-AcceptanceWaitsForDirectProcessOnly {
    $acceptanceSource = Get-Content -LiteralPath $vmAcceptanceScript -Raw
    if ($acceptanceSource -match 'Start-Process\s+-FilePath\s+\$FilePath[^\r\n]*\s-Wait\b' -or
        $acceptanceSource -notmatch '\$process\.WaitForExit\(\)') {
        throw 'VM acceptance must wait for the installer process only; -Wait hangs on the persistent PostgreSQL descendant'
    }
    Write-Output 'PASS VM acceptance ignores persistent installer descendants'
}

try {
    Assert-SilentInstallDoesNotLaunchConsole
    Assert-AcceptanceWaitsForDirectProcessOnly
    New-Item -ItemType Directory -Path (Join-Path $testRoot 'packaging\windows') -Force | Out-Null
    Copy-Item -LiteralPath $sourceScript -Destination (Join-Path $testRoot 'packaging\windows\release.ps1')
    Write-TestFile 'CHANGELOG.md' "# Changelog`n`n## [$version] - 2099-01-01`n`n- Release gate fixture.`n"

    & git init --quiet --initial-branch=main $testRoot
    if ($LASTEXITCODE -ne 0) {
        throw 'git init failed'
    }
    Invoke-TestGit @('config', 'user.name', 'errkb release test')
    Invoke-TestGit @('config', 'user.email', 'release-test@example.invalid')
    Invoke-TestGit @('config', 'core.autocrlf', 'false')
    Invoke-TestGit @('add', '.')
    Invoke-TestGit @('commit', '--quiet', '-m', 'initial fixture')

    Assert-ReleaseCheck 'missing tag rejected' $false "does not exist"

    Invoke-TestGit @('tag', $tag)
    Write-TestFile 'second-commit.txt' "moves HEAD away from the tag`n"
    Invoke-TestGit @('add', 'second-commit.txt')
    Invoke-TestGit @('commit', '--quiet', '-m', 'move head')
    Assert-ReleaseCheck 'tag on another commit rejected' $false "points to commit .* but HEAD is"

    Invoke-TestGit @('tag', '--delete', $tag)
    Invoke-TestGit @('tag', $tag)
    Write-TestFile 'untracked.txt' "dirty worktree`n"
    Assert-ReleaseCheck 'dirty worktree rejected' $false "worktree is not clean"
    Remove-Item -LiteralPath (Join-Path $testRoot 'untracked.txt') -Force

    Assert-ReleaseCheck 'clean tagged HEAD accepted' $true "release check: OK"
    Assert-MismatchedBuildCommitRejected
    Write-Output 'release gate tests: 7 passed, 0 failed'
} finally {
    $resolvedRoot = [System.IO.Path]::GetFullPath($testRoot)
    if ($resolvedRoot.StartsWith($tempBase, [System.StringComparison]::OrdinalIgnoreCase) -and
        (Split-Path -Leaf $resolvedRoot).StartsWith('errkb-release-test-', [System.StringComparison]::Ordinal)) {
        Remove-Item -LiteralPath $resolvedRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
