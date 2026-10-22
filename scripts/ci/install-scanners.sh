#!/usr/bin/env bash
set -euo pipefail
destination=${1:?scanner destination required}
mkdir -p "$destination"
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT
curl --fail --location --retry 3 https://github.com/gitleaks/gitleaks/releases/download/v8.30.1/gitleaks_8.30.1_linux_x64.tar.gz -o "$temporary/gitleaks.tar.gz"
echo "551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb  $temporary/gitleaks.tar.gz" | sha256sum --check
curl --fail --location --retry 3 https://github.com/aquasecurity/trivy/releases/download/v0.76.1/trivy_0.76.1_Linux-64bit.tar.gz -o "$temporary/trivy.tar.gz"
echo "c7cbe3137fb8a48a17574bede3dd6beb16742d1e9772929940fc90082c39d9c0  $temporary/trivy.tar.gz" | sha256sum --check
tar -xzf "$temporary/gitleaks.tar.gz" -C "$destination" gitleaks
tar -xzf "$temporary/trivy.tar.gz" -C "$destination" trivy
