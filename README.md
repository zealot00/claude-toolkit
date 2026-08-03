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
- [The three commands](#the-three-commands)
- [What the hooks do](#what-the-hooks-do)
- [How self-installation works](#how-self-installation-works)
- [Configuration](#configuration)
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

## The three commands

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
| `--scope user\|project\|local` | Target `~/.claude/settings.json`, `.claude/settings.json`, or `.claude/settings.local.json` (default `user`) |
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

---

## What the hooks do

### PreToolUse — guard

Blocks irreversible and exfiltrating commands before Claude runs them.

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
| `git-force-push` | ask | `git push --force` (but not `--force-with-lease`) |
| `power-state` | ask | `shutdown`, `reboot`, `halt` |
| `write-to-secret` | ask | Writing `.env`, `.npmrc`, `*.pem`, `~/.claude/settings.json` |

The rules are deliberately narrow. **`rm -rf ./build`, `rm -rf node_modules`, `git push --force-with-lease` and `curl -o file.sh` all pass through untouched** — a guard that cries wolf gets uninstalled. Roughly half of the [test suite](internal/hooks/guard_test.go) exists to pin the *absence* of false positives.

The command parser is quote-aware and descends into command substitution, so `echo $(rm -rf ~)` is caught while `echo "never run rm -rf /"` is not.

### SessionStart — repository context

Injects the repo's current state at session start: branch, divergence from upstream, uncommitted files, and the last five commits. Without it Claude opens every session blind and burns its first few tool calls rediscovering where it is.

### PostToolUse — formatter

Runs the project's formatter after Claude writes a file — `gofmt`, `prettier`, `ruff`/`black`, `rustfmt`, `shfmt`, picked by extension and skipped when the tool is not installed. `prettier` is resolved from the nearest `node_modules/.bin` first, so the project's pinned version wins.

The point is not tidiness. When a formatter rewrites a file, Claude's in-context copy goes stale and its next `Edit` fails on a string that no longer matches. So the hook speaks up **only when the file actually changed**, telling Claude to re-read. If the formatter fails outright, that usually means Claude just wrote code that does not parse — which it is also told.

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

## Architecture

```
claude-toolkit/
├── main.go                  Entry point; delegates to cmd
├── cmd/
│   ├── root.go              Subcommand routing (stdlib flag, zero deps)
│   ├── run.go               Hook runtime: stdin JSON -> dispatch -> stdout JSON
│   ├── init.go              Self-installation; derives config from routes
│   └── doctor.go            Diagnostics and hook self-tests
├── pkg/
│   └── installer/
│       └── settings.go      Non-destructive settings.json merge engine
├── internal/
│   ├── payload/             Typed stdin decoding and stdout response builders
│   ├── dispatcher/          Event -> handler routing and response merging
│   └── hooks/
│       ├── registry.go      The single source of truth for what ships
│       ├── guard.go         PreToolUse rules
│       ├── bashparse.go     Quote-aware shell tokenizer
│       ├── context.go       SessionStart repository context
│       └── format.go        PostToolUse formatter
└── scripts/
    └── install.sh           Checksum-verifying curl installer
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

`init` and `doctor` pick up the new route automatically — there is no second place to update.

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
