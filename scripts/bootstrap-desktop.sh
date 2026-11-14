#!/usr/bin/env sh
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
target=${1:-"$here/wowcensus-companion"}

command -v wails3 >/dev/null 2>&1 || { echo "wails3 not found. Run: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0" >&2; exit 1; }
[ -e "$target" ] && { echo "$target already exists" >&2; exit 1; }

wails3 init -n "$(basename "$target")" -t vanilla-js -mod wowcensuscompanion -d "$(dirname "$target")" -productname "WoWCensus Companion" -productcompany wowcensus -productidentifier io.wowcensus.companion -productversion 0.1.10 -productdescription "Shares WoWCensus add-on data with wowcensus.io" -productcopyright wowcensus
rm -f "$target/greetservice.go"
cp -R "$here/desktop/." "$target/"
rm -rf "$target/build/appicon.icon" "$target/build/darwin/Assets.car" "$target/build/windows/icon.ico" "$target/build/darwin/icons.icns"
sed -e 's/ -iconcomposerinput appicon.icon -macassetdir darwin//' -e '/- "appicon.icon"/d' "$target/build/Taskfile.yml" > "$target/build/Taskfile.yml.tmp"
mv "$target/build/Taskfile.yml.tmp" "$target/build/Taskfile.yml"
sed -e '/<key>CFBundleIconName<\/key>/{N;d;}' "$target/build/darwin/Info.plist" > "$target/build/darwin/Info.plist.tmp"
mv "$target/build/darwin/Info.plist.tmp" "$target/build/darwin/Info.plist"
cd "$target"
go mod tidy
go test ./companion/...
printf '\nReady. Next:\n  cd "%s"\n  wails3 dev\n' "$target"
