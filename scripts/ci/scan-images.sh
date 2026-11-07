#!/usr/bin/env bash
set -euo pipefail
directory=${1:?evidence directory required}
status=0
for kind in backend frontend postgres; do
  trivy image --input "$directory/bliss-$kind.tar" --scanners vuln --severity HIGH,CRITICAL --format json --output "$directory/$kind-vulnerabilities.json"
  trivy image --input "$directory/bliss-$kind.tar" --format cyclonedx --output "$directory/$kind-sbom.cdx.json"
  if ! trivy image --input "$directory/bliss-$kind.tar" --scanners vuln --severity HIGH,CRITICAL --exit-code 1; then
    status=1
  fi
done
exit "$status"
