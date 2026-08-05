---
description: Manage claude-toolkit hooks and provider profiles
argument-hint: [list | enable <capability> | disable <capability> | enable-all | disable-all | model [list|use|add|rm] | help]
allowed-tools: Bash(claude-toolkit:*)
---

# /claude-toolkit:toolkit — Manage claude-toolkit hooks

The user has installed the **claude-toolkit** guardrail toolkit. Its hooks may
be registered either by the plugin itself (`hooks/hooks.json`) or by
`claude-toolkit init` into `~/.claude/settings.json`; either way, this command
manages which capabilities are enabled or disabled.

> Namespacing: the slash command is `/claude-toolkit:toolkit`. Bare `/toolkit`
> works only while no other plugin claims that name, so always use the
> qualified form in scripts and documentation.
> `allowed-tools: Bash(claude-toolkit:*)` means the Claude Code calls starting
> with `claude-toolkit` skip the permission prompt for THIS invocation. It is
> not a sandbox: Claude can still call any other tool it needs.

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
| `envfix` | PreToolUse | Rewrites bare interpreter calls to the project's `.venv` when one exists |
| `autoproxy` | SessionStart, SessionEnd | In `bypassPermissions` mode, forks the local 429 retry proxy when a network proxy env (`HTTPS_PROXY` / `HTTP_PROXY` / `ALL_PROXY`) is set; cleans up on session end |
| `retryguard` | Stop | When autoproxy is not running, scans the transcript tail for `429` / `rate_limit_error` markers and blocks with a retry nudge |
| `hud` | (statusLine) | Not a hook — `init` registers `claude-toolkit hud` as Claude Code's `statusLine.command` so the chat area shows live token / proxy / retry / mode state |

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

   State lives in the toolkit's private directory under
   `${CLAUDE_PLUGIN_DATA}/state/capabilities.json` (or
   `~/.claude/plugins/data/claude-toolkit/state/capabilities.json` for
   non-plugin installs). The plugin lifecycle (`/plugin disable`,
   `/plugin uninstall`) removes the directory automatically.
   You may enable or disable several capabilities in one call:
   ```sh
   claude-toolkit manage enable guard format
   claude-toolkit manage disable enrich
   ```

3. **Verify.** After any change, run `claude-toolkit doctor` and report the
   result.

4. **Tell the user** that hooks load at session start, so they should restart
   Claude Code (or start a new session) for the change to take effect.

## Model profiles (provider switching)

The user may also ask to switch AI providers/models. The toolkit stores named
provider profiles (`base URL` + `auth token` + `model`) in
`<toolkit root>/profiles.json` and switches by rewriting the settings `env`
block — never edit settings.json by hand.

1. **Show profiles.** Run `claude-toolkit model list`. It marks the active
   profile with `* active`; if the env points at a provider with no stored
   profile it says so.
2. **Switch.** Run `claude-toolkit model use <name>` (add `--scope=project`
   to bind the switch to the current project only). The command writes the
   env atomically and prints the restart reminder.
3. **Add / remove.** `claude-toolkit model add --name <n> --base-url <u>
   --token <t> --model <m>` and `claude-toolkit model rm <name>`.
4. Report the new active provider to the user and remind them to restart
   Claude Code (`claude --resume` keeps the session).

> Always use the command-line forms above. **Never run bare `claude-toolkit
> model` with no arguments from inside a Claude Code session** — that starts
> the interactive picker and blocks waiting for a terminal keypress. When
> the user invokes `/toolkit model` with no subcommand, treat it as `model
> list`. The interactive TUI is for humans at a real terminal only.

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
