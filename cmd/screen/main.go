// Command screen screens a catalog for close approaches.
//
//	go run ./cmd/fetch                        # download data/ first
//	go run ./cmd/screen -days 7               # full screen, all pairs
//	go run ./cmd/screen -primary-only         # SOCRATES-style: active payloads vs everything
//	go run ./cmd/screen -max-steps 60         # time the first 60 steps and project the full run
//
// Every *.csv and *.tle file in -data is loaded; objects in -primary are
// the "active payloads".
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/catalog"
	"github.com/supremeinferno/orbitscreen/internal/screen"
)

func main() {
	dataDir := flag.String("data", "data", "folder of .csv (OMM) and .tle files")
	primary := flag.String("primary", "data/active.csv", "file listing the primary (active) objects")
	primaryOnly := flag.Bool("primary-only", false, "only screen pairs involving a primary object (SOCRATES mode)")
	days := flag.Float64("days", 7, "screening window, days")
	step := flag.Duration("step", 10*time.Second, "sampling step")
	threshold := flag.Float64("threshold", 5, "report approaches closer than this, km")
	startStr := flag.String("start", "", "window start, RFC 3339 (default: now, to the minute)")
	maxSteps := flag.Int("max-steps", 0, "stop after this many steps and project the full run time")
	sample := flag.Int("sample", 0, "screen a random subset of this many objects (for scaling tests)")
	out := flag.String("out", "", "write conjunctions to this CSV file")
	cpuProfile := flag.String("cpuprofile", "", "write a CPU profile here")
	flag.Parse()

	start := time.Now().UTC().Truncate(time.Minute)
	if *startStr != "" {
		var err error
		if start, err = time.Parse(time.RFC3339, *startStr); err != nil {
			log.Fatalf("-start: %v", err)
		}
	}

	files, _ := filepath.Glob(filepath.Join(*dataDir, "*.csv"))
	tles, _ := filepath.Glob(filepath.Join(*dataDir, "*.tle"))
	files = append(files, tles...)
	if len(files) == 0 {
		log.Fatalf("no data in %s; run: go run ./cmd/fetch", *dataDir)
	}
	var prim []string
	if *primary != "" {
		prim = []string{*primary}
	}
	t0 := time.Now()
	objs, st, err := catalog.Load(files, prim)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("loaded %d objects (%d primary, %d attached) from %d files in %v: %d records, %d duplicates, %d bad, %d failed at epoch, %d sub-orbital",
		st.Objects, st.Primaries, st.Attached, len(files), time.Since(t0).Round(time.Millisecond),
		st.Records, st.Duplicates, st.BadRecords, st.InitFailed, st.SubOrbital)

	if *sample > 0 && *sample < len(objs) {
		rng := rand.New(rand.NewPCG(1, 2)) // fixed seed: same subset every run
		rng.Shuffle(len(objs), func(i, j int) { objs[i], objs[j] = objs[j], objs[i] })
		objs = objs[:*sample]
		log.Printf("using a random sample of %d objects", len(objs))
	}

	cfg := screen.Config{
		Start:       start,
		Duration:    time.Duration(*days * 24 * float64(time.Hour)),
		Step:        *step,
		Threshold:   *threshold,
		PrimaryOnly: *primaryOnly,
		MaxSteps:    *maxSteps,
	}
	totalSteps := int(math.Ceil(cfg.Duration.Seconds() / cfg.Step.Seconds()))

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	conj, ss := screen.BruteForce(objs, cfg)

	perStep := ss.Elapsed / time.Duration(ss.Steps)
	fmt.Printf("\nbrute force: %d objects, %d of %d steps of %v, window from %s\n",
		len(objs), ss.Steps, totalSteps, cfg.Step, start.Format(time.RFC3339))
	fmt.Printf("  time            %v  (%v per step)\n", ss.Elapsed.Round(time.Millisecond), perStep.Round(time.Microsecond))
	if ss.Steps < totalSteps {
		proj := perStep * time.Duration(totalSteps)
		fmt.Printf("  projected full  %v  (%.1f hours) for %.0f days\n", proj.Round(time.Second), proj.Hours(), *days)
	}
	fmt.Printf("  propagations    %d\n", ss.Propagated)
	fmt.Printf("  pair checks     %d  (%.1f ns each, including sampling)\n", ss.PairChecks, float64(ss.Elapsed.Nanoseconds())/float64(max(ss.PairChecks, 1)))
	fmt.Printf("  candidates      %d  passed the coarse bound\n", ss.Candidates)
	fmt.Printf("  refined         %d  passed the chord bound\n", ss.Refined)
	fmt.Printf("  conjunctions    %d  closer than %g km\n", len(conj), *threshold)

	n := min(len(conj), 10)
	if n > 0 {
		fmt.Printf("\nclosest %d:\n", n)
		byMiss := append([]screen.Conjunction(nil), conj...)
		sortByMiss(byMiss)
		for _, c := range byMiss[:n] {
			fmt.Printf("  %s  %6.3f km  %5.2f km/s  %6d %-24s %6d %s\n", c.TCA.Format("2006-01-02 15:04:05"),
				c.MissKm, c.RelSpeed, c.A.ID, c.A.Name, c.B.ID, c.B.Name)
		}
	}

	if *out != "" {
		if err := writeCSV(*out, conj); err != nil {
			log.Fatal(err)
		}
		log.Printf("wrote %s", *out)
	}
}

func sortByMiss(cs []screen.Conjunction) {
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && cs[j].MissKm < cs[j-1].MissKm; j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}

func writeCSV(path string, cs []screen.Conjunction) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	w.Write([]string{"tca_utc", "miss_km", "rel_speed_km_s", "id_a", "name_a", "primary_a", "id_b", "name_b", "primary_b"})
	for _, c := range cs {
		w.Write([]string{
			c.TCA.Format("2006-01-02T15:04:05.000Z"),
			strconv.FormatFloat(c.MissKm, 'f', 4, 64),
			strconv.FormatFloat(c.RelSpeed, 'f', 4, 64),
			strconv.Itoa(c.A.ID), c.A.Name, strconv.FormatBool(c.A.Primary),
			strconv.Itoa(c.B.ID), c.B.Name, strconv.FormatBool(c.B.Primary),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
