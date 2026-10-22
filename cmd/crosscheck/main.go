// Command crosscheck propagates every TLE in a file and prints the TEME
// positions and velocities as CSV, so they can be compared with another
// SGP4 implementation (see crosscheck.py next to this file).
//
//	go run ./cmd/crosscheck -tle active.tle > go.csv
//	python crosscheck.py active.tle go.csv
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/supremeinferno/orbitscreen/pkg/sgp4"
	"github.com/supremeinferno/orbitscreen/pkg/tle"
)

func main() {
	path := flag.String("tle", "", "TLE file (2- or 3-line format)")
	days := flag.Float64("days", 7, "how far past each epoch to propagate")
	step := flag.Float64("step", 360, "step in minutes")
	flag.Parse()

	f, err := os.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	tles, err := tle.ReadAll(f)
	f.Close()
	if err != nil {
		log.Printf("some records skipped: %v", err)
	}

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	fmt.Fprintln(w, "catnum,tsince,x,y,z,vx,vy,vz,err")
	var bad int
	for _, el := range tles {
		sat, err := sgp4.New(el)
		if err != nil {
			bad++
			fmt.Fprintf(w, "%d,0,,,,,,,%d\n", el.CatalogNum, code(err))
			continue
		}
		for t := 0.0; t <= *days*1440; t += *step {
			r, v, err := sat.Propagate(t)
			if err != nil {
				fmt.Fprintf(w, "%d,%g,,,,,,,%d\n", el.CatalogNum, t, code(err))
				break
			}
			fmt.Fprintf(w, "%d,%g,%.9f,%.9f,%.9f,%.12f,%.12f,%.12f,0\n",
				el.CatalogNum, t, r[0], r[1], r[2], v[0], v[1], v[2])
		}
	}
	log.Printf("%d TLEs, %d failed at epoch", len(tles), bad)
}

func code(err error) int {
	var se *sgp4.Error
	if errors.As(err, &se) {
		return se.Code
	}
	return -1
}
