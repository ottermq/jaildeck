// checkfrontend verifies that a release executable contains the built frontend.
// It is a build tool, so the runtime's deliberate development fallback stays
// unchanged and normal `make build` still needs only Go.
package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
)

var assetReference = regexp.MustCompile(`(?:src|href)=["'](/assets/[^"']+)["']`)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: checkfrontend DIST BINARY")
		os.Exit(1)
	}
	binary, err := os.ReadFile(os.Args[2])
	if err == nil {
		err = verify(os.DirFS(os.Args[1]), binary)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "frontend:", err)
		os.Exit(1)
	}
	fmt.Println("frontend: built index and assets are present in the executable")
}

func verify(dist fs.FS, binary []byte) error {
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		// The cause, not advice: a mistyped DIST and an unreadable file are
		// not fixed by building a frontend that may already be built.
		return fmt.Errorf("reading dist/index.html: %w", err)
	}
	if len(index) == 0 {
		return fmt.Errorf("dist/index.html is empty; build the frontend first")
	}
	hasJS := false
	for _, match := range assetReference.FindAllSubmatch(index, -1) {
		name := strings.TrimPrefix(string(match[1]), "/")
		asset, err := fs.ReadFile(dist, name)
		if err != nil || len(asset) == 0 {
			return fmt.Errorf("index.html references a missing or empty asset: %s", name)
		}
		hasJS = hasJS || path.Ext(name) == ".js"
	}
	if !hasJS {
		return fmt.Errorf("index.html does not reference a built JavaScript bundle")
	}
	return fs.WalkDir(dist, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || name == ".gitkeep" {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("non-regular frontend asset: %s", name)
		}
		data, err := fs.ReadFile(dist, name)
		if err != nil {
			return err
		}
		// Every executable contains the empty string, so an empty asset
		// would pass against any binary at all, including a stale one. It
		// cannot be shown to be embedded, and this check fails closed.
		if len(data) == 0 {
			return fmt.Errorf("empty frontend asset %s: its embedding cannot be verified", name)
		}
		if !bytes.Contains(binary, data) {
			return fmt.Errorf("executable does not contain frontend asset %s", name)
		}
		return nil
	})
}
