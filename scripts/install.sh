#!/usr/bin/env bash
#
# claude-toolkit installer.
#
#   curl -fsSL https://raw.githubusercontent.com/zealot00/claude-toolkit/main/scripts/install.sh | bash
#
# Environment overrides:
#   CLAUDE_TOOLKIT_VERSION   tag to install (default: latest release)
#   INSTALL_DIR              destination directory (default: first writable of
#                            /usr/local/bin, ~/.local/bin)
#   CLAUDE_TOOLKIT_PLUGIN_DIR  if set, copy the bundled Claude Code plugin
#                            (.claude-plugin/ + commands/) into this directory
#
# This script downloads a release binary and verifies its SHA-256 against the
# published checksums file. It deliberately does NOT run `claude-toolkit init`
# for you: init edits ~/.claude/settings.json, and a script piped from the
# internet should not silently modify your configuration. Run it yourself.
# For the same reason, the Claude Code plugin is only copied when
# CLAUDE_TOOLKIT_PLUGIN_DIR is set explicitly.
#
# External tools (git, formatters like gofmt/ruff/prettier/black, and later
# pytest) are OPTIONAL: the binary itself is a zero-dependency static Go
# build, and every hook degrades to a silent no-op when a tool is missing.
# Nothing here installs or requires them.

set -euo pipefail

REPO="zealot00/claude-toolkit"
BIN="claude-toolkit"

info()  { printf '  %s\n' "$*"; }
warn()  { printf '  warning: %s\n' "$*" >&2; }
die()   { printf '  error: %s\n' "$*" >&2; exit 1; }

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"
}

detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"

  case "$os" in
    darwin|linux) ;;
    *) die "unsupported operating system: $os (build from source with: go install github.com/$REPO@latest)" ;;
  esac

  case "$arch" in
    x86_64|amd64)  arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) die "unsupported architecture: $arch (build from source with: go install github.com/$REPO@latest)" ;;
  esac

  printf '%s_%s' "$os" "$arch"
}

latest_tag() {
  # Parse the tag out of the releases API. Prefer jq when it is available and
  # fall back to sed, so the script works on a bare machine.
  local json
  json="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest")" \
    || die "could not reach the GitHub releases API"

  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$json" | jq -r '.tag_name'
  else
    printf '%s' "$json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1
  fi
}

# choose_dir picks the first directory we can actually write to, so the common
# case needs no sudo and no prompt.
choose_dir() {
  if [ -n "${INSTALL_DIR:-}" ]; then
    printf '%s' "$INSTALL_DIR"
    return
  fi
  if [ -w /usr/local/bin ] 2>/dev/null; then
    printf '/usr/local/bin'
    return
  fi
  printf '%s/.local/bin' "$HOME"
}

verify_checksum() {
  local file="$1" sums="$2" name="$3" expected actual

  expected="$(awk -v n="$name" '$2 == n || $2 == "*"n {print $1}' "$sums" | head -n1)"
  [ -n "$expected" ] || die "no checksum published for $name; refusing to install an unverified binary"

  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$file" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$file" | awk '{print $1}')"
  else
    die "neither sha256sum nor shasum is available; cannot verify the download"
  fi

  [ "$expected" = "$actual" ] || die "checksum mismatch for $name
    expected $expected
    actual   $actual
  The download may be corrupt or tampered with. Aborting."

  info "checksum verified"
}

main() {
  need curl
  need tar

  printf '\nInstalling %s\n\n' "$BIN"

  local platform tag archive url dir tmp
  platform="$(detect_platform)"
  info "platform: $platform"

  tag="${CLAUDE_TOOLKIT_VERSION:-$(latest_tag)}"
  [ -n "$tag" ] || die "could not determine the latest release tag"
  info "version:  $tag"

  archive="${BIN}_${platform}.tar.gz"
  url="https://github.com/$REPO/releases/download/$tag/$archive"

  tmp="$(mktemp -d)"
  # shellcheck disable=SC2064  # expand tmp now, not at trap time
  trap "rm -rf '$tmp'" EXIT

  curl -fsSL "$url" -o "$tmp/$archive" \
    || die "download failed: $url"
  curl -fsSL "https://github.com/$REPO/releases/download/$tag/checksums.txt" -o "$tmp/checksums.txt" \
    || die "could not download checksums.txt"

  verify_checksum "$tmp/$archive" "$tmp/checksums.txt" "$archive"

  tar -xzf "$tmp/$archive" -C "$tmp" "$BIN" \
    || die "could not extract $BIN from the archive"

  # Opt-in Claude Code plugin install. The archive carries .claude-plugin/
  # and commands/; nothing is copied unless the user asked.
  if [ -n "${CLAUDE_TOOLKIT_PLUGIN_DIR:-}" ]; then
    if [ -d "$tmp/.claude-plugin" ]; then
      mkdir -p "$CLAUDE_TOOLKIT_PLUGIN_DIR" \
        || die "could not create $CLAUDE_TOOLKIT_PLUGIN_DIR"
      cp -R "$tmp/.claude-plugin" "$tmp/commands" "$CLAUDE_TOOLKIT_PLUGIN_DIR/" \
        || die "could not copy the plugin into $CLAUDE_TOOLKIT_PLUGIN_DIR"
      info "installed Claude Code plugin to $CLAUDE_TOOLKIT_PLUGIN_DIR"
      info "  enable it with /plugin in Claude Code, or: claude plugin install $CLAUDE_TOOLKIT_PLUGIN_DIR"
    else
      warn "CLAUDE_TOOLKIT_PLUGIN_DIR set but this archive has no .claude-plugin; plugin skipped"
    fi
  fi

  dir="$(choose_dir)"
  mkdir -p "$dir" || die "could not create $dir"

  if [ -w "$dir" ]; then
    install -m 0755 "$tmp/$BIN" "$dir/$BIN"
  elif command -v sudo >/dev/null 2>&1; then
    info "$dir is not writable; escalating with sudo"
    sudo install -m 0755 "$tmp/$BIN" "$dir/$BIN"
  else
    die "$dir is not writable and sudo is unavailable; set INSTALL_DIR to somewhere you own"
  fi

  info "installed to $dir/$BIN"

  case ":$PATH:" in
    *":$dir:"*) ;;
    *)
      warn "$dir is not on your PATH"
      info "add this to your shell profile:"
      info "    export PATH=\"$dir:\$PATH\""
      ;;
  esac

  cat <<EOF

Done. Next steps:

  $BIN init      # register the hooks in ~/.claude/settings.json
  $BIN doctor    # verify the installation

init backs up your settings file and merges into it; your existing hooks,
credentials and model settings are left untouched.

The Claude Code plugin (the /toolkit command) is bundled in the archive under
.claude-plugin/ and commands/. Enable it by pointing /plugin at the unpacked
archive or the repo, or re-run this installer with CLAUDE_TOOLKIT_PLUGIN_DIR
set to a directory you want it copied into.

External tools (git, formatters like gofmt/ruff/prettier) are optional: the
binary itself is a zero-dependency static Go build, and each hook degrades to
a silent no-op when a tool is missing. Run `$BIN doctor` to see which are
present.

EOF
}

main "$@"
