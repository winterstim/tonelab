#!/bin/sh
# Packs one platform's tonelab-mcp into an MCP bundle, the file Claude
# Desktop installs with a double click.
#   pack.sh <version> <darwin|win32> <binary> <out.mcpb>
set -eu
version=$1 platform=$2 binary=$3 out=$4
here=$(cd "$(dirname "$0")" && pwd)

# The manifest wants semver; a build between tags is not one.
semver=${version#v}
case $semver in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *) semver=0.0.0-dev ;;
esac
entry=tonelab-mcp
[ "$platform" = win32 ] && entry=tonelab-mcp.exe

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir "$stage/server"
cp "$binary" "$stage/server/$entry"
chmod +x "$stage/server/$entry"
cp "$here/../appicon.png" "$stage/icon.png"
sed -e "s/\"VERSION\"/\"$semver\"/" -e "s/\"PLATFORM\"/\"$platform\"/" -e "s#server/ENTRY#server/$entry#" \
  "$here/manifest.json" > "$stage/manifest.json"

rm -f "$out"
out=$(cd "$(dirname "$out")" && pwd)/$(basename "$out")
(cd "$stage" && zip -q -r "$out" manifest.json icon.png server)
