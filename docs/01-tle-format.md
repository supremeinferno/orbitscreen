# 1. TLEs: the input data

Everything this project does starts from **Two-Line Element sets (TLEs)**. A TLE is a compact text description of one object's orbit at one moment in time. This chapter explains what is in a TLE, where it comes from, and how [`pkg/tle`](../pkg/tle/tle.go) parses it.

## Where TLEs come from

The US Space Force tracks objects in orbit with radar and optical sensors. For each object it fits a simple orbit model (SGP4, see [chapter 2](02-sgp4.md)) to the tracking data and publishes the fitted parameters as a TLE on [Space-Track.org](https://www.space-track.org/). [CelesTrak](https://celestrak.org/) redistributes them in convenient groups, without needing an account.

An object's TLE is refreshed every few hours to few days, whenever new tracking data comes in. Each TLE is a snapshot. It describes the orbit well near its **epoch** (the moment it was fitted for) and worse the further you predict away from it.

## Anatomy of a TLE

Here is the International Space Station in September 2008. This is the classic example used in the SGP4 literature and in our tests:

```
ISS (ZARYA)
1 25544U 98067A   08264.51782528 -.00002182  00000-0 -11606-4 0  2927
2 25544  51.6416 247.4627 0006703 130.5360 325.0288 15.72125391563537
```

The first line is an optional name (the "3-line" format). Then come two lines of exactly 69 characters each. **Every field lives at fixed column positions.** This is the most important thing to know about TLEs:

### Line 1

| Columns | Example | Field | Notes |
|---|---|---|---|
| 1 | `1` | Line number | |
| 3–7 | `25544` | NORAD catalog number | Unique ID of the object |
| 8 | `U` | Classification | U = unclassified |
| 10–17 | `98067A` | International designator | Launch year 1998, 67th launch, piece A |
| 19–20 | `08` | Epoch year | 2 digits: 57–99 → 19xx, 00–56 → 20xx |
| 21–32 | `264.51782528` | Epoch day of year | Day 1.0 = midnight at the *start* of 1 January |
| 34–43 | `-.00002182` | ṅ/2 | First derivative of mean motion, halved (rev/day²). SGP4 ignores it |
| 45–52 | `00000-0` | n̈/6 | Second derivative, in implied-exponent format. SGP4 ignores it |
| 54–61 | `-11606-4` | B\* | Drag term, in implied-exponent format |
| 63 | `0` | Ephemeris type | Always 0 in public data |
| 65–68 | `292` | Element set number | Counts how many TLEs have been issued |
| 69 | `7` | Checksum | |

### Line 2

| Columns | Example | Field | Notes |
|---|---|---|---|
| 3–7 | `25544` | Catalog number | Must match line 1 |
| 9–16 | `51.6416` | Inclination (°) | Tilt of the orbit relative to the equator |
| 18–25 | `247.4627` | RAAN (°) | Right ascension of the ascending node: where the orbit crosses the equator going north |
| 27–33 | `0006703` | Eccentricity | **Implied leading decimal point**: 0.0006703. 0 = circle |
| 35–42 | `130.5360` | Argument of perigee (°) | Where in the orbit the lowest point is |
| 44–51 | `325.0288` | Mean anomaly (°) | Where the object is along its orbit at epoch |
| 53–63 | `15.72125391` | Mean motion (rev/day) | Orbits per day. ISS ≈ 15.7, i.e. about 92 minutes per orbit |
| 64–68 | `56353` | Revolution number | Orbits completed since launch |
| 69 | `7` | Checksum | |

Inclination, RAAN, eccentricity, argument of perigee, mean anomaly and mean motion are the six **orbital elements**. Together they pin down the size, shape and orientation of the orbit and where the object is on it. See the [glossary](glossary.md) for pictures in words.

## Parsing decisions

### Never split on spaces

It is tempting to write `strings.Fields(line)`, but fields are only separated by position, not by spaces. Look at the end of the ISS line 2: `15.72125391563537` is the mean motion `15.72125391` immediately followed by the revolution number `56353` and the checksum `7`, with no space at all. In the other direction, a blank international designator (common in old TLEs) makes a field disappear entirely. The parser slices exact columns instead, using a helper that converts the 1-based column numbers from the spec into Go slice bounds:

```go
func col(line string, from, to int) string { return line[from-1 : to] }
```

so that the code reads like the table above: `col(line2, 9, 16)` is the inclination.

### Checksums

Column 69 is a checksum digit. Add up every digit in columns 1–68, count each `-` as 1, ignore everything else, and take the result mod 10. `Parse` rejects any line whose checksum doesn't match, with `ErrChecksum`. A single corrupted digit in an inclination would otherwise silently produce a wrong orbit.

### The implied-exponent format

B\* and n̈/6 are written in a cramped scientific notation. There is an implied `0.` before five digits, then an exponent:

```
-11606-4   →   -0.11606 × 10⁻⁴
 12345-3   →   +0.12345 × 10⁻³
```

`parseImpliedExp` rebuilds this as a normal string (`"-0.11606e-4"`) and lets `strconv.ParseFloat` convert it. That guarantees correct rounding, which multiplying by `math.Pow(10, exp)` would not.

### Epochs

`08264.51782528` means year 2008, day 264.51782528. Two traps:

- **Two-digit years.** By convention 57–99 means 1957–1999 (Sputnik launched in 1957) and 00–56 means 2000–2056.
- **Day 1.0 is midnight at the start of 1 January**, not day 0. Day 264.5 of 2008 (a leap year) is 20 September at noon.

`epochToTime` adds whole days and the fractional day separately. Adding them as one float would throw away precision in the fraction.

### Alpha-5 catalog numbers

Catalog numbers only have five columns, and the catalog is heading past 99,999. The agreed fix ("Alpha-5") replaces the first digit with a letter: `A0001` = 100001, … `Z9999` = 339999. The letters I and O are skipped because they look like 1 and 0. The parser supports this already, so it won't break the day the catalog crosses 100,000.

### Reading whole files

`ReadAll` reads a whole CelesTrak file. It accepts the 2-line format, the 3-line format with names, and Space-Track's `0 NAME` name lines. One bad record doesn't abort the read: you get every good TLE back, plus an error listing each bad record with its line number, and the caller decides what to do. On today's CelesTrak active catalog (15,939 objects), zero records fail.

## Tests

[`tle_test.go`](../pkg/tle/tle_test.go) checks:

- every field of the ISS TLE above;
- checksum computation;
- the implied-exponent format, including `+` exponents and all-zero values;
- epoch edge cases (leap year, 1957/2056 boundary, day 1.0);
- rejection of corrupted and truncated lines;
- Alpha-5 catalog numbers;
- `ReadAll` on mixed 2- and 3-line input with a bad record in it.

Next: [2. SGP4: from elements to positions →](02-sgp4.md)
