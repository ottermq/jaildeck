package vms

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
)

// Service answers what vm-bhyve's guests are doing (JD-035), and starts and
// stops them (JD-036).
type Service struct {
	system VMSystem
	// images is where a created guest's disk comes from (JD-038).
	images ImageCatalog
	audit  audit.Logger
	// wait is how the lifecycle waits between looks at vm-bhyve's listing.
	// A field so that the tests do not spend a minute watching a fake.
	wait func(context.Context, time.Duration)
}

func NewService(system VMSystem, images ImageCatalog, auditLogger audit.Logger) *Service {
	return &Service{system: system, images: images, audit: auditLogger, wait: sleep}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

func (s *Service) unavailable() error {
	return common.NewAppError(http.StatusNotImplemented,
		"this Jail Deck is running without the host access it needs to read virtual machines")
}

// List reads every guest vm-bhyve knows, with its state.
//
// vm-bhyve not installed, or not configured, is the whole answer and a 200:
// it is the common case, and Host Setup is where it is explained.
func (s *Service) List(ctx context.Context) (Listing, error) {
	if s.system == nil {
		return Listing{}, s.unavailable()
	}
	listing, err := s.system.VMList(ctx)
	if err != nil {
		return Listing{}, readError("the virtual machines", err)
	}
	if listing.VMs == nil {
		listing.VMs = make([]VM, 0)
	}
	return listing, nil
}

// vmName is vm-bhyve's own rule for a guest's name, from util::check_name:
// letters, digits, '.', '_' and '-', beginning and ending with a letter or a
// digit, at most 231 characters on FreeBSD 13 and later.
var vmName = regexp.MustCompile(`^(?i)[a-z0-9][.a-z0-9_-]{0,229}[a-z0-9]$`)

// Get reads one guest: its row in the listing, and what `vm info` adds.
//
// The name must be one the listing has. That makes "no such guest" a 404
// decided by Jail Deck, and it is where autostart comes from: `vm info` does
// not say.
func (s *Service) Get(ctx context.Context, name string) (Detail, error) {
	if s.system == nil {
		return Detail{}, s.unavailable()
	}
	row, err := s.guest(ctx, name)
	if err != nil {
		return Detail{}, err
	}

	info, err := s.system.VMInfo(ctx, name)
	if errors.Is(err, ErrNoSuchVM) {
		// Destroyed between the two reads.
		return Detail{}, notFound(name)
	}
	if err != nil {
		return Detail{}, readError("virtual machine "+strconv.Quote(name), err)
	}
	detail := Detail{
		VM:             *row,
		UUID:           info.UUID,
		MemoryResident: info.MemoryResident,
		Consoles:       info.Consoles,
		Networks:       info.Networks,
		Disks:          info.Disks,
	}
	if detail.Networks == nil {
		detail.Networks = make([]Network, 0)
	}
	if detail.Disks == nil {
		detail.Disks = make([]Disk, 0)
	}
	return detail, nil
}

// guest is one guest's row, by a name that has to be one vm-bhyve could have
// made and one its listing has.
func (s *Service) guest(ctx context.Context, name string) (*VM, error) {
	if !vmName.MatchString(name) {
		return nil, common.NewAppError(http.StatusBadRequest,
			strconv.Quote(name)+" is not a vm-bhyve guest name")
	}
	listing, err := s.system.VMList(ctx)
	if err != nil {
		return nil, readError("the virtual machines", err)
	}
	if listing.VMBhyve != VMBhyveReady {
		message := "vm-bhyve is not installed"
		if listing.Problem != "" {
			message = "vm-bhyve is not configured: " + listing.Problem
		}
		return nil, common.NewAppError(http.StatusConflict, message)
	}
	for i := range listing.VMs {
		if listing.VMs[i].Name == name {
			return &listing.VMs[i], nil
		}
	}
	return nil, notFound(name)
}

func notFound(name string) error {
	return common.NewAppError(http.StatusNotFound,
		"vm-bhyve has no virtual machine named "+strconv.Quote(name))
}

func readError(what string, err error) error {
	return common.NewAppError(http.StatusInternalServerError,
		"could not read "+what+": "+err.Error())
}
