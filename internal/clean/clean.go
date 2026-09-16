// Package clean removes only explicitly selected artifact directories beneath
// an anchored project root. It never follows directory symlinks.
package clean

import (
	"context"
	"os"
	"path"

	"github.com/FormlessEvoker/polyaudit/internal/fsutil"
	"github.com/FormlessEvoker/polyaudit/internal/model"
	"github.com/FormlessEvoker/polyaudit/internal/policy"
)

func Run(ctx context.Context, root *os.Root, project string, boundaries map[string]bool, rules policy.Policy, dryRun bool) ([]model.Cleanup, []model.Diagnostic) {
	var actions []model.Cleanup
	var diagnostics []model.Diagnostic
	if err := fsutil.NoSymlinks(root, project); err != nil {
		return nil, []model.Diagnostic{model.Error(project, err.Error())}
	}
	projectRoot, err := root.OpenRoot(project)
	if err != nil {
		return nil, []model.Diagnostic{model.Error(project, err.Error())}
	}
	defer projectRoot.Close()
	queue := []string{"."}
	for len(queue) > 0 {
		if ctx.Err() != nil {
			break
		}
		dir := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		entries, err := fsutil.ReadDir(projectRoot, dir)
		if err != nil {
			diagnostics = append(diagnostics, model.Error(path.Join(project, dir), err.Error()))
			continue
		}
		for _, e := range entries {
			if ctx.Err() != nil {
				break
			}
			if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
				continue
			}
			rel := path.Join(dir, e.Name())
			catalogPath := path.Join(project, rel)
			if boundaries[catalogPath] || rules.Excluded(catalogPath) {
				continue
			}
			if policy.BuildDirs[e.Name()] {
				a := model.Cleanup{Path: catalogPath, Status: "planned"}
				if rules.ProtectedSubtree(catalogPath) {
					a.Status = "skipped"
					a.Message = "contains an excluded path"
				} else if !dryRun {
					err = fsutil.NoSymlinks(projectRoot, rel)
					if err == nil {
						err = projectRoot.RemoveAll(rel)
					}
					if err != nil {
						a.Status = "failed"
						a.Message = err.Error()
						diagnostics = append(diagnostics, model.Error(catalogPath, "cleanup: "+err.Error()))
					} else {
						a.Status = "deleted"
					}
				}
				actions = append(actions, a)
				continue
			}
			if !rules.SkipDir(catalogPath) {
				queue = append(queue, rel)
			}
		}
	}
	return actions, diagnostics
}
