#!/bin/sh
# Run through `make package`. No root privileges or Ports tree are needed.
set -eu
export LC_ALL=C

fail() { echo "package: $*" >&2; exit 1; }
version=${1:-}
prefix=${2:-/usr/local}
go=${3:-go}
make=${4:-make}
commit=${5:-}
printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' ||
    fail 'VERSION must be a release number such as 0.10.0 (no v prefix).'
# The commit is injected like the version (#165): the Go toolchain stamps one
# only when it can run git on the tree, and CI's VM has no git. Asked of git
# here when not given, and left out, said aloud, when there is no repository.
if [ -z "$commit" ] && command -v git >/dev/null 2>&1 && git rev-parse --git-dir >/dev/null 2>&1; then
    commit=$(git rev-parse HEAD)
fi
if [ -n "$commit" ]; then
    printf '%s\n' "$commit" | grep -Eq '^[0-9a-f]{40}$' ||
        fail 'COMMIT must be a full git revision, 40 hexadecimal digits.'
else
    echo 'package: no COMMIT given and no git repository here; the binary will report no commit.' >&2
fi
[ "$prefix" = /usr/local ] || fail 'only PREFIX=/usr/local is supported.'
[ "$(uname -s)" = FreeBSD ] && [ "$(uname -m)" = amd64 ] ||
    fail 'build natively on FreeBSD 15 amd64.'
case $(uname -r) in 15.*) ;; *) fail 'build natively on FreeBSD 15 amd64.' ;; esac
for tool in "$go" "$make" node npm pkg; do
    command -v "$tool" >/dev/null 2>&1 || fail "missing prerequisite: $tool"
done
pkg -N >/dev/null 2>&1 || fail 'bootstrap pkg first: pkg bootstrap'
abi=$(pkg config ABI)
[ "$abi" = FreeBSD:15:amd64 ] || fail "unexpected pkg ABI: $abi"
[ "$("$go" env GOOS)" = freebsd ] && [ "$("$go" env GOARCH)" = amd64 ] ||
    fail 'cross-compilation is not supported; unset GOOS and GOARCH.'

# A fixed output directory and a private work directory keep previous staging
# files out of the inventory. Only the verified archive is moved into place.
mkdir -p bin/packages
work=$(mktemp -d "$(pwd)/bin/packages/.work.XXXXXX")
trap 'rm -rf "$work"' EXIT
trap 'exit 1' HUP INT TERM
mkdir -p "$work/stage" "$work/meta" "$work/out"
export CGO_ENABLED=0 GOAMD64=v1

# Sequential even under make -j: install depends on build, which must see the
# frontend produced by this invocation. Keep the ordinary build Go-only.
rm -rf web/dist
mkdir -p web/dist
touch web/dist/.gitkeep
"$make" web
[ -s web/dist/index.html ] || fail 'frontend build did not produce web/dist/index.html.'
# BINARY_NAME and MAIN_PATH restate the Makefile's defaults on purpose. A
# `make package BINARY_NAME=x` hands that override to this sub-make through
# MAKEFLAGS, and only an explicit argument here outranks it -- without one the
# staged binary would stop matching the packing list.
#
# The binary reports vX.Y.Z, as buildinfo documents and as a tag-built binary
# does; only the package name and manifest take the bare number, because pkg
# versions carry no v. One `jaildeck version` shape whichever way it was built.
"$make" install PREFIX=/usr/local DESTDIR="$work/stage" \
    BUILD_DIR="$work/build" BINARY_NAME=jaildeck MAIN_PATH=cmd/jaildeck \
    GO="$go" LDFLAGS="-X github.com/ottermq/jaildeck/internal/buildinfo.version=v$version${commit:+ -X github.com/ottermq/jaildeck/internal/buildinfo.commit=$commit}"
binary="$work/stage/usr/local/bin/jaildeck"
[ "$("$binary" version)" = "v$version" ] || fail 'binary version does not match VERSION.'
[ "$("$binary" version --commit)" = "$commit" ] || fail 'binary commit does not match COMMIT.'
"$go" run ./tools/checkfrontend web/dist "$binary"

# Fail closed if install ever starts staging anything outside the packing list.
awk '!/^@/ && NF { print "/usr/local/" $0 }' packaging/freebsd/plist | sort > "$work/expected"
(cd "$work/stage" && find . ! -type d | sed 's|^\.||' | sort) > "$work/staged"
diff -u "$work/expected" "$work/staged" || fail 'unexpected staged files.'
sed -e "s/%%VERSION%%/$version/g" -e "s/%%ABI%%/$abi/g" \
    packaging/freebsd/manifest.ucl.in > "$work/meta/+MANIFEST"
pkg create -m "$work/meta" -p packaging/freebsd/plist -r "$work/stage" -o "$work/out"
archive="$work/out/jaildeck-$version.pkg"
[ -s "$archive" ] || fail 'pkg create did not produce the expected archive.'
[ "$(pkg query -F "$archive" '%n %v %q %p')" = "jaildeck $version $abi /usr/local" ] ||
    fail 'archive metadata does not match the build.'
pkg query -F "$archive" '%Fp' | sort > "$work/packaged"
diff -u "$work/expected" "$work/packaged" || fail 'unexpected package inventory.'
printf '%s\n' \
    'root:wheel:555 /usr/local/bin/jaildeck' \
    'root:wheel:555 /usr/local/etc/rc.d/jaildeck' \
    'root:wheel:644 /usr/local/etc/jaildeck/jaildeck.env.sample' | sort > "$work/expected-modes"
pkg query -F "$archive" '%Fu:%Fg:%Fm %Fp' | sort > "$work/packaged-modes"
diff -u "$work/expected-modes" "$work/packaged-modes" || fail 'unexpected package ownership or modes.'
[ "$(pkg query -F "$archive" '%#d')" = 0 ] || fail 'unexpected runtime package dependencies.'

# Read the complete archive (including its payload), then check the packaged
# executable too. A valid manifest alone is not proof of a complete archive.
mkdir "$work/extracted"
tar -xf "$archive" -C "$work/extracted"
for file in bin/jaildeck etc/rc.d/jaildeck etc/jaildeck/jaildeck.env.sample; do
    cmp "$work/stage/usr/local/$file" "$work/extracted/usr/local/$file"
done
mv -f "$archive" "bin/packages/jaildeck-$version.pkg"
echo "package: verified bin/packages/jaildeck-$version.pkg ($abi)"
