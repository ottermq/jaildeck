package images

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
)

type Service struct {
	system ImageSystem
	audit  audit.Logger
	// running is what this process is doing right now, the one part of an
	// image's state that cannot be read off the disk (JD-008).
	running *operations
}

func NewService(system ImageSystem, auditLogger audit.Logger) *Service {
	return &Service{system: system, audit: auditLogger, running: newOperations()}
}

// imageName is what an image can be called: it becomes a dataset name and a
// URL segment, and neither should be built from whatever arrived. A leading
// dot is excluded with the rest, so an image is never hidden.
var imageName = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,62}[a-z0-9])?$`)

func checkName(name string) error {
	if !imageName.MatchString(name) {
		return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"%q is not an image name: lower-case letters, digits, '.', '_' and '-', "+
				"starting and ending with a letter or digit, at most 64", name))
	}
	return nil
}

// List reports every image, including one that is only work in flight: the
// resource a client was told to watch has to exist from the moment the work
// starts (JD-008).
func (s *Service) List(ctx context.Context) (Listing, error) {
	listing, _, err := s.list(ctx)
	return listing, err
}

// list is List with the store it read, which Prepare and Delete go on to use.
func (s *Service) list(ctx context.Context) (Listing, Store, error) {
	store, err := s.system.Store(ctx)
	if err != nil {
		return Listing{}, Store{}, err
	}
	listing := Listing{
		VMBhyve: store.VMBhyve,
		Problem: store.Problem,
		Dataset: store.Dataset,
		Images:  make([]Image, 0),
		Sources: s.sources(ctx, store),
	}
	if !store.Usable() {
		return listing, store, nil
	}

	found, err := s.system.Images(ctx, store)
	if err != nil {
		return Listing{}, Store{}, err
	}
	for i := range found {
		found[i].State = StateWritten
		if found[i].Ready {
			found[i].State = StateReady
		}
		if running, ok := s.running.of(found[i].Name); ok {
			found[i].State = running.State
			found[i].Running = &running
		}
	}
	// Work with no zvol yet: an archive being fetched.
	for _, name := range s.running.names() {
		if _, ok := byName(found, name); ok {
			continue
		}
		running, ok := s.running.of(name)
		if !ok {
			continue // finished between the two reads
		}
		found = append(found, Image{Name: name, Source: running.Source, State: running.State,
			Running: &running, Guests: make([]string, 0)})
	}
	slices.SortFunc(found, func(a, b Image) int { return strings.Compare(a.Name, b.Name) })
	listing.Images = found
	return listing, store, nil
}

// sources is the catalog with what is already on the host. A failure to look
// costs only the fetched flags, which a prepare finds out again for itself.
func (s *Service) sources(ctx context.Context, store Store) []Source {
	catalog := slices.Clone(s.system.Sources())
	if !store.Usable() {
		return catalog
	}
	fetched, err := s.system.Fetched(ctx, store)
	if err != nil {
		common.Logger(ctx, "images").Warn("could not read the image archives", slog.Any("err", err))
		return catalog
	}
	for i := range catalog {
		catalog[i].Fetched = slices.Contains(fetched, catalog[i].Name)
	}
	return catalog
}

// Get is one image from the listing.
func (s *Service) Get(ctx context.Context, name string) (Image, error) {
	if err := checkName(name); err != nil {
		return Image{}, err
	}
	listing, err := s.List(ctx)
	if err != nil {
		return Image{}, err
	}
	if image, ok := byName(listing.Images, name); ok {
		return image, nil
	}
	return Image{}, common.NewAppError(http.StatusNotFound, "no image named "+strconv.Quote(name))
}

// Prepare starts building an image from a source and returns at once (JD-008,
// JD-037). It reports whether it started anything: an image that is already
// there and ready is the answer to a client that timed out and asked again,
// not a conflict (JD-016).
//
// An empty source is the image's own name, when that names a source:
// PUT /api/vm-images/debian-13 with no body is the obvious thing to want.
func (s *Service) Prepare(ctx context.Context, name, source string) (Image, bool, error) {
	if err := checkName(name); err != nil {
		return Image{}, false, err
	}
	catalog := s.system.Sources()
	if source == "" && slices.ContainsFunc(catalog, func(c Source) bool { return c.Name == name }) {
		source = name
	}
	if source == "" {
		return Image{}, false, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"%s is not a source, so say which one to prepare it from: %s", name, sourceNames(catalog)))
	}
	if !slices.ContainsFunc(catalog, func(c Source) bool { return c.Name == source }) {
		return Image{}, false, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"%s is not a source Jail Deck knows; it can prepare %s", source, sourceNames(catalog)))
	}

	listing, store, err := s.list(ctx)
	if err != nil {
		return Image{}, false, err
	}
	if !store.Usable() {
		return Image{}, false, common.NewAppError(http.StatusConflict,
			"images cannot be prepared on this host: "+listing.Problem)
	}
	if current, ok := byName(listing.Images, name); ok {
		switch {
		case current.Running != nil:
			return Image{}, false, common.NewAppError(http.StatusConflict, fmt.Sprintf(
				"%s is already being prepared by this process; watch it rather than starting it again", name))
		case current.Source != source:
			return Image{}, false, common.NewAppError(http.StatusConflict, fmt.Sprintf(
				"%s already exists, written from %s, not %s", name, sourceOrNothing(current.Source), source))
		case current.Ready:
			return current, false, nil
		default:
			// A zvol with no @base is a write or a seal that stopped. Writing
			// over it would make a disk nothing can vouch for.
			return Image{}, false, common.NewAppError(http.StatusConflict, fmt.Sprintf(
				"%s already has a zvol and no @%s snapshot, which is what a write that stopped "+
					"leaves behind; remove it and prepare it again -- the archive is kept, so only "+
					"the write is repeated", name, ReadySnapshot))
		}
	}
	fetched := slices.ContainsFunc(listing.Sources, func(c Source) bool { return c.Name == source && c.Fetched })

	// start is the claim, so two requests arriving together have one refused
	// rather than both writing the same zvol.
	first := Running{State: StateFetching, Source: source, Step: "fetching " + source}
	if fetched {
		first = Running{State: StateWriting, Source: source, Step: "writing " + source + " into " + name}
	}
	if !s.running.start(name, first) {
		return Image{}, false, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s is already being prepared by this process; watch it rather than starting it again", name))
	}

	// Detached from the request on purpose: the client is told 202 and goes
	// away, and the work must not stop when it does (JD-014).
	work := context.WithoutCancel(ctx)
	go s.prepare(work, store, name, source, fetched)

	image, err := s.Get(work, name)
	if err != nil {
		return Image{}, false, err
	}
	return image, true, nil
}

// prepare runs the stages, each audited as it goes. A stage that fails leaves
// what it left: the next GET reads the disk, and the audit log says why.
func (s *Service) prepare(ctx context.Context, store Store, name, source string, fetched bool) {
	defer s.running.stop(name)

	if !fetched {
		command, err := s.system.Fetch(ctx, store, source)
		s.log(ctx, OperationFetch, name, command, err)
		if err != nil {
			return
		}
	}
	s.running.step(name, StateWriting, "writing "+source+" into "+name)
	command, err := s.system.Write(ctx, store, name, source)
	s.log(ctx, OperationWrite, name, command, err)
	if err != nil {
		return
	}
	s.running.step(name, StateWriting, "snapshotting "+name+"@"+ReadySnapshot)
	command, err = s.system.Seal(ctx, store, name)
	s.log(ctx, OperationSeal, name, command, err)
}

// Delete removes an image, refusing while any guest's disk is cloned from it.
// ZFS would refuse too; this says so in terms of guests rather than datasets.
// confirm is the image's name again, as a template's removal asks for.
func (s *Service) Delete(ctx context.Context, name, confirm string) (Removal, error) {
	if err := checkName(name); err != nil {
		return Removal{}, err
	}
	if confirm != name {
		return Removal{}, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"removing an image needs confirm=%s: it is a download and a write to make again", name))
	}
	listing, store, err := s.list(ctx)
	if err != nil {
		return Removal{}, err
	}
	image, ok := byName(listing.Images, name)
	if !ok {
		return Removal{}, common.NewAppError(http.StatusNotFound, "no image named "+strconv.Quote(name))
	}
	if image.Running != nil {
		return Removal{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s is being prepared right now; remove it once that has finished or failed", name))
	}
	if len(image.Guests) > 0 {
		return Removal{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s is cloned for the disk of %s; destroy the guests first", name, strings.Join(image.Guests, ", ")))
	}
	command, err := s.system.Destroy(ctx, store, name)
	s.log(ctx, OperationDestroy, name, command, err)
	if err != nil {
		return Removal{}, operationError("remove", name, err)
	}
	return Removal{Name: name, Dataset: image.Dataset}, nil
}

func (s *Service) log(ctx context.Context, operation, target, command string, opErr error) {
	if s.audit == nil {
		return
	}
	entry := audit.Entry{
		Timestamp: time.Now(),
		Operation: operation,
		Target:    target,
		Command:   command,
		Success:   opErr == nil,
	}
	if opErr != nil {
		entry.TimedOut = errors.Is(opErr, context.DeadlineExceeded)
		entry.ExitCode = -1
		entry.Error = opErr.Error()
		var commandErr *system.CommandError
		if errors.As(opErr, &commandErr) {
			entry.ExitCode = commandErr.Result.ExitCode
			entry.Error = commandErr.Unwrap().Error()
		}
	}
	if err := s.audit.Log(ctx, entry); err != nil {
		common.Logger(ctx, "images").Error("failed to write operation log",
			slog.String("operation", operation), slog.String("target", target), slog.Any("err", err))
	}
}

func operationError(verb, name string, err error) error {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	message := err.Error()
	var commandErr *system.CommandError
	if errors.As(err, &commandErr) {
		message = commandErr.Summary()
	}
	return common.NewAppError(http.StatusInternalServerError,
		fmt.Sprintf("could not %s %s: %s", verb, strconv.Quote(name), message))
}

func byName(all []Image, name string) (Image, bool) {
	for _, image := range all {
		if image.Name == name {
			return image, true
		}
	}
	return Image{}, false
}

func sourceNames(catalog []Source) string {
	names := make([]string, 0, len(catalog))
	for _, c := range catalog {
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}

func sourceOrNothing(source string) string {
	if source == "" {
		return "no source"
	}
	return source
}
