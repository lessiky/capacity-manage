# Reference script: remote script collection (collectType=script)
# Target: Windows host, disk space utilization of a given drive.
#
# Platform contract (see docs/10-design interface contract, section 1.4):
# - The script and params.json are pushed by the platform over SFTP into the
#   same temp directory on the target, and that directory is deleted afterwards.
# - Input: read params.json located next to this script (platform builtin vars
#   + custom params configured on the flow).
# - Output: print a JSON **array** [{"metric","value","unit"}] as the last line
#   of stdout.
# - Do not read stdin (in the local sandbox mode stdin carries the script source).
#
# Params (params.json):
#   drive   drive letter, e.g. "C:", "C", "C:\"  (default "C:")
#   metric  metric name to report (optional). When empty it falls back to the
#           platform builtin metric_code (the metric bound to this flow), then
#           to DISK_USAGE_PCT. Precedence: metric > metric_code > default, so
#           one script can be reused across devices/metrics without edits.
#
# NOTE: keep this file ASCII-only. Windows PowerShell 5.1 reads BOM-less files
# with the system ANSI code page, so non-ASCII comments can break parsing.
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
# Emit UTF-8 (no BOM) so redirected output stays clean.
try {
    [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
    $OutputEncoding = [System.Text.UTF8Encoding]::new($false)
} catch { }

# The remote working directory is NOT the temp dir, so locate params.json
# relative to the script file itself.
$scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
$paramsPath = Join-Path $scriptDir 'params.json'

function Get-Param {
    param([string]$Key, [string]$Default)
    if (-not (Test-Path -LiteralPath $paramsPath)) { return $Default }
    try {
        $obj = Get-Content -LiteralPath $paramsPath -Raw -Encoding UTF8 | ConvertFrom-Json
    } catch { return $Default }
    $v = $obj.$Key
    if ($null -eq $v -or "$v" -eq '') { return $Default }
    return [string]$v
}

# Normalize the drive letter: "C" -> "C:", "c:\" -> "C:"
$raw = (Get-Param -Key 'drive' -Default 'C:').Trim().TrimEnd('\')
if ($raw -match '^[A-Za-z]$') { $raw = $raw + ':' }
if ($raw -notmatch '^[A-Za-z]:$') {
    [Console]::Error.WriteLine("unsupported drive: $raw (expected e.g. C: or D:)")
    exit 2
}
$driveLetter = $raw.Substring(0, 1).ToUpper() + ':'

$total = $null; $free = $null
try {
    $disk = Get-CimInstance -ClassName Win32_LogicalDisk -Filter "DeviceID='$driveLetter'" -ErrorAction Stop
    if ($disk -and $disk.Size -gt 0) { $total = [double]$disk.Size; $free = [double]$disk.FreeSpace }
} catch { }
# Fall back to Get-PSDrive when CIM is unavailable.
if ($null -eq $total) {
    $ps = Get-PSDrive -Name $driveLetter.Substring(0, 1) -PSProvider FileSystem -ErrorAction SilentlyContinue
    if ($ps -and ($ps.Used + $ps.Free) -gt 0) { $total = [double]($ps.Used + $ps.Free); $free = [double]$ps.Free }
}

if ($null -eq $total -or $total -le 0) {
    [Console]::Error.WriteLine("disk not found or size is 0: $driveLetter")
    exit 3
}

$used = $total - $free
$pct = [math]::Round($used * 100.0 / $total, 2)

# Metric name: explicit param > flow builtin metric_code > default.
# ConvertTo-Json escapes it for us, so no manual escaping is needed here.
$metric = Get-Param -Key 'metric' -Default ''
if ([string]::IsNullOrWhiteSpace($metric)) { $metric = Get-Param -Key 'metric_code' -Default '' }
if ([string]::IsNullOrWhiteSpace($metric)) { $metric = 'DISK_USAGE_PCT' }

# To also report free space, append @{ metric = 'DISK_FREE_GB'; value = ...; unit = 'GB' }.
# -InputObject keeps a single-element array as a JSON array (piping would unwrap it).
$result = @([ordered]@{ metric = $metric; value = $pct; unit = '%' })
ConvertTo-Json -InputObject $result -Compress -Depth 3
