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
$ciAcceptanceScript = Join-Path $PSScriptRoot 'ci-acceptance.ps1'
$recoveryAcceptanceScript = Join-Path $PSScriptRoot 'recovery-acceptance.ps1'
$postgresScript = Join-Path $PSScriptRoot 'postgres.ps1'
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
            -Mode check -Version $version -Tag $tag -WindowsAcceptanceVersion $version 2>&1)
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
            -Mode package -Version $version -Tag $tag -Commit 'deadbeef' -WindowsAcceptanceVersion $version 2>&1)
    $exitCode = $LASTEXITCODE
    $text = $output -join "`n"
    if ($exitCode -eq 0 -or $text -notmatch "does not match Git tag") {
        throw "mismatched explicit build commit was not rejected correctly (exit $exitCode):`n$text"
    }
    Write-Output 'PASS mismatched explicit build commit rejected'
}

function Assert-MissingWindowsAcceptanceRejected {
    $output = @(& $shell.Source -NoProfile -File (Join-Path $testRoot 'packaging\windows\release.ps1') `
            -Mode check -Version $version -Tag $tag -WindowsAcceptanceVersion '' 2>&1)
    $exitCode = $LASTEXITCODE
    $text = $output -join "`n"
    if ($exitCode -eq 0 -or $text -notmatch 'no Windows CI acceptance result') {
        throw "missing Windows acceptance was not rejected correctly (exit $exitCode):`n$text"
    }
    Write-Output 'PASS missing Windows CI acceptance rejected'
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

function Assert-SilentInstallReusesRetainedCluster {
    $installerSource = Get-Content -LiteralPath $installerScript -Raw
    if ($installerSource -notmatch '\(not WizardSilent\(\)\) and\s*\(MsgBox\(' -or
        $installerSource -notmatch 'ClusterDataPresent\(\)') {
        throw 'silent reinstall must reuse retained cluster data without opening an unsuppressible script dialog'
    }
    Write-Output 'PASS silent reinstall reuses retained cluster data'
}

function Assert-SilentUninstallPersistsRetentionChoice {
    $installerSource = Get-Content -LiteralPath $installerScript -Raw
    $postgresSource = Get-Content -LiteralPath $postgresScript -Raw
    foreach ($required in @('UninstallChoicePresent', '.errkb-delete-data', '.errkb-keep-data', 'UninstallSilent()')) {
        if ($installerSource -notmatch [regex]::Escape($required)) {
            throw "silent uninstall must persist its retention choice across the uninstaller clone (missing $required)"
        }
    }
    if ($postgresSource -notmatch 'if \(Test-Path \$keepMarker\)' -or
        $postgresSource -notmatch 'if \(Test-Path \$marker\)') {
        throw 'postgres.ps1 must clear both transient choice markers before Inno final cleanup'
    }
    if ($installerSource -notmatch 'Type:\s*dirifempty;\s*Name:\s*"\{app\}"') {
        throw 'the uninstaller must register a safe final cleanup for an empty application root'
    }
    Write-Output 'PASS silent uninstall persists its retention choice'
}

function Assert-AcceptanceWaitsForDirectProcessOnly {
    $acceptanceSource = Get-Content -LiteralPath $vmAcceptanceScript -Raw
    if ($acceptanceSource -match 'Start-Process\s+-FilePath\s+\$FilePath[^\r\n]*\s-Wait\b' -or
        $acceptanceSource -notmatch '\$process\.WaitForExit\(\)') {
        throw 'VM acceptance must wait for the installer process only; -Wait hangs on the persistent PostgreSQL descendant'
    }
    Write-Output 'PASS VM acceptance ignores persistent installer descendants'
}

function Assert-AcceptanceUsesAbsoluteEvidencePaths {
    $acceptanceSource = Get-Content -LiteralPath $vmAcceptanceScript -Raw
    if ($acceptanceSource -notmatch '\$EvidenceDir\s*=\s*\(Get-Item\s+-LiteralPath\s+\$EvidenceDir\)\.FullName' -or
        $acceptanceSource -notmatch '\$Name\.command\.txt') {
        throw 'VM acceptance must resolve evidence paths before passing /LOG to the cloned Inno uninstaller and retain the invoked command'
    }
    Write-Output 'PASS VM acceptance uses absolute evidence paths'
}

function Assert-AcceptanceUsesInstalledMigrations {
    $acceptanceSource = Get-Content -LiteralPath $vmAcceptanceScript -Raw
    if ($acceptanceSource -notmatch '\$Migrations\s*=\s*Join-Path\s+\$AppDir\s+''migrations''' -or
        $acceptanceSource -notmatch "@\('daemon',[\s\S]*?'--migrations'" -or
        ([regex]::Matches($acceptanceSource, 'doctor[^\r\n]*--migrations\s+\$Migrations')).Count -lt 2) {
        throw 'VM acceptance must point daemon and both doctor calls at the migrations installed next to errkb.exe'
    }
    if ($acceptanceSource -notmatch 'function Stop-InstalledDaemon' -or
        $acceptanceSource -notmatch 'function Start-ErkbDaemon \{\s*#?[\s\S]*?Stop-InstalledDaemon') {
        throw 'VM acceptance must stop the installer-started daemon before binding its evidence-scoped pipe'
    }
    Write-Output 'PASS VM acceptance uses installed migrations'
}

function Assert-RetentionAcceptanceOwnsDaemon {
    $acceptanceSource = Get-Content -LiteralPath $vmAcceptanceScript -Raw
    $retention = [regex]::Match(
        $acceptanceSource,
        'function Step-Retention \{[\s\S]*?(?=# ---------------------------------------------------------------- reboot)'
    ).Value
    if ([string]::IsNullOrWhiteSpace($retention) -or
        $retention -notmatch 'Start-ErkbDaemon' -or
        $retention -notmatch 'finally\s*\{\s*Stop-ErkbDaemon') {
        throw 'retained-data acceptance must own a healthy daemon for the complete search/reuse assertion'
    }
    Write-Output 'PASS retained-data acceptance owns its daemon lifecycle'
}

function Assert-DeleteUninstallWaitsForCloneCleanup {
    $acceptanceSource = Get-Content -LiteralPath $vmAcceptanceScript -Raw
    if ($acceptanceSource -notmatch 'function Wait-PathAbsent' -or
        $acceptanceSource -notmatch '\$appDirectoryDeleted\s*=\s*Wait-PathAbsent\s+-Path\s+\$AppDir') {
        throw 'delete-data acceptance must wait for Inno Setup clone cleanup before asserting that the application directory is absent'
    }
    Write-Output 'PASS delete-data acceptance waits for Inno clone cleanup'
}

function Assert-DeleteUninstallOwnsApplicationRootCleanup {
    $installerSource = Get-Content -LiteralPath $installerScript -Raw
    if ($installerSource -notmatch '\[UninstallDelete\][\s\S]*?Type:\s*dirifempty;\s*Name:\s*"\{app\}"' -or
        $installerSource -match 'Type:\s*filesandordirs;\s*Name:\s*"\{app\}"') {
        throw 'the uninstaller must remove only an empty application root so retained data is preserved safely'
    }
    Write-Output 'PASS uninstall safely removes an empty application root'
}

function Assert-AcceptanceAllowsFreshConnectorState {
    $acceptanceSource = Get-Content -LiteralPath $vmAcceptanceScript -Raw
    if ($acceptanceSource -notmatch "degraded with zero registered connectors" -or
        $acceptanceSource -notmatch "Assert-Step 'doctor connectors'" -or
        $acceptanceSource -match "foreach \(\$required in @\('database', 'migrations', 'extension', 'connectors'\)\)") {
        throw 'VM acceptance must treat zero registered connectors as the expected degraded state on a fresh install'
    }
    Write-Output 'PASS VM acceptance allows the fresh connector state'
}

function Assert-RetainedClusterCanBeReused {
    $postgresSource = Get-Content -LiteralPath $postgresScript -Raw
    $acceptanceSource = Get-Content -LiteralPath $vmAcceptanceScript -Raw
    if ($postgresSource -notmatch '\$effectiveAction\s*=\s*''start''' -or
        $postgresSource -notmatch 'retained cluster' -or
        $acceptanceSource -notmatch "ExpectedConnectorState\s*=\s*'fresh'" -or
        $acceptanceSource -notmatch "ExpectedConnectorState -eq 'fresh'") {
        throw 'retained-data reinstall must reuse the cluster and validate fresh/existing connector states separately'
    }

    $probeRoot = Join-Path $tempBase ('errkb-retained-reuse-test-' + [guid]::NewGuid().ToString('N'))
    try {
        $dataDir = Join-Path $probeRoot 'pg\data'
        New-Item -ItemType Directory -Path $dataDir -Force | Out-Null
        [System.IO.File]::WriteAllText((Join-Path $dataDir 'PG_VERSION'), '16')
        $fakeExe = Join-Path $probeRoot 'fake-errkb.cmd'
        [System.IO.File]::WriteAllText($fakeExe, "@echo off`r`necho %*`r`nexit /b 0`r`n", [System.Text.Encoding]::ASCII)
        $credentials = [ordered]@{
            version      = 1
            port         = 5433
            database     = 'errkb'
            superuser    = 'postgres'
            password     = 'release-test-secret'
            data_dir     = $dataDir
            service_name = ''
        }
        [System.IO.File]::WriteAllText(
            (Join-Path $probeRoot 'pg\credentials.json'),
            ($credentials | ConvertTo-Json),
            [System.Text.UTF8Encoding]::new($false))

        $output = @(& $shell.Source -NoProfile -File $postgresScript install `
                -DataDir $dataDir -Port 5433 -Database errkb -Superuser postgres -Exe $fakeExe 2>&1)
        $exitCode = $LASTEXITCODE
        $text = $output -join "`n"
        if ($exitCode -ne 0 -or $text -notmatch 'reusing\s+retained cluster' -or
            $text -notmatch 'pg start' -or $text -match 'pg install') {
            throw "retained-cluster reuse probe failed (exit $exitCode):`n$text"
        }
    } finally {
        $resolvedProbe = [System.IO.Path]::GetFullPath($probeRoot)
        if ($resolvedProbe.StartsWith($tempBase, [System.StringComparison]::OrdinalIgnoreCase) -and
            (Split-Path -Leaf $resolvedProbe).StartsWith('errkb-retained-reuse-test-', [System.StringComparison]::Ordinal)) {
            Remove-Item -LiteralPath $resolvedProbe -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
    Write-Output 'PASS retained cluster can be reused'
}

function Assert-CiAcceptanceCoversLifecycle {
    $source = Get-Content -LiteralPath $ciAcceptanceScript -Raw
    $phaseSource = Get-Content -LiteralPath $vmAcceptanceScript -Raw
    $orderedStages = @(
        "Invoke-AcceptanceStage '03-install'",
        "Invoke-AcceptanceStage '04-first-run'",
        "Invoke-AcceptanceStage '05-error-reuse-loop'",
        'Invoke-RecoveryStage',
        "Invoke-AcceptanceStage '07-uninstall-keep'",
        "Invoke-AcceptanceStage '08-reinstall-retained'",
        "Invoke-AcceptanceStage '10-retained-data'",
        "Invoke-AcceptanceStage '11-uninstall-delete'"
    )
    $offset = -1
    foreach ($stage in $orderedStages) {
        $next = $source.IndexOf($stage, $offset + 1, [System.StringComparison]::Ordinal)
        if ($next -lt 0) {
            throw "WF-011A orchestrator is missing stage $stage"
        }
        if ($next -le $offset) {
            throw "WF-011A lifecycle stages are not in the required order"
        }
        $offset = $next
    }
    if ($source -notmatch "'-UninstallData', 'keep'" -or
        $source -notmatch "'-UninstallData', 'delete'" -or
        $source -notmatch "ExpectedConnectorState', 'existing'" -or
        $source -notmatch "results\.json") {
        throw 'WF-011A must cover both uninstall choices, retained-state validation and combined evidence'
    }
    if ($phaseSource -notmatch [regex]::Escape("if (`$keep) { '/KEEP_DATA=1' } else { '/DELETE_DATA=1' }")) {
        throw 'WF-011A silent uninstall must answer both sides of the retention prompt explicitly'
    }
    Write-Output 'PASS WF-011A covers the unattended Windows lifecycle'
}

function Assert-RecoveryAcceptanceCoversFailures {
    $source = Get-Content -LiteralPath $recoveryAcceptanceScript -Raw
    foreach ($required in @(
            'autostart configuration',
            'single instance after replay',
            'daemon failure isolated',
            'full failure simulated',
            'health after recovery',
            'knowledge after recovery')) {
        if ($source -notmatch [regex]::Escape($required)) {
            throw "WF-011B recovery acceptance is missing '$required'"
        }
    }
    if ($source -notmatch 'postgres\.ps1' -or $source -notmatch 'Named Pipe' -or
        $source -notmatch 'Get-InstalledDaemonProcesses') {
        throw 'WF-011B must exercise PostgreSQL, the Named Pipe and exact installed-process counting'
    }
    Write-Output 'PASS WF-011B covers deterministic startup recovery'
}

try {
    Assert-SilentInstallDoesNotLaunchConsole
    Assert-SilentInstallReusesRetainedCluster
    Assert-SilentUninstallPersistsRetentionChoice
    Assert-AcceptanceWaitsForDirectProcessOnly
    Assert-AcceptanceUsesAbsoluteEvidencePaths
    Assert-AcceptanceUsesInstalledMigrations
    Assert-RetentionAcceptanceOwnsDaemon
    Assert-DeleteUninstallWaitsForCloneCleanup
    Assert-DeleteUninstallOwnsApplicationRootCleanup
    Assert-AcceptanceAllowsFreshConnectorState
    Assert-RetainedClusterCanBeReused
    Assert-CiAcceptanceCoversLifecycle
    Assert-RecoveryAcceptanceCoversFailures
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
    Assert-MissingWindowsAcceptanceRejected
    Assert-MismatchedBuildCommitRejected
    Write-Output 'release gate tests: 19 passed, 0 failed'
} finally {
    $resolvedRoot = [System.IO.Path]::GetFullPath($testRoot)
    if ($resolvedRoot.StartsWith($tempBase, [System.StringComparison]::OrdinalIgnoreCase) -and
        (Split-Path -Leaf $resolvedRoot).StartsWith('errkb-release-test-', [System.StringComparison]::Ordinal)) {
        Remove-Item -LiteralPath $resolvedRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
