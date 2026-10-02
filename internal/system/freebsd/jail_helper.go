package freebsd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"codeberg.org/Otter-Labs/jaildeck/internal/common"
	"codeberg.org/Otter-Labs/jaildeck/internal/jails"
)

const (
	defaultJailConf    = "/etc/jail.conf"
	defaultJailConfDir = defaultJailConf + ".d"
)

var assignmentRE = regexp.MustCompile(`^\s*([a-zA-Z0-9_.-]+)\s*=\s*"?([^";]+)"?\s*;`)

func (a *Adapter) listConfiguredJails() ([]jails.Jail, error) {
	mapConfigJails := make(map[string]bool)
	dirJails, err := listJailsFromConfDir(defaultJailConfDir)
	if err != nil {
		return nil, err
	}
	for _, j := range dirJails {
		mapConfigJails[j.Name] = true
	}
	fileJails, err := listJailsFromConfFile(defaultJailConf)
	if err != nil {
		return nil, err
	}
	jailSlice := make([]jails.Jail, 0, len(dirJails)+len(fileJails))
	jailSlice = append(jailSlice, dirJails...)
	for _, j := range fileJails {
		if ok := mapConfigJails[j.Name]; !ok {
			jailSlice = append(jailSlice, j)
		}
	}
	return jailSlice, nil
}

func (a *Adapter) runningJails(ctx context.Context) ([]jails.Jail, error) {
	result, err := a.runner.Run(ctx, jlsListCommand)
	if err != nil {
		return nil, fmt.Errorf("list jails: %w", err)
	}
	parsedOutput, err := parseJLSOutput(result.Stdout)
	if err != nil {
		common.NewComponentLogger("freebsd").Debug("failed to parse jls output",
			"stderr", result.Stderr,
			slog.Any("err", err))
		return nil, fmt.Errorf("fail parsing jls output: %w", err)
	}
	jailSlice := make([]jails.Jail, len(parsedOutput.JailInformation.Jails))
	for idx, j := range parsedOutput.JailInformation.Jails {
		if j.Name == "" {
			// jls should never report a nameless jail. If it does, its schema
			// has changed and every name-keyed lookup below is unreliable.
			common.NewComponentLogger("freebsd").Warn("jls reported a jail with no name",
				"jid", j.JID)
		}
		jailSlice[idx] = jails.Jail{
			JID:      j.JID,
			Name:     j.Name,
			Status:   jails.JailStatusRunning,
			Hostname: j.Hostname,
			IP:       strings.Join(j.IP4, ", "),
			Path:     j.Path,
		}
	}
	return jailSlice, nil
}

func (a *Adapter) getJailByName(ctx context.Context, name string) (jails.Jail, error) {
	jailSlice, err := a.List(ctx)
	if err != nil {
		return jails.Jail{}, err
	}

	for _, jail := range jailSlice {
		if jail.Name == name {
			return jail, nil
		}
	}

	return jails.Jail{}, jails.ErrJailNotFound
}

func parseJLSOutput(stdout string) (jlsOutput, error) {
	var output jlsOutput
	err := json.Unmarshal([]byte(stdout), &output)
	if err != nil {
		return jlsOutput{}, err
	}
	return output, nil
}

func mergeJails(configured, running []jails.Jail) []jails.Jail {
	maxLen := len(configured) + len(running)
	merged := make([]jails.Jail, 0, maxLen)
	runningNotConfigured := make([]jails.Jail, 0, len(running))
	merged = append(merged, configured...)

	for _, running := range running {
		matched := false
		for idx, configured := range merged {
			if configured.Name == running.Name {
				merged[idx] = running
				matched = true
			}
		}
		if matched {
			continue
		}
		runningNotConfigured = append(runningNotConfigured, running)
		common.NewComponentLogger("freebsd").Warn("jail not found in configuration files",
			"jail", running.Name)
	}
	merged = append(merged, runningNotConfigured...)
	return merged
}

func listJailsFromConfDir(dir string) ([]jails.Jail, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.conf"))
	if err != nil {
		return nil, err
	}

	jails := make([]jails.Jail, 0, len(files))
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}

		parsed := parseJailConf(string(content))
		jails = append(jails, parsed...)
	}

	return jails, nil
}

func listJailsFromConfFile(filename string) ([]jails.Jail, error) {
	content, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseJailConf(string(content)), nil
}

func parseJailConf(content string) []jails.Jail {
	lines := strings.Split(content, "\n")

	var jailSlice []jails.Jail
	var jail jails.Jail
	var name string
	values := map[string]string{}

	for _, line := range lines {
		line = stripComment(line)
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		if before, ok := strings.CutSuffix(line, "{"); ok {
			name = strings.TrimSpace(before)
			continue
		}

		// understood tracks whether either branch below made sense of the line,
		// so only genuinely unrecognised syntax is reported. A line can be both
		// an assignment and the end of a block, hence a flag rather than if/else.
		understood := false

		matches := assignmentRE.FindStringSubmatch(line)
		if len(matches) == 3 {
			understood = true
			key := matches[1]
			value := resolveVars(matches[2], name)
			values[key] = value
		}

		if strings.HasSuffix(line, "}") {
			understood = true
			if name == "" {
				common.NewComponentLogger("freebsd").Debug("ignored closing brace outside a jail block",
					"line", line)
				continue
			}
			jail = jails.Jail{
				Name:     name,
				Status:   jails.JailStatusStopped,
				Hostname: values["host.hostname"],
				IP:       values["ip4"],
				Path:     values["path"],
			}
			if jail.Hostname == "" {
				jail.Hostname = name
			}
			jailSlice = append(jailSlice, jail)
			name = ""
			values = map[string]string{}
			jail = jails.Jail{}
		}

		if !understood {
			// The channel through which a jail.conf form this parser does not
			// know silently loses data. Valueless flags such as mount.devfs
			// land here legitimately; anything else is worth looking at.
			common.NewComponentLogger("freebsd").Debug("ignored unparsed jail.conf line",
				"jail", name, "line", line)
		}
	}

	return jailSlice

}

func stripComment(line string) string {
	if idx := strings.Index(line, "#"); idx >= 0 {
		return line[:idx]
	}
	return line
}

func resolveVars(value, name string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"`)
	value = strings.ReplaceAll(value, "${name}", name)
	value = strings.ReplaceAll(value, "$(name)", name)
	if strings.Contains(value, "$") {
		common.NewComponentLogger("freebsd").Debug("jail.conf value has an unresolved variable",
			"jail", name, "value", value)
	}
	return value
}
