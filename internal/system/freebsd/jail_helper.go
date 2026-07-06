package freebsd

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/otterlabs/jaildeck/internal/domain"
)

const (
	defaultJailConf    = "/etc/jail.conf"
	defaultJailConfDir = defaultJailConf + ".d"
)

var assignmentRE = regexp.MustCompile(`^\s*([a-zA-Z0-9_.-]+)\s*=\s*"?([^";]+)"?\s*;`)

func (a *Adapter) listConfiguredJails() ([]domain.Jail, error) {
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
	jails := make([]domain.Jail, 0, len(dirJails)+len(fileJails))
	jails = append(jails, dirJails...)
	for _, j := range fileJails {
		if ok := mapConfigJails[j.Name]; !ok {
			jails = append(jails, j)
		}
	}
	return jails, nil
}

func (a *Adapter) runningJails(ctx context.Context) ([]domain.Jail, error) {
	result, err := a.runner.Run(ctx, jlsListCommand)
	if err != nil {
		return nil, fmt.Errorf("list jails: %w", err)
	}
	parsedOutput, err := parseJLSOutput(result.Stdout)
	if err != nil {
		return nil, fmt.Errorf("fail parsing jls output: %w", err)
	}
	jails := make([]domain.Jail, len(parsedOutput.JailInformation.Jails))
	for idx, j := range parsedOutput.JailInformation.Jails {
		jails[idx] = domain.Jail{
			JID:      j.JID,
			Name:     j.Name,
			Status:   domain.JailStatusRunning,
			Hostname: j.Hostname,
			IP:       strings.Join(j.IP4, ", "),
			Path:     j.Path,
		}
	}
	return jails, nil
}

func parseJLSOutput(stdout string) (jlsOutput, error) {
	var output jlsOutput
	err := json.Unmarshal([]byte(stdout), &output)
	if err != nil {
		return jlsOutput{}, err
	}
	return output, nil
}

func mergeJails(configured, running []domain.Jail) []domain.Jail {
	maxLen := len(configured) + len(running)
	merged := make([]domain.Jail, 0, maxLen)
	runningNotConfigured := make([]domain.Jail, 0, len(running))
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
		log.Printf("[WARN] jail %s not found in configuration files", running.Name)
	}
	merged = append(merged, runningNotConfigured...)
	return merged
}

func listJailsFromConfDir(dir string) ([]domain.Jail, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.conf"))
	if err != nil {
		return nil, err
	}

	jails := make([]domain.Jail, 0, len(files))
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

func listJailsFromConfFile(filename string) ([]domain.Jail, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return parseJailConf(string(content)), nil
}

func parseJailConf(content string) []domain.Jail {
	lines := strings.Split(content, "\n")

	var jails []domain.Jail
	var jail domain.Jail
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

		matches := assignmentRE.FindStringSubmatch(line)
		if len(matches) == 3 {
			key := matches[1]
			value := resolveVars(matches[2], name)
			values[key] = value
		}

		if strings.HasSuffix(line, "}") {
			if name == "" {
				continue
			}
			jail = domain.Jail{
				Name:     name,
				Status:   domain.JailStatusStopped,
				Hostname: values["host.hostname"],
				IP:       values["ip4"],
				Path:     values["path"],
			}
			if jail.Hostname == "" {
				jail.Hostname = name
			}
			jails = append(jails, jail)
			name = ""
			values = map[string]string{}
			jail = domain.Jail{}
		}
	}

	return jails

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
	return value
}
