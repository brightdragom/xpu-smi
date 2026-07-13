#!/usr/bin/env bash
# Builds the xpu-smi binary at the repo root.
#
# NVIDIA's go-nvml dependency prints cgo "-Wdeprecated-declarations" warnings
# during compilation; these come from the NVIDIA header itself, not this
# project's code, and are not build failures.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

echo "==> go vet ./..."
go vet ./...

echo "==> go build -o xpu-smi ./cmd/xpu-smi"
go build -o xpu-smi ./cmd/xpu-smi

echo "==> build complete: $(pwd)/xpu-smi"
