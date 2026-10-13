package screen

import (
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/catalog"
)

// Method selects how each step's candidate pairs are found. All methods
// return exactly the same conjunctions; they differ only in how many pairs
// they have to look at.
type Method int

const (
	// Brute tests every pair: N²/2 per step.
	Brute Method = iota
	// Altitude sorts objects by distance from Earth's centre each step and
	// only tests pairs whose radii differ by less than the coarse bound
	// (two objects can't be closer than their difference in radius).
	Altitude
	// Grid buckets objects into cubes the size of the coarse bound and only
	// tests pairs in the same or neighbouring cubes.
	Grid
)

func (m Method) String() string {
	return [...]string{"brute", "altitude", "grid"}[m]
}

// BruteForce is Screen with the Brute method.
func BruteForce(objs []*catalog.Object, cfg Config) ([]Conjunction, Stats) {
	return Screen(objs, cfg, Brute)
}

// blockSize is how many steps a worker takes at a time: about four blocks
// per worker, between 30 and 360 steps. Blocks are handed out in order, so
// a worker that finishes early just takes the next one, which keeps every
// core busy to the end. Each block costs one extra sample, so blocks
// shouldn't be tiny.
func blockSize(steps, workers int) int {
	return max(30, min(360, steps/(4*workers)))
}

// Screen runs a screen with the given method on cfg.Workers goroutines.
func Screen(objs []*catalog.Object, cfg Config, m Method) ([]Conjunction, Stats) {
	start := time.Now()
	r := newRun(objs, cfg)
	workers := max(cfg.Workers, 1)
	bs := blockSize(r.steps, workers)
	nBlocks := (r.steps + bs - 1) / bs

	var next atomic.Int64
	founds := make([][]Conjunction, workers)
	stats := make([]Stats, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			f := newFinder(m, len(objs))
			for {
				b := int(next.Add(1)) - 1
				if b >= nBlocks {
					return
				}
				k0 := b * bs
				r.screenBlock(f, k0, min(k0+bs, r.steps), &founds[w], &stats[w])
			}
		})
	}
	wg.Wait()

	var st Stats
	var found []Conjunction
	for w := range workers {
		found = append(found, founds[w]...)
		st.Propagated += stats[w].Propagated
		st.PairChecks += stats[w].PairChecks
		st.Candidates += stats[w].Candidates
		st.Refined += stats[w].Refined
	}
	st.Steps = r.steps
	st.Elapsed = time.Since(start)
	return merge(found, cfg.Step, cfg.Threshold, cfg.Start, r.end()), st
}

// screenBlock screens intervals k0..k1-1. Each block samples its own first
// instant, so blocks are independent and can run on any goroutine.
func (r *run) screenBlock(f finder, k0, k1 int, found *[]Conjunction, st *Stats) {
	n := len(r.objs)
	prev, cur := newStates(n), newStates(n)
	live := make([]bool, n)
	r.sample(k0, cur)
	st.Propagated += int64(n)
	for k := k0; k < k1; k++ {
		// Interval k runs from sample k (prev) to sample k+1 (cur).
		prev, cur = cur, prev
		r.sample(k+1, cur)
		st.Propagated += int64(n)
		b := newBounds(r.cfg.Threshold, liveSpeed(prev, cur, live, r.dt), r.dt)
		st.PairChecks += f.pairs(r, prev, live, b, func(i, j int) {
			st.Candidates++
			if !passesChord(prev, cur, i, j, b) {
				return
			}
			st.Refined++
			if c, ok := r.refine(i, j, k); ok {
				*found = append(*found, c)
			}
		})
	}
}

// A finder calls visit(i, j), with i < j, for every pair of live,
// screenable objects within b.Coarse of each other in s, and returns how
// many pairs it tested to find them.
type finder interface {
	pairs(r *run, s *states, live []bool, b Bounds, visit func(i, j int)) int64
}

func newFinder(m Method, n int) finder {
	switch m {
	case Altitude:
		return &altitudeFinder{rad: make([]float64, n)}
	case Grid:
		return newGridFinder(n)
	}
	return bruteFinder{}
}

// ---- brute force ----

type bruteFinder struct{}

func (bruteFinder) pairs(r *run, s *states, live []bool, b Bounds, visit func(i, j int)) int64 {
	var checks int64
	x, y, z, n := s.x, s.y, s.z, len(live)
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
			if dx*dx+dy*dy+dz*dz <= b.CoarseSq {
				visit(i, j)
			}
		}
	}
	return checks
}

// ---- altitude filter ----

// altitudeFinder is a sweep over objects sorted by radius: |rᵢ − rⱼ| is a
// lower bound on their distance, so once the next object in radius order is
// more than b.Coarse further out, no later one can be close either.
type altitudeFinder struct {
	rad   []float64
	order []int32
}

func (f *altitudeFinder) pairs(r *run, s *states, live []bool, b Bounds, visit func(i, j int)) int64 {
	f.order = f.order[:0]
	for i := range live {
		if live[i] {
			f.rad[i] = math.Sqrt(s.x[i]*s.x[i] + s.y[i]*s.y[i] + s.z[i]*s.z[i])
			f.order = append(f.order, int32(i))
		}
	}
	slices.SortFunc(f.order, func(a, c int32) int {
		if f.rad[a] < f.rad[c] {
			return -1
		}
		return 1
	})
	var checks int64
	for a, ia := range f.order {
		i := int(ia)
		for _, jb := range f.order[a+1:] {
			j := int(jb)
			if f.rad[j]-f.rad[i] > b.Coarse {
				break
			}
			if r.skipPair(i, j) {
				continue
			}
			checks++
			dx, dy, dz := s.x[j]-s.x[i], s.y[j]-s.y[i], s.z[j]-s.z[i]
			if dx*dx+dy*dy+dz*dz <= b.CoarseSq {
				visit(min(i, j), max(i, j))
			}
		}
	}
	return checks
}

// ---- spatial grid ----

// gridFinder hashes each object's cube (side b.Coarse) into a fixed table
// and, for each object, tests the objects in the 27 cubes around it. Two
// objects within b.Coarse of each other are always in the same or
// neighbouring cubes. Unrelated cubes that share a hash bucket only cost a
// few extra distance checks, never a missed pair.
type gridFinder struct {
	mask        uint32
	cx, cy, cz  []int32
	bucketOf    []uint32
	start, fill []int32 // counting sort: bucket b holds items[start[b]:start[b+1]]
	items       []int32
}

func newGridFinder(n int) *gridFinder {
	size := 1
	for size < 4*n {
		size <<= 1
	}
	return &gridFinder{
		mask: uint32(size - 1),
		cx:   make([]int32, n), cy: make([]int32, n), cz: make([]int32, n),
		bucketOf: make([]uint32, n),
		start:    make([]int32, size+1), fill: make([]int32, size),
		items: make([]int32, 0, n),
	}
}

func (g *gridFinder) hash(x, y, z int32) uint32 {
	return (uint32(x)*73856093 ^ uint32(y)*19349663 ^ uint32(z)*83492791) & g.mask
}

func (g *gridFinder) pairs(r *run, s *states, live []bool, b Bounds, visit func(i, j int)) int64 {
	// Bucket every live object (counting sort, no allocation).
	clear(g.start)
	inv := 1 / b.Coarse
	for i := range live {
		if !live[i] {
			continue
		}
		g.cx[i] = int32(math.Floor(s.x[i] * inv))
		g.cy[i] = int32(math.Floor(s.y[i] * inv))
		g.cz[i] = int32(math.Floor(s.z[i] * inv))
		h := g.hash(g.cx[i], g.cy[i], g.cz[i])
		g.bucketOf[i] = h
		g.start[h+1]++
	}
	for k := 1; k < len(g.start); k++ {
		g.start[k] += g.start[k-1]
	}
	copy(g.fill, g.start[:len(g.fill)])
	g.items = g.items[:g.start[len(g.start)-1]]
	for i := range live {
		if live[i] {
			h := g.bucketOf[i]
			g.items[g.fill[h]] = int32(i)
			g.fill[h]++
		}
	}

	var checks int64
	var near [27]uint32
	for i := range live {
		if !live[i] {
			continue
		}
		// The 27 neighbouring buckets, without duplicates (two cubes can
		// hash to the same bucket; scanning it twice would test pairs twice).
		nb := 0
		for dx := int32(-1); dx <= 1; dx++ {
			for dy := int32(-1); dy <= 1; dy++ {
				for dz := int32(-1); dz <= 1; dz++ {
					h := g.hash(g.cx[i]+dx, g.cy[i]+dy, g.cz[i]+dz)
					if !slices.Contains(near[:nb], h) {
						near[nb] = h
						nb++
					}
				}
			}
		}
		xi, yi, zi := s.x[i], s.y[i], s.z[i]
		for _, h := range near[:nb] {
			for _, jj := range g.items[g.start[h]:g.start[h+1]] {
				j := int(jj)
				if j <= i || r.skipPair(i, j) {
					continue
				}
				checks++
				dx, dy, dz := s.x[j]-xi, s.y[j]-yi, s.z[j]-zi
				if dx*dx+dy*dy+dz*dz <= b.CoarseSq {
					visit(i, j)
				}
			}
		}
	}
	return checks
}
