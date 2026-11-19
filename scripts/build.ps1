param(
 [string]$ToolchainRoot = (Join-Path $env:LOCALAPPDATA 'Programs\MultiPanDev'),
 [string]$AppName = 'multipan-0.99.0',
 [switch]$SkipDesktopSmoke
)
$ErrorActionPreference = 'Stop'
if($AppName -notmatch '^[A-Za-z0-9.-]+$'){throw 'Invalid executable name'}
Set-Location (Split-Path $PSScriptRoot -Parent)
$uiVersionMatch = [regex]::Match((Get-Content -LiteralPath 'frontend/src/version.ts' -Raw), "APP_VERSION\s*=\s*'([^']+)'")
if(-not $uiVersionMatch.Success){throw 'Missing UI version'}
$uiVersion = $uiVersionMatch.Groups[1].Value
$resourceVersion = (Get-Content -LiteralPath 'build/windows/info.json' -Raw | ConvertFrom-Json).fixed.file_version
$configVersionMatch = [regex]::Match((Get-Content -LiteralPath 'build/config.yml' -Raw), 'version:\s*"(\d+\.\d+\.\d+)"')
if($uiVersion -ne $resourceVersion -or -not $configVersionMatch.Success -or $uiVersion -ne $configVersionMatch.Groups[1].Value){throw 'UI, Windows resource and application versions do not match'}
if($AppName -match '^multipan-(\d+\.\d+\.\d+)$' -and $Matches[1] -ne $uiVersion){throw 'Executable name version does not match UI version'}
$activate = Join-Path $ToolchainRoot 'Activate.ps1'
if (Test-Path -LiteralPath $activate) { . $activate }
foreach ($tool in @('go','node','npm.cmd','wails3')) {
 if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { throw "Missing development tool: $tool" }
}
go run ./tools/icon
if ($LASTEXITCODE -ne 0) { throw 'Icon generation failed' }
wails3 task build "APP_NAME=$AppName"
if ($LASTEXITCODE -ne 0) { throw 'Build failed' }
go test ./...
if ($LASTEXITCODE -ne 0) { throw 'Tests failed' }
go vet ./...
if ($LASTEXITCODE -ne 0) { throw 'Go vet failed' }
node ./tools/check-browse-history.mjs
if ($LASTEXITCODE -ne 0) { throw 'Browse history checks failed' }
node ./tools/check-file-list.mjs
if ($LASTEXITCODE -ne 0) { throw 'File list checks failed' }
node ./tools/check-file-shortcuts.mjs
if ($LASTEXITCODE -ne 0) { throw 'File shortcut checks failed' }
node ./tools/check-upload-tasks.mjs
if ($LASTEXITCODE -ne 0) { throw 'Upload task checks failed' }
node ./tools/check-transfer-recovery.mjs
if ($LASTEXITCODE -ne 0) { throw 'Transfer recovery checks failed' }
node ./tools/check-share-text.mjs
if ($LASTEXITCODE -ne 0) { throw 'Share text checks failed' }
node ./tools/check-download-view.mjs
if ($LASTEXITCODE -ne 0) { throw 'Download view checks failed' }
node ./tools/check-download-results.mjs
if ($LASTEXITCODE -ne 0) { throw 'Download results checks failed' }
node ./tools/check-download-meter.mjs
if ($LASTEXITCODE -ne 0) { throw 'Download meter checks failed' }
node ./tools/check-cloud-drag.mjs
if ($LASTEXITCODE -ne 0) { throw 'Cloud drag component checks failed' }
node ./tools/check-recycle-empty.mjs
if ($LASTEXITCODE -ne 0) { throw 'Recycle empty component checks failed' }
node ./tools/check-relay-ui.mjs
if ($LASTEXITCODE -ne 0) { throw 'Relay component checks failed' }
node ./tools/check-account-health.mjs
if ($LASTEXITCODE -ne 0) { throw 'Account health checks failed' }
node ./tools/check-batch-share-revoke.mjs
if ($LASTEXITCODE -ne 0) { throw 'Batch share revocation checks failed' }
node ./tools/check-pending-review.mjs
if ($LASTEXITCODE -ne 0) { throw 'Pending review checks failed' }
node ./tools/check-session-renewal.mjs
if ($LASTEXITCODE -ne 0) { throw 'Session renewal checks failed' }
# Exercise the actual GUI executable and hidden-console helper launch flags.
# The login window appears briefly; this does not enter credentials or upload files.
if ($SkipDesktopSmoke) {
 Write-Host 'Skipping interactive packaged login windows on the build runner.'
} else {
 $previousReleaseExe=$env:MULTIPAN_RELEASE_EXE
 $previousProvider=$env:MULTIPAN_RELEASE_PROVIDER
 try {
  $env:MULTIPAN_RELEASE_EXE=(Resolve-Path (Join-Path 'bin' ($AppName+'.exe'))).Path
  foreach($provider in @('lanzou','quark','pan123','baidu','guangya')) {
   $env:MULTIPAN_RELEASE_PROVIDER=$provider
   go test . -run TestPackagedLoginWindowStartup -count=1
   if($LASTEXITCODE -ne 0){throw "Packaged $provider login window startup test failed"}
  }
 } finally {
  if($null -eq $previousProvider){Remove-Item Env:\MULTIPAN_RELEASE_PROVIDER -ErrorAction SilentlyContinue}else{$env:MULTIPAN_RELEASE_PROVIDER=$previousProvider}
  if($null -eq $previousReleaseExe){Remove-Item Env:\MULTIPAN_RELEASE_EXE -ErrorAction SilentlyContinue}else{$env:MULTIPAN_RELEASE_EXE=$previousReleaseExe}
 }
}
Get-FileHash -LiteralPath (Join-Path 'bin' ($AppName+'.exe')) -Algorithm SHA256
