package common

import "strings"

// transientHoldTag is the tag zrepl puts on a snapshot while a replication
// step that uses it is in progress, and releases when the step ends (#280).
const transientHoldTag = "zrepl_STEP"

// TransientHoldHint is a sentence to add to a refusal when every hold in the
// way is a replication step's, which goes away by itself: retrying later is the
// answer, not releasing it. Empty when any hold is not one, or there are none.
// holds are "<snapshot> (<tag>)", as every refusal lists them.
func TransientHoldHint(holds []string) string {
	if len(holds) == 0 {
		return ""
	}
	for _, hold := range holds {
		open := strings.LastIndex(hold, " (")
		if open < 0 || !strings.HasPrefix(hold[open+2:], transientHoldTag) {
			return ""
		}
	}
	return " Every hold is a replication step's (zrepl_STEP...), which is released when " +
		"that step finishes: retry in a minute or two rather than releasing it."
}
