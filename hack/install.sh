#!/usr/bin/env bash
# shellcheck disable=SC2086 
# Quick install script for k8s-netperf
# Downloads the latest release version based on system architecture and OS

set -euo pipefail

# Configuration
REPO="cloud-bulldozer/k8s-netperf"
INSTALL_DIR="${INSTALL_DIR:-${HOME}/.local/bin/}"

# Detect OS
detect_os() {
  local os
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  
  case "${os}" in
    linux*)
      echo "linux"
      ;;
    darwin*)
      echo "darwin"
      ;;
    mingw* | msys* | cygwin*)
      echo "windows"
      ;;
    *)
      echo "Unsupported operating system: ${os}"
      exit 1
      ;;
  esac
}

# Get latest release version from GitHub
get_latest_version() {
  local version
  if command -v curl &> /dev/null; then
    version=$(curl -sL "https://api.github.com/repos/${REPO}/releases/latest" | \
              grep '"tag_name":' | \
              sed -E 's/.*"([^"]+)".*/\1/')
  else
    echo "curl command not found. Please install it."
    exit 1
  fi
  
  if [[ -z "${version}" ]]; then
    echo "Failed to fetch latest version"
    exit 1
  fi
  
  echo "${version}"
}

# Download and extract binary
download_and_extract() {
  local version=$1
  local os=$2
  local arch=$3
  local archive_name="k8s-netperf_${os}_${version}_${arch}.tar.gz"
  local download_url="https://github.com/${REPO}/releases/download/${version}/${archive_name}"
  mkdir -p ${INSTALL_DIR}
  echo "Downloading k8s-netperf ${version} for ${os}/${arch}..."
  echo "URL: ${download_url}"
  curl -sL -f "${download_url}" | tar xz -C ${INSTALL_DIR} k8s-netperf
}

# Verify installation
verify_installation() {
  if command -v k8s-netperf &> /dev/null; then
    echo "k8s-netperf is now available in your PATH, installed at ${INSTALL_DIR}"
  else
    echo "k8s-netperf installed to ${INSTALL_DIR}, but not found in PATH"
    echo "You may need to add ${INSTALL_DIR} to your PATH"
  fi
}

# Main installation flow
main() {
  echo "Starting k8s-netperf 🔥 installation..."
  
  # Detect system   
  local os
  local arch
  os=$(detect_os)
  arch=$(uname -m | sed s/aarch64/arm64/)
  
  echo "Detected system: ${os}/${arch}"
  
  # Get latest version
  local version
  version=$(get_latest_version)
  echo "Latest version: ${version}"
  
  # Download and extract
  download_and_extract "${version}" "${os}" "${arch}"
  
  # Verify
  verify_installation
  
  echo "Get started with: k8s-netperf help"
}

# Run main function
main "$@"

