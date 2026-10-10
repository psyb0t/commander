#!/bin/bash
# Security scan: govulncheck (reachable vulnerabilities) and semgrep, merged
# into one SARIF file. Runs inside the dev container via `make sec`.

set -euo pipefail

readonly SARIF_OUT="${SARIF_OUT:-sec.sarif}"
readonly SEMGREP_CONFIGS=(--config p/golang --config p/security-audit)

log() {
	local level="$1"
	shift

	printf '{"time":"%s","level":"%s","file":"%s","line":%d,"func":"%s","msg":"%s"}\n' \
		"$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$level" \
		"${BASH_SOURCE[1]##*/}" "${BASH_LINENO[0]}" "${FUNCNAME[1]:-main}" "$*" >&2
}

tmp="$(mktemp -d)"
trap 'rm -r "$tmp"' EXIT

log INFO "running govulncheck"
# SARIF mode exits 0 even with findings, so it only produces the report. The
# text run below gates, exiting non-zero on a reachable vulnerability.
go tool govulncheck -format sarif ./... >"$tmp/govulncheck.sarif"
gv_rc=0
go tool govulncheck ./... || gv_rc=$?

log INFO "running semgrep"
# --error gates on a finding, --sarif writes the report, --metrics=off keeps the
# scan from phoning home.
sg_rc=0
semgrep scan "${SEMGREP_CONFIGS[@]}" \
	--sarif --output "$tmp/semgrep.sarif" --metrics=off --error . || sg_rc=$?

log INFO "merging SARIF into $SARIF_OUT"
jq -s '{version:"2.1.0","$schema":"https://json.schemastore.org/sarif-2.1.0.json",runs:((.[0].runs // []) + (.[1].runs // []))}' \
	"$tmp/govulncheck.sarif" "$tmp/semgrep.sarif" >"$SARIF_OUT"

if [ "$gv_rc" -ne 0 ] || [ "$sg_rc" -ne 0 ]; then
	log ERROR "security findings govulncheck=$gv_rc semgrep=$sg_rc, see $SARIF_OUT"
	exit 1
fi

log INFO "no security findings"
