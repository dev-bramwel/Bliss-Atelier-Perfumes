#!/usr/bin/env bash
set -euo pipefail
directory=${1:?evidence directory required}
for kind in backend frontend; do
  trivy image --input "$directory/bliss-$kind.tar" --scanners vuln --severity HIGH,CRITICAL --exit-code 1 --format json --output "$directory/$kind-vulnerabilities.json"
  trivy image --input "$directory/bliss-$kind.tar" --format cyclonedx --output "$directory/$kind-sbom.cdx.json"
done
trivy image --scanners vuln --severity HIGH,CRITICAL --exit-code 1 --format json --output "$directory/postgres-vulnerabilities.json" postgres:17.11-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24
