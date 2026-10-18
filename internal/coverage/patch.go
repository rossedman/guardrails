package coverage

import "sort"

// LineSet is a set of 1-based line numbers.
type LineSet map[int]bool

// PatchResult is coverage restricted to changed lines.
type PatchResult struct {
	Stats
	// Uncovered maps local file path to the changed lines that hold
	// statements no test executed.
	Uncovered map[string][]int
}

// Patch computes coverage of changed lines. changed maps local file paths
// to the lines added or modified; resolve maps profile files to local paths.
// A changed line counts once, as covered if any block spanning it ran.
// Lines outside every block (comments, declarations) are ignored.
func (p *Profile) Patch(changed map[string]LineSet, resolve Resolver) PatchResult {
	type mark struct{ covered bool }
	marks := map[string]map[int]*mark{}
	for _, b := range p.Blocks {
		local, ok := resolve(b.File)
		if !ok {
			continue
		}
		lines := changed[local]
		if len(lines) == 0 || b.NumStmt == 0 {
			continue
		}
		for l := b.StartLine; l <= b.EndLine; l++ {
			if !lines[l] {
				continue
			}
			if marks[local] == nil {
				marks[local] = map[int]*mark{}
			}
			m := marks[local][l]
			if m == nil {
				m = &mark{}
				marks[local][l] = m
			}
			m.covered = m.covered || b.Covered()
		}
	}
	res := PatchResult{Uncovered: map[string][]int{}}
	for file, lines := range marks {
		for l, m := range lines {
			res.Total++
			if m.covered {
				res.Covered++
			} else {
				res.Uncovered[file] = append(res.Uncovered[file], l)
			}
		}
	}
	for file := range res.Uncovered {
		sort.Ints(res.Uncovered[file])
	}
	return res
}
