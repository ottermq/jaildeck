package vms

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/ottermq/jaildeck/internal/common"
)

// Service answers what vm-bhyve's guests are doing. It changes nothing
// (JD-035).
type Service struct {
	system VMSystem
}

func NewService(system VMSystem) *Service {
	return &Service{system: system}
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
	if !vmName.MatchString(name) {
		return Detail{}, common.NewAppError(http.StatusBadRequest,
			strconv.Quote(name)+" is not a vm-bhyve guest name")
	}
	listing, err := s.system.VMList(ctx)
	if err != nil {
		return Detail{}, readError("the virtual machines", err)
	}
	if listing.VMBhyve != VMBhyveReady {
		message := "vm-bhyve is not installed"
		if listing.Problem != "" {
			message = "vm-bhyve is not configured: " + listing.Problem
		}
		return Detail{}, common.NewAppError(http.StatusConflict, message)
	}
	var row *VM
	for i := range listing.VMs {
		if listing.VMs[i].Name == name {
			row = &listing.VMs[i]
			break
		}
	}
	if row == nil {
		return Detail{}, notFound(name)
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

func notFound(name string) error {
	return common.NewAppError(http.StatusNotFound,
		"vm-bhyve has no virtual machine named "+strconv.Quote(name))
}

func readError(what string, err error) error {
	return common.NewAppError(http.StatusInternalServerError,
		"could not read "+what+": "+err.Error())
}
