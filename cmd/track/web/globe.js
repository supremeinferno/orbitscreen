// The 3D view: every object at its true altitude around a lit Earth, drawn
// with three.js. app.js owns the data and the UI; this module only draws
// and answers "which object is under the pointer?". Everything is in an
// Earth-fixed frame (latitude, longitude, altitude), so the Earth stays
// still and the Sun's light moves. One unit is one Earth radius.
import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import { earthMaterial } from './earth.js';

const R = 6371; // km

// Earth-fixed position for latitude, longitude (degrees) and altitude (km),
// matching the texture layout of THREE.SphereGeometry: longitude −180° on
// −x, +90° on −z, north up.
function toVec(lat, lon, alt, v = new THREE.Vector3()) {
  const r = 1 + alt / R, p = lat * Math.PI / 180, l = lon * Math.PI / 180;
  return v.set(r * Math.cos(p) * Math.cos(l), r * Math.sin(p), -r * Math.cos(p) * Math.sin(l));
}

export function createGlobe(canvas) {
  const dpr = Math.min(window.devicePixelRatio || 1, 2);
  const renderer = new THREE.WebGLRenderer({ canvas, antialias: true });
  renderer.setPixelRatio(dpr);
  const scene = new THREE.Scene();
  scene.background = new THREE.Color('#03070d');
  const camera = new THREE.PerspectiveCamera(45, 1, 0.01, 200);
  camera.position.set(0, 1.2, 3.4);
  const controls = new OrbitControls(camera, canvas);
  Object.assign(controls, { enableDamping: true, enablePan: false, minDistance: 1.15, maxDistance: 25, rotateSpeed: 0.5, zoomSpeed: 0.8 });

  // Earth: NASA's day and night images, split where the Sun is (earth.js,
  // shared with satnogs-pipeline's page). The 2D map keeps its drawn coastlines.
  const sunDir = new THREE.Vector3(1, 0, 0);
  const earth = new THREE.Mesh(new THREE.SphereGeometry(1, 96, 64), earthMaterial(sunDir, { nightGlow: 1.8 }));
  scene.add(earth);
  scene.add(new THREE.AmbientLight(0xffffff, 1.1));
  const sun = new THREE.DirectionalLight(0xffffff, 2.6);
  scene.add(sun);

  // A faint atmosphere: a slightly larger sphere, seen from inside, that
  // glows where it is seen edge-on.
  scene.add(new THREE.Mesh(new THREE.SphereGeometry(1.06, 64, 32), new THREE.ShaderMaterial({
    side: THREE.BackSide, blending: THREE.AdditiveBlending, transparent: true, depthWrite: false,
    vertexShader: 'varying vec3 n; void main() { n = normalize(normalMatrix * normal); gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0); }',
    fragmentShader: 'varying vec3 n; void main() { float i = pow(max(0.0, 0.62 - dot(n, vec3(0.0, 0.0, 1.0))), 3.0); gl_FragColor = vec4(0.3, 0.7, 1.0, 1.0) * i * 1.6; }',
  })));

  // Background stars, fixed far away.
  const starPos = new Float32Array(3 * 2500);
  for (let k = 0; k < starPos.length; k += 3) {
    const v = new THREE.Vector3().randomDirection().multiplyScalar(90);
    starPos.set([v.x, v.y, v.z], k);
  }
  const starGeo = new THREE.BufferGeometry();
  starGeo.setAttribute('position', new THREE.BufferAttribute(starPos, 3));
  scene.add(new THREE.Points(starGeo, new THREE.PointsMaterial({ color: 0x6f7f95, size: 1.2 * dpr, sizeAttenuation: false })));

  // Every object, one vertex each. Hidden objects sit at the centre of the
  // Earth, where the depth test hides them.
  const objGeo = new THREE.BufferGeometry();
  const objects = new THREE.Points(objGeo, new THREE.PointsMaterial({ size: 2.2 * dpr, sizeAttenuation: false, vertexColors: true }));
  objects.frustumCulled = false;
  scene.add(objects);
  let xyz = new Float32Array(0);
  let shown = new Uint8Array(0);

  // Selection: a marker per highlighted object, the orbit, the pair line.
  const marker = (color, size) => {
    const g = new THREE.BufferGeometry();
    g.setAttribute('position', new THREE.BufferAttribute(new Float32Array(3), 3));
    const m = new THREE.Points(g, new THREE.PointsMaterial({ color, size: size * dpr, sizeAttenuation: false }));
    m.frustumCulled = false; m.visible = false; m.renderOrder = 2;
    scene.add(m);
    return m;
  };
  const selMarker = marker(0xffffff, 9), pairMarker = marker(0xef476f, 9);
  const line = (color, opacity) => {
    const l = new THREE.Line(new THREE.BufferGeometry(), new THREE.LineBasicMaterial({ color, transparent: true, opacity }));
    l.frustumCulled = false; l.visible = false;
    scene.add(l);
    return l;
  };
  const past = line(0xffffff, 0.3), future = line(0xffffff, 0.85), pairLine = line(0xef476f, 0.9);

  const labels = [0, 1].map(() => {
    const d = document.createElement('div');
    d.className = 'label'; d.hidden = true;
    document.body.append(d);
    return d;
  });

  let sel = null, pairB = null, fly = null, active = true;

  function resize() {
    const w = canvas.clientWidth, h = canvas.clientHeight;
    renderer.setSize(w, h, false);
    camera.aspect = w / h; camera.updateProjectionMatrix();
  }

  const v = new THREE.Vector3(), w = new THREE.Vector3();

  // Screen position of point i, or null if it's off screen or behind the
  // Earth (the segment from the camera to it passes inside the sphere).
  function screenOf(i) {
    if (!shown[i]) return null;
    v.fromArray(xyz, 3 * i);
    w.subVectors(v, camera.position);
    const t = -camera.position.dot(w) / w.lengthSq();
    if (t > 0 && t < 1) {
      const c = camera.position;
      const cx = c.x + t * w.x, cy = c.y + t * w.y, cz = c.z + t * w.z;
      if (cx * cx + cy * cy + cz * cz < 1) return null;
    }
    v.project(camera);
    if (v.z > 1) return null;
    return [(v.x + 1) / 2 * canvas.clientWidth, (1 - v.y) / 2 * canvas.clientHeight];
  }

  function placeMarker(m, i) {
    m.visible = i !== null && shown[i] === 1;
    if (!m.visible) return;
    m.geometry.attributes.position.array.set(xyz.subarray(3 * i, 3 * i + 3));
    m.geometry.attributes.position.needsUpdate = true;
  }

  // Labels sit right of their objects. When the two are close on screen,
  // the upper one's label moves up and the lower one's down, so they don't overlap.
  function placeLabels() {
    const ps = [sel, pairB].map((i) => (i === null ? null : screenOf(i)));
    const close = ps[0] && ps[1] && Math.abs(ps[0][1] - ps[1][1]) < 18 && Math.abs(ps[0][0] - ps[1][0]) < 160;
    ps.forEach((p, k) => {
      labels[k].hidden = !p || !active;
      if (!p) return;
      const dy = !close ? -8 : p[1] <= ps[1 - k][1] ? -20 : 4;
      labels[k].style.color = k ? '#ff8fa8' : '#fff';
      labels[k].style.transform = `translate(${p[0] + 12}px, ${p[1] + dy}px)`;
    });
  }

  function frameFly(now) {
    if (!fly) return;
    const t = Math.min(1, (now - fly.t0) / 900), e = t * t * (3 - 2 * t);
    const a = fly.from, b = fly.to, ang = Math.acos(Math.min(1, Math.max(-1, a.dot(b))));
    const dir = ang < 1e-4 ? b.clone()
      : a.clone().multiplyScalar(Math.sin((1 - e) * ang) / Math.sin(ang)).add(b.clone().multiplyScalar(Math.sin(e * ang) / Math.sin(ang)));
    camera.position.copy(dir.normalize().multiplyScalar(fly.d0 + (fly.d1 - fly.d0) * e));
    if (t === 1) fly = null;
  }

  canvas.addEventListener('pointerdown', () => { fly = null; });
  window.addEventListener('resize', resize);
  resize();

  return {
    // The map texture: an equirectangular canvas (north up, −180° left).
    // (kept for app.js; the globe now uses NASA's images instead)
    setTexture() {},
    // pos: Float32Array of lat, lon, alt; color(i): [r, g, b] in 0–1 or null to hide.
    setPoints(pos, n, color) {
      if (xyz.length !== 3 * n) {
        xyz = new Float32Array(3 * n); shown = new Uint8Array(n);
        objGeo.setAttribute('position', new THREE.BufferAttribute(xyz, 3));
        objGeo.setAttribute('color', new THREE.BufferAttribute(new Float32Array(3 * n), 3));
      }
      const col = objGeo.attributes.color.array;
      for (let i = 0; i < n; i++) {
        const c = Number.isNaN(pos[3 * i]) ? null : color(i);
        shown[i] = c ? 1 : 0;
        if (c) { toVec(pos[3 * i], pos[3 * i + 1], pos[3 * i + 2], v).toArray(xyz, 3 * i); col.set(c, 3 * i); }
        else xyz.fill(0, 3 * i, 3 * i + 3);
      }
      objGeo.attributes.position.needsUpdate = true;
      objGeo.attributes.color.needsUpdate = true;
      placeMarker(selMarker, sel); placeMarker(pairMarker, pairB);
      if (sel !== null && pairB !== null && shown[sel] && shown[pairB]) {
        pairLine.geometry.setFromPoints([v.fromArray(xyz, 3 * sel).clone(), w.fromArray(xyz, 3 * pairB).clone()]);
        pairLine.visible = true;
      } else pairLine.visible = false;
    },
    // i: selected index or null; names: [selected, pair] label texts.
    setSelection(i, b, names) {
      sel = i; pairB = b;
      labels[0].textContent = names[0] || ''; labels[1].textContent = names[1] || '';
      placeMarker(selMarker, sel); placeMarker(pairMarker, pairB);
      if (i === null) { past.visible = future.visible = false; pairLine.visible = false; }
    },
    // track: [[lat, lon, alt]], now: index of the current point.
    setTrack(track, now) {
      const pts = track.map(([la, lo, al]) => toVec(la, lo, al));
      past.geometry.setFromPoints(pts.slice(0, now + 1));
      future.geometry.setFromPoints(pts.slice(now));
      past.visible = future.visible = sel !== null;
    },
    setSun(lat, lon) { toVec(lat, lon, 0, sun.position).multiplyScalar(10); toVec(lat, lon, 0, sunDir).normalize(); },
    // Turn the camera to look down on a point, from far enough out to see
    // it. High orbits are seen from 25° above, so a ring like the
    // geostationary belt shows as an ellipse rather than edge-on.
    flyTo(lat, lon, alt) {
      const d = camera.position.length();
      const high = alt > 5000, r = 1 + alt / R;
      const viewLat = high ? Math.max(-60, Math.min(60, lat + (lat < 0 ? -25 : 25))) : lat;
      fly = { t0: performance.now(), from: camera.position.clone().normalize(), to: toVec(viewLat, lon, 0).normalize(),
        d0: d, d1: Math.max(d, high ? 2.2 * r : r + 1.6) };
    },
    pick(x, y, maxPx) {
      let best = null, bestD = maxPx * maxPx;
      for (let i = 0; i < shown.length; i++) {
        const p = screenOf(i);
        if (!p) continue;
        const d = (p[0] - x) ** 2 + (p[1] - y) ** 2;
        if (d < bestD) { bestD = d; best = i; }
      }
      return best;
    },
    setActive(on) { active = on; if (!on) labels.forEach((l) => { l.hidden = true; }); },
    render(now) {
      frameFly(now);
      controls.update();
      renderer.render(scene, camera);
      placeLabels();
    },
  };
}
