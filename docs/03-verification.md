# 3. Verification

An SGP4 implementation that is "probably right" is useless for screening. A small error in a rarely used branch (say, 12-hour resonant orbits) would quietly corrupt every close approach involving those objects. This chapter shows how the Go port was checked, and what the checks caught.

There are two independent checks:

1. **Vallado's verification set.** A small set of hand-picked hard cases with published expected output. This checks **correctness on every code path**.
2. **The live catalog against python-sgp4.** Every object in today's catalog, compared with the most widely used SGP4 library. This checks **robustness on real data** at scale.

## Check 1: Vallado's verification set

Vallado's paper ships a test file, [`SGP4-VER.TLE`](../pkg/sgp4/testdata/SGP4-VER.TLE), with 33 TLEs. Each one was chosen to exercise a specific branch or historical bug:

| Kind of case | Examples |
|---|---|
| Normal and heavy drag, near Earth | DELTA 1 DEB, CBERS 2 (eccentricity < 10⁻⁴ drops some drag terms) |
| Very low perigee (adjusted atmosphere below 156 km and 98 km) | COSMOS 2405, SL-6 R/B |
| 12-hour resonance, every eccentricity band the code switches on | MOLNIYA 2-14, MOLNIYA 1-36, MOLNIYA 1-83, COSMOS 1024 DEB |
| 24-hour (geostationary) resonance | ITALSAT 2, XM-3, AMC-4, INTELSAT 902 |
| Lyddane low-inclination switch (a historical source of jumps) | SL-12 R/B, ARIANE 44L+, EUTELSAT 1-F1 |
| Extreme eccentricity (0.97) | WIND |
| Propagating backwards in time | SMS 1 AKM, 4632 |
| Decay and deliberate error cases | MINOTAUR R/B, SL-14 DEB, cases 33333–33335 |

Each line 2 carries extra columns: start, stop and step times. The expected output, [`tcppver.out`](../pkg/sgp4/testdata/tcppver.out), lists position and velocity at every step, as computed by Vallado's C++ code.

### How the test works

`TestVallado` in [`sgp4_test.go`](../pkg/sgp4/sgp4_test.go):

1. Reproduces the exact time steps of Vallado's test driver (`driverTimes`): `t = 0`, then start to stop in steps, with the last step clamped to `stop`.
2. Propagates each satellite at each step and compares the result with the matching row of `tcppver.out`.
3. Requires each case to **stop at the same step**. Vallado's output simply ends when propagation fails, so a case that runs longer or shorter than his is a failure. The cases that should fail are listed in `expectedErrors`, along with the time and error code we expect.

Some test TLEs are deliberately corrupted copies of real ones with stale checksums, so the test recomputes column 69 before parsing. That is the only place the checksum rule is bypassed.

### Results

```
33 cases, 666 rows: max |Δr| = 1.01e-07 km, max |Δv| = 8.53e-10 km/s
```

The largest position difference is **0.1 mm**. `tcppver.out` prints positions to 10⁻⁸ km and velocities to 10⁻⁹ km/s, so this is agreement to the printed precision, give or take floating-point rounding. All 7 failure cases stop where Vallado's do:

| Case | Fails at | Error |
|---|---|---|
| 22312 SL-6 R/B (decayed the same day) | 494.2 min | 1: mean eccentricity < 0 |
| 28350 COSMOS 2405 (perigee 127 km) | 1560 min | 1: mean eccentricity < 0 |
| 28872 MINOTAUR R/B (sub-orbital) | 55 min | 6: decayed |
| 29141 SL-14 DEB | 440 min | 6: decayed |
| 33333 (eccentricity 0.995) | 25 min | 4: semi-latus rectum < 0 |
| 33334 (mean motion 0.00001 rev/day) | 0 min | 3: perturbed eccentricity out of range |
| 20413 SL-12 R/B, 3.5 years out | 1,844,345 min | 6: decayed |

### What the test caught

The first run was **not** clean. That is the reason this test exists.

**Bug 1: a wrong term in the resonance integrator.** 12-hour and 24-hour orbits were off by up to **200 m**. Every other orbit was fine. The error had a distinctive shape: zero at t = 0, 720, 1440 … minutes, and growing in between.

That pattern pointed straight at the cause. The resonance integrator takes fixed 720-minute steps, and between steps it interpolates with a second-order Taylor expansion. Exact at the steps but wrong in between means the steps were right and the interpolation was wrong. The bug was in the mean longitude:

```go
xl := xli + xldot*ft + xnddt*ft*ft*0.5   // wrong: xnddt is d²n/dt², not d²L/dt²
xl := xli + xldot*ft + xndt*ft*ft*0.5    // right:  dL/dt = n, so d²L/dt² = dn/dt = xndt
```

After the fix, the worst error dropped from 200 m to 4 mm.

**Not a bug: epoch rounding.** The remaining 4 mm was in one case only, WIND, with eccentricity 0.97. An experiment showed this orbit is so sensitive that shifting the epoch by 10⁻⁹ days (86 microseconds) moves the computed position by 1.5 cm. Vallado's code stores the epoch as a Julian date, about 2,450,000, in a `double`, and at that size a `double` only resolves about 40 µs. Our parser kept the epoch more precisely, so the two were really computing slightly different epochs.

The fix was to compute the epoch exactly the way Vallado does (`sgp4Epoch` in `sgp4.go`), rounding included, so both sides start from the same number. The difference dropped to 0.1 mm. This is not more "correct" physically: TLE epochs are only given to 10⁻⁸ days (0.86 ms) anyway. But it makes the comparison apples-to-apples, so any future deviation in the test means a real bug.

## Check 2: the live catalog against python-sgp4

The verification set proves the branches are right. It doesn't prove the parser copes with today's real data, or that nothing goes wrong across thousands of ordinary objects. So the second check runs **every object in the current catalog**.

[`cmd/crosscheck`](../cmd/crosscheck/) does it in two halves:

- [`main.go`](../cmd/crosscheck/main.go) reads a TLE file, propagates each object from its epoch out to 7 days in hourly steps, and writes the positions and velocities as CSV. If an object fails, its error code goes into the CSV too.
- [`crosscheck.py`](../cmd/crosscheck/crosscheck.py) loads the same TLE file into [python-sgp4](https://pypi.org/project/sgp4/), which wraps Vallado's C++ code and is the most widely used SGP4 library. It recomputes every row and reports the largest difference, plus any disagreement about errors.

### Results (CelesTrak "active" catalog, 12 October 2026)

```
15939 satellites, 2693435 positions compared
max |dr| = 4.51e-09 km, max |dv| = 5.12e-12 km/s
errors: 4 agree, 0 disagree
```

- **Parsing:** all 15,939 records parsed, with zero rejected.
- **Accuracy:** across 2.7 million positions, the largest difference is **4.5 micrometres**. That is floating-point rounding: Go and C compute `sin`, `pow` and so on slightly differently in the last bit.
- **Errors:** 4 objects fail partway through the week (decaying objects), and they fail at the same step with the same code in both implementations.

To reproduce, see [Running it](../README.md#running-it) in the README.

## Speed

`go test ./pkg/sgp4 -bench .`:

```
BenchmarkPropagateNearEarth    217 ns/op    0 B/op    0 allocs/op
BenchmarkPropagateDeepSpace    472 ns/op    0 B/op    0 allocs/op
```

That's one core of an Apple M-series laptop. This is the cost that milestone 2 builds on. For example, the full catalog (~30,000 objects) at 1-minute steps for 7 days needs about 300 million propagations, which is roughly a minute of CPU time just to compute positions, before comparing a single pair.

← [2. SGP4](02-sgp4.md) · [Glossary](glossary.md)
