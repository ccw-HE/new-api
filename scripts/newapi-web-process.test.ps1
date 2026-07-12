param(
    [Parameter(Mandatory = $true)]
    [string]$NodeExecutable
)

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path.TrimEnd('\')
$helper = Join-Path $PSScriptRoot 'newapi-web-process.ps1'
$tempRoot = Join-Path $root ("_newapi-web-process-test-" + [Guid]::NewGuid().ToString('N'))
$serverScript = Join-Path $tempRoot 'listener.js'
$foreignTempRoot = Join-Path ([IO.Path]::GetTempPath()) ("newapi-foreign-listener-" + [Guid]::NewGuid().ToString('N'))
$foreignServerScript = Join-Path $foreignTempRoot 'listener.js'
$junctionPath = Join-Path $tempRoot 'outside-junction'
$processes = [Collections.Generic.List[Diagnostics.Process]]::new()

function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) {
        throw $Message
    }
}

function Get-FreePort {
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    try {
        return ([Net.IPEndPoint]$listener.LocalEndpoint).Port
    } finally {
        $listener.Stop()
    }
}

function Wait-Listening([int]$Port) {
    for ($i = 0; $i -lt 100; $i++) {
        if (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue) {
            return
        }
        Start-Sleep -Milliseconds 50
    }
    throw "listener did not start on port $Port"
}

function Invoke-Helper([string[]]$Arguments) {
    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $output = & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $helper @Arguments 2>&1
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    if ($output) {
        $output | ForEach-Object { Write-Host $_ }
    }
    return [int]$exitCode
}

function Start-TestListener([int]$Port, [string]$Marker, [string]$ScriptPath) {
    $process = Start-Process -FilePath $NodeExecutable -ArgumentList @($ScriptPath, [string]$Port, $Marker) -WindowStyle Hidden -PassThru
    $processes.Add($process)
    Wait-Listening $Port
    return $process
}

New-Item -ItemType Directory -Path $tempRoot | Out-Null
New-Item -ItemType Directory -Path $foreignTempRoot | Out-Null
$listenerSource = @'
const net = require('net')
const port = Number(process.argv[2])
const marker = process.argv[3]
const server = net.createServer(() => {})
server.listen(port, '127.0.0.1')
process.title = marker
setInterval(() => {}, 1000)
'@
Set-Content -LiteralPath $serverScript -Encoding UTF8 -Value $listenerSource
Set-Content -LiteralPath $foreignServerScript -Encoding UTF8 -Value $listenerSource

try {
    $outsidePidFile = Join-Path ([IO.Path]::GetTempPath()) ("newapi-outside-" + [Guid]::NewGuid().ToString('N') + '.json')
    $freePort = Get-FreePort
    $exitCode = Invoke-Helper @('-Action', 'AssertFree', '-Root', $root, '-PidFile', $outsidePidFile, '-Port', [string]$freePort)
    Assert-True ($exitCode -ne 0) 'helper must reject PID files outside the project root'

    New-Item -ItemType Junction -Path $junctionPath -Target $foreignTempRoot | Out-Null
    $junctionPidFile = Join-Path $junctionPath 'escaped.pid.json'
    $freePort = Get-FreePort
    $exitCode = Invoke-Helper @('-Action', 'AssertFree', '-Root', $root, '-PidFile', $junctionPidFile, '-Port', [string]$freePort)
    Assert-True ($exitCode -ne 0) 'helper must reject PID files whose parent path escapes through a reparse point'

    $foreignPort = Get-FreePort
    $foreign = Start-TestListener $foreignPort 'foreign-listener' $foreignServerScript
    $foreignPidFile = Join-Path $tempRoot 'foreign.pid.json'

    $exitCode = Invoke-Helper @('-Action', 'AssertFree', '-Root', $root, '-PidFile', $foreignPidFile, '-Port', [string]$foreignPort)
    Assert-True ($exitCode -ne 0) 'occupied-port check must fail'
    Assert-True (-not $foreign.HasExited) 'occupied-port check must not terminate the foreign process'

    $exitCode = Invoke-Helper @('-Action', 'Stop', '-Root', $root, '-PidFile', $foreignPidFile, '-Port', [string]$foreignPort)
    Assert-True ($exitCode -eq 0) 'stop without a PID record must be non-destructive'
    Assert-True (-not $foreign.HasExited) 'stop without a PID record killed a foreign process'

    $ownedPort = Get-FreePort
    $ownedPidFile = Join-Path $tempRoot 'owned.pid.json'

    $exitCode = Invoke-Helper @('-Action', 'Launch', '-Root', $root, '-PidFile', $ownedPidFile, '-Port', [string]$ownedPort, '-LaunchMode', 'NodeScript', '-Executable', $NodeExecutable, '-ScriptPath', $serverScript)
    Assert-True ($exitCode -eq 0) 'launch must start and record its own verified listener'
    Assert-True (Test-Path -LiteralPath $ownedPidFile) 'verified PID record was not created'
    $ownedRecord = Get-Content -LiteralPath $ownedPidFile -Raw | ConvertFrom-Json
    $owned = Get-Process -Id ([int]$ownedRecord.pid)
    $processes.Add($owned)

    $exitCode = Invoke-Helper @('-Action', 'Stop', '-Root', $root, '-PidFile', $ownedPidFile, '-Port', [string]$ownedPort)
    Assert-True ($exitCode -eq 0) 'stop must terminate a verified project listener'
    $owned.WaitForExit(5000) | Out-Null
    Assert-True $owned.HasExited 'verified project listener remained alive'
    Assert-True (-not (Test-Path -LiteralPath $ownedPidFile)) 'PID record was not removed after a verified stop'

    $pathPort = Get-FreePort
    $pathPidFile = Join-Path $tempRoot 'path-command.pid.json'
    $oldPath = $env:PATH
    try {
        $env:PATH = (Split-Path -Parent $NodeExecutable) + [IO.Path]::PathSeparator + $env:PATH
        $nodeCommand = Split-Path -Leaf $NodeExecutable
        $exitCode = Invoke-Helper @('-Action', 'Launch', '-Root', $root, '-PidFile', $pathPidFile, '-Port', [string]$pathPort, '-LaunchMode', 'NodeScript', '-Executable', $nodeCommand, '-ScriptPath', $serverScript)
        Assert-True ($exitCode -eq 0) 'launch must resolve executables available only through PATH'
        $pathRecord = Get-Content -LiteralPath $pathPidFile -Raw | ConvertFrom-Json
        $pathOwned = Get-Process -Id ([int]$pathRecord.pid)
        $processes.Add($pathOwned)
        $exitCode = Invoke-Helper @('-Action', 'Stop', '-Root', $root, '-PidFile', $pathPidFile, '-Port', [string]$pathPort)
        Assert-True ($exitCode -eq 0) 'stop must terminate a listener launched through a PATH executable'
        $pathOwned.WaitForExit(5000) | Out-Null
        Assert-True $pathOwned.HasExited 'PATH-launched listener remained alive'
    } finally {
        $env:PATH = $oldPath
    }

    $batchFile = Get-ChildItem -LiteralPath $root -Filter '*.bat' -File |
        Where-Object { (Get-Content -LiteralPath $_.FullName -Raw) -match 'newapi-web-process\.ps1' } |
        Select-Object -First 1
    Assert-True ($null -ne $batchFile) 'startup batch file using the process helper was not found'
    $batch = Get-Content -LiteralPath $batchFile.FullName -Raw
    Assert-True ($batch -match '-Action AssertFree') 'batch startup does not reject occupied ports through the helper'
    Assert-True ($batch -match '-Action Launch') 'batch startup does not launch through the ownership-verifying helper'
    Assert-True ($batch -notmatch '-Action Save') 'batch still records an arbitrary existing listener'
    Assert-True ($batch -match '-Action Stop') 'batch stop does not use the verified PID helper'
    Assert-True ($batch -notmatch 'Get-NetTCPConnection|OwningProcess') 'batch file still enumerates and kills frontend port owners directly'

    Write-Output 'newapi web process safety tests passed'
} finally {
    foreach ($process in $processes) {
        if (-not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        }
    }
    $resolvedRoot = [IO.Path]::GetFullPath($root).TrimEnd('\') + '\'
    $resolvedJunctionPath = [IO.Path]::GetFullPath($junctionPath)
    if ($resolvedJunctionPath.StartsWith($resolvedRoot, [StringComparison]::OrdinalIgnoreCase) -and [IO.Directory]::Exists($resolvedJunctionPath)) {
        [IO.Directory]::Delete($resolvedJunctionPath)
    }
    $resolvedTempRoot = [IO.Path]::GetFullPath($tempRoot)
    if ($resolvedTempRoot.StartsWith($resolvedRoot, [StringComparison]::OrdinalIgnoreCase)) {
        Remove-Item -LiteralPath $resolvedTempRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
    $systemTempPrefix = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    $resolvedForeignTempRoot = [IO.Path]::GetFullPath($foreignTempRoot)
    if ($resolvedForeignTempRoot.StartsWith($systemTempPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        Remove-Item -LiteralPath $resolvedForeignTempRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
