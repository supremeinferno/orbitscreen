// orbitscreen live tracker: draws every catalogued object on a world map.
// The Go server (cmd/track) does all the orbit maths; this file only
// draws. See docs/06-live-tracker.md.
'use strict';

const KINDS = {
  active:   { label: 'Active satellites',   color: '#4cc9f0' },
  inactive: { label: 'Inactive satellites', color: '#8d99ae' },
  rocket:   { label: 'Rocket bodies',       color: '#f9c74f' },
  debris:   { label: 'Debris',              color: '#ef476f' },
};
const ORBITS = ['LEO', 'MEO', 'GEO', 'HEO'];

const canvas = document.getElementById('map');
const ctx = canvas.getContext('2d');
const $ = (id) => document.getElementById(id);

let catalog = [];        // [{id, name, kind, orbit, attached}], same order as positions
let byKind = {};         // kind -> [index]
let pos = null;          // Float32Array of lat, lon, alt triples
let land = null;         // GeoJSON land polygons
let base = null;         // offscreen canvas: ocean, graticule, land
let selected = null;     // catalog index
let info = null;         // /api/object response for the selected object
let dirty = true;
let lastFetchMs = 0;

const showKind = new Set(Object.keys(KINDS));
const showOrbit = new Set(ORBITS);

// ---- simulated clock: real time × speed, re-anchored on every change ----
let speed = 1, anchorReal = Date.now(), anchorSim = Date.now();
const simNow = () => anchorSim + (Date.now() - anchorReal) * speed;
function setClock(sim, newSpeed) {
  anchorSim = sim; anchorReal = Date.now(); speed = newSpeed;
}

// ---- map geometry: equirectangular, as large as fits, centred ----
let W = 0, H = 0, mapX = 0, mapY = 0, mapW = 0, mapH = 0;
const project = (lat, lon) => [mapX + (lon + 180) / 360 * mapW, mapY + (90 - lat) / 180 * mapH];

function resize() {
  const dpr = window.devicePixelRatio || 1;
  W = window.innerWidth; H = window.innerHeight;
  canvas.width = Math.round(W * dpr); canvas.height = Math.round(H * dpr);
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  mapW = Math.min(W, H * 2); mapH = mapW / 2;
  mapX = (W - mapW) / 2; mapY = (H - mapH) / 2;
  base = null; dirty = true;
}

function drawBase() {
  const dpr = window.devicePixelRatio || 1;
  base = document.createElement('canvas');
  base.width = canvas.width; base.height = canvas.height;
  const b = base.getContext('2d');
  b.setTransform(dpr, 0, 0, dpr, 0, 0);
  b.fillStyle = '#050b14'; b.fillRect(0, 0, W, H);
  b.fillStyle = '#0a1a2b'; b.fillRect(mapX, mapY, mapW, mapH);
  // graticule every 30°
  b.strokeStyle = 'rgba(255,255,255,0.06)'; b.lineWidth = 1; b.beginPath();
  for (let lon = -180; lon <= 180; lon += 30) { const [x] = project(0, lon); b.moveTo(x, mapY); b.lineTo(x, mapY + mapH); }
  for (let lat = -90; lat <= 90; lat += 30) { const [, y] = project(lat, 0); b.moveTo(mapX, y); b.lineTo(mapX + mapW, y); }
  b.stroke();
  if (land) {
    // Some coastlines (eastern Russia, Fiji, Antarctica) cross the ±180°
    // edge. Drawn naively, a ring would jump straight across the map at the
    // crossing. Instead, "unwrap" each ring so longitudes change smoothly
    // (going past 180° rather than jumping to −180°), draw it, and draw it
    // again shifted by ±360° so the part hanging off one edge shows on the
    // other. Clipping to the map hides whatever lands outside.
    b.save();
    b.beginPath(); b.rect(mapX, mapY, mapW, mapH); b.clip();
    b.fillStyle = '#1f3d54'; b.strokeStyle = '#2f5a76'; b.lineWidth = 0.6; b.beginPath();
    for (const f of land.features) {
      const polys = f.geometry.type === 'Polygon' ? [f.geometry.coordinates] : f.geometry.coordinates;
      for (const poly of polys) for (const ring of poly) {
        let prev = null;
        const unwrapped = ring.map(([lon, lat]) => {
          if (prev !== null) lon += 360 * Math.round((prev - lon) / 360);
          prev = lon;
          return [lon, lat];
        });
        for (const shift of [-360, 0, 360]) {
          unwrapped.forEach(([lon, lat], k) => { const [x, y] = project(lat, lon + shift); k ? b.lineTo(x, y) : b.moveTo(x, y); });
          b.closePath();
        }
      }
    }
    b.fill(); b.stroke();
    b.restore();
  }
}

// ---- the Sun: low-precision solar position (accurate to ~0.01°) ----
function subsolar(ms) {
  const n = ms / 86400000 + 2440587.5 - 2451545.0; // days since J2000
  const rad = Math.PI / 180;
  const L = 280.460 + 0.9856474 * n, g = (357.528 + 0.9856003 * n) * rad;
  const lambda = (L + 1.915 * Math.sin(g) + 0.020 * Math.sin(2 * g)) * rad;
  const eps = (23.439 - 0.0000004 * n) * rad;
  const ra = Math.atan2(Math.cos(eps) * Math.sin(lambda), Math.cos(lambda)) / rad;
  const dec = Math.asin(Math.sin(eps) * Math.sin(lambda)) / rad;
  const gmst = 280.46061837 + 360.98564736629 * n;
  const lon = ((ra - gmst) % 360 + 540) % 360 - 180;
  return { lat: dec, lon };
}

// Shade the night side: everything beyond the terminator, the line where
// the Sun is on the horizon. For each longitude, the terminator latitude is
// atan(−cos(Δlon) / tan(declination)).
function drawNight(ms) {
  const sun = subsolar(ms), rad = Math.PI / 180;
  const tanDec = Math.tan((Math.abs(sun.lat) < 0.01 ? 0.01 : sun.lat) * rad);
  const pole = sun.lat > 0 ? -90 : 90; // night covers the pole away from the Sun
  ctx.beginPath();
  ctx.moveTo(...project(pole, -180));
  for (let lon = -180; lon <= 180; lon += 2) {
    const lat = Math.atan(-Math.cos((lon - sun.lon) * rad) / tanDec) / rad;
    ctx.lineTo(...project(lat, lon));
  }
  ctx.lineTo(...project(pole, 180));
  ctx.closePath();
  ctx.fillStyle = 'rgba(0, 0, 6, 0.55)';
  ctx.fill();
  const [sx, sy] = project(sun.lat, sun.lon);
  ctx.fillStyle = 'rgba(255, 214, 102, 0.9)';
  ctx.beginPath(); ctx.arc(sx, sy, 5, 0, 2 * Math.PI); ctx.fill();
}

// Draw a list of [lat, lon] as a line, breaking it where it wraps across
// the ±180° edge of the map.
function polyline(points) {
  ctx.beginPath();
  let prevLon = null;
  for (const [lat, lon] of points) {
    const [x, y] = project(lat, lon);
    if (prevLon === null || Math.abs(lon - prevLon) > 180) ctx.moveTo(x, y); else ctx.lineTo(x, y);
    prevLon = lon;
  }
  ctx.stroke();
}

// The footprint: a small circle of angular radius r around the subpoint.
function footprint(lat0, lon0, r) {
  const rad = Math.PI / 180, pts = [];
  const p = lat0 * rad, d = r * rad;
  for (let b = 0; b <= 360; b += 4) {
    const br = b * rad;
    const lat = Math.asin(Math.sin(p) * Math.cos(d) + Math.cos(p) * Math.sin(d) * Math.cos(br));
    const lon = lon0 * rad + Math.atan2(Math.sin(br) * Math.sin(d) * Math.cos(p), Math.cos(d) - Math.sin(p) * Math.sin(lat));
    pts.push([lat / rad, ((lon / rad) % 360 + 540) % 360 - 180]);
  }
  return pts;
}

const visible = (i) => showKind.has(catalog[i].kind) && showOrbit.has(catalog[i].orbit);

function draw() {
  if (!base) drawBase();
  const dpr = window.devicePixelRatio || 1;
  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.drawImage(base, 0, 0);
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.save();
  ctx.beginPath(); ctx.rect(mapX, mapY, mapW, mapH); ctx.clip();
  drawNight(simNow());

  if (pos) {
    for (const kind of Object.keys(KINDS)) {
      if (!showKind.has(kind)) continue;
      ctx.fillStyle = KINDS[kind].color;
      for (const i of byKind[kind]) {
        if (!showOrbit.has(catalog[i].orbit)) continue;
        const lat = pos[3 * i], lon = pos[3 * i + 1];
        if (Number.isNaN(lat)) continue;
        const [x, y] = project(lat, lon);
        ctx.fillRect(x - 0.9, y - 0.9, 1.8, 1.8);
      }
    }
  }

  if (selected !== null && info && info.track) {
    ctx.lineWidth = 1.5;
    ctx.strokeStyle = 'rgba(255,255,255,0.35)';
    polyline(info.track.slice(0, info.trackNow + 1)); // past
    ctx.strokeStyle = 'rgba(255,255,255,0.9)';
    polyline(info.track.slice(info.trackNow));         // future
    if (pos && !Number.isNaN(pos[3 * selected])) {
      const lat = pos[3 * selected], lon = pos[3 * selected + 1];
      ctx.strokeStyle = 'rgba(76,201,240,0.7)'; ctx.lineWidth = 1;
      if (info.footprintDeg) polyline(footprint(lat, lon, info.footprintDeg));
      const [x, y] = project(lat, lon);
      ctx.fillStyle = '#fff';
      ctx.beginPath(); ctx.arc(x, y, 4, 0, 2 * Math.PI); ctx.fill();
      ctx.strokeStyle = '#fff'; ctx.lineWidth = 1.5;
      ctx.beginPath(); ctx.arc(x, y, 9, 0, 2 * Math.PI); ctx.stroke();
      ctx.font = '600 12px system-ui, sans-serif';
      ctx.fillText(catalog[selected].name, x + 13, y + 4);
    }
  }
  ctx.restore();
}

function frame() {
  $('simtime').textContent = new Date(simNow()).toISOString().replace('T', ' ').slice(0, 19) + ' UTC';
  if (dirty) { draw(); dirty = false; }
  requestAnimationFrame(frame);
}

// ---- data ----
async function fetchPositions() {
  try {
    const t0 = performance.now();
    const res = await fetch(`/api/positions?t=${Math.round(simNow())}`);
    // The server sends little-endian float32s; every mainstream browser
    // runs on little-endian hardware, so the bytes can be used directly.
    pos = new Float32Array(await res.arrayBuffer());
    lastFetchMs = performance.now() - t0;
    dirty = true;
    updateMeta();
  } catch (e) {
    console.error(e);
  }
  setTimeout(fetchPositions, speed > 1 ? 250 : 1000);
}

async function fetchInfo() {
  if (selected === null) return;
  const id = catalog[selected].id;
  const res = await fetch(`/api/object?id=${id}&t=${Math.round(simNow())}`);
  if (!res.ok || selected === null || catalog[selected].id !== id) return;
  info = await res.json();
  renderPanel();
  dirty = true;
}
setInterval(fetchInfo, 3000);

function select(i) {
  selected = i; info = null;
  // Keep the selection in the URL (#25544) so a view can be linked to.
  history.replaceState(null, '', i === null ? location.pathname : '#' + catalog[i].id);
  if (i === null) { $('panel').hidden = true; dirty = true; return; }
  $('panel').hidden = false;
  $('panel').innerHTML = `<p class="sub">Loading ${esc(catalog[i].name)}…</p>`;
  fetchInfo();
}

const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const fmt = (x, d = 0) => Number(x).toLocaleString('en-US', { minimumFractionDigits: d, maximumFractionDigits: d });

function renderPanel() {
  if (!info) return;
  const c = catalog[selected];
  const ageH = (simNow() - Date.parse(info.epoch)) / 3600000;
  const ns = (lat) => `${fmt(Math.abs(lat), 2)}° ${lat >= 0 ? 'N' : 'S'}`;
  const ew = (lon) => `${fmt(Math.abs(lon), 2)}° ${lon >= 0 ? 'E' : 'W'}`;
  $('panel').innerHTML = `
    <button class="close" aria-label="Close">×</button>
    <h2>${esc(c.name)}</h2>
    <p class="sub">NORAD ${c.id}${info.intlDes ? ' · ' + esc(info.intlDes) : ''} · ${KINDS[c.kind].label.replace(/s$/, '').toLowerCase()} · ${c.orbit}</p>
    ${info.error ? `<p class="note">${esc(info.error)}</p>` : `
    <dl>
      <dt>Position</dt><dd>${ns(info.lat)}, ${ew(info.lon)}</dd>
      <dt>Altitude</dt><dd>${fmt(info.altKm)} km</dd>
      <dt>Speed</dt><dd>${fmt(info.speedKmS, 2)} km/s</dd>
      <dt>Period</dt><dd>${fmt(info.periodMin, 1)} min</dd>
      <dt>Perigee / apogee</dt><dd>${fmt(info.perigeeKm)} / ${fmt(info.apogeeKm)} km</dd>
      <dt>Inclination</dt><dd>${fmt(info.inclination, 2)}°</dd>
      <dt>Elements age</dt><dd>${ageH >= 0 ? fmt(ageH, 1) + ' h' : 'in the future'}</dd>
    </dl>`}
    ${c.attached ? '<p class="note">Docked to or part of another catalogued object (same orbit data).</p>' : ''}
    <p class="note">Ground track: faint = last third of an orbit, bright = next orbit. Circle = area that can see it above the horizon.</p>`;
  $('panel').querySelector('.close').onclick = () => select(null);
}

function updateMeta() {
  let shown = 0;
  const counts = {};
  for (let i = 0; i < catalog.length; i++) {
    if (!pos || Number.isNaN(pos[3 * i]) || !showOrbit.has(catalog[i].orbit)) continue;
    counts[catalog[i].kind] = (counts[catalog[i].kind] || 0) + 1;
    if (showKind.has(catalog[i].kind)) shown++;
  }
  for (const k of Object.keys(KINDS)) $('count-' + k).textContent = fmt(counts[k] || 0);
  $('meta').textContent = `${fmt(shown)} of ${fmt(catalog.length)} objects shown · positions computed and sent in ${fmt(lastFetchMs)} ms`;
}

// ---- interaction ----
function nearest(px, py, maxDist) {
  if (!pos) return null;
  let best = null, bestD = maxDist * maxDist;
  for (let i = 0; i < catalog.length; i++) {
    if (!visible(i) || Number.isNaN(pos[3 * i])) continue;
    const [x, y] = project(pos[3 * i], pos[3 * i + 1]);
    const d = (x - px) ** 2 + (y - py) ** 2;
    if (d < bestD) { bestD = d; best = i; }
  }
  return best;
}

canvas.addEventListener('click', (e) => { select(nearest(e.clientX, e.clientY, 10)); dirty = true; });
canvas.addEventListener('mousemove', (e) => {
  const i = nearest(e.clientX, e.clientY, 8), tip = $('tip');
  if (i === null) { tip.hidden = true; return; }
  tip.hidden = false;
  tip.textContent = `${catalog[i].name} · ${fmt(pos[3 * i + 2])} km`;
  tip.style.left = e.clientX + 12 + 'px'; tip.style.top = e.clientY + 12 + 'px';
});
canvas.addEventListener('mouseleave', () => { $('tip').hidden = true; });

document.querySelectorAll('[data-speed]').forEach((b) => b.addEventListener('click', () => {
  setClock(simNow(), Number(b.dataset.speed));
  document.querySelectorAll('[data-speed]').forEach((x) => x.classList.toggle('on', x === b));
}));
$('now').addEventListener('click', () => {
  setClock(Date.now(), 1);
  document.querySelectorAll('[data-speed]').forEach((x) => x.classList.toggle('on', x.dataset.speed === '1'));
  fetchInfo();
});

// search: name substring or exact NORAD number, best matches first
let matches = [], active = -1;
$('q').addEventListener('input', () => {
  const q = $('q').value.trim().toUpperCase();
  const list = $('suggest');
  matches = [];
  if (q) {
    for (let i = 0; i < catalog.length && matches.length < 50; i++) {
      const c = catalog[i];
      if (String(c.id) === q || c.name.toUpperCase().includes(q)) matches.push(i);
    }
    matches.sort((a, b) => (catalog[a].name.toUpperCase().startsWith(q) ? 0 : 1) - (catalog[b].name.toUpperCase().startsWith(q) ? 0 : 1));
    matches = matches.slice(0, 8);
  }
  active = -1;
  list.innerHTML = matches.map((i) => `<li data-i="${i}">${esc(catalog[i].name)} <small>${catalog[i].id}</small></li>`).join('');
  list.hidden = matches.length === 0;
});
$('suggest').addEventListener('click', (e) => {
  const li = e.target.closest('li'); if (!li) return;
  pick(Number(li.dataset.i));
});
$('q').addEventListener('keydown', (e) => {
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault();
    active = Math.max(0, Math.min(matches.length - 1, active + (e.key === 'ArrowDown' ? 1 : -1)));
    [...$('suggest').children].forEach((li, k) => li.classList.toggle('active', k === active));
  } else if (e.key === 'Enter' && matches.length) {
    pick(matches[Math.max(active, 0)]);
  } else if (e.key === 'Escape') {
    $('suggest').hidden = true;
  }
});
function pick(i) {
  $('suggest').hidden = true; $('q').value = catalog[i].name;
  const c = catalog[i];
  showKind.add(c.kind); showOrbit.add(c.orbit); syncFilters();
  select(i);
}

function buildFilters() {
  $('kinds').innerHTML = Object.entries(KINDS).map(([k, v]) => `
    <label><input type="checkbox" data-kind="${k}" checked>
      <span class="dot" style="background:${v.color}"></span>${v.label}<span class="count" id="count-${k}">–</span></label>`).join('');
  $('orbits').innerHTML = ORBITS.map((o) => `<button data-orbit="${o}" class="on">${o}</button>`).join('');
  $('kinds').addEventListener('change', (e) => {
    const k = e.target.dataset.kind;
    e.target.checked ? showKind.add(k) : showKind.delete(k);
    updateMeta(); dirty = true;
  });
  $('orbits').addEventListener('click', (e) => {
    const o = e.target.dataset.orbit; if (!o) return;
    showOrbit.has(o) ? showOrbit.delete(o) : showOrbit.add(o);
    syncFilters();
  });
}
function syncFilters() {
  document.querySelectorAll('[data-kind]').forEach((x) => { x.checked = showKind.has(x.dataset.kind); });
  document.querySelectorAll('[data-orbit]').forEach((x) => x.classList.toggle('on', showOrbit.has(x.dataset.orbit)));
  updateMeta(); dirty = true;
}

// ---- start ----
async function start() {
  resize();
  window.addEventListener('resize', resize);
  buildFilters();
  catalog = await (await fetch('/api/catalog')).json();
  byKind = Object.fromEntries(Object.keys(KINDS).map((k) => [k, []]));
  catalog.forEach((c, i) => byKind[c.kind].push(i));
  requestAnimationFrame(frame);
  fetchPositions();
  // Select the object named in the URL (#41866), or the ISS.
  const want = Number(location.hash.slice(1)) || 25544;
  const i0 = catalog.findIndex((c) => c.id === want);
  if (i0 >= 0) select(i0);
  try {
    const world = await (await fetch('https://cdn.jsdelivr.net/npm/world-atlas@2.0.2/land-110m.json')).json();
    land = topojson.feature(world, world.objects.land);
    base = null; dirty = true;
  } catch (e) {
    console.warn('coastlines unavailable (offline?)', e);
  }
}
start();
