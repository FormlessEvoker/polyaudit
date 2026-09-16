# polyaudit

Prepare a catalog of polyglot repositories for architectural analysis. `polyaudit`
discovers projects, extracts declared dependencies, optionally removes build
folders, and writes bounded source packs plus a machine-readable inventory.
It never runs repository code, installs project dependencies, or calls an AI API.

## Build and run

Requires Go **1.26 or newer**. The two direct dependencies are `golang.org/x/mod`
for Go manifests and `gopkg.in/yaml.v3` for Ansible YAML; versions are pinned in
`go.mod` and `go.sum`.

```sh
make build
./bin/polyaudit scan /path/to/catalog --workers 8

# Preview metadata and proposed deletions. No filesystem writes.
./bin/polyaudit scan /path/to/catalog --clean --dry-run > cleanup-plan.json

# Delete the named artifact folders and create packs.
./bin/polyaudit scan /path/to/catalog --clean

# Optional gzip, different destination, and explicit exclusions.
./bin/polyaudit scan /path/to/catalog --gzip --output /path/to/audit-output \
  --exclude legacy/customer-data --exclude web/config/private.json
```

In this workspace, example repositories live in `../sample-repos`:

```sh
./bin/polyaudit scan ../sample-repos --clean --dry-run
./bin/polyaudit scan ../sample-repos
```

## Outputs

```text
<catalog>/_ai_audit_ready/
├── catalog_inventory.json
└── repo_packs/
    └── <generation>/
        ├── p-<path-hash>.txt
        └── p-<path-hash>.txt
```

The inventory is the entry point. `project.path` is relative to `scan_root`;
`bundle.path` is relative to `output_directory`; manifest filenames, manifest
diagnostic paths, and omission paths are relative to the project. Discovery and
project diagnostic paths and cleanup paths are relative to the catalog.

Each project has a stable ID derived from its relative path, an optional parent
ID, detected languages, manifest-specific names, language versions, and declared
dependencies (including development, optional, peer, indirect, and local sources
where supported). Versions are declared constraints, **not resolved versions**.
Dependency lists remain attached to their manifests to preserve ecosystem and
scope. Framework packages such as Phoenix, Express, or Next.js are included there.

Packs preserve source bytes and use quoted relative filenames with byte-counted
headers:

```text
--- FILE "src/main.js" BYTES 21 ---
console.log("demo");

--- END FILE ---
```

The byte count covers only the original source content. Pack SHA-256 and `bytes`
describe the stored file (compressed bytes when using gzip). File and omission
ordering is deterministic, as are pack contents for unchanged source and options.
Timestamps and generation paths in inventories intentionally vary between runs.

## Detection and parsing

| Signature | Metadata |
| --- | --- |
| `package.json` | Name, Node engine constraint, package manager, dependencies and scopes; TypeScript classification when declared |
| `mix.exs` | Literal project app and Elixir constraint, literal dependency tuples, supported local/Git sources and dev/test scopes |
| `go.mod` | Module name, Go version, toolchain, direct/indirect requirements, replacements |
| `site.yml` | Ansible roles, collections, imported playbooks, and adjacent `requirements.yml`, `roles/requirements.yml`, `collections/requirements.yml` |

Every manifest-bearing directory is a project, including workspace packages and
nested repositories. Parent packs omit nested projects; `parent_id` preserves the
relationship. A directory with several signatures becomes one project with several
manifests. Primary language follows the fixed signature priority Go, Elixir, Node,
Ansible; it is a label, not a measurement of source volume. Project name comes
from that primary manifest, falling back to the directory name.

Elixir extraction handles literal keyword lists returned by `project/0`, inline
dependency lists, and a literal list returned by `deps/0` called as `deps()`.
It does not evaluate attributes, macros, conditionals, concatenated lists, or
interpolation. Unresolved metadata produces warnings. YAML aliases in role lists
are also reported as unresolved. Ansible playbooks do not generally declare a
language/runtime version, so that field may be absent. Other playbook names,
standalone source directories without a signature, resolved lockfile graphs, and
runtime service discovery are outside the detector's scope.

## Cleanup and source selection

Without `--clean`, source directories are never changed. With `--clean`, these
exact directory names beneath discovered projects are recursively deleted:

```text
node_modules  _build  deps  dist  .next  target
```

Those names are treated as artifacts regardless of their contents. They are
pruned during discovery, so a project stored inside one is not discovered.
Artifact folders outside discovered projects are left alone. Symlinks are skipped;
all operations use anchored `os.Root` handles, and cleanup is additionally scoped
to each project's root. The output directory, nested projects, and explicit
exclusions are protected, including an excluded path inside a cleanup candidate.
The scan root itself is never deleted. Cleanup is irreversible and is **not**
rolled back if a subsequent bundle or output operation fails.

Packs include common JavaScript/TypeScript, Elixir/Erlang, Go, YAML, Markdown,
configuration, template, shell, SQL, and other source formats. They omit artifact
directories, version control metadata, vendor/cache/coverage folders, lockfiles,
minified assets, source maps, unsupported extensions, symlinks, special files,
non-UTF-8/binary content, and encrypted Ansible Vault files. Common sensitive
filenames such as `.env*`, `.npmrc`, private keys, `credentials.*`, and
`secrets.yml` are excluded. This is filename/content-type filtering, **not secret
redaction**: secrets embedded in ordinary source files can still be included.
Review packs before sharing them.

`--exclude` accepts an exact catalog-relative path and its descendants; repeat it
for multiple paths. It does not accept glob patterns. `.gitignore` rules are not
interpreted. Omission details are capped at 1,000 entries per project; `omitted`
counts every omitted file or pruned directory, including details beyond the cap.
Pruned directory contents are not enumerated or counted individually.

## Limits and failure behavior

| Flag | Default | Meaning |
| --- | --- | --- |
| `--workers` | min(CPUs, 8) | Concurrent discovery workers and subsequent project workers |
| `--max-file-bytes` | 1,048,576 | Maximum source file size |
| `--max-bundle-bytes` | 33,554,432 | Maximum uncompressed pack size including framing |
| `--dry-run` | false | Metadata and optional cleanup plan as JSON; no bundles or writes |
| `--json` | false | Print the final inventory to stdout |
| `--gzip` | false | Compress each pack independently |

Manifest reads have a separate 2 MiB limit. Limits omit complete files; source is
never truncated or minified. Size-limit omissions are recorded, not fatal errors.
Dry runs do not read all source files and therefore do not forecast bundle sizes
or source-read errors.

Directory discovery and project processing use bounded worker pools. Source is
read one bounded file at a time per worker and streamed into each pack. Memory
also includes pending directory paths, sorted directory entries, project metadata,
and bounded per-project omission details; it is not constant in catalog size.

Packs are written into a private staging directory. Complete packs are moved into
a new generation before the inventory is atomically replaced on Unix filesystems.
Per-project errors produce a partial inventory with diagnostics; other projects
continue. Fatal publication errors or cancellation preserve the previous inventory.
Previous generations remain available so readers of older inventories retain
valid references. Remove obsolete generations manually when no readers need them.

An exclusive `.polyaudit.lock` prevents overlapping writers to the same output.
After a hard crash, remove the stale lock and `.staging-*` directories only after
confirming no scan is running. Different output paths do not coordinate their
cleanup operations: scan a quiescent catalog, without concurrent builds, renames,
or cleanup jobs. Anchored roots prevent filesystem escape, but this is not a
transactional snapshot or a defense against hostile concurrent mutations, bind
mounts, or device-file replacement. New outputs use directory mode `0700` and file
mode `0600`. Native macOS and Linux are the intended platforms; do not assume
atomic replacement guarantees on other filesystems/platforms.

Exit codes: `0` success, `1` fatal configuration/I/O error, `2` invalid CLI syntax,
`3` published partial results with errors, `130` cancellation. Warnings alone do not
change the exit code. Use `scan --help` for all options.

## Analysis and development

Start with [the orchestration prompt](prompts/00-orchestrator.md), then use
[per-project analysis](prompts/10-project-analysis.md) and
[catalog synthesis](prompts/20-catalog-synthesis.md). These prompts request
evidence-linked architecture facts and a master dependency graph. They are text
templates for your chosen AI workflow; the CLI does not submit or execute them.

See [architecture](docs/architecture.md) for package responsibilities and
[the inventory schema](docs/catalog-inventory.schema.json) for the output contract.

```sh
make check               # vet, race-enabled tests, formatting check
make build VERSION=0.1.0
go test ./internal/manifest -fuzz=FuzzElixir -fuzztime=10s
```
