@echo off
setlocal
cd /d "%~dp0"
if not exist "yingku.exe" (
  echo Build yingku.exe with go build -o yingku.exe ./cmd/yingku
  pause
  exit /b 1
)
"%~dp0yingku.exe" -open -data "%~dp0data"
if errorlevel 1 pause
