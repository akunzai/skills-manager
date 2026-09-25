#!/usr/bin/env bash
# Prints a Markdown table of one test run for a CI job summary: how many tests
# passed and were skipped, and the statement coverage.
#
#   test-stats.sh LOG PROFILE
#
# LOG is `go test -v` output and PROFILE its -coverprofile. Subtests count as
# tests. Coverage is shown, never gated (docs/agents/pull-request.md).
set -euo pipefail

log=${1:?usage: test-stats.sh LOG PROFILE}
profile=${2:?usage: test-stats.sh LOG PROFILE}

count() { grep -cE "^[[:space:]]*--- $1: " "$log" || true; }
coverage=$(go tool cover -func="$profile" | awk '$1 == "total:" { print $NF }')

printf '### Tests\n\n| Passed | Skipped | Coverage |\n| ---: | ---: | ---: |\n| %s | %s | %s |\n' \
	"$(count PASS)" "$(count SKIP)" "$coverage"
