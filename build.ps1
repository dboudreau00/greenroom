# Build, test and package Greenroom on Windows.
#   .\build.ps1            vet + test + build for the current platform
#   .\build.ps1 -All       also cross-compile linux/amd64, linux/arm64, darwin/arm64
#   .\build.ps1 -SkipTests
param(
    [switch]$All,
    [switch]$SkipTests,
    [string]$Version = ""
)
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

if ($Version -eq "") {
    try { $Version = (git describe --tags --always --dirty 2>$null) } catch { }
    if (-not $Version) { $Version = "dev" }
}
$ldflags = "-s -w -X main.Version=$Version"
New-Item -ItemType Directory -Force build | Out-Null

Write-Host "go vet" -ForegroundColor Cyan
go vet ./...
if ($LASTEXITCODE -ne 0) { throw "vet failed" }

if (-not $SkipTests) {
    Write-Host "go test" -ForegroundColor Cyan
    go test ./... -count=1
    if ($LASTEXITCODE -ne 0) { throw "tests failed" }
}

Write-Host "build windows/amd64" -ForegroundColor Cyan
go build -ldflags $ldflags -o build/greenroom.exe ./cmd/greenroom
if ($LASTEXITCODE -ne 0) { throw "build failed" }

if ($All) {
    $targets = @(
        @{ os = "linux";  arch = "amd64"; out = "greenroom-linux-amd64" },
        @{ os = "linux";  arch = "arm64"; out = "greenroom-linux-arm64" },
        @{ os = "darwin"; arch = "arm64"; out = "greenroom-darwin-arm64" }
    )
    foreach ($t in $targets) {
        Write-Host "build $($t.os)/$($t.arch)" -ForegroundColor Cyan
        $env:GOOS = $t.os; $env:GOARCH = $t.arch; $env:CGO_ENABLED = "0"
        go build -ldflags $ldflags -o "build/$($t.out)" ./cmd/greenroom
        if ($LASTEXITCODE -ne 0) { throw "build $($t.os)/$($t.arch) failed" }
    }
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
}

Write-Host "done: build/ ($Version)" -ForegroundColor Green
Get-ChildItem build | Format-Table Name, Length
