// How the page gets its data. This version asks cmd/track's server. The
// public site replaces this file with site/api.js, which computes the same
// data in the browser with the tracker's Go code compiled to WebAssembly.
const api = {
  how: 'and sent',
  async catalog() {
    return (await fetch('/api/catalog')).json();
  },
  // Little-endian float32 lat, lon, alt triples. Every mainstream browser
  // runs on little-endian hardware, so the bytes can be used directly.
  async positions(t) {
    return new Float32Array(await (await fetch(`/api/positions?t=${t}`)).arrayBuffer());
  },
  async object(id, t) {
    const res = await fetch(`/api/object?id=${id}&t=${t}`);
    return res.ok ? res.json() : null;
  },
};
