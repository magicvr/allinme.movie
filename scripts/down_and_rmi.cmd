@echo off

pushd "%~dp0\.."

REM Stop and remove containers, networks, volumes
echo Taking down docker compose services...
docker compose down
if errorlevel 1 (
    echo Failed to bring down services
    popd
    exit /b 1
)

REM Remove images defined in the compose file only
echo Removing images defined in the compose file...
for /f "tokens=2" %%i in ('docker compose config ^| findstr /R /C:"image:"') do (
    echo Removing image %%i
    docker image rm -f "%%i" 2>nul
)

REM Clean up any dangling images left behind
echo Removing dangling images...
docker image prune -f

echo Cleanup complete.

REM Return to original directory
popd
