$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

$version = (Get-Content -Raw -Path (Join-Path $root "VERSION")).Trim()
if ($version -notmatch '^v\d+\.\d+\.\d+$') {
    throw "VERSION must look like vMAJOR.MINOR.PATCH"
}

$dist = Join-Path $root "dist"
New-Item -ItemType Directory -Force -Path $dist | Out-Null

$env:CGO_ENABLED = "0"
$ldflags = "-s -w -H=windowsgui -X main.appVersion=$version"
& go build -ldflags $ldflags -o (Join-Path $dist "ProxyBox.exe") .
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

$zipName = "ProxyBox-$version-win64.zip"
$zipPath = Join-Path $dist $zipName
if (Test-Path $zipPath) {
    Remove-Item -Force $zipPath
}

Push-Location $dist
try {
    $items = @("ProxyBox.exe")
    if (Test-Path -Path "mihomo.exe") {
        $items += "mihomo.exe"
    }
    Compress-Archive -Path $items -DestinationPath $zipName -Force
}
finally {
    Pop-Location
}

function Show-FileHashLine([string]$Path) {
    $item = Get-Item -Path $Path
    $hash = (Get-FileHash -Algorithm SHA256 -Path $Path).Hash.ToLower()
    Write-Output ("{0}  {1} bytes  SHA256 {2}" -f $item.Name, $item.Length, $hash)
}

Show-FileHashLine (Join-Path $dist "ProxyBox.exe")
Show-FileHashLine $zipPath
