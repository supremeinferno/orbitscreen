package socrates

import (
	"math"
	"sort"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/catalog"
	"github.com/supremeinferno/orbitscreen/internal/screen"
)

// Match pairs one SOCRATES conjunction with ours, if we found it.
type Match struct {
	Soc  Conjunction
	Ours *screen.Conjunction // nil if we have no conjunction of the same pair near its TCA
}

// Compare matches every SOCRATES conjunction to the conjunction of ours
// with the same two objects whose TCA is closest, within tol. Each of ours
// is used at most once. It returns one Match per SOCRATES conjunction, and
// ours that matched nothing.
func Compare(soc []Conjunction, ours []screen.Conjunction, tol time.Duration) (matches []Match, extra []screen.Conjunction) {
	type key [2]int
	pair := func(a, b int) key {
		if a > b {
			a, b = b, a
		}
		return key{a, b}
	}
	byPair := map[key][]int{} // indexes into ours
	for i, c := range ours {
		k := pair(c.A.ID, c.B.ID)
		byPair[k] = append(byPair[k], i)
	}
	used := make([]bool, len(ours))

	// Closest TCA first, so a near-perfect match isn't stolen by a looser one.
	type cand struct {
		s, o int
		dt   time.Duration
	}
	var cands []cand
	for si, c := range soc {
		for _, oi := range byPair[pair(c.ID1, c.ID2)] {
			if dt := ours[oi].TCA.Sub(c.TCA).Abs(); dt <= tol {
				cands = append(cands, cand{si, oi, dt})
			}
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].dt < cands[j].dt })
	matches = make([]Match, len(soc))
	for i, c := range soc {
		matches[i].Soc = c
	}
	for _, c := range cands {
		if matches[c.s].Ours != nil || used[c.o] {
			continue
		}
		matches[c.s].Ours = &ours[c.o]
		used[c.o] = true
	}
	for i, c := range ours {
		if !used[i] {
			extra = append(extra, c)
		}
	}
	return matches, extra
}

// ClosestApproach finds the minimum distance between a and b within ±window
// of around, by sampling every 50 ms and then every 1 ms near the best
// sample. It's for diagnosing misses, so it's simple rather than fast. ok
// is false if either object fails to propagate.
func ClosestApproach(a, b *catalog.Object, around time.Time, window time.Duration) (tca time.Time, km float64, ok bool) {
	dist := func(t time.Time) float64 {
		pa, _, err1 := a.Sat.PropagateTo(t)
		pb, _, err2 := b.Sat.PropagateTo(t)
		if err1 != nil || err2 != nil {
			return math.NaN()
		}
		dx, dy, dz := pa[0]-pb[0], pa[1]-pb[1], pa[2]-pb[2]
		return math.Sqrt(dx*dx + dy*dy + dz*dz)
	}
	km = math.Inf(1)
	for _, step := range []time.Duration{50 * time.Millisecond, time.Millisecond} {
		from, to := around.Add(-window), around.Add(window)
		if step == time.Millisecond {
			from, to = tca.Add(-50*time.Millisecond), tca.Add(50*time.Millisecond)
		}
		for t := from; !t.After(to); t = t.Add(step) {
			if d := dist(t); d < km {
				tca, km = t, d
			}
		}
	}
	return tca, km, !math.IsInf(km, 1)
}
