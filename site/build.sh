#!/bin/sh
# Builds the public site into a folder (default _site): the tracker's page
# with site/api.js swapped in, its engine compiled to WebAssembly, and the
# element sets from data/, and the next day's closest approaches (site/closest,
# ~30 s on 10 cores). Run `go run ./cmd/fetch` first.
set -eu
out=${1:-_site}
rm -rf "$out"
mkdir -p "$out/data"
cp -R cmd/track/web/* "$out/"
cp site/api.js "$out/api.js"
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$out/"
GOOS=js GOARCH=wasm go build -trimpath -ldflags=-s -o "$out/orbitscreen.wasm" ./cmd/trackwasm
cp data/*.csv "$out/data/"
(cd "$out/data" && ls *.csv) >"$out/data/files.txt"
go run ./site/closest -data data -out "$out/closest.json"
