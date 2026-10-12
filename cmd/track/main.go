// Command track serves a live map of every object in the catalog.
//
//	go run ./cmd/fetch    # download data/ first
//	go run ./cmd/track    # then open http://localhost:8080
//
// The browser asks for every object's position about once a second; the
// server propagates the whole catalog with SGP4 for each request (~20k
// objects in ~10 ms) and converts TEME positions to latitude, longitude
// and altitude. See docs/06-live-tracker.md.
package main

import (
	"embed"
	"encoding/binary"
	"encoding/json"
	"flag"
	"io/fs"
	"log"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/catalog"
	"github.com/supremeinferno/orbitscreen/internal/earth"
	"github.com/supremeinferno/orbitscreen/internal/sgp4"
)

//go:embed web
var webFiles embed.FS

type server struct {
	objs []*catalog.Object
	byID map[int]int // catalog number -> index in objs
	meta []objectMeta
}

// objectMeta is what the browser gets once, in /api/catalog.
type objectMeta struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`  // active, inactive, rocket, debris
	Orbit    string `json:"orbit"` // LEO, MEO, GEO, HEO
	Attached bool   `json:"attached,omitempty"`
}

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	dataDir := flag.String("data", "data", "folder of .csv (OMM) and .tle files")
	primary := flag.String("primary", "data/active.csv", "file listing the active satellites")
	flag.Parse()

	files, _ := filepath.Glob(filepath.Join(*dataDir, "*.csv"))
	tles, _ := filepath.Glob(filepath.Join(*dataDir, "*.tle"))
	files = append(files, tles...)
	if len(files) == 0 {
		log.Fatalf("no data in %s; run: go run ./cmd/fetch", *dataDir)
	}
	objs, st, err := catalog.Load(files, []string{*primary})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("loaded %d objects (%d active)", st.Objects, st.Primaries)

	log.Printf("open http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, newServer(objs).routes()))
}

func newServer(objs []*catalog.Object) *server {
	s := &server{objs: objs, byID: map[int]int{}}
	for i, o := range objs {
		s.byID[o.ID] = i
		s.meta = append(s.meta, objectMeta{
			ID: o.ID, Name: o.Name, Kind: kind(o), Orbit: orbitClass(o), Attached: o.Group != 0,
		})
	}
	return s
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	web, _ := fs.Sub(webFiles, "web")
	mux.Handle("/", http.FileServerFS(web))
	mux.HandleFunc("/api/catalog", s.handleCatalog)
	mux.HandleFunc("/api/positions", s.handlePositions)
	mux.HandleFunc("/api/object", s.handleObject)
	return mux
}

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

// timeParam reads ?t= (Unix milliseconds), defaulting to now.
func timeParam(r *http.Request) time.Time {
	if ms, err := strconv.ParseInt(r.URL.Query().Get("t"), 10, 64); err == nil {
		return time.UnixMilli(ms).UTC()
	}
	return time.Now().UTC()
}

func (s *server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.meta)
}

// handlePositions returns latitude, longitude (degrees) and altitude (km)
// for every object, in /api/catalog order, as little-endian float32
// triples: 12 bytes per object, NaN where SGP4 fails. Binary because it's
// ~3× smaller than JSON and needs no parsing in the browser.
func (s *server) handlePositions(w http.ResponseWriter, r *http.Request) {
	t := timeParam(r)
	buf := make([]byte, 12*len(s.objs))
	nan := math.Float32bits(float32(math.NaN()))
	for i, o := range s.objs {
		vals := [3]uint32{nan, nan, nan}
		if p, _, err := o.Sat.PropagateTo(t); err == nil {
			g := earth.SubPoint(p, t)
			vals = [3]uint32{math.Float32bits(float32(g.Lat)), math.Float32bits(float32(g.Lon)), math.Float32bits(float32(g.Alt))}
		}
		for k, v := range vals {
			binary.LittleEndian.PutUint32(buf[12*i+4*k:], v)
		}
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf)
}

type objectInfo struct {
	objectMeta
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

func (s *server) handleObject(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	i, ok := s.byID[id]
	if !ok {
		http.Error(w, "unknown id", http.StatusNotFound)
		return
	}
	o, t := s.objs[i], timeParam(r)
	el := o.Elems
	info := objectInfo{
		objectMeta:     s.meta[i],
		IntlDesignator: el.IntlDesignator,
		Epoch:          el.Epoch,
		Inclination:    el.Inclination,
		PeriodMin:      1440 / el.MeanMotion,
		PerigeeKm:      o.PerigeeKm - sgp4.EarthRadiusKm,
		ApogeeKm:       o.ApogeeKm - sgp4.EarthRadiusKm,
	}
	if p, v, err := o.Sat.PropagateTo(t); err != nil {
		info.Error = err.Error()
	} else {
		g := earth.SubPoint(p, t)
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
		tt := t.Add(time.Duration(m * float64(time.Minute)))
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}
