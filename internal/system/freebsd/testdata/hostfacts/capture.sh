#!/bin/sh
# Capture the raw output of every command jaildeck's Host Setup readiness report and
# dataset layout need to parse (#1, #21). Read-only: nothing here changes the host.
#
# For each command, three files: .stdout, .stderr, .exit (the exit status).
# Not ".out": the repository's .gitignore excludes *.out as a Go coverage artifact, which
# would silently swallow every capture.
# All three matter -- "pf not loaded" is an empty stdout, a message on stderr and a
# non-zero exit, and a parser that only ever sees stdout cannot tell it from "disabled".
#
# No command here is piped or filtered. system.CommandRunner takes a name and an argument
# array and runs no shell, so the adapter cannot pipe either: a capture that greps is a
# capture of something the adapter can never see. Filter in Go, against the whole output.

OUT=/tmp/jd-capture
rm -rf "$OUT"
mkdir -p "$OUT"
: > "$OUT/MANIFEST"

# privilege helper: some of these are root-only, and jaildeck runs as root
PRIV=""
if [ "$(id -u)" -ne 0 ]; then
	if command -v doas >/dev/null 2>&1; then
		PRIV="doas"
	elif command -v sudo >/dev/null 2>&1; then
		PRIV="sudo -n"
	fi
fi

cap() {
	name=$1
	shift
	sh -c "$*" >"$OUT/$name.stdout" 2>"$OUT/$name.stderr"
	status=$?
	echo "$status" >"$OUT/$name.exit"
	printf '%s\t%s\texit=%s\n' "$name" "$*" "$status" >>"$OUT/MANIFEST"
}

# --- provenance -----------------------------------------------------------
cap version_u 'freebsd-version -u'
cap version_k 'freebsd-version -k'
cap uname 'uname -a'
cap hw_model 'sysctl -n hw.model'
cap hw_machine_arch 'sysctl -n hw.machine_arch'
cap hw_ncpu 'sysctl -n hw.ncpu'
# What a kern.cp_time tick is worth. stathz, not hz -- they differ by eight
# times on isengard, and a metrics endpoint dividing by the wrong one reports
# CPU seconds that look entirely plausible (JD-033).
cap kern_clockrate 'sysctl -n kern.clockrate'
cap kern_vm_guest 'sysctl -n kern.vm_guest'
# Uptime, because it explains whether the live dmesg buffer has had time to wrap.
cap uptime 'uptime'

# --- #1: racct ------------------------------------------------------------
cap sysctl_racct 'sysctl -n kern.racct.enable'
cap sysctl_racct_full 'sysctl kern.racct.enable'
cap sysctl_racct_absent 'sysctl -n kern.racct.nosuchknob'
cap rctl_rules "$PRIV rctl"

# --- #1: bridges ----------------------------------------------------------
cap ifconfig_bridge_group 'ifconfig -g bridge'
cap ifconfig_list 'ifconfig -l'

# --- #1: devfs rulesets ---------------------------------------------------
# The kernel is the authority, not the files: a host with no /etc/devfs.rules still has
# the rulesets /etc/defaults/devfs.rules ships, devfsrules_jail=4 among them. The files
# are captured only because they are where the NAMES live; the numbers come from devfs.
cap devfs_rule_showsets "$PRIV devfs rule showsets"
# The same command without privilege. devfs rule is root-only, and its refusal is the one
# "unknown" shape any check here can actually produce -- worth a fixture, not prose.
cap devfs_rule_showsets_unpriv 'devfs rule showsets'
for set in $(cat "$OUT/devfs_rule_showsets.stdout"); do
	cap "devfs_rule_show_$set" "$PRIV devfs rule -s $set show"
done
cap devfs_rules_etc "$PRIV cat /etc/devfs.rules"
cap devfs_rules_defaults "$PRIV cat /etc/defaults/devfs.rules"

# --- #1: cpu virtualization -----------------------------------------------
# /var/run/dmesg.boot FIRST. It is the boot-time messages written to a file by
# /etc/rc.d/dmesg, mode 0644, and it DOES NOT WRAP -- whereas the live kernel message
# buffer does: on a host up 83 days the VT-x line was gone from dmesg entirely while
# dmesg.boot still had it. Read the file; fall back to dmesg only if it is missing.
cap dmesg_boot 'cat /var/run/dmesg.boot'
# The live buffer too, so the difference between them stays visible in the fixtures.
# The VT-x line is indented, its feature list varies by CPU, and "UG" is a substring of
# "root HUB", which dmesg prints once per USB controller.
cap dmesg 'dmesg'
cap sysctl_vmm 'sysctl hw.vmm'
cap kldstat 'kldstat'
cap kldstat_vmm 'kldstat -m vmm'

# --- #1: pf ---------------------------------------------------------------
cap pfctl_info "$PRIV pfctl -s info"
cap pfctl_rules "$PRIV pfctl -s rules"
cap kldstat_pf 'kldstat -m pf'
cap service_pf_status "$PRIV service pf status"

# --- #1: exec.timeout -----------------------------------------------------
# /etc/jail.conf is where the handbook puts shared defaults -- and it does not exist on
# every host that runs jails. Capture its absence as carefully as its contents.
cap jail_conf 'cat /etc/jail.conf'
cap jail_conf_d 'ls -la /etc/jail.conf.d'
cap jail_conf_d_cat 'cat /etc/jail.conf.d/*.conf'
cap jls 'jls'
cap service_jail_status "$PRIV service jail status"

# --- #21: pool discovery and the dataset layout ---------------------------
cap zpool_list_h 'zpool list -H -o name'
cap zpool_list 'zpool list'

# Every pool, never a guess. A host can have several, and the order zpool prints them in
# means nothing: on one real host the first is the backup pool, which holds a replica of
# the jails layout with every mountpoint set to none. Picking it would report a host as
# provisioned off datasets mounted nowhere.
for pool in $(cat "$OUT/zpool_list_h.stdout"); do
	cap "zfs_list_r_$pool" "zfs list -H -o name,mountpoint -r $pool"
	cap "zfs_list_r_${pool}_jails" "zfs list -H -o name,mountpoint -r $pool/jails"
done
cap zfs_list_all 'zfs list -H -o name,mountpoint'
cap zfs_list_all_canmount 'zfs list -H -o name,mountpoint,canmount'

# The probes below are about the SHAPE OF FAILURE, not about this host's layout, so any
# existing pool will do and which one it is carries no meaning.
#
# A host may have NO pool at all -- JD-006 requires ZFS, and a host that does not meet
# that requirement is exactly what the zfs_pool check has to report on. Without this
# guard an empty pool list would splice an empty string into the commands below and
# capture the shape of a malformed command instead of the shape of a missing dataset.
PROBE=$(sed -n 1p "$OUT/zpool_list_h.stdout")
echo "$PROBE" >"$OUT/PROBE_POOL"

if [ -n "$PROBE" ]; then
	# THE IMPORTANT ONE. Naming several datasets where one is missing exits non-zero and
	# still prints the others on stdout -- a partial success that a parser treating
	# exit != 0 as total failure gets wrong. Verified here rather than asserted.
	cap zfs_list_mixed "zfs list -H -o name,mountpoint $PROBE $PROBE/definitely-not-here"
	cap zfs_list_absent "zfs list -H -o name,mountpoint $PROBE/definitely-not-here"
	cap zfs_get_mountpoint "zfs get -H -o value mountpoint $PROBE"
else
	echo "no pools on this host: skipped the per-pool probes" >"$OUT/PROBE_POOL"
	cap zfs_version 'zfs version'
	cap kldstat_zfs 'kldstat -m zfs'
fi
cap zfs_list_absent_pool 'zfs list -H -o name,mountpoint -r nosuchpool'

# --- #2, #3: storage visibility -------------------------------------------
# The Storage Visibility milestone fixes the batched read as
#   zfs list -H -o name,used,avail,refer,mountpoint <paths...>
# and that command is the reason this section exists: those three size columns
# come back HUMAN-READABLE ("96K", "1.2G", "-"), not bytes. A parser that wants
# numbers needs -p, which is the same query with different output. Both are
# captured, side by side, so the choice is made against bytes and not from
# memory of what zfs list looks like.
for pool in $(cat "$OUT/zpool_list_h.stdout"); do
	cap "zfs_list_space_r_$pool" "zfs list -H -o name,used,avail,refer,mountpoint -r $pool"
	cap "zfs_list_space_rp_$pool" "zfs list -H -p -o name,used,avail,refer,mountpoint -r $pool"
	# mounted vs canmount vs mountpoint are three different questions. JD-021
	# records the mounted state as deliberately unparsed for want of real
	# output; this is that output.
	cap "zfs_list_mounted_r_$pool" "zfs list -H -o name,mounted,canmount,mountpoint -r $pool"
	# Every locally-set property on every dataset of the pool, in ONE call --
	# the shape #2 requires ("one batched zfs list/zfs get call, not one per
	# jail"). -s local is what separates a user property that was set from the
	# ~50 native ones every dataset reports; a user property is one whose name
	# contains a colon.
	cap "zfs_get_local_r_$pool" "zfs get -H -r -o name,property,value,source -s local all $pool"
done

# A dataset to probe the per-dataset shapes on. Prefer whichever pool actually
# has the jails layout, and do NOT just take $PROBE: the first pool zpool prints
# is the wrong one on the deployment target, where it is the backup pool whose
# datasets are mounted nowhere (JD-021). Fall back to the first pool's root,
# which always exists, and record the choice either way.
PROBE_DS=""
for pool in $(cat "$OUT/zpool_list_h.stdout"); do
	if zfs list -H -o name "$pool/jails" >/dev/null 2>&1; then
		PROBE_DS="$pool/jails"
		break
	fi
done
[ -n "$PROBE_DS" ] || PROBE_DS="$PROBE"
echo "$PROBE_DS" >"$OUT/PROBE_DATASET"

if [ -n "$PROBE_DS" ]; then
	# -d 1 is immediate children only, which is what a jail listing wants next
	# to each jail; -d 0 is the dataset alone. Neither is the default.
	cap zfs_list_depth1 "zfs list -H -d 1 -o name,used,avail,refer,mountpoint $PROBE_DS"
	cap zfs_list_depth0 "zfs list -H -d 0 -o name,used,avail,refer,mountpoint $PROBE_DS"
	# The batched multi-path form the milestone names, including one path that
	# does not exist -- the partial success zfs_list_mixed found with
	# name,mountpoint, re-checked with the columns storage actually asks for.
	cap zfs_list_space_mixed "zfs list -H -o name,used,avail,refer,mountpoint $PROBE_DS $PROBE_DS/definitely-not-here"
	# All properties of one dataset, so the SOURCE column's whole vocabulary is
	# on record: local, default, "inherited from <dataset>", temporary, -.
	cap zfs_get_all "zfs get -H -o name,property,value,source all $PROBE_DS"
	cap zfs_get_local "zfs get -H -o name,property,value,source -s local all $PROBE_DS"
	cap zfs_get_used "zfs get -H -o value used $PROBE_DS"
	cap zfs_get_used_p "zfs get -H -p -o value used $PROBE_DS"
	# A user property that was never set, and a native property that does not
	# exist. These are NOT the same shape and the difference decides whether an
	# adapter treats "no such property" as an error or as absence.
	cap zfs_get_unset_userprop "zfs get -H -o name,property,value,source otter:instance $PROBE_DS"
	# The mount state as its own question (#5). Both zfs unmount and zfs mount
	# fail when it is wrong -- unmounting what is not mounted and mounting what
	# already is are each an error, not a no-op -- so a move has to ask first.
	cap zfs_get_mounted "zfs get -H -o value mounted $PROBE_DS"
	cap zfs_get_bad_property "zfs get -H -o value nosuchproperty $PROBE_DS"
	cap zfs_get_absent_dataset "zfs get -H -o value used $PROBE_DS/definitely-not-here"
	# Snapshots are a separate type, absent from a default zfs list, and the
	# creation column is a locale-formatted date unless -p makes it a Unix
	# timestamp -- note the DOUBLE space a single-digit hour produces. Both
	# forms, and -d 1 rather than -d 0: a dataset's snapshots are its depth-1
	# children, so -d 0 with -t snapshot filters the filesystem itself out and
	# returns exit 0 with NOTHING, which looks like "no snapshots".
	# Depth-limited at all on purpose: recursive over a pool these run to
	# hundreds of lines on a host running zrepl, 93% of them hourly snapshots
	# that teach a parser nothing and would rewrite the fixture on every
	# re-capture. One dataset's own snapshots carry the whole format.
	cap zfs_list_snapshots "zfs list -H -d 1 -t snapshot -o name,used,refer,creation $PROBE_DS"
	cap zfs_list_snapshots_p "zfs list -H -p -d 1 -t snapshot -o name,used,refer,creation $PROBE_DS"
	# -t all mixes filesystems and snapshots in one listing, so the type column
	# is the only thing telling them apart -- and a snapshot's mountpoint is -.
	cap zfs_list_type_all "zfs list -H -d 1 -t all -o name,type,used,refer,mountpoint $PROBE_DS"
	# The same query against a dataset that does not exist, which is NOT the
	# same as a dataset with no snapshots. (A host whose probe dataset simply
	# has none produces that second shape in zfs_list_snapshots above, as an
	# exit 0 with empty stdout -- see the README's table for which hosts do.)
	cap zfs_list_snapshots_absent "zfs list -H -d 1 -t snapshot -o name,used,refer,creation $PROBE_DS/definitely-not-here"
	# What stops a destroy, asked structurally instead of read out of the
	# error zfs would raise. A snapshot with neither answers "-" and nothing,
	# which is the shape the no-blockers path has to recognise.
	FIRST_SNAP=$(zfs list -H -d 1 -t snapshot -o name "$PROBE_DS" | head -1)
	if [ -n "$FIRST_SNAP" ]; then
		cap zfs_get_clones_none "zfs get -H -o value clones $FIRST_SNAP"
		cap zfs_holds_none "zfs holds -H $FIRST_SNAP"
	fi
fi

# --- #4: snapshots of one jail -------------------------------------------
# Scoped to a single jail's dataset, not to <pool>/jails. The adapter asks
# recursively so that a jail's child datasets are included, and recursive over
# the whole layout is 276 lines on this host, 256 of them hourly zrepl
# snapshots that teach a parser nothing and rewrite the fixture every hour.
# One jail is the scope the API actually uses and it carries the whole format.
# Prefer a jail that has a child dataset of its own, the same way PROBE_DS
# prefers a pool that has the layout: a jail with no children cannot show what
# a recursive snapshot produces, which is the format this capture exists for.
PROBE_JAIL=""
if [ -n "$PROBE_DS" ]; then
	for jail in $(zfs list -H -o name -d 1 "$PROBE_DS/containers" 2>/dev/null | sed 1d); do
		[ -z "$PROBE_JAIL" ] && PROBE_JAIL="$jail"
		if [ "$(zfs list -H -o name -d 1 "$jail" 2>/dev/null | sed 1d | wc -l)" -gt 0 ]; then
			PROBE_JAIL="$jail"
			break
		fi
	done
fi
echo "$PROBE_JAIL" >"$OUT/PROBE_JAIL"

if [ -n "$PROBE_JAIL" ]; then
	# The exact command the storage adapter runs. createtxg is why this is its
	# own capture: creation has second granularity, so two snapshots taken in
	# the same second tie on it, and "which of these is newer" is precisely the
	# question a rollback asks before deciding what to destroy. The transaction
	# group is ZFS's own total order and never ties.
	cap zfs_list_snapshots_txg "zfs list -H -p -t snapshot -o name,used,refer,creation,createtxg -r $PROBE_JAIL"
fi

# --- which disk a pool stands on (#83) ------------------------------------
# zpool status is laid out for a person; this prints the same tree tab-indented
# with one name per line, which is the only form a parser should be given.
# A leaf is whatever resolves to a real device -- mirror-0 and indirect-1 do
# not, and the join simply ignores them rather than enumerating vdev types.
cap zpool_list_vdevs 'zpool list -v -H -o name'

# Pools name labels as often as partitions: gpt/backup1 is ada3p1 is ada3.
cap glabel_status 'glabel status'

# The disk inventory, and the only authoritative join between the name the
# kernel uses and the name smartctl answers to. On this host ZFS stands on
# nda0 while smartctl will only open the same device as /dev/nvme0 -- it
# refuses /dev/nda0 outright -- so nothing matches those two by name. geom
# prints both the kernel name and the drive's serial, and smartctl prints the
# same serial, which closes the loop. Base system, and it needs no privilege.
cap geom_disk_list 'geom disk list'

# --- disk health (#83) ----------------------------------------------------
# smartctl is a PACKAGE, not base (sysutils/smartmontools). A host without it is
# a real state the report has to describe, so a missing binary here is a capture
# too, not a reason to stop.
#
# One call per device gets everything a disk report needs -- identity, the
# drive's own verdict, the attribute table and the self-test log -- and --json
# means there is nothing to parse by hand for once.
#
# Every device --scan lists is captured, not just the disks. The list is not a
# list of disks: it carries a DVD drive, an enclosure, and on this host the same
# NVMe twice under two drivers. What each of those answers with is the whole
# reason a parser can tell them apart.
if command -v smartctl >/dev/null 2>&1; then
	cap smartctl_version 'smartctl --version'
	# The scan needs privilege too. Unprivileged it prints
	# "# scan_smart_devices: Permission denied" and exits 0, and a loop that
	# believed it would go on to capture a device named "#".
	cap smartctl_scan "$PRIV smartctl --scan"

	for dev in $($PRIV smartctl --scan | awk '$1 !~ /^#/ { print $1 }'); do
		cap "smartctl_$(basename "$dev")" \
			"$PRIV smartctl --json -i -H -A -l selftest $dev"
	done

	# A device that is not there, and a caller that is not root: both are
	# ordinary states, and both have their own exit status.
	cap smartctl_absent "$PRIV smartctl --json -i -H -A /dev/ada9"
	if [ -n "$PRIV" ]; then
		cap smartctl_unpriv 'smartctl --json -i -H -A /dev/ada3'
	fi
else
	cap smartctl_version 'smartctl --version'
fi

# --- redaction ------------------------------------------------------------
# dmesg names every NIC's MAC address. Nothing any parser reads is affected, and these
# fixtures live in a public repository, so mask them. This is the only edit the script
# makes to any captured byte, and it is reproducible.
for f in "$OUT"/*.stdout; do
	sed -E 's/([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}/xx:xx:xx:xx:xx:xx/g' "$f" >"$f.masked" &&
		mv "$f.masked" "$f"
done

# Drive serial numbers. Same reasoning as the MACs, with one constraint the MACs
# did not have: the report JOINS smartctl's serial_number to geom's ident to
# learn which kernel device a drive is, so the two have to mask to the SAME
# placeholder -- and two drives must mask to DIFFERENT ones, or a fixture would
# assert a join that could never happen. Blanking each serial to a run of X
# would do exactly that: isengard's ada2 and ada3 both have eight-character
# serials.
#
# So: collect every serial the capture holds, give each a distinct placeholder
# of its own length, and substitute them everywhere. Longest first, so a serial
# that is a prefix of another cannot eat it.
serials="$OUT/.serials"
script="$OUT/.serials.sed"
: >"$serials"
for f in "$OUT"/smartctl_*.stdout; do
	[ -f "$f" ] || continue
	sed -n 's/.*"serial_number": *"\([^"]*\)".*/\1/p' "$f" >>"$serials"
done
if [ -f "$OUT/geom_disk_list.stdout" ]; then
	sed -n 's/^[[:space:]]*ident:[[:space:]]*\(..*\)$/\1/p' "$OUT/geom_disk_list.stdout" >>"$serials"
fi
sort -u "$serials" | awk '
	# At least four characters and no whitespace. A real serial is neither
	# short nor spaced, and the DVD drive on isengard has an ident of one
	# single space -- masking that would rewrite every space in every file.
	{ if (length($0) >= 4 && $0 !~ /[[:space:]]/) keep[n++] = $0 }
	END {
		# longest first
		for (i = 0; i < n; i++)
			for (j = i + 1; j < n; j++)
				if (length(keep[j]) > length(keep[i])) { t = keep[i]; keep[i] = keep[j]; keep[j] = t }
		for (i = 0; i < n; i++) {
			p = sprintf("S%02d", i + 1)
			while (length(p) < length(keep[i])) p = p "X"
			printf "s|%s|%s|g\n", keep[i], substr(p, 1, length(keep[i]))
		}
	}
' >"$script"
if [ -s "$script" ]; then
	for f in "$OUT"/*.stdout; do
		sed -f "$script" "$f" >"$f.masked" && mv "$f.masked" "$f"
	done
fi
rm -f "$serials" "$script"

# smartctl also prints each drive's unit-unique WWN, which identifies it just as
# well as the serial does.
for f in "$OUT"/smartctl_*.stdout; do
	[ -f "$f" ] || continue
	awk '
		{
			# The WWN block only. Every SMART attribute carries an "id" too,
			# and masking those would turn attribute 184 into attribute 0.
			if ($0 ~ /"wwn"/) in_wwn = 1
			if (in_wwn && $0 ~ /"id":/) { sub(/"id": [0-9]+/, "\"id\": 0"); in_wwn = 0 }
			print
		}
	' "$f" >"$f.masked" && mv "$f.masked" "$f"
done

printf '\n=== captured to %s ===\n' "$OUT"
cat "$OUT/MANIFEST"
