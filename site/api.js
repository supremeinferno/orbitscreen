// How the page gets its data on the public site: the same data as
// cmd/track's server, computed in the browser by the tracker's Go code
// compiled to WebAssembly (cmd/trackwasm). There is no server; the element
// sets are static files, refreshed by a GitHub Action (.github/workflows/site.yml).
const api = (() => {
  const script = (src) => new Promise((ok, fail) => {
    const s = document.createElement('script');
    s.src = src; s.onload = ok; s.onerror = fail;
    document.head.append(s);
  });
  const text = async (url) => {
    const res = await fetch(url);
    if (!res.ok) throw new Error(`${url}: ${res.status}`);
    return res.text();
  };
  const ready = (async () => {
    await script('wasm_exec.js');
    const go = new Go();
    const files = (await text('data/files.txt')).split('\n').filter(Boolean);
    const [{ instance }, csvs] = await Promise.all([
      WebAssembly.instantiateStreaming(fetch('orbitscreen.wasm'), go.importObject),
      Promise.all(files.map((f) => text(`data/${f}`))),
    ]);
    go.run(instance);
    orbitscreen.load(csvs, files.map((f) => f === 'active.csv'));
  })();
  return {
    how: 'in your browser',
    async catalog() {
      await ready;
      return JSON.parse(orbitscreen.catalog());
    },
    async positions(t) {
      await ready;
      return new Float32Array(orbitscreen.positions(t).buffer);
    },
    async object(id, t) {
      await ready;
      const s = orbitscreen.object(id, t);
      return s === null ? null : JSON.parse(s);
    },
  };
})();
