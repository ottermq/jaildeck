package jails

import (
	"context"
	"net/http"
	"unicode/utf8"

	"github.com/ottermq/jaildeck/internal/common"
)

// MaxShownFile is how much of a configuration file the Configuration view
// shows (#127, JD-048). A stanza Jail Deck writes is about a kilobyte; one
// past this is something else, and the view says it was cut.
const MaxShownFile = 64 << 10

// ConfigFile is one file as the host has it.
type ConfigFile struct {
	Path string `json:"path"`
	// Present is false for a file that is not there. That is an answer, not
	// an error: a jail configured in /etc/jail.conf has no file of its own.
	Present bool   `json:"present"`
	Content string `json:"content,omitempty"`
	// Truncated says Content stops at MaxShownFile.
	Truncated bool `json:"truncated,omitempty"`
}

// Configuration is what the host has for a jail: its own stanza, and the
// rctl rules keyed to its name, read now (JD-011).
type Configuration struct {
	Name string     `json:"name"`
	File ConfigFile `json:"file"`
	// Rules are `rctl jail:<name>`, empty when there are none. Absent, with
	// RulesError, when they could not be read: a kernel without RACCT, for
	// one.
	Rules      []string `json:"rules"`
	RulesError string   `json:"rules_error,omitempty"`
}

// Configuration reads a jail's stanza and rules as they are on the host, so
// that "what did Jail Deck write?" needs no shell (#127). Read only.
//
// A jail with no file of its own is answered with the path that is not there:
// it is configured in /etc/jail.conf, or nowhere, and that file is the
// operator's, so it is not cut up and shown in part.
func (s *JailService) Configuration(ctx context.Context, name JailName) (Configuration, error) {
	if s.configReader == nil {
		return Configuration{}, common.NewAppError(http.StatusNotImplemented,
			"this Jail Deck cannot read a jail's configuration")
	}
	jail, err := s.Get(ctx, name)
	if err != nil {
		return Configuration{}, err
	}
	result := Configuration{Name: jail.Name}
	path, exists, err := s.configReader.ConfigFile(ctx, jail.Name)
	if err != nil {
		return Configuration{}, err
	}
	result.File = ConfigFile{Path: path, Present: exists}
	if exists {
		content, err := s.configReader.ReadConfig(ctx, jail.Name)
		if err != nil {
			return Configuration{}, err
		}
		result.File.Content, result.File.Truncated = shown(content)
	}

	if s.rctl == nil {
		result.RulesError = "this Jail Deck cannot read rctl rules"
		return result, nil
	}
	rules, err := s.rctl.JailRules(ctx, jail.Name)
	if err != nil {
		result.RulesError = err.Error()
		return result, nil
	}
	if rules == nil {
		rules = []string{}
	}
	result.Rules = rules
	return result, nil
}

// shown cuts content at MaxShownFile, on a character boundary.
func shown(content string) (string, bool) {
	if len(content) <= MaxShownFile {
		return content, false
	}
	cut := MaxShownFile
	for cut > 0 && !utf8.RuneStart(content[cut]) {
		cut--
	}
	return content[:cut], true
}
