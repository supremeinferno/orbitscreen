// Package screen finds close approaches between catalogued objects.
//
// The method, shared by every screener in this package:
//
//  1. Sample: propagate every object at t0, t0+Δt, t0+2Δt, …
//  2. For each pair and each interval [t, t+Δt], use the positions at
//     both ends to prove, cheaply, that the pair can't get within the
//     threshold during the interval (see Bounds). Almost every pair is
//     ruled out here.
//  3. Refine the few remaining candidates with a golden-section search on
//     the true SGP4 distance, to find the time of closest approach (TCA)
//     and the miss distance.
//  4. Merge detections of the same encounter from neighbouring intervals.
//
// The screeners differ only in how they find the pairs to test in step 2:
// BruteForce tests every pair.
package screen

import (
	"math"
	"sort"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/catalog"
)

// Config describes one screening run.
type Config struct {
	Start     time.Time
	Duration  time.Duration // e.g. 7 days
	Step      time.Duration // sampling interval Δt; 10 s is a good default
	Threshold float64       // report approaches closer than this, km (SOCRATES: 5)

	// PrimaryOnly restricts screening to pairs with at least one Primary
	// object (SOCRATES screens active payloads against everything).
	PrimaryOnly bool

	// MaxSteps, if > 0, stops after this many intervals. Used to time a
	// sample of a run that would take hours.
	MaxSteps int

	// Workers is how many goroutines share the work; 0 or 1 means one.
	Workers int
}

// Conjunction is one close approach.
type Conjunction struct {
	A, B     *catalog.Object // A.ID < B.ID
	TCA      time.Time       // time of closest approach
	MissKm   float64         // distance at TCA
	RelSpeed float64         // relative speed at TCA, km/s

	// edge is set while screening when the closest point found in an
	// interval is one of its ends: the distance was still falling there,
	// so the real minimum is in the neighbouring interval.
	edge bool
}

// Stats counts the work done, for comparing screeners.
type Stats struct {
	Steps      int   // intervals screened
	Propagated int64 // SGP4 calls for sampling
	PairChecks int64 // pairs tested with the coarse distance bound
	Candidates int64 // pairs that passed the coarse bound
	Refined    int64 // pairs that also passed the chord bound and were refined
	Elapsed    time.Duration
}

// aObj bounds how sharply any object's SGP4 path can curve: |r”(t)| in
// km/s². Physically that's gravity, at most ~9.83 m/s² at Earth's surface
// (SGP4 stops at the surface with a decay error). Measured over the whole
// live catalog for 7 days, the worst case is 9.80 m/s², for a Starlink
// minutes from re-entry. See docs/05-screening.md.
const aObj = 0.0100

// Bounds holds the per-step distances used to rule pairs out.
//
// The screeners only use sampled positions, never SGP4's velocity output:
// SGP4 computes velocity with a separate, truncated formula, and it can
// differ from the true rate of change of its own position by tens of m/s
// (measured: 52 m/s for an e = 0.93 orbit, 45 m/s for a re-entering
// Starlink). A bound built on it is not safe.
//
// Instead, for an interval of length Δt with relative positions d0 at its
// start and d1 at its end, the true relative path stays within
//
//	pad = 2·aObj·Δt²/8
//
// of the straight chord from d0 to d1 (the standard error bound for linear
// interpolation of a curve whose second derivative is at most 2·aObj).
// So a pair can only come within the threshold during the interval if
//
//	min over s in [0,1] of |d0 + (d1−d0)·s|  ≤  threshold + pad        (chord bound)
//
// and, since every object moves at most vMax·Δt per step, |d1−d0| ≤ 2·vMax·Δt,
// only if
//
//	|d0| ≤ threshold + 2·vMax·Δt + pad                                 (coarse bound)
//
// Both bounds are conservative: they can let through pairs that won't
// come close, but never rule out a pair that will.
type Bounds struct {
	Coarse   float64 // km
	CoarseSq float64
	Chord    float64 // km
}

func newBounds(threshold, vMax, dt float64) Bounds {
	pad := 2 * aObj * dt * dt / 8
	c := threshold + 2*vMax*dt + pad
	return Bounds{Coarse: c, CoarseSq: c * c, Chord: threshold + pad}
}

// states holds every object's position at one instant, as separate arrays
// (struct of arrays) so the pair loop streams through memory.
type states struct {
	x, y, z []float64
	ok      []bool // false if SGP4 failed (e.g. decayed)
}

func newStates(n int) *states {
	return &states{x: make([]float64, n), y: make([]float64, n), z: make([]float64, n), ok: make([]bool, n)}
}

// run holds what every screener needs during a run.
type run struct {
	objs    []*catalog.Object
	cfg     Config
	dt      float64   // step, seconds
	base    []float64 // per object: minutes from its epoch to cfg.Start
	steps   int
	primary []bool
	group   []int
}

func newRun(objs []*catalog.Object, cfg Config) *run {
	r := &run{objs: objs, cfg: cfg, dt: cfg.Step.Seconds()}
	r.steps = int(math.Ceil(cfg.Duration.Seconds() / r.dt))
	if cfg.MaxSteps > 0 && cfg.MaxSteps < r.steps {
		r.steps = cfg.MaxSteps
	}
	r.base = make([]float64, len(objs))
	r.primary = make([]bool, len(objs))
	r.group = make([]int, len(objs))
	for i, o := range objs {
		r.base[i] = o.Sat.MinutesSinceEpoch(cfg.Start)
		r.primary[i] = o.Primary
		r.group[i] = o.Group
	}
	return r
}

// sample fills s with every object's position at time k·Δt.
func (r *run) sample(k int, s *states) {
	tmin := float64(k) * r.dt / 60
	for i, o := range r.objs {
		p, _, err := o.Sat.Propagate(r.base[i] + tmin)
		s.ok[i] = err == nil
		s.x[i], s.y[i], s.z[i] = p[0], p[1], p[2]
	}
}

// liveSpeed marks in live the objects that propagated at both ends of the
// interval from prev to cur, and returns the largest distance any of them
// moved divided by dt: the speed used in the coarse bound.
func liveSpeed(prev, cur *states, live []bool, dt float64) float64 {
	var m float64
	for i := range live {
		live[i] = prev.ok[i] && cur.ok[i]
		if !live[i] {
			continue
		}
		dx, dy, dz := cur.x[i]-prev.x[i], cur.y[i]-prev.y[i], cur.z[i]-prev.z[i]
		m = math.Max(m, dx*dx+dy*dy+dz*dz)
	}
	return math.Sqrt(m) / dt
}

// chordMin returns the minimum of |d0 + (d1−d0)·s| over s in [0, 1].
func chordMin(d0x, d0y, d0z, d1x, d1y, d1z float64) float64 {
	wx, wy, wz := d1x-d0x, d1y-d0y, d1z-d0z
	ww := wx*wx + wy*wy + wz*wz
	s := 0.0
	if ww > 0 {
		s = -(d0x*wx + d0y*wy + d0z*wz) / ww
		s = math.Max(0, math.Min(1, s))
	}
	ex, ey, ez := d0x+wx*s, d0y+wy*s, d0z+wz*s
	return math.Sqrt(ex*ex + ey*ey + ez*ez)
}

// skipPair reports whether the pair isn't screened at all: attached to
// each other, or (in primary-only mode) neither is a primary.
func (r *run) skipPair(i, j int) bool {
	return (r.group[i] != 0 && r.group[i] == r.group[j]) ||
		(r.cfg.PrimaryOnly && !r.primary[i] && !r.primary[j])
}

// end is the end of the screened window.
func (r *run) end() time.Time {
	return r.cfg.Start.Add(time.Duration(float64(r.steps) * r.dt * float64(time.Second)))
}

// passesChord applies the chord bound to pair (i, j) over the interval
// from prev to cur.
func passesChord(prev, cur *states, i, j int, b Bounds) bool {
	return chordMin(
		prev.x[j]-prev.x[i], prev.y[j]-prev.y[i], prev.z[j]-prev.z[i],
		cur.x[j]-cur.x[i], cur.y[j]-cur.y[i], cur.z[j]-cur.z[i]) <= b.Chord
}

// refine finds the closest approach of objects i and j within interval k
// by golden-section search on the true SGP4 distance. ok is false if
// either object fails to propagate.
func (r *run) refine(i, j, k int) (c Conjunction, ok bool) {
	const invPhi = 0.6180339887498949
	t0 := float64(k) * r.dt // seconds from cfg.Start
	si, sj := r.objs[i].Sat, r.objs[j].Sat
	dist := func(tau float64) (float64, bool) {
		m := (t0 + tau) / 60
		pi, _, err1 := si.Propagate(r.base[i] + m)
		pj, _, err2 := sj.Propagate(r.base[j] + m)
		if err1 != nil || err2 != nil {
			return 0, false
		}
		dx, dy, dz := pj[0]-pi[0], pj[1]-pi[1], pj[2]-pi[2]
		return math.Sqrt(dx*dx + dy*dy + dz*dz), true
	}

	// 25 iterations shrink a 10 s interval to ~0.1 ms. Relative speeds are
	// at most ~15 km/s, so the TCA is good to ~1.5 m along track and the
	// miss distance (which is flat at the minimum) far better.
	lo, hi := 0.0, r.dt
	a := hi - invPhi*(hi-lo)
	b := lo + invPhi*(hi-lo)
	fa, ok1 := dist(a)
	fb, ok2 := dist(b)
	if !ok1 || !ok2 {
		return c, false
	}
	for it := 0; it < 25; it++ {
		if fa < fb {
			hi, b, fb = b, a, fa
			a = hi - invPhi*(hi-lo)
			if fa, ok1 = dist(a); !ok1 {
				return c, false
			}
		} else {
			lo, a, fa = a, b, fb
			b = lo + invPhi*(hi-lo)
			if fb, ok2 = dist(b); !ok2 {
				return c, false
			}
		}
	}
	tau := (lo + hi) / 2
	// The minimum of a 10 s window can sit on an edge (the encounter's
	// true TCA is in the neighbouring interval); check the edges too.
	best, bestTau := math.Inf(1), tau
	for _, t := range []float64{0, tau, r.dt} {
		if d, ok := dist(t); ok && d < best {
			best, bestTau = d, t
		}
	}

	m := (t0 + bestTau) / 60
	_, vi, _ := si.Propagate(r.base[i] + m)
	_, vj, _ := sj.Propagate(r.base[j] + m)
	dv := [3]float64{vj[0] - vi[0], vj[1] - vi[1], vj[2] - vi[2]}
	a1, b1 := r.objs[i], r.objs[j]
	if a1.ID > b1.ID {
		a1, b1 = b1, a1
	}
	return Conjunction{
		A: a1, B: b1,
		TCA:      r.cfg.Start.Add(time.Duration((t0 + bestTau) * float64(time.Second))),
		MissKm:   best,
		RelSpeed: math.Sqrt(dv[0]*dv[0] + dv[1]*dv[1] + dv[2]*dv[2]),
		edge:     bestTau == 0 || bestTau == r.dt,
	}, true
}

// merge turns detections into conjunctions: one per local minimum of the
// distance closer than the threshold, which is what SOCRATES (STK/CAT)
// reports. A slow pair can stay within the threshold for hours and pass
// through several minima; each is its own conjunction.
//
// The same minimum can be detected twice: from the interval that contains
// it, and from a neighbour whose closest point is the shared end (an edge
// detection, where the distance was still falling). Edge detections are
// dropped when a detection of the same pair within one step is at least as
// close; detections of the same pair less than 1 ms apart are one. Finally,
// minima at the very start or end of the window are dropped: the pair was
// closer before (or will be after) the window, so that's not a real TCA.
func merge(found []Conjunction, step time.Duration, threshold float64, start, end time.Time) []Conjunction {
	near := found[:0]
	for _, c := range found {
		if c.MissKm < threshold {
			near = append(near, c)
		}
	}
	found = near
	sort.Slice(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.A.ID != b.A.ID {
			return a.A.ID < b.A.ID
		}
		if a.B.ID != b.B.ID {
			return a.B.ID < b.B.ID
		}
		return a.TCA.Before(b.TCA)
	})
	samePair := func(i, j int) bool {
		return j >= 0 && j < len(found) && found[i].A == found[j].A && found[i].B == found[j].B
	}
	var out []Conjunction
	for i, c := range found {
		if c.edge {
			superseded := false
			for _, j := range []int{i - 1, i + 1} {
				if samePair(i, j) && found[j].TCA.Sub(c.TCA).Abs() <= step+time.Millisecond && found[j].MissKm <= c.MissKm && (found[j].MissKm < c.MissKm || j < i || !found[j].edge) {
					superseded = true
				}
			}
			if superseded {
				continue
			}
		}
		if n := len(out); n > 0 && out[n-1].A == c.A && out[n-1].B == c.B && c.TCA.Sub(out[n-1].TCA) < time.Millisecond {
			if c.MissKm < out[n-1].MissKm {
				out[n-1] = c
			}
			continue
		}
		out = append(out, c)
	}
	kept := out[:0]
	for _, c := range out {
		if c.TCA.Sub(start).Abs() > time.Millisecond && c.TCA.Sub(end).Abs() > time.Millisecond {
			c.edge = false
			kept = append(kept, c)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].TCA.Before(kept[j].TCA) })
	return kept
}
