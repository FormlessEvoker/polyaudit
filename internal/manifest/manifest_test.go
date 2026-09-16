package manifest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FormlessEvoker/polyaudit/internal/model"
)

func readFixture(t *testing.T, file, content string, extras map[string]string) *os.Root {
	t.Helper()
	dir := t.TempDir()
	extrasCopy := map[string]string{file: content}
	for name, value := range extras {
		extrasCopy[name] = value
	}
	for name, value := range extrasCopy {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestNode(t *testing.T) {
	r := readFixture(t, "package.json", `{"name":"@demo/web","engines":{"node":">=22"},"dependencies":{"express":"^5.0.0","@demo/sdk":"workspace:*"},"devDependencies":{"typescript":"^5.0.0"}}`, nil)
	m := Read(context.Background(), r, ".", "package.json")
	if len(m.Diagnostics) > 0 || m.Name != "@demo/web" || m.Language != "TypeScript" || m.LanguageVersion != ">=22" || len(m.Dependencies) != 3 {
		t.Fatalf("unexpected metadata: %+v", m)
	}
	found := false
	for _, d := range m.Dependencies {
		if d.Name == "@demo/sdk" && d.Source == "workspace:*" {
			found = true
		}
	}
	if !found {
		t.Fatal("workspace source missing")
	}
}

func TestGo(t *testing.T) {
	r := readFixture(t, "go.mod", "module example.test/orders\n\ngo 1.26.0\ntoolchain go1.26.5\nrequire (\n example.test/shared v1.2.3\n example.test/transitive v1.0.0 // indirect\n)\nreplace example.test/shared => ../shared\n", nil)
	m := Read(context.Background(), r, ".", "go.mod")
	if len(m.Diagnostics) > 0 || m.Name != "example.test/orders" || m.LanguageVersion != "1.26.0" || len(m.Dependencies) != 2 || m.Attributes["toolchain"] != "go1.26.5" {
		t.Fatalf("unexpected metadata: %+v", m)
	}
	if m.Dependencies[0].Kind != "indirect" || m.Dependencies[1].Source != "../shared" {
		t.Fatalf("missing require/replace semantics: %+v", m.Dependencies)
	}
}

func TestElixirLiteralAndComments(t *testing.T) {
	s := `defmodule Demo.MixProject do
  use Mix.Project
  # app: :wrong, elixir: "wrong"
  def project do
    [app: :demo, version: "0.1.0", elixir: "~> 1.16", deps: deps()]
  end
  defp deps do
    [
      {:phoenix, "~> 1.7"},
      {:shared, path: "../shared"},
      {:credo, "~> 1.7", only: [:dev, :test]},
      # {:fake, "9.9"}
    ]
  end
end`
	r := readFixture(t, "mix.exs", s, nil)
	m := Read(context.Background(), r, ".", "mix.exs")
	if len(m.Diagnostics) > 0 || m.Name != "demo" || m.LanguageVersion != "~> 1.16" || len(m.Dependencies) != 3 {
		t.Fatalf("unexpected metadata: %+v", m)
	}
	if m.Dependencies[0].Name != "credo" || m.Dependencies[0].Kind != "development" {
		t.Fatalf("only scope missing: %+v", m.Dependencies)
	}
}

func TestElixirDynamicDoesNotInventMetadata(t *testing.T) {
	s := `defmodule Demo.MixProject do
  def project do
    [app: app_name(), elixir: System.get_env("ELIXIR_VERSION"), deps: deps()]
  end
  defp deps do
    base_deps() ++ [{:hidden, "1.0"}]
  end
end`
	r := readFixture(t, "mix.exs", s, nil)
	m := Read(context.Background(), r, ".", "mix.exs")
	if m.Name != "" || m.LanguageVersion != "" || len(m.Dependencies) != 0 || len(m.Diagnostics) < 3 {
		t.Fatalf("dynamic metadata should remain unresolved: %+v", m)
	}
}

func TestAnsible(t *testing.T) {
	r := readFixture(t, "site.yml", "- name: Deploy\n  hosts: apps\n  roles:\n    - role: demo.web\n  collections:\n    - community.general\n- import_playbook: database.yml\n", map[string]string{"requirements.yml": "collections:\n  - name: community.general\n    version: '>=8.0.0'\nroles:\n  - src: https://example.test/demo.web.git\n    name: demo.web\n    version: v1\n"})
	m := Read(context.Background(), r, ".", "site.yml")
	if len(m.Diagnostics) > 0 || m.Ecosystem != "ansible" || len(m.Dependencies) != 5 {
		t.Fatalf("unexpected metadata: %+v", m)
	}
}

func TestInvalidManifests(t *testing.T) {
	for _, tt := range []struct{ file, body string }{{"package.json", "null"}, {"package.json", "{"}, {"go.mod", "require whatever"}, {"site.yml", "hosts: wrong-root"}, {"site.yml", "- hosts: one\n---\n- hosts: two\n"}} {
		t.Run(tt.file+tt.body, func(t *testing.T) {
			r := readFixture(t, tt.file, tt.body, nil)
			m := Read(context.Background(), r, ".", tt.file)
			if len(m.Diagnostics) == 0 || m.Diagnostics[0].Severity != "error" {
				t.Fatalf("expected parse error: %+v", m)
			}
		})
	}
}

func TestManifestSizeLimit(t *testing.T) {
	r := readFixture(t, "package.json", strings.Repeat(" ", MaxBytes+1), nil)
	m := Read(context.Background(), r, ".", "package.json")
	if len(m.Diagnostics) == 0 {
		t.Fatal("oversized manifest accepted")
	}
}

func FuzzElixir(f *testing.F) {
	f.Add("def project do [app: :demo, deps: []] end")
	f.Add("defp deps do [{:a, \"~> 1\", only: [:test]}] end")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 65536 {
			t.Skip()
		}
		_ = elixir([]byte(s), &model.Manifest{File: "mix.exs"})
	})
}
