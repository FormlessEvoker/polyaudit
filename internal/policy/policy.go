// Package policy centralizes discovery, cleanup, and bundle exclusions.
package policy

import (
	"fmt"
	"path"
	"strings"
)

var Signatures = map[string]bool{"package.json": true, "mix.exs": true, "site.yml": true, "go.mod": true}
var BuildDirs = map[string]bool{"node_modules": true, "_build": true, "deps": true, "dist": true, ".next": true, "target": true}

var excludedDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "vendor": true, ".venv": true, "venv": true,
	"__pycache__": true, ".cache": true, "coverage": true, ".turbo": true,
	".elixir_ls": true, "_ai_audit_ready": true,
}

var extensions = map[string]bool{}

func init() {
	for _, e := range strings.Fields(".js .jsx .mjs .cjs .ts .tsx .mts .cts .json .ex .exs .erl .hrl .go .mod .yml .yaml .md .mdx .rst .txt .toml .ini .cfg .conf .sh .bash .zsh .sql .graphql .gql .proto .html .css .scss .sass .svelte .vue .eex .heex .leex .hbs .mustache .j2 .jinja .jinja2 .tf .hcl .py .rb .rs .java .kt .xml") {
		extensions[e] = true
	}
}

type Policy struct {
	Excludes       []string // slash-separated paths relative to the catalog root
	OutputRelative string
}

func ValidateExclude(s string) (string, error) {
	s = strings.TrimSuffix(strings.ReplaceAll(s, "\\", "/"), "/")
	if s == "" || path.IsAbs(s) || path.Clean(s) == "." || path.Clean(s) == ".." || strings.HasPrefix(path.Clean(s), "../") {
		return "", fmt.Errorf("exclude must be a relative path inside the scan root: %q", s)
	}
	return path.Clean(s), nil
}

func Within(parent, child string) bool {
	return parent == "." || parent == child || strings.HasPrefix(child, parent+"/")
}

func (p Policy) Excluded(rel string) bool {
	if p.OutputRelative != "" && Within(p.OutputRelative, rel) {
		return true
	}
	for _, e := range p.Excludes {
		if Within(e, rel) {
			return true
		}
	}
	return false
}

func (p Policy) SkipDir(rel string) bool {
	name := path.Base(rel)
	return p.Excluded(rel) || BuildDirs[name] || excludedDirs[name]
}

// ProtectedSubtree prevents cleanup from deleting an excluded path indirectly.
func (p Policy) ProtectedSubtree(rel string) bool {
	if p.OutputRelative != "" && (Within(rel, p.OutputRelative) || Within(p.OutputRelative, rel)) {
		return true
	}
	for _, e := range p.Excludes {
		if Within(rel, e) || Within(e, rel) {
			return true
		}
	}
	return false
}

// IncludeFile returns a reason when a file should be left out of a bundle.
func (p Policy) IncludeFile(rel string) string {
	if p.Excluded(rel) {
		return "user_or_output_exclusion"
	}
	n := strings.ToLower(path.Base(rel))
	if n == ".env" || strings.HasPrefix(n, ".env.") || strings.HasSuffix(n, ".pem") || strings.HasSuffix(n, ".key") || strings.HasSuffix(n, ".p12") || strings.HasSuffix(n, ".pfx") || strings.HasPrefix(n, "id_rsa") || strings.HasPrefix(n, "id_ed25519") || n == ".npmrc" || n == ".netrc" || n == "credentials" || strings.HasPrefix(n, "credentials.") || n == "secrets.yml" || n == "secrets.yaml" {
		return "sensitive_filename"
	}
	switch n {
	case "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb", "mix.lock", "go.sum", "cargo.lock":
		return "lockfile"
	}
	if strings.HasSuffix(n, ".min.js") || strings.HasSuffix(n, ".min.css") || strings.HasSuffix(n, ".map") {
		return "generated_file"
	}
	if extensions[path.Ext(n)] || n == "dockerfile" || strings.HasPrefix(n, "dockerfile.") || n == "makefile" || n == "justfile" || n == "procfile" || n == ".gitignore" || n == ".dockerignore" || n == "license" || n == "readme" || n == ".tool-versions" || n == ".nvmrc" {
		return ""
	}
	return "unsupported_file_type"
}
