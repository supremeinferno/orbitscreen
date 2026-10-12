// Command fetch downloads orbit data from CelesTrak into a local folder.
//
//	go run ./cmd/fetch            # into ./data
//
// Everything is fetched as OMM CSV, since objects with 6-digit catalog
// numbers aren't published as TLEs. A file younger than -max-age is reused
// instead of re-downloaded: CelesTrak only updates every couple of hours
// and asks users not to fetch the same data more often than that.
//
// CelesTrak has no single "every object" download. The default sources
// below are the active satellites (the SOCRATES primaries), every object
// near GEO, the three largest debris clouds and recent launches. That is
// most, but not all, of the public catalog. The full catalog needs a
// Space-Track.org account; see docs/04-catalog.md.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sources maps a local file name to its CelesTrak query.
var sources = []struct{ file, query string }{
	{"active.csv", "GROUP=active"},                         // all active payloads
	{"gpz-plus.csv", "SPECIAL=gpz-plus"},                   // everything in/near the GEO belt, dead or alive
	{"cosmos-2251-debris.csv", "GROUP=cosmos-2251-debris"}, // 2009 Iridium-Cosmos collision
	{"iridium-33-debris.csv", "GROUP=iridium-33-debris"},   // ditto
	{"fengyun-1c-debris.csv", "GROUP=fengyun-1c-debris"},   // 2007 Chinese ASAT test
	{"last-30-days.csv", "GROUP=last-30-days"},             // recent launches, incl. rocket bodies
}

func main() {
	dir := flag.String("dir", "data", "output folder")
	maxAge := flag.Duration("max-age", 2*time.Hour, "reuse files younger than this")
	flag.Parse()

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		log.Fatal(err)
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	for i, s := range sources {
		path := filepath.Join(*dir, s.file)
		if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) < *maxAge {
			log.Printf("%-24s cached (%s old)", s.file, time.Since(st.ModTime()).Round(time.Minute))
			continue
		}
		if i > 0 {
			time.Sleep(time.Second) // be polite
		}
		url := "https://celestrak.org/NORAD/elements/gp.php?" + s.query + "&FORMAT=csv"
		n, err := download(client, url, path)
		if err != nil {
			log.Fatalf("%s: %v", s.file, err)
		}
		log.Printf("%-24s %d objects", s.file, n)
	}
}

// download writes url to path via a temp file, so an interrupted download
// never leaves a truncated file that looks fresh. It returns the number of
// data rows.
func download(c *http.Client, url, path string) (int, error) {
	resp, err := c.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	// CelesTrak answers bad or rate-limited queries with a 200 and a
	// plain-text message, so check it really is OMM CSV.
	if !strings.HasPrefix(string(body), "OBJECT_NAME,") {
		return 0, fmt.Errorf("GET %s: not OMM CSV: %.200q", url, body)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return 0, err
	}
	return strings.Count(string(body), "\n") - 1, os.Rename(tmp, path)
}
