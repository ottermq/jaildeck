#!/bin/sh
# Capture a shell in a jail on a PTY, for the browser console (#123, JD-042):
# what jexec -l leaves in the shell's environment, whether a resize reaches
# it, and how the session ends when the shell exits, when the jail stops
# under it, and when Jail Deck's side hangs up.
#
# UNLIKE capture.sh, THIS CHANGES THE HOST. It drives a running Jail Deck
# through its API: JD_URL (http://127.0.0.1:8888 by default) and JD_TOKEN, an
# API token. It creates one throwaway jail, jd-cap-pty, from the template
# snapshot JD_TEMPLATE (dataset@snapshot), with networking disabled,
# and destroys it again through the API. Every step lands in that Jail Deck's
# audit log. It needs the path of ptyprobe, a small Go program in ptyprobe/
# that starts the shell the way the console does (jexec -l <jail> /bin/sh -l
# through creack/pty), cross-built for FreeBSD, run as root.
#
# Usage: JD_TOKEN=... capture-jail-console-induced.sh /path/to/ptyprobe

PROBE=$1
URL=${JD_URL:-http://127.0.0.1:8888}
TEMPLATE=$JD_TEMPLATE
OUT=/tmp/jd-capture-jail-console
j=jd-cap-pty
rm -rf "$OUT"
mkdir -p "$OUT"
: > "$OUT/MANIFEST"
# The token goes to curl in a config file of its own, never on a command line:
# the MANIFEST records each command as it ran, and ps shows it to everyone.
AUTH=$(mktemp /tmp/jd-capture-auth.XXXXXX)
chmod 600 "$AUTH"
printf 'header = "Authorization: Bearer %s"\n' "$JD_TOKEN" > "$AUTH"
trap 'rm -f "$AUTH"' EXIT

PRIV=""
if [ "$(id -u)" -ne 0 ]; then
	if command -v doas >/dev/null 2>&1; then PRIV="doas"
	elif command -v sudo >/dev/null 2>&1; then PRIV="sudo -n"; fi
fi

cap() {
	name=$1
	shift
	t0=$(date +%s)
	sh -c "$*" >"$OUT/$name.stdout" 2>"$OUT/$name.stderr"
	status=$?
	echo "$status" >"$OUT/$name.exit"
	printf '%s\t%s\texit=%s\t%ss\n' "$name" "$*" "$status" "$(( $(date +%s) - t0 ))" >>"$OUT/MANIFEST"
	return $status
}

api() {
	curl -s -o /dev/null -w '%{http_code}' -X "$1" -K "$AUTH" ${3:+-d "$3"} "$URL/api$2"
}

[ -x "$PROBE" ] || { echo "capture-jail-console-induced: give the ptyprobe binary's path." >&2; exit 1; }
[ -n "$JD_TOKEN" ] && [ -n "$TEMPLATE" ] || { echo "capture-jail-console-induced: set JD_TOKEN and JD_TEMPLATE." >&2; exit 1; }
jls -j $j >/dev/null 2>&1 && { echo "capture-jail-console-induced: $j is running; refusing." >&2; exit 1; }
[ "$(api GET /jails/$j)" = 404 ] || { echo "capture-jail-console-induced: $j exists; refusing." >&2; exit 1; }

restore() {
	echo "--- restoring"
	api POST /jails/$j/stop >/dev/null
	echo "destroy: $(api DELETE "/jails/$j?confirm=$j")"
	printf 'jail:     %s\n' "$(api GET /jails/$j)"
	printf 'running:  %s\n' "$(jls -N name | tr '\n' ' ')"
	rm -f "$AUTH"
}
trap restore EXIT INT TERM

cap jpty_create "curl -s -X POST -K $AUTH -d '{\"name\":\"$j\",\"template\":\"$TEMPLATE\",\"network\":{\"mode\":\"disable\"}}' $URL/api/jails | cut -c1-300"
jls -j $j >/dev/null 2>&1 || { echo "capture-jail-console-induced: $j did not start:" >&2; cat "$OUT/jpty_create.stdout" >&2; exit 1; }

# Environment, size, resize, and the shell exiting on its own.
cap jpty_basic "$PRIV $PROBE $j basic"

# The same, on a non-blocking duplicate of the PTY master: what the console
# will use, so that closing it interrupts a blocked read.
cap jpty_basic_nb "$PRIV $PROBE $j basicnb"

# The jail stopped with no shell open, for how long a stop takes on its own.
cap jpty_stop_alone "curl -s -X POST -K $AUTH $URL/api/jails/$j/stop | cut -c1-200"
cap jpty_start_alone "curl -s -X POST -K $AUTH $URL/api/jails/$j/start | cut -c1-200"

# The jail stopped under an open shell.
$PRIV $PROBE $j stop >"$OUT/jpty_stop.stdout" 2>"$OUT/jpty_stop.stderr" &
probe=$!
s=$(date +%s)
until grep -q READY "$OUT/jpty_stop.stdout" || [ $(( $(date +%s) - s )) -gt 15 ]; do sleep 0.5; done
cap jpty_stop_jail "curl -s -X POST -K $AUTH $URL/api/jails/$j/stop | cut -c1-200"
wait $probe
echo $? >"$OUT/jpty_stop.exit"
printf '%s\t%s\texit=%s\n' jpty_stop "ptyprobe $j stop" "$(cat "$OUT/jpty_stop.exit")" >>"$OUT/MANIFEST"

# Jail Deck's side hanging up while a command runs in the shell: does anything
# stay behind in the jail?
cap jpty_start_again "curl -s -X POST -K $AUTH $URL/api/jails/$j/start | cut -c1-200"
cap jpty_hangup "$PRIV $PROBE $j hangup"
sleep 2
cap jpty_after_hangup "ps -J $j -o pid,stat,command 2>&1"

# Hanging up again, on the non-blocking master.
cap jpty_hangup_nb "$PRIV $PROBE $j hangupnb"
sleep 2
cap jpty_after_hangup_nb "ps -J $j -o pid,stat,command 2>&1"
