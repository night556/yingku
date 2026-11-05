@echo off
setlocal
cd /d "%~dp0"
if not exist "yingku.exe" (
  echo Please build yingku.exe first: go build -o yingku.exe .
  pause
  exit /b 1
)
"%~dp0yingku.exe" -open -data "%~dp0data"
if errorlevel 1 pause
