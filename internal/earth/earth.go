// Package earth converts SGP4's TEME positions into Earth-fixed
// coordinates and latitude, longitude and altitude, for drawing objects on
// a map.
package earth

import (
	"math"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/sgp4"
)

// WGS-84 ellipsoid, the one GPS and maps use. (SGP4 itself uses WGS-72
// constants for its dynamics; for turning a position into a latitude and
// altitude, WGS-84 is the right reference.)
const (
	a  = 6378.137          // equatorial radius, km
	f  = 1 / 298.257223563 // flattening
	b  = a * (1 - f)       // polar radius, km
	e2 = f * (2 - f)       // first eccentricity squared
)

// TEMEToECEF rotates a TEME position (km) at time t into the Earth-fixed
// frame by Greenwich mean sidereal time. Polar motion (~10 m) is ignored,
// which is far below TLE accuracy.
func TEMEToECEF(r [3]float64, t time.Time) [3]float64 {
	s, c := math.Sincos(sgp4.GMST(t))
	return [3]float64{c*r[0] + s*r[1], -s*r[0] + c*r[1], r[2]}
}

// Geodetic is a point given as latitude and longitude (degrees, longitude
// in [-180, 180)) and height above the WGS-84 ellipsoid (km).
type Geodetic struct {
	Lat, Lon, Alt float64
}

// ECEFToGeodetic converts an Earth-fixed position (km) to geodetic
// coordinates by fixed-point iteration on latitude. Five iterations give
// sub-millimetre accuracy from the surface to GEO.
func ECEFToGeodetic(r [3]float64) Geodetic {
	x, y, z := r[0], r[1], r[2]
	p := math.Hypot(x, y)
	lon := math.Atan2(y, x)
	lat := math.Atan2(z, p*(1-e2)) // first guess
	var n float64
	for range 5 {
		sinLat := math.Sin(lat)
		n = a / math.Sqrt(1-e2*sinLat*sinLat) // prime vertical radius of curvature
		lat = math.Atan2(z+n*e2*sinLat, p)
	}
	cosLat := math.Cos(lat)
	var alt float64
	if math.Abs(cosLat) > 1e-9 {
		alt = p/cosLat - n
	} else { // at a pole
		alt = math.Abs(z) - b
	}
	return Geodetic{Lat: lat * 180 / math.Pi, Lon: lon * 180 / math.Pi, Alt: alt}
}

// GeodeticToECEF is the inverse of ECEFToGeodetic.
func GeodeticToECEF(g Geodetic) [3]float64 {
	lat, lon := g.Lat*math.Pi/180, g.Lon*math.Pi/180
	sinLat, cosLat := math.Sincos(lat)
	sinLon, cosLon := math.Sincos(lon)
	n := a / math.Sqrt(1-e2*sinLat*sinLat)
	return [3]float64{
		(n + g.Alt) * cosLat * cosLon,
		(n + g.Alt) * cosLat * sinLon,
		(n*(1-e2) + g.Alt) * sinLat,
	}
}

// SubPoint returns the point on the ground directly below a TEME position
// at time t, with the object's altitude.
func SubPoint(r [3]float64, t time.Time) Geodetic {
	return ECEFToGeodetic(TEMEToECEF(r, t))
}
