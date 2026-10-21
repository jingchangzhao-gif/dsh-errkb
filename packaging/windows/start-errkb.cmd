@echo off
rem Open the errkb toolbox. `errkb ui --web` starts the daemon itself when needed
rem and keeps it running after the window closes.
setlocal
set "APP=%~dp0"
set "EXE=%APP%errkb.exe"
if not exist "%EXE%" (
  echo errkb.exe not found next to this launcher.
  pause
  exit /b 1
)
start "" "%EXE%" ui --web
exit /b 0
