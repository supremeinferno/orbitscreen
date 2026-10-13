# 5. Screening: finding close approaches

This is the core of the project. Given ~20,000 objects and a 7-day window, find **every pair that comes within 5 km**, along with the time of closest approach (TCA), the miss distance, and the relative speed. This chapter explains the method, the bounds that make it safe, a trap SGP4 sets for the unwary, and the brute-force baseline. The code is in [`internal/screen`](../internal/screen/).

## The problem

Two objects in low orbit typically pass each other at 5–15 km/s. A 5 km threshold is crossed in under a second, so we can't check positions once a minute and hope to catch it. But we also can't propagate every object every 0.1 s for a week: that's 6 million time steps.

And the pairs are the real problem. 20,489 objects make **~210 million pairs**. Whatever we do per pair, we do it 210 million times per time step.

## The method

Every screener in this project uses the same four steps. Only step 2's *choice of which pairs to test* differs between them.

1. **Sample.** Propagate every object at t₀, t₀ + Δt, t₀ + 2Δt, … with Δt = 10 s. That's 60,480 samples per object for 7 days.
2. **Rule pairs out.** For each interval [t, t + Δt] and each pair, use the two objects' positions at both ends to *prove*, cheaply, that they can't get within 5 km during the interval. Almost every pair is ruled out here.
3. **Refine** the few survivors. Golden-section search on the true SGP4 distance over the interval finds the TCA and miss distance.
4. **Merge** detections of the same encounter. An encounter near an interval boundary is seen from both neighbouring intervals, and a slow pair can stay close for many intervals; each becomes one conjunction.

### Step 2: the bounds

Ruling a pair out is only useful if it is **never wrong**. A screen that silently misses close approaches is worse than none. So the test in step 2 has to be *conservative*: it may let through pairs that won't come close (they just cost a refinement), but it must never discard a pair that will.

Take one interval of length Δt. Let **d₀** and **d₁** be the relative position of the pair (object B minus object A) at its start and end. If both objects moved in straight lines, the relative position would move along the straight **chord** from d₀ to d₁. They don't; orbits curve. But a curve can only stray from its chord by an amount set by its curvature. For a path whose second derivative is at most *a*, the standard result for linear interpolation gives:

```
distance from chord  ≤  a · Δt² / 8
```

Each object's path curves at most *a*_obj (measured below), so the relative path curves at most 2·*a*_obj. That gives two tests:

```
pad = 2 · a_obj · Δt² / 8                         (0.25 km for Δt = 10 s)

chord bound:   min over the chord of |d₀ + (d₁ − d₀)s|  ≤  threshold + pad
coarse bound:  |d₀|  ≤  threshold + 2 · v_max · Δt + pad
```

The coarse bound is the cheap one: one squared distance, no division. It follows from the chord bound, because no object moves more than *v*_max·Δt per step, so the chord can't be longer than 2·*v*_max·Δt. Here *v*_max is measured each step as the longest distance any object actually moved. Only pairs passing the coarse bound get the chord test, and only pairs passing that get refined.

On the live catalog, in one hour: **75.8 billion** pair checks → **8.6 million** pass the coarse bound → **10,331** pass the chord bound and are refined → **2,691** are real conjunctions.

### The trap: don't trust SGP4's velocity

The first version of the bounds used SGP4's *velocity* output. It assumed relative motion d(τ) ≈ d₀ + w·τ, with w the relative velocity, plus a curvature margin. That's the textbook approach, and a test that checked the bound against real SGP4 trajectories broke it immediately.

**SGP4's velocity is not the derivative of SGP4's position.** It is computed with a separate, truncated formula. Measured on the live catalog, the two differ by up to:

| Object | Orbit | \|v_SGP4 − dr/dt\| |
|---|---|---|
| 37755 SPEKTR-R | e = 0.93, deep space | 52 m/s |
| 67020 STARLINK-36172 | re-entering, B\* = 0.29 | 45 m/s |
| 66836 STARLINK-35838 | re-entering | 25 m/s |
| 48838 SXM-8 | geostationary | 12 m/s |

A 52 m/s error over 10 s is a 520 m position error, more than double the margin. A screen built on it could miss real encounters involving these objects. That's why the bounds above use **positions only**: the chord between two sampled positions, never the velocity. The velocity is used only to report the relative speed at TCA, where a few tens of m/s don't matter.

### Measuring a_obj

How sharply can an SGP4 path curve? Physically it's gravity, at most ~9.83 m/s² at Earth's surface. But after the velocity surprise, that was measured rather than assumed: the second difference of position (r(t−h) − 2r(t) + r(t+h)) / h², for every object in the live catalog, every ~3 minutes for 7 days, at h = 1 s and 10 s.

The worst case across all 20,489 objects is **9.80 m/s²**, for STARLINK-4464 minutes from re-entry, where gravity really is that strong. Positions behave physically even where velocities don't. The code uses **a_obj = 10.0 m/s²**, and `TestChordBound` checks the chord bound directly on Vallado's most extreme orbits: an 82 km perigee, e = 0.97, heavy drag, deep-space resonance. The worst case uses 97% of the bound.

(Objects with a perigee *below the surface* break any bound, because SGP4 happily produces positions inside the Earth. They're removed when the catalog loads; see [chapter 4](04-catalog.md#sub-orbital-objects).)

### A second trap: SGP4 that blows up without an error

The coarse bound uses the fastest object in the catalog, so it's only as good as the worst sample. The first full SOCRATES comparison ([chapter 8](08-socrates.md)), on Space-Track's whole catalog with debris, ran for over an hour without finishing. A 7-day screen normally takes minutes. The cause was three decaying debris objects (CZ-6A DEB 60778 and 54922, CZ-4C DEB 35248). A few days past their epochs their SGP4 elements blow up, and they're flung out to 70,000–230,000 km at **~6,000 km/s**, with no SGP4 error. One such sample makes the coarse bound ~120,000 km, every pair passes it, and the grid turns into brute force for the rest of the week.

Nothing in orbit can move faster than Earth's escape speed at the surface, 11.2 km/s. So an object that moves faster than that in a step is treated like an SGP4 failure for that step: it's left out of the step's pairs and out of the speed bound. Its positions there are meaningless anyway. `TestDivergedObjectNotLive` covers it.

### Step 3: refinement

Each surviving pair-interval gets a **golden-section search** on the true distance: two SGP4 calls per iteration, 25 iterations. That shrinks a 10 s interval to ~0.1 ms. At 15 km/s that's 1.5 m along track, and since distance is flat at its minimum, the miss distance is accurate far below a metre. The two interval edges are also checked, in case the minimum sits on a boundary because the encounter peaks in the neighbouring interval.

### Step 4: merging, and two kinds of non-event

A conjunction is a **local minimum** of the distance between two objects, closer than the threshold. That's also how SOCRATES counts them. A slow pair, such as two satellites from the same launch, can stay within 5 km for hours and pass through a minimum every orbit, and each minimum is a separate conjunction.

The same minimum can be detected twice. When it lies inside one interval, the neighbouring interval's closest point is its shared end, where the distance is still falling. Refinement marks such *edge* detections, and merging drops an edge detection when a detection of the same pair within one step is at least as close. (An earlier version instead chained every detection of a pair within two steps into one conjunction. For slow pairs that merged several real minima into one; the SOCRATES comparison exposed it, see [chapter 8](08-socrates.md).)

Two things are then dropped:

- **Window-edge minima.** If an encounter's closest point is exactly the start or end of the window, the pair was closer before the window (or will be after it). That's not a TCA.
- **Attached objects** (the ISS and its modules, docked vehicles) are skipped before any of this; see [chapter 4](04-catalog.md#attached-objects).

### How the screener is tested

[`screen_test.go`](../internal/screen/screen_test.go):

- **Synthetic encounters.** Seven made-up satellites in orbits designed to cross, at relative speeds from 0.13 km/s to ~9 km/s, over 3 hours. Ground truth comes from brute force: sample the true distance every 50 ms, then every 10 µs around each dip. The screener must find **every** encounter (16 of them), with TCAs within 1 ms and miss distances within 1 m. Writing this test exposed that a 50 ms ground truth wasn't fine enough: at 9 km/s the screener's answer was *more* accurate than the "truth".
- **Chord bound** on Vallado's extreme orbits, as above.
- **Edges and attachment.** Co-located objects, an encounter just before the window starts, and an attached pair all produce nothing.
- **Primary-only mode** only reports pairs involving a primary.

## The brute-force baseline

[`BruteForce`](../internal/screen/engine.go) tests **every pair at every step**. Run on one core (`-workers 1`), it is the "before" number that milestone 3 has to beat, and the reference answer the faster screeners must reproduce exactly.

The pair loop is as lean as a naive loop gets. Positions are stored as separate x/y/z arrays (struct of arrays) so the inner loop streams through memory, and the coarse bound is a squared-distance compare with no square root.

### Results

Apple M4, one core, live catalog of 20,489 objects, window starting 2026-10-12 06:00 UTC, Δt = 10 s, threshold 5 km:

| Run | Wall time | Per step | Conjunctions |
|---|---|---|---|
| All pairs, 1 hour | 1 min 37 s | 270 ms | 2,691 |
| Active vs all (SOCRATES mode), 1 hour | 2 min 3 s | 341 ms | 2,656 |
| **All pairs, 7 days** | *running, see below* | | |

Projected from the 1-hour run, a full 7-day brute-force screen takes **~4.5 hours** (60,480 steps × 270 ms).

**Scaling.** Time per step against catalog size (random subsets, 60 steps each):

| Objects | Per step | vs previous |
|---|---|---|
| 2,500 | 4.6 ms | |
| 5,000 | 17.1 ms | 3.7× |
| 10,000 | 66.0 ms | 3.9× |
| 20,489 | ~214–270 ms | ~4× |

Double the objects and the time goes up ~4×: textbook O(N²). Each pair check costs about **1.3 ns**, so the inner loop itself is close to as fast as it gets. The only way to go much faster is to **check fewer pairs**, which is milestone 3.

**A surprise: SOCRATES mode is slower.** Screening only pairs involving an active payload does ~3% *fewer* pair checks but takes 26% *longer*. The likely culprit is the extra `if` in the inner loop ("is either object a primary?"). 82% of objects are primaries, in no particular order, so the branch is unpredictable, and a mispredicted branch costs more than the distance check it was meant to skip. This is a hypothesis for now; milestone 3's profiling will confirm or refute it. Either way the lesson holds: a filter in the inner loop isn't free.

### What the conjunctions look like

In the one-hour all-pairs run, the closest approach was **STARLINK-5743 vs STARLINK-36528 at 120 m, 10.0 km/s**. Most conjunctions are Starlink against Starlink. With ~8,000 satellites in a few thin shells around 550 km, the density is high enough that a rough estimate (density × π·(5 km)² × relative speed) predicts several hundred 5 km encounters every few minutes, which matches what the screener finds.

That number needs context. These are **SGP4/TLE** distances, and TLE positions are uncertain by a kilometre or more. A "120 m" approach means "these two are close enough to look at with better data". Starlink satellites also manoeuvre autonomously to avoid each other, using SpaceX's own far more precise ephemerides.

← [4. The catalog](04-catalog.md) · [Glossary](glossary.md)
