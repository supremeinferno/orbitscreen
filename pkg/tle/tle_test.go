package tle

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// The classic ISS TLE used in SGP4 documentation (epoch Sept 2008).
// We use an old, well-known TLE on purpose: its values are published
// everywhere, so our tests have a trustworthy "answer key".
const (
	issName  = "ISS (ZARYA)"
	issLine1 = "1 25544U 98067A   08264.51782528 -.00002182  00000-0 -11606-4 0  2927"
	issLine2 = "2 25544  51.6416 247.4627 0006703 130.5360 325.0288 15.72125391563537"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestChecksum(t *testing.T) {
	if got := Checksum(issLine1); got != 7 {
		t.Errorf("line 1 checksum = %d, want 7", got)
	}
	if got := Checksum(issLine2); got != 7 {
		t.Errorf("line 2 checksum = %d, want 7", got)
	}
}

func TestParseImpliedExp(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"-11606-4", -0.11606e-4},
		{" 12345-3", 0.12345e-3},
		{" 00000-0", 0},
		{"+50000+1", 5.0},
	}
	for _, c := range cases {
		got, err := parseImpliedExp(c.in)
		if err != nil {
			t.Errorf("parseImpliedExp(%q) error: %v", c.in, err)
			continue
		}
		if !near(got, c.want, 1e-15) {
			t.Errorf("parseImpliedExp(%q) = %g, want %g", c.in, got, c.want)
		}
	}
}

func TestEpochToTime(t *testing.T) {
	// 2008 is a leap year: day 264 = September 20.
	// 0.51782528 day = 44740.104192 s = 12:25:40.104 UTC
	want := time.Date(2008, 9, 20, 12, 25, 40, 104192000, time.UTC)
	got := epochToTime(8, 264.51782528)
	if d := got.Sub(want); d > time.Millisecond || d < -time.Millisecond {
		t.Errorf("epochToTime = %v, want %v (diff %v)", got, want, d)
	}

	// Year boundary rule: 57 -> 1957 (Sputnik era), 56 -> 2056.
	if y := epochToTime(57, 1.0).Year(); y != 1957 {
		t.Errorf("yy=57 -> year %d, want 1957", y)
	}
	if y := epochToTime(56, 1.0).Year(); y != 2056 {
		t.Errorf("yy=56 -> year %d, want 2056", y)
	}
	// Day 1.0 is the START of Jan 1.
	if got := epochToTime(24, 1.0); !got.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("day 1.0 -> %v, want 2024-01-01 00:00:00", got)
	}
}

func TestParseISS(t *testing.T) {
	tle, err := Parse(issName, issLine1, issLine2)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if tle.Name != issName {
		t.Errorf("Name = %q", tle.Name)
	}
	if tle.CatalogNum != 25544 {
		t.Errorf("CatalogNum = %d, want 25544", tle.CatalogNum)
	}
	if tle.Classification != 'U' {
		t.Errorf("Classification = %c, want U", tle.Classification)
	}
	if tle.IntlDesignator != "98067A" {
		t.Errorf("IntlDesignator = %q, want 98067A", tle.IntlDesignator)
	}
	if tle.ElementSetNum != 292 {
		t.Errorf("ElementSetNum = %d, want 292", tle.ElementSetNum)
	}
	if tle.RevNum != 56353 {
		t.Errorf("RevNum = %d, want 56353", tle.RevNum)
	}

	floats := []struct {
		name      string
		got, want float64
	}{
		{"NDot", tle.NDot, -0.00002182},
		{"NDDot", tle.NDDot, 0},
		{"BStar", tle.BStar, -0.11606e-4},
		{"Inclination", tle.Inclination, 51.6416},
		{"RAAN", tle.RAAN, 247.4627},
		{"Eccentricity", tle.Eccentricity, 0.0006703},
		{"ArgPerigee", tle.ArgPerigee, 130.5360},
		{"MeanAnomaly", tle.MeanAnomaly, 325.0288},
		{"MeanMotion", tle.MeanMotion, 15.72125391},
	}
	for _, f := range floats {
		if !near(f.got, f.want, 1e-12) {
			t.Errorf("%s = %.10g, want %.10g", f.name, f.got, f.want)
		}
	}
}

func TestParseRejectsBadChecksum(t *testing.T) {
	// Change one digit of the inclination (51.6416 -> 51.6417) but keep the
	// old checksum: a corrupted line must be rejected.
	bad := "2 25544  51.6417 247.4627 0006703 130.5360 325.0288 15.72125391563537"
	_, err := Parse(issName, issLine1, bad)
	if !errors.Is(err, ErrChecksum) {
		t.Errorf("want ErrChecksum, got %v", err)
	}
}

func TestParseRejectsShortLine(t *testing.T) {
	if _, err := Parse(issName, issLine1[:60], issLine2); err == nil {
		t.Error("want error for a 60-char line 1, got nil")
	}
}

func TestParseAlpha5(t *testing.T) {
	// Same ISS lines with the catalog number rewritten to Alpha-5 "A0042"
	// (= 100042). Checksums are recomputed since the digits changed.
	fix := func(l string) string { l = l[:68]; return l + string(rune('0'+Checksum(l))) }
	l1 := fix("1 A0042" + issLine1[7:])
	l2 := fix("2 A0042" + issLine2[7:])
	tle, err := Parse(issName, l1, l2)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if tle.CatalogNum != 100042 {
		t.Errorf("CatalogNum = %d, want 100042", tle.CatalogNum)
	}
}

func TestReadAll(t *testing.T) {
	// A 3-line record, a bare 2-line record, then one with a bad checksum.
	bad := issLine2[:68] + "0"
	in := issName + "\r\n" + issLine1 + "\r\n" + issLine2 + "\n\n" +
		issLine1 + "\n" + issLine2 + "\n" +
		"BROKEN\n" + issLine1 + "\n" + bad + "\n"
	tles, err := ReadAll(strings.NewReader(in))
	if len(tles) != 2 {
		t.Fatalf("got %d TLEs, want 2", len(tles))
	}
	if tles[0].Name != issName || tles[1].Name != "" {
		t.Errorf("names = %q, %q; want %q, \"\"", tles[0].Name, tles[1].Name, issName)
	}
	if !errors.Is(err, ErrChecksum) {
		t.Errorf("want ErrChecksum for the broken record, got %v", err)
	}
}
