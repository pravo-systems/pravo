# Installs pravo from a GitHub release.
#
#   irm https://pravo.systems/install.ps1 | iex
#   $env:PRAVO_VERSION = "v0.1.0-rc1"; irm https://pravo.systems/install.ps1 | iex
#
# It finds the archive for this system, checks it against SHA256SUMS and puts
# pravo.exe in $env:PRAVO_INSTALL_DIR, which is %LOCALAPPDATA%\pravo\bin by
# default.
$ErrorActionPreference = "Stop"

$repo = "pravo-systems/pravo"
$dir = if ($env:PRAVO_INSTALL_DIR) { $env:PRAVO_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "pravo\bin" }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
	"AMD64" { "amd64" }
	"ARM64" { "arm64" }
	default { throw "install: no build of pravo for $($env:PROCESSOR_ARCHITECTURE)" }
}

$tag = $env:PRAVO_VERSION
if (-not $tag) {
	# The latest release is the newest one that is not a pre-release. Until there
	# is one, the newest pre-release is the one to install.
	try { $tag = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name } catch { $tag = $null }
	if (-not $tag) { $tag = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases?per_page=1" | Select-Object -First 1).tag_name }
	if (-not $tag) { throw "install: no release found; set PRAVO_VERSION to a tag such as v0.1.0-rc1" }
}
$version = $tag.TrimStart("v")
$name = "pravo_${version}_windows_${arch}"
$base = "https://github.com/$repo/releases/download/$tag"

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("pravo-install-" + [System.Guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
	Write-Host "Downloading pravo $version for windows/$arch"
	Invoke-WebRequest "$base/$name.zip" -OutFile "$tmp\$name.zip"
	Invoke-WebRequest "$base/SHA256SUMS" -OutFile "$tmp\SHA256SUMS"

	$line = Get-Content "$tmp\SHA256SUMS" | Where-Object { $_ -match " $([regex]::Escape($name)).zip$" } | Select-Object -First 1
	if (-not $line) { throw "install: SHA256SUMS has no entry for $name.zip" }
	$want = ($line -split "\s+")[0]
	$got = (Get-FileHash "$tmp\$name.zip" -Algorithm SHA256).Hash.ToLower()
	if ($got -ne $want) { throw "install: the checksum of the download is $got, want $want" }

	Expand-Archive "$tmp\$name.zip" -DestinationPath $tmp
	New-Item -ItemType Directory -Force -Path $dir | Out-Null
	Copy-Item "$tmp\$name\pravo.exe" (Join-Path $dir "pravo.exe") -Force
	Write-Host ("Installed " + (& (Join-Path $dir "pravo.exe") version | Select-Object -First 1) + " in $dir")
	if (($env:Path -split ";") -notcontains $dir) {
		Write-Host "Add $dir to your PATH:  setx PATH `"`$env:Path;$dir`""
	}
} finally {
	Remove-Item -Recurse -Force $tmp
}
