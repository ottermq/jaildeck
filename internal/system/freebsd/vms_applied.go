package freebsd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v2"

	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/vms"
)

// HostCPUs is hw.ncpu: 16 on isengard, where host CPU 999 made bhyve abort
// at start with status 134 while vm start exited 0 (JD-041).
func (a *Adapter) HostCPUs(ctx context.Context) (int, error) {
	result, err := a.runHost(ctx, system.Command{Name: "sysctl", Args: []string{"-n", "hw.ncpu"}})
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(result.Stdout))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("hw.ncpu is %q", strings.TrimSpace(result.Stdout))
	}
	return n, nil
}

// VMPinCPUs writes the pin into the guest's configuration the way vm-bhyve's
// own config::set writes a setting:
//
//	sysrc -f <datastore>/<name>/<name>.conf bhyve_options="-p 0:2 -p 1:3"
//
// Measured on isengard: vm-bhyve hands bhyve_options to bhyve as they are,
// and each vCPU thread's cpuset was then the one host CPU asked for.
func (a *Adapter) VMPinCPUs(ctx context.Context, name string, cpus []int) (string, error) {
	dir, _, err := a.vmDatastore(ctx)
	if err != nil {
		return "", err
	}
	cmd := system.Command{Name: "sysrc", Args: []string{"-f", vmConfPath(dir, name),
		"bhyve_options=" + vms.PinOptions(cpus)}}
	_, err = a.runHost(ctx, cmd)
	return cmd.String(), err
}

// VMApplied reads what a guest in the default datastore was made with:
//
//	<datastore>/<name>/<name>.conf      cpu, memory, bhyve_options, network<N>_switch
//	<datastore>/<name>/.cloud-init/     user-data always; network-config only for a static address
//	zfs list -H -p -o volsize,origin <dataset>/<name>/disk0
//
// vm-bhyve writes network-config only when vm create was given -n; without
// it the guest takes DHCP and there is no file (read in its source,
// core::create_cloud_init).
func (a *Adapter) VMApplied(ctx context.Context, name string) (vms.Applied, error) {
	dir, dataset, err := a.vmDatastore(ctx)
	if err != nil {
		return vms.Applied{}, err
	}
	content, err := os.ReadFile(vmConfPath(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return vms.Applied{}, vms.ErrNoSuchVM
	}
	if err != nil {
		return vms.Applied{}, err
	}
	conf := parseVMConf(string(content))
	applied := vms.Applied{CPU: conf["cpu"], Memory: conf["memory"], BhyveOptions: conf["bhyve_options"]}
	for n := 0; ; n++ {
		prefix := "network" + strconv.Itoa(n)
		_, typed := conf[prefix+"_type"]
		sw, switched := conf[prefix+"_switch"]
		if !typed && !switched {
			break
		}
		applied.Switches = append(applied.Switches, sw)
	}

	seed := filepath.Join(dir, name, ".cloud-init")
	userData, err := os.ReadFile(filepath.Join(seed, "user-data"))
	switch {
	case err == nil:
		applied.Seed = true
		if applied.SSHKeys, err = parseUserDataKeys(userData); err != nil {
			return vms.Applied{}, fmt.Errorf("reading %s's user-data: %w", name, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return vms.Applied{}, err
	}
	if networkConfig, err := os.ReadFile(filepath.Join(seed, "network-config")); err == nil {
		if err := parseNetworkConfig(networkConfig, &applied); err != nil {
			return vms.Applied{}, fmt.Errorf("reading %s's network-config: %w", name, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return vms.Applied{}, err
	}

	if dataset != "" {
		disk := dataset + "/" + name + "/disk0"
		result, err := a.runHost(ctx, system.Command{Name: "zfs", Args: []string{
			"list", "-H", "-p", "-o", "volsize,origin", disk}})
		switch {
		case err == nil:
			fields := strings.Fields(result.Stdout)
			if len(fields) == 2 {
				applied.DiskSize, _ = strconv.ParseInt(fields[0], 10, 64)
				applied.Origin = dashless(fields[1])
			}
		case exited(result, err) && strings.Contains(result.Stderr, "dataset does not exist"):
			// No disk0 on ZFS: a guest with a file disk, or none.
		default:
			return vms.Applied{}, err
		}
	}
	return applied, nil
}

func vmConfPath(dir, name string) string {
	return filepath.Join(dir, name, name+".conf")
}

// parseVMConf reads a vm-bhyve configuration, which is sh assignments as
// sysrc writes them: key="value", or key=value with no quotes, as a template
// may have it (cpu=2 in Jail Deck's own).
func parseVMConf(content string) map[string]string {
	conf := map[string]string{}
	for _, line := range nonEmptyLines(content) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		conf[strings.TrimSpace(key)] = value
	}
	return conf
}

// parseUserDataKeys is the default user's keys from vm-bhyve's user-data:
//
//	ssh_authorized_keys:
//	  - ssh-ed25519 AAAA... jd-capture
func parseUserDataKeys(content []byte) ([]string, error) {
	var userData struct {
		Keys []string `yaml:"ssh_authorized_keys"`
	}
	if err := yaml.Unmarshal(content, &userData); err != nil {
		return nil, err
	}
	return userData.Keys, nil
}

// parseNetworkConfig reads the first NIC of vm-bhyve's network-config:
//
//	ethernets:
//	  id0:
//	    addresses: [10.77.0.10/24]
//	    nameservers:
//	      addresses: [10.77.0.1]
//	    routes:
//	      - to: 0.0.0.0/0
//	        via: 10.77.0.1
func parseNetworkConfig(content []byte, applied *vms.Applied) error {
	var config struct {
		Ethernets map[string]struct {
			Addresses   []string `yaml:"addresses"`
			Nameservers struct {
				Addresses []string `yaml:"addresses"`
			} `yaml:"nameservers"`
			Routes []struct {
				To  string `yaml:"to"`
				Via string `yaml:"via"`
			} `yaml:"routes"`
		} `yaml:"ethernets"`
	}
	if err := yaml.Unmarshal(content, &config); err != nil {
		return err
	}
	nic, ok := config.Ethernets["id0"]
	if !ok {
		return nil
	}
	if len(nic.Addresses) > 0 {
		applied.Address = nic.Addresses[0]
	}
	applied.Nameservers = nic.Nameservers.Addresses
	for _, route := range nic.Routes {
		if route.To == "0.0.0.0/0" || route.To == "::/0" {
			applied.Gateway = route.Via
			break
		}
	}
	return nil
}
