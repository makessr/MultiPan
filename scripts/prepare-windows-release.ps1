param(
 [Parameter(Mandatory)][string]$X64Installer,
 [Parameter(Mandatory)][string]$Arm64Installer,
 [Parameter(Mandatory)][string]$PrivateKey,
 [Parameter(Mandatory)][ValidatePattern('^[0-9]+\.[0-9]+\.[0-9]+$')][string]$PreviousVersion,
 [Parameter(Mandatory)][string]$OutputDirectory
)
$ErrorActionPreference = 'Stop'

$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$destination = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutputDirectory)
$parent = (Resolve-Path -LiteralPath ([IO.Path]::GetDirectoryName($destination))).Path
$destination = Join-Path $parent ([IO.Path]::GetFileName($destination))
if (Test-Path -LiteralPath $destination) { throw '发行输出目录已存在。' }

$versionSource = Get-Content -LiteralPath (Join-Path $root 'frontend/src/version.ts') -Raw
$versionMatch = [regex]::Match($versionSource, "APP_VERSION\s*=\s*'([0-9]+\.[0-9]+\.[0-9]+)'")
if (!$versionMatch.Success) { throw '无法确定本次发行版本。' }
$version = $versionMatch.Groups[1].Value
if ([version]$PreviousVersion -ge [version]$version) { throw '用于验证更新的已安装版本必须低于发行版本。' }

$channel = Get-Content -LiteralPath (Join-Path $root 'build/update-channel.json') -Raw | ConvertFrom-Json
$publisher = [string]$channel.publisherKey
if ($publisher -notmatch '^[0-9a-fA-F]{64}$') { throw '客户端配置缺少有效的发行公钥。' }
$keyFile = Get-Item -LiteralPath $PrivateKey
if (!$keyFile -or $keyFile.PSIsContainer -or ($keyFile.Attributes -band [IO.FileAttributes]::ReparsePoint) -or
    $keyFile.FullName.StartsWith($root + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
 throw '请选择位于源码仓库之外的本机发行私钥文件。'
}

$packages = @(
 @{ Arch = 'amd64'; Platform = 'x64'; Source = $X64Installer; Manifest = 'update.json'; Signature = 'update.sig' }
 @{ Arch = 'arm64'; Platform = 'arm64'; Source = $Arm64Installer; Manifest = 'update-windows-arm64.json'; Signature = 'update-windows-arm64.sig' }
)
foreach ($package in $packages) {
 $file = Get-Item -LiteralPath $package.Source
 if (!$file -or $file.PSIsContainer -or $file.Extension -ne '.exe' -or ($file.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
  throw "指定的 $($package.Platform) 安装包必须为本机 EXE 普通文件。"
 }
 $package.Source = $file.FullName
}

# A sibling staging directory keeps incomplete asset sets away from the final output.
$stage = Join-Path $parent ('.multipan-windows-release-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $stage -ErrorAction Stop | Out-Null
Push-Location -LiteralPath $root
try {
 foreach ($package in $packages) {
  $name = "MultiPan-$version-windows-$($package.Platform)-setup.exe"
  $installer = Join-Path $stage $name
  [IO.File]::Copy($package.Source, $installer, $false)
  if ([Diagnostics.FileVersionInfo]::GetVersionInfo($installer).ProductVersion -ne $version) {
   throw "$($package.Platform) 安装包的产品版本与客户端版本不一致。"
  }
  $signature = Get-AuthenticodeSignature -LiteralPath $installer
  if ($signature.Status -ne 'Valid' -or $signature.SignatureType -ne 'Authenticode' -or !$signature.TimeStamperCertificate) {
   throw "$($package.Platform) 安装包需要可信的内嵌 Authenticode 签名及时间戳。"
  }

  $metadata = Join-Path $stage ('metadata-' + $package.Arch)
  $report = & go run -buildvcs=false ./tools/signupdate -installer $installer -version $version -private-key $keyFile.FullName -output-dir $metadata -require-authenticode
  if ($LASTEXITCODE -ne 0) { throw "$($package.Platform) 安装包的更新清单签名失败。" }
  $result = ($report -join "`n") | ConvertFrom-Json
  if ($result.publisherKey -ne $publisher) { throw '发行私钥和客户端预置公钥不匹配。' }

  $manifest = Join-Path $metadata $package.Manifest
  $detachedSignature = Join-Path $metadata $package.Signature
  $verifyArguments = @('-arch', $package.Arch, '-publisher-key', $publisher, '-installer', $installer,
   '-manifest', $manifest, '-signature', $detachedSignature, '-require-authenticode')
  $null = & go run -buildvcs=false ./tools/verifyupdate -first-install-version $version @verifyArguments
  if ($LASTEXITCODE -ne 0) { throw "$($package.Platform) 首次安装发行源验证失败。" }
  $null = & go run -buildvcs=false ./tools/verifyupdate -installed-version $PreviousVersion @verifyArguments
  if ($LASTEXITCODE -ne 0) { throw "$($package.Platform) 安装包和更新清单的离线验证失败。" }
  [IO.File]::Move($manifest, (Join-Path $stage $package.Manifest))
  [IO.File]::Move($detachedSignature, (Join-Path $stage $package.Signature))
  [IO.Directory]::Delete($metadata)
  $hash = (Get-FileHash -LiteralPath $installer -Algorithm SHA256).Hash.ToLowerInvariant()
  [IO.File]::WriteAllText("$installer.sha256", "$hash  $name`r`n", [Text.Encoding]::ASCII)
 }

 [IO.Directory]::Move($stage, $destination)
 [pscustomobject]@{ Directory = $destination; Version = $version; Architectures = @('x64','arm64') }
} finally {
 Pop-Location
 if (Test-Path -LiteralPath $stage) {
  $entry = Get-Item -LiteralPath $stage
  if ($entry.PSIsContainer -and !$entry.LinkType -and
      [string]::Equals($entry.FullName, $stage, [StringComparison]::OrdinalIgnoreCase) -and
      [string]::Equals($entry.Parent.FullName, $parent, [StringComparison]::OrdinalIgnoreCase)) {
   Remove-Item -LiteralPath $entry.FullName -Recurse -Force
  }
 }
}
