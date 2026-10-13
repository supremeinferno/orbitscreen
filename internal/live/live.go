// Package live computes what the live tracker shows: every object's
// position at an instant, and details plus a ground track for one object.
// The same code runs in the tracker's web server (cmd/track) and, compiled
// to WebAssembly, in the browser (cmd/trackwasm). See docs/06-live-tracker.md.
package live

import (
	"encoding/binary"
	"math"
	"strings"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/catalog"
	"github.com/supremeinferno/orbitscreen/internal/earth"
	"github.com/supremeinferno/orbitscreen/internal/sgp4"
)

// Tracker holds a loaded catalog.
type Tracker struct {
	objs []*catalog.Object
	byID map[int]int // catalog number -> index in objs
	meta []Meta
}

// Meta is what the browser gets once per object.
type Meta struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`  // active, inactive, rocket, debris
	Orbit    string `json:"orbit"` // LEO, MEO, GEO, HEO
	Attached bool   `json:"attached,omitempty"`
}

func New(objs []*catalog.Object) *Tracker {
	t := &Tracker{objs: objs, byID: map[int]int{}}
	for i, o := range objs {
		t.byID[o.ID] = i
		t.meta = append(t.meta, Meta{
			ID: o.ID, Name: o.Name, Kind: kind(o), Orbit: orbitClass(o), Attached: o.Group != 0,
		})
	}
	return t
}

// Catalog lists every object, in the order Positions uses.
func (t *Tracker) Catalog() []Meta { return t.meta }

// kind sorts objects for colouring. Names follow the catalog's conventions:
// debris is "... DEB", rocket bodies are "... R/B".
func kind(o *catalog.Object) string {
	switch n := strings.ToUpper(o.Name); {
	case strings.Contains(n, " DEB"):
		return "debris"
	case strings.Contains(n, "R/B"):
		return "rocket"
	case o.Primary:
		return "active"
	default:
		return "inactive"
	}
}

// orbitClass buckets an orbit by perigee and apogee altitude.
func orbitClass(o *catalog.Object) string {
	per, apo := o.PerigeeKm-sgp4.EarthRadiusKm, o.ApogeeKm-sgp4.EarthRadiusKm
	switch {
	case apo < 2000:
		return "LEO"
	case per > 35000 && apo < 36600:
		return "GEO"
	case (apo-per)/(apo+per+2*sgp4.EarthRadiusKm) > 0.25:
		return "HEO" // eccentricity > 0.25
	default:
		return "MEO"
	}
}

// Positions returns latitude, longitude (degrees) and altitude (km) for
// every object at tm, in Catalog order, as little-endian float32 triples:
// 12 bytes per object, NaN where SGP4 fails. Binary because it's ~3×
// smaller than JSON and the browser can use it as a Float32Array directly.
func (t *Tracker) Positions(tm time.Time) []byte {
	buf := make([]byte, 12*len(t.objs))
	nan := math.Float32bits(float32(math.NaN()))
	for i, o := range t.objs {
		vals := [3]uint32{nan, nan, nan}
		if p, _, err := o.Sat.PropagateTo(tm); err == nil {
			g := earth.SubPoint(p, tm)
			vals = [3]uint32{math.Float32bits(float32(g.Lat)), math.Float32bits(float32(g.Lon)), math.Float32bits(float32(g.Alt))}
		}
		for k, v := range vals {
			binary.LittleEndian.PutUint32(buf[12*i+4*k:], v)
		}
	}
	return buf
}

// Info describes one object at an instant.
type Info struct {
	Meta
	IntlDesignator string       `json:"intlDes"`
	Epoch          time.Time    `json:"epoch"`
	Inclination    float64      `json:"inclination"`
	PeriodMin      float64      `json:"periodMin"`
	PerigeeKm      float64      `json:"perigeeKm"` // altitude
	ApogeeKm       float64      `json:"apogeeKm"`  // altitude
	Lat            float64      `json:"lat"`
	Lon            float64      `json:"lon"`
	AltKm          float64      `json:"altKm"`
	SpeedKmS       float64      `json:"speedKmS"`
	Track          [][2]float64 `json:"track"`        // [lat, lon], from half an orbit ago to one orbit ahead
	TrackNow       int          `json:"trackNow"`     // index in Track of the current time
	FootprintDeg   float64      `json:"footprintDeg"` // angular radius of the area that can see the object
	Error          string       `json:"error,omitempty"`
}

// Object returns details for catalog number id at tm, or false if the
// catalog doesn't have it.
func (t *Tracker) Object(id int, tm time.Time) (Info, bool) {
	i, ok := t.byID[id]
	if !ok {
		return Info{}, false
	}
	o := t.objs[i]
	el := o.Elems
	info := Info{
		Meta:           t.meta[i],
		IntlDesignator: el.IntlDesignator,
		Epoch:          el.Epoch,
		Inclination:    el.Inclination,
		PeriodMin:      1440 / el.MeanMotion,
		PerigeeKm:      o.PerigeeKm - sgp4.EarthRadiusKm,
		ApogeeKm:       o.ApogeeKm - sgp4.EarthRadiusKm,
	}
	if p, v, err := o.Sat.PropagateTo(tm); err != nil {
		info.Error = err.Error()
	} else {
		g := earth.SubPoint(p, tm)
		info.Lat, info.Lon, info.AltKm = g.Lat, g.Lon, g.Alt
		info.SpeedKmS = math.Sqrt(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])
		info.FootprintDeg = math.Acos(sgp4.EarthRadiusKm/(sgp4.EarthRadiusKm+math.Max(g.Alt, 0))) * 180 / math.Pi
	}

	// Ground track: 240 points over 1.5 orbits (capped at 2 days, for GEO
	// and beyond where the track barely moves).
	span := math.Min(1.5*info.PeriodMin, 2880)
	from := -span / 3
	step := span / 240
	for k := 0; k <= 240; k++ {
		m := from + float64(k)*step
		tt := tm.Add(time.Duration(m * float64(time.Minute)))
		p, _, err := o.Sat.PropagateTo(tt)
		if err != nil {
			continue
		}
		g := earth.SubPoint(p, tt)
		if math.Abs(m) < step/2 {
			info.TrackNow = len(info.Track)
		}
		info.Track = append(info.Track, [2]float64{g.Lat, g.Lon})
	}
	return info, true
}
