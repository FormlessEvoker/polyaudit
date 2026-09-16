// Package model defines the versioned inventory exchanged with analysis tools.
package model

type Diagnostic struct {
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Kind    string `json:"kind"`
	Source  string `json:"source,omitempty"`
}

type Manifest struct {
	File            string            `json:"file"`
	Ecosystem       string            `json:"ecosystem"`
	Language        string            `json:"language"`
	Name            string            `json:"name,omitempty"`
	LanguageVersion string            `json:"language_version,omitempty"`
	Dependencies    []Dependency      `json:"dependencies"`
	Attributes      map[string]string `json:"attributes,omitempty"`
	Diagnostics     []Diagnostic      `json:"diagnostics,omitempty"`
}

type Omission struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Bundle struct {
	Path        string     `json:"path"`
	Compression string     `json:"compression"`
	Files       int        `json:"files"`
	SourceBytes int64      `json:"source_bytes"`
	Bytes       int64      `json:"bytes"`
	SHA256      string     `json:"sha256"`
	Omissions   []Omission `json:"omissions,omitempty"`
	// Omitted counts all omissions, including those beyond the bounded detail list.
	Omitted int `json:"omitted"`
}

type Cleanup struct {
	Path    string `json:"path"`
	Status  string `json:"status"` // planned, deleted, skipped, failed
	Message string `json:"message,omitempty"`
}

type Project struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Path            string       `json:"path"`
	ParentID        string       `json:"parent_id,omitempty"`
	PrimaryLanguage string       `json:"primary_language"`
	Languages       []string     `json:"languages"`
	Manifests       []Manifest   `json:"manifests"`
	Bundle          *Bundle      `json:"bundle,omitempty"`
	Cleanup         []Cleanup    `json:"cleanup,omitempty"`
	Diagnostics     []Diagnostic `json:"diagnostics,omitempty"`
}

type Inventory struct {
	SchemaVersion   int          `json:"schema_version"`
	ToolVersion     string       `json:"tool_version"`
	GeneratedAt     string       `json:"generated_at"`
	ScanRoot        string       `json:"scan_root"`
	OutputDirectory string       `json:"output_directory"`
	DryRun          bool         `json:"dry_run"`
	Settings        Settings     `json:"settings"`
	Projects        []Project    `json:"projects"`
	Diagnostics     []Diagnostic `json:"diagnostics,omitempty"`
}

type Settings struct {
	Workers        int      `json:"workers"`
	Clean          bool     `json:"clean"`
	Gzip           bool     `json:"gzip"`
	MaxFileBytes   int64    `json:"max_file_bytes"`
	MaxBundleBytes int64    `json:"max_bundle_bytes"`
	Excludes       []string `json:"excludes,omitempty"`
}

func Error(path, message string) Diagnostic {
	return Diagnostic{Severity: "error", Path: path, Message: message}
}

func Warning(path, message string) Diagnostic {
	return Diagnostic{Severity: "warning", Path: path, Message: message}
}

func (i Inventory) HasErrors() bool {
	for _, d := range i.Diagnostics {
		if d.Severity == "error" {
			return true
		}
	}
	for _, p := range i.Projects {
		for _, d := range p.Diagnostics {
			if d.Severity == "error" {
				return true
			}
		}
		for _, m := range p.Manifests {
			for _, d := range m.Diagnostics {
				if d.Severity == "error" {
					return true
				}
			}
		}
	}
	return false
}
