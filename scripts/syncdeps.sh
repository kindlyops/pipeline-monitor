#!/usr/bin/env bash

set -euo pipefail

export RUNFILES_DIR="$PWD/.."
export PATH="$PWD/external/go_sdk/bin:$PATH"
gazelle="$PWD/$1"

echo "Using these commands"
command -v go
echo "$gazelle"

cd "$BUILD_WORKSPACE_DIRECTORY"
go mod tidy
go mod vendor
$gazelle update-repos -from_file=go.mod