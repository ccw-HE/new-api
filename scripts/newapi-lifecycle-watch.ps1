param(
    [Parameter(Mandatory = $true)]
    [ValidateRange(1, 2147483647)]
    [int]$OwnerProcessId,

    [Parameter(Mandatory = $true)]
    [string]$Root,

    [Parameter(Mandatory = $true)]
    [string]$StartupScript
)

$ErrorActionPreference = 'Stop'
$normalizedRoot = [IO.Path]::GetFullPath($Root).TrimEnd('\', '/')
$rootPrefix = $normalizedRoot + [IO.Path]::DirectorySeparatorChar
$normalizedStartupScript = [IO.Path]::GetFullPath($StartupScript)

if (-not $normalizedStartupScript.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Startup script must be located inside the project root: $normalizedStartupScript"
}
if (-not (Test-Path -LiteralPath $normalizedStartupScript -PathType Leaf)) {
    throw "Startup script was not found: $normalizedStartupScript"
}

$owner = Get-Process -Id $OwnerProcessId -ErrorAction SilentlyContinue
if ($null -ne $owner) {
    $owner.WaitForExit()
}

$env:NEWAPI_NO_PAUSE = '1'
$cleanup = Start-Process -FilePath $normalizedStartupScript -ArgumentList 'stop-all' -WorkingDirectory $normalizedRoot -WindowStyle Hidden -PassThru
$cleanup.WaitForExit()
exit $cleanup.ExitCode
