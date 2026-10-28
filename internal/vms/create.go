package vms

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
)

// The audit log's names for what creating and destroying a guest run (JD-038).
const (
	OperationCreate      = "vm-create"
	OperationAddNetwork  = "vm-add-network"
	OperationReplaceDisk = "vm-replace-disk"
	OperationDestroy     = "vm-destroy"
)

// CreateRequest is the body of POST /api/vms (JD-038).
type CreateRequest struct {
	Name string `json:"name"`
	// Image is the VM image template the guest's disk is cloned from.
	Image string `json:"image"`
	// CPU is the number of virtual CPUs. Zero means 1.
	CPU int `json:"cpu"`
	// Memory is vm-bhyve's own form, a whole number of M or G: "512M",
	// "2G". Empty means 1G.
	Memory string `json:"memory"`
	// Disk is the guest's disk in M, G or T, at least the image's size;
	// the guest grows its root filesystem into it on first boot. Empty
	// means the image's size.
	Disk string `json:"disk"`
	// Switch is the vm-bhyve switch the guest's one NIC is on. Empty means
	// no NIC at all.
	Switch string `json:"switch"`
	// Network is a static address for that NIC. Absent means DHCP.
	Network *StaticNetwork `json:"network,omitempty"`
	// SSHKeys go into the default user's authorized_keys. A cloud image
	// has no password, so a guest with none is reachable only on its
	// console.
	SSHKeys []string `json:"ssh_keys"`
	// Start starts the guest once it is made.
	Start bool `json:"start"`
}

// StaticNetwork is the NIC's address, written into the cloud-init seed.
type StaticNetwork struct {
	// Address is the guest's, with its prefix: "10.77.0.10/24".
	Address     string   `json:"address"`
	Gateway     string   `json:"gateway,omitempty"`
	Nameservers []string `json:"nameservers,omitempty"`
}

// CreateSpec is a request with every default resolved and every value
// checked, which is all the adapter is handed.
type CreateSpec struct {
	Name   string
	CPU    int
	Memory string
	// Disk is the size vm create gives disk0; it is replaced at that size.
	Disk string
	// The netconfig vm create -n takes, empty for DHCP or for no NIC.
	Address     string
	Gateway     string
	Nameservers []string
	SSHKeys     []string
}

// Created is what POST /api/vms answers: the guest as vm-bhyve lists it.
type Created struct {
	VM
	// Image is what its disk was cloned from.
	Image string `json:"image"`
}

// Removal is what DELETE /api/vms/{name} answers.
type Removal struct {
	Name string `json:"name"`
}

const (
	defaultMemory = "1G"
	maxCPU        = 64
)

// memorySize is vm-bhyve's memory setting as Jail Deck accepts it: a whole
// number of megabytes or gigabytes.
var memorySize = regexp.MustCompile(`^[1-9][0-9]{0,6}[MG]$`)

// diskSize is a zvol size: whole megabytes, gigabytes or terabytes.
var diskSize = regexp.MustCompile(`^([1-9][0-9]{0,6})([MGT])$`)

// sshKey is one public key in authorized_keys form. The comment is narrowed
// to what a YAML list item cannot misread: vm-bhyve writes each key into
// user-data as "  - <key>", unquoted, so a comment holding ": " would turn the
// item into a mapping and cloud-init would install no key at all.
var sshKey = regexp.MustCompile(
	`^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(256|384|521)|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com) ` +
		`[A-Za-z0-9+/]+={0,2}( [A-Za-z0-9@._+-]{1,128})?$`)

// Create makes a guest from an image template (JD-038):
//
//	vm create -t jaildeck -c <cpu> -m <memory> -s <disk> -C [-k <keys>] [-n <netconfig>] <name>
//	vm add -d network -s <switch> <name>
//	zfs destroy <datastore>/<name>/disk0
//	zfs clone -o volmode=dev -o volsize=<disk> <image>@base <datastore>/<name>/disk0
//
// Every value is checked before anything runs, and a step that fails after
// vm create has made the guest destroys it again: a half-made guest would be
// one vm-bhyve can start and that boots nothing. Like the lifecycle, it is
// not cancelled by the client going away.
func (s *Service) Create(ctx context.Context, req CreateRequest) (Created, error) {
	if s.system == nil {
		return Created{}, s.unavailable()
	}
	ctx = context.WithoutCancel(ctx)

	spec, image, err := s.check(ctx, req)
	if err != nil {
		return Created{}, err
	}

	command, err := s.system.VMCreate(ctx, spec)
	s.log(ctx, OperationCreate, spec.Name, command, err)
	if err != nil {
		return Created{}, operationError("create", spec.Name, err)
	}
	if req.Switch != "" {
		command, err = s.system.VMAddNetwork(ctx, spec.Name, req.Switch)
		s.log(ctx, OperationAddNetwork, spec.Name, command, err)
		if err != nil {
			return Created{}, s.undo(ctx, spec.Name, "add its network interface", err)
		}
	}
	size, _ := bytesOf(spec.Disk)
	command, err = s.system.VMReplaceDisk(ctx, spec.Name, image.Snapshot, size)
	s.log(ctx, OperationReplaceDisk, spec.Name, command, err)
	if err != nil {
		return Created{}, s.undo(ctx, spec.Name, "clone its disk from "+req.Image, err)
	}

	if req.Start {
		if _, err := s.start(ctx, spec.Name); err != nil {
			// Made and not started: the guest is whole, so it stays, and the
			// answer says what did not happen.
			return Created{}, err
		}
	}
	vm, err := s.guest(ctx, spec.Name)
	if err != nil {
		return Created{}, err
	}
	return Created{VM: *vm, Image: req.Image}, nil
}

// undo destroys a guest whose creation stopped part way, and says which step
// it stopped at and whether the guest is gone.
func (s *Service) undo(ctx context.Context, name, step string, cause error) error {
	command, err := s.system.VMDestroy(ctx, name)
	s.log(ctx, OperationDestroy, name, command, err)
	message := fmt.Sprintf("could not %s, so %s was destroyed again: %s", step, strconv.Quote(name), summary(cause))
	if err != nil {
		message = fmt.Sprintf("could not %s, and destroying %s again failed too (%s); it is still there, half made: %s",
			step, strconv.Quote(name), summary(err), summary(cause))
	}
	return common.NewAppError(http.StatusInternalServerError, message)
}

// check resolves a request's defaults and refuses anything vm-bhyve would
// take and then get wrong, before anything runs.
func (s *Service) check(ctx context.Context, req CreateRequest) (CreateSpec, ImageRef, error) {
	bad := func(format string, args ...any) (CreateSpec, ImageRef, error) {
		return CreateSpec{}, ImageRef{}, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(format, args...))
	}
	if !vmName.MatchString(req.Name) {
		return bad("%s is not a vm-bhyve guest name: letters, digits, '.', '_' and '-', "+
			"beginning and ending with a letter or digit", strconv.Quote(req.Name))
	}
	spec := CreateSpec{Name: req.Name, CPU: req.CPU, Memory: req.Memory, Disk: req.Disk}
	if spec.CPU == 0 {
		spec.CPU = 1
	}
	if spec.CPU < 1 || spec.CPU > maxCPU {
		return bad("cpu is %d; a guest has from 1 to %d", req.CPU, maxCPU)
	}
	if spec.Memory == "" {
		spec.Memory = defaultMemory
	}
	if !memorySize.MatchString(spec.Memory) {
		return bad("memory is %s; give a whole number of M or G, as vm-bhyve does: 512M, 2G", strconv.Quote(req.Memory))
	}
	if spec.Disk != "" {
		if _, ok := bytesOf(spec.Disk); !ok {
			return bad("disk is %s; give a whole number of M, G or T: 20G", strconv.Quote(req.Disk))
		}
	}
	for i, key := range req.SSHKeys {
		if !sshKey.MatchString(strings.TrimSpace(key)) {
			return bad("ssh_keys[%d] is not one public key in authorized_keys form "+
				"(type, key, and an optional comment of letters, digits and @._+-)", i)
		}
		spec.SSHKeys = append(spec.SSHKeys, strings.TrimSpace(key))
	}
	if req.Network != nil {
		if req.Switch == "" {
			return bad("network is set and switch is not; an address needs a network interface to be on")
		}
		if err := resolveNetwork(&spec, *req.Network); err != nil {
			return CreateSpec{}, ImageRef{}, err
		}
	}

	if s.images == nil {
		return CreateSpec{}, ImageRef{}, common.NewAppError(http.StatusNotImplemented,
			"this Jail Deck has no VM images to create a guest from")
	}
	image, err := s.images.Image(ctx, req.Image)
	if errors.Is(err, ErrNoSuchImage) {
		return bad("there is no image named %s; prepare one first", strconv.Quote(req.Image))
	}
	if err != nil {
		return CreateSpec{}, ImageRef{}, readError("the image "+strconv.Quote(req.Image), err)
	}
	if !image.Ready {
		return CreateSpec{}, ImageRef{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"the image %s is not ready; a guest is cloned from its @base snapshot", strconv.Quote(req.Image)))
	}
	if spec.Disk == "" {
		spec.Disk = strconv.FormatInt(image.Size, 10)
	} else if size, _ := bytesOf(spec.Disk); size < image.Size {
		return bad("disk is %s and the image's is %s; a guest's disk cannot be smaller than its image",
			spec.Disk, humanBytes(image.Size))
	}

	listing, err := s.system.VMList(ctx)
	if err != nil {
		return CreateSpec{}, ImageRef{}, readError("the virtual machines", err)
	}
	if listing.VMBhyve != VMBhyveReady {
		why := "it is not installed"
		if listing.Problem != "" {
			why = listing.Problem
		}
		return CreateSpec{}, ImageRef{}, common.NewAppError(http.StatusConflict, "vm-bhyve is not ready: "+why)
	}
	if slices.ContainsFunc(listing.VMs, func(vm VM) bool { return vm.Name == req.Name }) {
		return CreateSpec{}, ImageRef{}, common.NewAppError(http.StatusConflict,
			"vm-bhyve already has a guest named "+strconv.Quote(req.Name))
	}
	if req.Switch != "" {
		switches, err := s.system.VMSwitches(ctx)
		if err != nil {
			return CreateSpec{}, ImageRef{}, readError("vm-bhyve's switches", err)
		}
		if !slices.Contains(switches, req.Switch) {
			known := "it has none"
			if len(switches) > 0 {
				known = "it has " + strings.Join(switches, ", ")
			}
			return bad("vm-bhyve has no switch named %s (%s); switches are the operator's, made with vm switch create",
				strconv.Quote(req.Switch), known)
		}
	}
	return spec, image, nil
}

// resolveNetwork checks a static address and writes it into the spec in the
// form vm-bhyve's netconfig takes. Every value is parsed as an address, which
// is also what keeps a ';' -- netconfig's separator -- out of it.
func resolveNetwork(spec *CreateSpec, network StaticNetwork) error {
	bad := func(format string, args ...any) error {
		return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(format, args...))
	}
	prefix, err := netip.ParsePrefix(network.Address)
	if err != nil {
		return bad("network.address is %s; give the guest's address with its prefix: 10.0.0.5/24", strconv.Quote(network.Address))
	}
	spec.Address = prefix.String()
	if network.Gateway != "" {
		gateway, err := netip.ParseAddr(network.Gateway)
		if err != nil {
			return bad("network.gateway is %s, which is not an address", strconv.Quote(network.Gateway))
		}
		if gateway.Is4() != prefix.Addr().Is4() {
			return bad("network.gateway %s and network.address %s are not the same IP version", gateway, prefix)
		}
		spec.Gateway = gateway.String()
	}
	for i, ns := range network.Nameservers {
		addr, err := netip.ParseAddr(ns)
		if err != nil {
			return bad("network.nameservers[%d] is %s, which is not an address", i, strconv.Quote(ns))
		}
		spec.Nameservers = append(spec.Nameservers, addr.String())
	}
	return nil
}

// bytesOf reads a size in Jail Deck's accepted form, or a plain number of
// bytes, which is how an image's own size is passed on.
func bytesOf(size string) (int64, bool) {
	if n, err := strconv.ParseInt(size, 10, 64); err == nil && n > 0 {
		return n, true
	}
	m := diskSize.FindStringSubmatch(size)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	shift := map[string]uint{"M": 20, "G": 30, "T": 40}[m[2]]
	return n << shift, true
}

// summary is an error in the fewest words: a command's own message when it was
// a command that failed.
func summary(err error) string {
	var commandErr *system.CommandError
	if errors.As(err, &commandErr) {
		return commandErr.Summary()
	}
	return err.Error()
}

func humanBytes(n int64) string {
	switch {
	case n%(1<<30) == 0:
		return fmt.Sprintf("%dG", n>>30)
	case n%(1<<20) == 0:
		return fmt.Sprintf("%dM", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}

// Destroy removes a stopped guest with `vm destroy -f`: its dataset, the
// disk cloned from its image with it, and its configuration (JD-038). A
// running guest is refused, as vm-bhyve refuses it; stopping is a separate
// decision with its own deadline (JD-036). confirm is the guest's name again.
func (s *Service) Destroy(ctx context.Context, name, confirm string) (Removal, error) {
	if s.system == nil {
		return Removal{}, s.unavailable()
	}
	ctx = context.WithoutCancel(ctx)
	vm, err := s.settled(ctx, name)
	if err != nil {
		return Removal{}, err
	}
	if confirm != name {
		return Removal{}, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"destroying a guest needs confirm=%s: its disk goes with it", name))
	}
	switch vm.State {
	case StateLocked:
		return Removal{}, s.lockError(vm)
	case StateRunning, StateBootloader:
		return Removal{}, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s is %s; stop it first", strconv.Quote(name), vm.State))
	}
	command, err := s.system.VMDestroy(ctx, name)
	s.log(ctx, OperationDestroy, name, command, err)
	if err != nil {
		return Removal{}, operationError("destroy", name, err)
	}
	return Removal{Name: name}, nil
}
