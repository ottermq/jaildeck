package vms

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/ottermq/jaildeck/internal/common"
)

// MaxShownFile is how much of a file the Configuration view shows (#127,
// JD-048). A guest's configuration and seed are a few hundred bytes.
const MaxShownFile = 64 << 10

// GuestFile is one of a guest's files as the host has it.
type GuestFile struct {
	// Name is what the file is to the guest: its configuration, or one of
	// the cloud-init seed's.
	Name string `json:"name"`
	Path string `json:"path"`
	// Present is false for a file that is not there, which is an answer: a
	// guest made without a seed has no .cloud-init.
	Present bool   `json:"present"`
	Content string `json:"content,omitempty"`
	// Truncated says Content stops at MaxShownFile.
	Truncated bool `json:"truncated,omitempty"`
	// Masked names the keys whose values were replaced: a console password's
	// hash, which bcrypt or not can be guessed at offline.
	Masked []string `json:"masked,omitempty"`
}

// Configuration is what the host has for a guest, read now (JD-011).
type Configuration struct {
	Name  string      `json:"name"`
	Files []GuestFile `json:"files"`
}

// GuestFiles reads a guest's configuration and its seed's files off the
// datastore, as they are. The adapter's, optional: a host without it answers
// 501 for the view and nothing else changes.
type GuestFiles interface {
	// VMFiles reads <name>.conf and .cloud-init's meta-data, user-data and
	// network-config, in that order, each at most limit bytes and said to be
	// cut past it. ErrNoSuchVM when the guest has no configuration there.
	VMFiles(ctx context.Context, name string, limit int) ([]GuestFile, error)
}

// SetFiles is set after construction, where the system can read them.
func (s *Service) SetFiles(f GuestFiles) { s.files = f }

// secretKey is a cloud-config key whose value is a password or its hash, at
// the start of a line. password is the one Jail Deck writes (#152); the others
// are cloud-init's for users, which an operator's own user-data may carry.
var secretKey = regexp.MustCompile(`(?m)^(\s*-?\s*(password|passwd|hashed_passwd|plain_text_passwd)\s*:)[ \t]*\S.*$`)

// Configuration reads the guest's files as the host has them, with any
// password value masked (#127). Read only.
func (s *Service) Configuration(ctx context.Context, name string) (Configuration, error) {
	if s.system == nil {
		return Configuration{}, s.unavailable()
	}
	if s.files == nil {
		return Configuration{}, common.NewAppError(http.StatusNotImplemented,
			"this Jail Deck cannot read a guest's files")
	}
	if _, err := s.guest(ctx, name); err != nil {
		return Configuration{}, err
	}
	files, err := s.files.VMFiles(ctx, name, MaxShownFile)
	if errors.Is(err, ErrNoSuchVM) {
		return Configuration{}, common.NewAppError(http.StatusNotFound, strconv.Quote(name)+
			" has no configuration in the default datastore, which is the only one Jail Deck reads")
	}
	if err != nil {
		return Configuration{}, readError("the files of virtual machine "+strconv.Quote(name), err)
	}
	for i := range files {
		files[i].Content, files[i].Masked = mask(files[i].Content)
		if files[i].Truncated {
			files[i].Content = runeCut(files[i].Content)
		}
	}
	return Configuration{Name: name, Files: files}, nil
}

// mask replaces every password value, keeping the key so the reader sees one
// was set.
func mask(content string) (string, []string) {
	var keys []string
	masked := secretKey.ReplaceAllStringFunc(content, func(line string) string {
		m := secretKey.FindStringSubmatch(line)
		keys = append(keys, m[2])
		return m[1] + " (masked)"
	})
	return masked, keys
}

// runeCut drops the bytes of a character the byte limit cut in half: at most
// three, so a file that was never text is not eaten from the end.
func runeCut(content string) string {
	for i := 0; i < utf8.UTFMax-1 && len(content) > 0; i++ {
		if r, size := utf8.DecodeLastRuneInString(content); r != utf8.RuneError || size != 1 {
			break
		}
		content = content[:len(content)-1]
	}
	return content
}
