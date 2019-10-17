#!/usr/bin/env bash

set -euo pipefail

export RUNFILES_DIR="$PWD/.."
export PATH="$PWD/external/go_sdk/bin:$PATH"
gazelle="$PWD/$1"

echo "Using these commands"
command -v go
echo "$gazelle"

cd "$BUILD_WORKSPACE_DIRECTORY"
# this makes it easy to confirm we are running go from the bazel toolchain
go version
go mod tidy
# mod vendor will vendor any new or changed deps
go mod vendor
# after we have new packages in vendor, run gazelle to generate BUILD files
$gazelle
# also run gazelle update-repos to fix the go_repositories rules in WORKSPACE to match go.mod
$gazelle update-repos -from_file=go.mod