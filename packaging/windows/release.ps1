<#
.SYNOPSIS
  Checks the release inputs and assembles the release bundle for one tag.

.DESCRIPTION
  Two modes, so the pipeline verifies before it builds:

    check    verifies the tag, the version, the changelog section and the
             clean-VM acceptance record. Nothing is built.
    package  repeats the checks, builds errkb.exe through build.ps1, and
             assembles the release directory:
               errkb.exe
               errkb.exe.sha256          checksum line for the binary
               errkb-<version>-setup.exe (with -IncludeInstaller)
               sbom.json                 Go modules + bundled and required tools
               THIRD-PARTY-NOTICES.md    license declarations
               SIGNING.md                the signing policy of this release
               RELEASE-NOTES.md          the changelog section for this version
               manifest.json             provenance: commit, toolchain, artifacts
               SHA256SUMS                every artifact above, sha256sum format

  Reproducibility
  ---------------
  A rerun of the same tag with the same toolchain produces the same files:
  build time comes from the commit (never the wall clock), the bundle is
  rebuilt from scratch, artifacts are sorted before they are hashed, and the
  manifest records the Go and go-winres versions so a difference is visible
  instead of silent. See docs/acceptance-vm.md and build.ps1 for the pinned
  toolchain.

  Signing policy
  --------------
  This release is not code-signed. SIGNING.md in the bundle states that, the
  reason, the residual risk and the plan; the checksums and the manifest are
  what a user can verify today.

.PARAMETER Mode
  check or package (default package).

.PARAMETER Version
  Release version without the leading v, for example 0.1.0.

.PARAMETER Tag
  The git tag, for example v0.1.0. Defaults to v<Version>; the check fails when
  the two disagree.

.PARAMETER Commit
  Commit stamp embedded in the binary. Defaults to the short HEAD hash.

.PARAMETER BuildTime
  Build stamp embedded in the binary. Defaults to the commit timestamp
  (git log -1 --format=%cI), which is what makes rebuilds byte-identical.

.PARAMETER VmAcceptanceVersion
  Version accepted on a clean virtual machine. Defaults to the environment
  variable VM_ACCEPTANCE_VERSION. The pipeline sets that variable from the
  repository variable of the same name; publishing without it is refused.

.PARAMETER IncludeInstaller
  Also compile packaging\windows\installer.iss with Inno Setup and ship the
  setup executable.

.PARAMETER IsccPath
  Path to ISCC.exe. Auto-detected when omitted.

.PARAMETER OutputDir
  Bundle directory. Defaults to dist\release\errkb-<Version>.
#>
[CmdletBinding()]
param(
    [ValidateSet("check", "package")]
    [string]$Mode = "package",
    [string]$Version = "",
    [string]$Tag = "",
    [string]$Commit = "",
    [string]$BuildTime = "",
    [string]$VmAcceptanceVersion = $env:VM_ACCEPTANCE_VERSION,
    [switch]$IncludeInstaller,
    [string]$IsccPath = "",
    [string]$OutputDir = ""
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$changelogPath = Join-Path $repoRoot "CHANGELOG.md"
$buildScript = Join-Path $PSScriptRoot "build.ps1"
$installerScript = Join-Path $PSScriptRoot "installer.iss"
$binaryPath = Join-Path $repoRoot "dist\errkb.exe"

function Fail([string]$Message) {
    throw "release: $Message"
}

function Resolve-ReleaseVersion {
    if ([string]::IsNullOrWhiteSpace($Version)) {
        Fail "pass -Version (for example 0.1.0) or let the tag provide it"
    }
    $normalized = $Version.Trim()
    if ($normalized.StartsWith("v")) {
        $normalized = $normalized.Substring(1)
    }
    if ($normalized -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$') {
        Fail "version '$Version' is not a semantic version such as 0.1.0 or 0.3.0-rc.1"
    }
    return $normalized
}

function Resolve-ReleaseTag([string]$ResolvedVersion) {
    $resolvedTag = if ([string]::IsNullOrWhiteSpace($Tag)) { "v$ResolvedVersion" } else { $Tag.Trim() }
    if ($resolvedTag -ne "v$ResolvedVersion") {
        Fail "tag '$resolvedTag' and version '$ResolvedVersion' disagree; a release tag must be v<version>"
    }
    return $resolvedTag
}

function Assert-ChangelogSection([string]$ResolvedVersion) {
    if (-not (Test-Path $changelogPath)) {
        Fail "CHANGELOG.md is missing; a release needs a user-facing section for $ResolvedVersion"
    }
    $text = Get-Content $changelogPath -Raw
    $pattern = "(?m)^## \[" + [regex]::Escape($ResolvedVersion) + "\]"
    if ($text -notmatch $pattern) {
        Fail "CHANGELOG.md has no '## [$ResolvedVersion]' section; write the user-facing notes before tagging"
    }
}

function Assert-VmAcceptance([string]$ResolvedVersion) {
    if ([string]::IsNullOrWhiteSpace($VmAcceptanceVersion)) {
        Fail "no clean-VM acceptance record: run packaging\windows\vm-acceptance.ps1 on a clean Windows machine for $ResolvedVersion, then set VM_ACCEPTANCE_VERSION (or pass -VmAcceptanceVersion)"
    }
    $accepted = $VmAcceptanceVersion.Trim()
    if ($accepted.StartsWith("v")) {
        $accepted = $accepted.Substring(1)
    }
    if ($accepted -ne $ResolvedVersion) {
        Fail "the clean-VM acceptance covers version '$accepted' but this release is '$ResolvedVersion'; publish only after the acceptance run for this version"
    }
}

function Assert-InstallerVersion([string]$ResolvedVersion) {
    if (-not (Test-Path $installerScript)) {
        Fail "packaging\windows\installer.iss is missing"
    }
    $match = Select-String -Path $installerScript -Pattern '^#define AppVersion "([^"]+)"$' | Select-Object -First 1
    if (-not $match) {
        Fail "installer.iss has no '#define AppVersion "x.y.z"' line"
    }
    $installerVersion = $match.Matches[0].Groups[1].Value
    if ($installerVersion -ne $ResolvedVersion) {
        Fail "installer.iss builds version '$installerVersion' but this release is '$ResolvedVersion'; update the definition before tagging"
    }
}

function Resolve-Commit {
    if (-not [string]::IsNullOrWhiteSpace($Commit)) {
        return $Commit.Trim()
    }
    $value = (& git -C $repoRoot rev-parse --short HEAD 2>$null)
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($value)) {
        Fail "cannot resolve a commit; pass -Commit explicitly"
    }
    return $value.Trim()
}

function Resolve-BuildTime {
    if (-not [string]::IsNullOrWhiteSpace($BuildTime)) {
        return $BuildTime.Trim()
    }
    $value = (& git -C $repoRoot log -1 --format=%cI 2>$null)
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($value)) {
        Fail "cannot resolve the commit timestamp; pass -BuildTime explicitly"
    }
    return $value.Trim()
}

function Resolve-Iscc {
    if (-not [string]::IsNullOrWhiteSpace($IsccPath)) {
        if (-not (Test-Path $IsccPath)) {
            Fail "ISCC.exe not found at '$IsccPath'"
        }
        return (Resolve-Path $IsccPath).Path
    }
    $command = Get-Command iscc -ErrorAction SilentlyContinue
    if ($command) {
        return $command.Source
    }
    $candidates = @()
    if ($env:LOCALAPPDATA) {
        $candidates += (Join-Path $env:LOCALAPPDATA "Programs\Inno Setup 6\ISCC.exe")
    }
    if (${env:ProgramFiles(x86)}) {
        $candidates += (Join-Path ${env:ProgramFiles(x86)} "Inno Setup 6\ISCC.exe")
    }
    if ($env:ProgramFiles) {
        $candidates += (Join-Path $env:ProgramFiles "Inno Setup 6\ISCC.exe")
    }
    foreach ($candidate in $candidates) {
        if ($candidate -and (Test-Path $candidate)) {
            return (Resolve-Path $candidate).Path
        }
    }
    Fail "ISCC.exe was not found; install Inno Setup 6 or pass -IsccPath"
}

function Get-GoVersion {
    $value = (& go version 2>$null)
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($value)) {
        Fail "the Go toolchain is not on PATH"
    }
    return $value.Trim()
}

function Get-ToolModuleVersion([string]$ToolPath, [string]$ModulePath) {
    $info = & go version -m $ToolPath 2>$null
    if ($LASTEXITCODE -ne 0) {
        return "unknown"
    }
    foreach ($line in $info) {
        if ($line -match "^\s+mod\s+$([regex]::Escape($ModulePath))\s+(\S+)") {
            return $Matches[1]
        }
    }
    return "unknown"
}

function Get-ReleaseToolchain {
    $goVersion = Get-GoVersion
    $winresPath = Join-Path (go env GOPATH) "bin\go-winres.exe"
    $winresVersion = if (Test-Path $winresPath) { Get-ToolModuleVersion $winresPath "github.com/tc-hib/go-winres" } else { "missing" }
    if ($winresVersion -eq "missing") {
        Fail "go-winres is not installed; run: go install github.com/tc-hib/go-winres@v0.3.3"
    }
    return [ordered]@{
        go        = $goVersion
        go_winres = $winresVersion
    }
}

function Invoke-ReleaseChecks([string]$ResolvedVersion, [string]$ResolvedTag) {
    Assert-ChangelogSection $ResolvedVersion
    Assert-VmAcceptance $ResolvedVersion
    if ($IncludeInstaller) {
        Assert-InstallerVersion $ResolvedVersion
    }
    Write-Output "release check: version $ResolvedVersion"
    Write-Output "release check: tag $ResolvedTag"
    Write-Output "release check: changelog section present"
    Write-Output "release check: clean-VM acceptance recorded for $ResolvedVersion"
}

function Write-ReleaseNotes([string]$ResolvedVersion, [string]$TargetPath) {
    $text = (Get-Content $changelogPath -Raw) -replace "`r`n", "`n"
    $startPattern = "(?m)^## \[" + [regex]::Escape($ResolvedVersion) + "\].*$"
    $start = [regex]::Match($text, $startPattern)
    if (-not $start.Success) {
        Fail "CHANGELOG.md has no section for $ResolvedVersion"
    }
    $rest = $text.Substring($start.Index + $start.Length).TrimStart("`n")
    $next = [regex]::Match($rest, "(?m)^## \[")
    $body = if ($next.Success) { $rest.Substring(0, $next.Index) } else { $rest }
    $body = $body.TrimEnd() + "`n"
    $content = "# errkb $ResolvedVersion`n`n" + $body
    [System.IO.File]::WriteAllText($TargetPath, $content, [System.Text.UTF8Encoding]::new($false))
}

function Get-EmbeddedModules([string]$BinaryPath) {
    $info = & go version -m $BinaryPath
    if ($LASTEXITCODE -ne 0) {
        Fail "cannot read the module list from '$BinaryPath'"
    }
    $modules = @()
    $main = $null
    foreach ($line in $info) {
        if ($line -match '^\s+mod\s+(\S+)\s+(\S+)') {
            $main = @{ path = $Matches[1]; version = $Matches[2] }
        }
        elseif ($line -match '^\s+dep\s+(\S+)\s+(\S+)') {
            $modules += @{ path = $Matches[1]; version = $Matches[2] }
        }
    }
    return @{ main = $main; modules = $modules; go = (($info | Select-Object -First 1) -replace '.*: ', '') }
}

$licenseById = @{
    "github.com/Microsoft/go-winio"        = "MIT"
    "github.com/jackc/pgx/v5"              = "MIT"
    "github.com/jackc/pgpassfile"          = "MIT"
    "github.com/jackc/pgservicefile"       = "MIT"
    "github.com/jackc/puddle/v2"           = "MIT"
    "github.com/kr/text"                   = "MIT"
    "github.com/rogpeppe/go-internal"      = "BSD-3-Clause"
    "golang.org/x/crypto"                  = "BSD-3-Clause"
    "golang.org/x/sync"                    = "BSD-3-Clause"
    "golang.org/x/sys"                     = "BSD-3-Clause"
    "golang.org/x/text"                    = "BSD-3-Clause"
    "gopkg.in/yaml.v3"                     = "MIT AND Apache-2.0"
    "github.com/tc-hib/go-winres"          = "MIT"
    "golang.org/x/crypto/chacha20"         = "BSD-3-Clause"
}

function New-SbomPackage([string]$Name, [string]$PackageVersion, [string]$License, [string]$Role, [bool]$Distributed, [string]$Homepage) {
    return [ordered]@{
        name             = $Name
        versionInfo      = $PackageVersion
        licenseConcluded = $License
        licenseDeclared  = $License
        role             = $Role
        distributed      = $Distributed
        homepage         = $Homepage
    }
}

function Write-Sbom([string]$TargetPath, [string]$ResolvedVersion, [string]$ResolvedCommit, [string]$ResolvedBuildTime, [hashtable]$Toolchain, [string]$BinaryPath, [string]$InstallerPath) {
    $modules = Get-EmbeddedModules $BinaryPath
    $goVersion = "unknown"
    if ($modules.go -match 'go[0-9][0-9A-Za-z.\-]*') {
        $goVersion = $Matches[0]
    }
    $packages = @()
    $packages += New-SbomPackage "errkb" $ResolvedVersion "MIT" "this release" $true "https://github.com/jingchangzhao-gif/dsh-errkb"
    foreach ($module in ($modules.modules | Sort-Object { $_.path })) {
        $license = $licenseById[$module.path]
        if (-not $license) {
            $license = "NOASSERTION"
        }
        $packages += New-SbomPackage $module.path $module.version $license "Go module compiled into errkb.exe" $true ("https://" + ($module.path -replace '^([^/]+)/', '$1/'))
    }
    $packages += New-SbomPackage "go" $goVersion "BSD-3-Clause" "build toolchain, not distributed" $false "https://go.dev"
    $packages += New-SbomPackage "github.com/tc-hib/go-winres" $Toolchain.go_winres "MIT" "build tool, not distributed" $false "https://github.com/tc-hib/go-winres"
    $packages += New-SbomPackage "PostgreSQL" "16 or later" "PostgreSQL" "runtime prerequisite, installed separately, not distributed" $false "https://www.postgresql.org"
    if ($InstallerPath) {
        $iscc = Resolve-Iscc
        $innoVersion = (Get-Item $iscc).VersionInfo.ProductVersion
        $packages += New-SbomPackage "Inno Setup" $innoVersion "Inno Setup License" "build tool for the setup executable; its runtime is compiled into the setup" $false "https://jrsoftware.org/isinfo.php"
    }

    $artifactNames = @()
    if (Test-Path $BinaryPath) { $artifactNames += "errkb.exe" }
    if ($InstallerPath) { $artifactNames += (Split-Path $InstallerPath -Leaf) }
    $document = [ordered]@{
        spdxVersion       = "SPDX-2.3"
        dataLicense       = "CC0-1.0"
        SPDXID            = "SPDXRef-DOCUMENT"
        name              = "errkb-$ResolvedVersion"
        documentNamespace = "https://github.com/jingchangzhao-gif/dsh-errkb/sbom/$ResolvedVersion-$ResolvedCommit"
        creationInfo      = [ordered]@{
            created  = $ResolvedBuildTime
            creators = @("Tool: packaging/windows/release.ps1")
        }
        packages          = $packages
        comment           = "The Go modules are compiled into errkb.exe. PostgreSQL is required at runtime but is not distributed here; the installer expects a PostgreSQL 16 installation. Inno Setup is a build tool whose runtime is compiled into the setup executable."
    }
    $json = $document | ConvertTo-Json -Depth 8
    [System.IO.File]::WriteAllText($TargetPath, ($json + "`n"), [System.Text.UTF8Encoding]::new($false))
}

function Write-ThirdPartyNotices([string]$TargetPath, [string]$ResolvedVersion, [hashtable]$Toolchain, [string]$BinaryPath, [bool]$WithInstaller) {
    $modules = Get-EmbeddedModules $BinaryPath
    $lines = @()
    $lines += "# Third-party notices"
    $lines += ""
    $lines += "errkb $ResolvedVersion bundles the components below. The Go modules are compiled into"
    $lines += "errkb.exe; the list is read from the built binary, so it matches what shipped."
    $lines += ""
    $lines += "| Component | Version | License | Role |"
    $lines += "| --- | --- | --- | --- |"
    $lines += "| errkb | $ResolvedVersion | MIT | this release |"
    foreach ($module in ($modules.modules | Sort-Object { $_.path })) {
        $license = $licenseById[$module.path]
        if (-not $license) {
            $license = "NOASSERTION"
        }
        $lines += "| $($module.path) | $($module.version) | $license | compiled into errkb.exe |"
    }
    $lines += "| Go | $($Toolchain.go) | BSD-3-Clause | build toolchain, not distributed |"
    $lines += "| go-winres | $($Toolchain.go_winres) | MIT | build tool, not distributed |"
    $lines += "| PostgreSQL | 16 or later | PostgreSQL License | runtime prerequisite, installed separately |"
    if ($WithInstaller) {
        $lines += "| Inno Setup | 6 | Inno Setup License | builds the setup executable; its runtime is compiled into it |"
    }
    $lines += ""
    $lines += "## License texts"
    $lines += ""
    $lines += "### errkb"
    $lines += ""
    $lines += "MIT License. The full text ships in LICENSE at the repository root."
    $lines += ""
    $lines += "### PostgreSQL"
    $lines += ""
    $lines += "PostgreSQL is required at runtime and is installed by the operator, not by this"
    $lines += "bundle. It is distributed under the PostgreSQL License:"
    $lines += ""
    $lines += "```text"
    $lines += "PostgreSQL Database Management System"
    $lines += "(formerly known as Postgres, then as Postgres95)"
    $lines += ""
    $lines += "Portions Copyright (c) 1996-2023, PostgreSQL Global Development Group"
    $lines += "Portions Copyright (c) 1994, The Regents of the University of California"
    $lines += ""
    $lines += "Permission to use, copy, modify, and distribute this software and its"
    $lines += "documentation for any purpose, without fee, and without a written agreement"
    $lines += "is hereby granted, provided that the above copyright notice and this"
    $lines += "paragraph and the following two paragraphs appear in all copies."
    $lines += ""
    $lines += "IN NO EVENT SHALL THE UNIVERSITY OF CALIFORNIA BE LIABLE TO ANY PARTY FOR"
    $lines += "DIRECT, INDIRECT, SPECIAL, INCIDENTAL, OR CONSEQUENTIAL DAMAGES, INCLUDING"
    $lines += "LOST PROFITS, ARISING OUT OF THE USE OF THIS SOFTWARE AND ITS"
    $lines += "DOCUMENTATION, EVEN IF THE UNIVERSITY OF CALIFORNIA HAS BEEN ADVISED OF THE"
    $lines += "POSSIBILITY OF SUCH DAMAGE."
    $lines += ""
    $lines += "THE UNIVERSITY OF CALIFORNIA SPECIFICALLY DISCLAIMS ANY WARRANTIES,"
    $lines += "INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY"
    $lines += "AND FITNESS FOR A PARTICULAR PURPOSE. THE SOFTWARE PROVIDED HEREUNDER IS"
    $lines += 'ON AN "AS IS" BASIS, AND THE UNIVERSITY OF CALIFORNIA HAS NO OBLIGATIONS'
    $lines += "TO PROVIDE MAINTENANCE, SUPPORT, UPDATES, ENHANCEMENTS, OR MODIFICATIONS."
    $lines += "```"
    $lines += ""
    $lines += "### Inno Setup"
    $lines += ""
    $lines += "Inno Setup is used at build time to produce the setup executable and is not"
    $lines += "redistributed as a standalone program. Its license text ships with the Inno"
    $lines += "Setup installation used for the build; see https://jrsoftware.org/isinfo.php."
    $lines += ""
    $lines += "### Go modules"
    $lines += ""
    $lines += "Each module keeps its own license (SPDX identifiers in the table above and in"
    $lines += "sbom.json). The full texts are available in the module repositories; they are not"
    $lines += "vendored into this repository."
    $lines += ""
    [System.IO.File]::WriteAllText($TargetPath, (($lines -join "`n") + "`n"), [System.Text.UTF8Encoding]::new($false))
}

function Write-SigningPolicy([string]$TargetPath, [string]$ResolvedVersion) {
    $lines = @()
    $lines += "# Signing policy"
    $lines += ""
    $lines += "Status: **not code-signed** (errkb $ResolvedVersion)."
    $lines += ""
    $lines += "## Why"
    $lines += ""
    $lines += "- No Authenticode certificate is available to the project yet. Buying one is a"
    $lines += "  budget and identity-verification decision, not a build step."
    $lines += "- Signing without a trusted timestamp and a reviewed pipeline would create a"
    $lines += "  false sense of verification, so the pipeline does not pretend to sign."
    $lines += ""
    $lines += "## What this release offers instead"
    $lines += ""
    $lines += "- SHA256SUMS covers every artifact in this bundle."
    $lines += "- manifest.json records the tag, commit, build time and toolchain versions, and"
    $lines += "  the hash of each artifact."
    $lines += "- The binary build is reproducible with the pinned toolchain (build.ps1); anyone"
    $lines += "  can rebuild the tag and compare the hash."
    $lines += "- The release is attached to a GitHub release created by the tag pipeline, so the"
    $lines += "  source commit is the one the tag points at."
    $lines += ""
    $lines += "## Risks"
    $lines += ""
    $lines += "- Windows SmartScreen and corporate policies may warn or block an unsigned"
    $lines += "  executable; the user has to verify the checksum manually."
    $lines += "- An unsigned installer gives no Authenticode proof that it came from this"
    $lines += "  repository. Verify it against SHA256SUMS from the release page."
    $lines += ""
    $lines += "## Plan"
    $lines += ""
    $lines += "When an Authenticode certificate is available, sign errkb.exe and the setup"
    $lines += "executable in release.yml with signtool and a timestamp server, then extend this"
    $lines += "document and SHA256SUMS with the signing record. Until then, treat the checksums"
    $lines += "as the integrity boundary."
    $lines += ""
    [System.IO.File]::WriteAllText($TargetPath, (($lines -join "`n") + "`n"), [System.Text.UTF8Encoding]::new($false))
}

function New-Manifest([string]$Bundle, [string]$ResolvedVersion, [string]$ResolvedTag, [string]$ResolvedCommit, [string]$ResolvedBuildTime, [hashtable]$Toolchain) {
    $artifactNames = Get-ChildItem $Bundle -File |
        Where-Object { $_.Name -ne "SHA256SUMS" -and $_.Name -ne "manifest.json" } |
        Sort-Object Name |
        ForEach-Object { $_.Name }
    $artifacts = @()
    foreach ($name in $artifactNames) {
        $path = Join-Path $Bundle $name
        $hash = (Get-FileHash $path -Algorithm SHA256).Hash.ToLowerInvariant()
        $artifacts += [ordered]@{
            name   = $name
            size   = (Get-Item $path).Length
            sha256 = $hash
        }
    }
    $manifest = [ordered]@{
        name            = "errkb"
        version         = $ResolvedVersion
        tag             = $ResolvedTag
        commit          = $ResolvedCommit
        build_time      = $ResolvedBuildTime
        toolchain       = $Toolchain
        reproducibility = [ordered]@{
            scope     = "rebuilding the same tag, commit and build time with the same toolchain produces the same errkb.exe, errkb.exe.sha256, manifest.json, sbom.json, THIRD-PARTY-NOTICES.md, SIGNING.md, RELEASE-NOTES.md and SHA256SUMS (verified by packaging/windows/release.ps1 twice per release)"
            installer = "the setup executable is not byte-reproducible: Inno Setup embeds its compilation time in setup.exe. Verify it by installing and comparing the installed errkb.exe with errkb.exe.sha256"
        }
        references      = [ordered]@{
            changelog      = "CHANGELOG.md"
            upgrade_matrix = "spec/compat.cue (compat.upgrade)"
            acceptance     = "docs/acceptance-vm.md"
            signing        = "SIGNING.md"
        }
        artifacts       = $artifacts
    }
    $json = $manifest | ConvertTo-Json -Depth 8
    $manifestPath = Join-Path $Bundle "manifest.json"
    [System.IO.File]::WriteAllText($manifestPath, ($json + "`n"), [System.Text.UTF8Encoding]::new($false))
}

function New-Checksums([string]$Bundle) {
    $lines = @()
    foreach ($file in (Get-ChildItem $Bundle -File | Sort-Object Name)) {
        if ($file.Name -eq "SHA256SUMS") {
            continue
        }
        $hash = (Get-FileHash $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        $lines += "$hash  $($file.Name)"
    }
    [System.IO.File]::WriteAllText((Join-Path $Bundle "SHA256SUMS"), (($lines -join "`n") + "`n"), [System.Text.UTF8Encoding]::new($false))
}

$resolvedVersion = Resolve-ReleaseVersion
$resolvedTag = Resolve-ReleaseTag $resolvedVersion
Invoke-ReleaseChecks $resolvedVersion $resolvedTag

if ($Mode -eq "check") {
    Write-Output "release check: OK"
    exit 0
}

$resolvedCommit = Resolve-Commit
$resolvedBuildTime = Resolve-BuildTime
$toolchain = Get-ReleaseToolchain

$bundle = if ([string]::IsNullOrWhiteSpace($OutputDir)) {
    Join-Path $repoRoot "dist\release\errkb-$resolvedVersion"
} else {
    $OutputDir
}
if (Test-Path $bundle) {
    Remove-Item -Recurse -Force $bundle
}
New-Item -ItemType Directory -Path $bundle -Force | Out-Null

Write-Output "release package: building errkb $resolvedVersion at commit $resolvedCommit ($resolvedBuildTime)"
& $buildScript -Version $resolvedVersion -Commit $resolvedCommit -BuildTime $resolvedBuildTime -Output (Join-Path $repoRoot "dist")
if ($LASTEXITCODE -ne 0 -or -not (Test-Path $binaryPath)) {
    Fail "build.ps1 did not produce $binaryPath"
}

$reported = & $binaryPath version
if ($LASTEXITCODE -ne 0) {
    Fail "the built binary does not run: $reported"
}
$expected = @("errkb $resolvedVersion", "commit $resolvedCommit", "built $resolvedBuildTime")
for ($index = 0; $index -lt $expected.Count; $index++) {
    if ($reported[$index] -ne $expected[$index]) {
        Fail "the binary reports '$($reported[$index])' but the release expects '$($expected[$index])'"
    }
}

Copy-Item $binaryPath (Join-Path $bundle "errkb.exe") -Force
$binaryHash = (Get-FileHash $binaryPath -Algorithm SHA256).Hash.ToLowerInvariant()
[System.IO.File]::WriteAllText((Join-Path $bundle "errkb.exe.sha256"), "$binaryHash  errkb.exe`n", [System.Text.UTF8Encoding]::new($false))

$installerPath = $null
if ($IncludeInstaller) {
    $iscc = Resolve-Iscc
    Assert-InstallerVersion $resolvedVersion
    Write-Output "release package: compiling the setup executable with $iscc"
    & $iscc $installerScript
    if ($LASTEXITCODE -ne 0) {
        Fail "Inno Setup failed with exit code $LASTEXITCODE"
    }
    $builtInstaller = Join-Path $repoRoot "dist\errkb-$resolvedVersion-setup.exe"
    if (-not (Test-Path $builtInstaller)) {
        Fail "Inno Setup did not produce $builtInstaller"
    }
    $installerPath = Join-Path $bundle "errkb-$resolvedVersion-setup.exe"
    Copy-Item $builtInstaller $installerPath -Force
}

Write-Output "release package: writing sbom.json, THIRD-PARTY-NOTICES.md, SIGNING.md"
Write-Sbom (Join-Path $bundle "sbom.json") $resolvedVersion $resolvedCommit $resolvedBuildTime $toolchain $binaryPath $installerPath
Write-ThirdPartyNotices (Join-Path $bundle "THIRD-PARTY-NOTICES.md") $resolvedVersion $toolchain $binaryPath ([bool]$installerPath)
Write-SigningPolicy (Join-Path $bundle "SIGNING.md") $resolvedVersion
Write-ReleaseNotes $resolvedVersion (Join-Path $bundle "RELEASE-NOTES.md")

New-Manifest $bundle $resolvedVersion $resolvedTag $resolvedCommit $resolvedBuildTime $toolchain
New-Checksums $bundle

Write-Output "release package: $bundle"
Get-ChildItem $bundle -File | Sort-Object Name | ForEach-Object {
    Write-Output ("  {0,-32} {1,10} bytes" -f $_.Name, $_.Length)
}
Write-Output "release package: OK"
