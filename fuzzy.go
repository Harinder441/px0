package main

import (
	"px0/internal/fuzzy"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// FuzzyResult represents a matched file path ranked by the fuzzy search engine.
// Re-exported from internal/fuzzy.Result.
type FuzzyResult = fuzzy.Result

// isBoundary reports whether a byte acts as a word or segment boundary.
func isBoundary(b byte) bool {
	return fuzzy.IsBoundary(b)
}

// fuzzyScore delegates to internal/fuzzy.Score.
func fuzzyScore(q, origQ string, e *FileEntry, pos []int) (int, []int, bool) {
	return fuzzy.Score(q, origQ, e.Path, e.lower, e.nameStart, pos)
}

func worseFuzzy(a, b FuzzyResult) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	return a.Path > b.Path
}

func siftDownFuzzy(h []FuzzyResult, i int) {
	n := len(h)
	for {
		left := 2*i + 1
		if left >= n {
			break
		}
		smallest := left
		if right := left + 1; right < n && worseFuzzy(h[right], h[left]) {
			smallest = right
		}
		if !worseFuzzy(h[smallest], h[i]) {
			break
		}
		h[i], h[smallest] = h[smallest], h[i]
		i = smallest
	}
}

// FuzzyFind ranks every indexed path against query and returns the best limit.
func FuzzyFind(files []FileEntry, query string, limit int) []FuzzyResult {
	origQ := strings.ReplaceAll(strings.TrimSpace(query), " ", "")
	q := strings.ToLower(origQ)

	if q == "" {
		out := make([]FuzzyResult, 0, limit)
		for i := range files {
			if i == limit {
				break
			}
			out = append(out, FuzzyResult{Path: files[i].Path, Name: files[i].Name})
		}
		return out
	}

	workers := runtime.NumCPU()
	if len(files) < 1024 {
		workers = 1
	}
	chunk := (len(files) + workers - 1) / workers
	if chunk == 0 {
		chunk = 1
	}
	parts := make([][]FuzzyResult, workers)
	qLen := len(q)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		lo := w * chunk
		if lo >= len(files) {
			break
		}
		hi := min(lo+chunk, len(files))
		wg.Add(1)
		go func(w, lo, hi int) {
			defer wg.Done()
			capLimit := limit
			if capLimit <= 0 || capLimit > hi-lo {
				capLimit = min(64, hi-lo)
			}
			local := make([]FuzzyResult, 0, capLimit)
			var posSlab []int
			if limit > 0 {
				posSlab = make([]int, limit*qLen)
			}
			scratch := make([]int, 0, max(64, qLen))
			for i := lo; i < hi; i++ {
				s, pos, ok := fuzzy.Score(q, origQ, files[i].Path, files[i].lower, files[i].nameStart, scratch)
				if !ok {
					continue
				}
				if limit > 0 {
					if len(local) < limit {
						slot := len(local)
						cp := posSlab[slot*qLen : (slot+1)*qLen]
						copy(cp, pos)
						local = append(local, FuzzyResult{Path: files[i].Path, Name: files[i].Name, Pos: cp, Score: s})
						if len(local) == limit {
							for k := limit/2 - 1; k >= 0; k-- {
								siftDownFuzzy(local, k)
							}
						}
					} else if s > local[0].Score || (s == local[0].Score && files[i].Path < local[0].Path) {
						cp := local[0].Pos
						copy(cp, pos)
						local[0] = FuzzyResult{Path: files[i].Path, Name: files[i].Name, Pos: cp, Score: s}
						siftDownFuzzy(local, 0)
					}
					continue
				}
				cp := make([]int, len(pos))
				copy(cp, pos)
				local = append(local, FuzzyResult{Path: files[i].Path, Name: files[i].Name, Pos: cp, Score: s})
			}
			parts[w] = local
		}(w, lo, hi)
	}
	wg.Wait()

	total := 0
	for _, p := range parts {
		total += len(p)
	}
	all := make([]FuzzyResult, 0, total)
	for _, p := range parts {
		all = append(all, p...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].Path < all[j].Path
	})
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all
}
