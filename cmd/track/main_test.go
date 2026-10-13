package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/supremeinferno/orbitscreen/internal/catalog"
	"github.com/supremeinferno/orbitscreen/internal/live"
)

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sats.csv")
	csv := "OBJECT_NAME,OBJECT_ID,EPOCH,MEAN_MOTION,ECCENTRICITY,INCLINATION,RA_OF_ASC_NODE,ARG_OF_PERICENTER,MEAN_ANOMALY,EPHEMERIS_TYPE,CLASSIFICATION_TYPE,NORAD_CAT_ID,ELEMENT_SET_NO,REV_AT_EPOCH,BSTAR,MEAN_MOTION_DOT,MEAN_MOTION_DDOT\n" +
		"ISS (ZARYA),1998-067A,2026-10-11T19:43:07.186944,15.4884,.0006770,51.6314,120.1,90.2,270.0,0,U,25544,999,1,.00025,0,0\n" +
		"COSMOS 2251 DEB,1993-036E,2026-10-11T23:05:43.094112,14.32641056,.00137286,74.0347,130.0435,72.9269,37.9659,0,U,33757,999,1,.0000317,0,0\n" +
		"GOES 16,2016-071A,2026-10-11T22:00:00,1.00271,.0001,0.6,90,0,0,0,U,41866,999,1,0,0,0\n"
	if err := os.WriteFile(p, []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	objs, _, err := catalog.Load([]string{p}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newServer(objs).routes())
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := httpGet(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var httpGet = http.Get

func TestAPI(t *testing.T) {
	srv := testServer(t)

	var cat []live.Meta
	if err := json.Unmarshal(get(t, srv.URL+"/api/catalog"), &cat); err != nil {
		t.Fatal(err)
	}
	want := map[int][2]string{25544: {"inactive", "LEO"}, 33757: {"debris", "LEO"}, 41866: {"inactive", "GEO"}}
	if len(cat) != 3 {
		t.Fatalf("catalog has %d objects", len(cat))
	}
	for _, c := range cat {
		if w := want[c.ID]; c.Kind != w[0] || c.Orbit != w[1] {
			t.Errorf("%d: kind %s orbit %s, want %v", c.ID, c.Kind, c.Orbit, w)
		}
	}

	// Positions: 3 float32s per object, plausible latitude/altitude.
	b := get(t, srv.URL+"/api/positions?t=1791784800000") // 2026-10-12T06:00:00Z
	if len(b) != 12*len(cat) {
		t.Fatalf("positions: %d bytes, want %d", len(b), 12*len(cat))
	}
	for i, c := range cat {
		f := func(k int) float64 { return float64(math.Float32frombits(binary.LittleEndian.Uint32(b[12*i+4*k:]))) }
		lat, lon, alt := f(0), f(1), f(2)
		if math.Abs(lat) > 90 || lon < -180 || lon > 180 {
			t.Errorf("%d: lat %v lon %v", c.ID, lat, lon)
		}
		if c.Orbit == "LEO" && (alt < 300 || alt > 1000) || c.Orbit == "GEO" && math.Abs(alt-35786) > 100 {
			t.Errorf("%d: altitude %.0f km for a %s orbit", c.ID, alt, c.Orbit)
		}
		// The ISS's latitude can't exceed its inclination.
		if c.ID == 25544 && math.Abs(lat) > 51.7 {
			t.Errorf("ISS latitude %v exceeds its 51.6° inclination", lat)
		}
	}

	var info live.Info
	if err := json.Unmarshal(get(t, srv.URL+"/api/object?id=25544&t=1791784800000"), &info); err != nil {
		t.Fatal(err)
	}
	if len(info.Track) != 241 || math.Abs(info.PeriodMin-92.97) > 0.1 || math.Abs(info.SpeedKmS-7.66) > 0.05 {
		t.Errorf("ISS: %d track points, period %.2f min, speed %.2f km/s", len(info.Track), info.PeriodMin, info.SpeedKmS)
	}
	if now := info.Track[info.TrackNow]; math.Abs(now[0]-info.Lat) > 1e-9 || math.Abs(now[1]-info.Lon) > 1e-9 {
		t.Errorf("track point %d = %v, current position %v,%v", info.TrackNow, now, info.Lat, info.Lon)
	}
	if res, _ := httpGet(srv.URL + "/api/object?id=1"); res.StatusCode != 404 {
		t.Errorf("unknown id: status %d", res.StatusCode)
	}
	if body := get(t, srv.URL+"/"); len(body) == 0 {
		t.Error("index page is empty")
	}
}
