# 4. The catalog: where the data comes from

The screener needs orbit data for every object in orbit. This chapter covers where that data comes from, the two formats it's published in, and what [`cmd/fetch`](../cmd/fetch/main.go) and [`internal/catalog`](../internal/catalog/catalog.go) do with it.

## Sources

There are two public distributors of the US Space Force's orbit data:

| Source | What you get | Account needed |
|---|---|---|
| [Space-Track.org](https://www.space-track.org/) | The full public catalog, the original source | Yes (free, with a user agreement) |
| [CelesTrak](https://celestrak.org/NORAD/elements/) | Curated **groups**: active satellites, Starlink, GEO, specific debris clouds… | No |

CelesTrak has **no single "every object" download**. You can fetch a group, a launch, or a single catalog number, but not the whole catalog at once. So `cmd/fetch` assembles as much as it can from groups:

| File | CelesTrak query | Objects (12 Oct 2026) | Why |
|---|---|---|---|
| `active.csv` | `GROUP=active` | 16,700 | Every operational payload. These are the **primaries** SOCRATES screens. |
| `gpz-plus.csv` | `SPECIAL=gpz-plus` | 1,707 | Everything in or near the geostationary belt, including dead satellites and rocket bodies |
| `fengyun-1c-debris.csv` | `GROUP=fengyun-1c-debris` | 1,978 | Debris from the 2007 Chinese anti-satellite test |
| `cosmos-2251-debris.csv` | `GROUP=cosmos-2251-debris` | 588 | Debris from the 2009 Iridium–Cosmos collision |
| `iridium-33-debris.csv` | `GROUP=iridium-33-debris` | 109 | Debris from the other half of that collision |
| `last-30-days.csv` | `GROUP=last-30-days` | 225 | Recent launches, including rocket bodies |

After removing duplicates (an object can be in several groups), that's **20,489 objects**. The full public catalog is around 30,000 objects in orbit, so roughly a third is missing. The missing part is mostly debris from other breakups, dead satellites and spent rocket bodies outside GEO. **This matters for the SOCRATES comparison.** SOCRATES screens against the full catalog, so it will report conjunctions with objects we don't have. The SOCRATES comparison and the final speed numbers therefore use Space-Track's full catalog, 32,591 objects in orbit on 13 October; see [chapter 8](08-socrates.md#results).

`cmd/fetch` reuses any file younger than 2 hours instead of downloading it again. CelesTrak only updates every few hours and asks users not to poll more often than that.

## TLE vs OMM: why we fetch CSV

The same orbital elements are published in two formats:

- **TLE**, the fixed-column text format from [chapter 1](01-tle-format.md).
- **OMM** (CCSDS Orbit Mean-Elements Message), a modern standard with named fields, available as XML, KVN, JSON or CSV.

The TLE format has a hard limit: **five columns for the catalog number**. The Alpha-5 letter trick stretches that to 339,999, but CelesTrak has stopped publishing TLEs for newly catalogued objects with 6-digit numbers (100000 and up). Those objects are **only** available as OMM.

You can see this directly. On the same day, CelesTrak's active group had **15,939** objects in TLE format and **16,700** in OMM CSV. The 761-object difference is the newest satellites, which a TLE-only screener would silently miss.

So `cmd/fetch` downloads CSV, and [`tle.ReadOMMCSV`](../pkg/tle/omm.go) parses it into the same `TLE` struct the TLE parser fills. SGP4 can't tell which format the elements came from. The reader finds columns by header name, so it also accepts Space-Track's CSV, which has many more columns in a different order. One small bonus: OMM gives B\* to full precision (`.35967262E-4`), where a TLE gives five digits (`35967-4`).

## Loading: `catalog.Load`

`Load` takes a list of files and returns one ready-to-propagate `Object` per catalog number:

1. **Sniff the format.** A file starting with an OMM header is read as CSV; anything else is read as TLE.
2. **Deduplicate.** When the same catalog number appears more than once, keep the element set with the **latest epoch**. It was fitted to the most recent tracking data.
3. **Mark primaries.** Objects that appear in a primary file (`active.csv`) get `Primary = true`, even if their newest element set came from another file.
4. **Initialise SGP4** for each object. Objects SGP4 rejects at their own epoch are dropped and counted.
5. **Drop sub-orbital objects** (see below).
6. **Group attached objects** (see below).

Records that fail to parse are counted rather than aborting the load. On the live data: 21,307 records, 818 duplicates, 0 bad, 0 rejected, 0 sub-orbital.

### Sub-orbital objects

An element set whose **perigee is below Earth's surface** describes an object that will re-enter within the hour. SGP4 positions along such an orbit are meaningless. Worse, they break the motion bounds the screener relies on ([chapter 5](05-screening.md)): Vallado's synthetic test case 33333, with a perigee 6,300 km *underground*, strays 1.3 km from a straight line in 10 seconds. These objects are dropped at load time. The live catalog has none, but Vallado's test set has two.

### Attached objects

The first screening run "found" dozens of conjunctions at exactly **0.000 km and 0.00 km/s**: the ISS against its own modules (Zvezda, Destiny, Nauka, Poisk), the ISS against docked Soyuz, Progress and Crew Dragon vehicles, and Intelsat 10-02 against MEV-2, a servicing spacecraft docked to it.

These objects are catalogued separately but **physically attached**, so they are published with identical element sets. `Load` finds groups of objects with bit-identical elements and gives each group a shared `Group` number, and the screeners skip pairs within a group. On the live data, 17 objects are in attached groups.

Formation-flying satellites that aren't attached keep their own element sets and are still screened. For example, SUPERVIEW NEO-2 03 and 04 pass 173 m apart at 0.00 km/s relative speed.

← [3. Verification](03-verification.md) · [5. Screening →](05-screening.md)
