[CmdletBinding()]
param(
    [string]$Version,
    [string]$ReleaseBaseUrl = 'https://github.com/blak0p-dev/relay-mcp/releases',
    [string]$ReleaseApiUrl = 'https://api.github.com/repos/blak0p-dev/relay-mcp/releases/latest'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$script:ClientFailures = 0

function Fail([string]$Message) { throw "relay installer: $Message" }

function Resolve-Version {
    if (-not [string]::IsNullOrWhiteSpace($Version)) { return $Version }
    try { $latest = Invoke-RestMethod -Uri $ReleaseApiUrl -Method Get }
    catch { Fail 'could not resolve the latest release; pass -Version vX.Y.Z to retry' }
    if ([string]::IsNullOrWhiteSpace($latest.tag_name)) { Fail 'latest release response did not contain a tag_name' }
    return [string]$latest.tag_name
}

function Assert-Version([string]$Candidate) {
    if ($Candidate -notmatch '^v[0-9][0-9A-Za-z._-]*$') { Fail "invalid release tag: $Candidate" }
}

function Get-Architecture {
    if (-not [System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Windows)) {
        Fail 'unsupported operating system: Windows installer requires Windows'
    }
    switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
        'X64' { return 'amd64' }
        'Arm64' { return 'arm64' }
        default { Fail "unsupported architecture: $([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture)" }
    }
}

function Download([string]$Uri, [string]$OutputPath) {
    try {
        $source = [uri]$Uri
        if ($source.Scheme -eq 'file') {
            Copy-Item -LiteralPath $source.LocalPath -Destination $OutputPath
        } else {
            Invoke-WebRequest -Uri $Uri -OutFile $OutputPath
        }
    }
    catch { Fail "could not download $Uri" }
}

function Assert-Checksum([string]$ManifestPath, [string]$ArchivePath) {
    $asset = [System.IO.Path]::GetFileName($ArchivePath)
    $entries = @(
        Get-Content -LiteralPath $ManifestPath | ForEach-Object {
            if ($_ -match '^([0-9A-Fa-f]{64})\s{2}(.+)$' -and $Matches[2] -ceq $asset) { $Matches[1].ToLowerInvariant() }
        }
    )
    if ($entries.Count -ne 1) { Fail "checksum manifest must contain exactly one valid entry for $asset" }
    $actual = (Get-FileHash -LiteralPath $ArchivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -cne $entries[0]) { Fail "SHA-256 mismatch for $asset" }
}

function Expand-Relay([string]$ArchivePath, [string]$StagePath) {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    try { $archive = [System.IO.Compression.ZipFile]::OpenRead($ArchivePath) }
    catch { Fail "invalid archive: $([System.IO.Path]::GetFileName($ArchivePath))" }
    try {
        $relayEntries = @()
        foreach ($entry in $archive.Entries) {
            $name = $entry.FullName.Replace('\', '/')
            if ($name.StartsWith('/') -or $name.Split('/') -contains '..') { Fail "unsafe archive member: $name" }
            if ($name -ceq 'relay.exe') { $relayEntries += $entry }
        }
        if ($relayEntries.Count -ne 1) { Fail 'archive must contain exactly one relay.exe binary member' }
        $target = Join-Path $StagePath 'relay.exe'
        $input = $relayEntries[0].Open()
        try {
            $output = [System.IO.File]::Create($target)
            try { $input.CopyTo($output) }
            finally { $output.Dispose() }
        }
        finally { $input.Dispose() }
    }
    finally { $archive.Dispose() }
}

function Install-StagedBinary([string]$StagedBinary, [string]$Destination) {
    if (Test-Path -LiteralPath $Destination) {
        [System.IO.File]::Replace($StagedBinary, $Destination, $null)
    } else {
        [System.IO.File]::Move($StagedBinary, $Destination)
    }
}

function Invoke-Client([string]$Name, [string[]]$RemoveArguments, [string[]]$AddArguments, [string]$Remediation) {
    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if ($null -eq $command) {
        Write-Output "Skipped $Name (not found). Run: $Remediation"
        return
    }
    & $command.Source @RemoveArguments 2>$null
    & $command.Source @AddArguments
    if ($LASTEXITCODE -ne 0) { $script:ClientFailures++ }
}

function Merge-PiConfiguration([string]$ConfigPath, [string]$Binary) {
    try {
        $document = if (Test-Path -LiteralPath $ConfigPath) {
            Get-Content -LiteralPath $ConfigPath -Raw | ConvertFrom-Json -AsHashtable
        } else { @{} }
    }
    catch { Fail "Pi configuration is invalid JSON: $ConfigPath" }
    if ($document -isnot [hashtable]) { Fail 'Pi configuration must be an object' }
    if (-not $document.ContainsKey('mcpServers')) { $document['mcpServers'] = @{} }
    if ($document['mcpServers'] -isnot [hashtable]) { Fail 'Pi mcpServers must be an object' }
    $document['mcpServers']['relay'] = @{ command = $Binary }
    $directory = Split-Path -Parent $ConfigPath
    New-Item -ItemType Directory -Force -Path $directory | Out-Null
    $temporary = Join-Path $directory ('.' + [System.IO.Path]::GetFileName($ConfigPath) + '.' + [guid]::NewGuid())
    try {
        [System.IO.File]::WriteAllText($temporary, (($document | ConvertTo-Json -Depth 16) + [Environment]::NewLine), [System.Text.UTF8Encoding]::new($false))
        if (Test-Path -LiteralPath $ConfigPath) { [System.IO.File]::Replace($temporary, $ConfigPath, $null) }
        else { [System.IO.File]::Move($temporary, $ConfigPath) }
    }
    finally { if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary -Force } }
}

function Configure-Pi([string]$Binary) {
    $pi = Get-Command pi -ErrorAction SilentlyContinue
    if ($null -eq $pi) {
        Write-Output 'Skipped Pi (not found). Run: pi install npm:pi-mcp-adapter'
        return
    }
    & $pi.Source install 'npm:pi-mcp-adapter'
    if ($LASTEXITCODE -ne 0) { $script:ClientFailures++ }
    $config = if ($env:RELAY_PI_CONFIG) { $env:RELAY_PI_CONFIG } else { Join-Path $HOME '.config/mcp/mcp.json' }
    try { Merge-PiConfiguration -ConfigPath $config -Binary $Binary }
    catch { Write-Error $_; $script:ClientFailures++ }
}

$workspace = $null
$stage = $null
try {
    $Version = Resolve-Version
    Assert-Version $Version
    $architecture = Get-Architecture
    $asset = "relay_${Version}_windows_${architecture}.zip"
    $workspace = Join-Path ([System.IO.Path]::GetTempPath()) ("relay-install-" + [guid]::NewGuid())
    New-Item -ItemType Directory -Path $workspace | Out-Null
    $archive = Join-Path $workspace $asset
    $manifest = Join-Path $workspace 'checksums.txt'
    Download "$ReleaseBaseUrl/download/$Version/$asset" $archive
    Download "$ReleaseBaseUrl/download/$Version/checksums.txt" $manifest
    Assert-Checksum $manifest $archive

    $destinationDirectory = if ($env:GOBIN) { $env:GOBIN } else { Join-Path $HOME 'go/bin' }
    New-Item -ItemType Directory -Force -Path $destinationDirectory | Out-Null
    $stage = Join-Path $destinationDirectory ('.relay-stage-' + [guid]::NewGuid())
    New-Item -ItemType Directory -Path $stage | Out-Null
    Expand-Relay $archive $stage
    $binary = Join-Path $destinationDirectory 'relay.exe'
    Install-StagedBinary (Join-Path $stage 'relay.exe') $binary
    Remove-Item -LiteralPath $stage -Recurse -Force
    $stage = $null
    Write-Output "Installed relay to $binary"
    if (($env:Path -split ';') -notcontains $destinationDirectory) { Write-Output "Add $destinationDirectory to PATH to run relay from a new shell." }

    Invoke-Client 'claude' @('mcp', 'remove', '--scope', 'user', 'relay') @('mcp', 'add', '--scope', 'user', 'relay', '--', $binary) "claude mcp add --scope user relay -- $binary"
    Invoke-Client 'codex' @('mcp', 'remove', 'relay') @('mcp', 'add', 'relay', '--', $binary) "codex mcp add relay -- $binary"
    Invoke-Client 'opencode' @('mcp', 'remove', 'relay') @('mcp', 'add', 'relay', '--', $binary) "opencode mcp add relay -- $binary"
    Configure-Pi $binary
    if ($script:ClientFailures -ne 0) { Fail 'one or more detected client setups failed; the verified relay binary remains installed' }
}
finally {
    if ($null -ne $stage -and (Test-Path -LiteralPath $stage)) { Remove-Item -LiteralPath $stage -Recurse -Force }
    if ($null -ne $workspace -and (Test-Path -LiteralPath $workspace)) { Remove-Item -LiteralPath $workspace -Recurse -Force }
}
