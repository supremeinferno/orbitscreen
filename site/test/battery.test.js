'use strict';
// Run with: node --test site/test
const test = require('node:test');
const assert = require('node:assert');
const b = require('../../cmd/track/web/battery.js');

const sunOverEquator = { lat: 0, lon: 0 }; // the Sun is straight above (0, 0)

test('in shadow: behind the Earth, near the axis', () => {
  assert.strictEqual(b.inEarthShadow(0, 180, 500, sunOverEquator), true);
  assert.strictEqual(b.inEarthShadow(60, 180, 500, sunOverEquator), true);
});

test('in sunlight: on the Sun side of the Earth', () => {
  assert.strictEqual(b.inEarthShadow(0, 0, 500, sunOverEquator), false);
  assert.strictEqual(b.inEarthShadow(0, 60, 500, sunOverEquator), false);
  assert.strictEqual(b.inEarthShadow(0, -60, 500, sunOverEquator), false);
});

test('in sunlight: behind the Earth but clear of the shadow (a polar pass)', () => {
  // At 80 degrees north on the night side the point is 6700 km from the axis,
  // more than the Earth's radius.
  assert.strictEqual(b.inEarthShadow(80, 180, 500, sunOverEquator), false);
});

test('the terminator at the equator is lit, as the satellite is above the surface', () => {
  assert.strictEqual(b.inEarthShadow(0, 90, 500, sunOverEquator), false);
  assert.strictEqual(b.inEarthShadow(0, -90, 500, sunOverEquator), false);
});

test('high orbits stay lit just past the terminator, low ones do not', () => {
  // Just behind the terminator (longitude 100) at the equator: a low satellite is
  // already in the shadow cylinder; a geostationary one, 35,786 km up, is not.
  assert.strictEqual(b.inEarthShadow(0, 100, 500, sunOverEquator), false); // 500 km: perp is 6870 km
  assert.strictEqual(b.inEarthShadow(0, 180, 35786, sunOverEquator), true); // dead behind: shadowed
  assert.strictEqual(b.inEarthShadow(10, 180, 35786, sunOverEquator), false); // 10 degrees off: lit
});

test('the Sun moving moves the shadow', () => {
  assert.strictEqual(b.inEarthShadow(0, 180, 500, { lat: 0, lon: 180 }), false); // now the Sun is over the satellite
  assert.strictEqual(b.inEarthShadow(0, 0, 500, { lat: 0, lon: 180 }), true);
});

const snapshot = {
  generated_at: '2026-10-23T10:00:00Z',
  satellites: [
    { norad: 43880, name: 'UWE-4', last_beacon: '2026-10-23T09:00:00Z', stale: false, batteries: [
      { battery: 'a', readings: [{ mv: 4100, ma: 118 }, { mv: 3972, ma: -250 }], anomalies: { discharging_in_sunlight: false } },
      { battery: 'b', readings: [{ mv: 3830, ma: -308 }], anomalies: {} },
    ] },
    { norad: 43784, name: 'SNUGLITE', last_beacon: '2026-10-22T10:00:00Z', stale: true, batteries: [
      { battery: 'main', readings: [{ mv: 8150 }], anomalies: { voltage_below_baseline: true } },
    ] },
  ],
};
const real = Date.parse('2026-10-23T10:30:00Z');
const sun = () => sunOverEquator;

test('no section before the snapshot has loaded, or for another satellite', () => {
  b.setStatus(null);
  assert.strictEqual(b.batterySection(43880, { lat: 0, lon: 0, altKm: 500 }, real, sun, real), '');
  b.setStatus(snapshot);
  assert.strictEqual(b.batterySection(25544, { lat: 0, lon: 0, altKm: 400 }, real, sun, real), '');
});

test('the section shows the latest voltage and current of each battery, and the lighting now', () => {
  b.setStatus(snapshot);
  const html = b.batterySection(43880, { lat: 0, lon: 180, altKm: 500 }, real, sun, real);
  assert.match(html, /in the Earth's shadow/);
  assert.match(html, /Battery A<\/dt><dd>3\.97 V, -250 mA/); // the newest reading, not the first
  assert.match(html, /Battery B<\/dt><dd>3\.83 V, -308 mA/);
  assert.match(html, /Last beacon<\/dt><dd>90 minutes ago/);
  assert.match(html, /none firing/);
  assert.match(html, /snapshot from 30 minutes ago/);
  assert.match(html, /supremeinferno\.github\.io\/satnogs-pipeline\//);
});

test('the lighting follows the time given, not the snapshot', () => {
  b.setStatus(snapshot);
  const lit = b.batterySection(43880, { lat: 0, lon: 0, altKm: 500 }, real, sun, real);
  assert.match(lit, /in sunlight/);
  assert.doesNotMatch(lit, /Earth's shadow/);
});

test('a battery without a current shows only the voltage; a fired rule is shown as an alert', () => {
  b.setStatus(snapshot);
  const html = b.batterySection(43784, { lat: 0, lon: 0, altKm: 500 }, real, sun, real);
  assert.match(html, /Battery<\/dt><dd>8\.15 V<\/dd>/);
  assert.match(html, /class="alert">voltage below its own usual/);
  assert.doesNotMatch(html, /none firing/);
});

test('a silent satellite says so', () => {
  b.setStatus({ ...snapshot, satellites: [{ ...snapshot.satellites[1], batteries: [{ battery: 'main', readings: [{ mv: 8150 }], anomalies: {} }] }] });
  assert.match(b.batterySection(43784, null, real, sun, real), /no recent beacons/);
});

test('without a position the section still works, with no lighting line', () => {
  b.setStatus(snapshot);
  const html = b.batterySection(43880, null, real, sun, real);
  assert.doesNotMatch(html, /Lighting/);
  assert.match(html, /Battery A/);
});

test('names from the file are escaped', () => {
  b.setStatus({ generated_at: '2026-10-23T10:00:00Z', satellites: [{ norad: 1, batteries: [{ battery: '<img src=x>', readings: [{ mv: 4000 }], anomalies: {} }] }] });
  const html = b.batterySection(1, null, real, sun, real);
  assert.doesNotMatch(html, /<img/);
  assert.match(html, /&lt;IMG SRC=X&gt;/);
});

test('ago', () => {
  assert.strictEqual(b.batteryAgo(30 * 1000), 'just now');
  assert.strictEqual(b.batteryAgo(45 * 60000), '45 minutes ago');
  assert.strictEqual(b.batteryAgo(5 * 3600000), '5 hours ago');
  assert.strictEqual(b.batteryAgo(3 * 86400000), '3 days ago');
});
