#!/usr/bin/env bash
# Run the full test suite with coverage and enforce a minimum total.
# Usage: COVERAGE_MIN=95 ./coverage_test.sh
set -euo pipefail

COVERAGE_MIN="${COVERAGE_MIN:-95}"

# Examples are standalone commands (`package main`) covered by the build +
# Example tests in scripts/check-docs.sh, not by statement coverage. Excluding
# them keeps the gate focused on library behavior; everything else, including
# internal packages and the integration suite, is included.
packages="$(go list ./... | grep -v '/examples/')"
# shellcheck disable=SC2086
go test -count=1 -coverprofile=coverage.txt -covermode=atomic $packages

total="$(go tool cover -func=coverage.txt | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
echo "total coverage: ${total}% (minimum ${COVERAGE_MIN}%)"

awk -v total="${total}" -v min="${COVERAGE_MIN}" 'BEGIN {
  if (total + 0 < min + 0) {
    printf("coverage %.2f%% is below the required %.2f%%\n", total, min) > "/dev/stderr"
    exit 1
  }
}'
