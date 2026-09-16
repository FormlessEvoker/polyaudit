// Package discover locates manifest-bearing directories without entering artifacts.
package discover

import (
	"context"
	"fmt"
	"os"
	"path"
	"sort"
	"sync"

	"github.com/FormlessEvoker/polyaudit/internal/fsutil"
	"github.com/FormlessEvoker/polyaudit/internal/model"
	"github.com/FormlessEvoker/polyaudit/internal/policy"
)

type Candidate struct {
	Path       string
	Signatures []string
}
type result struct {
	candidate Candidate
	dirs      []string
	err       error
}

// Scan uses a coordinator-owned queue: workers never enqueue into their own
// bounded input channel, avoiding deadlock on wide directory trees.
func Scan(ctx context.Context, root *os.Root, rules policy.Policy, workers int) ([]Candidate, []model.Diagnostic, error) {
	if workers < 1 {
		return nil, nil, fmt.Errorf("discovery needs at least one worker")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan string)
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case dir, ok := <-jobs:
					if !ok {
						return
					}
					r := inspect(root, rules, dir)
					select {
					case results <- r:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}
	defer func() { cancel(); close(jobs); wg.Wait() }()
	queue := []string{"."}
	active := 0
	var candidates []Candidate
	var diagnostics []model.Diagnostic
	for len(queue) > 0 || active > 0 {
		var send chan string
		var next string
		if len(queue) > 0 {
			send = jobs
			next = queue[0]
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case send <- next:
			queue[0] = ""
			queue = queue[1:]
			active++
		case r := <-results:
			active--
			if r.err != nil {
				diagnostics = append(diagnostics, model.Error(r.candidate.Path, r.err.Error()))
				continue
			}
			if len(r.candidate.Signatures) > 0 {
				candidates = append(candidates, r.candidate)
			}
			queue = append(queue, r.dirs...)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
	sort.Slice(diagnostics, func(i, j int) bool { return diagnostics[i].Path < diagnostics[j].Path })
	return candidates, diagnostics, nil
}

func inspect(root *os.Root, rules policy.Policy, dir string) result {
	r := result{candidate: Candidate{Path: dir}}
	entries, err := fsutil.ReadDir(root, dir)
	if err != nil {
		r.err = err
		return r
	}
	for _, entry := range entries {
		rel := path.Join(dir, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 || rules.Excluded(rel) {
			continue
		}
		if entry.IsDir() {
			if !rules.SkipDir(rel) {
				r.dirs = append(r.dirs, rel)
			}
		} else if entry.Type().IsRegular() && policy.Signatures[entry.Name()] {
			r.candidate.Signatures = append(r.candidate.Signatures, entry.Name())
		}
	}
	return r
}
