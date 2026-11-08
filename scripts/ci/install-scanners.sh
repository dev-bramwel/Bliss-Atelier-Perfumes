#!/usr/bin/env bash
set -euo pipefail
destination=${1:?scanner destination required}
mkdir -p "$destination"
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT

download_scanner() {
  local output=$1
  local release_url=$2
  local api_url=$3

  if ! curl --fail --location --retry 3 --retry-delay 2 "$release_url" -o "$output" 2>/dev/null; then
    echo "Direct download from $release_url failed or timed out; falling back to GitHub API asset endpoint..."
    curl --fail --location --retry 5 --retry-delay 2 -H "Accept: application/octet-stream" "$api_url" -o "$output"
  fi
}

download_scanner "$temporary/gitleaks.tar.gz" \
  "https://github.com/gitleaks/gitleaks/releases/download/v8.30.1/gitleaks_8.30.1_linux_x64.tar.gz" \
  "https://api.github.com/repos/gitleaks/gitleaks/releases/assets/378332058"
echo "551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb  $temporary/gitleaks.tar.gz" | sha256sum --check

download_scanner "$temporary/trivy.tar.gz" \
  "https://github.com/aquasecurity/trivy/releases/download/v0.76.1/trivy_0.76.1_Linux-64bit.tar.gz" \
  "https://api.github.com/repos/aquasecurity/trivy/releases/assets/662254617"
echo "c7cbe3137fb8a48a17574bede3dd6beb16742d1e9772929940fc90082c39d9c0  $temporary/trivy.tar.gz" | sha256sum --check

tar -xzf "$temporary/gitleaks.tar.gz" -C "$destination" gitleaks
tar -xzf "$temporary/trivy.tar.gz" -C "$destination" trivy
