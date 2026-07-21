#!/usr/bin/env bash
#
# Integration-test lane: the database-backed tests (internal/store,
# internal/service) against a real PostgreSQL. Slower than the unit lane because
# it stands up a Postgres and migrates it.
#
# Dependency handling — three ways it can find a database, in priority order:
#   1. AWG_TEST_DSN already set  -> that database is used as-is (host `go test`).
#   2. Docker, host can reach the published port (normal Linux / CI runners)
#      -> a throwaway postgres:17-alpine is published on localhost and torn down
#      on exit.
#   3. Docker, but published ports are NOT reachable from the host (e.g. Docker
#      Desktop on WSL2, which does not forward ports into every WSL distro)
#      -> `go test` is run from a sibling golang container on the same Docker
#      network, reaching Postgres by container name. No host port needed.
#
# Either way nothing is left behind. Usage:
#   scripts/test-integration.sh [extra `go test` flags]
set -euo pipefail
cd "$(dirname "$0")/.."

PG_IMAGE="postgres:17-alpine"
GO_IMAGE="golang:1.26" # Debian-based: ships gcc, so `-race` (cgo) works.

# Discover the packages that carry database-backed tests, so this stays correct
# if more are added later. Paths are repo-relative (e.g. ./internal/store).
mapfile -t DB_DIRS < <(grep -rl 'AWG_TEST_DSN' --include='*_test.go' . | xargs -r -n1 dirname | sort -u)
if [ "${#DB_DIRS[@]}" -eq 0 ]; then
	echo "No database-backed tests found (nothing references AWG_TEST_DSN)." >&2
	exit 0
fi

# --- Path 1: caller supplied a database ------------------------------------
if [ -n "${AWG_TEST_DSN:-}" ]; then
	echo "==> Using AWG_TEST_DSN from the environment."
	echo "==> go test -race -count=1 ${DB_DIRS[*]}"
	go test -race -count=1 "$@" "${DB_DIRS[@]}"
	echo "==> Integration tests passed."
	exit 0
fi

# --- Paths 2 & 3 need Docker ------------------------------------------------
if ! command -v docker >/dev/null 2>&1; then
	echo "docker not found and AWG_TEST_DSN is unset — cannot provide a database." >&2
	echo "Either install Docker or export AWG_TEST_DSN pointing at a test Postgres." >&2
	exit 1
fi

NETWORK="awg-test-net-$$"
CONTAINER="awg-test-pg-$$"
PG_PORT="${AWG_TEST_PG_PORT:-55432}"

cleanup() {
	docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
	docker network rm "$NETWORK" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker network create "$NETWORK" >/dev/null

echo "==> Starting throwaway Postgres ($CONTAINER)..."
docker run -d --rm --name "$CONTAINER" --network "$NETWORK" \
	-e POSTGRES_USER=awg -e POSTGRES_PASSWORD=testpw -e POSTGRES_DB=awgtest \
	-p "${PG_PORT}:5432" \
	--tmpfs /var/lib/postgresql/data \
	"$PG_IMAGE" >/dev/null

echo -n "==> Waiting for Postgres to accept connections"
ready=0
for _ in $(seq 1 60); do
	if docker exec "$CONTAINER" psql -U awg -d awgtest -tAc 'SELECT 1' >/dev/null 2>&1; then
		ready=1
		break
	fi
	echo -n "."
	sleep 0.5
done
echo
if [ "$ready" != 1 ]; then
	echo "Postgres did not become ready in time." >&2
	docker logs "$CONTAINER" 2>&1 | tail -20 >&2
	exit 1
fi

# Can the host actually reach the published port? (Not on Docker Desktop/WSL2.)
host_port_open() {
	(exec 3<>"/dev/tcp/127.0.0.1/${PG_PORT}") >/dev/null 2>&1 && exec 3>&- && return 0
	return 1
}
open=0
for _ in $(seq 1 10); do
	if host_port_open; then
		open=1
		break
	fi
	sleep 0.5
done

if [ "$open" = 1 ]; then
	# --- Path 2: host go test against the published port -------------------
	export AWG_TEST_DSN="postgres://awg:testpw@localhost:${PG_PORT}/awgtest?sslmode=disable"
	echo "==> Host reaches Postgres on :${PG_PORT}; running go test on the host."
	echo "==> go test -race -count=1 ${DB_DIRS[*]}"
	go test -race -count=1 "$@" "${DB_DIRS[@]}"
else
	# --- Path 3: sibling golang container on the DB network ----------------
	echo "==> Host cannot reach the published port (Docker Desktop/WSL2?);"
	echo "    running go test inside a ${GO_IMAGE} container on the DB network."
	echo "==> go test -race -count=1 ${DB_DIRS[*]}"
	docker run --rm --network "$NETWORK" \
		-v "$PWD":/src -w /src \
		-v awg-test-gomod:/go/pkg/mod \
		-v awg-test-gobuild:/root/.cache/go-build \
		-e CGO_ENABLED=1 \
		-e AWG_TEST_DSN="postgres://awg:testpw@${CONTAINER}:5432/awgtest?sslmode=disable" \
		"$GO_IMAGE" go test -race -count=1 "$@" "${DB_DIRS[@]}"
fi

echo "==> Integration tests passed."
