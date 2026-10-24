@echo off
rem Open the errkb toolbox. The first run creates the local PostgreSQL cluster
rem (PostgreSQL 16 binaries must be installed); later runs only make sure it is
rem up. `errkb ui --web` starts the daemon itself and keeps it running after
rem the window closes.
setlocal
set "APP=%~dp0"
set "EXE=%APP%errkb.exe"
set "PGDATA=%APP%pg\data"
if not exist "%EXE%" (
  echo errkb.exe not found next to this launcher.
  pause
  exit /b 1
)
if not exist "%PGDATA%\PG_VERSION" (
  echo First run: creating the local PostgreSQL cluster. PostgreSQL 16 binaries must be installed.
  "%EXE%" pg install --data-dir "%PGDATA%" --migrations "%APP%migrations"
  if errorlevel 1 (
    echo.
    echo The local database could not be created. See the messages above and the README.
    pause
    exit /b 1
  )
) else (
  "%EXE%" pg start --data-dir "%PGDATA%" >nul 2>&1
)
start "" "%EXE%" ui --web
exit /b 0
