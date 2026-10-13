// Package socrates reads CelesTrak SOCRATES Plus results and compares them
// with ours. See docs/08-socrates.md.
package socrates

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/tle"
)

// Conjunction is one row of a SOCRATES Plus CSV file.
type Conjunction struct {
	ID1, ID2         int
	Name1, Name2     string // SATCAT name, without the status suffix
	Status1, Status2 string // CelesTrak ops status: "+" operational, "-" not, "P" partial, ...
	DSE1, DSE2       float64
	TCA              time.Time
	RangeKm          float64 // miss distance at TCA
	RelSpeed         float64 // km/s
	MaxProb          float64
}

// Epoch1 and Epoch2 recover the epoch of the element set SOCRATES used for
// each object: DSE is "days since epoch" at TCA, given to 0.001 day, so
// these are good to about ±43 s.
func (c Conjunction) Epoch1() time.Time { return c.TCA.Add(-days(c.DSE1)) }
func (c Conjunction) Epoch2() time.Time { return c.TCA.Add(-days(c.DSE2)) }

func days(d float64) time.Duration { return time.Duration(d * 86400 * float64(time.Second)) }

// Read parses a SOCRATES Plus CSV file (the format described at
// celestrak.org/SOCRATES/socrates-format.php).
func Read(r io.Reader) ([]Conjunction, error) {
	cr := csv.NewReader(r)
	header, err := cr.Read()
	if err != nil {
		return nil, err
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.TrimSpace(h)] = i
	}
	for _, k := range []string{"NORAD_CAT_ID_1", "OBJECT_NAME_1", "DSE_1", "NORAD_CAT_ID_2",
		"OBJECT_NAME_2", "DSE_2", "TCA", "TCA_RANGE", "TCA_RELATIVE_SPEED", "MAX_PROB"} {
		if _, ok := col[k]; !ok {
			return nil, fmt.Errorf("socrates: missing column %s", k)
		}
	}
	var out []Conjunction
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("socrates line %d: %w", line, err)
		}
		get := func(k string) string { return strings.TrimSpace(rec[col[k]]) }
		var perr error
		num := func(k string) float64 {
			v, err := strconv.ParseFloat(get(k), 64)
			if err != nil && perr == nil {
				perr = fmt.Errorf("%s: %w", k, err)
			}
			return v
		}
		c := Conjunction{
			ID1: int(num("NORAD_CAT_ID_1")), ID2: int(num("NORAD_CAT_ID_2")),
			DSE1: num("DSE_1"), DSE2: num("DSE_2"),
			RangeKm: num("TCA_RANGE"), RelSpeed: num("TCA_RELATIVE_SPEED"), MaxProb: num("MAX_PROB"),
		}
		c.Name1, c.Status1 = splitStatus(get("OBJECT_NAME_1"))
		c.Name2, c.Status2 = splitStatus(get("OBJECT_NAME_2"))
		if c.TCA, err = time.Parse("2006-01-02 15:04:05.999", get("TCA")); err != nil && perr == nil {
			perr = err
		}
		if perr != nil {
			return nil, fmt.Errorf("socrates line %d: %w", line, perr)
		}
		out = append(out, c)
	}
}

// splitStatus splits "STARLINK-1234 [+]" into name and status.
func splitStatus(s string) (name, status string) {
	if i := strings.LastIndex(s, " ["); i >= 0 && strings.HasSuffix(s, "]") {
		return s[:i], s[i+2 : len(s)-1]
	}
	return s, ""
}

// UsedEpochs returns, for every object in the results, the epoch of the
// element set SOCRATES used. Each conjunction gives an estimate good to
// ±43 s; the median over all of an object's conjunctions is kept.
func UsedEpochs(cs []Conjunction) map[int]time.Time {
	all := map[int][]time.Time{}
	for _, c := range cs {
		all[c.ID1] = append(all[c.ID1], c.Epoch1())
		all[c.ID2] = append(all[c.ID2], c.Epoch2())
	}
	out := make(map[int]time.Time, len(all))
	for id, ts := range all {
		sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
		out[id] = ts[len(ts)/2]
	}
	return out
}

// Alignment says how an object's element set was chosen by AsOf.
type Alignment int

const (
	// Same: the element set SOCRATES used (epochs agree within a minute).
	Same Alignment = iota
	// Published: SOCRATES's set wasn't found (or the object isn't in its
	// results), so the latest set published by the cut-off time is used.
	Published
	// Newer: SOCRATES screened the object, but nothing published by the
	// cut-off was found, so the oldest available set is used. Usually an
	// object whose history starts after the downloaded window.
	Newer
	// Unpublished: the object isn't in SOCRATES's results and its first
	// element set was published after the cut-off (e.g. a new launch), so
	// SOCRATES can't have screened it. It's left out.
	Unpublished
)

// AsOf rebuilds the catalog as SOCRATES saw it. For each object it picks,
// from every element set available (history plus current), the one whose
// epoch matches the one SOCRATES used (used[id], from UsedEpochs). If there
// is none, it falls back to the latest set published (Created) at or before
// cutoff, then to the oldest set available. Sets without a creation time
// (CelesTrak data) count as published at their epoch.
func AsOf(sets []*tle.TLE, used map[int]time.Time, cutoff time.Time) (chosen []*tle.TLE, how map[int]Alignment) {
	byID := map[int][]*tle.TLE{}
	for _, s := range sets {
		byID[s.CatalogNum] = append(byID[s.CatalogNum], s)
	}
	how = map[int]Alignment{}
	ids := make([]int, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		cands := byID[id]
		created := func(s *tle.TLE) time.Time {
			if s.Created.IsZero() {
				return s.Epoch
			}
			return s.Created
		}
		var pick *tle.TLE
		if want, ok := used[id]; ok {
			best := math.Inf(1)
			for _, s := range cands {
				if d := math.Abs(s.Epoch.Sub(want).Seconds()); d < best {
					best, pick = d, s
				}
			}
			if best <= 60 {
				chosen, how[id] = append(chosen, pick), Same
				continue
			}
			pick = nil
		}
		for _, s := range cands {
			if c := created(s); !c.After(cutoff) && (pick == nil || s.Epoch.After(pick.Epoch)) {
				pick = s
			}
		}
		if pick != nil {
			chosen, how[id] = append(chosen, pick), Published
			continue
		}
		if _, ok := used[id]; !ok {
			how[id] = Unpublished
			continue
		}
		for _, s := range cands {
			if pick == nil || s.Epoch.Before(pick.Epoch) {
				pick = s
			}
		}
		chosen, how[id] = append(chosen, pick), Newer
	}
	return chosen, how
}
