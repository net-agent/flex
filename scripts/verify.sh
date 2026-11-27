#!/usr/bin/env bash
# Full local verification: formatting, vet, race tests, and the coverage gate.
set -euo pipefail

cd "$(dirname "$0")/.."

export GOCACHE="${GOCACHE:-$PWD/.gocache}"
export GOMODCACHE="${GOMODCACHE:-$PWD/.gomodcache}"

echo "== gofmt =="
unformatted="$(gofmt -l . | grep -vE '^(\./)?(\.gomodcache|\.gocache)/' || true)"
if [ -n "$unformatted" ]; then
  echo "$unformatted" >&2
  exit 1
fi

echo "== go vet =="
go vet ./...

echo "== go test -race -shuffle =="
go test -count=1 -race -shuffle=on -timeout 10m ./...

echo "== coverage =="
COVERAGE_MIN="${COVERAGE_MIN:-95}" ./coverage_test.sh
