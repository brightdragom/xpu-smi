#!/usr/bin/env bash
# Builds xpu-smi if needed, then runs it, forwarding all arguments.
#
# Examples:
#   ./scripts/run.sh                          # one-shot snapshot table
#   ./scripts/run.sh --watch                  # live TUI dashboard
#   ./scripts/run.sh --watch --interval 2s    # live TUI, 2s refresh
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

needs_build=0
if [ ! -x ./xpu-smi ]; then
  needs_build=1
elif [ -n "$(find . -name '*.go' -newer ./xpu-smi -print -quit)" ]; then
  needs_build=1
fi

if [ "$needs_build" -eq 1 ]; then
  ./scripts/build.sh
fi

exec ./xpu-smi "$@"
