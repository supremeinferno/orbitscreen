// Command track serves a live map of every object in the catalog.
//
//	go run ./cmd/fetch    # download data/ first
//	go run ./cmd/track    # then open http://localhost:8080
//
// The browser asks for every object's position about once a second; the
// server propagates the whole catalog with SGP4 for each request (~20k
// objects in ~10 ms) and converts TEME positions to latitude, longitude
// and altitude (package live). The public site runs the same code in the
// browser instead, compiled to WebAssembly (cmd/trackwasm). See
// docs/06-live-tracker.md.
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/catalog"
	"github.com/supremeinferno/orbitscreen/internal/live"
)

//go:embed web
var webFiles embed.FS

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

type server struct{ t *live.Tracker }

func newServer(objs []*catalog.Object) *server { return &server{live.New(objs)} }

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	web, _ := fs.Sub(webFiles, "web")
	mux.Handle("/", http.FileServerFS(web))
	mux.HandleFunc("/api/catalog", s.handleCatalog)
	mux.HandleFunc("/api/positions", s.handlePositions)
	mux.HandleFunc("/api/object", s.handleObject)
	return mux
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
	json.NewEncoder(w).Encode(s.t.Catalog())
}

func (s *server) handlePositions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(s.t.Positions(timeParam(r)))
}

func (s *server) handleObject(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	info, ok := s.t.Object(id, timeParam(r))
	if !ok {
		http.Error(w, "unknown id", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}
