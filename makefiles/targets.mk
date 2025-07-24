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

all: build

# ===== BUILD =====

build-dirs:
	mkdir -p "$(BIN_OUTPUT_DIR)"

build: # @HELP (default) build binary for current platform
build: build-dirs
	echo "# BUILD using local go sdk: $(LOCAL_GO_VERSION)"
	ARCH="$(ARCH)"                   \
	    OS="$(OS)"                   \
	    OUTPUT="$(OUTPUT)"           \
	    VERSION="$(VERSION)"         \
	    GIT_COMMIT="$(GIT_COMMIT)"   \
	    DEBUG="$(DEBUG)"             \
	    MACOSX_DEPLOYMENT_TARGET="$(MACOSX_DEPLOYMENT_TARGET)" \
	    bash build/build.sh $(ENTRY)
	echo "# BUILD linking $(DIST)/$(BIN_BASENAME) <==> $(OUTPUT) ..."
	ln -f "$(OUTPUT)" "$(DIST)/$(BIN_BASENAME)"

# INTERNAL: build-<os>_<arch> to build for a specific platform
build-%:
	$(MAKE) -f $(firstword $(MAKEFILE_LIST)) \
	    build                                \
	    --no-print-directory                 \
	    GOOS=$(firstword $(subst _, ,$*))    \
	    GOARCH=$(lastword $(subst _, ,$*))

all-build: # @HELP build binaries for all platforms
all-build: $(addprefix build-, $(subst /,_, $(BIN_PLATFORMS)))

# ===== PACKAGE =====

package: # @HELP build and package binary for current platform
package: build
	mkdir -p "$(PKG_OUTPUT_DIR)"
	ln -f LICENSE "$(DIST)/LICENSE"
	echo "# PACKAGE compressing $(OUTPUT) to $(PKG_OUTPUT)"
	$(RM) "$(PKG_OUTPUT)"
	tar czf "$(PKG_OUTPUT)" -C "$(DIST)" "$(BIN_BASENAME)" LICENSE;
	cd "$(PKG_OUTPUT_DIR)" && sha256sum "$(PKG_FULLNAME)" >> "$(CHECKSUM_FULLNAME)";
	echo "# PACKAGE checksum saved to $(PKG_OUTPUT_DIR)/$(CHECKSUM_FULLNAME)"
	echo "# PACKAGE linking $(DIST)/$(BIN)-packages-latest <==> $(PKG_OUTPUT_DIR)"
	ln -snf "$(BIN)-$(VERSION)/packages" "$(DIST)/$(BIN)-packages-latest"

# INTERNAL: package-<os>_<arch> to build and package for a specific platform
package-%:
	$(MAKE) -f $(firstword $(MAKEFILE_LIST)) \
	    package                              \
	    --no-print-directory                 \
	    GOOS=$(firstword $(subst _, ,$*))    \
	    GOARCH=$(lastword $(subst _, ,$*))

all-package: # @HELP build and package binaries for all platforms
all-package: $(addprefix package-, $(subst /,_, $(BIN_PLATFORMS)))
# overwrite previous checksums
	cd "$(PKG_OUTPUT_DIR)" && shopt -s nullglob && \
	    sha256sum *.{tar.gz,zip} > "$(CHECKSUM_FULLNAME)"
	echo "# PACKAGE all checksums saved to $(PKG_OUTPUT_DIR)/$(CHECKSUM_FULLNAME)"

# ===== MISC =====

clean: # @HELP clean built binaries
clean:
	$(RM) -r $(DIST)/$(BIN)*

all-clean: # @HELP clean built binaries, build cache, and helper tools
all-clean: clean
	test -d $(GOCACHE) && chmod -R u+w $(GOCACHE) || true
	$(RM) -r $(GOCACHE) $(DIST)

version: # @HELP output the version string
version:
	echo $(VERSION)

binaryname: # @HELP output current artifact binary name
binaryname:
	echo $(BIN_FULLNAME)

variables: # @HELP print makefile variables
variables:
	echo "BUILD:"
	echo "  build_output             $(OUTPUT)"
	echo "  app_version              $(VERSION)"
	echo "  git_commit               $(GIT_COMMIT)"
	echo "  debug_build_enabled      $(DEBUG)"
	echo "  local_go_sdk             $(LOCAL_GO_VERSION)"
	echo "PLATFORM:"
	echo "  current_os               $(OS)"
	echo "  current_arch             $(ARCH)"
	echo "  all_bin_os_arch          $(BIN_PLATFORMS)"

help: # @HELP print this message
help: variables
	echo "MAKE_TARGETS:"
	grep -E '^.*: *# *@HELP' $(MAKEFILE_LIST)    \
	    | sed -E 's_.*.mk:__g'                   \
	    | awk '                                  \
	        BEGIN {FS = ": *# *@HELP"};          \
	        { printf "  %-23s %s\n", $$1, $$2 }; \
	    '
