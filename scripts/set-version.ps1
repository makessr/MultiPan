param(
 [Parameter(Mandatory)][ValidatePattern('^[0-9]+\.[0-9]+\.[0-9]+$')][string]$Version,
 [switch]$DryRun
)
$ErrorActionPreference = 'Stop'

# Keeps the three version sources in lockstep. build.ps1 refuses to build when the
# UI version, the Windows resource versions and the Wails app version disagree, so
# a bump must touch all of them plus the changelog.

$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
Set-Location -LiteralPath $root

$versionFile = 'frontend/src/version.ts'
$infoFile = 'build/windows/info.json'
$configFile = 'build/config.yml'

$utf8 = New-Object System.Text.UTF8Encoding($false)
$versionText = [IO.File]::ReadAllText((Join-Path $root $versionFile), [Text.Encoding]::UTF8)
$versionMatch = [regex]::Match($versionText, "APP_VERSION\s*=\s*'([0-9]+\.[0-9]+\.[0-9]+)'")
if (!$versionMatch.Success) { throw '无法从界面版本文件读取当前版本。' }
$currentVersion = $versionMatch.Groups[1].Value

$infoText = [IO.File]::ReadAllText((Join-Path $root $infoFile), [Text.Encoding]::UTF8)
$info = $infoText | ConvertFrom-Json
if ([string]$info.fixed.file_version -ne $currentVersion -or [string]$info.fixed.product_version -ne $currentVersion) {
 throw '当前版本源本身不一致，请先手工对齐再升版。'
}

$configText = [IO.File]::ReadAllText((Join-Path $root $configFile), [Text.Encoding]::UTF8)
$configMatch = [regex]::Match($configText, 'version:\s*"([0-9]+\.[0-9]+\.[0-9]+)"')
if (!$configMatch.Success -or $configMatch.Groups[1].Value -ne $currentVersion) {
 throw '当前版本源本身不一致，请先手工对齐再升版。'
}

if ($currentVersion -eq $Version) {
 "版本已经是 $Version，无需修改。"
 return
}

$newVersionText = $versionText.Substring(0, $versionMatch.Groups[1].Index) + $Version +
 $versionText.Substring($versionMatch.Groups[1].Index + $versionMatch.Groups[1].Length)
$newConfigText = $configText.Substring(0, $configMatch.Groups[1].Index) + $Version +
 $configText.Substring($configMatch.Groups[1].Index + $configMatch.Groups[1].Length)
$newInfoText = $infoText.Replace('"file_version": "' + $currentVersion + '"', '"file_version": "' + $Version + '"').
 Replace('"product_version": "' + $currentVersion + '"', '"product_version": "' + $Version + '"').
 Replace('"FileVersion": "' + $currentVersion + '"', '"FileVersion": "' + $Version + '"').
 Replace('"ProductVersion": "' + $currentVersion + '"', '"ProductVersion": "' + $Version + '"')

$check = $newInfoText | ConvertFrom-Json
foreach ($value in @($check.fixed.file_version, $check.fixed.product_version,
  $check.info.'0409'.FileVersion, $check.info.'0409'.ProductVersion)) {
 if ([string]$value -ne $Version) { throw 'Windows 资源的版本替换不完整，请检查版本文件格式。' }
}
if ($newInfoText -notmatch '"file_version"') { throw 'Windows 资源文件的版本字段无法识别。' }

if ($DryRun) {
 Write-Host "将把 $currentVersion 升为 $Version："
 Write-Host "  $versionFile"
 Write-Host "  $infoFile（file_version / product_version / 0409 FileVersion / ProductVersion）"
 Write-Host "  $configFile"
 return
}

[IO.File]::WriteAllText((Join-Path $root $versionFile), $newVersionText, $utf8)
[IO.File]::WriteAllText((Join-Path $root $configFile), $newConfigText, $utf8)
[IO.File]::WriteAllText((Join-Path $root $infoFile), $newInfoText, $utf8)

"版本已从 $currentVersion 升为 $Version。"
"下一步：在 CHANGELOG.md 增加对应版本的条目，并更新 README 中提到的版本号与安装包名。"
