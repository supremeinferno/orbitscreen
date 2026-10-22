package tle

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ReadOMMCSV reads orbit data in CCSDS OMM (Orbit Mean-Elements Message)
// CSV form, as served by CelesTrak's FORMAT=csv and Space-Track's
// format/csv. It holds the same mean elements as a TLE, so it fills the
// same TLE struct.
//
// OMM matters because the TLE format can't hold catalog numbers above
// 99999 (or 339999 with Alpha-5): newly catalogued objects are only
// published as OMM. Columns are found by header name, so extra columns
// (Space-Track adds many) and different column orders are fine.
//
// Like ReadAll, bad rows don't stop the read: every good row is returned
// along with an error joining one message per bad row.
func ReadOMMCSV(r io.Reader) ([]*TLE, error) {
	cr := csv.NewReader(r)
	cr.ReuseRecord = true
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("omm: reading header: %w", err)
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.ToUpper(strings.TrimSpace(h))] = i
	}
	need := []string{"NORAD_CAT_ID", "EPOCH", "MEAN_MOTION", "ECCENTRICITY", "INCLINATION",
		"RA_OF_ASC_NODE", "ARG_OF_PERICENTER", "MEAN_ANOMALY", "BSTAR"}
	for _, k := range need {
		if _, ok := idx[k]; !ok {
			return nil, fmt.Errorf("omm: missing column %s", k)
		}
	}

	var out []*TLE
	var errs []error
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("omm line %d: %w", line, err))
			continue
		}
		t, err := ommRecord(rec, idx)
		if err != nil {
			errs = append(errs, fmt.Errorf("omm line %d: %w", line, err))
			continue
		}
		out = append(out, t)
	}
	return out, errors.Join(errs...)
}

func ommRecord(rec []string, idx map[string]int) (*TLE, error) {
	get := func(k string) string {
		if i, ok := idx[k]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	var f fields
	num := func(k string) float64 {
		s := get(k)
		if s == "" {
			return 0 // optional columns (NDot, NDDot) may be blank
		}
		return f.float(k, s)
	}
	t := &TLE{
		Name:           get("OBJECT_NAME"),
		CatalogNum:     f.intOrZero("NORAD_CAT_ID", get("NORAD_CAT_ID")),
		IntlDesignator: get("OBJECT_ID"), // OMM style: "1998-067A"
		NDot:           num("MEAN_MOTION_DOT"),
		NDDot:          num("MEAN_MOTION_DDOT"),
		BStar:          f.float("BSTAR", get("BSTAR")),
		ElementSetNum:  f.intOrZero("ELEMENT_SET_NO", get("ELEMENT_SET_NO")),
		Inclination:    f.float("INCLINATION", get("INCLINATION")),
		RAAN:           f.float("RA_OF_ASC_NODE", get("RA_OF_ASC_NODE")),
		Eccentricity:   f.float("ECCENTRICITY", get("ECCENTRICITY")),
		ArgPerigee:     f.float("ARG_OF_PERICENTER", get("ARG_OF_PERICENTER")),
		MeanAnomaly:    f.float("MEAN_ANOMALY", get("MEAN_ANOMALY")),
		MeanMotion:     f.float("MEAN_MOTION", get("MEAN_MOTION")),
		RevNum:         f.intOrZero("REV_AT_EPOCH", get("REV_AT_EPOCH")),
	}
	if c := get("CLASSIFICATION_TYPE"); c != "" {
		t.Classification = c[0]
	}
	if f.err != nil {
		return nil, f.err
	}
	if t.CatalogNum <= 0 {
		return nil, fmt.Errorf("bad NORAD_CAT_ID %q", get("NORAD_CAT_ID"))
	}
	ep, err := parseOMMEpoch(get("EPOCH"))
	if err != nil {
		return nil, err
	}
	t.Epoch = ep
	if c := get("CREATION_DATE"); c != "" {
		if t.Created, err = parseOMMEpoch(c); err != nil {
			return nil, fmt.Errorf("CREATION_DATE: %w", err)
		}
	}
	return t, nil
}

// parseOMMEpoch parses an OMM time such as "2026-10-11T22:55:09.150528"
// (Space-Track sometimes uses a space instead of the T). OMM times are UTC
// and carry no zone suffix.
func parseOMMEpoch(s string) (time.Time, error) {
	s = strings.Replace(strings.TrimSuffix(s, "Z"), " ", "T", 1)
	t, err := time.Parse("2006-01-02T15:04:05.999999999", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("EPOCH %q: %w", s, err)
	}
	return t, nil
}
