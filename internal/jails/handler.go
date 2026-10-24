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
// @Description  (no network, the default). `vnet` is decided (JD-017) and not built yet.
// @Tags         jails
// @Accept       json
// @Produce      json
// @Param        spec  body      Spec  true  "The jail to create"
// @Success      201   {object}  Created
// @Failure      400   {object}  common.ErrorResponse  "A malformed spec: see the message for the field"
// @Failure      401   {object}  common.ErrorResponse
// @Failure      404   {object}  common.ErrorResponse  "No such template snapshot"
// @Failure      409   {object}  common.ErrorResponse  "A jail of that name already exists, or its configuration file does"
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
