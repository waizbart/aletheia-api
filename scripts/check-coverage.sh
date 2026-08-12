#!/bin/bash
set -e

THRESHOLD=99

echo "Running tests with coverage..."
go test \
  -coverprofile=coverage.out \
  -coverpkg=github.com/waizbart/aletheia-api/internal/... \
  ./tests/...

# Infrastructure adapters are excluded from the unit-coverage gate: they are
# exercised by the integration and e2e suites against real Postgres, OpenCV and
# an EVM node, where a mock would only assert that the mock was called.
head -1 coverage.out > coverage_filtered.out
tail -n +2 coverage.out \
  | grep -v "internal/repository/postgres" \
  | grep -v "internal/feature/" \
  | grep -v "internal/observability/" \
  | grep -v "internal/handler/observability" \
  | grep -v "factory.go" \
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
