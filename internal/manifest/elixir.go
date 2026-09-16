package manifest

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"polyaudit/internal/model"
)

type token struct{ kind, text string }

// lexMix recognizes literal strings, atoms, and delimiters. Comments and string
// contents cannot become dependency tuples. This is intentionally not an Elixir
// evaluator: function calls, attributes, interpolation, and computed lists remain
// unresolved, with diagnostics instead of fabricated values.
func lexMix(s string) ([]token, error) {
	var tokens []token
	for i := 0; i < len(s); {
		c := s[i]
		if c == '#' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		if unicode.IsSpace(rune(c)) {
			i++
			continue
		}
		if c == '"' || c == '\'' {
			start, quote := i, c
			i++
			for i < len(s) && s[i] != quote {
				if s[i] == '\\' {
					i++
				}
				i++
			}
			if i >= len(s) {
				return nil, fmt.Errorf("unterminated string in mix.exs")
			}
			i++
			value, err := strconv.Unquote(s[start:i])
			kind := "string"
			if err != nil || quote != '"' || strings.Contains(value, "#{") {
				kind = "dynamic"
			}
			tokens = append(tokens, token{kind, value})
			continue
		}
		if ident(c) {
			start := i
			for i < len(s) && ident(s[i]) {
				i++
			}
			tokens = append(tokens, token{"ident", s[start:i]})
			continue
		}
		if c == ':' && i+1 < len(s) && ident(s[i+1]) {
			i++
			start := i
			for i < len(s) && ident(s[i]) {
				i++
			}
			tokens = append(tokens, token{"atom", s[start:i]})
			continue
		}
		tokens = append(tokens, token{string(c), string(c)})
		i++
	}
	return tokens, nil
}

func ident(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '!' || c == '?'
}

func enclosed(ts []token, open, close string) ([]token, int, bool) {
	if len(ts) == 0 || ts[0].kind != open {
		return nil, 0, false
	}
	depth := 0
	for i, t := range ts {
		if t.kind == open {
			depth++
		}
		if t.kind == close {
			depth--
			if depth == 0 {
				return ts[1:i], i + 1, true
			}
		}
	}
	return nil, 0, false
}

func split(ts []token) [][]token {
	var out [][]token
	start, depth := 0, 0
	for i, t := range ts {
		switch t.kind {
		case "[", "{", "(":
			depth++
		case "]", "}", ")":
			depth--
		case ",":
			if depth == 0 {
				if i > start {
					out = append(out, ts[start:i])
				}
				start = i + 1
			}
		}
	}
	if start < len(ts) {
		out = append(out, ts[start:])
	}
	return out
}

func keyword(ts []token, name string) []token {
	for _, part := range split(ts) {
		if len(part) >= 3 && part[0].kind == "ident" && part[0].text == name && part[1].kind == ":" {
			return part[2:]
		}
	}
	return nil
}

func literal(ts []token, kind string) string {
	if len(ts) == 1 && ts[0].kind == kind {
		return ts[0].text
	}
	return ""
}

func functionList(ts []token, name string) ([]token, bool) {
	for i := 0; i+2 < len(ts); i++ {
		if ts[i].kind != "ident" || (ts[i].text != "def" && ts[i].text != "defp") || ts[i+1].text != name {
			continue
		}
		j := i + 2
		if j+1 < len(ts) && ts[j].kind == "(" && ts[j+1].kind == ")" {
			j += 2
		}
		if j < len(ts) && ts[j].kind == "," {
			j++
		}
		if j >= len(ts) || ts[j].text != "do" {
			return nil, false
		}
		j++
		if j < len(ts) && ts[j].kind == ":" {
			j++
		}
		body, n, ok := enclosed(ts[j:], "[", "]")
		// Reject list concatenation or any other computation after the literal.
		if !ok || j+n < len(ts) && ts[j+n].kind != "ident" {
			return nil, false
		}
		if j+n < len(ts) && ts[j+n].text != "end" && ts[j+n].text != "def" && ts[j+n].text != "defp" {
			return nil, false
		}
		return body, true
	}
	return nil, false
}

func elixir(data []byte, m *model.Manifest) error {
	ts, err := lexMix(string(data))
	if err != nil {
		return err
	}
	project, ok := functionList(ts, "project")
	if !ok {
		m.Diagnostics = append(m.Diagnostics, model.Warning(m.File, "project/0 is not a supported literal keyword list; metadata is unresolved"))
	}
	m.Name = literal(keyword(project, "app"), "atom")
	m.LanguageVersion = literal(keyword(project, "elixir"), "string")
	if v := keyword(project, "app"); len(v) > 0 && m.Name == "" {
		m.Diagnostics = append(m.Diagnostics, model.Warning(m.File, "dynamic app name is unresolved"))
	}
	if v := keyword(project, "elixir"); len(v) > 0 && m.LanguageVersion == "" {
		m.Diagnostics = append(m.Diagnostics, model.Warning(m.File, "dynamic Elixir version is unresolved"))
	}
	depsValue := keyword(project, "deps")
	var deps []token
	if body, n, found := enclosed(depsValue, "[", "]"); found && n == len(depsValue) {
		deps, ok = body, true
	} else if len(depsValue) == 3 && depsValue[0].text == "deps" && depsValue[1].kind == "(" && depsValue[2].kind == ")" {
		deps, ok = functionList(ts, "deps")
	} else {
		ok = len(depsValue) == 0 && project != nil
	}
	if !ok {
		m.Diagnostics = append(m.Diagnostics, model.Warning(m.File, "computed dependency list is unresolved; inspect mix.exs manually"))
		return nil
	}
	for _, item := range split(deps) {
		body, n, found := enclosed(item, "{", "}")
		parts := split(body)
		if !found || n != len(item) || len(parts) < 2 || literal(parts[0], "atom") == "" {
			m.Diagnostics = append(m.Diagnostics, model.Warning(m.File, "non-literal dependency is unresolved"))
			continue
		}
		d := model.Dependency{Name: literal(parts[0], "atom"), Kind: "runtime"}
		index := 1
		if parts[1][0].kind == "string" {
			d.Version = literal(parts[1], "string")
			index++
		}
		var opts []token
		for _, part := range parts[index:] {
			if len(opts) > 0 {
				opts = append(opts, token{",", ","})
			}
			opts = append(opts, part...)
		}
		if body, n, yes := enclosed(opts, "[", "]"); yes && n == len(opts) {
			opts = body
		}
		for _, key := range []string{"path", "git", "github", "hex"} {
			if value := literal(keyword(opts, key), "string"); value != "" {
				d.Source = key + ":" + value
				break
			}
		}
		if only := keyword(opts, "only"); len(only) > 0 {
			if v := literal(only, "atom"); v == "dev" || v == "test" {
				d.Kind = "development"
			} else if body, _, yes := enclosed(only, "[", "]"); yes {
				dev := len(body) > 0
				for _, part := range split(body) {
					v := literal(part, "atom")
					if v != "dev" && v != "test" {
						dev = false
					}
				}
				if dev {
					d.Kind = "development"
				}
			}
		}
		if d.Version == "" && d.Source == "" {
			m.Diagnostics = append(m.Diagnostics, model.Warning(m.File, "version/source for "+d.Name+" is unresolved"))
		}
		m.Dependencies = append(m.Dependencies, d)
	}
	return nil
}
