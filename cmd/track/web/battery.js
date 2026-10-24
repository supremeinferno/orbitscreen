'use strict';
// Battery status of the few satellites that satnogs-pipeline follows
// (github.com/supremeinferno/satnogs-pipeline). That project publishes
// status.json every hour; when one of its satellites is selected, the panel
// gets a battery section: the latest voltage, how long ago the satellite last
// sent a beacon, and any alert. Whether the satellite is in sunlight or in the
// Earth's shadow is worked out here, from the position and the Sun at the
// simulation time, so it follows the clock when the time is moved.

const BATTERY_STATUS_URL = 'https://supremeinferno.github.io/satnogs-pipeline/status.json';
const BATTERY_PAGE_URL = 'https://supremeinferno.github.io/satnogs-pipeline/';

let batteryStatus = null; // { generatedAt (ms), sats: Map<norad, satellite> }

// loadBatteryStatus fetches the snapshot. Any failure just leaves the panel
// without a battery section: the tracker does not depend on it.
async function loadBatteryStatus() {
  try {
    const res = await fetch(BATTERY_STATUS_URL, { cache: 'no-cache' });
    if (!res.ok) return false;
    const d = await res.json();
    batteryStatus = { generatedAt: Date.parse(d.generated_at), sats: new Map(d.satellites.map((s) => [s.norad, s])) };
    return true;
  } catch {
    return false;
  }
}

// inEarthShadow reports whether a point (geodetic latitude and longitude in
// degrees, altitude in km) is in the Earth's shadow, given the point on the
// ground below the Sun, { lat, lon } in degrees. The shadow is a cylinder of
// the Earth's radius along the Sun-Earth line, as in satnogs-pipeline. The
// Sun is far enough away that its rays count as parallel.
function inEarthShadow(latDeg, lonDeg, altKm, sun) {
  const rad = Math.PI / 180;
  const a = 6378.137, f = 1 / 298.257223563, e2 = f * (2 - f);
  const lat = latDeg * rad, lon = lonDeg * rad;
  const n = a / Math.sqrt(1 - e2 * Math.sin(lat) ** 2);
  const r = [
    (n + altKm) * Math.cos(lat) * Math.cos(lon),
    (n + altKm) * Math.cos(lat) * Math.sin(lon),
    (n * (1 - e2) + altKm) * Math.sin(lat),
  ];
  const s = [Math.cos(sun.lat * rad) * Math.cos(sun.lon * rad), Math.cos(sun.lat * rad) * Math.sin(sun.lon * rad), Math.sin(sun.lat * rad)];
  const along = r[0] * s[0] + r[1] * s[1] + r[2] * s[2];
  if (along >= 0) return false; // on the Sun's side of the Earth
  const px = r[0] - along * s[0], py = r[1] - along * s[1], pz = r[2] - along * s[2];
  return Math.hypot(px, py, pz) < a; // behind the Earth, and inside the shadow cylinder
}

function batteryEsc(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function batteryAgo(ms) {
  const m = Math.round(ms / 60000);
  if (m < 2) return 'just now';
  if (m < 120) return `${m} minutes ago`;
  const h = Math.round(m / 60);
  return h < 48 ? `${h} hours ago` : `${Math.round(h / 24)} days ago`;
}

const BATTERY_RULES = {
  discharging_in_sunlight: 'discharging in sunlight',
  voltage_below_baseline: 'voltage below its own usual',
  low_voltage: 'voltage below the safe limit',
};

// batterySection returns the HTML for the panel, or '' when the satellite is
// not one of the followed ones or the snapshot has not loaded. info is the
// panel's object (lat, lon, altKm); sunAt(ms) gives the point below the Sun;
// nowMs is the simulation time and realNowMs the real one.
function batterySection(norad, info, nowMs, sunAt, realNowMs = Date.now()) {
  const sat = batteryStatus && batteryStatus.sats.get(norad);
  if (!sat) return '';
  let light = '';
  if (info && Number.isFinite(info.lat) && Number.isFinite(info.lon) && Number.isFinite(info.altKm)) {
    light = inEarthShadow(info.lat, info.lon, info.altKm, sunAt(nowMs)) ? "in the Earth's shadow" : 'in sunlight';
  }
  const rows = [];
  if (light) rows.push(`<dt>Lighting</dt><dd>${light}</dd>`);
  let fired = false;
  for (const b of sat.batteries || []) {
    const last = b.readings && b.readings[b.readings.length - 1];
    if (!last) continue;
    const name = b.battery === 'main' ? 'Battery' : `Battery ${batteryEsc(b.battery.toUpperCase())}`;
    let value = `${(last.mv / 1000).toFixed(2)} V`;
    if (last.ma != null) value += `, ${last.ma > 0 ? '+' : ''}${last.ma} mA`;
    rows.push(`<dt>${name}</dt><dd>${value}</dd>`);
    for (const [rule, text] of Object.entries(BATTERY_RULES)) {
      if (b.anomalies && b.anomalies[rule]) { fired = true; rows.push(`<dt class="alert">⚠ ${name}</dt><dd class="alert">${text}</dd>`); }
    }
  }
  if (sat.last_beacon) rows.push(`<dt>Last beacon</dt><dd>${batteryAgo(realNowMs - Date.parse(sat.last_beacon))}</dd>`);
  if (!fired) rows.push(`<dt>Alerts</dt><dd>${sat.stale ? 'no recent beacons' : 'none firing'}</dd>`);
  const snap = batteryAgo(realNowMs - batteryStatus.generatedAt);
  return `<div class="batt"><h3>Battery</h3><dl>${rows.join('')}</dl>
    <p class="note">Beacons heard by volunteer ground stations; snapshot from ${snap}. <a href="${BATTERY_PAGE_URL}" target="_blank" rel="noopener">Charts and all five on the battery page</a></p></div>`;
}

// For the tests, which run under Node.
if (typeof module !== 'undefined') {
  module.exports = {
    inEarthShadow, batterySection, batteryAgo,
    setStatus(d) { batteryStatus = d && { generatedAt: Date.parse(d.generated_at), sats: new Map(d.satellites.map((s) => [s.norad, s])) }; },
  };
}
