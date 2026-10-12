// Package sgp4 propagates TLEs with the SGP4/SDP4 model.
//
// This is a port of David Vallado's reference implementation (the 2020
// C++ release that accompanies "Revisiting Spacetrack Report #3"), using
// WGS-72 constants and the "improved" operation mode, which is what
// CelesTrak and the published verification vectors use. Variable names
// follow Vallado's so the code can be checked line-by-line against his.
//
// Positions and velocities are in the TEME frame (True Equator, Mean
// Equinox), in km and km/s. For collision screening that's all we need:
// both objects are in the same frame, so distances are frame-independent.
//
// One deliberate difference from Vallado: Propagate never writes to the
// Satellite. His version caches the deep-space resonance integrator state
// between calls; here it always restarts from epoch. The integrator takes
// fixed 720-minute steps from epoch either way, so the results are
// identical, and a *Satellite is safe to share between goroutines.
package sgp4

import (
	"fmt"
	"math"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/tle"
)

// EarthRadiusKm is the WGS-72 equatorial radius SGP4 uses; it also defines
// "decayed" (error 6: orbit radius below one Earth radius).
const EarthRadiusKm = 6378.135

// WGS-72 earth constants. SGP4 was fitted with these, so TLEs are only
// consistent with WGS-72 even though WGS-84 is "more accurate".
const (
	mu            = 398600.8 // km^3/s^2
	radiusEarthKm = 6378.135 // km
	j2            = 0.001082616
	j3            = -0.00000253881
	j4            = -0.00000165597
	j3oj2         = j3 / j2
	twoPi         = 2 * math.Pi
	x2o3          = 2.0 / 3.0
	deg2rad       = math.Pi / 180
	// temp4 guards divisions by (1 + cos i) for inclinations near 180 deg.
	temp4 = 1.5e-12
)

var (
	xke       = 60.0 / math.Sqrt(radiusEarthKm*radiusEarthKm*radiusEarthKm/mu) // sqrt(mu) in earth radii^1.5 / min
	tumin     = 1 / xke                                                        // minutes per time unit
	vkmpersec = radiusEarthKm * xke / 60                                       // velocity unit -> km/s
)

// Error codes, as numbered in Vallado's code.
const (
	ErrMeanEccentricity      = 1 // mean eccentricity out of range
	ErrMeanMotion            = 2 // mean motion <= 0
	ErrPerturbedEccentricity = 3 // perturbed eccentricity out of range
	ErrSemiLatusRectum       = 4 // semi-latus rectum < 0
	ErrDecayed               = 6 // orbit radius < 1 earth radius: satellite has decayed
)

// Error reports a propagation failure. These are normal for real catalog
// data (mostly decaying objects), so callers should drop the object, not abort.
type Error struct {
	Code   int
	Tsince float64 // minutes from epoch
	Value  float64 // the offending quantity
}

func (e *Error) Error() string {
	what := map[int]string{
		ErrMeanEccentricity:      "mean eccentricity out of range [0, 1)",
		ErrMeanMotion:            "mean motion <= 0",
		ErrPerturbedEccentricity: "perturbed eccentricity out of range [0, 1]",
		ErrSemiLatusRectum:       "semi-latus rectum < 0",
		ErrDecayed:               "satellite has decayed (radius < 1 earth radius)",
	}[e.Code]
	return fmt.Sprintf("sgp4: error %d at t=%.4f min: %s (value %g)", e.Code, e.Tsince, what, e.Value)
}

// Satellite holds a TLE's elements plus everything SGP4 precomputes from
// them. Build one with New.
type Satellite struct {
	CatalogNum int
	Epoch      time.Time

	// Elements in SGP4 units (radians, radians/minute).
	bstar, ecco, argpo, inclo, mo, noKozai, nodeo float64
	noUnkozai                                     float64 // Brouwer mean motion

	// a, alta, altp: semi-major axis, apogee and perigee altitude (earth radii).
	a, alta, altp float64

	isimp bool // simplified drag (perigee < 220 km, or deep space)
	deep  bool // deep space: period >= 225 min

	aycof, con41, cc1, cc4, cc5, d2, d3, d4, delmo, eta float64
	argpdot, omgcof, sinmao, t2cof, t3cof, t4cof, t5cof float64
	x1mth2, x7thm1, mdot, nodedot, xlcof, xmcof, nodecf float64

	// Deep-space (SDP4) terms.
	irez                                                     int // 0 none, 1 one-day, 2 half-day resonance
	d2201, d2211, d3210, d3222, d4410, d4422                 float64
	d5220, d5232, d5421, d5433                               float64
	dedt, del1, del2, del3, didt, dmdt, dnodt, domdt         float64
	e3, ee2, peo, pgho, pho, pinco, plo                      float64
	se2, se3, sgh2, sgh3, sgh4, sh2, sh3, si2, si3, sl2, sl3 float64
	sl4, gsto, xfact, xgh2, xgh3, xgh4, xh2, xh3, xi2, xi3   float64
	xl2, xl3, xl4, xlamo, zmol, zmos                         float64
}

// New initialises SGP4 for a TLE (Vallado's twoline2rv + sgp4init). Like
// sgp4init it propagates to t=0 as a sanity check and returns that error,
// if any: an element set that can't even be evaluated at its own epoch
// isn't usable.
func New(t *tle.TLE) (*Satellite, error) {
	const xpdotp = 1440.0 / twoPi // rev/day -> rad/min
	s := &Satellite{
		CatalogNum: t.CatalogNum,
		Epoch:      t.Epoch,
		bstar:      t.BStar,
		ecco:       t.Eccentricity,
		argpo:      t.ArgPerigee * deg2rad,
		inclo:      t.Inclination * deg2rad,
		mo:         t.MeanAnomaly * deg2rad,
		noKozai:    t.MeanMotion / xpdotp,
		nodeo:      t.RAAN * deg2rad,
	}
	s.init(sgp4Epoch(t.Epoch))
	if _, _, err := s.Propagate(0); err != nil {
		return nil, err
	}
	return s, nil
}

// sgp4Epoch returns the epoch as sgp4init wants it: days since
// 1949-12-31 00:00 UT (JD 2433281.5).
//
// It's computed the way Vallado does, (JD of midnight + day fraction) -
// 2433281.5, including the rounding: a JD near 2.45 million held in a
// float64 only resolves ~40 µs. That rounding matters at the millimetre
// level for very eccentric deep-space orbits (verification case 23333), so
// copying it lets us match his published output to its printed precision.
func sgp4Epoch(t time.Time) float64 {
	t = t.UTC()
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	days := midnight.Sub(time.Date(1949, 12, 31, 0, 0, 0, 0, time.UTC)).Hours() / 24
	jd := 2433281.5 + math.Round(days)
	jdFrac := t.Sub(midnight).Seconds() / 86400
	return (jd + jdFrac) - 2433281.5
}

// init is Vallado's sgp4init (with initl inlined).
func (s *Satellite) init(epoch float64) {
	ss := 78.0/radiusEarthKm + 1.0
	qzms2ttemp := (120.0 - 78.0) / radiusEarthKm
	qzms2t := qzms2ttemp * qzms2ttemp * qzms2ttemp * qzms2ttemp

	// ---- initl: auxiliary epoch quantities ----
	eccsq := s.ecco * s.ecco
	omeosq := 1.0 - eccsq
	rteosq := math.Sqrt(omeosq)
	cosio := math.Cos(s.inclo)
	cosio2 := cosio * cosio

	// Un-Kozai the mean motion: TLEs carry Kozai mean motion, SGP4
	// works with Brouwer's.
	ak := math.Pow(xke/s.noKozai, x2o3)
	d1 := 0.75 * j2 * (3.0*cosio2 - 1.0) / (rteosq * omeosq)
	del := d1 / (ak * ak)
	adel := ak * (1.0 - del*del - del*(1.0/3.0+134.0*del*del/81.0))
	del = d1 / (adel * adel)
	s.noUnkozai = s.noKozai / (1.0 + del)

	ao := math.Pow(xke/s.noUnkozai, x2o3)
	sinio := math.Sin(s.inclo)
	po := ao * omeosq
	con42 := 1.0 - 5.0*cosio2
	s.con41 = -con42 - cosio2 - cosio2
	posq := po * po
	rp := ao * (1.0 - s.ecco)
	s.gsto = gstime(epoch + 2433281.5)
	// ---- end initl ----

	s.a = math.Pow(s.noUnkozai*tumin, -2.0/3.0)
	s.alta = s.a*(1.0+s.ecco) - 1.0
	s.altp = s.a*(1.0-s.ecco) - 1.0

	s.isimp = rp < 220.0/radiusEarthKm+1.0
	sfour := ss
	qzms24 := qzms2t
	perige := (rp - 1.0) * radiusEarthKm

	// For perigees below 156 km, s and qoms2t are altered.
	if perige < 156.0 {
		sfour = perige - 78.0
		if perige < 98.0 {
			sfour = 20.0
		}
		qzms24temp := (120.0 - sfour) / radiusEarthKm
		qzms24 = qzms24temp * qzms24temp * qzms24temp * qzms24temp
		sfour = sfour/radiusEarthKm + 1.0
	}
	pinvsq := 1.0 / posq

	tsi := 1.0 / (ao - sfour)
	s.eta = ao * s.ecco * tsi
	etasq := s.eta * s.eta
	eeta := s.ecco * s.eta
	psisq := math.Abs(1.0 - etasq)
	coef := qzms24 * math.Pow(tsi, 4.0)
	coef1 := coef / math.Pow(psisq, 3.5)
	cc2 := coef1 * s.noUnkozai * (ao*(1.0+1.5*etasq+eeta*(4.0+etasq)) +
		0.375*j2*tsi/psisq*s.con41*(8.0+3.0*etasq*(8.0+etasq)))
	s.cc1 = s.bstar * cc2
	cc3 := 0.0
	if s.ecco > 1.0e-4 {
		cc3 = -2.0 * coef * tsi * j3oj2 * s.noUnkozai * sinio / s.ecco
	}
	s.x1mth2 = 1.0 - cosio2
	s.cc4 = 2.0 * s.noUnkozai * coef1 * ao * omeosq *
		(s.eta*(2.0+0.5*etasq) + s.ecco*(0.5+2.0*etasq) -
			j2*tsi/(ao*psisq)*(-3.0*s.con41*(1.0-2.0*eeta+etasq*(1.5-0.5*eeta))+
				0.75*s.x1mth2*(2.0*etasq-eeta*(1.0+etasq))*math.Cos(2.0*s.argpo)))
	s.cc5 = 2.0 * coef1 * ao * omeosq * (1.0 + 2.75*(etasq+eeta) + eeta*etasq)
	cosio4 := cosio2 * cosio2
	temp1 := 1.5 * j2 * pinvsq * s.noUnkozai
	temp2 := 0.5 * temp1 * j2 * pinvsq
	temp3 := -0.46875 * j4 * pinvsq * pinvsq * s.noUnkozai
	s.mdot = s.noUnkozai + 0.5*temp1*rteosq*s.con41 +
		0.0625*temp2*rteosq*(13.0-78.0*cosio2+137.0*cosio4)
	s.argpdot = -0.5*temp1*con42 + 0.0625*temp2*(7.0-114.0*cosio2+395.0*cosio4) +
		temp3*(3.0-36.0*cosio2+49.0*cosio4)
	xhdot1 := -temp1 * cosio
	s.nodedot = xhdot1 + (0.5*temp2*(4.0-19.0*cosio2)+2.0*temp3*(3.0-7.0*cosio2))*cosio
	xpidot := s.argpdot + s.nodedot
	s.omgcof = s.bstar * cc3 * math.Cos(s.argpo)
	s.xmcof = 0.0
	if s.ecco > 1.0e-4 {
		s.xmcof = -x2o3 * coef * s.bstar / eeta
	}
	s.nodecf = 3.5 * omeosq * xhdot1 * s.cc1
	s.t2cof = 1.5 * s.cc1
	if math.Abs(cosio+1.0) > 1.5e-12 {
		s.xlcof = -0.25 * j3oj2 * sinio * (3.0 + 5.0*cosio) / (1.0 + cosio)
	} else {
		s.xlcof = -0.25 * j3oj2 * sinio * (3.0 + 5.0*cosio) / temp4
	}
	s.aycof = -0.5 * j3oj2 * sinio
	delmotemp := 1.0 + s.eta*math.Cos(s.mo)
	s.delmo = delmotemp * delmotemp * delmotemp
	s.sinmao = math.Sin(s.mo)
	s.x7thm1 = 7.0*cosio2 - 1.0

	// ---- deep space initialisation ----
	if twoPi/s.noUnkozai >= 225.0 {
		s.deep = true
		s.isimp = true
		ds := dscom(epoch, s.ecco, s.argpo, 0, s.inclo, s.nodeo, s.noUnkozai)
		s.e3, s.ee2, s.peo, s.pgho, s.pho, s.pinco, s.plo = ds.e3, ds.ee2, ds.peo, ds.pgho, ds.pho, ds.pinco, ds.plo
		s.se2, s.se3, s.sgh2, s.sgh3, s.sgh4, s.sh2, s.sh3 = ds.se2, ds.se3, ds.sgh2, ds.sgh3, ds.sgh4, ds.sh2, ds.sh3
		s.si2, s.si3, s.sl2, s.sl3, s.sl4 = ds.si2, ds.si3, ds.sl2, ds.sl3, ds.sl4
		s.xgh2, s.xgh3, s.xgh4, s.xh2, s.xh3 = ds.xgh2, ds.xgh3, ds.xgh4, ds.xh2, ds.xh3
		s.xi2, s.xi3, s.xl2, s.xl3, s.xl4 = ds.xi2, ds.xi3, ds.xl2, ds.xl3, ds.xl4
		s.zmol, s.zmos = ds.zmol, ds.zmos
		// Vallado calls dpper here with init='y', but in that mode it
		// changes nothing, so the call is omitted.
		s.dsinit(&ds, xpidot)
	}

	// ---- set variables if not deep space ----
	if !s.isimp {
		cc1sq := s.cc1 * s.cc1
		s.d2 = 4.0 * ao * tsi * cc1sq
		temp := s.d2 * tsi * s.cc1 / 3.0
		s.d3 = (17.0*ao + sfour) * temp
		s.d4 = 0.5 * temp * ao * tsi * (221.0*ao + 31.0*sfour) * s.cc1
		s.t3cof = s.d2 + 2.0*cc1sq
		s.t4cof = 0.25 * (3.0*s.d3 + s.cc1*(12.0*s.d2+10.0*cc1sq))
		s.t5cof = 0.2 * (3.0*s.d4 + 12.0*s.cc1*s.d3 + 6.0*s.d2*s.d2 + 15.0*cc1sq*(2.0*s.d2+cc1sq))
	}
}

// GMST returns Greenwich mean sidereal time (radians, in [0, 2π)) at t:
// the angle Earth has rotated, which is exactly the rotation from SGP4's
// TEME frame to the Earth-fixed frame. UTC is used in place of UT1; the two
// differ by under a second, i.e. under ~0.5 km at the equator.
func GMST(t time.Time) float64 {
	jd := 2440587.5 + float64(t.UnixNano())/86400e9
	return gstime(jd)
}

// gstime returns Greenwich mean sidereal time (radians) for a UT1 Julian date
// (IAU-82 model).
func gstime(jdut1 float64) float64 {
	tut1 := (jdut1 - 2451545.0) / 36525.0
	temp := -6.2e-6*tut1*tut1*tut1 + 0.093104*tut1*tut1 +
		(876600.0*3600+8640184.812866)*tut1 + 67310.54841 // seconds
	temp = math.Mod(temp*deg2rad/240.0, twoPi) // 360/86400 = 1/240, to deg, to rad
	if temp < 0.0 {
		temp += twoPi
	}
	return temp
}

// PerigeeApogee returns the perigee and apogee radii (km from Earth's
// centre) of the mean orbit at epoch. The osculating orbit differs from
// this by tens of km, and drag lowers it over time, so callers using these
// for filtering need a margin.
func (s *Satellite) PerigeeApogee() (perigee, apogee float64) {
	return (s.altp + 1) * radiusEarthKm, (s.alta + 1) * radiusEarthKm
}

// MinutesSinceEpoch converts an absolute time to SGP4's time argument.
func (s *Satellite) MinutesSinceEpoch(t time.Time) float64 {
	return t.Sub(s.Epoch).Minutes()
}

// PropagateTo returns the TEME position (km) and velocity (km/s) at time t.
func (s *Satellite) PropagateTo(t time.Time) (r, v [3]float64, err error) {
	return s.Propagate(s.MinutesSinceEpoch(t))
}

// Propagate returns the TEME position (km) and velocity (km/s) tsince
// minutes after the TLE epoch (negative values propagate backwards).
func (s *Satellite) Propagate(tsince float64) (r, v [3]float64, err error) {
	t := tsince

	// These are constants for near-earth orbits but are recomputed every
	// call for deep-space ones. Vallado overwrites the satrec fields;
	// locals keep the Satellite read-only.
	aycof, xlcof, con41, x1mth2, x7thm1 := s.aycof, s.xlcof, s.con41, s.x1mth2, s.x7thm1

	// ---- secular gravity and atmospheric drag ----
	xmdf := s.mo + s.mdot*t
	argpdf := s.argpo + s.argpdot*t
	nodedf := s.nodeo + s.nodedot*t
	argpm := argpdf
	mm := xmdf
	t2 := t * t
	nodem := nodedf + s.nodecf*t2
	tempa := 1.0 - s.cc1*t
	tempe := s.bstar * s.cc4 * t
	templ := s.t2cof * t2

	if !s.isimp {
		delomg := s.omgcof * t
		delmtemp := 1.0 + s.eta*math.Cos(xmdf)
		delm := s.xmcof * (delmtemp*delmtemp*delmtemp - s.delmo)
		temp := delomg + delm
		mm = xmdf + temp
		argpm = argpdf - temp
		t3 := t2 * t
		t4 := t3 * t
		tempa = tempa - s.d2*t2 - s.d3*t3 - s.d4*t4
		tempe = tempe + s.bstar*s.cc5*(math.Sin(mm)-s.sinmao)
		templ = templ + s.t3cof*t3 + t4*(s.t4cof+t*s.t5cof)
	}

	nm := s.noUnkozai
	em := s.ecco
	inclm := s.inclo
	if s.deep {
		em, argpm, inclm, mm, nodem, nm = s.dspace(t, em, argpm, inclm, mm, nodem, nm)
	}

	if nm <= 0.0 {
		return r, v, &Error{ErrMeanMotion, tsince, nm}
	}
	am := math.Pow(xke/nm, x2o3) * tempa * tempa
	nm = xke / math.Pow(am, 1.5)
	em -= tempe

	if em >= 1.0 || em < -0.001 {
		return r, v, &Error{ErrMeanEccentricity, tsince, em}
	}
	if em < 1.0e-6 {
		em = 1.0e-6 // avoid a divide by zero
	}
	mm += s.noUnkozai * templ
	xlm := mm + argpm + nodem

	nodem = math.Mod(nodem, twoPi)
	argpm = math.Mod(argpm, twoPi)
	xlm = math.Mod(xlm, twoPi)
	mm = math.Mod(xlm-argpm-nodem, twoPi)

	sinim := math.Sin(inclm)
	cosim := math.Cos(inclm)

	// ---- lunar-solar periodics ----
	ep := em
	xincp := inclm
	argpp := argpm
	nodep := nodem
	mp := mm
	sinip := sinim
	cosip := cosim
	if s.deep {
		ep, xincp, nodep, argpp, mp = s.dpper(t, ep, xincp, nodep, argpp, mp)
		if xincp < 0.0 {
			xincp = -xincp
			nodep += math.Pi
			argpp -= math.Pi
		}
		if ep < 0.0 || ep > 1.0 {
			return r, v, &Error{ErrPerturbedEccentricity, tsince, ep}
		}
	}

	// ---- long period periodics ----
	if s.deep {
		sinip = math.Sin(xincp)
		cosip = math.Cos(xincp)
		aycof = -0.5 * j3oj2 * sinip
		if math.Abs(cosip+1.0) > 1.5e-12 {
			xlcof = -0.25 * j3oj2 * sinip * (3.0 + 5.0*cosip) / (1.0 + cosip)
		} else {
			xlcof = -0.25 * j3oj2 * sinip * (3.0 + 5.0*cosip) / temp4
		}
	}
	axnl := ep * math.Cos(argpp)
	temp := 1.0 / (am * (1.0 - ep*ep))
	aynl := ep*math.Sin(argpp) + temp*aycof
	xl := mp + argpp + nodep + temp*xlcof*axnl

	// ---- solve Kepler's equation ----
	u := math.Mod(xl-nodep, twoPi)
	eo1 := u
	tem5 := 9999.9
	var sineo1, coseo1 float64
	for ktr := 1; math.Abs(tem5) >= 1.0e-12 && ktr <= 10; ktr++ {
		sineo1 = math.Sin(eo1)
		coseo1 = math.Cos(eo1)
		tem5 = 1.0 - coseo1*axnl - sineo1*aynl
		tem5 = (u - aynl*coseo1 + axnl*sineo1 - eo1) / tem5
		if math.Abs(tem5) >= 0.95 {
			tem5 = math.Copysign(0.95, tem5)
		}
		eo1 += tem5
	}

	// ---- short period preliminary quantities ----
	ecose := axnl*coseo1 + aynl*sineo1
	esine := axnl*sineo1 - aynl*coseo1
	el2 := axnl*axnl + aynl*aynl
	pl := am * (1.0 - el2)
	if pl < 0.0 {
		return r, v, &Error{ErrSemiLatusRectum, tsince, pl}
	}
	rl := am * (1.0 - ecose)
	rdotl := math.Sqrt(am) * esine / rl
	rvdotl := math.Sqrt(pl) / rl
	betal := math.Sqrt(1.0 - el2)
	temp = esine / (1.0 + betal)
	sinu := am / rl * (sineo1 - aynl - axnl*temp)
	cosu := am / rl * (coseo1 - axnl + aynl*temp)
	su := math.Atan2(sinu, cosu)
	sin2u := (cosu + cosu) * sinu
	cos2u := 1.0 - 2.0*sinu*sinu
	temp = 1.0 / pl
	temp1 := 0.5 * j2 * temp
	temp2 := temp1 * temp

	// ---- update for short period periodics ----
	if s.deep {
		cosisq := cosip * cosip
		con41 = 3.0*cosisq - 1.0
		x1mth2 = 1.0 - cosisq
		x7thm1 = 7.0*cosisq - 1.0
	}
	mrt := rl*(1.0-1.5*temp2*betal*con41) + 0.5*temp1*x1mth2*cos2u
	su -= 0.25 * temp2 * x7thm1 * sin2u
	xnode := nodep + 1.5*temp2*cosip*sin2u
	xinc := xincp + 1.5*temp2*cosip*sinip*cos2u
	mvt := rdotl - nm*temp1*x1mth2*sin2u/xke
	rvdot := rvdotl + nm*temp1*(x1mth2*cos2u+1.5*con41)/xke

	// ---- orientation vectors ----
	sinsu, cossu := math.Sincos(su)
	snod, cnod := math.Sincos(xnode)
	sini, cosi := math.Sincos(xinc)
	xmx := -snod * cosi
	xmy := cnod * cosi
	ux := xmx*sinsu + cnod*cossu
	uy := xmy*sinsu + snod*cossu
	uz := sini * sinsu
	vx := xmx*cossu - cnod*sinsu
	vy := xmy*cossu - snod*sinsu
	vz := sini * cossu

	// ---- position and velocity (km, km/s) ----
	mr := mrt * radiusEarthKm
	r = [3]float64{mr * ux, mr * uy, mr * uz}
	v = [3]float64{
		(mvt*ux + rvdot*vx) * vkmpersec,
		(mvt*uy + rvdot*vy) * vkmpersec,
		(mvt*uz + rvdot*vz) * vkmpersec,
	}

	if mrt < 1.0 {
		return r, v, &Error{ErrDecayed, tsince, mrt}
	}
	return r, v, nil
}
