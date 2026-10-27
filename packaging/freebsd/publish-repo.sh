#!/bin/sh
# Publishes Jail Deck's GitHub releases into a signed pkg repository (JD-039).
#
# Run periodically on the repository host, by an unprivileged user that owns
# the repository directory and the signing key:
#
#   publish-repo.sh [-c config] run        publish what is new; quiet otherwise
#   publish-repo.sh [-c config] rebuild    re-sign the repository from the pool
#   publish-repo.sh [-c config] verify <package> <tag>
#                                          verify one file, publishing nothing
#
# It pulls; nothing pushes to it. For each published release at or above
# SINCE that the pool does not hold yet, it downloads the package and its
# checksum, checks the checksum, and verifies the package's artifact
# attestation: built in this GITHUB_REPO, by WORKFLOW, from the release's own
# tag. Only then does the package join the pool, and the repository is
# rebuilt from the whole pool, signed, and swapped in as one symlink. A
# package that fails is kept under rejected/ and not tried again.
#
# No GitHub token is needed: releases, assets and attestations of a public
# repository are read anonymously, and `gh attestation verify --bundle`
# checks the attestation against Sigstore's public trust root.
#
# Needs: fetch, sha256, lockf, logger and pkg (base); gh and jq (packages).
set -eu

config=/usr/local/etc/jaildeck-pkgrepo.conf
if [ "${1:-}" = -c ]; then
    config=$2
    shift 2
fi

# REPO_ROOT holds pool/ (every accepted package), builds/ (signed
# repositories), current (the one served), rejected/ and work/.
REPO_ROOT=
SIGNING_KEY=
GITHUB_REPO=ottermq/jaildeck
PACKAGE=jaildeck
WORKFLOW=.github/workflows/package.yml
# The oldest version to look for. Releases before the package workflow
# existed have nothing attached, and would only be looked for again and again.
SINCE=0.0.0
KEEP_BUILDS=3
# shellcheck source=/dev/null
. "$config"

: "${REPO_ROOT:?is not set in $config}"
: "${SIGNING_KEY:?is not set in $config}"

log() {
    logger -t jaildeck-pkgrepo -p "user.$1" "$2"
    [ -t 2 ] && echo "$2" >&2
    return 0
}

die() {
    log err "$1"
    exit 1
}

# gh needs somewhere to keep its configuration and Sigstore's trust root.
export HOME="$REPO_ROOT/.home"
export GH_CONFIG_DIR="$HOME/gh"
mkdir -p "$HOME" "$REPO_ROOT/pool" "$REPO_ROOT/builds" "$REPO_ROOT/rejected" "$REPO_ROOT/work"

api() {
    fetch -q -o - "https://api.github.com/repos/$GITHUB_REPO/$1"
}

# verify checks a package against its attestation: this repository, this
# workflow, this tag. The bundle comes from the public attestations API by
# the package's digest. It answers 0 for verified, 1 for refused, and 2 for
# could not ask -- which is not a verdict, and is tried again next run.
verify() {
    file=$1
    tag=$2
    sum=$(sha256 -q "$file")
    answer="$REPO_ROOT/work/$tag.attestations.json"
    bundle="$REPO_ROOT/work/$tag.bundle.json"
    trap 'rm -f "$answer" "$bundle"' EXIT
    if ! api "attestations/sha256:$sum" > "$answer" 2>/dev/null; then
        # The API answers 404 for a digest it has no attestation for, which
        # fetch reports as a failure too; tell the two apart by asking once
        # more for the status alone.
        if fetch -q -o /dev/null "https://api.github.com/repos/$GITHUB_REPO" 2>/dev/null; then
            echo "GitHub has no attestation for sha256:$sum"
            return 1
        fi
        echo "could not reach GitHub to fetch the attestation"
        return 2
    fi
    jq -ce '.attestations[0].bundle' "$answer" > "$bundle" ||
        { echo "GitHub has no attestation for sha256:$sum"; return 1; }
    if out=$(gh attestation verify "$file" --bundle "$bundle" -R "$GITHUB_REPO" \
        --source-ref "refs/tags/$tag" --signer-workflow "$GITHUB_REPO/$WORKFLOW" 2>&1); then
        return 0
    fi
    echo "$out" | grep -v '^$' | tail -1
    return 1
}

# rebuild signs a new repository from the whole pool and swaps it in. The
# catalogue lists every version in All/, which is what makes
# `pkg install -f jaildeck-<older>` a rollback.
rebuild() {
    # pkg repo refuses a directory with no package in it, measured; there is
    # then nothing to sign and nothing to serve.
    if ! ls "$REPO_ROOT"/pool/*.pkg >/dev/null 2>&1; then
        log notice "the pool is empty; nothing to publish"
        return 0
    fi
    build="$REPO_ROOT/builds/$(date -u +%Y%m%dT%H%M%SZ)"
    mkdir -p "$build/All"
    cp -p "$REPO_ROOT"/pool/*.pkg "$build/All/"
    if ! pkg repo -q "$build" "$SIGNING_KEY"; then
        rm -rf "$build"
        die "pkg repo failed; the repository served is unchanged"
    fi
    ln -sfn "$build" "$REPO_ROOT/current.new"
    mv -h "$REPO_ROOT/current.new" "$REPO_ROOT/current"
    log notice "published $(ls "$build/All" | wc -l | tr -d ' ') package(s) as $(basename "$build")"
    # Keep the newest builds; current is always among them.
    ls -1d "$REPO_ROOT"/builds/*/ | sort -r | tail -n +"$((KEEP_BUILDS + 1))" | while read -r old; do
        rm -rf "$old"
    done
}

run() {
    releases=$(api "releases?per_page=50" |
        jq -r '.[] | select(.draft == false and .prerelease == false) | .tag_name') ||
        die "could not list the releases of $GITHUB_REPO"
    new=0
    for tag in $releases; do
        case $tag in v[0-9]*.[0-9]*.[0-9]*) ;; *) continue ;; esac
        version=${tag#v}
        [ "$(pkg version -t "$version" "$SINCE")" = "<" ] && continue
        name="$PACKAGE-$version.pkg"
        [ -e "$REPO_ROOT/pool/$name" ] && continue
        [ -e "$REPO_ROOT/rejected/$tag" ] && continue

        work="$REPO_ROOT/work/$name"
        url="https://github.com/$GITHUB_REPO/releases/download/$tag/$name"
        # Not attached yet: the workflow runs after the release is published.
        fetch -q -o "$work" "$url" 2>/dev/null || continue
        fetch -q -o "$work.sha256" "$url.sha256" 2>/dev/null || continue

        if [ "$(sha256 -q "$work")" != "$(cut -d' ' -f1 "$work.sha256")" ]; then
            why="checksum does not match"
        else
            status=0
            why=$(verify "$work" "$tag") || status=$?
            case $status in
            0)
                mv "$work" "$REPO_ROOT/pool/$name"
                rm -f "$work.sha256"
                log notice "accepted $name from $tag"
                new=1
                continue
                ;;
            2)
                log warning "$name from $tag: $why; trying again next run"
                continue
                ;;
            esac
        fi
        mkdir -p "$REPO_ROOT/rejected/$tag"
        mv "$work" "$work.sha256" "$REPO_ROOT/rejected/$tag/"
        log err "rejected $name from $tag: $why"
    done
    [ "$new" = 1 ] && rebuild
    return 0
}

case ${1:-run} in
run)
    # One at a time: a run that finds another still going leaves it be.
    exec lockf -s -t 0 "$REPO_ROOT/.lock" "$0" -c "$config" run-locked
    ;;
run-locked)
    run
    ;;
rebuild)
    rebuild
    ;;
verify)
    [ $# -eq 3 ] || die "usage: $0 [-c config] verify <package> <tag>"
    verify "$2" "$3"
    ;;
*)
    die "usage: $0 [-c config] run|rebuild|verify <package> <tag>"
    ;;
esac
