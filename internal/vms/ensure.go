package vms

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
)

// Ensured is what PUT /api/vms/{name} did, or found already done (JD-041).
// Created is the guest as vm-bhyve lists it, whichever way it went.
type Ensured struct {
	Created
	// Changed says whether this call changed anything on the host: true only
	// when it created the guest. Nothing here reconciles a guest towards a
	// spec (JD-016).
	Changed bool `json:"changed"`
	// Differences is every setting the request and the guest disagree
	// about. A guest that differs is left as it is.
	Differences []Difference `json:"differences,omitempty"`
}

// Difference is one thing the request asks for and the guest does not have.
// Field is the request's own field: "cpu", "network.address", "ssh_keys".
type Difference struct {
	Field string `json:"field"`
	Want  string `json:"want"`
	Have  string `json:"have"`
}

// Ensure makes PUT /api/vms/{name} idempotent: create the guest if vm-bhyve
// has none by that name, say so if the one it has is what the request
// describes, and refuse with the differences if it is not (JD-016, JD-041).
//
// What a guest was made with is read off the host -- its configuration, its
// cloud-init seed and its disk's clone origin -- so the comparison needs no
// record of its own (JD-011). **start is not compared**: it is an instruction
// about the create, not a property of the guest, and a guest stopped for
// maintenance would otherwise refuse every PUT. The answer carries its state.
func (s *Service) Ensure(ctx context.Context, name string, req CreateRequest) (Ensured, error) {
	if s.system == nil {
		return Ensured{}, s.unavailable()
	}
	switch {
	case req.Name == "":
		// The URL has already named it, so the body need not repeat it.
		req.Name = name
	case req.Name != name:
		return Ensured{}, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"the request is for a guest named %s and the URL names %s; a guest has one name",
			strconv.Quote(req.Name), strconv.Quote(name)))
	}

	row, err := s.guest(ctx, name)
	var appErr *common.AppError
	if errors.As(err, &appErr) && appErr.Code == http.StatusNotFound {
		created, err := s.Create(ctx, req)
		if err != nil {
			return Ensured{}, err
		}
		return Ensured{Created: created, Changed: true}, nil
	}
	if err != nil {
		return Ensured{}, err
	}
	return s.compare(ctx, *row, req)
}

// compare holds the guest that is there against the request, field by field,
// in the request's own order.
func (s *Service) compare(ctx context.Context, vm VM, req CreateRequest) (Ensured, error) {
	result := Ensured{Created: Created{VM: vm, Image: req.Image}}
	spec, image, err := s.checkSpec(ctx, req)
	if err != nil {
		return result, err
	}
	have, err := s.system.VMApplied(ctx, vm.Name)
	if errors.Is(err, ErrNoSuchVM) {
		return result, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s is not in vm-bhyve's default datastore, where Jail Deck makes guests, so it cannot be "+
				"compared against a request", strconv.Quote(vm.Name)))
	}
	if err != nil {
		return result, readError("what "+strconv.Quote(vm.Name)+" was made with", err)
	}
	if !have.Seed {
		return result, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s has no cloud-init seed, so it was not made from an image by Jail Deck, and there is "+
				"nothing to compare a request against", strconv.Quote(vm.Name)))
	}

	differences := make([]Difference, 0)
	differ := func(field, want, have string) {
		if want != have {
			differences = append(differences, Difference{Field: field, Want: want, Have: have})
		}
	}
	origin := have.Origin
	if origin == image.Snapshot {
		origin = req.Image
	}
	differ("image", req.Image, origin)
	differ("cpu", strconv.Itoa(spec.CPU), have.CPU)
	pinned, ok := parsePins(have.BhyveOptions)
	switch {
	case have.BhyveOptions == "" || ok:
		differ("cpuset", ints(spec.CPUSet), ints(pinned))
	default:
		// Options Jail Deck did not write: whatever they are, they are not
		// the pin the request asks for, nor its absence.
		differences = append(differences, Difference{Field: "cpuset", Want: ints(spec.CPUSet),
			Have: "bhyve_options=" + strconv.Quote(have.BhyveOptions)})
	}
	differ("memory", spec.Memory, have.Memory)
	size, _ := bytesOf(spec.Disk)
	if size != have.DiskSize {
		differences = append(differences, Difference{Field: "disk", Want: humanBytes(size), Have: humanBytes(have.DiskSize)})
	}
	differ("switch", req.Switch, strings.Join(have.Switches, ","))
	differ("network.address", spec.Address, have.Address)
	differ("network.gateway", spec.Gateway, have.Gateway)
	differ("network.nameservers", strings.Join(spec.Nameservers, ","), strings.Join(have.Nameservers, ","))
	differ("ssh_keys", strings.Join(spec.SSHKeys, "\n"), strings.Join(have.SSHKeys, "\n"))
	if d, ok := comparePassword(req.ConsolePassword, have.PasswordHash); !ok {
		differences = append(differences, d)
	}

	if len(differences) > 0 {
		result.Differences = differences
	}
	return result, nil
}

// checkSpec is check without the name being free: the request resolved and
// refused as a create would refuse it, so that a PUT naming an image that is
// gone, or a cpuset the host cannot have, says so rather than "differs".
func (s *Service) checkSpec(ctx context.Context, req CreateRequest) (CreateSpec, ImageRef, error) {
	return s.resolve(ctx, req, false)
}

// ints is a cpuset as text, "" for none.
func ints(values []int) string {
	text := make([]string, len(values))
	for i, v := range values {
		text[i] = strconv.Itoa(v)
	}
	return strings.Join(text, ",")
}
