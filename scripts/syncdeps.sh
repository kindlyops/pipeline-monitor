#!/usr/bin/env bash

set -euo pipefail

export RUNFILES_DIR="$PWD/.."
export PATH="$PWD/external/go_sdk/bin:$PATH"
local gazelle="$PWD/$1"

echo "Using these commands"
command -v go
echo "$gazelle"


go tidy
go mod vendor
$gazelle update-repos -from_file=go.mod