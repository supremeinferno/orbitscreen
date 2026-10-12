# Glossary

Terms used in the code and docs, in plain words. Variable names from Vallado's code are listed where they appear.

**Apogee / perigee.** The highest and lowest points of an orbit. Altitudes are measured above Earth's surface; radii are measured from Earth's centre.

**Argument of perigee** (`argpo`, ω). The angle, within the orbit plane, from the ascending node to the perigee. It says which way the ellipse "points".

**B\* (B-star)** (`bstar`). The TLE's drag term. It bundles the object's area-to-mass ratio and atmospheric density into one fitted number. Bigger means more drag. It can come out negative when the fit absorbs other errors.

**Catalog number / NORAD ID.** The unique ID for each tracked object (ISS = 25544). See also *Alpha-5*.

**Alpha-5.** The scheme for catalog numbers above 99,999 that still fit in five TLE columns: the first digit becomes a letter (A = 10 … Z = 33, skipping I and O). `A0001` = 100001.

**Attached objects.** Separately catalogued objects that are physically joined, like ISS modules, docked spacecraft, or a servicing vehicle docked to its client. They share one element set, so the screener skips pairs within a group (`Object.Group`).

**Chord bound.** The screener's main filter: over one sampling interval, two objects' relative path stays within a·Δt²/8 of the straight line (chord) between its sampled endpoints. See [chapter 5](05-screening.md#step-2-the-bounds).

**Close approach / conjunction.** Two objects passing within some threshold distance of each other (SOCRATES uses 5 km). Not the same as a collision.

**Deep space / SDP4.** The branch of SGP4 for orbits with periods of 225 minutes or more, which adds lunar and solar gravity and resonance effects (`deep` in the code, `method == 'd'` in Vallado's).

**Eccentricity** (`ecco`, e). How stretched the ellipse is. 0 = circle, approaching 1 = very elongated. Most LEO objects are below 0.01. Molniya orbits are about 0.7.

**Epoch.** The moment a TLE describes. Predictions are best near the epoch and degrade with distance from it, in both directions.

**GEO (geostationary orbit).** A circular, equatorial orbit at ~35,786 km altitude with a period of one sidereal day, so the satellite appears fixed in the sky.

**Inclination** (`inclo`, i). The tilt of the orbit plane relative to the equator. 0° = equatorial, 90° = polar, above 90° = retrograde.

**J2, J3, J4.** Coefficients describing how Earth's gravity differs from a perfect sphere's. J2 (the equatorial bulge) is by far the largest, and it makes orbit planes precess.

**Kepler's equation.** M = E − e·sin E. It links *mean anomaly* M (which grows uniformly with time) to *eccentric anomaly* E (which gives the actual position on the ellipse). It has no closed-form solution, so SGP4 solves it by Newton iteration.

**Kozai vs Brouwer mean motion.** Two conventions for "average" orbital rate that differ slightly when J2 is included. TLEs publish Kozai; SGP4's formulas use Brouwer (`no_unkozai`), so `init` converts.

**LEO (low Earth orbit).** Roughly below 2,000 km altitude, with periods of about 90–130 minutes. It is the most crowded region and where most close approaches happen.

**Lyddane modification.** A reformulation of the lunar-solar periodic terms that avoids dividing by sin(inclination) for near-equatorial orbits. SGP4 switches to it below 0.2 rad (11.46°), and that switch was a classic source of bugs.

**Mean anomaly** (`mo`, M). Where the object is along its orbit, as an angle that increases at a constant rate.

**Mean elements.** Orbital elements with the short-term wobbles averaged out *in a model-specific way*. TLE elements are SGP4 mean elements, which is why they must be used with SGP4.

**Mean motion** (`no_kozai`, n). Orbits per day in the TLE (radians per minute internally). Period = 1440 / n minutes.

**Molniya orbit.** A highly eccentric (e ≈ 0.7), 63.4°-inclined, 12-hour orbit used for high-latitude coverage. It is a classic stress test for SGP4's 12-hour resonance code.

**OMM.** Orbit Mean-Elements Message, the CCSDS standard format for the same mean elements a TLE carries, with named fields (XML, KVN, JSON or CSV). Objects with 6-digit catalog numbers are only published as OMM. See [chapter 4](04-catalog.md#tle-vs-omm-why-we-fetch-csv).

**Primary / secondary.** In SOCRATES-style screening, *primaries* are active payloads (which can manoeuvre, so a warning is useful) and *secondaries* are everything else. Only pairs involving at least one primary are screened (`-primary-only`).

**Propagation.** Computing where an object will be (or was) at some time, starting from its elements.

**RAAN** (`nodeo`, Ω). Right Ascension of the Ascending Node: the direction in space where the orbit crosses the equator going north. Together with inclination, it fixes the orbit plane.

**Resonance** (`irez`). When an orbit's period is a simple fraction of Earth's rotation (12 h or 24 h), the satellite repeatedly passes over the same irregularities in Earth's gravity field, and their effect builds up. SGP4 integrates these effects numerically in 720-minute steps.

**Refinement.** Finding the exact time of closest approach and miss distance for a candidate pair, here by golden-section search on the true SGP4 distance.

**Secular vs periodic.** *Secular* changes accumulate steadily, like RAAN drifting a few degrees per day. *Periodic* changes oscillate and average out. SGP4 models both.

**SGP4.** Simplified General Perturbations 4, the analytical orbit model that TLEs are fitted with. See [chapter 2](02-sgp4.md).

**SOCRATES.** Satellite Orbital Conjunction Reports Assessing Threatening Encounters in Space: CelesTrak's public close-approach screening service, and the reference this project validates against.

**Space-Track.** [space-track.org](https://www.space-track.org/), the US Space Force's public distribution site for orbit data. It is the only source of the *full* public catalog, and needs a free account.

**TCA.** Time of Closest Approach: the moment two objects are nearest each other during an encounter. The distance at that moment is the *miss distance*.

**TEME.** True Equator, Mean Equinox: the Earth-centred inertial frame SGP4 outputs. All objects share it, so distances between them can be computed directly.

**TLE.** Two-Line Element set: the text format the orbit data comes in. See [chapter 1](01-tle-format.md).

**WGS-72.** The 1972 World Geodetic System Earth model. SGP4 uses its constants (Earth radius 6378.135 km, μ = 398600.8 km³/s²) because TLEs were fitted with them.
