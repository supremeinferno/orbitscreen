// orbitscreen live tracker: draws every catalogued object on a world map.
// The orbit maths is Go (internal/live), reached through api.js: over HTTP
// from cmd/track, or compiled to WebAssembly on the public site. This file
// only draws. See docs/06-live-tracker.md.
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
let info = null;         // api.object() result for the selected object
let dirty = true;
let lastFetchMs = 0;
let indexOf = new Map(); // NORAD id -> catalog index
let approaches = null;   // api.closest() result, if the page has one
let pair = null;         // {b: catalog index, c: approach} while one is shown
let view = '2d';         // '3d' (globe.js) or '2d' (this file's map)
let globe = null;        // the 3D view, once loaded
let lastPosT = null;     // the time pos was computed for

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
  paintMap(b, mapX, mapY, mapW, mapH, 1);
}

// The 3D globe's texture: the same map, filling a 4096×2048 canvas.
function mapTexture() {
  const c = document.createElement('canvas');
  c.width = 4096; c.height = 2048;
  paintMap(c.getContext('2d'), 0, 0, c.width, c.height, 2);
  return c;
}

// Ocean, graticule and land in the rectangle x0, y0, w, h (equirectangular).
function paintMap(b, x0, y0, w, h, line) {
  const project = (lat, lon) => [x0 + (lon + 180) / 360 * w, y0 + (90 - lat) / 180 * h];
  const [mapX, mapY, mapW, mapH] = [x0, y0, w, h];
  b.fillStyle = '#0a1a2b'; b.fillRect(mapX, mapY, mapW, mapH);
  // graticule every 30°
  b.strokeStyle = 'rgba(255,255,255,0.06)'; b.lineWidth = line; b.beginPath();
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
    b.fillStyle = '#1f3d54'; b.strokeStyle = '#2f5a76'; b.lineWidth = 0.6 * line; b.beginPath();
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
      if (pair && !Number.isNaN(pos[3 * pair.b])) {
        const [bx, by] = project(pos[3 * pair.b], pos[3 * pair.b + 1]);
        ctx.strokeStyle = '#ef476f'; ctx.fillStyle = '#ef476f'; ctx.lineWidth = 1.5;
        ctx.beginPath(); ctx.moveTo(x, y); ctx.lineTo(bx, by); ctx.stroke();
        ctx.beginPath(); ctx.arc(bx, by, 6, 0, 2 * Math.PI); ctx.stroke();
        ctx.fillText(catalog[pair.b].name, bx + 13, by < y ? by - 10 : by + 18); // clear of the other label
      }
    }
  }
  ctx.restore();
}

const clock = (ms) => new Date(ms).toISOString().slice(11, 19);

function frame() {
  $('simtime').textContent = new Date(simNow()).toISOString().replace('T', ' ').slice(0, 19) + ' UTC';
  const cd = pair && $('countdown');
  if (cd) {
    const s = Math.round((Date.parse(pair.c.tca) - simNow()) / 1000);
    cd.textContent = s > 0 ? `closest in ${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}` : s === 0 ? 'closest now' : `${fmt(-s)} s ago`;
  }
  if (view === '3d') {
    if (dirty) { syncGlobe(); dirty = false; }
    const sun = subsolar(simNow());
    globe.setSun(sun.lat, sun.lon);
    globe.render(performance.now());
  } else if (dirty) { draw(); dirty = false; }
  requestAnimationFrame(frame);
}

// ---- data ----
async function updatePositions() {
  try {
    const t0 = performance.now();
    const t = Math.round(simNow());
    if (!(speed === 0 && t === lastPosT)) {
      pos = await api.positions(t);
      lastPosT = t;
    }
    if (!$('loading').hidden && view === '3d' && selected !== null && !Number.isNaN(pos[3 * selected])) {
      globe.flyTo(pos[3 * selected], pos[3 * selected + 1], pos[3 * selected + 2]); // the first view
    }
    $('loading').hidden = true;
    lastFetchMs = performance.now() - t0;
    dirty = true;
    updateMeta();
  } catch (e) {
    console.error(e);
  }
}
async function positionsLoop() {
  await updatePositions();
  setTimeout(positionsLoop, speed > 1 ? 250 : 1000);
}

async function fetchInfo() {
  if (selected === null) return;
  const id = catalog[selected].id;
  const next = await api.object(id, Math.round(simNow()));
  if (!next || selected === null || catalog[selected].id !== id) return;
  info = next;
  globe?.setTrack(info.track, info.trackNow);
  renderPanel();
  dirty = true;
}
setInterval(fetchInfo, 3000);

function select(i, keepPair = false) {
  if (!keepPair && pair) { pair = null; renderApproaches(); }
  selected = i; info = null;
  // Keep the selection in the URL (#25544) so a view can be linked to.
  history.replaceState(null, '', i === null ? location.pathname : '#' + catalog[i].id);
  syncSelection();
  if (i === null) { $('panel').hidden = true; dirty = true; return; }
  if (view === '3d' && pos && !Number.isNaN(pos[3 * i])) globe.flyTo(pos[3 * i], pos[3 * i + 1], pos[3 * i + 2]);
  $('panel').hidden = false;
  $('panel').innerHTML = `<p class="sub">Loading ${esc(catalog[i].name)}…</p>`;
  fetchInfo();
}

const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
// Formation flyers pass each other at metres per second, not kilometres.
const relSpeed = (kms) => (kms < 1 ? `${fmt(kms * 1000)} m/s` : `${fmt(kms, 1)} km/s`);
const fmt = (x, d = 0) => Number(x).toLocaleString('en-US', { minimumFractionDigits: d, maximumFractionDigits: d });

function renderPanel() {
  if (!info) return;
  const c = catalog[selected];
  const ageH = (simNow() - Date.parse(info.epoch)) / 3600000;
  const ns = (lat) => `${fmt(Math.abs(lat), 2)}° ${lat >= 0 ? 'N' : 'S'}`;
  const ew = (lon) => `${fmt(Math.abs(lon), 2)}° ${lon >= 0 ? 'E' : 'W'}`;
  $('panel').innerHTML = `
    <button class="btn icon close" aria-label="Close" title="Close (Esc)"><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M4 4l8 8M12 4l-8 8"/></svg></button>
    <h2>${esc(c.name)}</h2>
    <p class="sub">NORAD ${c.id}${info.intlDes ? ' · ' + esc(info.intlDes) : ''} · ${KINDS[c.kind].label.replace(/s$/, '').toLowerCase()} · ${c.orbit}</p>
    ${pair ? `<div class="pair">Close approach with <strong>${esc(catalog[pair.b].name)}</strong><br>
      ${fmt(pair.c.km * 1000)} m at ${clock(Date.parse(pair.c.tca))} UTC, ${relSpeed(pair.c.kms)} relative
      <span class="countdown" id="countdown"></span></div>` : ''}
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
    ${info.error ? '' : batterySection(c.id, info, simNow(), subsolar)}
    ${c.attached ? '<p class="note">Docked to another catalogued object (same orbit data).</p>' : ''}
    <p class="note">${view === '3d' ? 'Orbit' : 'Ground track'}: faint behind, bright ahead.${view === '3d' ? '' : ' Circle: where it is above the horizon.'}</p>`;
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
  $('meta').textContent = `${fmt(shown)} of ${fmt(catalog.length)} objects · positions computed ${api.how} in ${fmt(lastFetchMs)} ms`;
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

function showTip(e, i) {
  const tip = $('tip');
  if (i === null) { tip.hidden = true; return; }
  tip.hidden = false;
  tip.textContent = `${catalog[i].name} · ${fmt(pos[3 * i + 2])} km`;
  tip.style.left = e.clientX + 12 + 'px'; tip.style.top = e.clientY + 12 + 'px';
}
canvas.addEventListener('click', (e) => { select(nearest(e.clientX, e.clientY, 10)); dirty = true; });
canvas.addEventListener('mousemove', (e) => showTip(e, nearest(e.clientX, e.clientY, 8)));
canvas.addEventListener('mouseleave', () => { $('tip').hidden = true; });

// The globe: a click is a press and release without dragging.
let down = null;
$('globe').addEventListener('pointerdown', (e) => { down = [e.clientX, e.clientY]; $('tip').hidden = true; });
$('globe').addEventListener('pointerup', (e) => {
  if (down && Math.hypot(e.clientX - down[0], e.clientY - down[1]) < 5) select(globe.pick(e.clientX, e.clientY, 12));
  down = null;
});
$('globe').addEventListener('mousemove', (e) => { if (!e.buttons) showTip(e, globe.pick(e.clientX, e.clientY, 8)); });
$('globe').addEventListener('mouseleave', () => { $('tip').hidden = true; });

function speedButtons(v) {
  document.querySelectorAll('[data-speed]').forEach((x) => x.classList.toggle('on', Number(x.dataset.speed) === v));
  $('pause').classList.toggle('on', v === 0);
  $('pause').innerHTML = v === 0
    ? '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M5 3.5v9l7.5-4.5z"/></svg>'
    : '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M5.5 3.5v9M10.5 3.5v9"/></svg>';
  $('pause').setAttribute('aria-label', v === 0 ? 'Resume' : 'Pause');
}
let resumeSpeed = 1;
function togglePause() {
  if (speed === 0) setClock(simNow(), resumeSpeed);
  else { resumeSpeed = speed; setClock(simNow(), 0); }
  speedButtons(speed);
}
$('pause').addEventListener('click', togglePause);
document.querySelectorAll('[data-speed]').forEach((b) => b.addEventListener('click', () => {
  setClock(simNow(), Number(b.dataset.speed));
  speedButtons(speed);
}));
$('now').addEventListener('click', () => {
  setClock(Date.now(), 1);
  speedButtons(1);
  if (pair) { pair = null; renderApproaches(); syncSelection(); }
  fetchInfo();
});

// search: name substring or exact NORAD number, best matches first
let matches = [], active = -1;
$('q').addEventListener('input', () => {
  const q = $('q').value.trim().toUpperCase();
  const list = $('suggest');
  matches = [];
  if (!q) {
    // Nothing typed yet: a few well-known objects.
    matches = POPULAR.map((id) => indexOf.get(id)).filter((i) => i !== undefined);
  } else {
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
$('q').addEventListener('focus', () => $('q').dispatchEvent(new Event('input')));
$('q').addEventListener('blur', () => setTimeout(() => { $('suggest').hidden = true; }, 150));
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
  $('orbits').innerHTML = ORBITS.map((o) => `<button class="btn on" data-orbit="${o}">${o}</button>`).join('');
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

// ---- close approaches (public site only) ----
function renderApproaches() {
  if (!approaches) return;
  const now = Date.now(), ago = (now - Date.parse(approaches.generated)) / 3600000;
  // The list was screened up to hours ago: show only what is still to come,
  // and leave out pairs flying together (an older list may still hold them).
  const ahead = approaches.approaches.map((c, k) => [c, k]).filter(([c]) => Date.parse(c.tca) > now - 60000 && c.kms >= 0.05);
  const left = Math.max(0, (Date.parse(approaches.start) + approaches.hours * 3600000 - now) / 3600000);
  const together = approaches.together ? ` ${fmt(approaches.together)} pairs that fly together (one docked to the other, or just released from the same rocket) are left out.` : '';
  $('approaches').innerHTML = `
    <p class="lede">The closest of ${fmt(approaches.total)} pairs passing within 5 km, involving an active satellite: the next ${fmt(left)} h of a screening this project ran ${ago < 1 ? 'under an hour' : fmt(ago) + ' h'} ago.${together} Click one to watch it.</p>
    ${ahead.length ? `<ol>${ahead.map(([c, k]) => `<li><button data-k="${k}" class="${pair && pair.c === c ? 'on' : ''}">
      <span class="names">${esc(c.a.name)} × ${esc(c.b.name)}</span><span class="km">${fmt(c.km * 1000)} m</span>
      <span class="when">${new Date(c.tca).toISOString().slice(5, 16).replace('T', ' ')} UTC · ${relSpeed(c.kms)}</span></button></li>`).join('')}</ol>`
      : '<p class="lede">All of the closest approaches from the last screening have passed; the next screening will list new ones.</p>'}`;
}
setInterval(renderApproaches, 60000); // passed approaches drop off the list
$('approaches').addEventListener('click', async (e) => {
  const b = e.target.closest('[data-k]'); if (!b) return;
  const c = approaches.approaches[Number(b.dataset.k)];
  const ia = indexOf.get(c.a.id), ib = indexOf.get(c.b.id);
  if (ia === undefined || ib === undefined) return;
  for (const i of [ia, ib]) { showKind.add(catalog[i].kind); showOrbit.add(catalog[i].orbit); }
  syncFilters();
  // A minute before closest approach, at normal speed, to watch it happen.
  setClock(Date.parse(c.tca) - 60000, 1);
  speedButtons(1);
  pair = { b: ib, c };
  await updatePositions(); // so the camera flies to where the objects are then
  select(ia, true);
  renderApproaches();
  if (matchMedia('(max-width: 640px)').matches) setSide(false);
});
// ---- side panel: tabs, and collapsing it out of the way ----
function showTab(name) {
  document.querySelectorAll('.tab').forEach((t) => t.setAttribute('aria-selected', String(t.dataset.tab === name)));
  $('approaches').hidden = name !== 'approaches';
  $('layers').hidden = name !== 'layers';
}
function setSide(open) {
  $('side').hidden = !open;
  $('openSide').hidden = open;
}
document.querySelectorAll('.tab').forEach((t) => t.addEventListener('click', () => showTab(t.dataset.tab)));
$('collapse').addEventListener('click', () => setSide(false));
$('openSide').addEventListener('click', () => setSide(true));
showTab('layers');
setSide(!matchMedia('(max-width: 900px)').matches);
speedButtons(1);

$('aboutBtn').addEventListener('click', () => $('about').showModal());
try {
  if (!localStorage.getItem('orbitscreen.about')) {
    localStorage.setItem('orbitscreen.about', '1');
    $('about').showModal();
  }
} catch (e) { /* storage blocked: skip the first-visit card */ }

// ---- 3D view ----
const POPULAR = [25544, 48274, 20580, 41866]; // ISS, Tiangong, Hubble, GOES 16

// Kind colours as linear RGB, which is what WebGL vertex colours expect.
const lin = (c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
const KIND_RGB = Object.fromEntries(Object.entries(KINDS).map(([k, v]) =>
  [k, [1, 3, 5].map((o) => lin(parseInt(v.color.slice(o, o + 2), 16) / 255))]));

function syncGlobe() {
  if (globe && pos) globe.setPoints(pos, catalog.length, (i) => (visible(i) ? KIND_RGB[catalog[i].kind] : null));
}
function syncSelection() {
  globe?.setSelection(selected, pair ? pair.b : null,
    [selected === null ? '' : catalog[selected].name, pair ? catalog[pair.b].name : '']);
}

async function setView(v) {
  if (v === '3d' && !globe) {
    try {
      const { createGlobe } = await import('./globe.js');
      $('globe').hidden = false;
      globe = createGlobe($('globe'));
      globe.setTexture(mapTexture());
      syncSelection();
      if (info) globe.setTrack(info.track, info.trackNow);
      // Positions may have arrived before three.js did: look at the selection now.
      if (selected !== null && pos && !Number.isNaN(pos[3 * selected])) {
        globe.flyTo(pos[3 * selected], pos[3 * selected + 1], pos[3 * selected + 2]);
      }
    } catch (e) {
      console.warn('3D view unavailable', e);
      v = '2d';
    }
  }
  view = v;
  $('globe').hidden = v !== '3d';
  $('map').hidden = v === '3d';
  globe?.setActive(v === '3d');
  document.querySelectorAll('[data-view]').forEach((b) => b.classList.toggle('on', b.dataset.view === v));
  try { localStorage.setItem('orbitscreen.view', v); } catch (e) { /* not remembered */ }
  dirty = true;
  renderPanel();
}
document.querySelectorAll('[data-view]').forEach((b) => b.addEventListener('click', () => setView(b.dataset.view)));

// ---- keyboard ----
document.addEventListener('keydown', (e) => {
  if (e.target.closest('input') || $('about').open) return;
  if (e.key === '/') { e.preventDefault(); $('q').focus(); }
  else if (e.key === ' ' && !e.target.closest('button')) { e.preventDefault(); togglePause(); }
  else if (e.key === 'Escape') { if (selected !== null) select(null); else setSide(false); }
});

// ---- start ----
async function start() {
  resize();
  window.addEventListener('resize', resize);
  buildFilters();
  try {
    catalog = await api.catalog();
  } catch (e) {
    $('loading').textContent = `Couldn't load the catalog (${e.message}).`;
    throw e;
  }
  byKind = Object.fromEntries(Object.keys(KINDS).map((k) => [k, []]));
  catalog.forEach((c, i) => { byKind[c.kind].push(i); indexOf.set(c.id, i); });
  // Battery status of a few satellites, from satnogs-pipeline (battery.js); the
  // tracker works the same without it.
  const refreshBattery = () => loadBatteryStatus().then((ok) => { if (ok && selected !== null) renderPanel(); });
  refreshBattery();
  setInterval(refreshBattery, 10 * 60 * 1000);
  approaches = await api.closest().catch(() => null);
  if (approaches && approaches.approaches.length) {
    $('tab-approaches').hidden = false;
    renderApproaches();
    showTab('approaches');
  }
  let saved = null;
  try { saved = localStorage.getItem('orbitscreen.view'); } catch (e) { /* default */ }
  setView(saved || '3d'); // the globe loads three.js; the map works meanwhile
  requestAnimationFrame(frame);
  positionsLoop();
  // Select the object named in the URL (#41866), or the ISS.
  const want = Number(location.hash.slice(1)) || 25544;
  const i0 = catalog.findIndex((c) => c.id === want);
  if (i0 >= 0) select(i0);
  try {
    const world = await (await fetch('https://cdn.jsdelivr.net/npm/world-atlas@2.0.2/land-110m.json')).json();
    land = topojson.feature(world, world.objects.land);
    base = null; dirty = true;
    globe?.setTexture(mapTexture());
  } catch (e) {
    console.warn('coastlines unavailable (offline?)', e);
  }
}
start();
