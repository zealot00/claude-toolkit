package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/zealot00/claude-toolkit/internal/profiles"
	"github.com/zealot00/claude-toolkit/pkg/installer"
)

const modelUsage = `Usage: claude-toolkit model [subcommand]

Manage provider profiles (base URL + auth token + model selection) and switch
between them without hand-editing settings.json.

  model                      interactive picker (number keys to switch)
  model list                 show profiles and which one is active
  model use <name>           switch to a profile (writes settings.json env)
                             flags: --scope=user|project|local (default user),
                                    --project-dir <dir>, --dry-run
  model add                  add a profile (interactive prompts, or flags)
                             flags: --name --base-url --token --model
                                    [--default-sonnet --default-opus --default-haiku]
  model rm <name>            remove a profile (settings.json is left untouched)

Profiles are stored in <toolkit root>/profiles.json (0600, atomic writes,
.profiles.json.bak backup) — they never live in settings.json. Switching
copies the chosen profile into settings.json's env block, so it takes effect
on the next Claude Code start (or ` + "`claude --resume`" + ` to keep the
session). See ` + "`claude-toolkit doctor`" + ` to validate profiles.
`

func modelCommand(args []string) int {
	if len(args) == 0 {
		return modelTUI()
	}
	switch args[0] {
	case "list", "ls":
		return modelList()
	case "use":
		return modelUse(args[1:])
	case "add", "new":
		return modelAdd(args[1:])
	case "rm", "remove", "delete":
		return modelRemove(args[1:])
	case "-h", "--help", "help":
		fmt.Print(modelUsage)
		return 0
	default:
		fmt.Fprint(os.Stderr, modelUsage)
		return 1
	}
}

// hostOf reduces a base URL to its host for display and matching.
func hostOf(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Host
}

// activeProfileName matches the user-scope settings env's base URL against the
// stored profiles. Empty when no profile matches (hand-edited env, or none).
func activeProfileName(st *profiles.Store) string {
	base := settingsEnvBaseURL()
	if base == "" {
		return ""
	}
	want := strings.ToLower(hostOf(base))
	for name, p := range st.Profiles {
		if strings.ToLower(hostOf(p["ANTHROPIC_BASE_URL"])) == want {
			return name
		}
	}
	return ""
}

// settingsEnvBaseURL reads ANTHROPIC_BASE_URL from the user-scope settings
// env (project-scope overrides are a per-project concern, reported separately
// by doctor). An unreadable/missing file yields "".
func settingsEnvBaseURL() string {
	path, err := installer.Path(installer.ScopeUser, "")
	if err != nil {
		return ""
	}
	env := readEnvBlock(path)
	return env["ANTHROPIC_BASE_URL"]
}

// readEnvBlock parses the env object out of a settings file, "" on any error.
func readEnvBlock(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	root := map[string]any{}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil
	}
	raw, ok := root["env"].(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func modelList() int {
	st, err := profiles.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(st.Profiles) == 0 {
		fmt.Println("No profiles yet. Add one with `claude-toolkit model add`.")
		return 0
	}
	active := activeProfileName(st)

	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tBASE URL\tMODEL\tSTATUS")
	names := make([]string, 0, len(st.Profiles))
	for n := range st.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := st.Profiles[n]
		status := ""
		if n == active {
			status = "* active"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", n, hostOf(p["ANTHROPIC_BASE_URL"]), p["ANTHROPIC_MODEL"], status)
	}
	w.Flush()
	if active == "" {
		fmt.Println("\n(settings.json env does not match any stored profile)")
	}
	return 0
}

// modelUseInner applies a profile to the settings env. Shared by the CLI and
// the TUI. dryRun reports what would change without writing.
func modelUseInner(name string, scope installer.Scope, projectDir string, dryRun bool) (int, error) {
	st, err := profiles.Load()
	if err != nil {
		return 1, err
	}
	p, ok := st.Profiles[name]
	if !ok {
		return 1, fmt.Errorf("no profile named %q (see `claude-toolkit model list`)", name)
	}
	path, err := installer.Path(scope, projectDir)
	if err != nil {
		return 1, err
	}
	plan, err := installer.ApplyEnv(path, profiles.ManagedKeys, p)
	if err != nil {
		return 1, err
	}
	if dryRun {
		if plan.Changed() {
			fmt.Printf("(dry run) would switch to profile %s in %s\n", name, path)
		} else {
			fmt.Printf("(dry run) already on profile %s\n", name)
		}
		return 0, nil
	}
	if !plan.Changed() {
		fmt.Printf("Already on profile %s (env unchanged).\n", name)
		return 0, nil
	}
	if err := plan.Apply(); err != nil {
		return 1, err
	}
	fmt.Printf("Switched to profile %s\n", name)
	fmt.Printf("  base_url: %s\n", hostOf(p["ANTHROPIC_BASE_URL"]))
	fmt.Printf("  model:    %s\n", p["ANTHROPIC_MODEL"])
	fmt.Println("Restart Claude Code for it to take effect (or `claude --resume` to keep the session).")
	return 0, nil
}

func modelUse(args []string) int {
	scope := installer.ScopeUser
	projectDir := ""
	dryRun := false
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		take := func(name string) (string, bool) {
			if strings.HasPrefix(a, name+"=") {
				return strings.TrimPrefix(a, name+"="), true
			}
			if a == name && i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case a == "--dry-run":
			dryRun = true
		case a == "-h" || a == "--help":
			fmt.Print(modelUsage)
			return 0
		default:
			if v, ok := take("--scope"); ok {
				scope = installer.Scope(v)
				continue
			}
			if v, ok := take("--project-dir"); ok {
				projectDir = v
				continue
			}
			rest = append(rest, a)
		}
	}
	if len(rest) != 1 {
		fmt.Fprintf(os.Stderr, "error: model use needs exactly one profile name\n%s", modelUsage)
		return 1
	}
	code, err := modelUseInner(rest[0], scope, projectDir, dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	return code
}

func modelAdd(args []string) int {
	name, baseURL, token, model := "", "", "", ""
	defS, defO, defH := "", "", ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		take := func(flag string) (string, bool) {
			if strings.HasPrefix(a, flag+"=") {
				return strings.TrimPrefix(a, flag+"="), true
			}
			if a == flag && i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case a == "-h" || a == "--help":
			fmt.Print(modelUsage)
			return 0
		default:
			if v, ok := take("--name"); ok {
				name = v
			} else if v, ok := take("--base-url"); ok {
				baseURL = v
			} else if v, ok := take("--token"); ok {
				token = v
			} else if v, ok := take("--model"); ok {
				model = v
			} else if v, ok := take("--default-sonnet"); ok {
				defS = v
			} else if v, ok := take("--default-opus"); ok {
				defO = v
			} else if v, ok := take("--default-haiku"); ok {
				defH = v
			} else {
				fmt.Fprintf(os.Stderr, "error: unknown flag %q\n%s", a, modelUsage)
				return 1
			}
		}
	}

	// Interactive prompts when any required value is missing.
	sc := bufio.NewScanner(os.Stdin)
	ask := func(prompt string) string {
		fmt.Printf("%s: ", prompt)
		if !sc.Scan() {
			return ""
		}
		return strings.TrimSpace(sc.Text())
	}
	if name == "" {
		name = ask("profile name")
	}
	if baseURL == "" {
		baseURL = ask("base URL (e.g. https://api.minimaxi.com/anthropic)")
	}
	if model == "" {
		model = ask("model (e.g. MiniMax-M3)")
	}
	if token == "" {
		token = ask("auth token (stored 0600 in profiles.json)")
	}
	if name == "" || baseURL == "" || model == "" {
		fmt.Fprintln(os.Stderr, "error: profile needs a name, base URL and model")
		return 1
	}

	st, err := profiles.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	p := profiles.Profile{
		"ANTHROPIC_BASE_URL":   baseURL,
		"ANTHROPIC_AUTH_TOKEN": token,
		"ANTHROPIC_MODEL":      model,
	}
	if defS != "" {
		p["ANTHROPIC_DEFAULT_SONNET_MODEL"] = defS
	}
	if defO != "" {
		p["ANTHROPIC_DEFAULT_OPUS_MODEL"] = defO
	}
	if defH != "" {
		p["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = defH
	}
	if _, dup := st.Profiles[name]; dup {
		fmt.Fprintf(os.Stderr, "error: profile %q already exists (use `model rm %s` first)\n", name, name)
		return 1
	}
	st.Profiles[name] = p
	if err := st.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("Saved profile %s -> %s (model %s)\n", name, hostOf(baseURL), model)
	fmt.Println("Switch to it with `claude-toolkit model use " + name + "`.")
	return 0
}

func modelRemove(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "error: model rm needs exactly one profile name\n%s", modelUsage)
		return 1
	}
	name := args[0]
	st, err := profiles.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if _, ok := st.Profiles[name]; !ok {
		fmt.Fprintf(os.Stderr, "error: no profile named %q\n", name)
		return 1
	}
	delete(st.Profiles, name)
	if err := st.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("Removed profile %s (settings.json untouched).\n", name)
	return 0
}

// modelTUI is the interactive picker: number keys switch, n adds, d deletes,
// q quits. Switching shows a before/after confirmation before writing.
func modelTUI() int {
	sc := bufio.NewScanner(os.Stdin)
	for {
		st, err := profiles.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		active := activeProfileName(st)
		names := make([]string, 0, len(st.Profiles))
		for n := range st.Profiles {
			names = append(names, n)
		}
		sort.Strings(names)

		fmt.Printf("\nclaude-toolkit provider profiles\n\n")
		for i, n := range names {
			p := st.Profiles[n]
			mark := "  "
			if n == active {
				mark = " *"
			}
			fmt.Printf("  %s[%d] %-10s %-32s %s\n", mark, i+1, n, hostOf(p["ANTHROPIC_BASE_URL"]), p["ANTHROPIC_MODEL"])
		}
		fmt.Printf("\n  1-%d: switch   n: add   d: delete   q: quit\n> ", len(names))

		if !sc.Scan() {
			return 0
		}
		line := strings.TrimSpace(sc.Text())
		switch line {
		case "q", "quit", "":
			return 0
		case "n":
			if code := modelAdd(nil); code != 0 {
				return code
			}
			continue
		case "d":
			if len(names) == 0 {
				continue
			}
			idx, err := strconv.Atoi(askLine(sc, "delete which profile (number)"))
			if err != nil || idx < 1 || idx > len(names) {
				continue
			}
			name := names[idx-1]
			conf := askLine(sc, fmt.Sprintf("delete profile %q? (y/N)", name))
			if strings.EqualFold(conf, "y") {
				delete(st.Profiles, name)
				if err := st.Save(); err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return 1
				}
				fmt.Printf("Removed profile %s.\n", name)
			}
			continue
		default:
			idx, err := strconv.Atoi(line)
			if err != nil || idx < 1 || idx > len(names) {
				fmt.Println("  ?")
				continue
			}
			name := names[idx-1]
			if name == active {
				fmt.Printf("Already on profile %s.\n", name)
				continue
			}
			p := st.Profiles[name]
			fmt.Printf("\nSwitch %s -> %s?\n", displayActive(active), fmt.Sprintf("%s (%s)", name, hostOf(p["ANTHROPIC_BASE_URL"])))
			conf := askLine(sc, "confirm (y/N)")
			if !strings.EqualFold(conf, "y") {
				continue
			}
			if code, err := modelUseInner(name, installer.ScopeUser, "", false); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return code
			}
		}
	}
}

func askLine(sc *bufio.Scanner, prompt string) string {
	fmt.Printf("%s: ", prompt)
	if !sc.Scan() {
		return ""
	}
	return strings.TrimSpace(sc.Text())
}

func displayActive(active string) string {
	if active == "" {
		return "(no stored profile active)"
	}
	return active
}

// checkProfiles validates the provider profile store and reports which
// profile is active for the current session. Called by doctor.
func checkProfiles(rp *report) {
	st, err := profiles.Load()
	if err != nil {
		rp.warn("provider profiles", fmt.Sprintf("cannot load profiles: %v", err), "Run `claude-toolkit model add` to recreate the store.")
		return
	}
	if len(st.Profiles) == 0 {
		rp.ok("provider profiles", "no profiles stored")
		return
	}
	rp.ok("provider profiles", fmt.Sprintf("%d stored (profiles.json)", len(st.Profiles)))
	base := settingsEnvBaseURL()
	if base == "" {
		rp.ok("active provider", "no ANTHROPIC_BASE_URL in settings env — default Anthropic auth")
		return
	}
	if active := activeProfileName(st); active != "" {
		p := st.Profiles[active]
		rp.ok("active provider", fmt.Sprintf("%s (%s)", active, hostOf(p["ANTHROPIC_BASE_URL"])))
	} else {
		rp.ok("active provider", fmt.Sprintf("settings env uses %s but no stored profile matches", hostOf(base)))
	}
}
