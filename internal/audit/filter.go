package audit

import (
	"net/url"
	"strings"
)

type Filter struct {
	Operation string
	Targets   []string
	Success   *bool
	TimedOut  *bool
}

func parseFilter(q url.Values) Filter {
	var f Filter

	f.Operation = strings.ToLower(q.Get("operation"))

	f.Success = parseBool(q.Get("success"))
	f.TimedOut = parseBool(q.Get("timed_out"))

	for part := range strings.SplitSeq(q.Get("targets"), ",") {
		if name := strings.TrimSpace(part); name != "" {
			f.Targets = append(f.Targets, name)
		}
	}

	return f
}

func parseBool(v string) *bool {
	switch v {
	case "true":
		t := true
		return &t

	case "false":
		t := false
		return &t
	}
	return nil
}
