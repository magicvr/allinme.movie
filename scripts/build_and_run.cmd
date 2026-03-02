@echo off

pushd "%~dp0\.."

REM Build docker compose services
echo Building docker compose services...
docker compose build
if errorlevel 1 (
    echo Build failed
    popd
    exit /b 1
)

REM Run docker compose in detached mode
echo Starting services in background...
docker compose up -d
if errorlevel 1 (
    echo Failed to start services
    popd
    exit /b 1
)

echo Services are up and running.

REM Return to original directory
popd
