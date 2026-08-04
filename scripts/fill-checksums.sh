#!/usr/bin/env bash
#
# Usage: scripts/fill-checksums.sh <checksums.txt> [version]
#
# Reads a checksums.txt (as produced by GoReleaser's release or `make dist`)
# and replaces the REPLACE_WITH_*_SHA256 placeholders in packaging/ with the
# real per-platform hashes. When a version argument is given, the version
# strings in both packaging files are rewritten too.
#
# Idempotent: re-running with the same checksums.txt produces the same files.
# A platform missing from the checksums file keeps its placeholder and is
# reported (e.g. `make dist` builds every platform, so all six are filled;
# a partial checksums file leaves the rest untouched).
#
# Portable: avoids associative arrays so it runs on macOS's stock bash 3.2
# as well as the bash 5 on GitHub's ubuntu runners.

set -euo pipefail

SUMS="${1:?usage: fill-checksums.sh <checksums.txt> [version]}"
VERSION="${2:-}"

# checksums.txt entry (file basename) -> packaging placeholder key.
key_for() {
  case "$1" in
    claude-toolkit_darwin_arm64.tar.gz)  echo DARWIN_ARM64 ;;
    claude-toolkit_darwin_amd64.tar.gz)  echo DARWIN_AMD64 ;;
    claude-toolkit_linux_arm64.tar.gz)   echo LINUX_ARM64 ;;
    claude-toolkit_linux_amd64.tar.gz)   echo LINUX_AMD64 ;;
    claude-toolkit_windows_amd64.zip)    echo WINDOWS_AMD64 ;;
    claude-toolkit_windows_arm64.zip)    echo WINDOWS_ARM64 ;;
  esac
}

filled=0
while read -r sum file; do
  file="${file#\*}" # tolerate a leading '*' (BSD shasum style)
  key="$(key_for "$file")"
  [ -n "$key" ] || continue
  sed -i.bak "s/REPLACE_WITH_${key}_SHA256/${sum}/g" \
    packaging/homebrew/claude-toolkit.rb packaging/scoop/claude-toolkit.json
  rm -f packaging/homebrew/claude-toolkit.rb.bak packaging/scoop/claude-toolkit.json.bak
  filled=$((filled + 1))
done < "$SUMS"

if [ "$filled" -eq 0 ]; then
  echo "error: no claude-toolkit entries found in $SUMS" >&2
  exit 1
fi

if [ -n "$VERSION" ]; then
  v="${VERSION#v}"
  sed -i.bak "s/version \"[0-9][^\"]*\"/version \"$v\"/; s|/v[0-9][^/]*/|/v$v/|g" packaging/homebrew/claude-toolkit.rb
  sed -i.bak "s/\"version\": \"[0-9][^\"]*\"/\"version\": \"$v\"/; s|/v[0-9][^/]*/|/v$v/|g" packaging/scoop/claude-toolkit.json
  rm -f packaging/homebrew/claude-toolkit.rb.bak packaging/scoop/claude-toolkit.json.bak
fi

# Warn about placeholders that could not be filled.
leftover="$(grep -o 'REPLACE_WITH_[A-Z0-9_]*_SHA256' packaging/homebrew/claude-toolkit.rb packaging/scoop/claude-toolkit.json | sort -u || true)"
if [ -n "$leftover" ]; then
  echo "warning: checksums missing for: $(echo "$leftover" | tr '\n' ' ')" >&2
fi

echo "filled $filled checksum(s) in packaging/ (version: ${VERSION:-unchanged})"
