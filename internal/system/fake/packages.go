package fake

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/templates"
)

// PackageSystem is an in-memory pkg. It installs nothing and remembers what it
// was asked to install, so the web interface and the request collection can
// exercise deriving a template without a FreeBSD host.
//
// "nosuchpackage" always fails, because a template left unready by a failed
// install is a state worth being able to see.
type PackageSystem struct {
	mu        sync.Mutex
	installed map[string][]string
}

func NewPackageSystem() *PackageSystem {
	return &PackageSystem{installed: map[string][]string{}}
}

func (s *PackageSystem) Install(_ context.Context, root string, packages []string) (templates.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := templates.Result{
		Root:    root,
		Command: "pkg -c " + root + " install -y " + strings.Join(packages, " "),
	}
	for _, name := range packages {
		if name == "nosuchpackage" {
			// Shaped like the real failure, not just like an error. pkg exits
			// non-zero and the adapter wraps that in a system.CommandError,
			// and the exit code is what decides whether the client sees 400 or
			// 500 (JD-023). A fake that returns a bare error makes that
			// decision untestable, and silently wrong.
			stderr := fmt.Sprintf(
				"pkg: No packages available to install matching '%s' have been found in the repositories", name)
			return result, &system.CommandError{
				Command: "pkg",
				Args:    []string{"-c", root, "install", "-y"},
				Result:  system.CommandResult{Stderr: stderr, ExitCode: 70},
				Err:     errors.New("exit status 70: " + stderr),
			}
		}
	}
	s.installed[root] = append(s.installed[root], packages...)
	return result, nil
}

var _ templates.PackageSystem = (*PackageSystem)(nil)
