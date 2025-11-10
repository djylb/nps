#!/bin/bash

# NPC GUI Build Script
# This script helps build the NPC GUI application for different platforms

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}NPC GUI Build Script${NC}"
echo "========================"
echo ""

# Check if wails is installed
if ! command -v wails &> /dev/null; then
    echo -e "${RED}Error: Wails CLI is not installed${NC}"
    echo "Please install it with: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
    exit 1
fi

# Check platform
PLATFORM=$(uname -s)
echo -e "Platform detected: ${YELLOW}${PLATFORM}${NC}"

# Build flags
BUILD_FLAGS=""

# Platform-specific checks and flags
case "$PLATFORM" in
    Linux*)
        # Check for GTK and WebKit2GTK
        if ! pkg-config --exists gtk+-3.0; then
            echo -e "${RED}Error: GTK+ 3.0 development libraries not found${NC}"
            echo "Please install with:"
            echo "  Ubuntu/Debian: sudo apt-get install libgtk-3-dev libwebkit2gtk-4.1-dev"
            echo "  Fedora: sudo dnf install gtk3-devel webkit2gtk4.1-devel"
            echo "  Arch: sudo pacman -S webkit2gtk-4.1"
            exit 1
        fi
        
        # Check which webkit version is available
        if pkg-config --exists webkit2gtk-4.1; then
            echo -e "${GREEN}Using webkit2gtk-4.1${NC}"
            BUILD_FLAGS="-tags webkit2gtk_4_1"
        elif pkg-config --exists webkit2gtk-4.0; then
            echo -e "${GREEN}Using webkit2gtk-4.0${NC}"
        else
            echo -e "${RED}Error: WebKit2GTK not found${NC}"
            echo "Please install webkit2gtk development libraries"
            exit 1
        fi
        ;;
    Darwin*)
        echo -e "${GREEN}macOS detected - no additional dependencies required${NC}"
        ;;
    MINGW*|MSYS*|CYGWIN*)
        echo -e "${GREEN}Windows detected - no additional dependencies required${NC}"
        ;;
    *)
        echo -e "${YELLOW}Warning: Unknown platform, attempting to build anyway...${NC}"
        ;;
esac

# Build mode
MODE="${1:-production}"

echo ""
echo "Build mode: ${MODE}"
echo ""

# Run build
if [ "$MODE" = "dev" ]; then
    echo -e "${GREEN}Starting development mode...${NC}"
    wails dev $BUILD_FLAGS
else
    echo -e "${GREEN}Building production binary...${NC}"
    wails build $BUILD_FLAGS
    
    if [ $? -eq 0 ]; then
        echo ""
        echo -e "${GREEN}Build successful!${NC}"
        echo -e "Binary location: ${YELLOW}build/bin/${NC}"
        
        # List built files
        if [ -d "build/bin" ]; then
            echo ""
            echo "Built files:"
            ls -lh build/bin/
        fi
    else
        echo -e "${RED}Build failed${NC}"
        exit 1
    fi
fi
