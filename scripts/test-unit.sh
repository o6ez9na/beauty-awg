#!/usr/bin/env bash
#
# Fast unit-test lane: every Go test that needs no external service.
#
# The database-backed tests in internal/store and internal/service skip
# themselves when AWG_TEST_DSN is unset (see internal/store/grant_exit_test.go),
# so `go test ./...` here exercises only the pure-logic tests and stays quick.
# For the database-backed suite, run scripts/test-integration.sh.
#
# Usage: scripts/test-unit.sh [extra `go test` flags]
set -euo pipefail
cd "$(dirname "$0")/.."

# Force the DB-backed tests onto their skip path even if a DSN happens to be
# exported in the current shell — this script is the "no dependencies" lane.
unset AWG_TEST_DSN

echo "==> Unit tests: go test -count=1 ./..."
go test -count=1 "$@" ./...
echo "==> Unit tests passed."
