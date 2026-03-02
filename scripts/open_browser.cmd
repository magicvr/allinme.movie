@echo off

pushd "%~dp0\.."

REM Open browser to localhost:20080/admin
echo Opening http://localhost:20080/admin in default browser...
start "" "http://localhost:20080/admin"
if errorlevel 1 (
    echo Failed to open browser.
)

REM Return to original directory
popd
