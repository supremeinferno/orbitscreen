// Command socrates screens the catalog exactly as a published SOCRATES Plus
// run did, and reports how many of SOCRATES's conjunctions we also find.
//
//	go run ./cmd/socrates \
//	    -socrates data/socrates/sort-minRange.csv \
//	    -asof 2026-10-12T08:22:44Z -start 2026-10-12T09:00:00Z \
//	    -data data/spacetrack/gp_history.csv,data/spacetrack/gp.csv,data/active.csv
//
// The run's data time (-asof) and window start (-start) are shown on
// celestrak.org/SOCRATES/search.php. See docs/08-socrates.md.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/catalog"
	"github.com/supremeinferno/orbitscreen/internal/screen"
	"github.com/supremeinferno/orbitscreen/internal/socrates"
	"github.com/supremeinferno/orbitscreen/internal/tle"
)

// activeStatus are CelesTrak ops-status codes SOCRATES treats as active.
var activeStatus = map[string]bool{"+": true, "P": true, "B": true, "S": true, "X": true}

func main() {
	socPath := flag.String("socrates", "data/socrates/sort-minRange.csv", "SOCRATES Plus CSV")
	asofStr := flag.String("asof", "", "the run's data time, RFC 3339 (\"Data current as of\")")
	startStr := flag.String("start", "", "the run's window start, RFC 3339 (\"Computation Interval: Start\")")
	dataList := flag.String("data", "", "comma-separated element-set files: Space-Track gp_history and gp, CelesTrak CSVs")
	activePath := flag.String("active", "data/active.csv", "CelesTrak active satellites (primaries)")
	tol := flag.Duration("tol", time.Minute, "TCA tolerance for a match")
	outDir := flag.String("out", "runs/socrates", "folder for misses.csv and extras.csv")
	flag.Parse()

	asof, err1 := time.Parse(time.RFC3339, *asofStr)
	start, err2 := time.Parse(time.RFC3339, *startStr)
	if err1 != nil || err2 != nil || *dataList == "" {
		flag.Usage()
		log.Fatal("-asof, -start and -data are required")
	}

	// ---- SOCRATES results ----
	f, err := os.Open(*socPath)
	if err != nil {
		log.Fatal(err)
	}
	soc, err := socrates.Read(f)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}
	used := socrates.UsedEpochs(soc)
	status := map[int]string{}
	for _, c := range soc {
		status[c.ID1], status[c.ID2] = c.Status1, c.Status2
	}
	log.Printf("SOCRATES: %d conjunctions involving %d objects", len(soc), len(used))

	// ---- the catalog as SOCRATES saw it ----
	var sets []*tle.TLE
	for _, p := range append(strings.Split(*dataList, ","), *activePath) {
		recs, bad, err := catalog.ReadFile(strings.TrimSpace(p))
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("read %s: %d element sets (%d bad)", p, len(recs), bad)
		sets = append(sets, recs...)
	}
	chosen, how := socrates.AsOf(sets, used, asof)
	counts := map[socrates.Alignment]int{}
	for _, a := range how {
		counts[a]++
	}
	primary := map[int]bool{}
	if recs, _, err := catalog.ReadFile(*activePath); err == nil {
		for _, r := range recs {
			primary[r.CatalogNum] = true
		}
	}
	for id, s := range status {
		if activeStatus[s] {
			primary[id] = true
		}
	}
	objs, st := catalog.FromElements(chosen, primary)
	byID := map[int]*catalog.Object{}
	for _, o := range objs {
		byID[o.ID] = o
	}
	missing, rejected := 0, 0
	for id := range used {
		if byID[id] == nil {
			if _, ok := how[id]; ok {
				rejected++
			} else {
				missing++
			}
		}
	}
	log.Printf("catalog as of %s: %d objects (%d primaries); element sets: %d same as SOCRATES, %d latest published, %d newer, %d left out (published later); of SOCRATES's objects, %d are not in our data and %d were rejected (SGP4 failed at epoch: %d, sub-orbital: %d)",
		asof.Format(time.RFC3339), st.Objects, st.Primaries, counts[socrates.Same], counts[socrates.Published],
		counts[socrates.Newer], counts[socrates.Unpublished], missing, rejected, st.InitFailed, st.SubOrbital)

	// ---- our screen, SOCRATES-style ----
	cfg := screen.Config{Start: start, Duration: 7 * 24 * time.Hour, Step: 10 * time.Second,
		Threshold: 5, PrimaryOnly: true, Workers: runtime.NumCPU()}
	ours, ss := screen.Screen(objs, cfg, screen.Grid)
	log.Printf("our screen: %d conjunctions in %v", len(ours), ss.Elapsed.Round(time.Second))

	// ---- compare ----
	matches, extra := socrates.Compare(soc, ours, *tol)
	aligned := func(c socrates.Conjunction) bool {
		return how[c.ID1] == socrates.Same && how[c.ID2] == socrates.Same
	}
	category := func(c socrates.Conjunction) string {
		switch {
		case byID[c.ID1] == nil || byID[c.ID2] == nil:
			return "object not in our data"
		case aligned(c):
			return "both objects: same element sets"
		default:
			return "different element sets"
		}
	}

	fmt.Printf("\nSOCRATES run: data as of %s, window %s to %s\n", asof.Format(time.RFC3339),
		start.Format(time.RFC3339), start.Add(cfg.Duration).Format(time.RFC3339))
	fmt.Printf("SOCRATES conjunctions: %d    ours: %d    matched (same pair, TCA within %v): %d\n",
		len(soc), len(ours), *tol, countMatched(matches))

	fmt.Printf("\nRecall: share of SOCRATES conjunctions we also found\n")
	table(matches, category, []string{"both objects: same element sets", "different element sets", "object not in our data"})
	fmt.Printf("  %-34s %7d / %-7d %6.2f%%\n", "ALL", countMatched(matches), len(matches), pct(countMatched(matches), len(matches)))

	fmt.Printf("\nRecall by SOCRATES miss distance (both objects: same element sets)\n")
	sameData := func(c socrates.Conjunction) bool { return category(c) == "both objects: same element sets" }
	table(filter(matches, sameData), func(c socrates.Conjunction) string {
		return fmt.Sprintf("%d-%d km", int(c.RangeKm), int(c.RangeKm)+1)
	}, []string{"0-1 km", "1-2 km", "2-3 km", "3-4 km", "4-5 km"})

	fmt.Printf("\nMatched conjunctions, both objects with the same element sets:\n")
	var dT, dR []float64
	for _, m := range matches {
		if m.Ours != nil && sameData(m.Soc) {
			dT = append(dT, m.Ours.TCA.Sub(m.Soc.TCA).Abs().Seconds())
			dR = append(dR, math.Abs(m.Ours.MissKm-m.Soc.RangeKm)*1000)
		}
	}
	fmt.Printf("  |ΔTCA|       median %8.3f s   p95 %8.3f s   max %8.3f s\n", q(dT, .5), q(dT, .95), q(dT, 1))
	fmt.Printf("  |Δmiss|      median %8.1f m   p95 %8.1f m   max %8.1f m\n", q(dR, .5), q(dR, .95), q(dR, 1))

	// ---- why did we miss the ones we missed? ----
	diag := diagnose(matches, byID)
	fmt.Printf("\nMisses, by our own closest approach for that pair within ±2 min of SOCRATES's TCA:\n")
	for _, cat := range []string{"both objects: same element sets", "different element sets"} {
		bins := map[string]int{}
		for i, m := range matches {
			if m.Ours == nil && category(m.Soc) == cat {
				bins[diag[i].bin]++
			}
		}
		fmt.Printf("  %s:\n", cat)
		for _, b := range []string{"under 5 km (should have been found)", "5.00-5.25 km (borderline)", "over 5.25 km", "propagation failed"} {
			if bins[b] > 0 {
				fmt.Printf("    %-38s %7d\n", b, bins[b])
			}
		}
	}

	fmt.Printf("\nOurs that SOCRATES doesn't list: %d\n", len(extra))
	ex := map[string]int{}
	for _, c := range extra {
		ex[extraKind(c, used)]++
	}
	keys := make([]string, 0, len(ex))
	for k := range ex {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return ex[keys[i]] > ex[keys[j]] })
	for _, k := range keys {
		fmt.Printf("  %-58s %7d\n", k, ex[k])
	}

	if err := writeOutputs(*outDir, matches, diag, category, extra); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s/misses.csv and %s/extras.csv", *outDir, *outDir)
}

func countMatched(ms []socrates.Match) int {
	n := 0
	for _, m := range ms {
		if m.Ours != nil {
			n++
		}
	}
	return n
}

func pct(a, b int) float64 { return 100 * float64(a) / float64(max(b, 1)) }

func filter(ms []socrates.Match, keep func(socrates.Conjunction) bool) []socrates.Match {
	var out []socrates.Match
	for _, m := range ms {
		if keep(m.Soc) {
			out = append(out, m)
		}
	}
	return out
}

func table(ms []socrates.Match, key func(socrates.Conjunction) string, order []string) {
	found, total := map[string]int{}, map[string]int{}
	for _, m := range ms {
		k := key(m.Soc)
		total[k]++
		if m.Ours != nil {
			found[k]++
		}
	}
	for _, k := range order {
		fmt.Printf("  %-34s %7d / %-7d %6.2f%%\n", k, found[k], total[k], pct(found[k], total[k]))
	}
}

func q(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[int(p*float64(len(s)-1))]
}

type diagnosis struct {
	km  float64
	bin string
}

// diagnose computes, for every SOCRATES conjunction we missed, our own
// closest approach for that pair near SOCRATES's TCA.
func diagnose(ms []socrates.Match, byID map[int]*catalog.Object) []diagnosis {
	out := make([]diagnosis, len(ms))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Go(func() {
			for i := range jobs {
				c := ms[i].Soc
				a, b := byID[c.ID1], byID[c.ID2]
				if a == nil || b == nil {
					continue
				}
				_, km, ok := socrates.ClosestApproach(a, b, c.TCA, 2*time.Minute)
				d := diagnosis{km: km}
				switch {
				case !ok:
					d.bin = "propagation failed"
				case km < 5:
					d.bin = "under 5 km (should have been found)"
				case km < 5.25:
					d.bin = "5.00-5.25 km (borderline)"
				default:
					d.bin = "over 5.25 km"
				}
				out[i] = d
			}
		})
	}
	for i, m := range ms {
		if m.Ours == nil {
			jobs <- i
		}
	}
	close(jobs)
	wg.Wait()
	return out
}

// constellation guesses a fleet from a name: "STARLINK-1234" -> "STARLINK".
func constellation(name string) string {
	if i := strings.IndexAny(name, "- "); i > 0 {
		return name[:i]
	}
	return name
}

func extraKind(c screen.Conjunction, used map[int]time.Time) string {
	_, inA := used[c.A.ID]
	_, inB := used[c.B.ID]
	switch {
	case c.A.Primary && c.B.Primary && constellation(c.A.Name) == constellation(c.B.Name):
		return "both active, same fleet (" + constellation(c.A.Name) + "): SOCRATES may skip"
	case !inA || !inB:
		return "an object that's in none of SOCRATES's conjunctions"
	case c.MissKm > 4.75:
		return "other, our miss 4.75-5 km (borderline)"
	default:
		return "other"
	}
}

func writeOutputs(dir string, ms []socrates.Match, diag []diagnosis, category func(socrates.Conjunction) string, extra []screen.Conjunction) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, "misses.csv"))
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	w.Write([]string{"tca_utc", "socrates_km", "our_closest_km", "category", "id_1", "name_1", "status_1", "id_2", "name_2", "status_2"})
	for i, m := range ms {
		if m.Ours != nil {
			continue
		}
		c := m.Soc
		w.Write([]string{c.TCA.Format("2006-01-02T15:04:05.000Z"), strconv.FormatFloat(c.RangeKm, 'f', 3, 64),
			strconv.FormatFloat(diag[i].km, 'f', 3, 64), category(c),
			strconv.Itoa(c.ID1), c.Name1, c.Status1, strconv.Itoa(c.ID2), c.Name2, c.Status2})
	}
	w.Flush()
	f.Close()
	f, err = os.Create(filepath.Join(dir, "extras.csv"))
	if err != nil {
		return err
	}
	w = csv.NewWriter(f)
	w.Write([]string{"tca_utc", "miss_km", "rel_speed_km_s", "id_a", "name_a", "id_b", "name_b"})
	for _, c := range extra {
		w.Write([]string{c.TCA.Format("2006-01-02T15:04:05.000Z"), strconv.FormatFloat(c.MissKm, 'f', 3, 64),
			strconv.FormatFloat(c.RelSpeed, 'f', 3, 64), strconv.Itoa(c.A.ID), c.A.Name, strconv.Itoa(c.B.ID), c.B.Name})
	}
	w.Flush()
	return f.Close()
}
