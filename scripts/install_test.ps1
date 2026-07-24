[CmdletBinding()]
param([switch]$Red)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$Installer = Join-Path $PSScriptRoot 'install.ps1'
$script:Passed = 0
$script:Failed = 0

function Add-RedResult {
    param([string]$Scenario)
    if (Test-Path -LiteralPath $Installer) {
        Write-Error "FAIL ${Scenario}: expected Phase 2 installer to be absent while this RED contract is introduced"
        $script:Failed++
        return
    }
    Write-Output "RED  ${Scenario}: install.ps1 is absent, so the behavioral assertion is correctly RED"
    $script:Passed++
}

function New-ZipFixture {
    param([string]$Path, [string[]]$Entries)
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [System.IO.Compression.ZipFile]::Open($Path, [System.IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($entryName in $Entries) {
            $entry = $archive.CreateEntry($entryName)
            if ($entryName.EndsWith('relay.exe')) {
                $writer = [System.IO.StreamWriter]::new($entry.Open())
                try { $writer.Write('relay fixture binary') }
                finally { $writer.Dispose() }
            }
        }
    }
    finally { $archive.Dispose() }
}

function New-Fixtures {
    $workspace = Join-Path ([System.IO.Path]::GetTempPath()) ("relay-install-test-" + [guid]::NewGuid())
    $releaseRoot = Join-Path $workspace 'releases'
    $release = Join-Path $releaseRoot 'download/v1.2.3'
    $goBin = Join-Path $workspace 'go/bin'
    $home = Join-Path $workspace 'home'
    $piDirectory = Join-Path $home '.config/mcp'
    $fakeBin = Join-Path $workspace 'fake bin'
    $clientLog = Join-Path $workspace 'client.log'
    New-Item -ItemType Directory -Force -Path $release, $goBin, $piDirectory, $fakeBin, $home | Out-Null
    Set-Content -LiteralPath (Join-Path $goBin 'relay.exe') -Value 'prior relay binary' -NoNewline
    $asset = 'relay_v1.2.3_windows_amd64.zip'
    New-ZipFixture -Path (Join-Path $release $asset) -Entries 'relay.exe'
    Write-Manifest -Release $release -Asset $asset
    $clientScript = @'
@echo off
echo %~n0 %*>>"%RELAY_CLIENT_LOG%"
if "%RELAY_FAIL_CLIENT%"=="%~n0" exit /b 1
exit /b 0
'@
    foreach ($client in @('claude', 'codex', 'opencode', 'pi')) {
        Set-Content -LiteralPath (Join-Path $fakeBin "$client.cmd") -Value $clientScript -NoNewline
    }
    $piConfig = Join-Path $piDirectory 'mcp.json'
    Set-Content -LiteralPath $piConfig -Value '{"mcpServers":{"unrelated":{"command":"keep-me"}}}' -NoNewline
    $releaseBaseUrl = [uri]::new($releaseRoot + [System.IO.Path]::DirectorySeparatorChar).AbsoluteUri.TrimEnd('/')
    return @{ Workspace = $workspace; Release = $release; ReleaseBaseUrl = $releaseBaseUrl; GoBin = $goBin; Home = $home; FakeBin = $fakeBin; ClientLog = $clientLog; PiConfig = $piConfig; Asset = $asset }
}

function Write-Manifest {
    param([string]$Release, [string]$Asset)
    $hash = (Get-FileHash -LiteralPath (Join-Path $Release $Asset) -Algorithm SHA256).Hash.ToLowerInvariant()
    Set-Content -LiteralPath (Join-Path $Release 'checksums.txt') -Value "$hash  $Asset" -NoNewline
}

function Set-Archive {
    param([hashtable]$Fixture, [ValidateSet('valid', 'malformed', 'traversal', 'duplicate')][string]$Kind)
    $archive = Join-Path $Fixture.Release $Fixture.Asset
    Remove-Item -LiteralPath $archive -Force
    switch ($Kind) {
        'valid' { New-ZipFixture -Path $archive -Entries 'relay.exe' }
        'malformed' { Set-Content -LiteralPath $archive -Value 'not an archive' -NoNewline }
        'traversal' { New-ZipFixture -Path $archive -Entries '../relay.exe' }
        'duplicate' { New-ZipFixture -Path $archive -Entries 'first/relay.exe', 'second/relay.exe' }
    }
    Write-Manifest -Release $Fixture.Release -Asset $Fixture.Asset
}

function Set-FixtureEnvironment {
    param([hashtable]$Fixture)
    $env:HOME = $Fixture.Home
    $env:GOBIN = $Fixture.GoBin
    $env:RELAY_PI_CONFIG = $Fixture.PiConfig
    $env:RELAY_CLIENT_LOG = $Fixture.ClientLog
    $env:Path = "$($Fixture.FakeBin);$script:OriginalPath"
}

function Invoke-Installer {
    param([hashtable]$Fixture, [string]$Version = 'v1.2.3')
    & $Installer -Version $Version -ReleaseBaseUrl $Fixture.ReleaseBaseUrl
}

function Assert-Fails {
    param([scriptblock]$Action, [string]$Scenario)
    try { & $Action }
    catch { return }
    throw "expected failure: $Scenario"
}

function Assert-PriorBinary {
    param([hashtable]$Fixture)
    if ([System.IO.File]::ReadAllText((Join-Path $Fixture.GoBin 'relay.exe')) -ne 'prior relay binary') {
        throw 'existing relay binary changed after a failed installation'
    }
}

function Remove-Fixture {
    param([hashtable]$Fixture)
    Remove-Item -LiteralPath $Fixture.Workspace -Recurse -Force
}

if ($Red) {
    throw 'The RED-only Phase 1 mode has been replaced by the Phase 2 functional harness. Run without -Red.'
}

if (-not (Test-Path -LiteralPath $Installer)) { throw 'install.ps1 is missing: expected RED state before Task 2.2' }

if (-not [System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Windows)) {
    throw 'Windows installer runtime harness requires a native Windows host'
}

function Test-FirstInstallRepeatAndPi {
    $fixture = New-Fixtures
    try {
        Set-FixtureEnvironment $fixture
        $output = Invoke-Installer $fixture | Out-String
        if ([System.IO.File]::ReadAllText((Join-Path $fixture.GoBin 'relay.exe')) -ne 'relay fixture binary') { throw 'first install did not replace relay.exe' }
        if ($output -notlike "*Add $($fixture.GoBin) to PATH*") { throw 'first install did not provide PATH guidance' }
        Invoke-Installer $fixture | Out-Null
        $configuration = Get-Content -LiteralPath $fixture.PiConfig -Raw | ConvertFrom-Json -AsHashtable
        if ($configuration['mcpServers']['unrelated']['command'] -ne 'keep-me') { throw 'Pi setup removed an unrelated entry' }
        if ($configuration['mcpServers']['relay']['command'] -ne (Join-Path $fixture.GoBin 'relay.exe')) { throw 'Pi setup did not add relay.exe' }
        foreach ($client in @('claude', 'codex', 'opencode')) {
            if ((Select-String -LiteralPath $fixture.ClientLog -SimpleMatch "$client mcp add" | Measure-Object).Count -ne 2) { throw "repeat setup did not invoke $client with one add per install" }
        }
        Write-Output 'PASS native Windows first install, repeat setup, clients, and Pi merge'
        $script:Passed++
    }
    finally { Remove-Fixture $fixture }
}

function Test-ClientFailureAndMissingClient {
    $fixture = New-Fixtures
    try {
        Set-FixtureEnvironment $fixture
        Remove-Item -LiteralPath (Join-Path $fixture.FakeBin 'codex.cmd') -Force
        $output = Invoke-Installer $fixture | Out-String
        if ($output -notlike "*codex mcp add relay -- $(Join-Path $fixture.GoBin 'relay.exe')*") { throw 'missing Codex remediation was not reported' }
        $env:RELAY_FAIL_CLIENT = 'claude'
        Assert-Fails { Invoke-Installer $fixture | Out-Null } 'detected client failure'
        Remove-Item Env:RELAY_FAIL_CLIENT -ErrorAction SilentlyContinue
        if (-not (Select-String -LiteralPath $fixture.ClientLog -SimpleMatch 'opencode mcp add' -Quiet)) { throw 'later clients were not attempted after a client failure' }
        Write-Output 'PASS native Windows missing and failing client paths'
        $script:Passed++
    }
    finally { Remove-Fixture $fixture }
}

function Test-FailurePreservation {
    $cases = @(
        @{ Name = 'bad checksum'; Prepare = { param($f) Set-Content -LiteralPath (Join-Path $f.Release 'checksums.txt') -Value "$('0' * 64)  $($f.Asset)" -NoNewline } },
        @{ Name = 'missing checksum'; Prepare = { param($f) Set-Content -LiteralPath (Join-Path $f.Release 'checksums.txt') -Value '' -NoNewline } },
        @{ Name = 'duplicate checksum'; Prepare = { param($f) $manifest = Join-Path $f.Release 'checksums.txt'; $entry = Get-Content -LiteralPath $manifest -Raw; Set-Content -LiteralPath $manifest -Value "$entry`n$entry" -NoNewline } },
        @{ Name = 'malformed archive'; Prepare = { param($f) Set-Archive $f malformed } },
        @{ Name = 'traversal archive'; Prepare = { param($f) Set-Archive $f traversal } },
        @{ Name = 'duplicate binary archive'; Prepare = { param($f) Set-Archive $f duplicate } },
        @{ Name = 'interrupted download'; Prepare = { param($f) Remove-Item -LiteralPath (Join-Path $f.Release $f.Asset) -Force } }
    )
    foreach ($case in $cases) {
        $fixture = New-Fixtures
        try {
            Set-FixtureEnvironment $fixture
            & $case.Prepare $fixture
            Assert-Fails { Invoke-Installer $fixture | Out-Null } $case.Name
            Assert-PriorBinary $fixture
        }
        finally { Remove-Fixture $fixture }
    }
    Write-Output 'PASS native Windows checksum, archive, and interrupted-download failures preserve relay.exe'
    $script:Passed++
}

function Test-UnsafeVersionPreservation {
    $fixture = New-Fixtures
    try {
        Set-FixtureEnvironment $fixture
        Assert-Fails { Invoke-Installer $fixture 'v1.2.3;touch unsafe' | Out-Null } 'unsafe release tag'
        Assert-PriorBinary $fixture
        if (Test-Path -LiteralPath (Join-Path $fixture.Workspace 'unsafe')) { throw 'unsafe version executed a command' }
        Write-Output 'PASS unsafe version rejects and preserves the destination'
        $script:Passed++
    }
    finally { Remove-Fixture $fixture }
}

$script:OriginalPath = $env:Path
$originalEnvironment = @{}
foreach ($name in @('HOME', 'GOBIN', 'RELAY_PI_CONFIG', 'RELAY_CLIENT_LOG', 'RELAY_FAIL_CLIENT')) { $originalEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
try {
    Test-FirstInstallRepeatAndPi
    Test-ClientFailureAndMissingClient
    Test-FailurePreservation
    Test-UnsafeVersionPreservation
}
finally {
    $env:Path = $script:OriginalPath
    foreach ($name in $originalEnvironment.Keys) {
        if ($null -eq $originalEnvironment[$name]) { Remove-Item "Env:$name" -ErrorAction SilentlyContinue }
        else { Set-Item "Env:$name" $originalEnvironment[$name] }
    }
}

Write-Output "Installer harness: $script:Passed scenarios passed; $script:Failed harness failures."
if ($script:Failed -ne 0) { exit 1 }
