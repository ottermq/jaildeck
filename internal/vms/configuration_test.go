package vms

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// guestFiles answers VMFiles from a map, as the adapter would read them.
type guestFiles struct {
	files []GuestFile
	err   error
	asked []string
}

func (g *guestFiles) VMFiles(_ context.Context, name string, limit int) ([]GuestFile, error) {
	g.asked = append(g.asked, name)
	if limit != MaxShownFile {
		return nil, errors.New("not the view's limit")
	}
	return g.files, g.err
}

// The user-data capture-vm-configuration-induced.sh made with -u (vcf_user_data_u).
const capturedUserData = `#cloud-config
resize_rootfs: True
manage_etc_hosts: localhost
ssh_authorized_keys:
  - "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFMz6uwq6uCySEimJJLIKB1qaSuK3GpjZ0BSxasBIfDG jd-capture"
password: "$2a$10$jdcaptureplaceholderhashXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
chpasswd:
  expire: false
ssh_pwauth: false
`

func filesService(files *guestFiles) *Service {
	s, _ := newMakerService(newMaker())
	s.SetFiles(files)
	return s
}

func TestTheConfigurationMasksThePasswordAndNothingElse(t *testing.T) {
	files := &guestFiles{files: []GuestFile{
		{Name: "existing.conf", Path: "/vm/existing/existing.conf", Present: true, Content: "cpu=1\nmemory=256M\n"},
		{Name: "meta-data", Path: "/vm/existing/.cloud-init/meta-data", Present: true, Content: "local-hostname: existing\n"},
		{Name: "user-data", Path: "/vm/existing/.cloud-init/user-data", Present: true, Content: capturedUserData},
		{Name: "network-config", Path: "/vm/existing/.cloud-init/network-config"},
	}}
	got, err := filesService(files).Configuration(context.Background(), "existing")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "existing" || len(got.Files) != 4 {
		t.Fatalf("got = %+v", got)
	}
	user := got.Files[2]
	if strings.Contains(user.Content, "$2a$") || strings.Contains(user.Content, "placeholder") {
		t.Errorf("the hash is shown:\n%s", user.Content)
	}
	want := strings.Replace(capturedUserData,
		`password: "$2a$10$jdcaptureplaceholderhashXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"`, "password: (masked)", 1)
	if user.Content != want || !reflect.DeepEqual(user.Masked, []string{"password"}) {
		t.Errorf("user-data =\n%s\nmasked %v", user.Content, user.Masked)
	}
	// ssh_pwauth and chpasswd are settings, not secrets; the key is public.
	for _, kept := range []string{"ssh_pwauth: false", "expire: false", "jd-capture"} {
		if !strings.Contains(user.Content, kept) {
			t.Errorf("%q was taken out", kept)
		}
	}
	if got.Files[0].Masked != nil || got.Files[3].Present {
		t.Errorf("files = %+v", got.Files)
	}
}

func TestEveryPasswordKeyIsMasked(t *testing.T) {
	content := "users:\n  - name: ops\n    passwd: $6$abc\n    hashed_passwd: $6$def\n    plain_text_passwd: hunter2\n" +
		"  - password: inline\nssh_pwauth: true\n# password: in a comment stays a comment\n"
	got, keys := mask(content)
	for _, secret := range []string{"$6$abc", "$6$def", "hunter2", "inline"} {
		if strings.Contains(got, secret) {
			t.Errorf("%q is shown:\n%s", secret, got)
		}
	}
	if !reflect.DeepEqual(keys, []string{"passwd", "hashed_passwd", "plain_text_passwd", "password"}) {
		t.Errorf("keys = %v", keys)
	}
	if !strings.Contains(got, "ssh_pwauth: true") || !strings.Contains(got, "# password: in a comment") {
		t.Errorf("more than the values went:\n%s", got)
	}
}

func TestTheConfigurationOfAGuestNotThereIs404(t *testing.T) {
	files := &guestFiles{}
	_, err := filesService(files).Configuration(context.Background(), "web")
	wantAppError(t, err, http.StatusNotFound, "web")
	if len(files.asked) != 0 {
		t.Errorf("read the files of a guest vm-bhyve does not list: %v", files.asked)
	}

	files = &guestFiles{err: ErrNoSuchVM}
	_, err = filesService(files).Configuration(context.Background(), "existing")
	wantAppError(t, err, http.StatusNotFound, "default datastore")

	_, err = filesService(files).Configuration(context.Background(), "../etc")
	wantAppError(t, err, http.StatusBadRequest, "not a vm-bhyve guest name")
}

func TestTheConfigurationNeedsTheFiles(t *testing.T) {
	s, _ := newMakerService(newMaker())
	_, err := s.Configuration(context.Background(), "existing")
	wantAppError(t, err, http.StatusNotImplemented, "cannot read a guest's files")
}

func TestACutFileLosesHalfACharacterAndNoMore(t *testing.T) {
	if got := runeCut("abc\xc3"); got != "abc" {
		t.Errorf("%q", got)
	}
	if got := runeCut("abcé"); got != "abcé" {
		t.Errorf("%q", got)
	}
	if got := runeCut("\xff\xff\xff\xff\xff"); got != "\xff\xff" {
		t.Errorf("a binary file was eaten: %q", got)
	}
}
