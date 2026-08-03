---
description: Manage claude-toolkit hooks (list, enable, disable)
argument-hint: [list | enable <capability> | disable <capability> | enable-all | disable-all | help]
allowed-tools: Bash(claude-toolkit:*)
---

# Manage claude-toolkit hooks

The user has installed the **claude-toolkit** guardrail toolkit, which registers
its hooks into `~/.claude/settings.json`. This command manages which of its
capabilities are enabled or disabled.

## Capabilities

The toolkit ships the following hook capabilities (each may listen on one or
more events; each has its own `manage` switch):

| Capability | Event(s) | What it does |
|-----------|-----------|--------------|
| `guard` | PreToolUse | Blocks destructive / exfiltrating shell commands before they run |
| `loopguard` | PreToolUse + PostToolUse | Blocks a Bash command that keeps failing (3+ consecutive) |
| `format` | PostToolUse | Runs the project formatter after Claude writes a file |
| `heal` | PostToolUse | Points Claude at `claude-toolkit test <file>` when tests cover it |
| `enrich` | SessionStart, UserPromptSubmit | Injects git / toolchain / working-tree state |
| `notify` | PreToolUse + PostToolUse | Desktop notification for slow or failed calls (opt-in via `CLAUDE_TOOLKIT_NOTIFY`) |

## What to do

When this command is invoked, manage the toolkit's capabilities by running the
toolkit's own CLI. Do **not** edit `~/.claude/settings.json` by hand.

1. **Show current state.** Run:
   ```sh
   claude-toolkit manage list
   ```
   It prints every capability with its `enabled` / `disabled` state. If the
   toolkit is not installed (`init` was never run), it says so — report that
   to the user and tell them to run `claude-toolkit init` first.

2. **Apply the user's requested change.** Depending on what they asked:
   ```sh
   claude-toolkit manage enable <capability>     # e.g. enable guard
   claude-toolkit manage disable <capability>    # e.g. disable format
   ```
   You may enable or disable several capabilities in one call:
   ```sh
   claude-toolkit manage enable guard format
   claude-toolkit manage disable enrich
   ```

3. **Verify.** After any change, run `claude-toolkit doctor` and report the
   result.

4. **Tell the user** that hooks load at session start, so they should restart
   Claude Code (or start a new session) for the change to take effect.

## Rules

- Use the command-line forms above. Never rewrite the settings file yourself.
- If the user asks for something the toolkit does not support (an unknown
  capability name), report the valid names from the table above.
- If the user just asks "what is claude-toolkit" or "what hooks do I have",
  `claude-toolkit manage list` is the answer.

## Dependencies

This plugin adds none. It ships no binaries and no scripts; it only runs the
`claude-toolkit` binary the user already installed. The toolkit itself is a
zero-dependency static Go binary — its hooks may optionally call tools the
user already has (git, gofmt, ruff, black, prettier, ...) and silently skip
anything missing. See the project README's "Dependencies" section.
