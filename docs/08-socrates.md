# 8. Validation against SOCRATES

A screener can be internally consistent and still wrong in ways no unit test anticipates. So the last check compares it with an independent, established service: [SOCRATES Plus](https://celestrak.org/SOCRATES/), which CelesTrak has run since 2004. The headline question is: **what share of SOCRATES's close approaches do we also find?**

The code is in [`internal/socrates`](../internal/socrates/) and [`cmd/socrates`](../cmd/socrates/main.go).

## What SOCRATES does

From CelesTrak's documentation and the run we compare against:

| | SOCRATES Plus run used here |
|---|---|
| Software | AGI's STK Conjunction Analysis Tools, with the SGP4 propagator |
| Data | GP element sets current as of **2026-10-12 08:22:44 UTC**, or SupGP where available (see below) |
| Window | 2026-10-12 09:00 UTC to 2026-10-19 09:00 UTC (7 days) |
| Threshold | 5 km at TCA |
| Primaries | 16,698 active satellites |
| Secondaries | 32,584 objects: the full catalog, including analyst objects |
| Result | **206,222 conjunctions** (computed in 14 h 02 min) |

Three of SOCRATES's rules matter for a fair comparison:

1. **Only pairs involving an active satellite are screened.** This is our `-primary-only` mode.
2. **Fully operational satellites of the same constellation aren't screened against each other.** The operator (SpaceX, OneWeb, …) has far better data for its own fleet. CelesTrak works out the constellations dynamically each run. We can't reproduce that grouping exactly, so this only affects conjunctions we report that SOCRATES doesn't, never the ones we're scored on.
3. **Docked objects** (ISS, Tiangong, the MEV servicers) form their own "constellations" and aren't screened against each other. That's our attached-object grouping ([chapter 4](04-catalog.md#attached-objects)).

SOCRATES also uses **SupGP** element sets where they exist. These are fitted by CelesTrak to ephemerides that operators publish (Starlink, OneWeb, Kuiper, Planet, GPS, …), and are much more accurate than the US Space Force's GP sets for those satellites.

## The data must match

A first attempt screened with the freshest CelesTrak data, about a day newer than SOCRATES's. Only **12%** of SOCRATES's conjunctions matched. Yet for the 90 conjunctions where both objects happened to have the *same* element set in both runs, the match rate was **100%** (after fixing the bug below). TCAs agreed to a median of 0 ms (max 88 ms), and miss distances to within 0.5 m.

For every miss, the tool checks our own closest approach for that pair. None of them was a conjunction our screener should have reported and didn't. All were explained by the data difference: we computed more than 5 km (143,333), within 0.25 km of the threshold (1,170), a minimum more than 2 minutes from SOCRATES's TCA (657), or a conjunction we did report, but more than 60 s from SOCRATES's TCA (217).

That's the most important lesson of the comparison. **Conjunctions are extremely sensitive to which element sets you use.** A newer TLE moves an object by a kilometre or more, enough to make most 5 km conjunctions appear, disappear or shift. Comparing two screens run on different data mostly measures the data difference, not the screeners.

So the comparison rebuilds the catalog **exactly as SOCRATES saw it**.

### Recovering SOCRATES's element sets

SOCRATES's CSV doesn't list the element sets it used, but it gives, for each object in each conjunction, the **days since epoch** (DSE) at TCA. TCA − DSE is the epoch of the element set it used, to ±43 s (DSE has three decimals).

Space-Track keeps every element set ever published (`gp_history`), with the time each was published (`CREATION_DATE`). For each object, `socrates.AsOf` picks:

1. **The same set SOCRATES used:** the one whose epoch matches TCA − DSE within a minute.
2. Otherwise, the **latest set published by the run's data time**: for objects SOCRATES used SupGP for (not on Space-Track), or objects in none of its conjunctions.
3. Objects first published after the data time (new launches) are left out; SOCRATES can't have seen them.

## Matching

Each SOCRATES conjunction is matched to the conjunction of ours with the same two objects and the nearest TCA, within 60 s. Each of ours can match only once. Recall is reported separately for conjunctions where **both objects had the same element sets** in both runs (a direct test of the screener) and for the rest (which mixes in data differences: SupGP, missing history).

For every SOCRATES conjunction we miss, the tool computes our own closest approach for that pair near SOCRATES's TCA, by brute-force sampling. That separates "we computed more than 5 km" (a data or model difference) from "we computed less than 5 km but didn't report it" (a bug in the screener).

## A bug the comparison found

The very first comparison (79 of 82 same-data conjunctions found) flagged three conjunctions where both objects had the same data and our own closest-approach check got **exactly** SOCRATES's distance (2.350 km, 3.065 km, 3.490 km), yet the screener hadn't reported them.

All three were slow pairs from the same launch (Transporter-18 objects, a Hulianwang pair) that stay within 5 km for hours and pass through a minimum every orbit. The screener's merge step chained every detection of a pair within 20 s of the previous one into a single conjunction, so a pair that never left the 5 km sphere got one conjunction for its whole stay. SOCRATES reports each minimum. The merge now keeps every local minimum ([chapter 5](05-screening.md#step-4-merging-and-two-kinds-of-non-event)), and `TestSlowPairEveryMinimum` covers it.

## Results

| SOCRATES conjunctions | Found by us | Recall |
|---|---|---|
| Both objects with the same element sets | 205,716 / 205,716 | **100%** |
| Different element sets (SupGP, missing history) | 177 / 502 | 35.26% |
| An object not in our data | 0 / 4 | 0% |
| **All** | **205,893 / 206,222** | **99.84%** |

**Every** SOCRATES conjunction computed from the same data as ours is found, in every 1 km band from 0 to 5 km.

**Accuracy**, for those 205,716 conjunctions: the TCAs agree to a median of 0 ms (95th percentile 0 ms at the CSV's millisecond precision, max 0.42 s), and the miss distances to a median of 0.2 m (**max 0.5 m**). Two independent implementations, STK's SGP4 and this project's, agree to half a metre on every one of them. The few larger TCA differences are slow pairs, whose distance is nearly flat around its minimum.

**The misses** (329) all involve an object with different element sets: SupGP, or a set missing from the downloaded history. The tool computes our own closest approach for each pair near SOCRATES's TCA. In no case did we compute under 5 km and fail to report it, so **no miss is a screener bug**: 306 are over 5.25 km with our data, 11 are borderline (5.00–5.25 km), and 8 come within 5 km only at the edge of the ±2 min search, with the true minimum further away.

**What we report and SOCRATES doesn't:** 771,087 conjunctions, of which 762,525 are Starlink against Starlink and ~6,000 more are between other active satellites of the same fleet (Qianfan, Tianhui, OneWeb, Kuiper, …). SOCRATES skips these by design (rule 2 above). Of the remaining ~2,500, 199 involve an object in none of SOCRATES's conjunctions and 89 are borderline (our miss 4.75–5 km).

### A data bug the comparison found

The first full run gave 99.95%, not 100%. The 103 same-data misses, with miss distances off by up to 3.5 km, involved only a handful of objects, 23 of them one Transporter-16 object. Space-Track sometimes **republishes an element set with the same epoch**: a corrected fit, with a later `CREATION_DATE` and slightly different elements. `AsOf` matched on epoch alone and picked whichever copy it saw first, sometimes one published after SOCRATES's run. It now picks, among sets with the right epoch, the latest one published by the run's data time, and `TestAsOf` covers it. That took same-data recall to 100%.

← [7. Optimisation](07-optimisation.md) · [Glossary](glossary.md)
