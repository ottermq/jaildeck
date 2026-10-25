#!/bin/sh
# Destructive integration test. Run only in a fresh, disposable FreeBSD VM.
# Deliberately leaves the operator-data fixtures behind for inspection.
set -eu
export LC_ALL=C
fail() { echo "lifecycle: $*" >&2; exit 1; }
[ "${JAILDECK_TEST_DISPOSABLE:-}" = YES ] || fail 'set JAILDECK_TEST_DISPOSABLE=YES in a disposable VM only.'
[ "$(uname -s)" = FreeBSD ] && [ "$(id -u)" = 0 ] || fail 'requires root on FreeBSD.'
[ "$#" = 3 ] || fail 'usage: test-lifecycle.sh OLD.pkg NEW.pkg ZFS_POOL'
old=$1
new=$2
pool=$3
for tool in pkg curl sha256 zfs; do
    command -v "$tool" >/dev/null || fail "missing prerequisite: $tool"
done
for archive in "$old" "$new"; do
    [ "$(pkg query -F "$archive" '%n')" = jaildeck ] || fail 'expected jaildeck packages.'
done
oldversion=$(pkg query -F "$old" '%v')
newversion=$(pkg query -F "$new" '%v')
[ "$(pkg version -t "$oldversion" "$newversion")" = '<' ] || fail 'NEW must have a newer version.'
if pkg info -e jaildeck || pgrep -x jaildeck >/dev/null; then
    fail 'an installed or running Jaildeck already exists.'
fi
for item in /usr/local/bin/jaildeck /usr/local/etc/rc.d/jaildeck \
    /usr/local/etc/jaildeck /var/db/jaildeck /var/log/jaildeck /usr/local/jails/package-test; do
    [ ! -e "$item" ] || fail "existing operator/application path: $item"
done
dataset="$pool/jaildeck-package-test"
zfs list "$pool" >/dev/null
if zfs list "$dataset" >/dev/null 2>&1; then fail "existing dataset: $dataset"; fi
work=$(mktemp -d /tmp/jaildeck-lifecycle.XXXXXX)
echo "lifecycle: evidence directory $work"
running=NO
cleanup() {
    if [ "$running" = YES ]; then service jaildeck onestop || true; fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

zfs list -H -o name,mountpoint | sort > "$work/datasets.before"
ifconfig -l > "$work/interfaces.before"
sha256 -q /etc/rc.conf > "$work/rc.before"
pkg add "$old"
pkg info jaildeck
pkg check -s jaildeck
pkg query '%Fp' jaildeck | sort > "$work/files"
printf '%s\n' /usr/local/bin/jaildeck /usr/local/etc/jaildeck/jaildeck.env.sample \
    /usr/local/etc/rc.d/jaildeck | sort > "$work/expected-files"
diff -u "$work/expected-files" "$work/files"
# The binary reports vX.Y.Z; pkg's version has no v (see package.sh).
[ "$(jaildeck version)" = "v$oldversion" ] || fail 'installed version mismatch.'
[ "$(stat -f '%Su:%Sg:%Lp' /usr/local/bin/jaildeck)" = root:wheel:555 ] || fail 'binary permissions.'
[ "$(stat -f '%Su:%Sg:%Lp' /usr/local/etc/rc.d/jaildeck)" = root:wheel:555 ] || fail 'service permissions.'
[ "$(stat -f '%Su:%Sg:%Lp' /usr/local/etc/jaildeck/jaildeck.env.sample)" = root:wheel:644 ] || fail 'sample permissions.'
for item in /usr/local/etc/jaildeck/jaildeck.env /var/db/jaildeck /var/log/jaildeck; do
    [ ! -e "$item" ] || fail "install created operator data: $item"
done
zfs list -H -o name,mountpoint | sort > "$work/datasets.after"
ifconfig -l > "$work/interfaces.after"
sha256 -q /etc/rc.conf > "$work/rc.after"
diff -u "$work/datasets.before" "$work/datasets.after"
diff -u "$work/interfaces.before" "$work/interfaces.after"
diff -u "$work/rc.before" "$work/rc.after"
if pgrep -x jaildeck >/dev/null; then fail 'install started the service.'; fi

# These are explicit operator actions, never package hooks. Use the fake
# adapter for the HTTP smoke test so no jail operation can touch the host.
cp /usr/local/etc/jaildeck/jaildeck.env.sample /usr/local/etc/jaildeck/jaildeck.env
cat >> /usr/local/etc/jaildeck/jaildeck.env <<'EOF'
JAILDECK_ADAPTER=fake
JAILDECK_PORT=18884
EOF
# Credential setup reads the same env the service does, as PACKAGING.md tells
# an operator to. Without it, a JAILDECK_STATE_DIR set in jaildeck.env would
# send the credential to the default directory while the service reads the
# configured one, and the service would refuse to start for want of it.
envfile=/usr/local/etc/jaildeck/jaildeck.env
with_env() { (set -a; . "$envfile"; set +a; "$@"); }
openssl rand -hex 24 | with_env jaildeck passwd --stdin
with_env jaildeck token add package-test > "$work/token"
# And the preservation checks below hash the directories it actually uses.
state_dir=$(with_env sh -c 'echo "${JAILDECK_STATE_DIR:-/var/db/jaildeck}"')
audit_log=$(with_env sh -c 'echo "${JAILDECK_AUDIT_LOG:-/var/log/jaildeck/audit.log}"')
service jaildeck onestart
running=YES
attempt=0
until curl -fsS http://127.0.0.1:18884/ -o "$work/index.html"; do
    attempt=$((attempt + 1))
    [ "$attempt" -lt 20 ] || fail 'service never became ready.'
    sleep 1
done
asset=$(sed -n 's/.*src="\([^"]*\.js\)".*/\1/p' "$work/index.html" | head -1)
case "$asset" in /assets/*.js) ;; *) fail 'the installed binary is serving the development fallback.' ;; esac
curl -fsS "http://127.0.0.1:18884$asset" -o "$work/bundle.js"
[ -s "$work/bundle.js" ] || fail 'empty frontend bundle.'
[ "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:18884/api/jails)" = 401 ] || fail 'authentication gate failed.'
service jaildeck onestop
running=NO

# Verify the real credential/token store, live config, logs and ZFS resources
# remain byte-for-byte intact during both replacement and removal.
printf '%s\n' 'operator-owned audit fixture' >> "$audit_log"
zfs create -o mountpoint=/usr/local/jails/package-test "$dataset"
printf '%s\n' 'operator-owned jail data' > /usr/local/jails/package-test/sentinel
zfs snapshot "$dataset@preserve"
inventory() {
    find "$envfile" "$state_dir" "$(dirname "$audit_log")" \
        /usr/local/jails/package-test -type f -exec sha256 {} \; | sort
}
inventory > "$work/operator.before"
guid=$(zfs get -H -o value guid "$dataset@preserve")
pkg add -f "$new"
pkg info jaildeck
pkg check -s jaildeck
[ "$(jaildeck version)" = "v$newversion" ] || fail 'upgrade version mismatch.'
inventory > "$work/operator.after-upgrade"
diff -u "$work/operator.before" "$work/operator.after-upgrade"
[ "$(zfs get -H -o value guid "$dataset@preserve")" = "$guid" ] || fail 'upgrade changed the ZFS snapshot.'
sha256 -q /etc/rc.conf > "$work/rc.after-upgrade"
diff -u "$work/rc.before" "$work/rc.after-upgrade"
if pgrep -x jaildeck >/dev/null; then fail 'upgrade started the service.'; fi

pkg delete -y jaildeck
if pkg info -e jaildeck; then fail 'package is still registered.'; fi
for item in /usr/local/bin/jaildeck /usr/local/etc/rc.d/jaildeck /usr/local/etc/jaildeck/jaildeck.env.sample; do
    [ ! -e "$item" ] || fail "package-owned file survived removal: $item"
done
inventory > "$work/operator.after-delete"
diff -u "$work/operator.before" "$work/operator.after-delete"
[ "$(zfs get -H -o value guid "$dataset@preserve")" = "$guid" ] || fail 'removal changed the ZFS snapshot.'
sha256 -q /etc/rc.conf > "$work/rc.after-delete"
diff -u "$work/rc.before" "$work/rc.after-delete"
echo "lifecycle: PASS (install, runtime/UI/auth, upgrade, removal, operator data and ZFS preservation)"
