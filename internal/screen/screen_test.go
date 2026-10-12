package screen

import (
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/catalog"
	"github.com/supremeinferno/orbitscreen/internal/sgp4"
	"github.com/supremeinferno/orbitscreen/internal/tle"
)

var epoch = time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

func object(t testing.TB, id int, incl, raan, meanAnomaly float64) *catalog.Object {
	t.Helper()
	el := &tle.TLE{
		CatalogNum:   id,
		Epoch:        epoch,
		Inclination:  incl,
		RAAN:         raan,
		Eccentricity: 0.0001,
		MeanAnomaly:  meanAnomaly,
		MeanMotion:   15.2, // ~ 470 km altitude
		BStar:        0.0001,
	}
	sat, err := sgp4.New(el)
	if err != nil {
		t.Fatal(err)
	}
	return &catalog.Object{ID: id, Elems: el, Sat: sat}
}

// minima finds every local minimum below threshold of the true distance
// between a and b by brute force: sample every 50 ms to find each dip, then
// resample ±50 ms around it every 10 µs. Slow, but obviously correct.
func minima(a, b *catalog.Object, start time.Time, dur time.Duration, threshold float64) []Conjunction {
	dist := func(t float64) float64 {
		at := start.Add(time.Duration(t * float64(time.Second)))
		pa, _, _ := a.Sat.PropagateTo(at)
		pb, _, _ := b.Sat.PropagateTo(at)
		return math.Sqrt((pa[0]-pb[0])*(pa[0]-pb[0]) + (pa[1]-pb[1])*(pa[1]-pb[1]) + (pa[2]-pb[2])*(pa[2]-pb[2]))
	}
	const h = 0.05 // s
	var out []Conjunction
	prev, cur := dist(0), dist(h)
	for t := h; t < dur.Seconds(); t += h {
		next := dist(t + h)
		if cur < prev && cur <= next && cur < threshold {
			best, bestT := cur, t
			for u := t - h; u <= t+h; u += 1e-5 {
				if d := dist(u); d < best {
					best, bestT = d, u
				}
			}
			out = append(out, Conjunction{A: a, B: b, TCA: start.Add(time.Duration(bestT * float64(time.Second))), MissKm: best})
		}
		prev, cur = cur, next
	}
	return out
}

func TestBruteForceFindsSyntheticEncounters(t *testing.T) {
	// Pairs of circular orbits sharing an ascending node. The second object
	// of each pair trails by a tiny mean-anomaly offset, so every time they
	// cross the node line (twice per orbit) they pass a few hundred metres
	// apart.
	//   1,2: 1° apart in inclination -> slow encounter, ~0.13 km/s
	//   3,4: 27° apart                -> fast encounter, ~3.5 km/s
	//   6,7: prograde vs retrograde   -> very fast encounter, ~9 km/s
	// 5 is a bystander in a different plane.
	const lag = 0.004 // degrees of mean anomaly ≈ 0.5 km
	objs := []*catalog.Object{
		object(t, 1, 53, 100, 0),
		object(t, 2, 54, 100, -lag),
		object(t, 3, 53, 200, 0),
		object(t, 4, 80, 200, -lag),
		object(t, 5, 97, 300, 90),
		object(t, 6, 53, 40, 0),
		object(t, 7, 127, 40, -lag),
	}
	// Start 10 minutes before epoch so the first node crossing (at epoch)
	// is inside the window rather than on its edge.
	cfg := Config{Start: epoch.Add(-10 * time.Minute), Duration: 3 * time.Hour, Step: 10 * time.Second, Threshold: 5}
	got, st := BruteForce(objs, cfg)

	var want []Conjunction
	for i := 0; i < len(objs); i++ {
		for j := i + 1; j < len(objs); j++ {
			want = append(want, minima(objs[i], objs[j], cfg.Start, cfg.Duration, cfg.Threshold)...)
		}
	}
	if len(want) < 6 {
		t.Fatalf("test setup: only %d true encounters, want several per pair", len(want))
	}
	if len(got) != len(want) {
		t.Fatalf("found %d conjunctions, ground truth has %d\n got: %v\nwant: %v", len(got), len(want), got, want)
	}
	byKey := map[[2]int][]Conjunction{}
	for _, c := range got {
		k := [2]int{c.A.ID, c.B.ID}
		byKey[k] = append(byKey[k], c)
	}
	for _, w := range want {
		k := [2]int{w.A.ID, w.B.ID}
		matched := false
		for _, c := range byKey[k] {
			if d := c.TCA.Sub(w.TCA); d.Abs() <= time.Millisecond {
				matched = true
				// Both are minima of the same function to within ~10 µs,
				// i.e. ~0.1 m along track at 9 km/s, and the distance is
				// flat at its minimum, so they should agree to the metre.
				if math.Abs(c.MissKm-w.MissKm) > 0.001 {
					t.Errorf("%v: miss %.6f km, ground truth %.6f km", k, c.MissKm, w.MissKm)
				}
			}
		}
		if !matched {
			t.Errorf("missed encounter %v at %v (%.3f km)", k, w.TCA, w.MissKm)
		}
	}
	t.Logf("%d encounters, all found; %d pair checks, %d candidates, %d refined", len(got), st.PairChecks, st.Candidates, st.Refined)
}

// verificationObjects loads Vallado's verification TLEs. Their line 2s
// carry extra columns and some have stale checksums, so they're trimmed
// and fixed first, as in the sgp4 tests.
func verificationObjects(t *testing.T) []*catalog.Object {
	t.Helper()
	data, err := os.ReadFile("../sgp4/testdata/SGP4-VER.TLE")
	if err != nil {
		t.Fatal(err)
	}
	fix := func(l string) string { return l[:68] + strconv.Itoa(tle.Checksum(l)) }
	var objs []*catalog.Object
	var line1 string
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimRight(l, " \r")
		switch {
		case strings.HasPrefix(l, "1 "):
			line1 = l
		case strings.HasPrefix(l, "2 "):
			el, err := tle.Parse("", fix(line1), fix(l[:69]))
			if err != nil {
				t.Fatal(err)
			}
			sat, err := sgp4.New(el)
			if err != nil {
				continue
			}
			// Skip the two whose perigee is underground (28872 and the
			// synthetic 33333), as catalog.Load does.
			if p, _ := sat.PerigeeApogee(); p < sgp4.EarthRadiusKm {
				continue
			}
			objs = append(objs, &catalog.Object{ID: el.CatalogNum, Elems: el, Sat: sat})
		}
	}
	return objs
}

func TestSkipsAttachedAndWindowEdges(t *testing.T) {
	a, b := object(t, 1, 53, 100, 0), object(t, 2, 53, 100, 0)
	cfg := Config{Start: epoch, Duration: time.Hour, Step: 10 * time.Second, Threshold: 5}
	// Same orbit, same place: 0 km apart the whole time. As unrelated
	// objects that's an encounter, but its closest point is the window
	// start, so it's dropped anyway...
	if got, _ := BruteForce([]*catalog.Object{a, b}, cfg); len(got) != 0 {
		t.Errorf("co-located pair: %d conjunctions, want 0 (closest at window edge)", len(got))
	}
	// ...and a real encounter at the very start of the window is dropped
	// too: the pair at 1,2 below is closest ~1.6 s after epoch, so a window
	// starting 5 s after epoch only sees it receding.
	c, d := object(t, 3, 53, 100, 0), object(t, 4, 54, 100, -0.004)
	cfg2 := Config{Start: epoch.Add(5 * time.Second), Duration: 10 * time.Minute, Step: 10 * time.Second, Threshold: 5}
	if got, _ := BruteForce([]*catalog.Object{c, d}, cfg2); len(got) != 0 {
		t.Errorf("receding pair: %d conjunctions, want 0", len(got))
	}
	// Attached objects are skipped outright, even if they'd otherwise
	// produce interior minima.
	cfg3 := Config{Start: epoch.Add(-10 * time.Minute), Duration: time.Hour, Step: 10 * time.Second, Threshold: 5}
	if got, _ := BruteForce([]*catalog.Object{c, d}, cfg3); len(got) == 0 {
		t.Fatal("test setup: pair 3,4 should meet")
	}
	c.Group, d.Group = 1, 1
	if got, _ := BruteForce([]*catalog.Object{c, d}, cfg3); len(got) != 0 {
		t.Errorf("attached pair: %d conjunctions, want 0", len(got))
	}
}

func TestPrimaryOnly(t *testing.T) {
	a, b := object(t, 1, 53, 100, 0), object(t, 2, 54, 100, -0.004)
	cfg := Config{Start: epoch, Duration: 2 * time.Hour, Step: 10 * time.Second, Threshold: 5, PrimaryOnly: true}
	if got, _ := BruteForce([]*catalog.Object{a, b}, cfg); len(got) != 0 {
		t.Errorf("no primaries, but found %d conjunctions", len(got))
	}
	b.Primary = true
	if got, _ := BruteForce([]*catalog.Object{a, b}, cfg); len(got) == 0 {
		t.Error("one primary, but found nothing")
	}
}

// The bounds are only safe if no object's SGP4 path strays from the chord
// between two samples by more than aObj·Δt²/8. Check that on the most
// extreme orbits in Vallado's verification set (perigees below 100 km,
// eccentricity up to 0.97, heavy drag, deep-space resonance).
func TestChordBound(t *testing.T) {
	objs := verificationObjects(t)
	if len(objs) < 28 {
		t.Fatalf("only %d verification objects loaded", len(objs))
	}
	worst := 0.0
	for _, o := range objs {
		for _, dt := range []float64{10, 30, 60} {
			for m := 0.0; m < 1440; m += 3 {
				r0, _, err0 := o.Sat.Propagate(m)
				r1, _, err1 := o.Sat.Propagate(m + dt/60)
				if err0 != nil || err1 != nil {
					break
				}
				// Largest gap between the path and the chord, sampled densely.
				var gap float64
				for s := 0.05; s < 1; s += 0.05 {
					p, _, _ := o.Sat.Propagate(m + s*dt/60)
					var d float64
					for k := range 3 {
						e := p[k] - (r0[k] + (r1[k]-r0[k])*s)
						d += e * e
					}
					gap = math.Max(gap, math.Sqrt(d))
				}
				ratio := gap / (aObj * dt * dt / 8)
				worst = math.Max(worst, ratio)
				if ratio > 1 {
					t.Errorf("object %d t=%.0f Δt=%.0fs: path strays %.4f km from chord, bound %.4f km",
						o.ID, m, dt, gap, aObj*dt*dt/8)
				}
			}
		}
	}
	t.Logf("worst gap is %.0f%% of the bound", 100*worst)
}
