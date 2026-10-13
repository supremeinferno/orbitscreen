package socrates

import (
	"strings"
	"testing"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/catalog"
	"github.com/supremeinferno/orbitscreen/internal/screen"
	"github.com/supremeinferno/orbitscreen/internal/tle"
)

const sample = `NORAD_CAT_ID_1,OBJECT_NAME_1,DSE_1,NORAD_CAT_ID_2,OBJECT_NAME_2,DSE_2,TCA,TCA_RANGE,TCA_RELATIVE_SPEED,MAX_PROB,DILUTION
66259,STARLINK-35529 [+],4.531,100303,STARLINK-38247 [+],4.568,2026-10-16 10:59:33.430,0.010,14.759,1.000E+00,0.000
48354,STARLINK-2674 [+],2.406,54569,CZ-6A DEB [-],1.298,2026-10-12 13:37:31.305,0.015,7.364,3.054E-01,0.004
`

func TestRead(t *testing.T) {
	cs, err := Read(strings.NewReader(sample))
	if err != nil || len(cs) != 2 {
		t.Fatalf("got %d, err %v", len(cs), err)
	}
	c := cs[1]
	if c.ID1 != 48354 || c.Name2 != "CZ-6A DEB" || c.Status1 != "+" || c.Status2 != "-" ||
		c.RangeKm != 0.015 || c.RelSpeed != 7.364 || c.MaxProb != 0.3054 {
		t.Errorf("parsed %+v", c)
	}
	if want := time.Date(2026, 10, 12, 13, 37, 31, 305e6, time.UTC); !c.TCA.Equal(want) {
		t.Errorf("TCA = %v", c.TCA)
	}
	// DSE 1.298 days before TCA.
	if want := c.TCA.Add(-time.Duration(1.298 * 86400 * float64(time.Second))); !c.Epoch2().Equal(want) {
		t.Errorf("Epoch2 = %v, want %v", c.Epoch2(), want)
	}
}

func TestAsOf(t *testing.T) {
	cut := time.Date(2026, 10, 12, 8, 22, 44, 0, time.UTC)
	at := func(h float64) time.Time { return cut.Add(time.Duration(h * float64(time.Hour))) }
	set := func(id int, epochH, createdH float64) *tle.TLE {
		return &tle.TLE{CatalogNum: id, Epoch: at(epochH), Created: at(createdH)}
	}
	sets := []*tle.TLE{
		set(1, -30, -29), set(1, -10, -9), set(1, +5, +6), // SOCRATES used the -10 h set
		set(2, -20, -19), set(2, -2, -1), set(2, +3, +4), // not in SOCRATES: latest published before cut
		set(3, +2, +3), // SOCRATES had it, but our history starts later
		set(4, +1, +2), // new object, published after the cut: left out
		// Same epoch republished: SOCRATES saw the -5 h correction, not the
		// +1 h one. Object 6 is the same, but not in SOCRATES's results.
		set(5, -10, -9), set(5, -10, -5), set(5, -10, +1),
		set(6, -10, -9), set(6, -10, -5), set(6, -10, +1),
	}
	used := map[int]time.Time{1: at(-10).Add(20 * time.Second), 3: at(-50), 5: at(-10)}
	chosen, how := AsOf(sets, used, cut)
	got := map[int]*tle.TLE{}
	for _, s := range chosen {
		got[s.CatalogNum] = s
	}
	check := func(id int, wantEpochH float64, wantHow Alignment) {
		t.Helper()
		if how[id] != wantHow {
			t.Errorf("object %d: alignment %v, want %v", id, how[id], wantHow)
		}
		if s := got[id]; s == nil || !s.Epoch.Equal(at(wantEpochH)) {
			t.Errorf("object %d: chose %v, want epoch %+.0f h", id, s, wantEpochH)
		}
	}
	check(1, -10, Same)
	check(2, -2, Published)
	check(3, +2, Newer)
	check(5, -10, Same)
	check(6, -10, Published)
	for _, id := range []int{5, 6} {
		if s := got[id]; s != nil && !s.Created.Equal(at(-5)) {
			t.Errorf("object %d: chose the set published at %v, want the -5 h one", id, s.Created)
		}
	}
	if _, ok := got[4]; ok || how[4] != Unpublished {
		t.Errorf("object 4: chosen=%v alignment %v, want left out", ok, how[4])
	}
}

func TestCompare(t *testing.T) {
	obj := func(id int) *catalog.Object { return &catalog.Object{ID: id} }
	a, b, c := obj(1), obj(2), obj(3)
	t0 := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	soc := []Conjunction{
		{ID1: 2, ID2: 1, TCA: t0},                       // ours has it 2 s later, IDs swapped
		{ID1: 1, ID2: 2, TCA: t0.Add(90 * time.Minute)}, // same pair, next orbit: ours is 3 min off
		{ID1: 1, ID2: 3, TCA: t0.Add(time.Hour)},        // we don't have this pair at all
	}
	ours := []screen.Conjunction{
		{A: a, B: b, TCA: t0.Add(2 * time.Second)},
		{A: a, B: b, TCA: t0.Add(93 * time.Minute)},
		{A: b, B: c, TCA: t0}, // only ours
	}
	m, extra := Compare(soc, ours, time.Minute)
	if m[0].Ours != &ours[0] || m[1].Ours != nil || m[2].Ours != nil {
		t.Errorf("matches: %v %v %v", m[0].Ours, m[1].Ours, m[2].Ours)
	}
	if len(extra) != 2 {
		t.Errorf("%d extra, want 2 (the 3-min-off one and the 2-3 pair)", len(extra))
	}
}
