#!/usr/bin/env python3
"""Zero-dependency structural validation of the Claude Code plugin files.

Runs in CI without installing anything (plain python3 + json + re). It checks
what `claude plugin validate` checks for the files that matter:

  - .claude-plugin/plugin.json parses and has a kebab-case name
  - hooks/hooks.json parses, has the plugin-style top-level "hooks" object,
    every event's groups are arrays with a "hooks" array of command hooks,
    and every matcher is "*" or a valid regex.

Exit 0 on success, 1 with a message on the first problem found.
"""

import json
import re
import sys

VALID_EVENTS = {
    "PreToolUse", "PostToolUse", "PostToolUseFailure", "UserPromptSubmit",
    "UserPromptExpansion", "Notification", "Stop", "StopFailure",
    "SubagentStart", "SubagentStop", "PreCompact", "ConfigChange",
    "SessionStart", "SessionEnd", "WorktreeCreate", "WorktreeRemove",
    "PermissionRequest", "PermissionDenied", "Setup",
}


def fail(msg):
    print(f"✘ {msg}", file=sys.stderr)
    sys.exit(1)


def validate_manifest():
    try:
        with open(".claude-plugin/plugin.json") as f:
            p = json.load(f)
    except Exception as e:
        fail(f".claude-plugin/plugin.json: {e}")
    name = p.get("name", "")
    if not re.fullmatch(r"[a-z][a-z0-9]*(-[a-z0-9]+)*", name):
        fail(f'plugin.json: name {name!r} must be kebab-case')
    if not p.get("description"):
        fail("plugin.json: description is required")


def validate_hooks():
    try:
        with open("hooks/hooks.json") as f:
            h = json.load(f)
    except Exception as e:
        fail(f"hooks/hooks.json: {e}")
    hooks = h.get("hooks")
    if not isinstance(hooks, dict):
        fail('hooks/hooks.json: missing top-level "hooks" object')
    for ev, groups in hooks.items():
        if ev not in VALID_EVENTS:
            fail(f"hooks: unknown event {ev!r}")
        if not isinstance(groups, list):
            fail(f"hooks: {ev} must be a list of matcher groups")
        for g in groups:
            if not isinstance(g, dict):
                fail(f"hooks: {ev} group is not an object")
            matcher = g.get("matcher", "*")
            if matcher != "*":
                try:
                    re.compile(matcher)
                except re.error as e:
                    fail(f"hooks: {ev} invalid matcher regex {matcher!r}: {e}")
            entries = g.get("hooks")
            if not isinstance(entries, list):
                fail(f"hooks: {ev} group missing a hooks array")
            for hk in entries:
                if hk.get("type") != "command" or not hk.get("command"):
                    fail(f"hooks: {ev} hook must be {{type: command, command: ...}}")
                if hk.get("timeout") is not None and not isinstance(hk.get("timeout"), int):
                    fail(f"hooks: {ev} timeout must be an integer")


def main():
    validate_manifest()
    validate_hooks()
    print("✔ plugin structure valid")


if __name__ == "__main__":
    main()
