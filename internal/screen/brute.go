package screen

import (
	"time"

	"github.com/supremeinferno/orbitscreen/internal/catalog"
)

// BruteForce screens by testing every pair of objects at every step, on
// one goroutine. It is the baseline: O(N²) pair tests per step, no spatial
// indexing, no parallelism. Faster screeners must find exactly the same
// conjunctions.
func BruteForce(objs []*catalog.Object, cfg Config) ([]Conjunction, Stats) {
	start := time.Now()
	r := newRun(objs, cfg)
	n := len(objs)
	prev, cur := newStates(n), newStates(n)
	live := make([]bool, n)
	var st Stats
	var found []Conjunction

	r.sample(0, cur)
	st.Propagated += int64(n)
	for k := 0; k < r.steps; k++ {
		// Interval k runs from sample k (prev) to sample k+1 (cur).
		prev, cur = cur, prev
		r.sample(k+1, cur)
		st.Propagated += int64(n)
		b := newBounds(cfg.Threshold, liveSpeed(prev, cur, live, r.dt), r.dt)

		var checks, cands int64
		x, y, z := prev.x, prev.y, prev.z
		for i := 0; i < n; i++ {
			if !live[i] {
				continue
			}
			xi, yi, zi := x[i], y[i], z[i]
			for j := i + 1; j < n; j++ {
				if !live[j] || r.skipPair(i, j) {
					continue
				}
				checks++
				dx, dy, dz := x[j]-xi, y[j]-yi, z[j]-zi
				if dx*dx+dy*dy+dz*dz > b.CoarseSq {
					continue
				}
				cands++
				if !passesChord(prev, cur, i, j, b) {
					continue
				}
				st.Refined++
				if c, ok := r.refine(i, j, k); ok {
					found = append(found, c)
				}
			}
		}
		st.PairChecks += checks
		st.Candidates += cands
	}
	st.Steps = r.steps
	st.Elapsed = time.Since(start)
	return merge(found, cfg.Step, cfg.Threshold, cfg.Start, r.end()), st
}
