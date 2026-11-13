param(
    [string]$Target = (Join-Path (Split-Path $PSScriptRoot -Parent) "wowcensus-companion")
)
$ErrorActionPreference = "Stop"
$overlay = Join-Path (Split-Path $PSScriptRoot -Parent) "desktop"

if (-not (Get-Command wails3 -ErrorAction SilentlyContinue)) {
    throw "wails3 not found. Run: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0"
}
if (Test-Path $Target) { throw "$Target already exists; remove it or pass -Target" }

$parent = Split-Path $Target -Parent
$name = Split-Path $Target -Leaf
Push-Location $parent
try {
    wails3 init -n $name -t vanilla-js -mod wowcensuscompanion -d $parent -productname "WoWCensus Companion" -productcompany wowcensus -productidentifier io.wowcensus.companion -productversion 0.1.4 -productdescription "Shares WoWCensus add-on data with wowcensus.io" -productcopyright wowcensus
    if ($LASTEXITCODE -ne 0) { throw "wails3 init failed" }
} finally { Pop-Location }

Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $Target "greetservice.go")
Copy-Item -Recurse -Force (Join-Path $overlay "*") $Target
Remove-Item -Force -Recurse -ErrorAction SilentlyContinue (Join-Path $Target "build/appicon.icon"), (Join-Path $Target "build/darwin/Assets.car"), (Join-Path $Target "build/windows/icon.ico"), (Join-Path $Target "build/darwin/icons.icns")
$taskfile = Join-Path $Target "build/Taskfile.yml"
$content = (Get-Content -Raw $taskfile).Replace(" -iconcomposerinput appicon.icon -macassetdir darwin", "") -replace '(?m)^\s*- "appicon\.icon"\r?\n', ''
[IO.File]::WriteAllText($taskfile, $content)

Push-Location $Target
try {
    go mod tidy
    if ($LASTEXITCODE -ne 0) { throw "go mod tidy failed" }
    go test ./companion/...
    Write-Host "`nReady. Next:`n  cd `"$Target`"`n  wails3 dev"
} finally { Pop-Location }
