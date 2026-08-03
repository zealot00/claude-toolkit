package hooks

import "strings"

// segment is one simple command inside a compound shell line: the words that
// make it up, its redirect targets, and whether the preceding segment pipes
// into it.
type segment struct {
	fields    []string
	redirects []string
	pipedFrom bool
}

// base returns the command word, with leading VAR=value assignments and
// transparent prefixes (sudo, env, nohup, ...) stripped, along with the
// remaining operands. It reports ok=false for a segment that is only
// assignments or is empty.
func (s segment) base() (cmd string, rest []string, ok bool) {
	i := 0
	for i < len(s.fields) {
		f := s.fields[i]
		if isAssignment(f) {
			i++
			continue
		}
		if transparentPrefixes[trimPath(f)] {
			i++
			// `sudo -u bob rm ...`: skip the prefix's own flags.
			for i < len(s.fields) && strings.HasPrefix(s.fields[i], "-") {
				i++
			}
			continue
		}
		break
	}
	if i >= len(s.fields) {
		return "", nil, false
	}
	return trimPath(s.fields[i]), s.fields[i+1:], true
}

// transparentPrefixes are wrappers that delegate to the command after them, so
// the guard must look past them to find what actually runs.
var transparentPrefixes = map[string]bool{
	"sudo": true, "doas": true, "env": true, "command": true,
	"nohup": true, "time": true, "exec": true, "xargs": true,
}

func isAssignment(f string) bool {
	eq := strings.IndexByte(f, '=')
	if eq <= 0 {
		return false
	}
	for _, r := range f[:eq] {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// trimPath reduces /usr/bin/rm to rm so a rule keyed on "rm" still fires.
func trimPath(f string) string {
	if i := strings.LastIndexByte(f, '/'); i >= 0 {
		return f[i+1:]
	}
	return f
}

// tokenize splits a shell command line into segments. It is deliberately a
// partial shell parser: it understands quoting, escapes, the operators that
// separate commands, redirects, and command substitution. It does not
// understand control flow, and it does not need to -- everything it fails to
// split simply stays in one segment, which errs toward inspecting more text
// rather than less.
func tokenize(cmd string) []segment {
	var (
		segs     []segment
		cur      segment
		field    strings.Builder
		hasField bool
		redirect bool
	)

	flush := func() {
		if !hasField {
			return
		}
		if redirect {
			cur.redirects = append(cur.redirects, field.String())
			redirect = false
		} else {
			cur.fields = append(cur.fields, field.String())
		}
		field.Reset()
		hasField = false
	}
	end := func(piped bool) {
		flush()
		if len(cur.fields) > 0 || len(cur.redirects) > 0 {
			segs = append(segs, cur)
		}
		cur = segment{pipedFrom: piped}
	}

	r := []rune(cmd)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\'':
			j := i + 1
			for j < len(r) && r[j] != '\'' {
				j++
			}
			field.WriteString(string(r[i+1 : min(j, len(r))]))
			hasField = true
			i = j

		case c == '"':
			j := i + 1
			for j < len(r) && r[j] != '"' {
				if r[j] == '\\' {
					j++
				}
				j++
			}
			field.WriteString(string(r[i+1 : min(j, len(r))]))
			hasField = true
			i = j

		case c == '\\' && i+1 < len(r):
			field.WriteRune(r[i+1])
			hasField = true
			i++

		case c == '`':
			j := i + 1
			for j < len(r) && r[j] != '`' {
				j++
			}
			end(false)
			segs = append(segs, tokenize(string(r[i+1:min(j, len(r))]))...)
			i = j

		case c == '$' && i+1 < len(r) && r[i+1] == '(':
			j, depth := i+2, 1
			for j < len(r) && depth > 0 {
				if r[j] == '(' {
					depth++
				} else if r[j] == ')' {
					depth--
				}
				j++
			}
			inner := string(r[i+2 : max(i+2, j-1)])
			end(false)
			segs = append(segs, tokenize(inner)...)
			i = j - 1

		case c == ' ' || c == '\t':
			flush()

		case c == '\n' || c == ';':
			end(false)

		case c == '&':
			if i+1 < len(r) && r[i+1] == '&' {
				i++
			}
			end(false)

		case c == '|':
			if i+1 < len(r) && r[i+1] == '|' {
				i++
				end(false)
			} else {
				end(true)
			}

		case c == '>':
			flush()
			if i+1 < len(r) && r[i+1] == '>' {
				i++
			}
			redirect = true

		case c == '<':
			flush()

		default:
			field.WriteRune(c)
			hasField = true
		}
	}
	end(false)
	return segs
}
