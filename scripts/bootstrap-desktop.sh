#!/usr/bin/env sh
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
target=${1:-"$here/wowcensus-companion"}

command -v wails3 >/dev/null 2>&1 || { echo "wails3 not found. Run: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0" >&2; exit 1; }
[ -e "$target" ] && { echo "$target already exists" >&2; exit 1; }

wails3 init -n "$(basename "$target")" -t vanilla-js -mod wowcensuscompanion -d "$(dirname "$target")" -productname "WoWCensus Companion" -productcompany wowcensus -productidentifier io.wowcensus.companion -productversion 0.1.0 -productdescription "Shares WoWCensus add-on data with wowcensus.io" -productcopyright wowcensus
rm -f "$target/greetservice.go"
cp -R "$here/desktop/." "$target/"
cd "$target"
go mod tidy
go test ./companion/...
printf '\nReady. Next:\n  cd "%s"\n  wails3 dev\n' "$target"
