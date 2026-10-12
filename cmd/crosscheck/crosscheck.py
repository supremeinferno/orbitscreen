"""Compare cmd/crosscheck's CSV with the python-sgp4 library (Vallado's
C++ SGP4, wrapped). Usage: python crosscheck.py TLEFILE GOCSV"""
import csv
import math
import sys

from sgp4.api import Satrec

tle_path, csv_path = sys.argv[1], sys.argv[2]

sats = {}
lines = [l.rstrip() for l in open(tle_path) if l.strip()]
for i, l in enumerate(lines):
    if l.startswith("1 ") and i + 1 < len(lines) and lines[i + 1].startswith("2 "):
        s = Satrec.twoline2rv(l, lines[i + 1])
        sats[s.satnum] = s

rows = errs_match = errs_mismatch = 0
max_dr = max_dv = 0.0
worst = None
for row in csv.DictReader(open(csv_path)):
    s = sats[int(row["catnum"])]
    e, r, v = s.sgp4_tsince(float(row["tsince"]))
    go_err = int(row["err"])
    if go_err or e:
        if go_err == e:
            errs_match += 1
        else:
            errs_mismatch += 1
            print(f"error mismatch: {row['catnum']} t={row['tsince']} go={go_err} python={e}")
        continue
    gr = [float(row[k]) for k in ("x", "y", "z")]
    gv = [float(row[k]) for k in ("vx", "vy", "vz")]
    dr = math.dist(gr, r)
    dv = math.dist(gv, v)
    rows += 1
    if dr > max_dr:
        max_dr, worst = dr, (row["catnum"], row["tsince"])
    max_dv = max(max_dv, dv)

print(f"{len(sats)} satellites, {rows} positions compared")
print(f"max |dr| = {max_dr:.3g} km (catalog {worst[0]} at t={worst[1]} min), max |dv| = {max_dv:.3g} km/s")
print(f"errors: {errs_match} agree, {errs_mismatch} disagree")
