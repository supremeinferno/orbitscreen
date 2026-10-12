package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

const header = "OBJECT_NAME,OBJECT_ID,EPOCH,MEAN_MOTION,ECCENTRICITY,INCLINATION,RA_OF_ASC_NODE,ARG_OF_PERICENTER,MEAN_ANOMALY,EPHEMERIS_TYPE,CLASSIFICATION_TYPE,NORAD_CAT_ID,ELEMENT_SET_NO,REV_AT_EPOCH,BSTAR,MEAN_MOTION_DOT,MEAN_MOTION_DDOT\n"

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	active := write(t, dir, "active.csv", header+
		"SAT A,2020-001A,2026-10-10T00:00:00,15.0,.001,53.0,10,20,30,0,U,100,999,1,.0001,0,0\n")
	debris := write(t, dir, "debris.csv", header+
		// A newer element set for 100: it should replace the one above and
		// stay primary.
		"SAT A,2020-001A,2026-10-11T00:00:00,15.0,.001,53.0,11,20,30,0,U,100,999,1,.0001,0,0\n"+
		"DEB B,2009-001B,2026-10-11T00:00:00,14.3,.002,74.0,10,20,30,0,U,200,999,1,.0001,0,0\n"+
		// Mean motion 0.00001 rev/day: SGP4 rejects it at epoch.
		"BAD C,2009-001C,2026-10-11T00:00:00,0.00001,.5,74.0,10,20,30,0,U,300,999,1,.0001,0,0\n"+
		"BROKEN,x,not-a-date,1,1,1,1,1,1,0,U,400,1,1,1,0,0\n")
	// A TLE file with the 2008 ISS, to check format sniffing.
	tleFile := write(t, dir, "iss.tle", "ISS (ZARYA)\n"+
		"1 25544U 98067A   08264.51782528 -.00002182  00000-0 -11606-4 0  2927\n"+
		"2 25544  51.6416 247.4627 0006703 130.5360 325.0288 15.72125391563537\n")

	objs, st, err := Load([]string{debris, tleFile}, []string{active})
	if err != nil {
		t.Fatal(err)
	}
	want := Stats{Records: 6, Duplicates: 1, BadRecords: 1, InitFailed: 1, Objects: 3, Primaries: 1}
	if st != want {
		t.Errorf("stats = %+v, want %+v", st, want)
	}
	if len(objs) != 3 || objs[0].ID != 100 || objs[1].ID != 200 || objs[2].ID != 25544 {
		t.Fatalf("objects = %v", objs)
	}
	a := objs[0]
	if !a.Primary || a.Elems.RAAN != 11 {
		t.Errorf("object 100: primary=%v RAAN=%v; want the newer element set, still primary", a.Primary, a.Elems.RAAN)
	}
	if objs[1].Primary || objs[2].Name != "ISS (ZARYA)" {
		t.Errorf("object 200 primary=%v, ISS name=%q", objs[1].Primary, objs[2].Name)
	}
	// ISS in 2008: roughly 350 km perigee, 360 km apogee.
	if p, ap := objs[2].PerigeeKm-6378, objs[2].ApogeeKm-6378; p < 330 || ap > 380 || p > ap {
		t.Errorf("ISS perigee/apogee altitude = %.0f / %.0f km", p, ap)
	}
}
