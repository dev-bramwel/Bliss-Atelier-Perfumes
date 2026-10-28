#!/usr/bin/env bash
set -euo pipefail
directory=${1:?evidence directory required}
for kind in backend frontend postgres; do
  trivy image --input "$directory/bliss-$kind.tar" --scanners vuln --severity HIGH,CRITICAL --exit-code 1 --format json --output "$directory/$kind-vulnerabilities.json"
  trivy image --input "$directory/bliss-$kind.tar" --format cyclonedx --output "$directory/$kind-sbom.cdx.json"
done
