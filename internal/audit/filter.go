package audit

import (
	"net/url"
	"strings"
)

type Filter struct {
	Operation string
	Targets   []string
	Success   *bool
}

func parseFilter(q url.Values) Filter {
	var f Filter

	f.Operation = strings.ToLower(q.Get("operation"))

	switch q.Get("success") {
	case "true":
		ok := true
		f.Success = &ok

	case "false":
		ok := false
		f.Success = &ok
	}

	for part := range strings.SplitSeq(q.Get("targets"), ",") {
		if name := strings.TrimSpace(part); name != "" {
			f.Targets = append(f.Targets, name)
		}
	}

	return f
}
