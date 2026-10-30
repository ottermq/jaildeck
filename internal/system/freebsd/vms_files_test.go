package freebsd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/vms"
)

// filesHost is a datastore at a temporary directory holding jd-cap-cfgu as
// capture-vm-configuration-induced.sh left it: its configuration and the
// seed's user-data, 0600 as -u kept it (vcf_dir_u), and no network-config.
func filesHost(t *testing.T) (*Adapter, string) {
	t.Helper()
	store := t.TempDir()
	guest := filepath.Join(store, "jd-cap-cfgu")
	if err := os.MkdirAll(filepath.Join(guest, ".cloud-init"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"jd-cap-cfgu.conf":      captured(t, "vcf_conf_u"),
		".cloud-init/meta-data": "instance-id: 9a2db688\nlocal-hostname: jd-cap-cfgu\n",
		".cloud-init/user-data": captured(t, "vcf_user_data_u"),
	} {
		if err := os.WriteFile(filepath.Join(guest, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		if cmd.String() == "vm datastore list" {
			return system.CommandResult{Stdout: "NAME            TYPE        PATH                      ZFS DATASET\n" +
				"default         zfs         " + store + "                 tank/vm\n"}, nil
		}
		t.Fatalf("unexpected command %q", cmd.String())
		return system.CommandResult{}, nil
	}))
	return a, guest
}

func TestVMFilesReadsTheConfigurationAndTheSeed(t *testing.T) {
	a, guest := filesHost(t)
	files, err := a.VMFiles(context.Background(), "jd-cap-cfgu", vms.MaxShownFile)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, f := range files {
		names = append(names, f.Name)
	}
	if strings.Join(names, " ") != "jd-cap-cfgu.conf meta-data user-data network-config" {
		t.Fatalf("files = %v", names)
	}
	if files[0].Path != filepath.Join(guest, "jd-cap-cfgu.conf") || !strings.Contains(files[0].Content, `disk1_name="seed.iso"`) {
		t.Errorf("conf = %+v", files[0])
	}
	// The adapter reads as it is; masking is the service's.
	if !strings.Contains(files[2].Content, "$2a$10$jdcaptureplaceholder") {
		t.Errorf("user-data = %q", files[2].Content)
	}
	if files[3].Present || files[3].Path != filepath.Join(guest, ".cloud-init", "network-config") {
		t.Errorf("network-config = %+v", files[3])
	}
}

func TestVMFilesCutsAtTheLimit(t *testing.T) {
	a, _ := filesHost(t)
	files, err := a.VMFiles(context.Background(), "jd-cap-cfgu", 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(files[0].Content) != 16 || !files[0].Truncated {
		t.Errorf("conf = %+v", files[0])
	}
}

func TestVMFilesOfAGuestWithNoConfigurationIsNoSuchVM(t *testing.T) {
	a, _ := filesHost(t)
	if _, err := a.VMFiles(context.Background(), "jd-cap-cfgk", vms.MaxShownFile); !errors.Is(err, vms.ErrNoSuchVM) {
		t.Errorf("err = %v", err)
	}
}
