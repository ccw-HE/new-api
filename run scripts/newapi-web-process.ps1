param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('AssertFree', 'Launch', 'Stop')]
    [string]$Action,

    [Parameter(Mandatory = $true)]
    [string]$Root,

    [Parameter(Mandatory = $true)]
    [string]$PidFile,

    [Parameter(Mandatory = $true)]
    [ValidateRange(1, 65535)]
    [int]$Port,

    [ValidateSet('BunDev', 'NodeScript')]
    [string]$LaunchMode,

    [string]$Executable,

    [string]$ScriptPath
)

$ErrorActionPreference = 'Stop'
$normalizedRoot = [IO.Path]::GetFullPath($Root).TrimEnd('\', '/')
$normalizedPidFile = [IO.Path]::GetFullPath($PidFile)
$rootPrefix = $normalizedRoot + [IO.Path]::DirectorySeparatorChar

function Assert-SafePidFile {
    if (-not $normalizedPidFile.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "PID file must be located inside the project root: $normalizedRoot"
    }
    $relativePidFile = $normalizedPidFile.Substring($rootPrefix.Length)
    $relativeParent = [IO.Path]::GetDirectoryName($relativePidFile)
    if (-not [string]::IsNullOrEmpty($relativeParent)) {
        $currentPath = $normalizedRoot
        foreach ($segment in ($relativeParent -split '[\\/]')) {
            if ([string]::IsNullOrEmpty($segment)) {
                continue
            }
            $currentPath = Join-Path $currentPath $segment
            if (Test-Path -LiteralPath $currentPath) {
                $pathItem = Get-Item -LiteralPath $currentPath -Force
                if (($pathItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                    throw "PID file parent path must not contain a reparse point: $currentPath"
                }
            }
        }
    }
    if (Test-Path -LiteralPath $normalizedPidFile) {
        $pidFileItem = Get-Item -LiteralPath $normalizedPidFile -Force
        if (($pidFileItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "PID file must not be a reparse point: $normalizedPidFile"
        }
    }
}

function Get-SafeProjectPath([string]$Path) {
    $normalizedPath = [IO.Path]::GetFullPath($Path).TrimEnd('\', '/')
    if ($normalizedPath -ne $normalizedRoot -and -not $normalizedPath.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Path must be located inside the project root: $normalizedPath"
    }
    $relativePath = $normalizedPath.Substring($normalizedRoot.Length).TrimStart('\', '/')
    $currentPath = $normalizedRoot
    foreach ($segment in ($relativePath -split '[\\/]')) {
        if ([string]::IsNullOrEmpty($segment)) {
            continue
        }
        $currentPath = Join-Path $currentPath $segment
        if (Test-Path -LiteralPath $currentPath) {
            $pathItem = Get-Item -LiteralPath $currentPath -Force
            if (($pathItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw "Project path must not contain a reparse point: $currentPath"
            }
        }
    }
    return $normalizedPath
}

Assert-SafePidFile

function Get-ListenerProcessId {
    $processIds = @(
        Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue |
            Where-Object { $_.OwningProcess -gt 0 } |
            Select-Object -ExpandProperty OwningProcess -Unique
    )
    if ($processIds.Count -eq 0) {
        return $null
    }
    if ($processIds.Count -ne 1) {
        throw "Port $Port has multiple listener owners; refusing to choose one."
    }
    return [int]$processIds[0]
}

function Get-ProcessIdentity([int]$ProcessId) {
    $process = Get-CimInstance Win32_Process -Filter "ProcessId = $ProcessId" -ErrorAction SilentlyContinue
    if (-not $process) {
        return $null
    }
    return [pscustomobject]@{
        pid = [int]$process.ProcessId
        parent_pid = [int]$process.ParentProcessId
        created_at_ticks = $process.CreationDate.ToUniversalTime().Ticks.ToString([Globalization.CultureInfo]::InvariantCulture)
        name = [string]$process.Name
        command_line = [string]$process.CommandLine
    }
}

function Test-IdentityBelongsToProject($Identity) {
    if ($null -eq $Identity) {
        return $false
    }
    $allowedProcessNames = @('node.exe', 'bun.exe')
    if ($allowedProcessNames -notcontains $Identity.name) {
        return $false
    }
    return $Identity.command_line.IndexOf($normalizedRoot, [StringComparison]::OrdinalIgnoreCase) -ge 0
}

function Save-ListenerRecord($Identity, [int]$LauncherProcessId) {
    $record = [ordered]@{
        version = 2
        root = $normalizedRoot
        port = $Port
        pid = $Identity.pid
        created_at_ticks = $Identity.created_at_ticks
        command_line = $Identity.command_line
        launcher_pid = $LauncherProcessId
    }
    Assert-SafePidFile
    $record | ConvertTo-Json -Depth 3 | Set-Content -LiteralPath $normalizedPidFile -Encoding UTF8
}

function Test-OrAdoptOwnedListener([int]$ListenerProcessId) {
    $identity = Get-ProcessIdentity $ListenerProcessId
    if ($null -eq $identity) {
        return $false
    }
    if (Test-Path -LiteralPath $normalizedPidFile -PathType Leaf) {
        try {
            $record = Get-Content -LiteralPath $normalizedPidFile -Raw | ConvertFrom-Json
            $recordMatches = [int]$record.version -eq 2 -and
                [string]$record.root -eq $normalizedRoot -and
                [int]$record.port -eq $Port -and
                [int]$record.pid -eq $identity.pid -and
                [string]$record.created_at_ticks -eq $identity.created_at_ticks -and
                [string]$record.command_line -eq $identity.command_line
            if ($recordMatches) {
                return $true
            }
        } catch {
        }
    }
    if (-not (Test-IdentityBelongsToProject $identity)) {
        return $false
    }
    Save-ListenerRecord $identity 0
    Write-Output "Adopted existing project WebUI PID: $ListenerProcessId"
    return $true
}

function Test-ProcessDescendsFrom([int]$ProcessId, [int]$AncestorProcessId) {
    $visited = @{}
    $currentProcessId = $ProcessId
    for ($depth = 0; $depth -lt 64 -and $currentProcessId -gt 0; $depth++) {
        if ($currentProcessId -eq $AncestorProcessId) {
            return $true
        }
        if ($visited.ContainsKey($currentProcessId)) {
            return $false
        }
        $visited[$currentProcessId] = $true
        $identity = Get-ProcessIdentity $currentProcessId
        if ($null -eq $identity) {
            return $false
        }
        $currentProcessId = $identity.parent_pid
    }
    return $false
}

switch ($Action) {
    'AssertFree' {
        $listenerPid = Get-ListenerProcessId
        if ($null -ne $listenerPid) {
            if (Test-OrAdoptOwnedListener $listenerPid) {
                exit 0
            }
            Write-Error "Port $Port is already used by PID $listenerPid. Refusing to reuse or terminate it."
            exit 1
        }
        exit 0
    }

    'Launch' {
        $launchedProcess = $null
        try {
            $existingListenerPid = Get-ListenerProcessId
            if ($null -ne $existingListenerPid -and (Test-OrAdoptOwnedListener $existingListenerPid)) {
                Write-Output "WebUI is already running with verified PID: $existingListenerPid"
                exit 0
            }
            if ($null -ne $existingListenerPid) {
                throw "Port $Port is already in use. Refusing to launch or terminate its owner."
            }
            if ([string]::IsNullOrWhiteSpace($Executable)) {
                throw 'Launch executable is required.'
            }
            $resolvedExecutable = $null
            if (Test-Path -LiteralPath $Executable -PathType Leaf) {
                $resolvedExecutable = [IO.Path]::GetFullPath($Executable)
            } else {
                $command = Get-Command $Executable -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
                if ($null -ne $command) {
                    $resolvedExecutable = $command.Source
                }
            }
            if ([string]::IsNullOrWhiteSpace($resolvedExecutable)) {
                throw "Launch executable was not found: $Executable"
            }
            switch ($LaunchMode) {
                'BunDev' {
                    $workingDirectory = Get-SafeProjectPath (Join-Path $normalizedRoot 'web\default')
                    if (-not (Test-Path -LiteralPath $workingDirectory -PathType Container)) {
                        throw "WebUI working directory was not found: $workingDirectory"
                    }
                    $launchArguments = @('run', 'dev', '--', '--host', '0.0.0.0', '--port', [string]$Port)
                }
                'NodeScript' {
                    if ([string]::IsNullOrWhiteSpace($ScriptPath)) {
                        throw 'NodeScript launch requires ScriptPath.'
                    }
                    $normalizedScriptPath = Get-SafeProjectPath $ScriptPath
                    if (-not (Test-Path -LiteralPath $normalizedScriptPath -PathType Leaf)) {
                        throw "Node test script was not found: $normalizedScriptPath"
                    }
                    $workingDirectory = Split-Path -Parent $normalizedScriptPath
                    $launchArguments = @($normalizedScriptPath, [string]$Port, $normalizedRoot)
                }
                default {
                    throw 'LaunchMode is required for Launch.'
                }
            }

            $launchedProcess = Start-Process -FilePath $resolvedExecutable -ArgumentList $launchArguments -WorkingDirectory $workingDirectory -WindowStyle Hidden -PassThru
            $listenerPid = $null
            for ($i = 0; $i -lt 600; $i++) {
                if ($launchedProcess.HasExited) {
                    throw "Launched WebUI process exited before port $Port began listening."
                }
                $listenerPid = Get-ListenerProcessId
                if ($null -ne $listenerPid) {
                    break
                }
                Start-Sleep -Milliseconds 100
            }
            if ($null -eq $listenerPid) {
                throw "WebUI did not begin listening on port $Port within 60 seconds."
            }
            if (-not (Test-ProcessDescendsFrom $listenerPid $launchedProcess.Id)) {
                throw "Port $Port was claimed by PID $listenerPid outside the launched process tree."
            }
            $identity = Get-ProcessIdentity $listenerPid
            if ($null -eq $identity) {
                throw "Listener PID $listenerPid exited before its identity could be recorded."
            }
            Save-ListenerRecord $identity $launchedProcess.Id
            Write-Output "WebUI launched and PID recorded: $listenerPid"
            exit 0
        } catch {
            if ($null -ne $launchedProcess -and -not $launchedProcess.HasExited) {
                Stop-Process -Id $launchedProcess.Id -Force -ErrorAction SilentlyContinue
            }
            Write-Error $_.Exception.Message -ErrorAction Continue
            exit 1
        }
    }

    'Stop' {
        if (-not (Test-Path -LiteralPath $normalizedPidFile -PathType Leaf)) {
            Write-Output 'No verified WebUI PID record was found. No frontend process was terminated.'
            exit 0
        }
        Assert-SafePidFile
        try {
            $record = Get-Content -LiteralPath $normalizedPidFile -Raw | ConvertFrom-Json
        } catch {
            Write-Warning "The WebUI PID record is invalid. No process was terminated; handle it manually."
            exit 0
        }
        if ([int]$record.version -ne 2 -or [string]$record.root -ne $normalizedRoot -or [int]$record.port -ne $Port -or [int]$record.pid -le 0) {
            Write-Warning "The WebUI PID record does not belong to this project. No process was terminated."
            exit 0
        }
        try {
            $processToStop = Get-Process -Id ([int]$record.pid) -ErrorAction Stop
            $null = $processToStop.Handle
        } catch {
            Write-Warning "Recorded WebUI PID $($record.pid) is no longer running. No process was terminated."
            exit 0
        }
        $identity = Get-ProcessIdentity ([int]$record.pid)
        if ($null -eq $identity -or $processToStop.HasExited) {
            Write-Warning "Recorded WebUI PID $($record.pid) exited during ownership validation. No process was terminated."
            exit 0
        }
        $listenerPid = Get-ListenerProcessId
        $identityMatches = $identity.created_at_ticks -eq [string]$record.created_at_ticks -and
            $identity.command_line -eq [string]$record.command_line
        if (-not $identityMatches -or $listenerPid -ne [int]$record.pid) {
            Write-Warning "Recorded WebUI PID $($record.pid) failed ownership validation. No process was terminated."
            exit 0
        }
        $processToStop.Kill()
        Assert-SafePidFile
        Remove-Item -LiteralPath $normalizedPidFile -Force
        Write-Output "Stopped verified WebUI PID: $($record.pid)"
        exit 0
    }
}
