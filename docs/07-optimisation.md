# 7. Optimisation: from hours to minutes

The brute-force screener ([chapter 5](05-screening.md)) tests all ~210 million pairs at every 10 s step, and would take over four hours for a 7-day screen. This chapter covers the three optimisations from the project plan (altitude filter, spatial grid, parallel workers), what each actually bought, and what the profiler says is left. The code is in [`internal/screen/engine.go`](../internal/screen/engine.go).

## The rule: same answer, faster

Every optimisation must return **exactly** the conjunctions brute force returns: the same pairs, the same TCAs and the same miss distances, to the last bit. That's what makes the speedup numbers meaningful.

`TestMethodsAgree` enforces it. It runs 300 synthetic satellites in crowded low orbits for 3 hours (357 conjunctions at a 50 km threshold) through every method on 1 and 4 workers, in both all-pairs and primary-only mode, and requires identical output. On the real catalog, the CSV output of all four 1-hour runs below is byte-for-byte identical (same MD5).

All three methods share one engine. Each step is sampled, bounded and refined exactly as in chapter 5. The only thing that changes is how the engine finds pairs within the coarse bound, behind a small `finder` interface.

## 1. Altitude filter

Two objects can't be closer than the difference of their distances from Earth's centre. So each step, the altitude finder sorts objects by radius and, for each object, only tests the ones further out by less than the coarse bound (~215 km). As soon as the next object in radius order is too far out, it stops.

**Result: 1.05×.** It halves the pair checks (75.7 → 40.0 billion per hour), but most of the catalog sits in a few hundred kilometres of altitude, so half is all it can remove. And each remaining check got more expensive. The loop now jumps around memory through the sorted index instead of streaming through arrays, and there's a sort every step. Net gain: 5%.

That's a useful negative result. A filter on one dimension can't separate objects that share a crowded shell, which is exactly where conjunctions happen.

## 2. Spatial grid

The grid generalises the altitude idea to all three dimensions. Each step, space is cut into cubes whose side is the coarse bound, and each object is placed in its cube. Two objects within the coarse bound of each other must be in the **same or neighbouring cubes** (27 in total), so each object only needs testing against objects in those 27 cubes.

Implementation details that matter:

- **No allocation per step.** Cubes are hashed into a fixed table (4× as many buckets as objects), filled with a counting sort into one flat array. A hash collision just puts an unrelated cube's objects in the same bucket. That costs a few wasted distance checks, never a missed pair.
- **No duplicate tests.** Two of the 27 neighbouring cubes can hash to the same bucket. The finder de-duplicates the bucket list before scanning, or that bucket's pairs would be tested (and refined) twice.

**Result: 17× on one core.** Pair checks per hour fall from 75.7 billion to 82 million, about 900× fewer. The time doesn't fall 900× because the rest (SGP4 sampling and refinement) is now a large share.

## 3. Parallel workers

The steps of a screen are independent: interval *k* only needs samples *k* and *k+1*. So the window is cut into **blocks** of consecutive steps, and a pool of goroutines pulls blocks from a shared atomic counter until none are left. A worker that finishes early simply takes the next block, so all cores stay busy to the end. Each block samples its own first instant, which costs one extra sample per block. Each worker keeps its own finder, sample arrays and results; nothing is shared except the read-only catalog. That's why `Propagate` had to be pure ([chapter 2](02-sgp4.md#the-one-deliberate-change-no-hidden-state)). The results are merged once at the end.

**A first mistake:** blocks were fixed at 360 steps (1 hour). A 1-hour screen was then a single block, and 10 workers ran exactly as fast as one. Block size now adapts: about four blocks per worker, between 30 and 360 steps.

**Result: 4.8× on 10 cores** (an Apple M4 has 4 performance cores and 6 slower efficiency cores, so 10× was never on the table).

## Results

Apple M4 (4P + 6E cores), live catalog of 20,481 objects (13 Oct 2026), 10 s steps, 5 km threshold:

| Method | Workers | 1-hour window | Speedup | Pair checks / hour |
|---|---|---|---|---|
| Brute force | 1 | 92.5 s | 1× | 75.7 billion |
| Altitude filter | 1 | 88.5 s | 1.05× | 40.0 billion |
| Grid | 1 | 5.5 s | 17× | 82 million |
| **Grid** | **10** | **1.15 s** | **80×** | 82 million |

All four found the same 2,264 conjunctions.

**Full 7-day screen, whole catalog: 3 min 3 s** with the grid on 10 cores, against ~4.3 hours projected for brute force (60,480 steps × 256 ms). That's **~85×**. The run made 1.24 billion SGP4 calls, 13.5 billion pair checks and 2.0 million refinements, and found 847,469 conjunctions closer than 5 km.

## Where the time goes now

`go run ./cmd/screen -cpuprofile cpu.prof`, then `go tool pprof -top`, on the 7-day run:

| Share of CPU | What |
|---|---|
| 55% | Grid search, including **11%** spent de-duplicating the 27 neighbour buckets with `slices.Contains` |
| 46% | SGP4 (`sin`, `cos`, `pow`, `mod` dominate inside it) |

(These overlap slightly, because refinement calls SGP4 from inside the grid's visit callback.)

Next targets, if more speed is wanted:

- **Cheaper de-duplication of neighbour buckets.** Compute the 27 buckets once per cube instead of once per object, or use a hash where neighbours never collide.
- **A tighter coarse bound.** It currently assumes both objects move at the fastest speed of any object in the catalog (~10.5 km/s, an eccentric orbit at perigee). Typical LEO speed is 7.6 km/s. Per-object speeds would shrink the cubes, and with them the 1.48 billion pairs that pass the coarse bound only to fail the chord bound.
- **Fewer SGP4 calls.** Sample at a coarser step and interpolate between samples, keeping the bounds conservative.

## A bug only the full run found

The first full 7-day run seemed to hang after the parallel work finished, with one core busy. The culprit was in the command-line tool, not the screener. It picked the 10 closest conjunctions with a hand-written **insertion sort**, which is O(n²). That's instant for the 2,264 conjunctions of a 1-hour test, and hours for 847,469. It's now `sort.Slice`. Lesson: test at full scale, not just at the size that's convenient.

## Why conjunctions grow day by day

The 7-day run finds far more conjunctions per day late in the week than early:

| Day | Conjunctions |
|---|---|
| 13 Oct (from 06:00) | 43,024 |
| 14 Oct | 77,350 |
| 15 Oct | 111,098 |
| 16 Oct | 132,309 |
| 17 Oct | 142,267 |
| 18 Oct | 150,040 |
| 19 Oct | 152,813 |

No single object is responsible. The increase is spread across thousands of Starlink satellites, each in ~100 conjunctions a day by day 7. The most likely explanation:

- **The real constellation is coordinated.** Starlink phases its satellites so they avoid each other where orbit planes cross. On day 1, the screen finds about 230 conjunctions per 5 minutes, fewer than the ~600 a back-of-the-envelope estimate for *randomly* placed satellites at the same density predicts.
- **Predictions lose that coordination.** TLE predictions don't include manoeuvres, and each satellite's drag estimate differs slightly. Over days, the predicted satellites drift out of their slots towards effectively random positions, and by day 7 the rate (~530 per 5 minutes) is close to the random estimate.

This is a hypothesis that fits both numbers, not a proven result. But the lesson is solid either way: **late-week conjunctions from TLEs are much less trustworthy than early-week ones**, which matters for the SOCRATES comparison and is one more reason this is a screening tool, not a predictor.

← [6. The live tracker](06-live-tracker.md) · [Glossary](glossary.md)
