#!/bin/sh

# Cross-compiles release binaries for this fork's supported target
# platforms only: Linux (amd64, arm64), Windows (amd64), and macOS
# (Apple Silicon / arm64). All builds are CGO-free (see the Makefile's
# CGO_ENABLED default), so they can all run from a single Linux runner
# without any cross-toolchain.

set -e

VERSION="$1"
if [ -z "$VERSION" ]; then
	VERSION="$(go run tools/build-version.go)"
fi

mkdir -p binaries
mkdir -p micro-$VERSION

cp LICENSE micro-$VERSION
cp README.md micro-$VERSION
cp LICENSE-THIRD-PARTY micro-$VERSION

# Syntax highlighting depends on generated *.hdr header files (see the
# go:generate directive in runtime/runtime.go); this only needs to run
# once since it doesn't depend on GOOS/GOARCH.
go generate ./runtime

create_artefact_generic()
{
	mv micro micro-$VERSION/
	tar -czf micro-$VERSION-$1.tar.gz micro-$VERSION
	sha256sum micro-$VERSION-$1.tar.gz > micro-$VERSION-$1.tar.gz.sha256
	mv micro-$VERSION-$1.* binaries
	rm micro-$VERSION/micro
}

create_artefact_windows()
{
	mv micro.exe micro-$VERSION/
	zip -r -q -T micro-$VERSION-$1.zip micro-$VERSION
	sha256sum micro-$VERSION-$1.zip > micro-$VERSION-$1.zip.sha256
	mv micro-$VERSION-$1.* binaries
	rm micro-$VERSION/micro.exe
}

echo "Linux amd64"
GOOS=linux GOARCH=amd64 make build-quick
create_artefact_generic "linux-amd64"

echo "Linux arm64"
GOOS=linux GOARCH=arm64 make build-quick
create_artefact_generic "linux-arm64"

echo "Windows amd64"
GOOS=windows GOARCH=amd64 make build-quick
create_artefact_windows "windows-amd64"

echo "macOS arm64 (Apple Silicon)"
GOOS=darwin GOARCH=arm64 make build-quick
create_artefact_generic "macos-arm64"

rm -rf micro-$VERSION
