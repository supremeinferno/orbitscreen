# orbitscreen

**How fast can one laptop screen the whole satellite catalog for close approaches, and how well does it match CelesTrak SOCRATES?**

`orbitscreen` is a conjunction screener written in Go from scratch, with no orbital-mechanics dependencies. It reads the public satellite catalog, predicts where every object will be over the next 7 days, and finds every pair that comes within a few kilometres of each other. Then it checks its answers against [CelesTrak SOCRATES](https://celestrak.org/SOCRATES/), the public reference service for this job.

It also includes a **live tracker** that draws every object in the catalog moving on a world map. **Try it: [supremeinferno.github.io/orbitscreen](https://supremeinferno.github.io/orbitscreen/)**. It runs the project's own Go SGP4 in your browser, compiled to WebAssembly, and lists the closest approaches of the next 24 hours from the project's screener.

![Live tracker: ~21,000 objects around a 3D Earth, the ISS selected with its orbit, and the next day's closest approaches](docs/img/tracker.jpg)

> **This is a screening tool, not a collision predictor.** Public orbit data (TLEs) is only accurate to about a kilometre at its epoch, and the error grows by kilometres per day. A "close approach" here means "worth a closer look with better data", not "will collide". See [Limitations](#limitations).

## Status

| Milestone | Status |
|---|---|
| 1. SGP4 propagator in Go, verified against reference implementations | ✅ Done |
| 2. Brute-force screener: check every pair, measure the "before" time | ✅ Done |
| 3. Optimised screener: altitude filter, spatial grid, goroutine worker pool, pprof profiling | ✅ Done |
| 4. Validate against SOCRATES: what % of their close approaches do we find? | ✅ Done |
| 5. Final numbers: full catalog over 7 days in 5 min, 100% SOCRATES match (same data), ~136× speedup | ✅ Done |
| ➕ Live tracker: every satellite moving on a world map in the browser | ✅ Done |

## Results

**SGP4 accuracy** (details in [docs/03-verification.md](docs/03-verification.md)):

| Check | Compared | Max position error | Max velocity error |
|---|---|---|---|
| Vallado's official verification set (33 edge-case satellites) | 666 states | 0.1 mm | 0.85 µm/s |
| Live CelesTrak catalog (15,939 objects × 7 days, hourly) vs [python-sgp4](https://pypi.org/project/sgp4/) | 2,693,435 states | 4.5 µm | 5 nm/s |

All 7 verification cases that are *supposed* to fail (decayed satellites, impossible orbits) fail at the same time step with the same error code as the reference.

**SGP4 speed** (Apple M4, one core): **~0.2 µs** per position for a low-Earth orbit, **~0.5 µs** for a deep-space orbit, zero heap allocations.

**Screening speed** (details in [docs/07-optimisation.md](docs/07-optimisation.md)). Apple M4, 20,481 objects, 10 s steps, 5 km threshold. Every method finds exactly the same conjunctions:

| Method | Cores | 1-hour window | 7-day window |
|---|---|---|---|
| Brute force (baseline) | 1 | 92.5 s | ~4.3 h (projected) |
| Altitude filter | 1 | 88.5 s | |
| Spatial grid | 1 | 5.5 s | |
| **Spatial grid** | **10** | **1.15 s** | **3 min 3 s** |

On the **whole public catalog** from Space-Track (32,591 objects, 13 Oct 2026, all pairs):

| Method | Cores | 7-day window | Conjunctions |
|---|---|---|---|
| Brute force | 1 | ~11.4 h (projected from 10 minutes) | |
| **Spatial grid** | **10** | **5 min 0 s** | 995,779 |

**Whole catalog over 7 days in 5 minutes, ~136× faster than brute force.** The speedup grows with the catalog. Going from 20,481 to 32,591 objects (1.6×), brute force takes 2.6× as long (O(N²)), the grid only 1.6× as long.

**Validation against SOCRATES** (details in [docs/08-socrates.md](docs/08-socrates.md)). One SOCRATES Plus run (206,222 conjunctions, 7 days from 12 Oct 2026), rebuilt with the element sets SOCRATES used from Space-Track's history. Our screen of 32,465 objects took 5 min 1 s; SOCRATES took 14 h.

| SOCRATES conjunctions | Found by us |
|---|---|
| Both objects with the same element sets as SOCRATES | **205,716 / 205,716 (100%)**, miss distances within 0.5 m |
| Different element sets (SupGP, missing history) | 177 / 502 |
| **All** | **205,893 / 206,222 (99.84%)** |

None of the misses is a screener bug: with our data, every one of those pairs stays outside 5 km.

## How it works

Start with the docs. They are written to be read in order, and they assume no orbital-mechanics background:

1. [TLEs: the input data](docs/01-tle-format.md). What a Two-Line Element set is, field by field, and how the parser reads it.
2. [SGP4: from elements to positions](docs/02-sgp4.md). What the SGP4 model does, why we must use it, and how this Go port is structured.
3. [Verification](docs/03-verification.md). How we proved the propagator is right, including the bug the tests caught.
4. [The catalog](docs/04-catalog.md). Where the data comes from, why we fetch OMM instead of TLEs, and the cleanup it needs (duplicates, docked objects).
5. [Screening](docs/05-screening.md). How close approaches are found without missing any, why SGP4's velocity can't be trusted for this, and the brute-force baseline.
6. [The live tracker](docs/06-live-tracker.md). Turning SGP4 positions into latitude and longitude, and drawing 20,000 objects on a map every second.
7. [Optimisation](docs/07-optimisation.md). Altitude filter, spatial grid and parallel workers: what each bought, and what the profiler says is left.
8. [Validation against SOCRATES](docs/08-socrates.md). Rebuilding the catalog exactly as CelesTrak's SOCRATES saw it, and matching its results: 100% where the data is the same.
9. [Glossary](docs/glossary.md). Every term of art used in the code and docs.

## Results in short

- **Speed.** The whole public catalog, **32,591 objects**, screened for every approach within 5 km over **7 days in 5 minutes** on a laptop (Apple M4). Brute force would take **11.4 hours**: a **~136× speedup**.
- **Correctness.** Against CelesTrak SOCRATES, rebuilt with the exact element sets it used: **100%** of its close approaches found where the data is the same (**205,716 of 205,716**), with miss distances within **0.5 m**. 99.84% overall; every miss is explained by different input data, none by the screener.
- **Along the way**, the checks caught real bugs and traps, among them: SGP4's velocity isn't the derivative of its position, decaying objects that blow up without an error, a merge step that hid repeated close approaches, an O(n²) sort that only showed at full scale, and Space-Track's republished element sets. Each is described in the chapter where it was found.

## Repository layout

```
internal/tle/        TLE and OMM CSV parsers
internal/sgp4/       SGP4/SDP4 propagator, a port of Vallado's 2020 reference code
  testdata/          Vallado's verification TLEs and expected output
internal/catalog/    Loads data files: dedup, primaries, docked-object groups
internal/screen/     Close-approach screening: bounds, refinement, brute force, grid
internal/socrates/   SOCRATES CSV reader, as-of catalog rebuild, matching
internal/earth/      TEME → Earth-fixed → latitude/longitude/altitude
internal/live/       What the live tracker shows: all positions, one object's details and ground track
cmd/fetch/           Downloads orbit data from CelesTrak into data/
cmd/screen/          The screener CLI
cmd/track/           Live tracker: web server + embedded map page
cmd/socrates/        Screens as a SOCRATES run did and reports recall
cmd/trackwasm/       The tracker's engine compiled to WebAssembly, for the public site
site/                Builds the public site (GitHub Pages, refreshed every 6 hours)
cmd/crosscheck/      Dumps positions for a whole catalog + Python script to compare with python-sgp4
docs/                The explanations above
```

## Running it

Needs Go 1.26+.

```sh
go test ./...                       # all tests, including the Vallado verification
go test ./internal/sgp4 -bench .    # propagation speed

go run ./cmd/fetch                  # download today's data into data/ (reused for 2 hours)
go run ./cmd/screen                 # full 7-day screen: spatial grid on all cores (~3 min)
go run ./cmd/screen -method brute -workers 1 -max-steps 60   # baseline: time 10 minutes, project 7 days
go run ./cmd/screen -days 0.0417 -out conj.csv   # a real 1-hour screen, results to CSV
go run ./cmd/screen -primary-only   # SOCRATES-style: active payloads vs everything
go run ./cmd/track                  # live map at http://localhost:8080

site/build.sh                       # the public site into _site/: WebAssembly engine, data, next day's approaches
```

To repeat the full-catalog cross-check against python-sgp4:

```sh
curl -o active.tle 'https://celestrak.org/NORAD/elements/gp.php?GROUP=active&FORMAT=tle'
go run ./cmd/crosscheck -tle active.tle -step 60 > go.csv
python3 -m venv .venv && .venv/bin/pip install sgp4
.venv/bin/python cmd/crosscheck/crosscheck.py active.tle go.csv
```

(CelesTrak asks users not to download the same data more than once every couple of hours. Please save the file and reuse it.)

To repeat the SOCRATES comparison you need a free [Space-Track.org](https://www.space-track.org/) account. Log in with a browser, then open these query URLs to download CSVs:

| Save as | Query URL (after `https://www.space-track.org/basicspacedata/query/`) |
|---|---|
| `data/spacetrack/gp.csv` | `class/gp/decay_date/null-val/epoch/%3Enow-30/orderby/norad_cat_id/format/csv` |
| `data/spacetrack/gp_history.csv` | `class/gp_history/EPOCH/2026-10-07--2026-10-13/orderby/norad_cat_id/format/csv` (the days before the SOCRATES run) |

Save SOCRATES's results, `https://celestrak.org/SOCRATES/sort-minRange.csv`, as `data/socrates/sort-minRange.csv`. CelesTrak replaces it with every run, so note the run's data time and window start from [the SOCRATES search page](https://celestrak.org/SOCRATES/search.php). Then:

```sh
go run ./cmd/socrates -asof 2026-10-12T08:22:44Z -start 2026-10-12T09:00:00Z \
    -data data/spacetrack/gp_history.csv,data/spacetrack/gp.csv     # ~5 min
```

## Limitations

Each of these was measured during the project, not just assumed:

- **The input data is the ceiling.** TLEs are fitted to tracking data and published without uncertainty information. Position errors are around a kilometre at epoch and grow by kilometres per day. How much that matters showed up directly: screening with data **one day newer** than SOCRATES's reproduced only **12%** of its conjunctions, although the screener matched 100% on the same data ([chapter 8](docs/08-socrates.md#the-data-must-match)).
- **Day 7 is far less certain than day 1.** The number of conjunctions found per day more than triples over the week (43,024 on day 1, 152,813 on day 7), most likely because predictions lose Starlink's coordinated spacing ([chapter 7](docs/07-optimisation.md#why-conjunctions-grow-day-by-day)).
- **SGP4 is a simplified model by design.** It's the *right* model for TLEs (they're fitted with it), but not a high-precision propagator. Its velocity output is off from its own position's derivative by up to 52 m/s, and decaying objects can blow up to thousands of km/s without an error ([chapter 5](docs/05-screening.md)). The screener guards against both.
- **No SupGP data.** SOCRATES uses more accurate, operator-based SupGP element sets for Starlink, OneWeb and others where available. We don't, which is why only 35% of the 502 SOCRATES conjunctions computed from different element sets match.
- **No collision probability.** Operators decide on manoeuvres using covariance-based probability of collision, computed from far better data. This project reports geometry only.
- **The public catalog is incomplete.** Classified objects and debris too small to track (roughly under 10 cm in LEO) aren't in it.

## References

- Vallado, Crawford, Hujsak, Kelso, *Revisiting Spacetrack Report #3*, AIAA 2006-6753. This is the reference SGP4 implementation and the verification set used here.
- Hoots & Roehrich, *Spacetrack Report No. 3: Models for Propagation of NORAD Element Sets*, 1980. The original SGP4 definition.
- [CelesTrak](https://celestrak.org/), for the TLE data and SOCRATES.
