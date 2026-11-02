#!/bin/sh
# Regenerates the constructed OpenPGP fixtures with GnuPG: two throwaway
# keys, Ed25519 and RSA, and what each signs the way a publisher in the
# catalog does. The keys sign nothing else and their secret halves are not
# kept. The publishers' own files beside these (ubuntu-*, fedora-*) were
# downloaded as published and are not regenerated.
set -eu
cd "$(dirname "$0")"
home=$(mktemp -d); trap 'rm -rf "$home"' EXIT
g="gpg --homedir $home --batch --quiet --pinentry-mode loopback --passphrase ''"
$g --quick-gen-key 'jaildeck test ed25519 <test-ed25519@example.invalid>' ed25519 sign never
$g --quick-gen-key 'jaildeck test rsa <test-rsa@example.invalid>' rsa2048 sign never
$g --armor --export test-ed25519@example.invalid > test-ed25519.asc
$g --armor --export test-rsa@example.invalid > test-rsa.asc
for k in ed25519 rsa; do
	$g --with-colons --fingerprint test-$k@example.invalid | awk -F: '$1=="fpr"{print $10; exit}' > test-$k.fpr
done
printf 'a disk image, standing in\n' > archive.bin
# Arch's form: a binary detached signature, Ed25519 over SHA-512.
$g --local-user test-ed25519@example.invalid --digest-algo SHA512 --detach-sign -o archive.bin.sig archive.bin
# Alpine's: an armored detached signature, RSA over SHA-256.
$g --local-user test-rsa@example.invalid --digest-algo SHA256 --armor --detach-sign -o archive.bin.asc archive.bin
# Fedora's: a cleartext signature over a checksum file with a line that
# needs dash escaping and one with trailing spaces.
printf '# a comment  \n- a line starting with a dash\nSHA256 (archive.bin) = %s\n' \
	"$(sha256sum archive.bin | cut -d' ' -f1)" > checksum.txt
$g --local-user test-rsa@example.invalid --digest-algo SHA256 --clearsign -o checksum.txt.asc checksum.txt
rm checksum.txt
