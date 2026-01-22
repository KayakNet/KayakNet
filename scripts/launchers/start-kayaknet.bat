@echo off
REM KayakNet Launcher for Windows
REM Double-click this file to start KayakNet

title KayakNet

echo ════════════════════════════════════════════════════════════
echo                    Starting KayakNet...
echo ════════════════════════════════════════════════════════════
echo.

REM Find kayakd.exe
if exist "%~dp0kayakd.exe" (
    set KAYAKD=%~dp0kayakd.exe
) else if exist "kayakd.exe" (
    set KAYAKD=kayakd.exe
) else (
    echo Error: kayakd.exe not found!
    echo Please place this script in the same directory as kayakd.exe
    pause
    exit /b 1
)

REM Start with bootstrap and proxy
"%KAYAKD%" -i --bootstrap 203.161.33.237:4242 --proxy --name "user-%COMPUTERNAME%"

pause




