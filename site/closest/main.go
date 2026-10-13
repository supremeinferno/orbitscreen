// Command closest screens the catalog for the public site: the next 24
// hours, SOCRATES-style (pairs involving an active satellite, but not two
// active satellites of the same fleet, which their operator handles), and
// writes the closest approaches as JSON for the tracker's list.
//
//	go run ./site/closest -out _site/closest.json
package main

import (
	"encoding/json"
	"flag"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"time"
	"unicode"

	"github.com/supremeinferno/orbitscreen/internal/catalog"
	"github.com/supremeinferno/orbitscreen/internal/screen"
)

type object struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type approach struct {
	TCA      time.Time `json:"tca"`
	MissKm   float64   `json:"km"`
	RelSpeed float64   `json:"kms"`
	A        object    `json:"a"`
	B        object    `json:"b"`
}

type output struct {
	Generated  time.Time  `json:"generated"`
	Start      time.Time  `json:"start"`
	Hours      float64    `json:"hours"`
	Objects    int        `json:"objects"`
	Total      int        `json:"total"` // after the fleet filter, one per pair
	Approaches []approach `json:"approaches"`
}

// fleet is a name's leading letters: STARLINK-1234 → STARLINK.
func fleet(name string) string {
	for i, r := range name {
		if !unicode.IsLetter(r) {
			return name[:i]
		}
	}
	return name
}

// closest returns the top closest approaches, one per pair (slow pairs pass
// many times), leaving out pairs of active satellites of the same fleet,
// and how many pairs are left in all.
func closest(found []screen.Conjunction, top int) ([]approach, int) {
	found = slices.Clone(found)
	sort.SliceStable(found, func(i, j int) bool { return found[i].MissKm < found[j].MissKm })
	var out []approach
	seen := map[[2]int]bool{}
	for _, c := range found {
		if c.A.Primary && c.B.Primary && fleet(c.A.Name) == fleet(c.B.Name) {
			continue
		}
		if k := [2]int{c.A.ID, c.B.ID}; !seen[k] {
			seen[k] = true
			if len(out) < top {
				out = append(out, approach{c.TCA.Truncate(time.Millisecond),
					math.Round(c.MissKm*1000) / 1000, math.Round(c.RelSpeed*1000) / 1000,
					object{c.A.ID, c.A.Name}, object{c.B.ID, c.B.Name}})
			}
		}
	}
	return out, len(seen)
}

func main() {
	dataDir := flag.String("data", "data", "folder of OMM CSV files")
	hours := flag.Float64("hours", 24, "screening window, hours from now")
	top := flag.Int("top", 30, "how many approaches to keep")
	out := flag.String("out", "closest.json", "output file")
	flag.Parse()

	files, _ := filepath.Glob(filepath.Join(*dataDir, "*.csv"))
	objs, st, err := catalog.Load(files, []string{filepath.Join(*dataDir, "active.csv")})
	if err != nil {
		log.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Minute)
	cfg := screen.Config{Start: start, Duration: time.Duration(*hours * float64(time.Hour)), Step: 10 * time.Second,
		Threshold: 5, PrimaryOnly: true, Workers: runtime.NumCPU()}
	found, ss := screen.Screen(objs, cfg, screen.Grid)
	log.Printf("%d objects, %d conjunctions in %v", st.Objects, len(found), ss.Elapsed.Round(time.Second))

	res := output{Generated: time.Now().UTC(), Start: start, Hours: *hours, Objects: st.Objects}
	res.Approaches, res.Total = closest(found, *top)
	b, _ := json.Marshal(res)
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s: %d pairs, kept the closest %d", *out, res.Total, len(res.Approaches))
}
