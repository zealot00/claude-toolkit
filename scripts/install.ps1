# claude-toolkit installer for Windows.
#
#   irm https://raw.githubusercontent.com/zealot00/claude-toolkit/main/scripts/install.ps1 | iex
#
# Environment overrides:
#   CLAUDE_TOOLKIT_VERSION   tag to install (default: latest release)
#   INSTALL_DIR              destination directory (default: $env:LOCALAPPDATA\bin)
#   CLAUDE_TOOLKIT_PLUGIN_DIR  if set, copy the bundled Claude Code plugin
#                            (.claude-plugin/ + commands/) into this directory
#
# Like install.sh, this downloads the release binary, verifies its SHA-256
# against the published checksums, and does NOT run `claude-toolkit init` for
# you. The Claude Code plugin is only copied when CLAUDE_TOOLKIT_PLUGIN_DIR
# is set explicitly.

$ErrorActionPreference = "Stop"

$Repo = "zealot00/claude-toolkit"
$Bin = "claude-toolkit.exe"

function Get-LatestTag {
    $json = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ "User-Agent" = "claude-toolkit-installer" }
    return $json.tag_name
}

function Get-Platform {
    $arch = switch ($env:PROCESSOR_ARCHITECTURE) {
        "AMD64" { "amd64" }
        "ARM64" { "arm64" }
        default { throw "unsupported architecture: $env:PROCESSOR_ARCHITECTURE (build from source with: go install github.com/$Repo@latest)" }
    }
    return "windows_$arch"
}

Write-Host ""
Write-Host "Installing $Bin"

$platform = Get-Platform
Write-Host "  platform: $platform"

$tag = if ($env:CLAUDE_TOOLKIT_VERSION) { $env:CLAUDE_TOOLKIT_VERSION } else { Get-LatestTag }
Write-Host "  version:  $tag"

$archive = "${Bin}_${platform}.zip"
$baseUrl = "https://github.com/$Repo/releases/download/$tag"
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("claude-toolkit-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
    $zip = Join-Path $tmp $archive
    Invoke-WebRequest -Uri "$baseUrl/$archive" -OutFile $zip
    $sums = Join-Path $tmp "checksums.txt"
    Invoke-WebRequest -Uri "$baseUrl/checksums.txt" -OutFile $sums

    $expected = (Get-Content $sums | Where-Object { $_ -match [regex]::Escape($archive) } | Select-Object -First 1).Split(" ")[0]
    if (-not $expected) { throw "no checksum published for $archive; refusing to install an unverified binary" }
    $actual = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLower()
    if ($expected.ToLower() -ne $actual) {
        throw "checksum mismatch for $archive`n  expected $expected`n  actual   $actual"
    }
    Write-Host "  checksum verified"

    Expand-Archive -Path $zip -DestinationPath $tmp -Force

    $dir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "bin" }
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
    Copy-Item -Path (Join-Path $tmp $Bin) -Destination (Join-Path $dir $Bin) -Force
    Write-Host "  installed to $dir\$Bin"

    # Opt-in Claude Code plugin install.
    if ($env:CLAUDE_TOOLKIT_PLUGIN_DIR) {
        $pluginDir = $env:CLAUDE_TOOLKIT_PLUGIN_DIR
        if (Test-Path (Join-Path $tmp ".claude-plugin")) {
            New-Item -ItemType Directory -Path $pluginDir -Force | Out-Null
            Copy-Item -Path (Join-Path $tmp ".claude-plugin") -Destination $pluginDir -Recurse -Force
            Copy-Item -Path (Join-Path $tmp "commands") -Destination $pluginDir -Recurse -Force
            Write-Host "  installed Claude Code plugin to $pluginDir"
        } else {
            Write-Host "  warning: CLAUDE_TOOLKIT_PLUGIN_DIR set but this archive has no .claude-plugin; plugin skipped"
        }
    }
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

Write-Host ""
Write-Host "Done. Next steps:"
Write-Host ""
Write-Host "  $Bin init      # register the hooks in ~/.claude/settings.json"
Write-Host "  $Bin doctor    # verify the installation"
Write-Host ""
Write-Host "External tools (git, formatters) are optional; each hook degrades to a silent no-op when a tool is missing."
Write-Host ""
