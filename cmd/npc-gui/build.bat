@echo off
REM NPC GUI Build Script for Windows
REM This script helps build the NPC GUI application on Windows

echo NPC GUI Build Script
echo ========================
echo.

REM Check if wails is installed
where wails >nul 2>nul
if %ERRORLEVEL% NEQ 0 (
    echo Error: Wails CLI is not installed
    echo Please install it with: go install github.com/wailsapp/wails/v2/cmd/wails@latest
    echo Make sure Go bin directory is in your PATH
    exit /b 1
)

REM Build mode
set MODE=%1
if "%MODE%"=="" set MODE=production

echo Build mode: %MODE%
echo.

REM Run build
if "%MODE%"=="dev" (
    echo Starting development mode...
    wails dev
) else (
    echo Building production binary...
    wails build
    
    if %ERRORLEVEL% EQU 0 (
        echo.
        echo Build successful!
        echo Binary location: build\bin\
        
        REM List built files
        if exist "build\bin" (
            echo.
            echo Built files:
            dir build\bin\
        )
    ) else (
        echo Build failed
        exit /b 1
    )
)
