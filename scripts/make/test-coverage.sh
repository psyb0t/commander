#!/bin/bash
# Race-enabled test run with a coverage floor. Writes the decimal percentage to
# coverage-percent.txt for the coverage badge and removes the raw profile.

set -euo pipefail

readonly MIN_TEST_COVERAGE="${MIN_TEST_COVERAGE:-85}"
readonly PROFILE="coverage.txt"
readonly PERCENT_FILE="coverage-percent.txt"

log() {
	local level="$1"
	shift

	printf '{"time":"%s","level":"%s","file":"%s","line":%d,"func":"%s","msg":"%s"}\n' \
		"$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$level" \
		"${BASH_SOURCE[1]##*/}" "${BASH_LINENO[0]}" "${FUNCNAME[1]:-main}" "$*" >&2
}

trap 'rm -f "$PROFILE"' EXIT

go test -race -count=1 -coverprofile="$PROFILE" ./...

pct="$(go tool cover -func="$PROFILE" | awk '$1 == "total:" { sub(/%$/, "", $3); print $3 }')"

if [ -z "$pct" ]; then
	log ERROR "no test coverage information available"
	exit 1
fi

printf '%s\n' "$pct" >"$PERCENT_FILE"

if [ "${pct%%.*}" -lt "$MIN_TEST_COVERAGE" ]; then
	log ERROR "coverage $pct% is below the minimum $MIN_TEST_COVERAGE%"
	exit 1
fi

log INFO "coverage $pct% meets the minimum $MIN_TEST_COVERAGE%"
