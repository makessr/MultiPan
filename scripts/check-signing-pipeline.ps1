param(
 [string]$SignToolPath
)
$ErrorActionPreference = 'Stop'

# Exercises the Authenticode signing pipeline without a real release certificate:
# a throwaway self-signed code-signing certificate signs a freshly built, unsigned
# sample EXE, then the script asserts the signature, the missing timestamp
# behaviour and the rewritten .sha256 sidecar. Everything happens in the temp
# directory and no repository file is touched.

$signer = Join-Path $PSScriptRoot 'sign-windows-bundle.ps1'
if (!(Test-Path -LiteralPath $signer -PathType Leaf)) { throw '找不到签名脚本。' }
if (!(Get-Command go -CommandType Application -ErrorAction SilentlyContinue)) { throw '需要 Go 工具链来生成未签名的示例程序。' }

$folder = Join-Path ([IO.Path]::GetTempPath()) ('multipan-sign-check-' + [guid]::NewGuid().ToString('N'))
$created = $null
try {
 New-Item -ItemType Directory -Path $folder -ErrorAction Stop | Out-Null
 $source = Join-Path $folder 'main.go'
 $goSource = @'
package main

func main() { println("multipan signing sample") }
'@
 [IO.File]::WriteAllText($source, $goSource, [Text.Encoding]::ASCII)
 $sample = Join-Path $folder 'sample.exe'
 Push-Location -LiteralPath $folder
 try {
  $env:CGO_ENABLED = '0'
  & go build -buildvcs=false -ldflags '-s -w' -o $sample $source
  if ($LASTEXITCODE -ne 0) { throw '示例程序构建失败。' }
 } finally {
  Pop-Location
 }
 if ((Get-AuthenticodeSignature -LiteralPath $sample).Status -ne 'NotSigned') { throw '示例程序应当未签名。' }
 $before = (Get-FileHash -LiteralPath $sample -Algorithm SHA256).Hash

 $created = New-SelfSignedCertificate -Type CodeSigningCert `
  -Subject 'CN=MultiPan Signing Pipeline Test' -KeyAlgorithm RSA -KeyLength 2048 `
  -HashAlgorithm SHA256 -CertStoreLocation 'Cert:\CurrentUser\My' `
  -NotAfter (Get-Date).AddDays(2) -KeyExportPolicy Exportable
 $created = Get-Item -LiteralPath ('Cert:\CurrentUser\My\' + $created.Thumbprint)
 if (!$created.HasPrivateKey) { throw '自签名证书缺少私钥。' }

 $arguments = @{
  Path = $sample
  CertificateThumbprint = $created.Thumbprint
  TimestampServer = ''
  AllowUntrustedSigner = $true
 }
 if ($SignToolPath) { $arguments.SignToolPath = $SignToolPath }
 $result = & $signer @arguments

 $signed = @($result)
 if ($signed.Count -ne 1 -or $signed[0].Name -ne 'sample.exe') { throw '签名结果条目不正确。' }
 $signature = Get-AuthenticodeSignature -LiteralPath $sample
 if ($signature.SignatureType -ne 'Authenticode') { throw '示例文件没有 Authenticode 签名。' }
 if (![string]::Equals($signature.SignerCertificate.Thumbprint, $created.Thumbprint, [StringComparison]::OrdinalIgnoreCase)) {
  throw '示例文件的签名者与测试证书不一致。'
 }
 $after = (Get-FileHash -LiteralPath $sample -Algorithm SHA256).Hash
 if ($before -eq $after) { throw '签名后文件内容没有变化。' }
 $sidecar = (Get-Content -LiteralPath "$sample.sha256" -Raw).Trim()
 if ($sidecar -ne ("$($after.ToLowerInvariant())  sample.exe")) { throw '.sha256 伴随文件没有按签名后的内容重写。' }
 if ($signature.TimeStamperCertificate) { throw '未启用时间戳时不应出现时间戳证书。' }

 "签名流程检查通过：无时间戳签名、签名者匹配、校验和已重写。"
} finally {
 if ($created) {
  Remove-Item -LiteralPath ('Cert:\CurrentUser\My\' + $created.Thumbprint) -Force -ErrorAction SilentlyContinue
 }
 if (Test-Path -LiteralPath $folder) {
  $entry = Get-Item -LiteralPath $folder
  if ($entry.PSIsContainer -and !$entry.LinkType) { Remove-Item -LiteralPath $entry.FullName -Recurse -Force -ErrorAction SilentlyContinue }
 }
}
