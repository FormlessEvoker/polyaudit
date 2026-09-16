// Package manifest extracts declared metadata without running repository code.
package manifest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"gopkg.in/yaml.v3"
	"polyaudit/internal/fsutil"
	"polyaudit/internal/model"
)

const MaxBytes = 2 << 20

func Read(ctx context.Context, root *os.Root, dir, file string) model.Manifest {
	m := model.Manifest{File: file, Dependencies: []model.Dependency{}}
	switch file {
	case "package.json":
		m.Ecosystem, m.Language = "node", "JavaScript"
	case "mix.exs":
		m.Ecosystem, m.Language = "elixir", "Elixir"
	case "site.yml":
		m.Ecosystem, m.Language = "ansible", "YAML"
	case "go.mod":
		m.Ecosystem, m.Language = "go", "Go"
	}
	data, err := fsutil.ReadLimited(ctx, root, path.Join(dir, file), MaxBytes)
	if err == nil {
		switch file {
		case "package.json":
			err = node(data, &m)
		case "go.mod":
			err = gomod(data, &m)
		case "mix.exs":
			err = elixir(data, &m)
		case "site.yml":
			err = ansible(data, &m)
		}
	}
	if err != nil {
		m.Diagnostics = append(m.Diagnostics, model.Error(file, err.Error()))
	}
	if file == "site.yml" && err == nil {
		for _, extra := range []string{"requirements.yml", "roles/requirements.yml", "collections/requirements.yml"} {
			_, statErr := root.Lstat(path.Join(dir, extra))
			if os.IsNotExist(statErr) {
				continue
			}
			b, readErr := fsutil.ReadLimited(ctx, root, path.Join(dir, extra), MaxBytes)
			if readErr == nil {
				readErr = requirements(b, extra, &m)
			}
			if readErr != nil {
				m.Diagnostics = append(m.Diagnostics, model.Error(extra, readErr.Error()))
			}
		}
	}
	sort.Slice(m.Dependencies, func(i, j int) bool {
		a, b := m.Dependencies[i], m.Dependencies[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		return a.Source < b.Source
	})
	deps := m.Dependencies[:0]
	for _, d := range m.Dependencies {
		if len(deps) == 0 || deps[len(deps)-1] != d {
			deps = append(deps, d)
		}
	}
	m.Dependencies = deps
	return m
}

func node(data []byte, m *model.Manifest) error {
	var p struct {
		Name                 string            `json:"name"`
		Engines              map[string]string `json:"engines"`
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
		PackageManager       string            `json:"packageManager"`
	}
	if len(strings.TrimSpace(string(data))) == 0 || strings.TrimSpace(string(data))[0] != '{' {
		return fmt.Errorf("package.json must be an object")
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("parse package.json: %w", err)
	}
	m.Name, m.LanguageVersion = p.Name, p.Engines["node"]
	for kind, deps := range map[string]map[string]string{"runtime": p.Dependencies, "development": p.DevDependencies, "peer": p.PeerDependencies, "optional": p.OptionalDependencies} {
		for name, version := range deps {
			d := model.Dependency{Name: name, Version: version, Kind: kind}
			if strings.HasPrefix(version, "file:") || strings.HasPrefix(version, "link:") || strings.HasPrefix(version, "workspace:") {
				d.Source = version
			}
			m.Dependencies = append(m.Dependencies, d)
			if name == "typescript" {
				m.Language = "TypeScript"
			}
		}
	}
	if p.PackageManager != "" {
		m.Attributes = map[string]string{"package_manager": p.PackageManager}
	}
	return nil
}

func gomod(data []byte, m *model.Manifest) error {
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return err
	}
	if f.Module == nil {
		return fmt.Errorf("go.mod has no module directive")
	}
	m.Name = f.Module.Mod.Path
	if f.Go != nil {
		m.LanguageVersion = f.Go.Version
	}
	if f.Toolchain != nil {
		m.Attributes = map[string]string{"toolchain": f.Toolchain.Name}
	}
	for _, req := range f.Require {
		d := model.Dependency{Name: req.Mod.Path, Version: req.Mod.Version, Kind: "runtime"}
		if req.Indirect {
			d.Kind = "indirect"
		}
		for _, rep := range f.Replace {
			if rep.Old.Path == req.Mod.Path && (rep.Old.Version == "" || rep.Old.Version == req.Mod.Version) {
				d.Source = rep.New.Path
				if rep.New.Version != "" {
					d.Source += "@" + rep.New.Version
				}
			}
		}
		m.Dependencies = append(m.Dependencies, d)
	}
	return nil
}

func document(data []byte) (*yaml.Node, error) {
	var n yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&n); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected a single YAML document")
	}
	if len(n.Content) != 1 {
		return nil, fmt.Errorf("expected one YAML document")
	}
	return n.Content[0], nil
}

func field(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

func ansible(data []byte, m *model.Manifest) error {
	n, err := document(data)
	if err != nil {
		return err
	}
	if n.Kind != yaml.SequenceNode {
		return fmt.Errorf("site.yml must be a playbook sequence")
	}
	for _, play := range n.Content {
		if play.Kind != yaml.MappingNode {
			m.Diagnostics = append(m.Diagnostics, model.Warning(m.File, "non-literal play could not be inspected"))
			continue
		}
		for _, key := range []string{"import_playbook", "ansible.builtin.import_playbook"} {
			if value := scalar(field(play, key)); value != "" {
				m.Dependencies = append(m.Dependencies, model.Dependency{Name: value, Kind: "playbook", Source: value})
			}
		}
		for _, group := range []string{"roles", "collections"} {
			if list := field(play, group); list != nil {
				if list.Kind != yaml.SequenceNode {
					m.Diagnostics = append(m.Diagnostics, model.Warning(m.File, "dynamic "+group+" could not be inspected"))
					continue
				}
				for _, item := range list.Content {
					name := scalar(item)
					if name == "" {
						name = scalar(field(item, "role"))
					}
					if name == "" {
						m.Diagnostics = append(m.Diagnostics, model.Warning(m.File, "unresolved "+group+" entry"))
						continue
					}
					m.Dependencies = append(m.Dependencies, model.Dependency{Name: name, Kind: strings.TrimSuffix(group, "s")})
				}
			}
		}
	}
	return nil
}

func requirements(data []byte, filename string, m *model.Manifest) error {
	n, err := document(data)
	if err != nil {
		return err
	}
	lists := map[string]*yaml.Node{}
	if n.Kind == yaml.SequenceNode {
		lists["role"] = n
	} else if n.Kind == yaml.MappingNode {
		lists["role"], lists["collection"] = field(n, "roles"), field(n, "collections")
	} else {
		return fmt.Errorf("requirements must be a sequence or mapping")
	}
	for _, kind := range []string{"role", "collection"} {
		list := lists[kind]
		if list == nil {
			continue
		}
		if list.Kind != yaml.SequenceNode {
			return fmt.Errorf("%s requirements must be a sequence", kind)
		}
		for _, item := range list.Content {
			name, version, source := scalar(item), scalar(field(item, "version")), scalar(field(item, "src"))
			if name == "" {
				name = scalar(field(item, "name"))
			}
			if name == "" {
				name = source
			}
			if name == "" {
				m.Diagnostics = append(m.Diagnostics, model.Warning(filename, "unresolved requirement"))
				continue
			}
			m.Dependencies = append(m.Dependencies, model.Dependency{Name: name, Version: version, Source: source, Kind: kind})
		}
	}
	return nil
}
