#!/usr/bin/env bash

# Copyright 2022 Charlie Chiang
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

GOLANGCI_VERSION="1.52.2"

GOLANGCI="${GOLANGCI:-golangci-lint}"

if [ -f "bin/golangci-lint" ]; then
  GOLANGCI="bin/golangci-lint"
fi

function print_install_help() {
  echo "Automatic installation failed, you can install golangci-lint v${GOLANGCI_VERSION} manually by running:"
  echo "  curl -sSfL \"https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh\" | sh -s -- -b \"$(pwd)/bin\" v${GOLANGCI_VERSION}"
  echo "It will be installed to \"$(pwd)/bin/golangci-lint\" so that it won't interfere with existing versions (if any)."
  exit 1
}

function install_golangci() {
  echo "Installing golangci-lint v${GOLANGCI_VERSION} ..."
  echo "It will be installed to \"$(pwd)/bin/golangci-lint\" so that it won't interfere with existing versions (if any)."
  curl -sSfL "https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh" |
    sh -s -- -b "$(pwd)/bin" v${GOLANGCI_VERSION} || print_install_help
}

if ! ${GOLANGCI} version >/dev/null 2>&1; then
  echo "You don't have golangci-lint installed." 2>&1
  install_golangci
  $0 "$@"
  exit
fi

CURRENT_GOLANGCI_VERSION="$(${GOLANGCI} version 2>&1)"
CURRENT_GOLANGCI_VERSION="${CURRENT_GOLANGCI_VERSION#*version }"
CURRENT_GOLANGCI_VERSION="${CURRENT_GOLANGCI_VERSION% built*}"

if [ "${CURRENT_GOLANGCI_VERSION}" != "${GOLANGCI_VERSION}" ]; then
  echo "You have golangci-lint v${CURRENT_GOLANGCI_VERSION} installed, but we want v${GOLANGCI_VERSION}" 1>&2
  install_golangci
  $0 "$@"
  exit
fi

echo "# Running golangci-lint v${CURRENT_GOLANGCI_VERSION}..."

${GOLANGCI} run "$@"
