package main

import (
	"testing"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/catalog"
	"github.com/supremeinferno/orbitscreen/internal/screen"
)

func TestFleet(t *testing.T) {
	for name, want := range map[string]string{
		"STARLINK-1234": "STARLINK", "ONEWEB-0123": "ONEWEB", "ISS (ZARYA)": "ISS", "COSMOS 2251 DEB": "COSMOS", "": "",
	} {
		if got := fleet(name); got != want {
			t.Errorf("fleet(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestClosest(t *testing.T) {
	obj := func(id int, name string, primary bool) *catalog.Object {
		return &catalog.Object{ID: id, Name: name, Primary: primary}
	}
	s1, s2 := obj(1, "STARLINK-1", true), obj(2, "STARLINK-2", true)
	deb, sat := obj(3, "FENGYUN 1C DEB", false), obj(4, "ICEYE-X1", true)
	t0 := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	found := []screen.Conjunction{
		{A: s1, B: s2, TCA: t0, MissKm: 0.01},                                     // same fleet, both active: left out
		{A: deb, B: sat, TCA: t0.Add(time.Hour), MissKm: 2.5, RelSpeed: 10},       // the pair again, further: counted once
		{A: s1, B: deb, TCA: t0, MissKm: 3.0, RelSpeed: 14.1234},                  // third closest pair
		{A: deb, B: sat, TCA: t0.Add(2 * time.Hour), MissKm: 0.0634, RelSpeed: 9}, // closest kept pair
		{A: s2, B: sat, TCA: t0, MissKm: 1.0, RelSpeed: 12},                       // different fleets: kept
	}
	got, total, _ := closest(found, 2)
	if total != 3 {
		t.Errorf("total = %d pairs, want 3", total)
	}
	if len(got) != 2 || got[0].A.ID != 3 || got[0].B.ID != 4 || got[1].A.ID != 2 {
		t.Fatalf("got %+v, want pairs 3-4 then 2-4", got)
	}
	if got[0].MissKm != 0.063 || !got[0].TCA.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("closest = %+v, want 0.063 km at the 2 h pass", got[0])
	}
	if found[0].MissKm != 0.01 {
		t.Error("closest reordered its input")
	}
}

// Two objects released from one rocket stay close at almost no relative
// speed: that is not a pass, and it must not top the list.
func TestClosestLeavesOutPairsFlyingTogether(t *testing.T) {
	a, b, c := &catalog.Object{ID: 10, Name: "W-SERIES 8", Primary: true}, &catalog.Object{ID: 11, Name: "MIKROGLOB-3", Primary: true}, &catalog.Object{ID: 12, Name: "COSMOS 2251 DEB"}
	t0 := time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)
	found := []screen.Conjunction{
		{A: a, B: b, TCA: t0, MissKm: 0.081, RelSpeed: 0},  // together, closest of all
		{A: a, B: b, TCA: t0.Add(time.Hour), MissKm: 0.09}, // together again: one pair
		{A: a, B: c, TCA: t0, MissKm: 0.5, RelSpeed: 14.8}, // a real pass
	}
	got, total, together := closest(found, 5)
	if len(got) != 1 || got[0].B.ID != 12 || total != 1 || together != 1 {
		t.Fatalf("got %+v, %d pairs, %d together; want only the pass with 12, 1 pair, 1 together", got, total, together)
	}
}
