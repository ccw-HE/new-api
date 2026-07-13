@echo off
setlocal EnableExtensions
cd /d "%~dp0"

set "ROOT=%~dp0"
set "PROJECT_ROOT=%~dp0."
set "WEB_PORT=3001"
set "PID_FILE=%ROOT%.newapi-web.pid.json"
set "WEB_PROCESS_HELPER=%ROOT%scripts\newapi-web-process.ps1"
set "LIFECYCLE_WATCHER=%ROOT%scripts\newapi-lifecycle-watch.ps1"
set "DOCKER_BIN=C:\Program Files\Docker\Docker\resources\bin"
set "DOCKER_DESKTOP=C:\Program Files\Docker\Docker\Docker Desktop.exe"
set "DOCKER_CLI=C:\Program Files\Docker\Docker\DockerCli.exe"
set "BUN_WINGET=%LOCALAPPDATA%\Microsoft\WinGet\Packages\Oven-sh.Bun_Microsoft.Winget.Source_8wekyb3d8bbwe\bun-windows-x64\bun.exe"
set "SOURCE_COMMIT=unknown"
set "IMAGE_COMMIT="
set "CLOSE_DOCKER_DESKTOP="

if exist "%DOCKER_BIN%\docker.exe" (
  set "PATH=%DOCKER_BIN%;%PATH%"
  set "DOCKER_EXE=%DOCKER_BIN%\docker.exe"
) else (
  set "DOCKER_EXE=docker"
)

if exist "%BUN_WINGET%" (
  set "BUN_EXE=%BUN_WINGET%"
) else (
  set "BUN_EXE=bun"
)

if /i "%~1"=="stop-all" (
  set "CLOSE_DOCKER_DESKTOP=1"
  goto stop
)
if /i "%~1"=="stop" goto stop
if /i "%~1"=="probe" goto probe
set "COMPOSE_BUILD_ARG="
if /i "%~1"=="build" set "COMPOSE_BUILD_ARG=--build"
if /i "%~1"=="rebuild" set "COMPOSE_BUILD_ARG=--build"

echo [1/6] Checking Docker...
call :CheckDocker
if errorlevel 1 goto failed

echo.
call :AutoBuildIfSourceChanged
if errorlevel 1 goto failed

if defined COMPOSE_BUILD_ARG (
  echo [2/6] Starting Docker services with rebuild...
) else (
  echo [2/6] Starting Docker services...
)
"%DOCKER_EXE%" compose -f docker-compose.dev.yml up -d %COMPOSE_BUILD_ARG%
if errorlevel 1 goto failed

echo.
echo [3/6] Checking Bun...
call :CheckBun
if errorlevel 1 goto failed

echo.
echo [4/6] Checking frontend dependencies...
if not exist "%ROOT%web\node_modules" (
  echo node_modules not found. Installing frontend dependencies...
  cd /d "%ROOT%web"
  "%BUN_EXE%" install
  if errorlevel 1 goto failed
) else (
  echo node_modules exists. Skip install.
)

echo.
echo [5/6] Starting frontend WebUI...
call :AssertWebPortFree
if errorlevel 1 goto failed
call :LaunchWeb
if errorlevel 1 goto failed

echo.
echo [6/6] Opening browser...
start "" "http://localhost:%WEB_PORT%"

echo.
echo new-api is starting or already running.
echo Backend: http://localhost:3000
echo WebUI:   http://localhost:%WEB_PORT%
echo.
echo Tip: backend rebuild is automatic when the source stamp changes; use "%~nx0" build to force it.
echo Keep this window open.
echo Press Ctrl+C in this window to stop WebUI, project services, and Docker Desktop.
echo.

set "NEWAPI_SCRIPT=%~f0"
powershell -NoProfile -ExecutionPolicy Bypass -Command "$watcher=$env:LIFECYCLE_WATCHER; $root=$env:PROJECT_ROOT; $script=$env:NEWAPI_SCRIPT; if (-not (Test-Path -LiteralPath $watcher)) { throw 'Lifecycle watcher not found: ' + $watcher }; Start-Process -FilePath powershell.exe -WindowStyle Hidden -ArgumentList @('-NoProfile','-ExecutionPolicy','Bypass','-File',$watcher,'-OwnerProcessId',[string]$PID,'-Root',$root,'-StartupScript',$script) | Out-Null; Write-Host 'Guarding. Press Ctrl+C to stop new-api and Docker Desktop.'; try { [Console]::TreatControlCAsInput=$true; while ($true) { if ([Console]::KeyAvailable) { $key=[Console]::ReadKey($true); if (($key.Key -eq 'C') -and (($key.Modifiers -band [ConsoleModifiers]::Control) -ne 0)) { break } }; Start-Sleep -Milliseconds 150 } } finally { try { [Console]::TreatControlCAsInput=$false } catch {}; if (Test-Path -LiteralPath $script) { $env:NEWAPI_NO_PAUSE='1'; & $script stop-all } else { Write-Host 'Startup script not found. Please stop services manually.' } }"
exit /b 0

:CheckDocker
if not exist "%DOCKER_EXE%" (
  where %DOCKER_EXE% >nul 2>nul
  if errorlevel 1 (
    echo Docker command not found. Please install Docker Desktop first.
    exit /b 1
  )
)

"%DOCKER_EXE%" info >nul 2>nul
if not errorlevel 1 (
  call :HideDockerDesktopWindow
  exit /b 0
)

echo Docker Engine is not ready. Trying to open Docker Desktop...
if exist "%DOCKER_DESKTOP%" call :StartDockerDesktop

echo Waiting for Docker Engine. This may take a few minutes...
for /l %%i in (1,1,180) do (
  "%DOCKER_EXE%" info >nul 2>nul
  if not errorlevel 1 exit /b 0
  timeout /t 1 /nobreak >nul
)

echo Docker Engine did not become ready in time.
exit /b 1

:CheckBun
if not exist "%BUN_EXE%" (
  where %BUN_EXE% >nul 2>nul
  if errorlevel 1 (
    echo Bun command not found. Please install Bun first.
    echo Suggested command: winget install --id Oven-sh.Bun -e
    exit /b 1
  )
)
exit /b 0

:AutoBuildIfSourceChanged
call :DetectSourceCommit
if defined COMPOSE_BUILD_ARG exit /b 0

set "IMAGE_COMMIT="
for /f "usebackq delims=" %%i in (`powershell -NoProfile -ExecutionPolicy Bypass -Command "$docker=$env:DOCKER_EXE; try { $image = & $docker image inspect new-api-dev:local 2>$null | ConvertFrom-Json; if ($image) { $label = $image[0].Config.Labels.'org.opencontainers.image.revision'; if ($label) { [Console]::Write($label) } } } catch {}"`) do set "IMAGE_COMMIT=%%i"

if not defined IMAGE_COMMIT (
  echo Backend image is missing or has no source stamp. Rebuild required.
  set "COMPOSE_BUILD_ARG=--build"
  exit /b 0
)

if /i not "%IMAGE_COMMIT%"=="%SOURCE_COMMIT%" (
  echo Backend source changed: %IMAGE_COMMIT% -^> %SOURCE_COMMIT%. Rebuild required.
  set "COMPOSE_BUILD_ARG=--build"
)
exit /b 0

:DetectSourceCommit
set "SOURCE_COMMIT=unknown"
for /f "usebackq delims=" %%i in (`powershell -NoProfile -ExecutionPolicy Bypass -Command "$root=(Resolve-Path '.').Path; $paths=@('main.go','go.mod','go.sum','VERSION','.dockerignore','Dockerfile.dev','docker-compose.dev.yml','common','constant','controller','dto','i18n','logger','middleware','model','oauth','pkg','relay','router','service','setting','types'); $files=@(); foreach($path in $paths){ if(Test-Path -LiteralPath $path -PathType Leaf){ $files += Get-Item -LiteralPath $path } elseif(Test-Path -LiteralPath $path -PathType Container){ $files += Get-ChildItem -LiteralPath $path -Recurse -File -Force } }; $files = $files | Sort-Object FullName -Unique; $sha=[Security.Cryptography.SHA256]::Create(); foreach($file in $files){ $rel=$file.FullName.Substring($root.Length).TrimStart('\','/').Replace('\','/'); $name=[Text.Encoding]::UTF8.GetBytes($rel + [char]10); $null=$sha.TransformBlock($name,0,$name.Length,$null,0); $bytes=[IO.File]::ReadAllBytes($file.FullName); $null=$sha.TransformBlock($bytes,0,$bytes.Length,$null,0) }; $null=$sha.TransformFinalBlock([byte[]]::new(0),0,0); $hash=([BitConverter]::ToString($sha.Hash)-replace '-','').Substring(0,16).ToLowerInvariant(); [Console]::Write($hash)"`) do set "SOURCE_COMMIT=%%i"
exit /b 0

:probe
call :AutoBuildIfSourceChanged
echo Source stamp: %SOURCE_COMMIT%
if defined IMAGE_COMMIT echo Image stamp:  %IMAGE_COMMIT%
if defined COMPOSE_BUILD_ARG (
  echo Backend action: rebuild
) else (
  echo Backend action: reuse
)
exit /b 0

:AssertWebPortFree
if not exist "%WEB_PROCESS_HELPER%" (
  echo WebUI process helper not found: %WEB_PROCESS_HELPER%
  exit /b 1
)
powershell -NoProfile -ExecutionPolicy Bypass -File "%WEB_PROCESS_HELPER%" -Action AssertFree -Root "%PROJECT_ROOT%" -PidFile "%PID_FILE%" -Port %WEB_PORT%
exit /b %errorlevel%

:LaunchWeb
powershell -NoProfile -ExecutionPolicy Bypass -File "%WEB_PROCESS_HELPER%" -Action Launch -Root "%PROJECT_ROOT%" -PidFile "%PID_FILE%" -Port %WEB_PORT% -LaunchMode BunDev -Executable "%BUN_EXE%"
exit /b %errorlevel%

:stop
echo.
echo Stopping frontend WebUI...
if exist "%WEB_PROCESS_HELPER%" (
  powershell -NoProfile -ExecutionPolicy Bypass -File "%WEB_PROCESS_HELPER%" -Action Stop -Root "%PROJECT_ROOT%" -PidFile "%PID_FILE%" -Port %WEB_PORT%
) else (
  echo WebUI process helper not found. No frontend process was terminated; handle it manually.
)

echo.
echo Stopping Docker services...
if not exist "%DOCKER_EXE%" (
  where %DOCKER_EXE% >nul 2>nul
  if errorlevel 1 (
    echo Docker command not found. Skip Docker shutdown.
    if defined CLOSE_DOCKER_DESKTOP call :CloseDockerDesktop
    if "%NEWAPI_NO_PAUSE%"=="1" exit /b 0
    pause
    exit /b 0
  )
)

"%DOCKER_EXE%" info >nul 2>nul
if errorlevel 1 (
  echo Docker Engine is not running. Skip Docker shutdown.
  if defined CLOSE_DOCKER_DESKTOP call :CloseDockerDesktop
  if "%NEWAPI_NO_PAUSE%"=="1" exit /b 0
  pause
  exit /b 0
)

"%DOCKER_EXE%" compose -f docker-compose.dev.yml stop
if errorlevel 1 (
  echo Docker shutdown failed.
  if defined CLOSE_DOCKER_DESKTOP call :CloseDockerDesktop
  if "%NEWAPI_NO_PAUSE%"=="1" exit /b 1
  pause
  exit /b 1
)

if defined CLOSE_DOCKER_DESKTOP call :CloseDockerDesktop

echo.
echo new-api development environment stopped.
echo Containers are stopped and kept for faster next start.
echo Test database volume is kept.
if defined CLOSE_DOCKER_DESKTOP (
  echo Docker Desktop shutdown requested.
) else (
  echo Docker Desktop is left running. Use "%~nx0" stop-all to close Docker Desktop too.
)
echo To fully remove stopped containers and networks manually, run:
echo docker compose -f docker-compose.dev.yml down
echo To delete test database volume manually, run:
echo docker compose -f docker-compose.dev.yml down -v

if "%NEWAPI_NO_PAUSE%"=="1" exit /b 0
pause
exit /b 0

:failed
echo.
echo Startup failed. Please check the error message above.
echo You can also run this script with "stop" to clean up:
echo "%~nx0" stop
pause
exit /b 1

:StartDockerDesktop
if not exist "%DOCKER_DESKTOP%" exit /b 0
powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process -FilePath $env:DOCKER_DESKTOP -WindowStyle Minimized -ErrorAction SilentlyContinue"
timeout /t 3 /nobreak >nul
call :HideDockerDesktopWindow
exit /b 0

:HideDockerDesktopWindow
powershell -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -Command "$q=[char]34; $src='using System;using System.Runtime.InteropServices;public static class Win32{[DllImport('+$q+'user32.dll'+$q+')]public static extern bool ShowWindowAsync(IntPtr hWnd,int nCmdShow);}'; try { Add-Type -TypeDefinition $src -ErrorAction SilentlyContinue } catch {}; Get-Process -Name 'Docker Desktop' -ErrorAction SilentlyContinue | ForEach-Object { if ($_.MainWindowHandle -ne 0) { [Win32]::ShowWindowAsync($_.MainWindowHandle, 6) | Out-Null } }"
exit /b 0

:CloseDockerDesktop
echo.
echo Closing Docker Desktop...
powershell -NoProfile -ExecutionPolicy Bypass -Command "$dockerCli=$env:DOCKER_CLI; if (Test-Path -LiteralPath $dockerCli) { Start-Process -FilePath $dockerCli -ArgumentList '-Shutdown' -WindowStyle Hidden -ErrorAction SilentlyContinue }; $cmd='Start-Sleep -Milliseconds 700; Stop-Process -Name ''Docker Desktop'',''com.docker.backend'',''com.docker.build'',''docker-sandbox'' -Force -ErrorAction SilentlyContinue'; Start-Process -FilePath powershell.exe -WindowStyle Hidden -ArgumentList @('-NoProfile','-ExecutionPolicy','Bypass','-Command',$cmd)"
exit /b 0
