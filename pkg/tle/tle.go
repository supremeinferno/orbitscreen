// Package tle parses NORAD Two-Line Element sets.
//
// A TLE is a FIXED-WIDTH format: every field lives in specific columns.
// Do NOT split on spaces — field widths are fixed and some fields
// (negative numbers, small catalog numbers) change how many spaces appear.
//
// Column numbers in comments below are 1-based (as in official docs).
// In Go, column c..d becomes the slice line[c-1 : d].
package tle

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// TLE holds every field from the three-line (name + 2 lines) format.
type TLE struct {
	Name string

	// ---- Line 1 ----
	CatalogNum     int       // cols 3-7    NORAD catalog number (ISS = 25544)
	Classification byte      // col  8      U = unclassified
	IntlDesignator string    // cols 10-17  launch year, launch number, piece ("98067A")
	Epoch          time.Time // cols 19-32  YYDDD.DDDDDDDD -> the moment this TLE describes (UTC)
	NDot           float64   // cols 34-43  1st derivative of mean motion / 2 (rev/day^2)
	NDDot          float64   // cols 45-52  2nd derivative of mean motion / 6, implied-exponent format
	BStar          float64   // cols 54-61  drag term, implied-exponent format
	ElementSetNum  int       // cols 65-68

	// ---- Line 2 (the six orbital elements live here) ----
	Inclination  float64 // cols 9-16   degrees
	RAAN         float64 // cols 18-25  degrees (right ascension of ascending node)
	Eccentricity float64 // cols 27-33  IMPLIED leading decimal: "0006703" -> 0.0006703
	ArgPerigee   float64 // cols 35-42  degrees
	MeanAnomaly  float64 // cols 44-51  degrees
	MeanMotion   float64 // cols 53-63  revolutions per day
	RevNum       int     // cols 64-68  revolution number at epoch

	// Created is when the element set was published (OMM CREATION_DATE).
	// TLE text doesn't carry it, so it's zero for TLE input.
	Created time.Time
}

// ErrChecksum is returned when a line's last digit doesn't match Checksum(line).
var ErrChecksum = errors.New("tle: checksum mismatch")

// Checksum returns the expected checksum digit (column 69) of a TLE line.
// Rule: over columns 1-68, add up every digit; each '-' counts as 1;
// everything else (letters, spaces, '.', '+') counts as 0. Result mod 10.
func Checksum(line string) int {
	sum := 0
	for i := 0; i < len(line) && i < 68; i++ {
		switch c := line[i]; {
		case c >= '0' && c <= '9':
			sum += int(c - '0')
		case c == '-':
			sum++
		}
	}
	return sum % 10
}

// parseImpliedExp parses the "implied decimal + exponent" format used by
// NDDot and BStar.  Examples:
//
//	"-11606-4"  -> -0.11606e-4
//	" 12345-3"  ->  0.12345e-3
//	" 00000-0"  ->  0
//
// Layout: [sign][5 digits][exponent sign][1 exponent digit].
// The decimal point is implied BEFORE the 5 digits.
func parseImpliedExp(s string) (float64, error) {
	if len(s) != 8 {
		return 0, fmt.Errorf("tle: implied-exponent field %q is %d chars, want 8", s, len(s))
	}
	sign := ""
	switch s[0] {
	case '-':
		sign = "-"
	case '+', ' ':
	default:
		return 0, fmt.Errorf("tle: implied-exponent field %q: bad sign %q", s, s[0])
	}
	mantissa := strings.TrimSpace(s[1:6])
	if mantissa == "" {
		return 0, nil // some old TLEs leave unused fields blank
	}
	expSign := s[6]
	if expSign == ' ' {
		expSign = '+'
	}
	// Rebuild it as normal scientific notation and let strconv do the
	// rounding, e.g. "-11606-4" -> "-0.11606e-4".
	v, err := strconv.ParseFloat(sign+"0."+mantissa+"e"+string(expSign)+s[7:8], 64)
	if err != nil {
		return 0, fmt.Errorf("tle: implied-exponent field %q: %w", s, err)
	}
	return v, nil
}

// epochToTime converts a 2-digit year and a fractional day-of-year into UTC.
// Year rule: 57-99 -> 1957-1999, 00-56 -> 2000-2056.
// Day 1.0 is midnight at the START of January 1st (not day 0!).
func epochToTime(yy int, dayOfYear float64) time.Time {
	year := 2000 + yy
	if yy >= 57 {
		year = 1900 + yy
	}
	// Add whole days and the fraction separately so the fraction keeps
	// its full float64 precision (~5 ns) instead of being swamped by the
	// day count.
	whole := math.Floor(dayOfYear)
	frac := time.Duration(math.Round((dayOfYear - whole) * 86400e9))
	return time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, int(whole)-1).
		Add(frac)
}

// col returns the 1-based, inclusive column range from..to of line.
func col(line string, from, to int) string { return line[from-1 : to] }

// fields parses columns and remembers the first error, so Parse can read
// every field in a straight line and check for failure once at the end.
type fields struct{ err error }

func (f *fields) fail(what, s string, err error) {
	if f.err == nil {
		f.err = fmt.Errorf("tle: %s %q: %w", what, s, err)
	}
}

func (f *fields) float(what, s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		f.fail(what, s, err)
	}
	return v
}

// intOrZero parses an integer field; a blank field means 0 (old TLEs often
// leave the element set and revolution numbers empty).
func (f *fields) intOrZero(what, s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		f.fail(what, s, err)
	}
	return v
}

func (f *fields) impliedExp(what, s string) float64 {
	v, err := parseImpliedExp(s)
	if err != nil && f.err == nil {
		f.err = fmt.Errorf("%s: %w", what, err)
	}
	return v
}

// alpha5 is the letter set for "Alpha-5" catalog numbers, used once NORAD
// numbers pass 99999: "A0001" = 100001, "Z9999" = 339999. I and O are
// skipped because they look like 1 and 0.
const alpha5 = "ABCDEFGHJKLMNPQRSTUVWXYZ"

func (f *fields) catalogNum(s string) int {
	if i := strings.IndexByte(alpha5, s[0]); i >= 0 {
		rest := f.intOrZero("catalog number", s[1:])
		return (10+i)*10000 + rest
	}
	return f.intOrZero("catalog number", s)
}

// Parse builds a TLE from a name and two lines.
// It checks both lines are 69 chars, checks line numbers ('1' and '2'),
// verifies both checksums (returning ErrChecksum on mismatch), and fills every field.
func Parse(name, line1, line2 string) (*TLE, error) {
	line1 = strings.TrimRight(line1, " \r\n")
	line2 = strings.TrimRight(line2, " \r\n")
	for i, l := range []string{line1, line2} {
		n := i + 1
		if len(l) != 69 {
			return nil, fmt.Errorf("tle: line %d is %d chars, want 69", n, len(l))
		}
		if l[0] != byte('0'+n) {
			return nil, fmt.Errorf("tle: line %d starts with %q, want '%d'", n, l[0], n)
		}
		if want := Checksum(l); int(l[68])-'0' != want {
			return nil, fmt.Errorf("%w on line %d: got %q, want %d", ErrChecksum, n, l[68], want)
		}
	}
	if col(line1, 3, 7) != col(line2, 3, 7) {
		return nil, fmt.Errorf("tle: catalog numbers differ: %q vs %q", col(line1, 3, 7), col(line2, 3, 7))
	}

	var f fields
	t := &TLE{
		Name:           strings.TrimSpace(name),
		CatalogNum:     f.catalogNum(col(line1, 3, 7)),
		Classification: line1[7],
		IntlDesignator: strings.TrimSpace(col(line1, 10, 17)),
		NDot:           f.float("ndot", col(line1, 34, 43)),
		NDDot:          f.impliedExp("nddot", col(line1, 45, 52)),
		BStar:          f.impliedExp("bstar", col(line1, 54, 61)),
		ElementSetNum:  f.intOrZero("element set number", col(line1, 65, 68)),

		Inclination:  f.float("inclination", col(line2, 9, 16)),
		RAAN:         f.float("RAAN", col(line2, 18, 25)),
		Eccentricity: f.float("eccentricity", "0."+strings.TrimSpace(col(line2, 27, 33))),
		ArgPerigee:   f.float("argument of perigee", col(line2, 35, 42)),
		MeanAnomaly:  f.float("mean anomaly", col(line2, 44, 51)),
		MeanMotion:   f.float("mean motion", col(line2, 53, 63)),
		RevNum:       f.intOrZero("revolution number", col(line2, 64, 68)),
	}
	yy := f.intOrZero("epoch year", col(line1, 19, 20))
	day := f.float("epoch day", col(line1, 21, 32))
	if f.err != nil {
		return nil, f.err
	}
	t.Epoch = epochToTime(yy, day)
	return t, nil
}

// ReadAll reads every element set from r. It accepts both the 2-line format
// and the 3-line format with a name line before each pair (CelesTrak's
// FORMAT=tle; Space-Track's "0 NAME" name lines also work).
//
// A record that fails to parse doesn't stop the read: ReadAll returns every
// good TLE plus an error joining one message per bad record, so the caller
// can decide whether a few bad records are acceptable.
func ReadAll(r io.Reader) ([]*TLE, error) {
	var (
		out   []*TLE
		errs  []error
		name  string
		line1 string
		l1No  int
	)
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), " \r")
		switch {
		case line == "":
		case strings.HasPrefix(line, "1 ") && line1 == "":
			line1, l1No = line, n
		case strings.HasPrefix(line, "2 ") && line1 != "":
			t, err := Parse(name, line1, line)
			if err != nil {
				errs = append(errs, fmt.Errorf("line %d: %w", l1No, err))
			} else {
				out = append(out, t)
			}
			name, line1 = "", ""
		default:
			if line1 != "" {
				errs = append(errs, fmt.Errorf("line %d: line 1 not followed by a line 2", l1No))
				line1 = ""
			}
			name = strings.TrimPrefix(line, "0 ")
		}
	}
	if line1 != "" {
		errs = append(errs, fmt.Errorf("line %d: line 1 not followed by a line 2", l1No))
	}
	if err := sc.Err(); err != nil {
		errs = append(errs, err)
	}
	return out, errors.Join(errs...)
}
