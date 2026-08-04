# claude-toolkit

A self-bootstrapping guardrail and productivity toolkit for [Claude Code](https://code.claude.com), shipped as a single dependency-free binary.

Install it, run `init`, and it registers its own hooks into `~/.claude/settings.json`. No scripts to copy, no paths to hard-code, no manual JSON editing. Moving to a new machine is three commands.

```sh
go install github.com/zealot00/claude-toolkit@latest
claude-toolkit init
claude-toolkit doctor
```

---

## Contents

- [Install](#install)
- [The commands](#the-commands)
- [What the hooks do](#what-the-hooks-do)
- [How self-installation works](#how-self-installation-works)
- [Configuration](#configuration)
- [Dependencies](#dependencies)
- [Architecture](#architecture)
- [Development](#development)
- [Releasing](#releasing)

---

## Install

### With Go

```sh
go install github.com/zealot00/claude-toolkit@latest
```

Binaries land in `$GOBIN`, or `$GOPATH/bin`, or `~/go/bin`. That directory must be on your `PATH` — see [the PATH caveat](#the-path-caveat).

### Without Go

```sh
curl -fsSL https://raw.githubusercontent.com/zealot00/claude-toolkit/main/scripts/install.sh | bash
```

The script detects your OS and architecture, downloads the matching release archive, **verifies its SHA-256 against the published `checksums.txt`**, and installs to `/usr/local/bin` or `~/.local/bin`.

It does *not* run `init` for you. `init` modifies your Claude Code configuration, and a script piped from the internet has no business doing that silently.

> If piping a remote script into `bash` makes you uneasy — good instinct, and this toolkit's own guard hook blocks Claude from doing exactly that. Download and read it first:
>
> ```sh
> curl -fsSLO https://raw.githubusercontent.com/zealot00/claude-toolkit/main/scripts/install.sh
> less install.sh && bash install.sh
> ```

Override the version or destination with `CLAUDE_TOOLKIT_VERSION` and `INSTALL_DIR`.

### From source

```sh
git clone https://github.com/zealot00/claude-toolkit
cd claude-toolkit
make install
```

---

## The commands

### 1. `claude-toolkit init`

Merges the toolkit's hooks into `~/.claude/settings.json`.

```
$ claude-toolkit init

Installing claude-toolkit hooks in /Users/you/.claude/settings.json

  + PostToolUse
  + PreToolUse
  + SessionStart

Backed up previous settings to /Users/you/.claude/settings.json.bak.20260803-142201
Wrote /Users/you/.claude/settings.json

Hooks are loaded when a session starts, so restart Claude Code for this to
take effect. Then run `claude-toolkit doctor` to verify.
```

| Flag | Effect |
| --- | --- |
| `--dry-run` | Print the resulting `hooks` block without writing anything |
| `--uninstall` | Remove the toolkit's entries, leaving everything else alone |
| `--scope user\|project\|local` | Write hooks into `~/.claude/settings.json` / project settings (default `user`) |
| `--scope skills-user\|skills-project` | Install the auto-loading plugin directory instead: `~/.claude/skills/claude-toolkit/` (global) or `<dir>/.claude/skills/claude-toolkit/` (per-project, needs workspace trust) |
| `--project-dir <path>` | Project root for `--scope=project\|local` |
| `--abs-path` | Write this binary's absolute path instead of resolving `claude-toolkit` on `PATH` |
| `--force` | Install even if the binary is not resolvable on `PATH` |

**Hooks load at session start.** Restart Claude Code after running `init`.

### 2. `claude-toolkit doctor`

Diagnoses the installation and self-tests every hook against synthetic events.

```
$ claude-toolkit doctor
claude-toolkit v0.1.0  (darwin/arm64, go1.25.11)

  ✓ binary location             /Users/you/go/bin/claude-toolkit
  ✓ binary on PATH              /Users/you/go/bin/claude-toolkit
  ✓ settings file               /Users/you/.claude/settings.json
  ! settings permissions        0644 -- readable by other users
      This file contains a credential. Consider: chmod 600 /Users/you/.claude/settings.json
  ✓ hook registration           PostToolUse, PreToolUse, SessionStart
  ✓ configured command runs     claude-toolkit v0.1.0 (commit a1b2c3d, built 2026-08-03T14:22:01Z)
  ✓ hook self-test              6/6 cases pass
  ✓ git                         available; current directory is a repository
  ✓ formatters                  gofmt, prettier, ruff

All checks passed with 1 warning(s).
```

It exits non-zero if any check fails, so it works in CI or a dotfiles bootstrap script.

What it actually verifies:

- The binary is on `PATH`, and it is the *same* binary you just ran.
- The settings file exists, parses as JSON, and is not world-readable while holding a credential.
- Every event this build registers is present in the config — catching the case where an upgrade added a hook your config predates.
- The configured command is genuinely executable, by running it.
- The guard still blocks what it should and still ignores what it should, by dispatching real events through the real handlers.

### 3. `claude-toolkit run`

The hook runtime. Claude Code invokes this; you generally do not. It reads one event JSON document on stdin and writes at most one response document on stdout.

You can drive it by hand to test a rule:

```sh
echo '{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}' \
  | claude-toolkit run
```

```json
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Blocked by claude-toolkit guard:\n  - [rm-rf-root] recursive force-delete of \"/\" removes data outside the working tree and cannot be undone"}}
```

### 4. `claude-toolkit manage`

Lists, enables and disables the toolkit's hook capabilities — the interface the plugin's `/toolkit` command drives. State is **global** and stored in the toolkit's private config (`~/.claude-toolkit/state/capabilities.json`), separate from Claude Code's settings.json, so `manage` works identically for both the plugin's `hooks/hooks.json` and the `init` registration path — and survives plugin uninstall/reinstall.

```
$ claude-toolkit manage list
claude-toolkit hooks (global state: /Users/you/.claude-toolkit/state/capabilities.json)

capability  event                           matcher                           state
enrich      SessionStart, UserPromptSubmit  *                                 enabled
format      PostToolUse                     ^(Write|Edit|NotebookEdit)$       enabled
guard       PreToolUse                      ^(Bash|Write|Edit|NotebookEdit)$  enabled
heal        PostToolUse                     ^(Write|Edit|NotebookEdit)$       enabled
loopguard   PreToolUse, PostToolUse         ^(Bash)$                          enabled
notify      PreToolUse, PostToolUse         *                                 enabled
envfix      PreToolUse                      ^(Bash)$                          enabled

7 capability(ies), 7 enabled, 0 disabled.
```

```sh
claude-toolkit manage enable guard        # turn one on
claude-toolkit manage disable format      # turn one off
claude-toolkit manage enable-all
claude-toolkit manage disable-all
```

Run bare (`claude-toolkit manage`) for an interactive toggle UI in the terminal. Every write backs up your settings file and merges, exactly like `init` — and like `init`, the change only takes effect after you restart Claude Code. `--dry-run` shows the change without writing.

### 5. Other commands

| Command | What it does |
|---|---|
| `test <file.go\|file.py>` | Runs the incremental tests covering a file (`go test` / `pytest`), output truncated to 35 lines |
| `ast <file>` | Prints a compressed JSON structural summary (signatures, no bodies) |
| `rules` | Lists every built-in rule and its verdict |
| `proxy` | Optional local API proxy with 429 auto-retry (opt-in, not auto-wired) |
| `upgrade [--check-only]` | Checks GitHub Releases for a newer version |
| `uninstall [--purge-config]` | Removes the toolkit's hooks (alias of `init --uninstall`) |
| `log [--follow] [--event=…]` | Tails the debug log written under `CLAUDE_TOOLKIT_DEBUG` |

---

## The Claude Code plugin

A companion plugin lets you manage the toolkit without leaving Claude Code. Install it, and a `/claude-toolkit:toolkit` slash command appears; Claude then runs `claude-toolkit manage list/enable/disable` for you and shows the state right in the session. Bare `/toolkit` works only while no other plugin claims the name.

**Install the plugin** (choose one path):

1. **`claude-toolkit init --scope=skills-user`** (recommended): copies the plugin into `~/.claude/skills/claude-toolkit/`, which Claude Code auto-loads for every project. The plugin ships `.claude-plugin/plugin.json`, `commands/toolkit.md` and `hooks/hooks.json`; its own hooks are the registration.
2. **`claude-toolkit init --scope=skills-project`**: same as above, but into `<dir>/.claude/skills/claude-toolkit/` for one project (loads after the workspace is trusted).
3. **`claude-toolkit init`** (default): writes hooks into `~/.claude/settings.json` and copies the plugin (without `hooks/hooks.json`) to `~/.claude/plugins/claude-toolkit/` so the `/toolkit` slash command is available.
4. **`/plugin` in Claude Code**: point it at this repository's path (or an unpacked release archive).

There is exactly one hook registration per capability — either `settings.json` (default `init`) or the plugin's own `hooks/hooks.json` (`--scope=skills-*`), never both. The `settings.json` path installs the plugin without hooks; the skills-* path uses the plugin's hooks. Restart the session, then:

```
/claude-toolkit:toolkit
```

The fully qualified form `/claude-toolkit:toolkit` is the stable contract — Claude Code namespaced plugin commands as `<plugin>:<command>`. The bare `/toolkit` works only while no other plugin claims the name, so always use the qualified form in scripts and documentation.

Ask for what you want in plain language — "show the hooks", "disable format", "re-enable guard" — and Claude will call `claude-toolkit manage` and report back. `claude-toolkit doctor` (also run by the command) verifies the result.

### `/toolkit` capabilities

| Capability | Event | What it does |
|---|---|---|
| `guard` | PreToolUse | blocks destructive / exfiltrating shell commands (covers Bash, Write, Edit, MultiEdit, NotebookEdit) |
| `loopguard` | Pre+PostToolUse | blocks a Bash command that keeps failing (3+ consecutive) |
| `format` | PostToolUse | runs the project formatter after Claude writes a file |
| `heal` | PostToolUse | points Claude at `claude-toolkit test` when tests cover the file |
| `enrich` | SessionStart, UserPromptSubmit | injects git / toolchain / working-tree state |
| `notify` | Pre+PostToolUse | desktop notification for slow or failed calls (opt-in) |

---

## What the hooks do

### PreToolUse — guard

Blocks irreversible and exfiltrating commands before Claude runs them — over Bash, Write, Edit, **MultiEdit** (each edit in the batch is checked) and NotebookEdit.

| Rule | Verdict | Catches |
| --- | --- | --- |
| `rm-rf-root` | deny | Recursive force-delete of `/`, `~`, `$HOME`, `/usr`, `/etc`, … |
| `pipe-to-shell` | deny | `curl … \| sh`, `wget … \| bash`, `curl … \| python` |
| `secret-exfil` | deny | A credential file in a pipeline that ends at `curl`, `nc`, `ssh` |
| `dd-to-device` | deny | `dd of=/dev/disk0` |
| `mkfs` | deny | Any `mkfs*` invocation |
| `block-device-write` | deny | `> /dev/disk0` |
| `fork-bomb` | deny | `:(){ :\|:& };:` |
| `write-to-secret` | deny | Writing `~/.ssh/id_*`, `~/.aws/credentials`, `.netrc` |
| `git-reset-hard` | deny | `git reset --hard` discards uncommitted work |
| `high-entropy-secret` | deny | A credential-shaped token (AKIA/ghp_/sk-/PEM) in Write/Edit content |
| `protected-branch` | ask | `git commit`/`push` on `main`/`master`/`release/*` (opt out: `.claude-toolkit-allow`) |
| `log-dump` | ask | `cat` of log paths, unbounded `journalctl`, `kubectl logs` — suggests a bounded view |
| `git-force-push` | ask | `git push --force` (but not `--force-with-lease`) |
| `power-state` | ask | `shutdown`, `reboot`, `halt` |
| `write-to-secret` | ask | Writing `.env`, `.npmrc`, `*.pem`, `~/.claude/settings.json` |

The rules are deliberately narrow. **`rm -rf ./build`, `rm -rf node_modules`, `git push --force-with-lease` and `curl -o file.sh` all pass through untouched** — a guard that cries wolf gets uninstalled. Roughly half of the [test suite](internal/hooks/guard_test.go) exists to pin the *absence* of false positives.

The command parser is quote-aware and descends into command substitution, so `echo $(rm -rf ~)` is caught while `echo "never run rm -rf /"` is not.

### SessionStart — repository context

Injects the repo's current state at session start: branch, divergence from upstream, uncommitted files, and the last five commits. Without it Claude opens every session blind and burns its first few tool calls rediscovering where it is.

### PostToolUse — formatter

Runs the project's formatter after Claude writes a file — `gofmt`, `prettier`, `ruff`/`black`, `rustfmt`, `shfmt`, picked by extension and skipped when the tool is not installed. `prettier` is resolved from the nearest `node_modules/.bin` first, so the project's pinned version wins. See [Dependencies](#dependencies) for the full list of optional tools and what happens when one is missing.

The point is not tidiness. When a formatter rewrites a file, Claude's in-context copy goes stale and its next `Edit` fails on a string that no longer matches. So the hook speaks up **only when the file actually changed**, telling Claude to re-read. If the formatter fails outright, that usually means Claude just wrote code that does not parse — which it is also told.

### PostToolUse — heal (incremental tests)

After Claude writes a `.go`/`.py` file that has tests (a sibling `*_test.go` or a `test_*.py`), the hook tells Claude to run them with `claude-toolkit test <file>`. The hook only *detects* — it never runs the tests itself, because a hook's ~60s timeout would kill a real test run. The standalone command maps the file to `go test -count=1 [-run …]` or `pytest … --maxfail=1 --tb=short` and truncates output to 35 lines.

### PreToolUse+PostToolUse — loopguard

A Bash command that fails three times in a row (exit code ≠ 0, recorded on PostToolUse) is blocked on the next attempt with a diagnostic. Any success resets the count. The ledger lives in `~/.claude-toolkit/state/`, not `/tmp`.

### PreToolUse+PostToolUse — notify (opt-in)

Set `CLAUDE_TOOLKIT_NOTIFY=<seconds>` to get an OS desktop notification (macOS `osascript` / Linux `notify-send` / Windows `msg *`) plus a terminal bell when a tool call runs longer than that or fails. Off by default. Note: on Windows Server Core / headless RDP (session-0 isolation) `msg *` is silent and only the terminal bell fires.

---

## How self-installation works

`init` treats your settings file as user property. Three rules follow:

**1. Merge per event; never replace the `hooks` object.**

The naive implementation is `settings["hooks"] = myHooks`. That preserves your `env` and `model` but silently deletes every hook you configured by hand. This toolkit walks into each event's array, removes only entries it owns, and appends its own — leaving foreign entries exactly where they were.

Ownership is decided by matching the *invocation* (`…/claude-toolkit run …`), not an exact string. So re-running `init` after moving the binary replaces the stale entry instead of stacking a second one beside it. `init` is idempotent: running it twice produces byte-identical files.

**2. Never write a file that failed to parse.**

If `settings.json` has a syntax error, `init` reports it and stops. A file we cannot read is a file we must not clobber.

**3. Never write in place.**

The original is copied to `settings.json.bak.<timestamp>`, the new content goes to a temp file in the same directory, that file is `fsync`ed, and only then is it `rename`d over the target. A process killed mid-write cannot truncate a file holding your `ANTHROPIC_AUTH_TOKEN`. File mode is preserved; a file the toolkit *creates* is `0600`, because it will come to hold a credential.

### The PATH caveat

The generated config invokes the binary by name, which is what makes it portable across machines:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash|Write|Edit|NotebookEdit",
        "hooks": [
          { "type": "command", "command": "claude-toolkit run --event=pre", "timeout": 10 }
        ]
      }
    ]
  }
}
```

That only works if Claude Code's environment can find it. **On macOS, a GUI-launched application does not inherit the `PATH` from your shell profile**, so `~/go/bin` is frequently missing and every hook would silently never fire.

So `init` refuses to write a config it cannot verify, and tells you the three ways out: fix `PATH`, use `--abs-path` to pin this binary's location, or `--force` past it. `doctor` re-checks the same thing later.

`--event=pre` is presentational — it makes the config readable. The authoritative event always comes from the stdin payload, so a miswired matcher cannot change what a hook decides.

### Matchers are narrow on purpose

Each event's matcher is derived from the routes the binary actually registers, so the config cannot drift from the code — and Claude Code never spawns the process for a tool no route would have handled. `PreToolUse` matches `Bash|Write|Edit|NotebookEdit`, not `.*`.

---

## Configuration

| Variable | Effect |
| --- | --- |
| `CLAUDE_TOOLKIT_DEBUG` | When set, `run` appends diagnostics to `~/.claude/claude-toolkit.log`. Hook stdout is reserved for the JSON response, so this is where errors go. |

To disable the toolkit without uninstalling, run `claude-toolkit init --uninstall` and restart Claude Code.

---

## Dependencies

**The binary itself has zero dependencies.** It is a single static Go
(stdlib-only) executable; nothing is bundled and nothing is required to run
it. The hooks shell out only to tools that are *already installed on your
machine*, probed via `PATH` at hook time, and each hook **degrades to a
silent no-op** when a tool is missing — no errors, no warnings in your
session.

### Optional external tools

| Tool | Used by | Purpose | When missing |
|---|---|---|---|
| `git` | `enrich` | branch / working-tree / recent-commits context at SessionStart | the context hook produces nothing |
| `gofmt` | `format` (`.go`) | format Go source | `.go` files are not formatted |
| `rustfmt` | `format` (`.rs`) | format Rust source | `.rs` files are not formatted |
| `shfmt` | `format` (`.sh`) | format shell scripts | shell scripts are not formatted |
| `prettier` | `format` (js/ts/json/css/html/md/…) | format web & config files (project-local `node_modules/.bin` first) | those extensions are not formatted |
| `ruff` | `format` (`.py`) | format and fix Python | falls back to `black` |
| `black` | `format` (`.py`) | format Python | `.py` files are not formatted |
| `pytest` | `test` (`.py`) | run incremental Python tests | `claude-toolkit test` reports the gap |

`doctor` reports which of these are present in the `formatters` / `git`
checks, so you can see the gap before it matters.

> **For plugin-marketplace and community reviewers:** the Claude Code plugin
> ships only the `/toolkit` management command and adds **no dependencies of
> its own** — no bundled binaries, no install scripts, no runtime
> requirements. It merely drives the `claude-toolkit` binary you already
> installed. Every external tool in the table above is optional and used only
> when the hook for that file type actually runs.

---

## Architecture

```
claude-toolkit/
├── .claude-plugin/
│   └── plugin.json         Claude Code plugin manifest
├── commands/
│   └── toolkit.md          /toolkit slash command (drives manage)
├── main.go                  Entry point; delegates to cmd
├── cmd/
│   ├── root.go              Subcommand routing (stdlib flag, zero deps)
│   ├── run.go               Hook runtime: stdin JSON -> dispatch -> stdout JSON
│   ├── init.go              Self-installation; derives config from routes
│   ├── manage.go            Capability toggle UI + subcommands for /toolkit
│   ├── test.go              Incremental test runner (go test / pytest)
│   ├── ast.go               Structural summary of a .go/.py file
│   ├── rules.go             Lists the built-in guard rules
│   ├── proxy.go             Optional local API proxy (429 auto-retry)
│   ├── upgrade.go           Version check against GitHub Releases
│   ├── uninstall.go         Symmetric removal of the toolkit's hooks
│   ├── log.go               Tail of the CLAUDE_TOOLKIT_DEBUG log
│   ├── capabilities.go      Capability enumeration shared by init/manage/doctor
│   └── doctor.go            Diagnostics and hook self-tests
├── pkg/
│   ├── installer/           Non-destructive settings.json merge engine
│   └── dir/                 Toolkit private dir (~/.claude-toolkit) resolution
├── internal/
│   ├── payload/             Typed stdin decoding and stdout response builders
│   ├── dispatcher/          Event -> handler routing and response merging
│   ├── hooks/
│   │   ├── registry.go      The single source of truth for what ships
│   │   ├── guard.go         PreToolUse rules (plus high-entropy secret scan)
│   │   ├── loopguard.go     Repeated-failure blocker (Pre+PostToolUse)
│   │   ├── bashparse.go     Quote-aware shell tokenizer
│   │   ├── context.go       SessionStart/UserPromptSubmit context injection
│   │   ├── format.go        PostToolUse formatter/fixer pipeline
│   │   ├── heal.go          PostToolUse incremental-test pointer
│   │   └── notify.go        Opt-in desktop notifications (Pre+PostToolUse)
│   ├── astsum/              Pure-Go structural summarizers (Go + Python)
│   ├── proxy/               429-retrying HTTP transport
│   └── testloc/             Source file -> incremental test mapping
└── scripts/
    ├── install.sh           Checksum-verifying curl installer
    └── install.ps1          Windows installer (irm | iex)
```

Three decisions worth knowing:

**No third-party dependencies.** `run` executes on the hot path of every matching tool call, so startup cost is user-visible latency. A stdlib-only binary starts in single-digit milliseconds; Cobra would add ~2 MB and measurable init time for help text nobody reads during a hook.

**`run` always exits 0.** Claude Code treats exit 2 as *block this tool* and any other non-zero exit as a hook error surfaced in the transcript. A bug in this binary would therefore either wedge your session or spam it. Every error path degrades to "no opinion" and logs instead. Blocking is only ever expressed through a deliberate JSON `deny`.

**`internal/hooks/registry.go` is the single source of truth.** `init` generates settings entries from the registered routes and `doctor` validates against the same list, so the installed configuration cannot silently diverge from the compiled behaviour.

---

## Development

```sh
make build      # build into ./bin
make test       # go test -race ./...
make check      # everything CI runs: vet, gofmt check, shellcheck, tests
make cover      # coverage report in a browser
make dist       # cross-compile archives + checksums into ./dist
make help       # list targets
```

### Adding a hook

1. Write a `dispatcher.Route` in `internal/hooks/`.
2. Register it in `internal/hooks/registry.go`.
3. If it is a new event, add its timeout to `hookTimeouts` in `cmd/init.go` and its short alias to `eventAlias` in `cmd/root.go`.
4. Run `claude-toolkit init` again and restart Claude Code.

`init` and `doctor` pick up the new route automatically — there is no second place to update. A capability that listens on two events (like `loopguard` and `notify`, which record on PostToolUse and block on PreToolUse) is one route with both events in `Events`; each matcher group in settings.json still carries a single `--cap` name.

The standalone command packages (`internal/testloc`, `internal/astsum`, `internal/proxy`) are pure libraries with no hook wiring; `cmd/` exposes them as subcommands (`test`, `ast`, `proxy`).

### Debugging

```sh
CLAUDE_TOOLKIT_DEBUG=1 claude-toolkit run < event.json
tail -f ~/.claude/claude-toolkit.log
```

Or run Claude Code itself with `claude --debug` to see hook registration, payloads and timing. `/hooks` inside a session lists everything currently loaded.

---

## Releasing

Tag and push; GitHub Actions handles the rest.

```sh
git tag v0.1.0
git push origin v0.1.0
```

[GoReleaser](https://goreleaser.com) cross-compiles for darwin/linux/windows on amd64/arm64, publishes archives and `checksums.txt` to the release page, and stamps version metadata via `-ldflags`. `scripts/install.sh` consumes those artifacts directly.

Dry-run the whole pipeline locally with `make snapshot`.

> The archive name template in `.goreleaser.yaml` and the URL `scripts/install.sh` builds must stay in sync — they are the two halves of one contract.

---

## License

[MIT](LICENSE)
