#!/bin/sh

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

set -x
set -o errexit
set -o nounset

if [ -z "${OS:-}" ]; then
  echo "OS must be set"
  exit 1
fi

if [ -z "${ARCH:-}" ]; then
  echo "ARCH must be set"
  exit 1
fi

if [ -z "${VERSION:-}" ]; then
  echo "VERSION is not set, defaulting to 'UNKNOWN'"
fi

if [ -z "${GIT_COMMIT:-}" ]; then
  echo "GIT_COMMIT is not set, defaulting to 'UNKNOWN'"
fi

if [ -z "${OUTPUT:-}" ]; then
  echo "OUTPUT must be set"
  exit 1
fi

# this project must use cgo
export CGO_ENABLED=1
export GOARCH="${ARCH}"
export GOOS="${OS}"
export GO111MODULE=on
export GOFLAGS="${GOFLAGS:-} -mod=mod "

printf "# BUILD output: %s\ttarget: %s/%s\tversion: %s\ttags: %s\n" \
  "${OUTPUT}" "${OS}" "${ARCH}" "${VERSION}" "${GOTAGS:- }"

printf "# BUILD building for "

if [ "${DEBUG:-}" != "1" ]; then
  # release build
  # trim paths, disable symbols and DWARF.
  goasmflags="all=-trimpath=$(pwd)"
  gogcflags="all=-trimpath=$(pwd)"
  goldflags="-s -w"

  printf "release...\n"
else
  # debug build
  # disable optimizations and inlining
  gogcflags="all=-N -l"
  goasmflags=""
  goldflags=""

  printf "debug...\n"
fi

# Set some version info.
always_ldflags=""
if [ -n "${VERSION:-}" ]; then
  always_ldflags="${always_ldflags} -X $(go list -m)/pkg/version.Version=${VERSION}"
fi
if [ -n "${GIT_COMMIT:-}" ]; then
  always_ldflags="${always_ldflags} -X $(go list -m)/pkg/version.GitCommit=${GIT_COMMIT}"
fi

export CGO_CFLAGS="-O2"
export CGO_LDFLAGS="-O2"

ls -alh
ls -lah cmd

go build \
  -gcflags="${gogcflags}" \
  -tags="${GOTAGS:-}" \
  -asmflags="${goasmflags}" \
  -ldflags="${always_ldflags} ${goldflags}" \
  -o "${OUTPUT}" \
  "$@"
