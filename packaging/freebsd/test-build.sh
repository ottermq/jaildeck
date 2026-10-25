#!/bin/sh
# Build failure/recovery checks. Run from the checkout on FreeBSD, as a normal
# user. No packages are installed. Like make package, this rebuilds web/dist.
set -eu
[ "$(uname -s)" = FreeBSD ] || { echo 'test-build: requires FreeBSD' >&2; exit 1; }
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
trap 'exit 1' HUP INT TERM
mkdir "$work/bin" "$work/minimal"
original_path=$PATH
real_go=$(command -v go)
real_pkg=$(command -v pkg)
real_make=$(command -v make)
version=99.0.0
archive="bin/packages/jaildeck-$version.pkg"
[ ! -e "$archive" ] || { echo "test-build: move $archive aside first" >&2; exit 1; }

expect_failure() {
    expected=$1
    shift
    if "$@" > "$work/error.log" 2>&1; then
        echo "test-build: unexpectedly succeeded: $*" >&2
        exit 1
    fi
    grep -F "$expected" "$work/error.log" >/dev/null || { cat "$work/error.log"; exit 1; }
    [ ! -e "$archive" ]
    [ -z "$(find bin/packages -name '.work.*' -print 2>/dev/null)" ]
    echo "test-build: PASS $expected"
}

expect_failure 'VERSION must be' make package
expect_failure 'VERSION must be' make package VERSION=v0.10.0
expect_failure 'only PREFIX=/usr/local' make package VERSION="$version" PREFIX=/opt
expect_failure 'cross-compilation is not supported' env GOOS=linux make package VERSION="$version"
expect_failure 'unexpected pkg ABI' env ABI=FreeBSD:14:amd64 make package VERSION="$version"
expect_failure 'missing prerequisite:' make package VERSION="$version" GO=jaildeck-missing-go

# Base PATH has neither Go nor Node. Add only the prerequisites preceding the
# one being tested, rather than relying on what happens to be installed.
ln -s "$real_go" "$work/minimal/go"
expect_failure 'missing prerequisite: node' env PATH="$work/minimal:/usr/bin:/bin:/usr/sbin:/sbin" \
    "$real_make" package VERSION="$version"
ln -s "$(command -v node)" "$work/minimal/node"
expect_failure 'missing prerequisite: npm' env PATH="$work/minimal:/usr/bin:/bin:/usr/sbin:/sbin" \
    "$real_make" package VERSION="$version"

printf '#!/bin/sh\nexit 1\n' > "$work/bin/pkg"
chmod +x "$work/bin/pkg"
expect_failure 'bootstrap pkg first' env PATH="$work/bin:$original_path" make package VERSION="$version"
rm "$work/bin/pkg"
printf '#!/bin/sh\necho intentional-npm-failure >&2\nexit 42\n' > "$work/bin/npm"
chmod +x "$work/bin/npm"
expect_failure 'intentional-npm-failure' env PATH="$work/bin:$original_path" make package VERSION="$version"

# A stale index must not rescue a successful-but-empty frontend build. Each
# case sets up what it needs rather than inheriting it from the one before:
# dist/ need not survive the failed run above, and the fake npm is made
# executable here, not by the earlier case that happens to share its path.
mkdir -p web/dist
printf 'stale frontend\n' > web/dist/index.html
printf '#!/bin/sh\nexit 0\n' > "$work/bin/npm"
chmod +x "$work/bin/npm"
expect_failure 'did not produce web/dist/index.html' env PATH="$work/bin:$original_path" make package VERSION="$version"

# The three cases below inject a failure after the frontend step, so its result
# is all they need from it. Build it for real once and replay that build,
# rather than paying for npm ci and Vite -- and the network -- three times.
rm "$work/bin/npm"
"$real_make" web
cp -R web/dist "$work/dist"
cat > "$work/bin/npm" <<EOF
#!/bin/sh
# Run from web/ by make web: ci is a no-op, run build replays the real build.
case "\$1" in
ci) exit 0 ;;
run) rm -rf dist && cp -R "$work/dist" dist ;;
*) exit 1 ;;
esac
EOF
chmod +x "$work/bin/npm"

cat > "$work/bin/go" <<EOF
#!/bin/sh
if [ "\$1" = build ]; then echo intentional-compiler-failure >&2; exit 42; fi
exec "$real_go" "\$@"
EOF
chmod +x "$work/bin/go"
expect_failure 'intentional-compiler-failure' env PATH="$work/bin:$original_path" make package VERSION="$version"
rm "$work/bin/go"
cat > "$work/bin/make" <<EOF
#!/bin/sh
if [ "\$1" = install ]; then echo intentional-staging-failure >&2; exit 42; fi
exec "$real_make" "\$@"
EOF
chmod +x "$work/bin/make"
expect_failure 'intentional-staging-failure' env PATH="$work/bin:$original_path" \
    sh packaging/freebsd/package.sh "$version" /usr/local go "$work/bin/make"
cat > "$work/bin/pkg" <<EOF
#!/bin/sh
if [ "\$1" = create ]; then echo intentional-pkg-create-failure >&2; exit 42; fi
exec "$real_pkg" "\$@"
EOF
chmod +x "$work/bin/pkg"
rm "$work/bin/make"
expect_failure 'intentional-pkg-create-failure' env PATH="$work/bin:$original_path" make package VERSION="$version"
rm "$work/bin/npm" "$work/bin/pkg"
echo 'test-build: all failure paths returned nonzero without publishing a partial package'
