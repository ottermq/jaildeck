package main

import (
	"bytes"
	"testing"
	"testing/fstest"
)

func TestVerify(t *testing.T) {
	index := []byte(`<div id="app"></div><script type="module" src="/assets/index-123.js"></script><link href="/assets/index-123.css">`)
	js, css := []byte("compiled Svelte bundle"), []byte("compiled stylesheet")
	valid := func() fstest.MapFS {
		return fstest.MapFS{
			"index.html":           {Data: index},
			"assets/index-123.js":  {Data: js},
			"assets/index-123.css": {Data: css},
			".gitkeep":             {},
		}
	}
	binary := bytes.Join([][]byte{[]byte("executable"), index, js, css}, []byte{0})
	for _, test := range []struct {
		name   string
		change func(fstest.MapFS)
		binary []byte
		ok     bool
	}{
		{name: "built and embedded", binary: binary, ok: true},
		{name: "development fallback", change: func(f fstest.MapFS) { delete(f, "index.html") }, binary: binary},
		{name: "empty index", change: func(f fstest.MapFS) { f["index.html"].Data = nil }, binary: binary},
		{name: "source HTML", change: func(f fstest.MapFS) { f["index.html"].Data = []byte(`<script src="/src/main.ts"></script>`) }, binary: binary},
		{name: "missing bundle", change: func(f fstest.MapFS) { delete(f, "assets/index-123.js") }, binary: binary},
		{name: "missing stylesheet", change: func(f fstest.MapFS) { delete(f, "assets/index-123.css") }, binary: binary},
		{name: "stale executable", binary: []byte("an executable with no frontend")},
		{name: "partial embedding", binary: bytes.Join([][]byte{index, js}, nil)},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := valid()
			if test.change != nil {
				test.change(files)
			}
			if err := verify(files, test.binary); (err == nil) != test.ok {
				t.Fatalf("verify error = %v, want success %v", err, test.ok)
			}
		})
	}
}
