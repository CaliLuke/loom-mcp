#!/usr/bin/env bash
# Records in go.work.sum the module checksums that workspace-mode go commands
# need beyond the workspace modules' own go.sum files.
#
# Per-module refreshes (go get and go mod tidy, often with GOWORK=off) update
# only module go.sum files. Loading the full workspace module graph adds the
# missing go.mod checksums, so later workspace-mode builds, tests and
# regeneration leave go.work.sum unchanged. Run it after every dependency
# refresh; verify_generated.sh runs it to fail on an incomplete go.work.sum.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_command="${GO:-go}"

GOWORK="${ROOT_DIR}/go.work" "${go_command}" -C "${ROOT_DIR}" list -m all >/dev/null
