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

# Setup make
include makefiles/common.mk

# Settings for this subproject
# Entry file, containing func main
ENTRY           := ./cmd/batt
# All supported platforms for binary distribution
BIN_PLATFORMS   := darwin/arm64
# Binary basename (.exe will be automatically added when building for Windows)
BIN             := batt
# macOS specific settings
MACOSX_DEPLOYMENT_TARGET ?= 13.0

# Setup make variables
include makefiles/consts.mk

# Setup common targets
include makefiles/targets.mk

lint:
	bash build/lint.sh

app: build
	rm -rf bin/batt.app
	cp -r hack/boilerplates/batt.app bin
	export version=$$(echo "$(VERSION)" | sed 's/v//g') && sed -i '' "s|BATT_VERSION|$$version|g" bin/batt.app/Contents/Info.plist
	mkdir -p bin/batt.app/Contents/MacOS
	cp bin/batt bin/batt.app/Contents/MacOS/batt

dmg: app
	cd bin && create-dmg --volname batt --app-drop-link 360 130 --icon batt 130 130 --format ULFO "batt-$(VERSION).dmg" batt.app
