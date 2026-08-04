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
  version "0.2.1"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/zealot00/claude-toolkit/releases/download/v0.2.1/claude-toolkit_darwin_arm64.tar.gz"
      sha256 "e183815ca564240b7b1653b3406eadaf7efd948b397ff491ebcda2bca9440719"
    else
      url "https://github.com/zealot00/claude-toolkit/releases/download/v0.2.1/claude-toolkit_darwin_amd64.tar.gz"
      sha256 "e9cfbdb6efbe1bd51746a75cb15f2961aec8546822febf73656f7b52e9f03aa5"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/zealot00/claude-toolkit/releases/download/v0.2.1/claude-toolkit_linux_arm64.tar.gz"
      sha256 "aed9831236d53af2c8ee65f63cc394e9b7e22b843ace8bb00cf47fd63937a9d6"
    else
      url "https://github.com/zealot00/claude-toolkit/releases/download/v0.2.1/claude-toolkit_linux_amd64.tar.gz"
      sha256 "ca0c22a7647b6852dc90e2afe13dec8093facd60efe1d92ffb182a077847ba17"
    end
  end

  def install
    bin.install "claude-toolkit"
  end

  test do
    system "#{bin}/claude-toolkit", "version"
  end
end
