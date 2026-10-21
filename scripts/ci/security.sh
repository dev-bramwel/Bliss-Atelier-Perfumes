#!/usr/bin/env bash
set -euo pipefail
reports=${1:?report directory required}
mkdir -p "$reports"
gitleaks git --redact=100 --no-banner --report-format json --report-path "$reports/secrets.json" --log-opts="--all"
(cd backend && go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 -json ./...) > "$reports/go-vulnerabilities.json"
# An unregistered temporary key never reaches Git. Prove detection fails closed.
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$fixture/probe.pem"
set +e
gitleaks dir "$fixture" --redact=100 --no-banner --report-format json --report-path "$reports/scanner-negative-control.json"
result=$?
set -e
test "$result" -eq 1
printf '{"secretDetectionRejectsBrokenInput":true}\n' > "$reports/negative-control.json"
