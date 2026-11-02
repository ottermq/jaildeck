#!/bin/sh
# Jail Deck's front runs as the jaildeck user (JD-056). Created when missing,
# with the first id from 850 that is free as both a uid and a gid, and kept
# on removal as ports keep theirs.
PW="pw -R ${PKG_ROOTDIR:-/}"
if ! $PW groupshow jaildeck >/dev/null 2>&1 || ! $PW usershow jaildeck >/dev/null 2>&1; then
    id=850
    while $PW usershow -u "$id" >/dev/null 2>&1 || $PW groupshow -g "$id" >/dev/null 2>&1; do
        id=$((id + 1))
    done
    if ! $PW groupshow jaildeck >/dev/null 2>&1; then
        echo "Creating group jaildeck with gid $id."
        $PW groupadd jaildeck -g "$id"
    fi
    if ! $PW usershow jaildeck >/dev/null 2>&1; then
        echo "Creating user jaildeck with uid $id."
        $PW useradd jaildeck -u "$id" -g jaildeck -c "Jail Deck front" -d /nonexistent -s /usr/sbin/nologin -h -
    fi
fi
