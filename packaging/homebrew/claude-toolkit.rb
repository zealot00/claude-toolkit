# Homebrew formula for claude-toolkit.
#
# Publish: create a tap repo (github.com/zealot00/homebrew-tap), put this
# file at Formula/claude-toolkit.rb, replace both SHA256 placeholders with
# the values from the release's checksums.txt, then:
#
#   brew tap zealot00/tap https://github.com/zealot00/homebrew-tap
#   brew install zealot00/tap/claude-toolkit
#
# The archive names must match .goreleaser.yaml's name_template
# (claude-toolkit_<os>_<arch>.tar.gz).

class ClaudeToolkit < Formula
  desc "Self-bootstrapping guardrail and productivity toolkit for Claude Code"
  homepage "https://github.com/zealot00/claude-toolkit"
  license "MIT"
  version "0.1.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/zealot00/claude-toolkit/releases/download/v0.1.0/claude-toolkit_darwin_arm64.tar.gz"
      sha256 "REPLACE_WITH_DARWIN_ARM64_SHA256"
    else
      url "https://github.com/zealot00/claude-toolkit/releases/download/v0.1.0/claude-toolkit_darwin_amd64.tar.gz"
      sha256 "REPLACE_WITH_DARWIN_AMD64_SHA256"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/zealot00/claude-toolkit/releases/download/v0.1.0/claude-toolkit_linux_arm64.tar.gz"
      sha256 "REPLACE_WITH_LINUX_ARM64_SHA256"
    else
      url "https://github.com/zealot00/claude-toolkit/releases/download/v0.1.0/claude-toolkit_linux_amd64.tar.gz"
      sha256 "REPLACE_WITH_LINUX_AMD64_SHA256"
    end
  end

  def install
    bin.install "claude-toolkit"
  end

  test do
    system "#{bin}/claude-toolkit", "version"
  end
end
