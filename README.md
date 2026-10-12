# orbitscreen

**How fast can one laptop screen the whole satellite catalog for close approaches, and how well does it match CelesTrak SOCRATES?**

`orbitscreen` is a conjunction screener written in Go from scratch, with no orbital-mechanics dependencies. It reads the public satellite catalog, predicts where every object will be over the next 7 days, and finds every pair that comes within a few kilometres of each other. Then it checks its answers against [CelesTrak SOCRATES](https://celestrak.org/SOCRATES/), the public reference service for this job.

It also includes a **live tracker** that draws every object in the catalog moving on a world map:

![Live tracker: ~20,000 objects on a world map, ISS selected with its ground track](docs/img/tracker.jpg)

> **This is a screening tool, not a collision predictor.** Public orbit data (TLEs) is only accurate to about a kilometre at its epoch, and the error grows by kilometres per day. A "close approach" here means "worth a closer look with better data", not "will collide". See [Limitations](#limitations).

## Status

| Milestone | Status |
|---|---|
| 1. SGP4 propagator in Go, verified against reference implementations | ✅ Done |
| 2. Brute-force screener: check every pair, measure the "before" time | ✅ Done |
| 3. Optimised screener: altitude filter, spatial grid / k-d tree, goroutine worker pool, pprof profiling | ⏳ Next |
| 4. Validate against SOCRATES: what % of their close approaches do we find? | ☐ |
| 5. Final numbers: full catalog over 7 days in ___ min, ___% SOCRATES match, ___× speedup | ☐ |
| ➕ Live tracker: every satellite moving on a world map in the browser | ✅ Done |

## Results so far

**SGP4 accuracy** (details in [docs/03-verification.md](docs/03-verification.md)):

| Check | Compared | Max position error | Max velocity error |
|---|---|---|---|
| Vallado's official verification set (33 edge-case satellites) | 666 states | 0.1 mm | 0.85 µm/s |
| Live CelesTrak catalog (15,939 objects × 7 days, hourly) vs [python-sgp4](https://pypi.org/project/sgp4/) | 2,693,435 states | 4.5 µm | 5 nm/s |

All 7 verification cases that are *supposed* to fail (decayed satellites, impossible orbits) fail at the same time step with the same error code as the reference.

**SGP4 speed** (Apple M4, one core): **~0.2 µs** per position for a low-Earth orbit, **~0.5 µs** for a deep-space orbit, zero heap allocations.

**Brute-force screening, the "before" number** (details in [docs/05-screening.md](docs/05-screening.md)). Apple M4, one core, 20,489 objects, 10 s steps, 5 km threshold:

| Window | Time | Pair checks | Conjunctions < 5 km |
|---|---|---|---|
| 1 hour | 1 min 37 s | 75.8 billion | 2,691 |
| 7 days (projected) | ~4.5 hours | ~12.7 trillion | |

Time grows as N²: doubling the catalog makes each step ~4× slower.

## How it works

Start with the docs. They are written to be read in order, and they assume no orbital-mechanics background:

1. [TLEs: the input data](docs/01-tle-format.md). What a Two-Line Element set is, field by field, and how the parser reads it.
2. [SGP4: from elements to positions](docs/02-sgp4.md). What the SGP4 model does, why we must use it, and how this Go port is structured.
3. [Verification](docs/03-verification.md). How we proved the propagator is right, including the bug the tests caught.
4. [The catalog](docs/04-catalog.md). Where the data comes from, why we fetch OMM instead of TLEs, and the cleanup it needs (duplicates, docked objects).
5. [Screening](docs/05-screening.md). How close approaches are found without missing any, why SGP4's velocity can't be trusted for this, and the brute-force baseline.
6. [The live tracker](docs/06-live-tracker.md). Turning SGP4 positions into latitude and longitude, and drawing 20,000 objects on a map every second.
7. [Glossary](docs/glossary.md). Every term of art used in the code and docs.

More chapters arrive with each milestone: optimisations, SOCRATES validation.

## Repository layout

```
internal/tle/        TLE and OMM CSV parsers
internal/sgp4/       SGP4/SDP4 propagator, a port of Vallado's 2020 reference code
  testdata/          Vallado's verification TLEs and expected output
internal/catalog/    Loads data files: dedup, primaries, docked-object groups
internal/screen/     Close-approach screening: bounds, refinement, brute force
internal/earth/      TEME → Earth-fixed → latitude/longitude/altitude
cmd/fetch/           Downloads orbit data from CelesTrak into data/
cmd/screen/          The screener CLI
cmd/track/           Live tracker: web server + embedded map page
cmd/crosscheck/      Dumps positions for a whole catalog + Python script to compare with python-sgp4
docs/                The explanations above
```

## Running it

Needs Go 1.26+.

```sh
go test ./...                       # all tests, including the Vallado verification
go test ./internal/sgp4 -bench .    # propagation speed

go run ./cmd/fetch                  # download today's data into data/ (reused for 2 hours)
go run ./cmd/screen -max-steps 60   # time the first 10 minutes, project the full 7 days
go run ./cmd/screen -days 0.0417 -out conj.csv   # a real 1-hour screen, results to CSV
go run ./cmd/screen -primary-only   # SOCRATES-style: active payloads vs everything
go run ./cmd/track                  # live map at http://localhost:8080
```

To repeat the full-catalog cross-check against python-sgp4:

```sh
curl -o active.tle 'https://celestrak.org/NORAD/elements/gp.php?GROUP=active&FORMAT=tle'
go run ./cmd/crosscheck -tle active.tle -step 60 > go.csv
python3 -m venv .venv && .venv/bin/pip install sgp4
.venv/bin/python cmd/crosscheck/crosscheck.py active.tle go.csv
```

(CelesTrak asks users not to download the same data more than once every couple of hours. Please save the file and reuse it.)

## Limitations

These limits are real, and they will be measured, not hand-waved, as the project progresses:

- **TLE accuracy is the ceiling.** TLEs are fitted to tracking data and published without covariance (uncertainty) information. Position errors are on the order of a kilometre at epoch and grow by roughly a few kilometres per day, more for low, high-drag orbits. A 7-day screen is therefore much less certain on day 7 than on day 1.
- **SGP4 is a simplified model by design.** It is the *correct* model for TLEs (they are fitted with it), but it is not a high-precision propagator.
- **The public catalog is incomplete.** Classified objects and debris too small to track (roughly under 10 cm in LEO) are not in it.
- Operators decide on manoeuvres using covariance-based probability of collision computed from far better data. Nothing here replaces that.

## References

- Vallado, Crawford, Hujsak, Kelso, *Revisiting Spacetrack Report #3*, AIAA 2006-6753. This is the reference SGP4 implementation and the verification set used here.
- Hoots & Roehrich, *Spacetrack Report No. 3: Models for Propagation of NORAD Element Sets*, 1980. The original SGP4 definition.
- [CelesTrak](https://celestrak.org/), for the TLE data and SOCRATES.
