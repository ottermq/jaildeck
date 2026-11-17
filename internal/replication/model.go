// Package replication reports how long ago each dataset a zrepl job
// replicates last reached the other side (#247, JD-062).
//
// The answer comes from zrepl's replication cursors: a bookmark zrepl keeps
// on the sending side of every filesystem it replicates, one per job,
// <fs>#zrepl_CURSOR_G_<guid>_J_<job>. A bookmark keeps the creation time of
// the snapshot it was made from, so a cursor's creation time is the newest
// snapshot known to have reached the other side -- for a push job, and for
// a source job another host pulls from, which the sender otherwise cannot
// know about. Reading them needs neither zrepl's configuration nor its
// control socket, and nothing here talks to zrepl.
package replication

import "time"

// Cursor is one replication cursor, as the adapter reads it.
type Cursor struct {
	Dataset string
	Job     string
	// Snapshot is the creation time of the snapshot the cursor marks.
	Snapshot time.Time
}

// Status is what the last read found.
type Status string

const (
	// Replicating: cursors were read, and there is at least one.
	Replicating Status = "replicating"
	// None: cursors were read, and there are none. zrepl is not replicating
	// anything from this host, or has never finished a first replication.
	None Status = "none"
	// Unknown: the cursors could not be read. Never read as either of the
	// others.
	Unknown Status = "unknown"
	// Unchecked: the first read has not finished.
	Unchecked Status = "unchecked"
)

// Dataset is one dataset a job replicates.
type Dataset struct {
	Name string `json:"name"`
	// Replicated is when the newest snapshot that reached the other side
	// was taken.
	Replicated time.Time `json:"replicated"`
}

// Job is one zrepl job, by the name its cursors carry.
type Job struct {
	Name string `json:"name"`
	// Oldest is the earliest Replicated among its datasets: how far behind
	// the job is at worst.
	Oldest   time.Time `json:"oldest"`
	Datasets []Dataset `json:"datasets"`
}

// Snapshot is the last read.
type Snapshot struct {
	Status Status `json:"status" enums:"replicating,none,unknown,unchecked"`
	// Checked is when the cursors were last read successfully. A failed
	// read leaves it where it was, so its age says how stale this is.
	Checked *time.Time `json:"checked,omitempty"`
	// Reason says why the status is unknown or unchecked.
	Reason string `json:"reason,omitempty"`
	// Jobs is sorted by name, and each job's datasets by name.
	Jobs []Job `json:"jobs"`
}
