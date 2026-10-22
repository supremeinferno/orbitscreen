// Package catalog loads orbit data files into a deduplicated set of
// ready-to-propagate objects.
package catalog

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/supremeinferno/orbitscreen/pkg/sgp4"
	"github.com/supremeinferno/orbitscreen/pkg/tle"
)

// Object is one catalogued object, initialised for propagation.
type Object struct {
	ID      int // NORAD catalog number
	Name    string
	Primary bool // came from a primary file (SOCRATES: active payloads)
	Elems   *tle.TLE
	Sat     *sgp4.Satellite

	// Perigee and apogee radius (km from Earth's centre) at epoch, used
	// for coarse filtering.
	PerigeeKm, ApogeeKm float64

	// Group is non-zero for objects physically attached to each other:
	// ISS modules, docked spacecraft, a servicing vehicle docked to its
	// client. They share one identical element set, so they'd otherwise
	// show up as permanent 0 km "conjunctions". Screeners skip pairs in the
	// same group.
	Group int
}

// Stats summarises what Load did.
type Stats struct {
	Records    int // element sets read from all files
	Duplicates int // records dropped because a newer one for the same object exists
	BadRecords int // records that failed to parse
	InitFailed int // objects SGP4 rejected at epoch (usually decayed)
	Attached   int // objects sharing an identical element set with another (see Object.Group)
	SubOrbital int // objects whose perigee is below Earth's surface
	Objects    int // objects returned
	Primaries  int
}

// Load reads every file, keeps the newest element set per catalog number
// and initialises SGP4 for each. Files can be TLE (2- or 3-line) or OMM CSV;
// the format is detected from the content.
//
// Objects that appear in any file listed in primary are marked Primary.
// Unparseable records, objects SGP4 can't evaluate at epoch and objects
// with a perigee below the surface are counted in Stats and skipped rather
// than failing the whole load.
func Load(files []string, primary []string) ([]*Object, Stats, error) {
	var st Stats
	isPrimary := map[string]bool{}
	for _, p := range primary {
		isPrimary[filepath.Clean(p)] = true
		if !contains(files, p) {
			files = append(files, p)
		}
	}

	var all []*tle.TLE
	primaryIDs := map[int]bool{}
	for _, path := range files {
		recs, bad, err := ReadFile(path)
		if err != nil {
			return nil, st, err
		}
		st.Records += len(recs) + bad
		st.BadRecords += bad
		if isPrimary[filepath.Clean(path)] {
			for _, el := range recs {
				primaryIDs[el.CatalogNum] = true
			}
		}
		all = append(all, recs...)
	}
	objs, st2 := FromElements(all, primaryIDs)
	st2.Records, st2.BadRecords = st.Records, st.BadRecords
	return objs, st2, nil
}

// FromElements builds objects from element sets already in memory: it
// keeps the newest set per catalog number, marks the IDs in primary as
// primaries, initialises SGP4, drops objects SGP4 rejects or whose perigee
// is underground, and groups attached objects. Stats.Records and
// Stats.BadRecords are left at zero.
func FromElements(sets []*tle.TLE, primary map[int]bool) ([]*Object, Stats) {
	var st Stats
	byID := map[int]*Object{}
	for _, el := range sets {
		o, seen := byID[el.CatalogNum]
		if seen {
			st.Duplicates++
			if !el.Epoch.After(o.Elems.Epoch) {
				continue
			}
			o.Elems = el
		} else {
			o = &Object{ID: el.CatalogNum, Elems: el, Primary: primary[el.CatalogNum]}
			byID[el.CatalogNum] = o
		}
		if el.Name != "" {
			o.Name = el.Name
		}
	}

	out := make([]*Object, 0, len(byID))
	for _, o := range byID {
		sat, err := sgp4.New(o.Elems)
		if err != nil {
			st.InitFailed++
			continue
		}
		o.Sat = sat
		o.PerigeeKm, o.ApogeeKm = sat.PerigeeApogee()
		// An orbit that dips below the surface is re-entering within the
		// hour, and SGP4 positions along it are meaningless (they violate
		// the bounds the screener relies on). None are in the live catalog;
		// Vallado's test set has two.
		if o.PerigeeKm < sgp4.EarthRadiusKm {
			st.SubOrbital++
			continue
		}
		out = append(out, o)
		if o.Primary {
			st.Primaries++
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	st.Attached = groupAttached(out)
	st.Objects = len(out)
	return out, st
}

// groupAttached gives objects with identical orbital elements a shared,
// non-zero Group and returns how many objects got one.
func groupAttached(objs []*Object) int {
	type key struct {
		epoch                          int64
		n, e, i, raan, argp, ma, bstar float64
	}
	byKey := map[key][]*Object{}
	for _, o := range objs {
		el := o.Elems
		k := key{el.Epoch.UnixNano(), el.MeanMotion, el.Eccentricity, el.Inclination,
			el.RAAN, el.ArgPerigee, el.MeanAnomaly, el.BStar}
		byKey[k] = append(byKey[k], o)
	}
	attached, group := 0, 0
	for _, o := range objs { // in ID order, so group numbers are stable
		el := o.Elems
		k := key{el.Epoch.UnixNano(), el.MeanMotion, el.Eccentricity, el.Inclination,
			el.RAAN, el.ArgPerigee, el.MeanAnomaly, el.BStar}
		g := byKey[k]
		if len(g) < 2 || o.Group != 0 {
			continue
		}
		group++
		for _, m := range g {
			m.Group = group
		}
		attached += len(g)
	}
	return attached
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if filepath.Clean(s) == filepath.Clean(x) {
			return true
		}
	}
	return false
}

// ReadFile parses one file, sniffing OMM CSV versus TLE from its first line.
// It returns the good records and a count of bad ones.
func ReadFile(path string) ([]*tle.TLE, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	// OMM CSV has a header row naming its columns. CelesTrak's starts with
	// OBJECT_NAME; Space-Track's starts with "CCSDS_OMM_VERS" (quoted) and
	// is long, so look for NORAD_CAT_ID anywhere in the first line.
	first, _ := bufio.NewReaderSize(f, 1<<16).ReadString('\n')
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, 0, err
	}

	var recs []*tle.TLE
	if strings.Contains(strings.ToUpper(first), "NORAD_CAT_ID") {
		recs, err = tle.ReadOMMCSV(f)
	} else {
		recs, err = tle.ReadAll(f)
	}
	if err == nil {
		return recs, 0, nil
	}
	// Per-record errors come back joined; anything else is fatal.
	var joined interface{ Unwrap() []error }
	if errors.As(err, &joined) && len(recs) > 0 {
		return recs, len(joined.Unwrap()), nil
	}
	return nil, 0, fmt.Errorf("%s: %w", path, err)
}
