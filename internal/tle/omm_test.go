package tle

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReadOMMCSV(t *testing.T) {
	// The ISS TLE from tle_test.go, as CelesTrak would serve it in OMM CSV,
	// plus a row with a 6-digit catalog number (which no TLE can carry) and
	// a broken row.
	in := `OBJECT_NAME,OBJECT_ID,EPOCH,MEAN_MOTION,ECCENTRICITY,INCLINATION,RA_OF_ASC_NODE,ARG_OF_PERICENTER,MEAN_ANOMALY,EPHEMERIS_TYPE,CLASSIFICATION_TYPE,NORAD_CAT_ID,ELEMENT_SET_NO,REV_AT_EPOCH,BSTAR,MEAN_MOTION_DOT,MEAN_MOTION_DDOT
ISS (ZARYA),1998-067A,2008-09-20T12:25:40.104192,15.72125391,.0006703,51.6416,247.4627,130.5360,325.0288,0,U,25544,292,56353,-.11606E-4,-.00002182,0
NEW OBJECT,2026-100B,2026-10-11T00:00:00.000000,15.0,.0001,53.0,10.0,20.0,30.0,0,U,123456,999,5,.0001,0,0
BROKEN,2026-100C,not-a-date,15.0,.0001,53.0,10.0,20.0,30.0,0,U,123457,999,5,.0001,0,0
`
	got, err := ReadOMMCSV(strings.NewReader(in))
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2 (err %v)", len(got), err)
	}
	if err == nil || !strings.Contains(err.Error(), "line 4") {
		t.Errorf("want an error for line 4, got %v", err)
	}

	want, perr := Parse(issName, issLine1, issLine2)
	if perr != nil {
		t.Fatal(perr)
	}
	iss := got[0]
	if iss.CatalogNum != 25544 || iss.Name != issName || iss.Classification != 'U' || iss.RevNum != 56353 {
		t.Errorf("ids: %+v", iss)
	}
	if d := iss.Epoch.Sub(want.Epoch); d > time.Microsecond || d < -time.Microsecond {
		t.Errorf("Epoch = %v, TLE says %v", iss.Epoch, want.Epoch)
	}
	for _, f := range []struct {
		name      string
		got, want float64
	}{
		{"BStar", iss.BStar, want.BStar},
		{"NDot", iss.NDot, want.NDot},
		{"Inclination", iss.Inclination, want.Inclination},
		{"RAAN", iss.RAAN, want.RAAN},
		{"Eccentricity", iss.Eccentricity, want.Eccentricity},
		{"ArgPerigee", iss.ArgPerigee, want.ArgPerigee},
		{"MeanAnomaly", iss.MeanAnomaly, want.MeanAnomaly},
		{"MeanMotion", iss.MeanMotion, want.MeanMotion},
	} {
		if !near(f.got, f.want, 1e-12) {
			t.Errorf("%s = %g, TLE says %g", f.name, f.got, f.want)
		}
	}
	if got[1].CatalogNum != 123456 {
		t.Errorf("6-digit catalog number = %d", got[1].CatalogNum)
	}
}

func TestReadOMMCSVMissingColumn(t *testing.T) {
	_, err := ReadOMMCSV(strings.NewReader("OBJECT_NAME,EPOCH\nX,2026-01-01T00:00:00\n"))
	if err == nil || errors.Is(err, ErrChecksum) {
		t.Errorf("want missing-column error, got %v", err)
	}
}
