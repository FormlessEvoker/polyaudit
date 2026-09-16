// Package cli implements the command line without process-global flag state.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"polyaudit/internal/catalog"
)

type excludes []string

func (e *excludes) String() string     { return strings.Join(*e, ",") }
func (e *excludes) Set(s string) error { *e = append(*e, s); return nil }

const usage = `polyaudit — prepare polyglot repositories for architectural analysis

Usage:
  polyaudit scan [ROOT] [options]
  polyaudit version

ROOT defaults to the current directory. Flags may precede or follow ROOT.
Build folders are excluded from packs; deletion requires --clean.
--dry-run writes nothing and prints the inventory and cleanup plan as JSON.

Options:
`

func Run(ctx context.Context, args []string, stdout, stderr io.Writer, version string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Fprintln(stdout, version)
		return 0
	}
	if args[0] != "scan" {
		fmt.Fprintf(stderr, "unknown command %q; use polyaudit scan --help\n", args[0])
		return 2
	}
	opts := catalog.Defaults()
	opts.Version = version
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage); fs.PrintDefaults() }
	fs.StringVar(&opts.Output, "output", "", "output directory (default ROOT/_ai_audit_ready)")
	fs.IntVar(&opts.Workers, "workers", opts.Workers, "concurrent workers (1–256)")
	fs.BoolVar(&opts.Clean, "clean", false, "delete artifact directories inside detected projects")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "preview metadata and cleanup; do not write or delete anything")
	fs.BoolVar(&opts.Gzip, "gzip", false, "compress individual packs with gzip")
	fs.Int64Var(&opts.MaxFileBytes, "max-file-bytes", opts.MaxFileBytes, "maximum bytes read per source file")
	fs.Int64Var(&opts.MaxBundleBytes, "max-bundle-bytes", opts.MaxBundleBytes, "maximum uncompressed bytes per pack, including framing")
	var omit excludes
	fs.Var(&omit, "exclude", "exclude a catalog-relative path and its descendants (repeatable)")
	var asJSON bool
	fs.BoolVar(&asJSON, "json", false, "print the inventory as JSON instead of a summary")
	reordered, err := interspersed(fs, args[1:])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err = fs.Parse(reordered); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "scan accepts at most one ROOT")
		return 2
	}
	if fs.NArg() == 1 {
		opts.Root = fs.Arg(0)
	}
	opts.Excludes = omit
	inv, err := catalog.Run(ctx, opts)
	if err != nil {
		fmt.Fprintln(stderr, "polyaudit:", err)
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return 1
	}
	if opts.DryRun || asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err = encoder.Encode(inv); err != nil {
			fmt.Fprintln(stderr, "write output:", err)
			return 1
		}
	} else {
		fmt.Fprintln(stdout, catalog.Summary(inv))
		fmt.Fprintln(stdout, "Inventory:", inv.OutputDirectory+"/catalog_inventory.json")
	}
	if inv.HasErrors() {
		fmt.Fprintln(stderr, "Scan completed with errors; inspect diagnostics in the inventory.")
		return 3
	}
	return 0
}

// flag.FlagSet normally stops at ROOT. Move positional arguments to the end,
// honoring values, boolean flags, and the standard -- terminator.
func interspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var options, positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			positionals = append(positionals, a)
			continue
		}
		options = append(options, a)
		name := strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue
		}
		if i+1 >= len(args) {
			return nil, fmt.Errorf("flag needs an argument: %s", a)
		}
		i++
		options = append(options, args[i])
	}
	if len(positionals) > 0 {
		options = append(options, "--")
		options = append(options, positionals...)
	}
	return options, nil
}
