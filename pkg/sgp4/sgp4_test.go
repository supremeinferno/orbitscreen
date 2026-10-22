package sgp4

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/supremeinferno/orbitscreen/pkg/tle"
)

// The verification set from Vallado et al., "Revisiting Spacetrack Report
// #3" (AIAA 2006-6753), as shipped with his reference code:
//
//	testdata/SGP4-VER.TLE  33 test cases chosen to exercise every branch
//	                       (drag regimes, deep space, both resonances, the
//	                       Lyddane switch, decay, error codes)
//	testdata/tcppver.out   positions and velocities from his C++ code
//
// Each line 2 in SGP4-VER.TLE carries extra columns after column 69:
// start, stop and step times in minutes.

// verCase is one TLE from SGP4-VER.TLE plus its start/stop/step (minutes).
type verCase struct {
	tle               *tle.TLE
	start, stop, step float64
}

type verRow struct {
	t    float64
	r, v [3]float64
}

func readVerTLEs(t testing.TB) []verCase {
	t.Helper()
	f, err := os.Open("testdata/SGP4-VER.TLE")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Some cases are deliberately-corrupted copies of real TLEs with stale
	// checksums, so recompute column 69 before parsing.
	fix := func(l string) string { return l[:68] + strconv.Itoa(tle.Checksum(l)) }

	var out []verCase
	var line1 string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), " \r")
		switch {
		case strings.HasPrefix(l, "1 "):
			line1 = l
		case strings.HasPrefix(l, "2 "):
			el, err := tle.Parse("", fix(line1), fix(l[:69]))
			if err != nil {
				t.Fatalf("parsing %q: %v", line1, err)
			}
			c := verCase{tle: el}
			if _, err := fmt.Sscan(l[69:], &c.start, &c.stop, &c.step); err != nil {
				t.Fatalf("start/stop/step in %q: %v", l, err)
			}
			out = append(out, c)
		}
	}
	return out
}

// readVerOutput returns one slice of rows per "<catnum> xx" block.
func readVerOutput(t testing.TB) (ids []int, blocks [][]verRow) {
	t.Helper()
	f, err := os.Open("testdata/tcppver.out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) == 2 && fs[1] == "xx" {
			id, err := strconv.Atoi(fs[0])
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
			blocks = append(blocks, nil)
			continue
		}
		if len(fs) < 7 {
			continue
		}
		var nums [7]float64
		for i := range nums {
			if nums[i], err = strconv.ParseFloat(fs[i], 64); err != nil {
				t.Fatalf("bad number %q in %q", fs[i], sc.Text())
			}
		}
		row := verRow{t: nums[0], r: [3]float64(nums[1:4]), v: [3]float64(nums[4:7])}
		blocks[len(blocks)-1] = append(blocks[len(blocks)-1], row)
	}
	return ids, blocks
}

// driverTimes reproduces the time steps of Vallado's test driver: t=0,
// then start..stop in steps of step, with the final step clamped to stop.
func driverTimes(c verCase) []float64 {
	ts := []float64{0}
	t := c.start
	if math.Abs(t) > 1e-8 {
		t -= c.step // so the first value isn't written twice
	}
	for t < c.stop {
		t += c.step
		if t > c.stop {
			t = c.stop
		}
		ts = append(ts, t)
	}
	return ts
}

func dist(a, b [3]float64) float64 {
	return math.Sqrt((a[0]-b[0])*(a[0]-b[0]) + (a[1]-b[1])*(a[1]-b[1]) + (a[2]-b[2])*(a[2]-b[2]))
}

// expectedErrors lists the cases where propagation stops with an error:
// catalog number -> time of the first failing step and the error code.
// Vallado's output simply ends at the last good step, so the test checks
// that our output ends at the same place and for a sensible reason.
var expectedErrors = map[int]struct {
	t    float64
	code int
}{
	22312: {494.2028672, ErrMeanEccentricity}, // decaying: drag drives e below 0
	28350: {1560, ErrMeanEccentricity},        // perigee 127 km, same story
	28872: {55, ErrDecayed},                   // sub-orbital, "lost in 50 minutes"
	29141: {440, ErrDecayed},                  // "lost in under 420 min"
	33333: {25, ErrSemiLatusRectum},           // "check error code 4"
	33334: {0, ErrPerturbedEccentricity},      // mean motion 0.00001 rev/day
	20413: {1844345, ErrDecayed},              // 2nd 20413 case, ~3.5 years out
}

func TestVallado(t *testing.T) {
	const (
		posTol = 1e-6 // km   (1 mm; the file prints 1e-8 km)
		velTol = 5e-9 // km/s (the file prints 1e-9 km/s)
	)
	cases := readVerTLEs(t)
	ids, blocks := readVerOutput(t)
	if len(cases) != len(blocks) {
		t.Fatalf("%d TLEs but %d output blocks", len(cases), len(blocks))
	}

	var maxPos, maxVel float64
	rows := 0
	for i, c := range cases {
		num := c.tle.CatalogNum
		if num != ids[i] {
			t.Fatalf("case %d: TLE is %d but output block is %d", i, num, ids[i])
		}
		ref := blocks[i]

		sat, err := New(c.tle)
		var good int // rows propagated without error
		var failT float64
		for _, ts := range driverTimes(c) {
			if err != nil {
				break
			}
			var r, v [3]float64
			if r, v, err = sat.Propagate(ts); err != nil {
				failT = ts
				break
			}
			if good >= len(ref) {
				t.Errorf("%d: propagated t=%v but the reference stopped at t=%v", num, ts, ref[len(ref)-1].t)
				break
			}
			if math.Abs(ref[good].t-ts) > 1e-6 {
				t.Fatalf("%d: row %d is t=%v, want t=%v", num, good, ts, ref[good].t)
			}
			dp, dv := dist(r, ref[good].r), dist(v, ref[good].v)
			maxPos, maxVel = math.Max(maxPos, dp), math.Max(maxVel, dv)
			if dp > posTol || dv > velTol {
				t.Errorf("%d t=%.4f: |Δr| = %.3g km, |Δv| = %.3g km/s", num, ts, dp, dv)
			}
			good++
			rows++
		}

		want, wantErr := expectedErrors[num]
		wantErr = wantErr && want.t >= c.start && want.t <= c.stop // 20413 appears twice
		switch {
		case err == nil && wantErr:
			t.Errorf("%d: no error, want code %d at t=%v", num, want.code, want.t)
		case err != nil && !wantErr:
			t.Errorf("%d: unexpected %v", num, err)
		case err != nil:
			var se *Error
			if !errors.As(err, &se) || se.Code != want.code || math.Abs(failT-want.t) > 1e-6 {
				t.Errorf("%d: got %v, want code %d at t=%v", num, err, want.code, want.t)
			}
			// On an error at t=0 Vallado still prints a (stale) t=0 row.
			if failT == 0 {
				good++
			}
		}
		if good != len(ref) {
			t.Errorf("%d: %d rows, reference has %d", num, good, len(ref))
		}
	}
	t.Logf("%d cases, %d rows: max |Δr| = %.3g km, max |Δv| = %.3g km/s", len(cases), rows, maxPos, maxVel)
}

// Propagate must not depend on call history (Vallado's version caches the
// resonance integrator), or a shared Satellite would give different answers
// depending on which goroutine asked first.
func TestPropagateIsPure(t *testing.T) {
	for _, c := range readVerTLEs(t) {
		sat, err := New(c.tle)
		if err != nil {
			continue
		}
		times := driverTimes(c)
		fwd := make([][3]float64, len(times))
		for i, ts := range times {
			fwd[i], _, _ = sat.Propagate(ts)
		}
		for i := len(times) - 1; i >= 0; i-- {
			if r, _, _ := sat.Propagate(times[i]); r != fwd[i] {
				t.Fatalf("%d t=%v: %v forwards, %v backwards", c.tle.CatalogNum, times[i], fwd[i], r)
			}
		}
	}
}

func TestPropagateTo(t *testing.T) {
	c := readVerTLEs(t)[0] // catalog 5, near earth
	sat, err := New(c.tle)
	if err != nil {
		t.Fatal(err)
	}
	r1, v1, _ := sat.Propagate(360)
	r2, v2, _ := sat.PropagateTo(c.tle.Epoch.Add(6 * time.Hour))
	if r1 != r2 || v1 != v2 {
		t.Errorf("PropagateTo(epoch+6h) = %v, Propagate(360) = %v", r2, r1)
	}
}

func benchmarkSat(b *testing.B, catalogNum int) {
	for _, c := range readVerTLEs(b) {
		if c.tle.CatalogNum != catalogNum {
			continue
		}
		sat, err := New(c.tle)
		if err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			sat.Propagate(float64(i % 10080)) // one week of minutes
		}
		return
	}
	b.Fatalf("no case %d", catalogNum)
}

func BenchmarkPropagateNearEarth(b *testing.B) { benchmarkSat(b, 28057) } // CBERS 2, LEO
func BenchmarkPropagateDeepSpace(b *testing.B) { benchmarkSat(b, 28626) } // XM-3, GEO resonant
