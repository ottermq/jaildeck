package common

import "testing"

func TestTransientHoldHint(t *testing.T) {
	tests := []struct {
		name  string
		holds []string
		want  bool
	}{
		{name: "none", holds: nil},
		{name: "a step hold", holds: []string{"p/j@zrepl_1 (zrepl_STEP_J_push)"}, want: true},
		{name: "two step holds", holds: []string{"p/j@a (zrepl_STEP_J_push)", "p/j@b (zrepl_STEP_J_push)"}, want: true},
		{name: "a kept hold", holds: []string{"p/j@a (keep)"}},
		{name: "a step and a kept hold", holds: []string{"p/j@a (zrepl_STEP_J_push)", "p/j@a (keep)"}},
		{name: "a replication cursor, not a step", holds: []string{"p/j@a (zrepl_last_received_J_sink)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TransientHoldHint(tt.holds) != ""; got != tt.want {
				t.Errorf("hinted = %v, want %v", got, tt.want)
			}
		})
	}
}
