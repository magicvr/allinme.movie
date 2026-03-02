@echo off

pushd "%~dp0\.."

REM Open browser to localhost:20080
echo Opening http://localhost:20080 in default browser...
start "" "http://localhost:20080"
if errorlevel 1 (
    echo Failed to open browser.
)

REM Return to original directory
popd
