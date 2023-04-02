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

function print_download_help() {
  echo "You can install golangci-lint v${GOLANGCI_VERSION} by running:" 1>&2
  echo "  curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(pwd)/bin v${GOLANGCI_VERSION}" 1>&2
  echo "By default, it will be installed in ./bin/golangci-lint so that it won't interfere with other versions (if any)." 1>&2
}

if ! ${GOLANGCI} version >/dev/null 2>&1; then
  echo "You don't have golangci-lint installed." 2>&1
  print_download_help
  exit 1
fi

CURRENT_GOLANGCI_VERSION="$(${GOLANGCI} version 2>&1)"
CURRENT_GOLANGCI_VERSION="${CURRENT_GOLANGCI_VERSION#*version }"
CURRENT_GOLANGCI_VERSION="${CURRENT_GOLANGCI_VERSION% built*}"

function greaterver() {
  if [[ $1 == $2 ]]; then
    return 0
  fi
  local IFS=.
  local i ver1=($1) ver2=($2)
  # fill empty fields in ver1 with zeros
  for ((i = ${#ver1[@]}; i < ${#ver2[@]}; i++)); do
    ver1[i]=0
  done
  for ((i = 0; i < ${#ver1[@]}; i++)); do
    if [[ -z ${ver2[i]} ]]; then
      # fill empty fields in ver2 with zeros
      ver2[i]=0
    fi
    if ((10#${ver1[i]} > 10#${ver2[i]})); then
      return 0
    fi
    if ((10#${ver1[i]} < 10#${ver2[i]})); then
      return 2
    fi
  done
  return 0
}

if ! greaterver "${CURRENT_GOLANGCI_VERSION}" "${GOLANGCI_VERSION}"; then
  echo "golangci-lint version is too low." 1>&2
  echo "You have v${CURRENT_GOLANGCI_VERSION}, but we need at least v${GOLANGCI_VERSION}" 1>&2
  print_download_help
  exit 1
fi

if [ "${CURRENT_GOLANGCI_VERSION}" != "${GOLANGCI_VERSION}" ]; then
  echo "Warning: you have golangci-lint v${CURRENT_GOLANGCI_VERSION}, but we want v${GOLANGCI_VERSION}" 1>&2
  print_download_help
fi

echo "# Running golangci-lint v${CURRENT_GOLANGCI_VERSION}..."

${GOLANGCI} run "$@"
