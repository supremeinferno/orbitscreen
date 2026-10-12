package sgp4

import "math"

// Deep-space (SDP4) routines: lunar-solar perturbations and the resonance
// integrator for 12-hour (Molniya, GPS-ish) and 24-hour (GEO) orbits.
// Used for orbits with a period >= 225 minutes.

const (
	zes   = 0.01675                // solar eccentricity
	zel   = 0.05490                // lunar eccentricity
	zns   = 1.19459e-5             // solar mean motion (rad/min)
	znl   = 1.5835218e-4           // lunar mean motion (rad/min)
	rptim = 4.37526908801129966e-3 // earth rotation, rad/min (7.29211514668855e-5 rad/s)
)

// dscomResult holds dscom's outputs. Names are Vallado's.
type dscomResult struct {
	sinim, cosim, emsq                             float64
	e3, ee2, peo, pgho, pho, pinco, plo            float64
	se2, se3, sgh2, sgh3, sgh4, sh2, sh3, si2, si3 float64
	sl2, sl3, sl4                                  float64
	s1, s2, s3, s4, s5                             float64
	ss1, ss2, ss3, ss4, ss5                        float64
	sz1, sz3, sz11, sz13, sz21, sz23, sz31, sz33   float64
	xgh2, xgh3, xgh4, xh2, xh3, xi2, xi3           float64
	xl2, xl3, xl4                                  float64
	z1, z3, z11, z13, z21, z23, z31, z33           float64
	zmol, zmos                                     float64
}

// dscom computes the lunar and solar terms used by the deep-space routines.
func dscom(epoch, ep, argpp, tc, inclp, nodep, np float64) (o dscomResult) {
	const (
		c1ss   = 2.9864797e-6
		c1l    = 4.7968065e-7
		zsinis = 0.39785416
		zcosis = 0.91744867
		zcosgs = 0.1945905
		zsings = -0.98088458
	)

	nm := np
	em := ep
	snodm := math.Sin(nodep)
	cnodm := math.Cos(nodep)
	sinomm := math.Sin(argpp)
	cosomm := math.Cos(argpp)
	sinim := math.Sin(inclp)
	cosim := math.Cos(inclp)
	emsq := em * em
	betasq := 1.0 - emsq
	rtemsq := math.Sqrt(betasq)
	o.sinim, o.cosim, o.emsq = sinim, cosim, emsq

	// ---- initialize lunar solar terms ----
	o.peo, o.pinco, o.plo, o.pgho, o.pho = 0, 0, 0, 0, 0
	day := epoch + 18261.5 + tc/1440.0
	xnodce := math.Mod(4.5236020-9.2422029e-4*day, twoPi)
	stem := math.Sin(xnodce)
	ctem := math.Cos(xnodce)
	zcosil := 0.91375164 - 0.03568096*ctem
	zsinil := math.Sqrt(1.0 - zcosil*zcosil)
	zsinhl := 0.089683511 * stem / zsinil
	zcoshl := math.Sqrt(1.0 - zsinhl*zsinhl)
	gam := 5.8351514 + 0.0019443680*day
	zx := 0.39785416 * stem / zsinil
	zy := zcoshl*ctem + 0.91744867*zsinhl*stem
	zx = math.Atan2(zx, zy)
	zx = gam + zx - xnodce
	zcosgl := math.Cos(zx)
	zsingl := math.Sin(zx)

	// ---- do solar terms (pass 1), then lunar terms (pass 2) ----
	zcosg := zcosgs
	zsing := zsings
	zcosi := zcosis
	zsini := zsinis
	zcosh := cnodm
	zsinh := snodm
	cc := c1ss
	xnoi := 1.0 / nm

	var s1, s2, s3, s4, s5, s6, s7 float64
	var z1, z2, z3, z11, z12, z13, z21, z22, z23, z31, z32, z33 float64
	var ss1, ss2, ss3, ss4, ss5, ss6, ss7 float64
	var sz1, sz2, sz3, sz11, sz12, sz13, sz21, sz22, sz23, sz31, sz32, sz33 float64
	for lsflg := 1; lsflg <= 2; lsflg++ {
		a1 := zcosg*zcosh + zsing*zcosi*zsinh
		a3 := -zsing*zcosh + zcosg*zcosi*zsinh
		a7 := -zcosg*zsinh + zsing*zcosi*zcosh
		a8 := zsing * zsini
		a9 := zsing*zsinh + zcosg*zcosi*zcosh
		a10 := zcosg * zsini
		a2 := cosim*a7 + sinim*a8
		a4 := cosim*a9 + sinim*a10
		a5 := -sinim*a7 + cosim*a8
		a6 := -sinim*a9 + cosim*a10

		x1 := a1*cosomm + a2*sinomm
		x2 := a3*cosomm + a4*sinomm
		x3 := -a1*sinomm + a2*cosomm
		x4 := -a3*sinomm + a4*cosomm
		x5 := a5 * sinomm
		x6 := a6 * sinomm
		x7 := a5 * cosomm
		x8 := a6 * cosomm

		z31 = 12.0*x1*x1 - 3.0*x3*x3
		z32 = 24.0*x1*x2 - 6.0*x3*x4
		z33 = 12.0*x2*x2 - 3.0*x4*x4
		z1 = 3.0*(a1*a1+a2*a2) + z31*emsq
		z2 = 6.0*(a1*a3+a2*a4) + z32*emsq
		z3 = 3.0*(a3*a3+a4*a4) + z33*emsq
		z11 = -6.0*a1*a5 + emsq*(-24.0*x1*x7-6.0*x3*x5)
		z12 = -6.0*(a1*a6+a3*a5) + emsq*(-24.0*(x2*x7+x1*x8)-6.0*(x3*x6+x4*x5))
		z13 = -6.0*a3*a6 + emsq*(-24.0*x2*x8-6.0*x4*x6)
		z21 = 6.0*a2*a5 + emsq*(24.0*x1*x5-6.0*x3*x7)
		z22 = 6.0*(a4*a5+a2*a6) + emsq*(24.0*(x2*x5+x1*x6)-6.0*(x4*x7+x3*x8))
		z23 = 6.0*a4*a6 + emsq*(24.0*x2*x6-6.0*x4*x8)
		z1 = z1 + z1 + betasq*z31
		z2 = z2 + z2 + betasq*z32
		z3 = z3 + z3 + betasq*z33
		s3 = cc * xnoi
		s2 = -0.5 * s3 / rtemsq
		s4 = s3 * rtemsq
		s1 = -15.0 * em * s4
		s5 = x1*x3 + x2*x4
		s6 = x2*x3 + x1*x4
		s7 = x2*x4 - x1*x3

		if lsflg == 1 {
			ss1, ss2, ss3, ss4, ss5, ss6, ss7 = s1, s2, s3, s4, s5, s6, s7
			sz1, sz2, sz3 = z1, z2, z3
			sz11, sz12, sz13 = z11, z12, z13
			sz21, sz22, sz23 = z21, z22, z23
			sz31, sz32, sz33 = z31, z32, z33
			zcosg = zcosgl
			zsing = zsingl
			zcosi = zcosil
			zsini = zsinil
			zcosh = zcoshl*cnodm + zsinhl*snodm
			zsinh = snodm*zcoshl - cnodm*zsinhl
			cc = c1l
		}
	}

	o.zmol = math.Mod(4.7199672+0.22997150*day-gam, twoPi)
	o.zmos = math.Mod(6.2565837+0.017201977*day, twoPi)

	// ---- solar terms ----
	o.se2 = 2.0 * ss1 * ss6
	o.se3 = 2.0 * ss1 * ss7
	o.si2 = 2.0 * ss2 * sz12
	o.si3 = 2.0 * ss2 * (sz13 - sz11)
	o.sl2 = -2.0 * ss3 * sz2
	o.sl3 = -2.0 * ss3 * (sz3 - sz1)
	o.sl4 = -2.0 * ss3 * (-21.0 - 9.0*emsq) * zes
	o.sgh2 = 2.0 * ss4 * sz32
	o.sgh3 = 2.0 * ss4 * (sz33 - sz31)
	o.sgh4 = -18.0 * ss4 * zes
	o.sh2 = -2.0 * ss2 * sz22
	o.sh3 = -2.0 * ss2 * (sz23 - sz21)

	// ---- lunar terms ----
	o.ee2 = 2.0 * s1 * s6
	o.e3 = 2.0 * s1 * s7
	o.xi2 = 2.0 * s2 * z12
	o.xi3 = 2.0 * s2 * (z13 - z11)
	o.xl2 = -2.0 * s3 * z2
	o.xl3 = -2.0 * s3 * (z3 - z1)
	o.xl4 = -2.0 * s3 * (-21.0 - 9.0*emsq) * zel
	o.xgh2 = 2.0 * s4 * z32
	o.xgh3 = 2.0 * s4 * (z33 - z31)
	o.xgh4 = -18.0 * s4 * zel
	o.xh2 = -2.0 * s2 * z22
	o.xh3 = -2.0 * s2 * (z23 - z21)

	o.s1, o.s2, o.s3, o.s4, o.s5 = s1, s2, s3, s4, s5
	o.ss1, o.ss2, o.ss3, o.ss4, o.ss5 = ss1, ss2, ss3, ss4, ss5
	o.sz1, o.sz3, o.sz11, o.sz13, o.sz21, o.sz23, o.sz31, o.sz33 = sz1, sz3, sz11, sz13, sz21, sz23, sz31, sz33
	o.z1, o.z3, o.z11, o.z13, o.z21, o.z23, o.z31, o.z33 = z1, z3, z11, z13, z21, z23, z31, z33
	return o
}

// dpper applies the lunar-solar periodics at time t (Vallado's dpper with
// init='n' and opsmode='i').
func (s *Satellite) dpper(t, ep, inclp, nodep, argpp, mp float64) (float64, float64, float64, float64, float64) {
	// ---- time varying periodics ----
	zm := s.zmos + zns*t
	zf := zm + 2.0*zes*math.Sin(zm)
	sinzf := math.Sin(zf)
	f2 := 0.5*sinzf*sinzf - 0.25
	f3 := -0.5 * sinzf * math.Cos(zf)
	ses := s.se2*f2 + s.se3*f3
	sis := s.si2*f2 + s.si3*f3
	sls := s.sl2*f2 + s.sl3*f3 + s.sl4*sinzf
	sghs := s.sgh2*f2 + s.sgh3*f3 + s.sgh4*sinzf
	shs := s.sh2*f2 + s.sh3*f3

	zm = s.zmol + znl*t
	zf = zm + 2.0*zel*math.Sin(zm)
	sinzf = math.Sin(zf)
	f2 = 0.5*sinzf*sinzf - 0.25
	f3 = -0.5 * sinzf * math.Cos(zf)
	sel := s.ee2*f2 + s.e3*f3
	sil := s.xi2*f2 + s.xi3*f3
	sll := s.xl2*f2 + s.xl3*f3 + s.xl4*sinzf
	sghl := s.xgh2*f2 + s.xgh3*f3 + s.xgh4*sinzf
	shll := s.xh2*f2 + s.xh3*f3

	pe := ses + sel - s.peo
	pinc := sis + sil - s.pinco
	pl := sls + sll - s.plo
	pgh := sghs + sghl - s.pgho
	ph := shs + shll - s.pho

	inclp += pinc
	ep += pe
	sinip := math.Sin(inclp)
	cosip := math.Cos(inclp)

	// Lyddane choice: above 0.2 rad (11.46 deg) apply the periodics
	// directly; below it, use Lyddane's modification to avoid the
	// singularity at zero inclination. GSFC/Vallado test the perturbed
	// inclination here.
	if inclp >= 0.2 {
		ph /= sinip
		pgh -= cosip * ph
		argpp += pgh
		nodep += ph
		mp += pl
	} else {
		sinop := math.Sin(nodep)
		cosop := math.Cos(nodep)
		alfdp := sinip * sinop
		betdp := sinip * cosop
		dalf := ph*cosop + pinc*cosip*sinop
		dbet := -ph*sinop + pinc*cosip*cosop
		alfdp += dalf
		betdp += dbet
		nodep = math.Mod(nodep, twoPi)
		xls := mp + argpp + cosip*nodep
		dls := pl + pgh - pinc*nodep*sinip
		xls += dls
		xnoh := nodep
		nodep = math.Atan2(alfdp, betdp)
		if math.Abs(xnoh-nodep) > math.Pi {
			if nodep < xnoh {
				nodep += twoPi
			} else {
				nodep -= twoPi
			}
		}
		mp += pl
		argpp = xls - mp - cosip*nodep
	}
	return ep, inclp, nodep, argpp, mp
}

// dsinit sets up the deep-space secular rates and, for resonant orbits,
// the resonance integrator's coefficients. It's only called at epoch
// (t = 0), where Vallado's secular updates to em, inclm, etc. are no-ops,
// so only the persistent Satellite fields are computed here.
func (s *Satellite) dsinit(ds *dscomResult, xpidot float64) {
	const (
		q22    = 1.7891679e-6
		q31    = 2.1460748e-6
		q33    = 2.2123015e-7
		root22 = 1.7891679e-6
		root44 = 7.3636953e-9
		root54 = 2.1765803e-9
		root32 = 3.7393792e-7
		root52 = 1.1428639e-7
	)
	cosim, sinim, emsq := ds.cosim, ds.sinim, ds.emsq
	nm := s.noUnkozai
	em := s.ecco
	inclm := s.inclo

	// ---- deep space resonance type ----
	s.irez = 0
	if 0.0034906585 < nm && nm < 0.0052359877 {
		s.irez = 1 // ~24 h: geosynchronous
	}
	if 8.26e-3 <= nm && nm <= 9.24e-3 && em >= 0.5 {
		s.irez = 2 // ~12 h, eccentric: Molniya-like
	}

	// ---- solar terms ----
	ses := ds.ss1 * zns * ds.ss5
	sis := ds.ss2 * zns * (ds.sz11 + ds.sz13)
	sls := -zns * ds.ss3 * (ds.sz1 + ds.sz3 - 14.0 - 6.0*emsq)
	sghs := ds.ss4 * zns * (ds.sz31 + ds.sz33 - 6.0)
	shs := -zns * ds.ss2 * (ds.sz21 + ds.sz23)
	if inclm < 5.2359877e-2 || inclm > math.Pi-5.2359877e-2 {
		shs = 0.0 // sgp4fix for 180 deg incl
	}
	if sinim != 0.0 {
		shs /= sinim
	}
	sgs := sghs - cosim*shs

	// ---- lunar terms ----
	s.dedt = ses + ds.s1*znl*ds.s5
	s.didt = sis + ds.s2*znl*(ds.z11+ds.z13)
	s.dmdt = sls - znl*ds.s3*(ds.z1+ds.z3-14.0-6.0*emsq)
	sghl := ds.s4 * znl * (ds.z31 + ds.z33 - 6.0)
	shll := -znl * ds.s2 * (ds.z21 + ds.z23)
	if inclm < 5.2359877e-2 || inclm > math.Pi-5.2359877e-2 {
		shll = 0.0
	}
	s.domdt = sgs + sghl
	s.dnodt = shs
	if sinim != 0.0 {
		s.domdt -= cosim / sinim * shll
		s.dnodt += shll / sinim
	}

	if s.irez == 0 {
		return
	}

	// ---- initialize the resonance terms ----
	theta := math.Mod(s.gsto, twoPi) // gsto + tc*rptim with tc = 0
	aonv := math.Pow(nm/xke, x2o3)

	// Geopotential resonance for 12-hour orbits.
	if s.irez == 2 {
		cosisq := cosim * cosim
		eoc := em * emsq
		g201 := -0.306 - (em-0.64)*0.440

		var g211, g310, g322, g410, g422, g520, g521, g532, g533 float64
		if em <= 0.65 {
			g211 = 3.616 - 13.2470*em + 16.2900*emsq
			g310 = -19.302 + 117.3900*em - 228.4190*emsq + 156.5910*eoc
			g322 = -18.9068 + 109.7927*em - 214.6334*emsq + 146.5816*eoc
			g410 = -41.122 + 242.6940*em - 471.0940*emsq + 313.9530*eoc
			g422 = -146.407 + 841.8800*em - 1629.014*emsq + 1083.4350*eoc
			g520 = -532.114 + 3017.977*em - 5740.032*emsq + 3708.2760*eoc
		} else {
			g211 = -72.099 + 331.819*em - 508.738*emsq + 266.724*eoc
			g310 = -346.844 + 1582.851*em - 2415.925*emsq + 1246.113*eoc
			g322 = -342.585 + 1554.908*em - 2366.899*emsq + 1215.972*eoc
			g410 = -1052.797 + 4758.686*em - 7193.992*emsq + 3651.957*eoc
			g422 = -3581.690 + 16178.110*em - 24462.770*emsq + 12422.520*eoc
			if em > 0.715 {
				g520 = -5149.66 + 29936.92*em - 54087.36*emsq + 31324.56*eoc
			} else {
				g520 = 1464.74 - 4664.75*em + 3763.64*emsq
			}
		}
		if em < 0.7 {
			g533 = -919.22770 + 4988.6100*em - 9064.7700*emsq + 5542.21*eoc
			g521 = -822.71072 + 4568.6173*em - 8491.4146*emsq + 5337.524*eoc
			g532 = -853.66600 + 4690.2500*em - 8624.7700*emsq + 5341.4*eoc
		} else {
			g533 = -37995.780 + 161616.52*em - 229838.20*emsq + 109377.94*eoc
			g521 = -51752.104 + 218913.95*em - 309468.16*emsq + 146349.42*eoc
			g532 = -40023.880 + 170470.89*em - 242699.48*emsq + 115605.82*eoc
		}

		sini2 := sinim * sinim
		f220 := 0.75 * (1.0 + 2.0*cosim + cosisq)
		f221 := 1.5 * sini2
		f321 := 1.875 * sinim * (1.0 - 2.0*cosim - 3.0*cosisq)
		f322 := -1.875 * sinim * (1.0 + 2.0*cosim - 3.0*cosisq)
		f441 := 35.0 * sini2 * f220
		f442 := 39.3750 * sini2 * sini2
		f522 := 9.84375 * sinim * (sini2*(1.0-2.0*cosim-5.0*cosisq) +
			0.33333333*(-2.0+4.0*cosim+6.0*cosisq))
		f523 := sinim * (4.92187512*sini2*(-2.0-4.0*cosim+10.0*cosisq) +
			6.56250012*(1.0+2.0*cosim-3.0*cosisq))
		f542 := 29.53125 * sinim * (2.0 - 8.0*cosim + cosisq*(-12.0+8.0*cosim+10.0*cosisq))
		f543 := 29.53125 * sinim * (-2.0 - 8.0*cosim + cosisq*(12.0+8.0*cosim-10.0*cosisq))
		xno2 := nm * nm
		ainv2 := aonv * aonv
		temp1 := 3.0 * xno2 * ainv2
		temp := temp1 * root22
		s.d2201 = temp * f220 * g201
		s.d2211 = temp * f221 * g211
		temp1 *= aonv
		temp = temp1 * root32
		s.d3210 = temp * f321 * g310
		s.d3222 = temp * f322 * g322
		temp1 *= aonv
		temp = 2.0 * temp1 * root44
		s.d4410 = temp * f441 * g410
		s.d4422 = temp * f442 * g422
		temp1 *= aonv
		temp = temp1 * root52
		s.d5220 = temp * f522 * g520
		s.d5232 = temp * f523 * g532
		temp = 2.0 * temp1 * root54
		s.d5421 = temp * f542 * g521
		s.d5433 = temp * f543 * g533
		s.xlamo = math.Mod(s.mo+s.nodeo+s.nodeo-theta-theta, twoPi)
		s.xfact = s.mdot + s.dmdt + 2.0*(s.nodedot+s.dnodt-rptim) - s.noUnkozai
	}

	// Synchronous resonance terms (24-hour orbits).
	if s.irez == 1 {
		g200 := 1.0 + emsq*(-2.5+0.8125*emsq)
		g310 := 1.0 + 2.0*emsq
		g300 := 1.0 + emsq*(-6.0+6.60937*emsq)
		f220 := 0.75 * (1.0 + cosim) * (1.0 + cosim)
		f311 := 0.9375*sinim*sinim*(1.0+3.0*cosim) - 0.75*(1.0+cosim)
		f330 := 1.0 + cosim
		f330 = 1.875 * f330 * f330 * f330
		s.del1 = 3.0 * nm * nm * aonv * aonv
		s.del2 = 2.0 * s.del1 * f220 * g200 * q22
		s.del3 = 3.0 * s.del1 * f330 * g300 * q33 * aonv
		s.del1 = s.del1 * f311 * g310 * q31 * aonv
		s.xlamo = math.Mod(s.mo+s.nodeo+s.argpo-theta, twoPi)
		s.xfact = s.mdot + xpidot - rptim + s.dmdt + s.domdt + s.dnodt - s.noUnkozai
	}
}

// dspace applies the deep-space secular effects and integrates the
// resonance terms out to time t.
func (s *Satellite) dspace(t, em, argpm, inclm, mm, nodem, nm float64) (float64, float64, float64, float64, float64, float64) {
	const (
		fasx2 = 0.13130908
		fasx4 = 2.8843198
		fasx6 = 0.37448087
		g22   = 5.7686396
		g32   = 0.95240898
		g44   = 1.8014998
		g52   = 1.0508330
		g54   = 4.4108898
		stepp = 720.0
		stepn = -720.0
		step2 = 259200.0
	)

	theta := math.Mod(s.gsto+t*rptim, twoPi)
	em += s.dedt * t
	inclm += s.didt * t
	argpm += s.domdt * t
	nodem += s.dnodt * t
	mm += s.dmdt * t

	if s.irez == 0 {
		return em, argpm, inclm, mm, nodem, nm
	}

	// Euler-Maclaurin integration in fixed 720-minute steps from epoch.
	// Vallado resumes from the previous call's state when it can; restarting
	// every time takes the exact same steps, so the answer is identical.
	atime := 0.0
	xni := s.noUnkozai
	xli := s.xlamo
	delt := stepp
	if t <= 0.0 {
		delt = stepn
	}
	var xndt, xldot, xnddt, ft float64
	for {
		if s.irez != 2 {
			// Near-synchronous resonance terms.
			xndt = s.del1*math.Sin(xli-fasx2) + s.del2*math.Sin(2.0*(xli-fasx4)) +
				s.del3*math.Sin(3.0*(xli-fasx6))
			xldot = xni + s.xfact
			xnddt = s.del1*math.Cos(xli-fasx2) +
				2.0*s.del2*math.Cos(2.0*(xli-fasx4)) +
				3.0*s.del3*math.Cos(3.0*(xli-fasx6))
			xnddt *= xldot
		} else {
			// Near half-day resonance terms.
			xomi := s.argpo + s.argpdot*atime
			x2omi := xomi + xomi
			x2li := xli + xli
			xndt = s.d2201*math.Sin(x2omi+xli-g22) + s.d2211*math.Sin(xli-g22) +
				s.d3210*math.Sin(xomi+xli-g32) + s.d3222*math.Sin(-xomi+xli-g32) +
				s.d4410*math.Sin(x2omi+x2li-g44) + s.d4422*math.Sin(x2li-g44) +
				s.d5220*math.Sin(xomi+xli-g52) + s.d5232*math.Sin(-xomi+xli-g52) +
				s.d5421*math.Sin(xomi+x2li-g54) + s.d5433*math.Sin(-xomi+x2li-g54)
			xldot = xni + s.xfact
			xnddt = s.d2201*math.Cos(x2omi+xli-g22) + s.d2211*math.Cos(xli-g22) +
				s.d3210*math.Cos(xomi+xli-g32) + s.d3222*math.Cos(-xomi+xli-g32) +
				s.d5220*math.Cos(xomi+xli-g52) + s.d5232*math.Cos(-xomi+xli-g52) +
				2.0*(s.d4410*math.Cos(x2omi+x2li-g44)+
					s.d4422*math.Cos(x2li-g44)+s.d5421*math.Cos(xomi+x2li-g54)+
					s.d5433*math.Cos(-xomi+x2li-g54))
			xnddt *= xldot
		}

		if math.Abs(t-atime) < stepp {
			ft = t - atime
			break
		}
		xli += xldot*delt + xndt*step2
		xni += xndt*delt + xnddt*step2
		atime += delt
	}

	nm = xni + xndt*ft + xnddt*ft*ft*0.5
	xl := xli + xldot*ft + xndt*ft*ft*0.5
	if s.irez != 1 {
		mm = xl - 2.0*nodem + 2.0*theta
	} else {
		mm = xl - nodem - argpm + theta
	}
	return em, argpm, inclm, mm, nodem, nm
}
