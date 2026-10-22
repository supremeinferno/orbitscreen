//go:build js && wasm

// Command trackwasm is the live tracker's engine compiled to WebAssembly,
// for the public site: the browser downloads the element sets and runs
// SGP4 itself, so the site needs no server. It exposes the same data as
// cmd/track's HTTP API, as functions on globalThis.orbitscreen:
//
//	load(csvs, primary)  parse OMM CSV texts; primary[i] marks active-satellite files
//	catalog()            JSON, like /api/catalog
//	positions(ms)        Uint8Array of float32 triples, like /api/positions
//	object(id, ms)       JSON, like /api/object, or null
//
// Build:
//
//	GOOS=js GOARCH=wasm go build -o orbitscreen.wasm ./cmd/trackwasm
//
// See docs/06-live-tracker.md.
package main

import (
	"encoding/json"
	"strings"
	"syscall/js"
	"time"

	"github.com/supremeinferno/orbitscreen/internal/catalog"
	"github.com/supremeinferno/orbitscreen/internal/live"
	"github.com/supremeinferno/orbitscreen/pkg/tle"
)

var tracker *live.Tracker

func main() {
	js.Global().Set("orbitscreen", js.ValueOf(map[string]any{
		"load":      js.FuncOf(load),
		"catalog":   js.FuncOf(catalogJSON),
		"positions": js.FuncOf(positions),
		"object":    js.FuncOf(object),
	}))
	select {} // keep the functions alive
}

func load(_ js.Value, args []js.Value) any {
	csvs, primary := args[0], args[1]
	var all []*tle.TLE
	ids := map[int]bool{}
	for i := range csvs.Length() {
		// Per-record parse errors come back joined with the good records,
		// which are kept, as catalog.ReadFile does.
		recs, _ := tle.ReadOMMCSV(strings.NewReader(csvs.Index(i).String()))
		if primary.Index(i).Truthy() {
			for _, el := range recs {
				ids[el.CatalogNum] = true
			}
		}
		all = append(all, recs...)
	}
	objs, st := catalog.FromElements(all, ids)
	tracker = live.New(objs)
	return js.ValueOf(map[string]any{"objects": st.Objects, "active": st.Primaries})
}

func at(ms js.Value) time.Time { return time.UnixMilli(int64(ms.Float())).UTC() }

func catalogJSON(js.Value, []js.Value) any {
	b, _ := json.Marshal(tracker.Catalog())
	return string(b)
}

func positions(_ js.Value, args []js.Value) any {
	buf := tracker.Positions(at(args[0]))
	out := js.Global().Get("Uint8Array").New(len(buf))
	js.CopyBytesToJS(out, buf)
	return out
}

func object(_ js.Value, args []js.Value) any {
	info, ok := tracker.Object(args[0].Int(), at(args[1]))
	if !ok {
		return js.Null()
	}
	b, _ := json.Marshal(info)
	return string(b)
}
