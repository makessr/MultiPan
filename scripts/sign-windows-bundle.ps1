param(
 [Parameter(Mandatory)][string[]]$Path,
 [string]$PfxPath,
 [string]$PfxBase64,
 [string]$PfxPassword,
 [string]$CertificateThumbprint,
 [string]$TimestampServer = 'http://timestamp.digicert.com',
 [string]$SignToolPath,
 [switch]$AllowUntrustedSigner,
 [switch]$SkipChecksum
)
$ErrorActionPreference = 'Stop'

# Signs release executables, installers and MSI packages with an Authenticode
# certificate, then rewrites their .sha256 sidecars so the published digests match
# the signed bytes. Certificates come either from a PFX file / base64 PFX (the CI
# path, fed from secrets) or from a thumbprint already present in the user store.

function Resolve-WindowsSignTool {
 param([string]$Requested)

 if ($Requested) {
  $resolved = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Requested)
  if (!(Test-Path -LiteralPath $resolved -PathType Leaf)) { throw "找不到指定的 signtool：$Requested" }
  return $resolved
 }
 $command = Get-Command signtool.exe -CommandType Application -ErrorAction SilentlyContinue
 if ($command) { return $command.Source }
 $kits = Join-Path $env:ProgramFiles 'Windows Kits\10\bin'
 if (!(Test-Path -LiteralPath $kits -PathType Container)) {
  $kits = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits\10\bin'
 }
 if (Test-Path -LiteralPath $kits -PathType Container) {
  $arch = if ([Environment]::Is64BitOperatingSystem -and $env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'x64' }
  $versions = Get-ChildItem -LiteralPath $kits -Directory |
   Where-Object { $_.Name -match '^\d+\.\d+\.\d+\.\d+$' } |
   Sort-Object { [version]$_.Name } -Descending
  foreach ($version in $versions) {
   foreach ($candidateArch in @($arch, 'x64')) {
    $candidate = Join-Path (Join-Path $version.FullName $candidateArch) 'signtool.exe'
    if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
   }
  }
 }
 throw '找不到 signtool.exe，请安装 Windows SDK 或用 -SignToolPath 指定。'
}

$timestampUri = $null
if ($TimestampServer) {
 try { $timestampUri = [uri]$TimestampServer } catch { throw '时间戳服务地址无效。' }
 if (!$timestampUri.IsAbsoluteUri -or @('http', 'https') -notcontains $timestampUri.Scheme -or
     $timestampUri.UserInfo -or $timestampUri.Fragment -or !$timestampUri.Host) {
  throw '时间戳服务必须是绝对的 HTTP/HTTPS 地址，且不能包含用户信息或片段。'
 }
}

$targets = @()
foreach ($item in $Path) {
 $resolved = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($item)
 if (!(Test-Path -LiteralPath $resolved -PathType Leaf)) { throw "找不到待签名文件：$item" }
 $file = Get-Item -LiteralPath $resolved
 if (($file.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw "待签名文件不能是链接：$($file.Name)" }
 if (@('.exe', '.msi', '.dll') -notcontains $file.Extension.ToLowerInvariant()) {
  throw "只支持签名 EXE、MSI 与 DLL：$($file.Name)"
 }
 if ($targets | Where-Object { [string]::Equals($_.FullName, $file.FullName, [StringComparison]::OrdinalIgnoreCase) }) {
  throw "重复的待签名文件：$($file.Name)"
 }
 $targets += $file
}
if ($targets.Count -eq 0) { throw '没有待签名文件。' }

if ($PfxPath -and $PfxBase64) { throw 'PFX 文件与 PFX base64 只能提供一个。' }
$tempPfx = $null
$importedThumbprint = $null
$certificate = $null
try {
 if ($PfxPath -or $PfxBase64) {
  if ($CertificateThumbprint) { throw '已提供 PFX 时不要再指定证书指纹。' }
  $tempDirectory = Join-Path ([IO.Path]::GetTempPath()) ('multipan-sign-' + [guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $tempDirectory -ErrorAction Stop | Out-Null
  $tempPfx = Join-Path $tempDirectory 'release.pfx'
  if ($PfxBase64) {
   try { $bytes = [Convert]::FromBase64String(($PfxBase64 -replace '\s','')) } catch { throw 'PFX base64 内容无法解码。' }
   [IO.File]::WriteAllBytes($tempPfx, $bytes)
  } else {
   $source = (Resolve-Path -LiteralPath $PfxPath).Path
   [IO.File]::Copy($source, $tempPfx, $false)
  }
  if ($PfxPassword) {
   $secure = ConvertTo-SecureString -String $PfxPassword -AsPlainText -Force
   $imported = Import-PfxCertificate -FilePath $tempPfx -CertStoreLocation 'Cert:\CurrentUser\My' -Password $secure
  } else {
   $imported = Import-PfxCertificate -FilePath $tempPfx -CertStoreLocation 'Cert:\CurrentUser\My'
  }
  if (!$imported) { throw 'PFX 中没有可用的代码签名证书。' }
  $candidate = @($imported) | Where-Object { $_.HasPrivateKey } | Select-Object -First 1
  if (!$candidate) { throw 'PFX 中的证书没有私钥。' }
  $importedThumbprint = $candidate.Thumbprint
  $certificate = Get-Item -LiteralPath ('Cert:\CurrentUser\My\' + $importedThumbprint)
 } else {
  $thumbprint = ($CertificateThumbprint -replace '\s','')
  if ($thumbprint -notmatch '^[0-9A-Fa-f]{40}$') { throw '请提供 40 位证书指纹，或用 PFX 文件 / base64 提供证书。' }
  $certificate = Get-Item -LiteralPath ('Cert:\CurrentUser\My\' + $thumbprint) -ErrorAction SilentlyContinue
  if (!$certificate) { throw '当前用户证书存储中没有该指纹的证书。' }
 }
 if (!$certificate.HasPrivateKey) { throw '代码签名证书缺少私钥。' }
 if ((Get-Date) -lt $certificate.NotBefore -or (Get-Date) -ge $certificate.NotAfter) { throw '代码签名证书不在有效期内。' }

 $signTool = Resolve-WindowsSignTool -Requested $SignToolPath
 $results = @()
 foreach ($file in $targets) {
  $arguments = @('sign', '/sha1', $certificate.Thumbprint, '/fd', 'SHA256')
  if ($timestampUri) { $arguments += @('/tr', $timestampUri.AbsoluteUri.TrimEnd('/'), '/td', 'SHA256') }
  $arguments += $file.FullName
  $signOutput = & $signTool @arguments 2>&1 | Out-String
  if ($LASTEXITCODE -ne 0) { throw "signtool 无法签名 $($file.Name)：`n$($signOutput.Trim())" }

  if (!$AllowUntrustedSigner) {
   $verifyOutput = & $signTool verify /pa /all /tw $file.FullName 2>&1 | Out-String
   if ($LASTEXITCODE -ne 0) { throw "$($file.Name) 的 Authenticode 校验失败：`n$($verifyOutput.Trim())" }
  }

  $signature = Get-AuthenticodeSignature -LiteralPath $file.FullName
  if ($signature.SignatureType -ne 'Authenticode' -or
      [string]::Equals($signature.SignerCertificate.Thumbprint, $certificate.Thumbprint, [StringComparison]::OrdinalIgnoreCase) -eq $false) {
   throw "$($file.Name) 的签名者与所选证书不一致。"
  }
  if (!$AllowUntrustedSigner -and $signature.Status -ne 'Valid') {
   throw "$($file.Name) 的签名不可信：$($signature.StatusMessage)"
  }
  if ($timestampUri -and !$signature.TimeStamperCertificate) { throw "$($file.Name) 缺少时间戳。" }

  $hash = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
  if (!$SkipChecksum) {
   [IO.File]::WriteAllText("$($file.FullName).sha256", "$hash  $($file.Name)`r`n", [Text.Encoding]::ASCII)
  }
  $results += [pscustomobject]@{
   Name = $file.Name
   SHA256 = $hash
   Status = [string]$signature.Status
   Timestamped = [bool]$signature.TimeStamperCertificate
   Signer = $certificate.Thumbprint
  }
 }
 $results
} finally {
 if ($importedThumbprint) {
  Remove-Item -LiteralPath ('Cert:\CurrentUser\My\' + $importedThumbprint) -Force -ErrorAction SilentlyContinue
 }
 if ($tempPfx -and (Test-Path -LiteralPath $tempPfx)) {
  $folder = [IO.Path]::GetDirectoryName($tempPfx)
  Remove-Item -LiteralPath $tempPfx -Force -ErrorAction SilentlyContinue
  if (Test-Path -LiteralPath $folder) { Remove-Item -LiteralPath $folder -Recurse -Force -ErrorAction SilentlyContinue }
 }
}
