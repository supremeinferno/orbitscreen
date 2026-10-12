package earth

import (
	"math"
	"testing"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/sgp4"
)

func TestGMST(t *testing.T) {
	// Vallado, Fundamentals of Astrodynamics and Applications, Example 3-5:
	// 1992 August 20, 12:14 UT1 -> GMST = 152.578787810°.
	got := sgp4.GMST(time.Date(1992, 8, 20, 12, 14, 0, 0, time.UTC)) * 180 / math.Pi
	if math.Abs(got-152.578787810) > 1e-6 {
		t.Errorf("GMST = %.9f°, want 152.578787810°", got)
	}
}

func TestGeodeticRoundTrip(t *testing.T) {
	for _, lat := range []float64{-90, -89.999, -45, -1e-7, 0, 23.4, 51.6, 89.9999, 90} {
		for _, lon := range []float64{-180, -120.5, 0, 0.001, 77.2, 179.999} {
			for _, alt := range []float64{0, 0.5, 400, 20200, 35786} {
				g := Geodetic{lat, lon, alt}
				back := ECEFToGeodetic(GeodeticToECEF(g))
				dLon := math.Abs(back.Lon - lon)
				if math.Abs(lat) == 90 {
					dLon = 0 // longitude is undefined at the poles
				}
				dLon = math.Min(dLon, 360-dLon)
				if math.Abs(back.Lat-lat) > 1e-9 || dLon > 1e-9 || math.Abs(back.Alt-alt) > 1e-6 {
					t.Errorf("%+v -> %+v", g, back)
				}
			}
		}
	}
	// A point on the equator at the prime meridian is one equatorial radius out.
	if r := GeodeticToECEF(Geodetic{0, 0, 0}); math.Abs(r[0]-6378.137) > 1e-9 || r[1] != 0 || r[2] != 0 {
		t.Errorf("equator/prime meridian = %v", r)
	}
}

func TestTEMEToECEF(t *testing.T) {
	// At any time, the rotation must keep length and z, and turn by GMST.
	tm := time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)
	r := [3]float64{7000, 0, 1000}
	e := TEMEToECEF(r, tm)
	if math.Abs(math.Hypot(e[0], e[1])-7000) > 1e-9 || e[2] != 1000 {
		t.Errorf("rotation changed length or z: %v", e)
	}
	want := -sgp4.GMST(tm)
	if d := math.Remainder(math.Atan2(e[1], e[0])-want, 2*math.Pi); math.Abs(d) > 1e-12 {
		t.Errorf("rotated by %v, want %v", math.Atan2(e[1], e[0]), want)
	}
}
