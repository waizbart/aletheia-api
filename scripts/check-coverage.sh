#!/bin/bash
set -e

THRESHOLD=99

echo "Running tests with coverage..."
# Both trees are measured: ./tests/... holds the black-box suites, and a few
# packages keep internal tests for logic with no exported surface (the RLP
# encoder, the transaction signer).
go test \
  -coverprofile=coverage.out \
  -coverpkg=github.com/waizbart/aletheia-api/internal/... \
  ./internal/... ./tests/...

# Two kinds of package sit outside the gate.
#
# Infrastructure adapters (Postgres, OpenCV, the observability exporters, the
# env-reading factories) are exercised by the integration and e2e suites against
# the real dependency, where a unit test with a mock would only assert that the
# mock was called.
#
# The dataset generator and the testdata resolver are offline benchmarking
# tooling rather than service code — they never run in production, and holding
# them to the service's bar would mean testing a downloader against the network.
head -1 coverage.out > coverage_filtered.out
tail -n +2 coverage.out \
  | grep -v "internal/repository/postgres" \
  | grep -v "internal/feature/" \
  | grep -v "internal/observability/" \
  | grep -v "internal/handler/observability" \
  | grep -v "factory.go" \
  | grep -v "internal/dataset/" \
  | grep -v "internal/testdata/" \
  >> coverage_filtered.out

COVERAGE=$(go tool cover -func=coverage_filtered.out | grep total | awk '{print $3}' | tr -d '%')

echo "Coverage: ${COVERAGE}% (threshold: ${THRESHOLD}%)"

PASS=$(echo "$COVERAGE $THRESHOLD" | awk '{print ($1 >= $2) ? 1 : 0}')

if [ "$PASS" = "0" ]; then
  echo "FAIL: coverage ${COVERAGE}% is below ${THRESHOLD}%"
  rm -f coverage.out coverage_filtered.out
  exit 1
fi

echo "OK: coverage meets threshold"
rm -f coverage.out coverage_filtered.out
