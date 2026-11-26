#!/bin/bash
# hack/check-i18n.sh — list keys in code missing a Vietnamese translation.
# Usage: bash hack/check-i18n.sh (run from repo root)
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# 1. Keys in Objective-C code: NSLocalizedString(@"...")
grep -rhoE 'NSLocalizedString\(@"([^"\\]|\\.)*"' pkg/gui/*.m \
  | sed -E 's/.*NSLocalizedString\(@"(([^"\\]|\\.)*)".*/\1/' \
  | sort -u > "$tmp/code_keys.txt"

# 2. Keys in Go code: tr("...")
grep -rhoE '\btr\("([^"\\]|\\.)*"\)' pkg/gui/*.go \
  | sed -E 's/.*tr\("(([^"\\]|\\.)*)"\)/\1/' \
  | sort -u >> "$tmp/code_keys.txt"

# 3. Tooltip constants in strings.go (wrapped with tr(const) at call sites).
awk '/^const \(/,/^\)/ { if ($0 ~ /= `/) { gsub(/.*= `|`.*/, ""); print } }' pkg/gui/strings.go \
  | sort -u >> "$tmp/code_keys.txt"
sort -u "$tmp/code_keys.txt" -o "$tmp/code_keys.txt"

# 4. Translated keys in Localizable.strings.
grep -oE '^"([^"\\]|\\.)*"' pkg/gui/vi.lproj/Localizable.strings \
  | sed -E 's/^"//; s/"$//' | sort -u > "$tmp/strings_keys.txt"

echo "=== Keys in code missing translation (will fall back to English) ==="
comm -23 "$tmp/code_keys.txt" "$tmp/strings_keys.txt" || true
echo
echo "=== Keys in Localizable.strings no longer used in code ==="
comm -13 "$tmp/code_keys.txt" "$tmp/strings_keys.txt" || true
