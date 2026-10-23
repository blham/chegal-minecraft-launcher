#!/bin/sh
# Cross-compile release binaries (Go 1.24+, standard library only)
set -e
V=${1:-1.0.0}; OUT=dist; mkdir -p $OUT
LD="-s -w -X main.LauncherVersion=$V"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$LD -H=windowsgui" -o $OUT/KoreaWarLauncher-windows-x64.exe .
GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$LD -H=windowsgui" -o $OUT/KoreaWarLauncher-windows-arm64.exe .
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$LD" -o $OUT/KoreaWarLauncher-linux-x64 .
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$LD" -o $OUT/KoreaWarLauncher-linux-arm64 .
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$LD" -o $OUT/kwl-arm64 .
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$LD" -o $OUT/kwl-x64 .
