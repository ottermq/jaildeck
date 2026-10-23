package templates

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
)

const OperationInstallPackages = "install-packages"

type Service struct {
	storage         TemplateStorage
	packages        PackageSystem
	operationLogger audit.Logger
}

func NewService(storage TemplateStorage, packages PackageSystem, operationLogger audit.Logger) *Service {
	return &Service{storage: storage, packages: packages, operationLogger: operationLogger}
}

func (s *Service) List(ctx context.Context) ([]Template, error) {
	return s.storage.Templates(ctx)
}

// Derive builds a template from another one plus packages: clone the parent's
// @base, install into the clone, snapshot it as @base (#7, JD-027).
//
// Idempotent in the sense that matters: a template that already exists and is
// ready is left alone and reported, so a client that timed out can repeat the
// call (JD-016). One that exists and is NOT ready is a previous attempt that
// failed, and finishing it silently would install packages on top of whatever
// that attempt left behind -- so it is refused, by name, for the operator to
// destroy or inspect.
func (s *Service) Derive(ctx context.Context, name, parent string, packages []string) (Template, bool, error) {
	if err := checkTemplateName(name); err != nil {
		return Template{}, false, err
	}
	if err := checkPackages(packages); err != nil {
		return Template{}, false, err
	}

	existing, err := s.List(ctx)
	if err != nil {
		return Template{}, false, err
	}
	if found, ok := byName(existing, name); ok {
		if !found.Ready {
			return Template{}, false, common.NewAppError(http.StatusConflict, fmt.Sprintf(
				"%s exists but has no @%s snapshot, which is what a failed derive leaves behind; "+
					"destroy it or finish it by hand rather than building on top of it", name, ReadySnapshot))
		}
		return found, false, nil
	}

	from, ok := byName(existing, parent)
	if !ok {
		return Template{}, false, common.NewAppError(http.StatusNotFound, "no template named "+parent)
	}
	if !from.Ready {
		return Template{}, false, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s has no @%s snapshot, so it is not usable as a parent (JD-007)", parent, ReadySnapshot))
	}

	root, err := s.storage.Derive(ctx, from.Snapshot, name)
	if err != nil {
		return Template{}, false, err
	}

	if len(packages) > 0 {
		result, installErr := s.packages.Install(ctx, root, packages)
		s.logInstall(ctx, result, installErr)
		if installErr != nil {
			// The clone stays, without its @base. It shows up in the listing
			// as a template that is not ready, which is exactly what it is.
			return Template{}, false, toInstallError(name, installErr)
		}
	}

	if err := s.storage.Seal(ctx, name); err != nil {
		return Template{}, false, err
	}

	after, err := s.List(ctx)
	if err != nil {
		return Template{}, false, err
	}
	derived, ok := byName(after, name)
	if !ok {
		return Template{}, false, common.NewAppError(http.StatusInternalServerError,
			"the template was built but is not in the listing: "+name)
	}
	return derived, true, nil
}

func byName(templates []Template, name string) (Template, bool) {
	for _, template := range templates {
		if template.Name == name {
			return template, true
		}
	}
	return Template{}, false
}

// checkTemplateName allows what ZFS allows in a dataset component, which is
// also what a release name needs: 15.1-RELEASE, postgres-16.
func checkTemplateName(name string) error {
	switch {
	case name == "":
		return common.NewAppError(http.StatusBadRequest, "a template name cannot be empty")
	case name == "." || name == "..":
		return common.NewAppError(http.StatusBadRequest, fmt.Sprintf("%q is not a template name", name))
	case !isAlphanumeric(name[0]):
		return common.NewAppError(http.StatusBadRequest,
			fmt.Sprintf("template name %q must begin with a letter or a digit", name))
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if isAlphanumeric(c) || c == '_' || c == '-' || c == ':' || c == '.' {
			continue
		}
		return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"template name %q contains a character ZFS does not allow at offset %d", name, i))
	}
	return nil
}

// checkPackages refuses anything that is not a package name.
//
// No shell is involved -- CommandRunner takes an argument array -- so this is
// not about quoting. It is about flags: an argument beginning with a dash
// would be read by pkg as an option rather than as a package, and
// "pkg install -y -f" is a different command from the one that was asked for.
func checkPackages(packages []string) error {
	for _, name := range packages {
		switch {
		case name == "":
			return common.NewAppError(http.StatusBadRequest, "a package name cannot be empty")
		case strings.HasPrefix(name, "-"):
			return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
				"%q begins with a dash, which pkg would read as an option rather than a package", name))
		case strings.ContainsAny(name, " \t\n\r"):
			return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
				"%q is not a package name: it contains whitespace", name))
		}
	}
	if slices.Contains(packages, "") {
		return common.NewAppError(http.StatusBadRequest, "a package name cannot be empty")
	}
	return nil
}

func isAlphanumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func (s *Service) logInstall(ctx context.Context, result Result, opErr error) {
	entry := audit.Entry{
		Timestamp: time.Now(),
		Operation: OperationInstallPackages,
		Target:    result.Root,
		Command:   result.Command,
		Success:   opErr == nil,
	}
	if opErr != nil {
		entry.TimedOut = errors.Is(opErr, context.DeadlineExceeded)
		var cmdErr *system.CommandError
		if errors.As(opErr, &cmdErr) {
			entry.ExitCode = cmdErr.Result.ExitCode
			entry.Error = cmdErr.Unwrap().Error()
		} else {
			entry.Error = opErr.Error()
			entry.ExitCode = -1
		}
	}
	if err := s.operationLogger.Log(ctx, entry); err != nil {
		common.Logger(ctx, "templates").Error("failed to write operation log",
			slog.String("target", result.Root), slog.Any("err", err))
	}
}

// toInstallError carries pkg's own message, which says precisely which package
// does not exist or which repository could not be reached.
func toInstallError(name string, err error) error {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return common.NewAppError(http.StatusGatewayTimeout,
			"installing packages into "+name+" timed out; the template exists and is not ready")
	}
	message := err.Error()
	var cmdErr *system.CommandError
	if errors.As(err, &cmdErr) {
		message = cmdErr.Summary()
		if cmdErr.Result.ExitCode > 0 {
			// pkg ran and refused: a package that does not exist is the
			// request's fault, and a client told 500 would retry it forever.
			return common.NewAppError(http.StatusBadRequest,
				"installing packages into "+name+" failed: "+message+
					"; the template exists and is not ready")
		}
	}
	return common.NewAppError(http.StatusInternalServerError,
		"installing packages into "+name+" failed: "+message+"; the template exists and is not ready")
}
