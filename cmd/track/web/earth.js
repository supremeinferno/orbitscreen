// The Earth on the globe (the same file as satnogs-pipeline's status page): NASA's Blue Marble by day and
// Black Marble (city lights) by night, blended across the line between day and
// night by a small shader. sun is a THREE.Vector3 in the sphere's own frame,
// which the caller keeps up to date; SphereGeometry's texture layout puts
// longitude −180° on −x and north up, as globe.js's toVec expects.
//
// Images: NASA Earth Observatory (Blue Marble Next Generation, December 2004;
// Black Marble 2016), reduced to 2048 × 1024.
import * as THREE from "three";

const loader = new THREE.TextureLoader();
let textures = null;
const load = () => textures || (textures = {
  day: loader.load(new URL("img/earth-day.jpg", import.meta.url).href),
  night: loader.load(new URL("img/earth-night.jpg", import.meta.url).href),
});

export function earthMaterial(sun, { dayLight = 1, nightGlow = 1.6 } = {}) {
  const { day, night } = load();
  for (const t of [day, night]) t.anisotropy = 4;
  return new THREE.ShaderMaterial({
    uniforms: { dayTex: { value: day }, nightTex: { value: night }, sun: { value: sun }, dayLight: { value: dayLight }, nightGlow: { value: nightGlow } },
    vertexShader: `
      varying vec2 vUv; varying vec3 vN;
      void main() { vUv = uv; vN = normal; gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0); }`,
    fragmentShader: `
      uniform sampler2D dayTex, nightTex; uniform vec3 sun; uniform float dayLight, nightGlow;
      varying vec2 vUv; varying vec3 vN;
      void main() {
        float d = dot(normalize(vN), normalize(sun));
        float k = smoothstep(-0.12, 0.18, d);                 // the soft line between day and night
        vec3 day = texture2D(dayTex, vUv).rgb * (0.3 + 0.85 * max(d, 0.0)) * dayLight;
        vec3 night = texture2D(nightTex, vUv).rgb * vec3(1.0, 0.82, 0.55) * nightGlow + vec3(0.004, 0.01, 0.025);
        vec3 col = mix(night, day, k);
        col += vec3(1.0, 0.55, 0.25) * 0.12 * (1.0 - abs(k * 2.0 - 1.0)) * step(0.0, d + 0.12); // a warm rim at sunset
        gl_FragColor = vec4(col, 1.0);
      }`,
  });
}
