package jails

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ottermq/jaildeck/internal/common"
)

type JailHandler struct {
	service *JailService
}

func NewJailHandler(jailService *JailService) *JailHandler {
	return &JailHandler{
		service: jailService,
	}
}

// List returns every jail this host knows about.
//
// @Summary      List jails
// @Description  Running jails from jls merged with configured ones from jail.conf and
// @Description  jail.conf.d, so a stopped jail is listed too — seeing it is how you start
// @Description  it (JD-004).
// @Description
// @Description  Each jail carries its ZFS storage: datasets mounted at its path or under
// @Description  it, and the user properties of its own dataset. Sizes are bytes, not the
// @Description  1.41G zfs prints, so they can be compared and totalled.
// @Description
// @Description  Each dataset also carries the native properties it sets **locally**, out of
// @Description  the four an existing dataset may be given (JD-023) — as the strings zfs
// @Description  prints, so `refquota` reads `2G` rather than a byte count, and a client can
// @Description  send the same value back. One a dataset merely *inherits* is absent: that
// @Description  value is the parent's, and writing it here would create a local override
// @Description  instead of changing what is in effect.
// @Description
// @Description  Both keys are absent for a jail with no dataset — and also when the pool
// @Description  could not be read, which is logged at WARN rather than failing the
// @Description  request, so an unreadable pool never costs you the list (JD-022).
// @Tags         jails
// @Produce      json
// @Success      200  {array}   Jail
// @Failure      401  {object}  common.ErrorResponse
// @Security     BearerAuth
// @Router       /jails [get]
func (h *JailHandler) List(w http.ResponseWriter, r *http.Request) {
	jails, err := h.service.List(r.Context())
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, jails)
}

// Get returns one jail by name.
//
// @Summary      Show one jail
// @Description  The same shape as an element of the listing, storage included. It reads
// @Description  that same merged listing rather than asking for a single jail, because a
// @Description  jail present only in jls or only in configuration must still be found.
// @Tags         jails
// @Produce      json
// @Param        name  path      string  true  "Jail name"
// @Success      200   {object}  Jail
// @Failure      400   {object}  common.ErrorResponse  "Not a jail name"
// @Failure      401   {object}  common.ErrorResponse
// @Failure      404   {object}  common.ErrorResponse  "No such jail"
// @Security     BearerAuth
// @Router       /jails/{name} [get]
func (h *JailHandler) Get(w http.ResponseWriter, r *http.Request) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	jail, err := h.service.Get(r.Context(), name)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, jail)
}

func (h *JailHandler) do(w http.ResponseWriter, r *http.Request, call func(context.Context, JailName) (OperationResult, error)) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}

	result, err := call(r.Context(), name)

	if err != nil {
		common.HandlerError(w, err)
		return
	}

	common.WriteJSON(w, http.StatusOK, result.Jail)

}

// Start start the jail.
//
// @Summary      Start a jail
// @Description  Runs service jail start.
// @Description  A client disconnecting does not abort the operation: the command is
// @Description  detached from request cancellation and still bounded by its own timeout
// @Description  (JD-014). Audited either way.
// @Tags         jails
// @Produce      json
// @Param        name  path      string  true  "Jail name"
// @Success      200   {object}  Jail
// @Failure      400   {object}  common.ErrorResponse  "Not a jail name"
// @Failure      401   {object}  common.ErrorResponse
// @Failure      404   {object}  common.ErrorResponse  "No such jail"
// @Failure      504   {object}  common.ErrorResponse  "Timed out and may still be running"
// @Security     BearerAuth
// @Router       /jails/{name}/start [post]
func (h *JailHandler) Start(w http.ResponseWriter, r *http.Request) {
	h.do(w, r, h.service.Start)
}

// Stop stop the jail.
//
// @Summary      Stop a jail
// @Description  Runs service jail stop. Anything running inside is interrupted.
// @Description  A client disconnecting does not abort the operation: the command is
// @Description  detached from request cancellation and still bounded by its own timeout
// @Description  (JD-014). Audited either way.
// @Tags         jails
// @Produce      json
// @Param        name  path      string  true  "Jail name"
// @Success      200   {object}  Jail
// @Failure      400   {object}  common.ErrorResponse  "Not a jail name"
// @Failure      401   {object}  common.ErrorResponse
// @Failure      404   {object}  common.ErrorResponse  "No such jail"
// @Failure      504   {object}  common.ErrorResponse  "Timed out and may still be running"
// @Security     BearerAuth
// @Router       /jails/{name}/stop [post]
func (h *JailHandler) Stop(w http.ResponseWriter, r *http.Request) {
	h.do(w, r, h.service.Stop)
}

// Restart restart the jail.
//
// @Summary      Restart a jail
// @Description  Runs service jail restart.
// @Description  A client disconnecting does not abort the operation: the command is
// @Description  detached from request cancellation and still bounded by its own timeout
// @Description  (JD-014). Audited either way.
// @Tags         jails
// @Produce      json
// @Param        name  path      string  true  "Jail name"
// @Success      200   {object}  Jail
// @Failure      400   {object}  common.ErrorResponse  "Not a jail name"
// @Failure      401   {object}  common.ErrorResponse
// @Failure      404   {object}  common.ErrorResponse  "No such jail"
// @Failure      504   {object}  common.ErrorResponse  "Timed out and may still be running"
// @Security     BearerAuth
// @Router       /jails/{name}/restart [post]
func (h *JailHandler) Restart(w http.ResponseWriter, r *http.Request) {
	h.do(w, r, h.service.Restart)
}

// ensureDatasetRequest is the body of PUT /api/jails/{name}/datasets/{child}.
// An object rather than the bare map so the request has somewhere to grow --
// a quota today, a source snapshot when #6 arrives.
type ensureDatasetRequest struct {
	Properties map[string]string `json:"properties"`
}

// EnsureDataset creates a child dataset or converges an existing one.
//
// @Summary      Create or update a child dataset
// @Description  201 when it was created, 200 when it was already there, 409 when it
// @Description  exists at a different mountpoint. Idempotent, so a client that timed out
// @Description  repeats the call and the answer says where it stands (JD-016).
// @Description
// @Description  Five native properties may be set — mountpoint, refquota, recordsize,
// @Description  compression, atime — out of the sixty-odd ZFS calls editable. Anything
// @Description  else is 400 naming the list: sharenfs would export the jail's data and
// @Description  canmount would stop it mounting. quota is deliberately absent, because
// @Description  JD-007 puts the limit on refquota.
// @Description
// @Description  mountpoint may be set only at creation; asking for one an existing dataset
// @Description  does not have is a 409 with both values rather than a move (JD-023).
// @Description
// @Description  Values are not validated here. zfs answers recordsize=7K with "must be
// @Description  power of 2 from 512B to 16M", and that sentence comes back — as a 400, so
// @Description  a client does not retry a value that can never work.
// @Tags         storage
// @Accept       json
// @Produce      json
// @Param        name        path      string                true  "Jail name"
// @Param        child       path      string                true  "Dataset name, one level"
// @Param        properties  body      ensureDatasetRequest  true  "Native ZFS properties"
// @Success      200         {object}  DatasetChange  "Already there"
// @Success      201         {object}  DatasetChange  "Created"
// @Failure      400         {object}  common.ErrorResponse  "A property outside the allowlist, or a value zfs refused"
// @Failure      401         {object}  common.ErrorResponse
// @Failure      404         {object}  common.ErrorResponse  "No such jail, or it is not on ZFS"
// @Failure      409         {object}  DatasetChange         "It exists at a different mountpoint"
// @Security     BearerAuth
// @Router       /jails/{name}/datasets/{child} [put]
func (h *JailHandler) EnsureDataset(w http.ResponseWriter, r *http.Request) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	var body ensureDatasetRequest
	if err := decodeJSON(r, &body); err != nil {
		common.HandlerError(w, err)
		return
	}

	change, err := h.service.EnsureDataset(r.Context(), name, chi.URLParam(r, "child"), body.Properties)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	switch {
	case change.Conflict != nil:
		common.WriteJSON(w, http.StatusConflict, change)
	case change.Created:
		common.WriteJSON(w, http.StatusCreated, change)
	default:
		common.WriteJSON(w, http.StatusOK, change)
	}
}

// SetProperties tags the jail's own dataset. The body is the property map
// itself: a null value removes that property rather than setting it empty,
// which is the difference between "this jail has no instance" and "this jail's
// instance is the empty string".
// SetProperties tags the jail's own dataset.
//
// @Summary      Tag the jail
// @Description  Sets ZFS user properties on the jail's own dataset — the tags the listing
// @Description  reads back (JD-004). Returns what the dataset carries afterwards, read
// @Description  from ZFS rather than assumed.
// @Description
// @Description  PATCH, not PUT: these merge. A replacing write would strip a tag another
// @Description  tool had set, and zrepl:placeholder and com.sun:auto-snapshot both live in
// @Description  this namespace on real hosts.
// @Description
// @Description  A null value removes the property via zfs inherit, which is not the same
// @Description  as setting it empty. Only user properties — the name must contain a colon
// @Description  — and the value is passed through untouched (JD-011).
// @Tags         storage
// @Accept       json
// @Produce      json
// @Param        name        path      string             true  "Jail name"
// @Param        properties  body      map[string]string  true  "Property names to values; null removes"
// @Success      200         {object}  map[string]string  "The dataset's user properties now"
// @Failure      400         {object}  common.ErrorResponse  "A name that is not a user property"
// @Failure      401         {object}  common.ErrorResponse
// @Failure      404         {object}  common.ErrorResponse  "No such jail, or it is not on ZFS"
// @Security     BearerAuth
// @Router       /jails/{name}/properties [patch]
func (h *JailHandler) SetProperties(w http.ResponseWriter, r *http.Request) {
	h.setProperties(w, r, "")
}

// SetDatasetProperties does the same for a dataset beneath the jail's own.
// SetDatasetProperties tags one of the jail's child datasets.
//
// @Summary      Tag a child dataset
// @Description  The same as tagging the jail, on a dataset beneath its own.
// @Description
// @Description  Worth keeping separate: a jail's listing reports its own dataset's
// @Description  properties, never a child's. Tag a child with otter:instance and it will
// @Description  not appear on the jail — by design, or one jail with three children would
// @Description  look like four instances to a client searching by tag (JD-022).
// @Tags         storage
// @Accept       json
// @Produce      json
// @Param        name        path      string             true  "Jail name"
// @Param        child       path      string             true  "Dataset name"
// @Param        properties  body      map[string]string  true  "Property names to values; null removes"
// @Success      200         {object}  map[string]string
// @Failure      400         {object}  common.ErrorResponse
// @Failure      401         {object}  common.ErrorResponse
// @Failure      404         {object}  common.ErrorResponse
// @Security     BearerAuth
// @Router       /jails/{name}/datasets/{child}/properties [patch]
func (h *JailHandler) SetDatasetProperties(w http.ResponseWriter, r *http.Request) {
	h.setProperties(w, r, chi.URLParam(r, "child"))
}

func (h *JailHandler) setProperties(w http.ResponseWriter, r *http.Request, child string) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	var properties map[string]*string
	if err := decodeJSON(r, &properties); err != nil {
		common.HandlerError(w, err)
		return
	}

	found, err := h.service.SetUserProperties(r.Context(), name, child, properties)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, found)
}

// decodeJSON reads a request body, refusing a field the target does not have.
// A client that misspells "properties" should hear about it rather than have
// its request silently do nothing.
func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return common.NewAppError(http.StatusBadRequest, "could not read the request body: "+err.Error())
	}
	return nil
}

// ListSnapshots returns every snapshot of the jail's datasets.
//
// @Summary      List snapshots
// @Description  The jail's own dataset and its children's, oldest first.
// @Description
// @Description  Ordered by create_txg, ZFS's transaction group, not by created. A
// @Description  timestamp has second granularity and two snapshots taken in the same
// @Description  second tie on it — and "which is newer" is exactly what decides what a
// @Description  rollback destroys. The transaction group never ties.
// @Description
// @Description  name is the full dataset@snapshot, which is what confirm must equal
// @Description  elsewhere; short is the part after the @, which goes in a URL.
// @Description
// @Description  Unfiltered. On a host running zrepl this is long — one jail on the
// @Description  deployment target has 39 snapshots, 35 of them hourly.
// @Tags         snapshots
// @Produce      json
// @Param        name  path      string  true  "Jail name"
// @Success      200   {array}   Snapshot
// @Failure      401   {object}  common.ErrorResponse
// @Failure      404   {object}  common.ErrorResponse  "No such jail, or it is not on ZFS"
// @Security     BearerAuth
// @Router       /jails/{name}/snapshots [get]
func (h *JailHandler) ListSnapshots(w http.ResponseWriter, r *http.Request) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	snapshots, err := h.service.Snapshots(r.Context(), name)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, snapshots)
}

// The jail's own routes take it and its children at one point in time; the
// child routes take that dataset alone. recursive is the difference, and it is
// the route that decides it rather than a parameter a caller can forget.
// CreateSnapshot takes the jail and its children at one point in time.
//
// @Summary      Snapshot a jail
// @Description  201 when taken, 200 with existed=true when it was already there, so the
// @Description  retry after a timeout does not raise zfs's "dataset already exists".
// @Description
// @Description  The jail route is recursive: zfs snapshot -r takes the jail's dataset
// @Description  and every child at ONE point in time, which is what lets a root be
// @Description  re-cloned while its data stays consistent with it (JD-007, JD-024).
// @Tags         snapshots
// @Produce      json
// @Param        name      path      string  true  "Jail name"
// @Param        snapshot  path      string  true  "Snapshot name, the part after the @"
// @Success      200       {object}  SnapshotResult  "Already there"
// @Success      201       {object}  SnapshotResult  "Taken"
// @Failure      400       {object}  common.ErrorResponse  "Not a snapshot name"
// @Failure      401       {object}  common.ErrorResponse
// @Failure      404       {object}  common.ErrorResponse  "No such jail, or it is not on ZFS"
// @Security     BearerAuth
// @Router       /jails/{name}/snapshots/{snapshot} [put]
func (h *JailHandler) CreateSnapshot(w http.ResponseWriter, r *http.Request) {
	h.createSnapshot(w, r, "", true)
}

// CreateDatasetSnapshot takes one dataset, without its children.
//
// @Summary      Snapshot one dataset
// @Description  No -r, no children. Use it when the thing worth snapshotting is the data
// @Description  and not the userland: a jail's root is a clone of a template and can be
// @Description  made again, which is the premise of JD-007.
// @Tags         snapshots
// @Produce      json
// @Param        name      path      string  true  "Jail name"
// @Param        child     path      string  true  "Dataset name"
// @Param        snapshot  path      string  true  "Snapshot name"
// @Success      200       {object}  SnapshotResult  "Already there"
// @Success      201       {object}  SnapshotResult  "Taken"
// @Failure      400       {object}  common.ErrorResponse
// @Failure      401       {object}  common.ErrorResponse
// @Failure      404       {object}  common.ErrorResponse
// @Security     BearerAuth
// @Router       /jails/{name}/datasets/{child}/snapshots/{snapshot} [put]
func (h *JailHandler) CreateDatasetSnapshot(w http.ResponseWriter, r *http.Request) {
	h.createSnapshot(w, r, chi.URLParam(r, "child"), false)
}

// DestroySnapshot removes the snapshot from the jail and its children.
//
// @Summary      Destroy a snapshot
// @Description  Recursive, matching what the recursive create made.
// @Description
// @Description  409 with a dependent clone or a hold, naming them. Both are checked by
// @Description  asking ZFS first — zfs get clones and zfs holds — rather than by running
// @Description  the destroy and reading its complaint, so the refusal happens before
// @Description  anything destructive does. Neither can be overridden here: getting past a
// @Description  clone needs zfs destroy -R, which takes the clone with it (JD-024).
// @Description
// @Description  confirm must be the snapshot's FULL dataset@snapshot, not the word in the
// @Description  path, so a script looping over jails with one snapshot name in a variable
// @Description  cannot confirm every one of them by accident (JD-016).
// @Tags         snapshots
// @Produce      json
// @Param        name      path      string  true  "Jail name"
// @Param        snapshot  path      string  true  "Snapshot name"
// @Param        confirm   query     string  true  "The full dataset@snapshot"
// @Success      200       {object}  SnapshotResult
// @Failure      400       {object}  common.ErrorResponse  "Missing or mismatched confirm"
// @Failure      401       {object}  common.ErrorResponse
// @Failure      404       {object}  common.ErrorResponse  "No such jail or snapshot"
// @Failure      409       {object}  SnapshotResult        "A clone or a hold is in the way"
// @Security     BearerAuth
// @Router       /jails/{name}/snapshots/{snapshot} [delete]
func (h *JailHandler) DestroySnapshot(w http.ResponseWriter, r *http.Request) {
	h.destroySnapshot(w, r, "", true)
}

// DestroyDatasetSnapshot removes one dataset's snapshot.
//
// @Summary      Destroy one dataset's snapshot
// @Description  As above, on one dataset only. Same clone and hold refusals, same
// @Description  full-name confirm.
// @Tags         snapshots
// @Produce      json
// @Param        name      path      string  true  "Jail name"
// @Param        child     path      string  true  "Dataset name"
// @Param        snapshot  path      string  true  "Snapshot name"
// @Param        confirm   query     string  true  "The full dataset@snapshot"
// @Success      200       {object}  SnapshotResult
// @Failure      400       {object}  common.ErrorResponse
// @Failure      401       {object}  common.ErrorResponse
// @Failure      404       {object}  common.ErrorResponse
// @Failure      409       {object}  SnapshotResult
// @Security     BearerAuth
// @Router       /jails/{name}/datasets/{child}/snapshots/{snapshot} [delete]
func (h *JailHandler) DestroyDatasetSnapshot(w http.ResponseWriter, r *http.Request) {
	h.destroySnapshot(w, r, chi.URLParam(r, "child"), false)
}

// Rollback returns the jail's own dataset to a snapshot.
//
// @Summary      Roll back
// @Description  NEVER recursive. zfs rollback has no way to descend into children, and its
// @Description  own -r means something else entirely — "destroy the snapshots newer than
// @Description  this one". So this rolls back the jail's own dataset and leaves its
// @Description  children where they are; a child is rolled back through its own route, as
// @Description  a separate decision (JD-024).
// @Description
// @Description  409 when newer snapshots exist, naming every one it would destroy. Pass
// @Description  destroy_newer=true to accept losing exactly those — the refusal comes
// @Description  first so the second call is informed rather than a retry with more force.
// @Description
// @Description  Still 409, even with destroy_newer=true, when one of those newer snapshots
// @Description  is held or has a clone.
// @Tags         snapshots
// @Produce      json
// @Param        name           path      string  true   "Jail name"
// @Param        snapshot       path      string  true   "Snapshot name"
// @Param        confirm        query     string  true   "The full dataset@snapshot"
// @Param        destroy_newer  query     bool    false  "Accept losing the snapshots taken after it"
// @Success      200            {object}  SnapshotResult
// @Failure      400            {object}  common.ErrorResponse
// @Failure      401            {object}  common.ErrorResponse
// @Failure      404            {object}  common.ErrorResponse
// @Failure      409            {object}  SnapshotResult  "Newer snapshots, a clone or a hold"
// @Security     BearerAuth
// @Router       /jails/{name}/snapshots/{snapshot}/rollback [post]
func (h *JailHandler) Rollback(w http.ResponseWriter, r *http.Request) {
	h.rollback(w, r, "")
}

// RollbackDataset returns one child dataset to a snapshot.
//
// @Summary      Roll back one dataset
// @Description  The route that matters for a jail whose state lives in a child: rolling
// @Description  the data back without touching the userland.
// @Tags         snapshots
// @Produce      json
// @Param        name           path      string  true   "Jail name"
// @Param        child          path      string  true   "Dataset name"
// @Param        snapshot       path      string  true   "Snapshot name"
// @Param        confirm        query     string  true   "The full dataset@snapshot"
// @Param        destroy_newer  query     bool    false  "Accept losing the snapshots taken after it"
// @Success      200            {object}  SnapshotResult
// @Failure      400            {object}  common.ErrorResponse
// @Failure      401            {object}  common.ErrorResponse
// @Failure      404            {object}  common.ErrorResponse
// @Failure      409            {object}  SnapshotResult
// @Security     BearerAuth
// @Router       /jails/{name}/datasets/{child}/snapshots/{snapshot}/rollback [post]
func (h *JailHandler) RollbackDataset(w http.ResponseWriter, r *http.Request) {
	h.rollback(w, r, chi.URLParam(r, "child"))
}

func (h *JailHandler) createSnapshot(w http.ResponseWriter, r *http.Request, child string, recursive bool) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	result, err := h.service.CreateSnapshot(r.Context(), name, child, chi.URLParam(r, "snapshot"), recursive)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	if result.Existed {
		common.WriteJSON(w, http.StatusOK, result)
		return
	}
	common.WriteJSON(w, http.StatusCreated, result)
}

func (h *JailHandler) destroySnapshot(w http.ResponseWriter, r *http.Request, child string, recursive bool) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	result, err := h.service.DestroySnapshot(r.Context(), name, child,
		chi.URLParam(r, "snapshot"), r.URL.Query().Get("confirm"), recursive)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	writeSnapshotResult(w, result)
}

func (h *JailHandler) rollback(w http.ResponseWriter, r *http.Request, child string) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	// Anything but the literal "true" is not consent. A client that sends
	// destroy_newer=maybe has not said yes.
	destroyNewer := r.URL.Query().Get("destroy_newer") == "true"
	result, err := h.service.Rollback(r.Context(), name, child,
		chi.URLParam(r, "snapshot"), r.URL.Query().Get("confirm"), destroyNewer)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	writeSnapshotResult(w, result)
}

// A refusal is 409: the request was well formed and the host's state is what
// says no.
func writeSnapshotResult(w http.ResponseWriter, result SnapshotResult) {
	if result.Blocked != nil {
		common.WriteJSON(w, http.StatusConflict, result)
		return
	}
	common.WriteJSON(w, http.StatusOK, result)
}

// retainRequest names where the dataset will live once it is out of the jail:
// <pool>/jails/retained/<name>.
type retainRequest struct {
	Name string `json:"name"`
}

// restoreRequest names which retained dataset to bring back.
type restoreRequest struct {
	From string `json:"from"`
}

// Retain moves a child dataset out of the jail.
//
// @Summary      Retain a dataset past its jail
// @Description  Moves it to <pool>/jails/retained/<name>. zfs destroy -r on a jail takes
// @Description  its children and their snapshots with it (JD-007), so a dataset worth
// @Description  keeping has to stop being a child first.
// @Description
// @Description  Three audited commands: zfs rename -u, zfs unmount, zfs mount. The -u is
// @Description  not optional — a plain zfs rename unmounts and remounts inside the same
// @Description  command, which on a running jail is the filesystem vanishing from under a
// @Description  live process (JD-025).
// @Description
// @Description  It still unmounts: remounted=true means the filesystem moved and a running
// @Description  jail just lost that path, so stop it first. Leaving the move half done is
// @Description  worse — a dataset still mounted inside a jail makes the jail undestroyable.
// @Description
// @Description  The retained parent is created on demand and is deliberately not part of
// @Description  the layout readiness check (JD-021).
// @Tags         storage
// @Accept       json
// @Produce      json
// @Param        name    path      string         true  "Jail name"
// @Param        child   path      string         true  "Dataset name"
// @Param        target  body      retainRequest  true  "The name it will have under retained"
// @Success      200     {object}  DatasetMove
// @Failure      400     {object}  common.ErrorResponse  "Not a dataset name"
// @Failure      401     {object}  common.ErrorResponse
// @Failure      404     {object}  common.ErrorResponse  "No such jail or dataset"
// @Failure      409     {object}  common.ErrorResponse  "That retained name is taken"
// @Security     BearerAuth
// @Router       /jails/{name}/datasets/{child}/retain [post]
func (h *JailHandler) Retain(w http.ResponseWriter, r *http.Request) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	var body retainRequest
	if err := decodeJSON(r, &body); err != nil {
		common.HandlerError(w, err)
		return
	}
	moved, err := h.service.Retain(r.Context(), name, chi.URLParam(r, "child"), body.Name)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, moved)
}

// Restore moves a retained dataset back in under this jail.
//
// @Summary      Restore a retained dataset
// @Description  The same three commands in the other direction.
// @Description
// @Description  It need not be the jail it left, and it need not keep the name it left
// @Description  with: a dataset retained from ottermq can come back as ottermq-2's data,
// @Description  which is the point of keeping it. Its properties and snapshots travel with
// @Description  it, because they belong to the dataset and not to where it sits.
// @Description
// @Description  from is the short name from GET /retained, not the full dataset path.
// @Tags         storage
// @Accept       json
// @Produce      json
// @Param        name    path      string          true  "Jail name"
// @Param        child   path      string          true  "The name it will have in this jail"
// @Param        source  body      restoreRequest  true  "Which retained dataset to bring back"
// @Success      200     {object}  DatasetMove
// @Failure      400     {object}  common.ErrorResponse
// @Failure      401     {object}  common.ErrorResponse
// @Failure      404     {object}  common.ErrorResponse  "Nothing retained under that name"
// @Failure      409     {object}  common.ErrorResponse  "The jail already has a child of that name"
// @Security     BearerAuth
// @Router       /jails/{name}/datasets/{child}/restore [post]
func (h *JailHandler) Restore(w http.ResponseWriter, r *http.Request) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	var body restoreRequest
	if err := decodeJSON(r, &body); err != nil {
		common.HandlerError(w, err)
		return
	}
	moved, err := h.service.Restore(r.Context(), name, body.From, chi.URLParam(r, "child"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, moved)
}

// recloneRequest names the template snapshot the jail's new root is cloned
// from. The full dataset@snapshot, because @base is not a safe default: on a
// real host every jail is already a clone of templates/<release>@base, so
// defaulting to it would re-clone a jail onto the snapshot it is already on.
type recloneRequest struct {
	Template string `json:"template"`
}

// Reclone replaces the jail's root, keeping its child datasets.
//
// @Summary      Re-clone a jail's root from a template snapshot
// @Description  The upgrade path for a jail whose state lives in a child dataset: stop,
// @Description  move the children aside, park the old root, clone the new one, move the
// @Description  children back, start (JD-007, JD-026).
// @Description
// @Description  **Nothing is destroyed.** The old root is renamed to
// @Description  <pool>/jails/retained/<jail>-root-<snapshot>, so a re-clone onto a bad
// @Description  template costs one call to undo rather than a restore from backup. It is a
// @Description  clone, so it occupies only what the jail wrote since it was made.
// @Description
// @Description  The jail is stopped and started again by this call, and ends in the state
// @Description  it was found in. A jail that was already stopped stays stopped.
// @Description
// @Description  **If a step fails, nothing is retried and nothing is undone.** The response
// @Description  names the step and lists every dataset that is no longer under the jail,
// @Description  with where it now sits, so it can be restored with the call you already
// @Description  have. Running a recovery automatically while the first failure is still
// @Description  unexplained is how a bad situation becomes an unreadable one.
// @Description
// @Description  The template must be the full dataset@snapshot. There is no default:
// @Description  every jail on a real host is already a clone of templates/<release>@base,
// @Description  so defaulting to it would be a no-op dressed as an upgrade.
// @Tags         storage
// @Accept       json
// @Produce      json
// @Param        name      path      string          true  "Jail name"
// @Param        confirm   query     string          true  "The jail's name again"
// @Param        template  body      recloneRequest  true  "The template snapshot to clone from"
// @Success      200       {object}  Reclone
// @Failure      400       {object}  common.ErrorResponse  "Missing confirm, or a malformed snapshot name"
// @Failure      401       {object}  common.ErrorResponse
// @Failure      404       {object}  common.ErrorResponse  "No such jail, no such template snapshot, or the jail is not on ZFS"
// @Failure      500       {object}  Reclone               "A step failed; the body says which and where the datasets are"
// @Security     BearerAuth
// @Router       /jails/{name}/reclone [post]
func (h *JailHandler) Reclone(w http.ResponseWriter, r *http.Request) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	var body recloneRequest
	if err := decodeJSON(r, &body); err != nil {
		common.HandlerError(w, err)
		return
	}

	result, err := h.service.Reclone(r.Context(), name, body.Template, r.URL.Query().Get("confirm"))
	if err != nil {
		// A failure after the datasets started moving carries the recovery
		// information, and an error message alone would throw it away.
		if result.FailedAt != "" {
			common.WriteJSON(w, statusForPartial(err), result)
			return
		}
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, result)
}

// statusForPartial keeps a refusal a refusal when the response carries a
// partly-done operation. A template that does not exist is a 404 whether or
// not anything had started.
func statusForPartial(err error) int {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return http.StatusInternalServerError
}

// makeTemplateRequest names the template the jail becomes.
type makeTemplateRequest struct {
	Name string `json:"name"`
}

// MakeTemplate promotes a configured jail to a template.
//
// @Summary      Clone this jail as a template
// @Description  Turns a jail you configured by hand into a template every later jail is
// @Description  cloned from — the FreeBSD Handbook's "Cloning a Jail as a Template" (17.4.3),
// @Description  made callable (JD-027).
// @Description
// @Description  This is the general way to fill a template: it captures whatever you did to
// @Description  the jail, not only packages. Installing packages into a template without a
// @Description  jail is `PUT /api/templates/{name}`, which is faster and narrower.
// @Description
// @Description  Four commands: snapshot the jail's dataset as `@base`, clone it into
// @Description  `<pool>/jails/templates/<name>`, then **promote** the clone.
// @Description
// @Description  The promote matters more than it looks. Without it the template is a clone
// @Description  *of* the jail, and that jail can never be destroyed — `zfs destroy` answers
// @Description  `filesystem has dependent clones` and names the template. After it the jail
// @Description  is a clone of the template instead, which is the right way round: a template
// @Description  should outlive the jail it came from, and the lineage then reads
// @Description  release template → this one → the jail.
// @Description
// @Description  **The jail must be stopped.** A snapshot of a running jail succeeds but is an
// @Description  image of a system caught mid-stride; the Handbook says to stop it first, and
// @Description  taking a service down is your call rather than this endpoint's.
// @Tags         templates
// @Accept       json
// @Produce      json
// @Param        name      path      string               true  "The jail to clone"
// @Param        template  body      makeTemplateRequest  true  "The template's name"
// @Success      201       {object}  Template
// @Failure      400       {object}  common.ErrorResponse  "Not a template name"
// @Failure      401       {object}  common.ErrorResponse
// @Failure      404       {object}  common.ErrorResponse  "No such jail, or it is not on ZFS"
// @Failure      409       {object}  common.ErrorResponse  "The jail is running, or that template exists"
// @Security     BearerAuth
// @Router       /jails/{name}/template [post]
func (h *JailHandler) MakeTemplate(w http.ResponseWriter, r *http.Request) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	var body makeTemplateRequest
	if err := decodeJSON(r, &body); err != nil {
		common.HandlerError(w, err)
		return
	}
	dataset, err := h.service.MakeTemplate(r.Context(), name, body.Name)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusCreated, Template{Name: body.Name, Dataset: dataset})
}

// Template is what a jail became. Deliberately thin: the full picture --
// readiness, lineage, sizes -- comes from GET /api/templates, which reads it
// all from ZFS rather than from anything recorded here.
type Template struct {
	Name    string `json:"name"`
	Dataset string `json:"dataset"`
}

// Create builds a jail from a ready template.
//
// @Summary      Create a jail
// @Description  Clones a ready template's snapshot into the jail's own dataset, writes
// @Description  its stanza to /etc/jail.conf.d/<name>.conf, optionally adds it to
// @Description  rc.conf's jail_list, and starts it (JD-007, JD-030).
// @Description
// @Description  **The start is confirmed, not assumed.** `service jail start` exits 0
// @Description  whether or not the jail came up, so the jail is read back from jls before
// @Description  this reports it running.
// @Description
// @Description  **If a step fails, nothing is undone.** The response names the step and
// @Description  says what exists — a dataset that was cloned stays cloned, a stanza that
// @Description  was written stays written. Rolling back automatically is a second
// @Description  destructive operation run at the moment least is known about the state.
// @Description
// @Description  The template is the full dataset@snapshot of a template that is ready;
// @Description  GET /api/templates lists them with the snapshot each one offers. The
// @Description  jail's root is cloned into <pool>/jails/containers/<name>, in the
// @Description  template's own pool, because a clone cannot cross one.
// @Description
// @Description  Network modes: `alias` (one address on a host interface, needs ip4 and
// @Description  interface), `inherit` (the host's addresses and its stack), `disable`
// @Description  (no network, the default), and `vnet`.
// @Description
// @Description  **`vnet`** gives the jail its own network stack on an epair, with the host
// @Description  end on `bridge` — which must already exist, because **Jail Deck never
// @Description  creates a bridge** (JD-017); `GET /api/host` reports the ones that do.
// @Description  `private: true` makes the host end a private bridge member. Measured: two
// @Description  **private** members cannot reach each other at layer 2, but a private
// @Description  member still reaches non-private ones and the bridge itself — so the
// @Description  isolation only holds where every jail on the bridge asks for it.
// @Description  `ip4` and `gateway`
// @Description  are set inside the jail and are both optional — a VNET jail may instead
// @Description  get its address from DHCP of its own.
// @Description
// @Description  The epair is created in `exec.prestart` and destroyed in `exec.poststop`,
// @Description  so the jail gets the same network started from a shell as from here. The
// @Description  unit is chosen when the stanza is written, from what no interface uses and
// @Description  no jail configuration claims.
// @Description
// @Description  **A VNET jail reports no `ip` in the listing.** It sets no `ip4.addr`, so
// @Description  neither `jls` nor its configuration carries one — the address is in the
// @Description  jail's own stack. This response carries the one that was asked for.
// @Description
// @Description  If the start of a VNET jail cannot be confirmed, Jail Deck destroys the
// @Description  epair, because `jail(8)` does not run `exec.poststop` for a start that
// @Description  failed and the orphan would block every retry (JD-030). That is the only
// @Description  thing a failed create undoes. If the cleanup itself fails, the interface
// @Description  is named in the error rather than left silent.
// @Description
// @Description  **`limits`** are rctl rules — `memoryuse`, `vmemoryuse`, `maxproc`,
// @Description  `openfiles`, `pcpu` — plus a `cpuset`. They are applied by the stanza's
// @Description  `exec.poststart` and removed by its `exec.prestop`, so a jail started
// @Description  from a shell is limited the same way, and a jail whose limits cannot be
// @Description  applied does not start at all: `jail(8)` destroys a jail whose poststart
// @Description  fails (JD-018). Sizes take a K, M, G or T suffix; `pcpu` is percent of
// @Description  one core.
// @Description
// @Description  rctl rules need `kern.racct.enable=1`, which `GET /api/host/readiness`
// @Description  reports; a spec asking for them on a host without it is refused rather
// @Description  than built. `cpuset` needs no accounting and is allowed either way.
// @Description
// @Description  **rctl rules are keyed to the jail's name, not to the jail**, and outlive
// @Description  it. A create is refused when rules are still keyed to the name it was
// @Description  given — otherwise the new jail would silently run under them — and the
// @Description  refusal lists them and the one command that clears them. A start that
// @Description  could not be confirmed has whatever rules it managed to apply taken back
// @Description  here, and says so in `rules_removed`.
// @Description
// @Description  **`params`** are jail(8) parameters, and only these:
// @Description  `devfs_ruleset`, `securelevel` (-1..3), `enforce_statfs` (0..2),
// @Description  `children.max` (0 only), `sysvmsg`, `sysvsem`, `sysvshm`
// @Description  (`"disable"` or `"new"`), `allow.chflags` and `allow.set_hostname`.
// @Description  Anything else is refused with the parameter named — an allowlist, because
// @Description  a denylist fails open the day jail(8) gains a parameter (JD-018).
// @Description
// @Description  `devfs_ruleset` is checked against the rulesets the kernel has loaded.
// @Description  Measured on 15.1-RELEASE-p5: a number it does not have gives the jail the
// @Description  host's **whole /dev** — disks, mem, kmem, bpf — silently, while `jls`
// @Description  reports the number that was asked for. Rulesets live in
// @Description  /etc/devfs.rules and are yours; Jail Deck never writes them.
// @Description
// @Description  **`datasets`** are child datasets made beneath the jail's own, one level
// @Description  down, before the stanza is written and long before the jail starts — a
// @Description  jail whose `/etc/rc` expects its data mounted should find it there, and a
// @Description  quota that arrives after the first write was not in force when it
// @Description  mattered. Each carries native ZFS properties from JD-023's allowlist:
// @Description  `mountpoint`, `refquota`, `recordsize`, `compression`, `atime`. A quota
// @Description  belongs on a child rather than on the jail's root, because a clone's
// @Description  `referenced` space includes the template's blocks (JD-007).
// @Description
// @Description  `mountpoint` is a path on the **host** and defaults to the one the child
// @Description  inherits — `<jail path>/<name>`, which is `/<name>` seen from inside the
// @Description  jail. A dataset wanted at `/var/db/postgres` inside a jail at
// @Description  `/usr/local/jails/containers/db1` asks for
// @Description  `/usr/local/jails/containers/db1/var/db/postgres`.
// @Description
// @Description  **A mountpoint outside the jail is refused.** Measured: ZFS makes such a
// @Description  dataset without complaint, the jail cannot see it, and `zfs destroy -r`
// @Description  on the jail destroys it anyway — so the jail's destroy would take a
// @Description  dataset living elsewhere on the host. Data meant to outlive the jail goes
// @Description  through `POST /api/jails/{name}/datasets/{child}/retain` instead.
// @Description
// @Description  **`properties`** are ZFS user properties set on the jail's own dataset —
// @Description  the tags a client finds its jails by. They are merged, never replaced, so
// @Description  a tag another tool set is left alone (JD-004, JD-023).
// @Tags         jails
// @Accept       json
// @Produce      json
// @Param        spec  body      Spec  true  "The jail to create"
// @Success      201   {object}  Created
// @Failure      400   {object}  common.ErrorResponse  "A malformed spec, a parameter that is not allowed, a devfs ruleset the kernel has not loaded, or rctl limits on a host that is not accounting"
// @Failure      401   {object}  common.ErrorResponse
// @Failure      404   {object}  common.ErrorResponse  "No such template snapshot"
// @Failure      409   {object}  common.ErrorResponse  "A jail of that name already exists, its configuration file does, rctl rules are still keyed to the name, or a child dataset is somewhere else"
// @Failure      500   {object}  Created               "A step failed; the body says which and what exists"
// @Failure      501   {object}  common.ErrorResponse  "This Jail Deck has no storage wired"
// @Security     BearerAuth
// @Router       /jails [post]
func (h *JailHandler) Create(w http.ResponseWriter, r *http.Request) {
	var spec Spec
	if err := decodeJSON(r, &spec); err != nil {
		common.HandlerError(w, err)
		return
	}

	result, err := h.service.Create(r.Context(), spec)
	if err != nil {
		// Once the root exists there is something the caller has to know
		// about, and an error message on its own would throw it away. Before
		// that -- a name already taken, a template that is not there, a clone
		// that failed -- nothing was created, so the plain error is the whole
		// answer and the empty result would only bury it.
		if result.Root != "" {
			result.Error = err.Error()
			common.WriteJSON(w, statusForPartial(err), result)
			return
		}
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusCreated, result)
}

// Ensure makes a jail exist exactly as the spec describes it, or says why not.
//
// @Summary      Create a jail if it is not there (idempotent)
// @Description  The same spec as POST /api/jails, by the verb an automation client
// @Description  wants: absent → created, `201`; already what the spec describes →
// @Description  `200` with `changed: false`; there but different → `409` with the
// @Description  differences. **Nothing is ever reconciled** — a jail that differs is
// @Description  left exactly as it is and the client decides (JD-016).
// @Description
// @Description  Safe to repeat. A client that lost its answer to a timeout sends the
// @Description  same request again and the reply says where it stands, derived from the
// @Description  host rather than from anything Jail Deck remembers (JD-011).
// @Description
// @Description  **How "matching" is decided.** Jail Deck renders the stanza this spec
// @Description  would write — against the path the jail's dataset reports and the epair
// @Description  its existing stanza already claims — and compares it with the stanza on
// @Description  disk, setting by setting. So the comparison covers every field the
// @Description  generator writes, including ones added after this endpoint was built.
// @Description  Comments, blank lines, line order and the quoting of a value are not
// @Description  differences; anything else is, including a setting only one side has.
// @Description
// @Description  `template` and `autostart` are not in that file and are read where they
// @Description  live: the root's clone origin, and rc.conf's `jail_list`. A jail whose
// @Description  root was re-cloned reports the template it actually came from.
// @Description
// @Description  **Child datasets and tags** are compared against ZFS. A child the spec
// @Description  asks for and the jail does not have is a difference, and so is one the
// @Description  jail has and the spec does not name — a child takes space and `zfs
// @Description  destroy -r` on the jail takes it along, so it is part of the jail in a
// @Description  way a tag is not. Only direct children: the spec describes one level, so
// @Description  a deeper dataset is one it could neither ask for nor disown.
// @Description
// @Description  Properties are compared as a subset, both kinds. A native property a
// @Description  dataset sets and the spec does not mention is not a difference, because
// @Description  the write side applies rather than replaces; nor is a user property
// @Description  another tool set, because the write side merges (JD-023). Values compare
// @Description  as text, so a `refquota` written `2048M` against one ZFS prints as `2G`
// @Description  reads as a difference — parsing a size here would be the second opinion
// @Description  JD-023 refuses to add.
// @Description
// @Description  **`start` is not compared.** It says whether to start the jail once it
// @Description  has been configured, which is an instruction about the create rather
// @Description  than a property of the jail — comparing it would make a jail stopped for
// @Description  maintenance refuse every PUT, and acting on it would be the
// @Description  reconciliation this endpoint refuses to do. The response says whether
// @Description  the jail is running and the client decides.
// @Description
// @Description  `field` in a difference is the jail.conf(5) setting, because that is
// @Description  where the difference is and what an operator would edit; the ones that
// @Description  live elsewhere are named for the spec field — `template`, `autostart`,
// @Description  `datasets.<child>[.<property>]` and `properties.<tag>`. They are reported
// @Description  in that order: template, the configuration's settings as they sort,
// @Description  datasets, tags, autostart.
// @Description
// @Description  The body may leave `name` out — the URL has already said it. A body that
// @Description  names a different jail is refused.
// @Tags         jails
// @Accept       json
// @Produce      json
// @Param        name  path      string  true  "The jail to ensure"
// @Param        spec  body      Spec    true  "The jail it should be"
// @Success      200   {object}  Ensured  "Already what the spec describes; nothing changed"
// @Success      201   {object}  Ensured  "Created"
// @Failure      400   {object}  common.ErrorResponse  "A malformed spec, or one naming a different jail"
// @Failure      401   {object}  common.ErrorResponse
// @Failure      404   {object}  common.ErrorResponse  "No such template snapshot"
// @Failure      409   {object}  Ensured               "It is there and different; `differences` says how"
// @Failure      500   {object}  Ensured               "A step of the create failed; the body says which and what exists"
// @Failure      501   {object}  common.ErrorResponse  "This Jail Deck cannot read a jail back"
// @Security     BearerAuth
// @Router       /jails/{name} [put]
func (h *JailHandler) Ensure(w http.ResponseWriter, r *http.Request) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	var spec Spec
	if err := decodeJSON(r, &spec); err != nil {
		common.HandlerError(w, err)
		return
	}

	result, err := h.service.Ensure(r.Context(), name, spec)
	if err != nil {
		// As with POST: once the root exists there is something the caller
		// has to know about, and an error message alone would throw it away.
		if result.Root != "" && result.Changed {
			result.Error = err.Error()
			common.WriteJSON(w, statusForPartial(err), result)
			return
		}
		common.HandlerError(w, err)
		return
	}
	switch {
	case len(result.Differences) > 0:
		common.WriteJSON(w, http.StatusConflict, result)
	case result.Changed:
		common.WriteJSON(w, http.StatusCreated, result)
	default:
		common.WriteJSON(w, http.StatusOK, result)
	}
}

// Exec runs a command inside a running jail.
//
// @Summary      Run a command in a jail
// @Description  Runs `argv` inside the jail as root and returns its stdout, stderr and
// @Description  exit code. This is JD-007's "hand off to in-jail provisioning" made
// @Description  callable, and it is root in the jail — no more than this API already
// @Description  implies (JD-001, JD-016).
// @Description
// @Description  **A non-zero exit is still a `200`.** The exec succeeded; the thing it ran
// @Description  failed, and `exit_code` is the answer. A client that reads the HTTP status
// @Description  instead is reading the wrong field. `exit_code` is `-1` when the command
// @Description  never ran or was stopped by the timeout.
// @Description
// @Description  **`argv` is an array and there is no shell.** Nothing splits a string here,
// @Description  so a quote or a semicolon in an argument is just a character. A caller who
// @Description  wants a shell asks for one — `["sh", "-c", "…"]` — and has chosen it. There
// @Description  is no stdin: a command that needs input writes a file in the jail first.
// @Description
// @Description  **The environment is clean.** The command runs under `jexec -l`, which gives
// @Description  it `HOME`, `PATH`, `SHELL`, `USER` and `LANG` and nothing of the host's —
// @Description  measured, plain `jexec` hands the jail everything the Jail Deck process was
// @Description  started with, `jaildeck.env` included. `env` adds variables on top of that.
// @Description
// @Description  A request that sets `env` and whose `argv[0]` contains an `=` is refused:
// @Description  `env(1)` has no way to end its assignments — measured, `env -- cmd` fails —
// @Description  so it would read that argument as one more and run nothing.
// @Description
// @Description  **`timeout`** is a duration like `"30s"` or `"2m"`, 60s by default and five
// @Description  minutes at most (JD-014). Longer is refused rather than clamped, because a
// @Description  command silently given less time than asked for looks like one that failed.
// @Description  Work that needs longer belongs in a template, which is prepared in the
// @Description  background and polled (JD-008). A timed-out command is **killed inside the
// @Description  jail**, along with anything it forked, and whatever it printed first is
// @Description  still returned with `timed_out: true`.
// @Description
// @Description  Each stream is capped at 1 MiB and says so with `stdout_truncated` /
// @Description  `stderr_truncated` — output that stops exactly at the cap is otherwise
// @Description  indistinguishable from output that happened to end there.
// @Description
// @Description  Every call is audited with the command line it became, which includes any
// @Description  environment values: a secret passed this way is written to the audit log,
// @Description  which is `0700` and root-owned for that kind of reason.
// @Tags         jails
// @Accept       json
// @Produce      json
// @Param        name  path      string       true  "Jail name"
// @Param        exec  body      ExecRequest  true  "The command to run"
// @Success      200   {object}  ExecResult   "It ran; read exit_code for what it did"
// @Failure      400   {object}  common.ErrorResponse  "No argv, a timeout past the cap, or a bad env name"
// @Failure      401   {object}  common.ErrorResponse
// @Failure      404   {object}  common.ErrorResponse  "No such jail"
// @Failure      409   {object}  common.ErrorResponse  "The jail is not running"
// @Failure      500   {object}  common.ErrorResponse  "The command could not be run at all"
// @Failure      501   {object}  common.ErrorResponse  "This Jail Deck cannot run commands in a jail"
// @Security     BearerAuth
// @Router       /jails/{name}/exec [post]
func (h *JailHandler) Exec(w http.ResponseWriter, r *http.Request) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	var request ExecRequest
	if err := decodeJSON(r, &request); err != nil {
		common.HandlerError(w, err)
		return
	}

	result, err := h.service.Exec(r.Context(), name, request)
	if err != nil {
		common.HandlerError(w, err)
		return
	}
	common.WriteJSON(w, http.StatusOK, result)
}

// Destroy removes a jail, its configuration and its filesystem.
//
// @Summary      Destroy a jail
// @Description  Stops the jail, takes it out of rc.conf's `jail_list`, deletes
// @Description  /etc/jail.conf.d/<name>.conf, and destroys its dataset — in that order
// @Description  (JD-030).
// @Description
// @Description  A VNET jail's epair goes when it stops, through its own `exec.poststop`.
// @Description  One left behind by an earlier failed start is destroyed here too, and
// @Description  named in `epair` — otherwise it would outlive the jail it belongs to.
// @Description
// @Description  **rctl rules are cleared on the same terms**, and named in `rules`. A
// @Description  jail stopped cleanly has had them removed by its own `exec.prestop`, so
// @Description  normally there are none; one left behind matters more than a stranded
// @Description  interface, because rules are keyed to the name and the next jail called
// @Description  this would inherit them (JD-018). Neither sweep can fail the destroy:
// @Description  what could not be cleared is reported in `epair_error` or `rules_error`.
// @Description  A sweep that removed something, or failed to, is audited as
// @Description  `remove-leftover-epair` or `remove-leftover-rctl`.
// @Description
// @Description  **Child datasets go with it** (JD-007). They are listed in `taken`. A
// @Description  dataset worth keeping has to stop being a child first: `POST
// @Description  /api/jails/{name}/datasets/{child}/retain` moves it out.
// @Description
// @Description  **Snapshots, clones and holds refuse the destroy**, with each one named
// @Description  and nothing touched. Each was kept on purpose, so each goes first — and
// @Description  `zfs destroy -R` is never passed, which is what would make this check
// @Description  decorative.
// @Description
// @Description  **The last word is ZFS's.** Nothing here re-checks that the jail is down
// @Description  before destroying its dataset: `zfs destroy` refuses one whose jail is
// @Description  running with `pool or dataset is busy`, and a check of our own would read
// @Description  the state and then act on what it read, while the mount is held the whole
// @Description  time. Only one of those can lose a race.
// @Description
// @Description  A jail with no file of its own in jail.conf.d is refused: its block is in
// @Description  /etc/jail.conf, which belongs to you and which Jail Deck never writes, so
// @Description  destroying the dataset would leave the jail declared and pointing nowhere.
// @Description
// @Description  **If a step fails, nothing is undone.** The body names the step and says
// @Description  what was already done — a stopped jail stays stopped.
// @Tags         jails
// @Produce      json
// @Param        name     path      string  true  "The jail to destroy"
// @Param        confirm  query     string  true  "The jail's name again"
// @Success      200      {object}  Destroyed
// @Failure      400      {object}  common.ErrorResponse  "Missing or mismatched confirm"
// @Failure      401      {object}  common.ErrorResponse
// @Failure      404      {object}  common.ErrorResponse  "No such jail, or it has no dataset of its own"
// @Failure      409      {object}  Destroyed             "Snapshots, clones or holds are in the way, or the jail is configured in /etc/jail.conf"
// @Failure      500      {object}  Destroyed             "A step failed; the body says which and what was done"
// @Failure      501      {object}  common.ErrorResponse  "This Jail Deck has no storage wired"
// @Security     BearerAuth
// @Router       /jails/{name} [delete]
func (h *JailHandler) Destroy(w http.ResponseWriter, r *http.Request) {
	name, err := NewJailName(chi.URLParam(r, "name"))
	if err != nil {
		common.HandlerError(w, err)
		return
	}

	result, err := h.service.Destroy(r.Context(), name, r.URL.Query().Get("confirm"))
	if err != nil {
		// Once a step has run there is something the caller has to know
		// about, and an error message on its own would throw it away. Before
		// that -- a mismatched confirm, no such jail, a configuration file
		// Jail Deck cannot remove -- nothing happened, so the plain error is
		// the whole answer and an empty body would only bury it.
		if result.Stopped || result.Unlisted || result.Config != "" {
			result.Error = err.Error()
			common.WriteJSON(w, statusForPartial(err), result)
			return
		}
		common.HandlerError(w, err)
		return
	}
	if result.Blocked != nil {
		common.WriteJSON(w, http.StatusConflict, result)
		return
	}
	common.WriteJSON(w, http.StatusOK, result)
}
