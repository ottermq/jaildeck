package jails

import (
	"context"
	"time"
)

type OperationResult struct {
	Jail    Jail
	Command string
}

type JailSystem interface {
	List(ctx context.Context) ([]Jail, error)
	Start(ctx context.Context, name string) (OperationResult, error)
	Stop(ctx context.Context, name string) (OperationResult, error)
	Restart(ctx context.Context, name string) (OperationResult, error)
}

// Dataset is a ZFS dataset belonging to a jail, as the listing reports it.
//
// This is deliberately the jails domain's own type rather than the storage
// domain's: the port below is defined by the package that needs it and speaks
// that package's types (JD-010), which is what keeps internal/jails free of
// any import of internal/storage -- and testable with no ZFS in sight.
type Dataset struct {
	Name       string `json:"name"`
	Mountpoint string `json:"mountpoint"`
	// Bytes, not the "1.41G" zfs list prints by default, so a client can
	// compare and total them.
	Used       int64 `json:"used"`
	Referenced int64 `json:"referenced"`
	Available  int64 `json:"available"`
	// Properties are the native ZFS properties this dataset sets locally, out
	// of the four an existing one may be given (JD-023). Absent rather than
	// empty when it sets none, and a property it merely inherits is not here:
	// that value belongs to the parent, and setting it here would create a
	// local override rather than change what is in effect.
	//
	// The jail's *user* properties stay at the jail level, where JD-004 put
	// them -- a client tags the jail, not each of its datasets.
	Properties map[string]string `json:"properties,omitempty"`
}

// JailStorage is the storage half of one jail's listing.
type JailStorage struct {
	// Datasets are the jail's own dataset and everything mounted beneath it,
	// in the order zfs listed them.
	Datasets []Dataset
	// Properties are the ZFS user properties of the dataset mounted at the
	// jail's path -- the jail's own, not a child's. An automation client tags
	// its jails with these (otter:instance) and finds them again from the
	// listing alone, with nothing stored in Jail Deck (JD-004, JD-011).
	Properties map[string]string
}

// DatasetSource answers which datasets belong to a jail. It takes the paths
// rather than the names because a jail's path is what ties it to ZFS: a
// dataset is the jail's when it is mounted at that path or under it, which
// stays true for a jail placed outside the conventional <pool>/jails/
// containers/<name> layout, and reports nothing for a jail not on ZFS at all.
//
// One call answers every jail, because the implementation reads the host once
// (#2: "one batched zfs list/zfs get call, not one per jail").
type DatasetSource interface {
	// StorageForPaths answers for each path given. A path with no dataset of
	// its own is absent from the map rather than present and empty, so the
	// caller can tell "no dataset is mounted there" from "that dataset has no
	// children". An empty path is never answered: a jail with no path matches
	// nothing rather than everything.
	StorageForPaths(ctx context.Context, paths []string) (map[string]JailStorage, error)
}

// DatasetChange is what ensuring a dataset did. It decides the status code:
// created is 201, a conflict is 409, anything else 200.
type DatasetChange struct {
	Dataset string            `json:"dataset"`
	Created bool              `json:"created"`
	Applied map[string]string `json:"applied"`
	// Conflict is set when the dataset exists somewhere other than where the
	// request said. Mountpoint is settable only at creation (JD-023), so this
	// is not a change the API will make -- and reporting both values says more
	// than refusing would.
	Conflict *PropertyConflict `json:"conflict,omitempty"`
}

type PropertyConflict struct {
	Property string `json:"property"`
	Want     string `json:"want"`
	Have     string `json:"have"`
}

// DatasetWriter is the write half, kept apart from DatasetSource because a
// listing must not need write access to a port in order to read one.
//
// Both take the jail's path for the same reason StorageForPaths does: the path
// is what ties a jail to ZFS. child names a dataset directly beneath the
// jail's own; an empty child means the jail's own dataset, which is where a
// client's otter:instance tag belongs.
type DatasetWriter interface {
	// EnsureDataset creates the child with properties, or brings an existing
	// one to them. Idempotent: a client that timed out repeats the call and
	// the result says where it stands (JD-016).
	EnsureDataset(ctx context.Context, jailPath, child string, properties map[string]string) (DatasetChange, error)
	// SetUserProperties merges properties in; a nil value removes one. It
	// returns what the dataset carries afterwards, read back rather than
	// assumed.
	SetUserProperties(ctx context.Context, jailPath, child string, properties map[string]*string) (map[string]string, error)
}

// Snapshot is one snapshot of one of a jail's datasets.
type Snapshot struct {
	// Name is "dataset@snapshot" as zfs prints it, and is what confirm= must
	// equal on a destroy or a rollback (JD-024).
	Name    string `json:"name"`
	Dataset string `json:"dataset"`
	// Short is the part after the @, which is what the URL carries.
	Short      string    `json:"short"`
	Used       int64     `json:"used"`
	Referenced int64     `json:"referenced"`
	Created    time.Time `json:"created"`
	// CreateTXG is ZFS's transaction group: the only exact ordering. Two
	// snapshots taken in the same second share a Created, so a client deciding
	// which is newer has to compare this instead.
	CreateTXG uint64 `json:"create_txg"`
}

// SnapshotResult is what a snapshot call did.
type SnapshotResult struct {
	Snapshot  string         `json:"snapshot"`
	Created   []string       `json:"created"`
	Destroyed []string       `json:"destroyed"`
	Existed   bool           `json:"existed,omitempty"`
	Blocked   *SnapshotBlock `json:"blocked,omitempty"`
}

// SnapshotBlock names what stopped the operation. Nothing ran.
type SnapshotBlock struct {
	Reason string   `json:"reason"`
	Newer  []string `json:"newer,omitempty"`
	Clones []string `json:"clones,omitempty"`
	Holds  []string `json:"holds,omitempty"`
}

// SnapshotWriter is the snapshot half of a jail's storage. child names a
// dataset beneath the jail's own; empty means the jail's own dataset.
//
// recursive is the caller's, not the port's, because the two routes mean
// different things: snapshotting a jail takes it and its children at one point
// in time, and snapshotting a child takes that child alone.
type SnapshotWriter interface {
	Snapshots(ctx context.Context, jailPath string) ([]Snapshot, error)
	CreateSnapshot(ctx context.Context, jailPath, child, short string, recursive bool) (SnapshotResult, error)
	DestroySnapshot(ctx context.Context, jailPath, child, short, confirm string, recursive bool) (SnapshotResult, error)
	// Rollback is never recursive: zfs rollback cannot descend into children,
	// and its own -r means "destroy the snapshots newer than this one".
	Rollback(ctx context.Context, jailPath, child, short, confirm string, destroyNewer bool) (SnapshotResult, error)
}

// DatasetMove is what moving a dataset in or out of a jail did.
type DatasetMove struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Mountpoint string `json:"mountpoint"`
	// Remounted says whether the filesystem was actually carried to the new
	// mountpoint. False when the dataset was not mounted to begin with --
	// which is not a failure, just nothing to carry.
	Remounted bool `json:"remounted"`
}

// DatasetMover moves a child dataset out of a jail and back in.
//
// Destroying a jail takes its children and their snapshots with it (JD-007),
// so a dataset worth keeping has to stop being a child before the jail goes.
type DatasetMover interface {
	// Retain moves the jail's child dataset to <pool>/jails/retained/<name>.
	Retain(ctx context.Context, jailPath, child, name string) (DatasetMove, error)
	// Restore moves a retained dataset back in as the jail's child.
	Restore(ctx context.Context, jailPath, name, child string) (DatasetMove, error)
}

// DatasetDestroy is what destroying one of a jail's children took, or what
// stopped it (#120): the storage domain's answer, in this domain's terms.
type DatasetDestroy struct {
	Dataset   string        `json:"dataset"`
	Snapshots []string      `json:"snapshots"`
	Destroyed bool          `json:"destroyed"`
	Blocked   *DatasetBlock `json:"blocked,omitempty"`
}

// DatasetBlock names what kept a child from being destroyed: datasets of its
// own, dependent clones, holds. Nothing ran.
type DatasetBlock struct {
	Reason   string   `json:"reason"`
	Children []string `json:"children"`
	Clones   []string `json:"clones"`
	Holds    []string `json:"holds"`
}

// ChildDestroyer destroys one child of a jail and its snapshots, leaving the
// jail (#120). Until it, only destroying the whole jail removed a child.
type ChildDestroyer interface {
	DestroyChild(ctx context.Context, jailPath, child string) (DatasetDestroy, error)
}

// Reclone is what replacing a jail's root did, or got as far as doing.
type Reclone struct {
	Root string `json:"root"`
	From string `json:"from"`
	To   string `json:"to"`
	// OldRoot is where the previous root is parked. A re-clone destroys
	// nothing (JD-026).
	OldRoot  string   `json:"old_root,omitempty"`
	Children []string `json:"children"`
	// Running is the jail's state afterwards, which is the state it was found
	// in unless a step failed before it could be started again.
	Running bool `json:"running"`
	// FailedAt and Parked are the recovery information: which step failed, and
	// where every dataset that is no longer under the jail currently sits.
	Parked   []Parked `json:"parked,omitempty"`
	FailedAt string   `json:"failed_at,omitempty"`
}

// Parked is one dataset moved out of the jail and not yet moved back.
type Parked struct {
	Was string `json:"was"`
	Now string `json:"now"`
}

// Recloner replaces a jail's root dataset with a fresh clone of a template
// snapshot, keeping the jail's children. It expects the jail to be stopped
// already: the jails domain owns starting and stopping (JD-005), and this
// moves the filesystem the jail's processes run on.
type Recloner interface {
	// CheckReclone refuses a re-clone that cannot work, without touching
	// anything. Asked before the jail is stopped, so a mistyped template name
	// costs an error rather than an outage.
	CheckReclone(ctx context.Context, jailPath, templateSnapshot string) error
	Reclone(ctx context.Context, jailPath, templateSnapshot string) (Reclone, error)
}

// TemplateMaker turns a configured jail into a template (Handbook 17.4.3).
//
// The jail must be stopped when this is called; enforcing that is the service's
// job, because whether a jail is running is this domain's to know.
type TemplateMaker interface {
	MakeTemplate(ctx context.Context, jailPath, templateName string) (string, error)
}

// Root is a jail's own dataset, as the storage domain created it.
type Root struct {
	Dataset    string
	Mountpoint string
}

// RootCloner creates a jail's root by cloning a ready template's snapshot
// (JD-007). The jails domain defines it and the storage domain implements it,
// which is the direction JD-010 fixed.
type RootCloner interface {
	// CheckTemplate refuses a template that cannot be cloned, before anything
	// has been created. Asked first so a mistyped snapshot name costs an
	// error rather than a dataset nobody wanted.
	CheckTemplate(ctx context.Context, templateSnapshot string) error
	// CloneRoot clones templateSnapshot into the conventional root for a jail
	// of this name, <pool>/jails/containers/<name> (JD-021), and reports
	// where it ended up mounted. A root that already exists is refused by ZFS
	// rather than by a check here, because only ZFS can answer without racing.
	CloneRoot(ctx context.Context, templateSnapshot, name string) (Root, error)
}

// ConfigWriter puts a jail's generated stanza on disk as its own file in
// jail.conf.d (JD-030). The content is rendered in this package; the adapter
// only persists it and says where it went.
type ConfigWriter interface {
	// WriteConfig refuses to overwrite a file that is already there. A
	// configuration Jail Deck did not write this moment belongs to whoever
	// did write it.
	WriteConfig(ctx context.Context, name, content string) (string, error)
}

// Autostarter is rc.conf's jail_list. Membership means "start at boot" and
// nothing else: a jail is startable by name whether or not it is listed
// (JD-030).
type Autostarter interface {
	// SetAutostart returns the command it ran, for the audit log.
	SetAutostart(ctx context.Context, name string, enabled bool) (string, error)
}

// ConfigRemover deletes the file a jail was configured by, and says whether
// it is there at all.
//
// Kept apart from ConfigWriter for the reason DatasetSource and DatasetWriter
// are kept apart: creating a jail must not need the ability to delete another
// jail's configuration. One adapter implements both, which costs nothing.
type ConfigRemover interface {
	// ConfigFile reports where this jail's own file would be and whether it
	// exists. A jail declared in /etc/jail.conf has none, because Jail Deck
	// never writes that file (JD-030) -- so it also cannot take a jail out of
	// it, which is a destroy that has to be refused rather than half done.
	ConfigFile(ctx context.Context, name string) (path string, exists bool, err error)
	// RemoveConfig deletes the file and returns the path it deleted.
	RemoveConfig(ctx context.Context, name string) (string, error)
}

// RootDependents is what destroying a jail's root would take with it, and
// what would stop it.
//
// The two halves are deliberately separate. Children go with the jail by
// design (JD-007): a dataset worth keeping is moved out with retain before
// the jail is destroyed. Snapshots, clones and holds stop the destroy, because
// each is something somebody kept on purpose.
type RootDependents struct {
	// Root is the jail's own dataset.
	Root string `json:"root"`
	// Children are the datasets mounted beneath it, which are destroyed with
	// it. Named in the response because they go silently otherwise.
	Children []string `json:"children,omitempty"`
	// Snapshots are of the root or of a child, and each one blocks.
	Snapshots []string `json:"snapshots,omitempty"`
	// Clones are datasets made from one of those snapshots -- a template
	// cloned from this jail, or another jail re-cloned off it.
	Clones []string `json:"clones,omitempty"`
	Holds  []string `json:"holds,omitempty"`
}

// Blocking reports whether anything here stops the destroy.
func (d RootDependents) Blocking() bool {
	return len(d.Snapshots) > 0 || len(d.Clones) > 0 || len(d.Holds) > 0
}

// RootDestroyer destroys a jail's own dataset and everything mounted beneath
// it. Which zfs flags that takes is the storage domain's business (JD-030);
// this names the act.
type RootDestroyer interface {
	// CheckDestroy answers what a destroy would take and what would stop it,
	// touching nothing. Asked before the jail is stopped, so a jail that
	// cannot be destroyed is not left shut down to no purpose.
	CheckDestroy(ctx context.Context, jailPath string) (RootDependents, error)
	// DestroyRoot destroys the jail's root and returns the dataset it
	// destroyed. It is called last and checks nothing itself: by then the
	// jail is stopped and unconfigured, and ZFS refusing a dataset that is
	// still busy is the guard that cannot race (JD-030).
	DestroyRoot(ctx context.Context, jailPath string) (string, error)
}

// BridgeSource names the bridges the host has.
//
// Asked before a VNET jail is created, so a mistyped bridge name costs an
// error rather than a jail whose start fails after its epair already exists.
// The same list Host Setup reports (JD-017); Jail Deck never creates one.
type BridgeSource interface {
	Bridges(ctx context.Context) ([]string, error)
}

// EpairAllocator picks the epair unit a VNET jail's stanza is written
// against.
//
// The unit is fixed in the file rather than allocated at start time, because
// both ends have to be named before the jail runs: vnet.interface names the
// jail end, and exec.poststop names the host end. Measured: `ifconfig epair
// create` allocates a unit and prints only the "a" end, so the peer's name
// is not knowable in advance without choosing the unit.
type EpairAllocator interface {
	// FreeEpairUnit returns a unit as "epair7": one no interface currently
	// uses and no jail configuration already names. Two creates racing can
	// still be handed the same unit; the loser's start fails at `ifconfig
	// epair7 create: already exists` and is cleaned up like any other failed
	// start, which is a refusal rather than a wrong jail.
	FreeEpairUnit(ctx context.Context) (string, error)
}

// EpairCleaner destroys the epair a failed start left behind.
//
// This is the one piece of a half-created jail Jail Deck undoes (JD-030).
// When a start fails after exec.prestart has run, jail(8) does not run
// exec.poststop, and nothing afterwards will: `service jail stop` and
// `jail -r` both answer "no such jail" and so never run the hook. The orphan
// then blocks every retry at `ifconfig epair7 create: already exists`.
type EpairCleaner interface {
	// RemoveJailEpair destroys the interface described as "jail:<name>" and
	// returns what it destroyed, or "" when there was nothing to destroy.
	//
	// Found by the description rather than by the interface name, because a
	// sweep matching on name could take an interface belonging to something
	// else on the host. Destroying the host end takes the jail end and the
	// bridge membership with it -- measured, so no bridge name is needed
	// here and no second command can fail halfway.
	RemoveJailEpair(ctx context.Context, name string) (string, error)
}

// RctlSystem is the kernel's resource-limit facility: whether it is turned
// on, and what rules are keyed to a jail's name (JD-018).
//
// One port rather than three, because it is one facility with one switch.
// Nothing here *applies* a limit -- the generated stanza does that -- so
// every method is either a refusal's evidence or a cleanup.
type RctlSystem interface {
	// RacctEnabled reports kern.racct.enable. A kernel built without RACCT
	// has no such oid, which is false rather than an error: it is an
	// answer about the host, not a failure to ask.
	RacctEnabled(ctx context.Context) (bool, error)
	// JailRules lists the rules keyed to this jail's name, which is not the
	// same as the rules of a jail that is running: a rule can be added for a
	// name no jail answers to, and a jail removed without `rctl -r` leaves
	// its rules behind. Measured, both. No rules is an empty list, not an
	// error.
	JailRules(ctx context.Context, name string) ([]string, error)
	// RemoveJailRules clears them and returns the command that did it. A
	// name with no rules is not an error here, though rctl itself exits 1
	// for one -- the caller's question is whether any are left, not whether
	// this particular invocation found work.
	RemoveJailRules(ctx context.Context, name string) (string, error)
}

// DevfsRulesetSource lists the devfs rulesets the kernel has loaded.
//
// Asked before a jail that names one is created, and the one check in a
// create that exists for confinement rather than accuracy: measured on
// 15.1-RELEASE-p5, a devfs_ruleset the kernel does not have gives the jail
// the host's entire /dev -- disks, mem, kmem, io, bpf -- silently, while jls
// reports the number that was asked for (JD-018).
type DevfsRulesetSource interface {
	DevfsRulesetNumbers(ctx context.Context) ([]int, error)
}

// ConfigReader hands back a jail's stanza as it is on disk.
//
// Read rather than remembered, and compared rather than trusted: the
// idempotent PUT answers "does this jail already match the spec?" by
// rendering what the spec would write and holding it against this (JD-016).
type ConfigReader interface {
	// ConfigFile is ConfigRemover's, repeated here because a comparison has
	// to tell "no file of its own" from "a file that differs" before it
	// reads anything: the first is a refusal, the second is a diff.
	ConfigFile(ctx context.Context, name string) (path string, exists bool, err error)
	// ReadConfig returns the content of that file.
	ReadConfig(ctx context.Context, name string) (string, error)
}

// AutostartSource reads what Autostarter writes.
//
// Separate from Autostarter because they are asked at different times and
// by different callers -- a create and a destroy only ever set it, and only
// the comparison needs to read it back.
type AutostartSource interface {
	// Autostarts reports whether rc.conf's jail_list names this jail. A host
	// that has never set jail_list has an empty list, not an error.
	Autostarts(ctx context.Context, name string) (bool, error)
}

// CloneOrigin answers what a jail's root was cloned from, which is the one
// part of a spec that is neither in the jail's configuration nor in jls.
type CloneOrigin interface {
	// RootOrigin names the dataset mounted at the jail's path and the
	// snapshot it was cloned from.
	//
	// A root that is not a clone reports an empty origin, which is a fact
	// about the dataset rather than a failure -- a jail whose root was made
	// by hand has no template. A path no dataset is mounted at is
	// ErrJailNotOnZFS, as it is everywhere else here.
	RootOrigin(ctx context.Context, jailPath string) (dataset string, origin string, err error)
}

// JailExecutor runs a command inside a running jail (JD-016).
//
// The domain decides what may be asked for -- the argv, the caps, the
// refusals -- and this runs it. The adapter's job is the shape of the
// command line and the dialect of its failures, which is why the timeout
// arrives as a duration rather than as something already baked in.
type JailExecutor interface {
	// ExecInJail runs argv in the jail, with env on top of a clean login
	// environment, bounded by timeout.
	//
	// A command that ran and exited non-zero is **not** an error here: its
	// exit code is the answer. An error means the command could not be run
	// at all, or was stopped by the timeout.
	ExecInJail(ctx context.Context, name string, argv []string, env map[string]string,
		timeout time.Duration) (ExecResult, error)
}
