#!/bin/sh
# Build the gnome-monitor-pin RPM inside a Fedora container.
#
#   packaging/build-rpm.sh <tag> <archive.tar.gz> <outdir>
#
# <archive.tar.gz> is goreleaser's linux/amd64 archive for <tag>, which
# carries the stamped binary, README.md, and LICENSE. The RPM lands in
# <outdir>. Needs rpm-build and systemd-rpm-macros.
set -eu

tag=$1
archive=$2
out=$3

version=$(printf '%s' "$tag" | sed -e 's/^v//' -e 's/-/~/g')
top=$(mktemp -d)
trap 'rm -rf "$top"' EXIT
mkdir -p "$top/SOURCES" "$top/SPECS" "$top/BUILD" "$top/RPMS"

tar -xzf "$archive" -C "$top/SOURCES" gnome-monitor-pin README.md LICENSE
cp contrib/gnome-monitor-pin.service "$top/SOURCES/"
cp packaging/gnome-monitor-pin.spec "$top/SPECS/"

rpmbuild --define "_topdir $top" --define "version $version" -bb "$top/SPECS/gnome-monitor-pin.spec"

mkdir -p "$out"
cp "$top"/RPMS/x86_64/*.rpm "$out/"
ls -1 "$out"
