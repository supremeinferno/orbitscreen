// Package catalog loads orbit data files into a deduplicated set of
// ready-to-propagate objects.
package catalog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/supremeinferno/orbitscreen/internal/sgp4"
	"github.com/supremeinferno/orbitscreen/internal/tle"
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

	byID := map[int]*Object{}
	for _, path := range files {
		recs, bad, err := readFile(path)
		if err != nil {
			return nil, st, err
		}
		st.Records += len(recs) + bad
		st.BadRecords += bad
		prim := isPrimary[filepath.Clean(path)]
		for _, el := range recs {
			o, seen := byID[el.CatalogNum]
			if seen {
				st.Duplicates++
				o.Primary = o.Primary || prim
				if !el.Epoch.After(o.Elems.Epoch) {
					continue
				}
				o.Elems = el
			} else {
				o = &Object{ID: el.CatalogNum, Elems: el, Primary: prim}
				byID[el.CatalogNum] = o
			}
			if el.Name != "" {
				o.Name = el.Name
			}
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
	return out, st, nil
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

// readFile parses one file, sniffing OMM CSV versus TLE from its first line.
// It returns the good records and a count of bad ones.
func readFile(path string) ([]*tle.TLE, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	head := make([]byte, 64)
	n, _ := io.ReadFull(f, head)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, 0, err
	}

	var recs []*tle.TLE
	if strings.Contains(strings.ToUpper(string(head[:n])), "NORAD_CAT_ID") ||
		strings.HasPrefix(string(head[:n]), "OBJECT_NAME,") || strings.HasPrefix(string(head[:n]), "CCSDS_OMM_VERS,") {
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
