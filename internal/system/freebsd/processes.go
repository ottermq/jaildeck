package freebsd

import (
	"context"
	"strconv"
	"strings"

	"github.com/ottermq/jaildeck/internal/system"
)

// ResidentMemory is the resident set size of each of pids, in bytes, from
// one `ps -o pid= -o rss=` (#224). ps reports kilobytes.
//
// Two -o flags, not `-o pid=,rss=`: measured on FreeBSD 15.1, everything
// after = is the column's header, so that is one column, pid, headed
// ",rss=". A pid that has gone -- the front between a crash and its
// restart -- is simply missing from the answer, and ps still exits 0;
// exit 1 with the others printed is taken as an answer too.
func (a *Adapter) ResidentMemory(ctx context.Context, pids []int) (map[int]int64, error) {
	out := map[int]int64{}
	if len(pids) == 0 {
		return out, nil
	}
	list := make([]string, 0, len(pids))
	for _, pid := range pids {
		list = append(list, strconv.Itoa(pid))
	}
	result, err := a.runner.Run(ctx, system.Command{
		Name: "ps", Args: []string{"-o", "pid=", "-o", "rss=", "-p", strings.Join(list, ",")}, Timeout: metricsTimeout,
	})
	if err != nil && result.ExitCode != 1 {
		return nil, err
	}
	for line := range strings.SplitSeq(result.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		kb, rssErr := strconv.ParseInt(fields[1], 10, 64)
		if pidErr != nil || rssErr != nil {
			continue
		}
		out[pid] = kb * 1024
	}
	return out, nil
}
