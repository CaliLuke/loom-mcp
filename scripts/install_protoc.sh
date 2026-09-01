#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $(basename "$0") <version> <install-dir>" >&2
  exit 1
fi

version="$1"
install_dir="$2"
if [[ -z "${version}" || -z "${install_dir}" || "${install_dir}" == "/" ]]; then
  echo "version and a non-root install directory are required" >&2
  exit 1
fi

case "$(uname -s)" in
  Darwin)
    platform="osx"
    ;;
  Linux)
    platform="linux"
    ;;
  *)
    echo "unsupported operating system: $(uname -s)" >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  x86_64|amd64)
    architecture="x86_64"
    ;;
  arm64|aarch64)
    architecture="aarch_64"
    ;;
  *)
    echo "unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

archive="protoc-${version}-${platform}-${architecture}.zip"
url="https://github.com/protocolbuffers/protobuf/releases/download/v${version}/${archive}"
temp_dir="$(mktemp -d)"
trap 'rm -rf "${temp_dir}"' EXIT INT TERM

echo "Installing protoc ${version} from ${url}"
curl --fail --location --silent --show-error --output "${temp_dir}/${archive}" "${url}"
mkdir -p "${install_dir}"
unzip -oq "${temp_dir}/${archive}" -d "${install_dir}"
"${install_dir}/bin/protoc" --version
