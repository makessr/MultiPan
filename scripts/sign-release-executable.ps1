param(
 [Parameter(Mandatory)][string]$InputPath,
 [Parameter(Mandatory)][string]$OutputPath,
 [Parameter(Mandatory)][string]$CertificateThumbprint,
 [Parameter(Mandatory)][uri]$TimestampUri,
 [string]$SignToolPath
)
$ErrorActionPreference='Stop'

$thumbprint=$CertificateThumbprint -replace '\s',''
if($thumbprint -notmatch '^[0-9A-Fa-f]{40}$'){throw 'Specify a 40-digit code-signing certificate thumbprint.'}
if(!$TimestampUri.IsAbsoluteUri -or $TimestampUri.Scheme -ne 'https' -or $TimestampUri.UserInfo -or $TimestampUri.Fragment){
 throw 'Specify an absolute HTTPS RFC 3161 timestamp URL.'
}
$inputFile=Get-Item -LiteralPath $InputPath
$outputFile=$ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutputPath)
if($inputFile.PSIsContainer -or $inputFile.Extension -ne '.exe' -or [IO.Path]::GetExtension($outputFile) -ne '.exe'){
 throw 'Input and output must be Windows executables.'
}
if(Test-Path -LiteralPath $outputFile){throw 'The signed output path already exists.'}

$cert=Get-Item -LiteralPath ('Cert:\CurrentUser\My\'+$thumbprint) -ErrorAction SilentlyContinue
if(!$cert -or !$cert.HasPrivateKey -or (Get-Date) -lt $cert.NotBefore -or (Get-Date) -ge $cert.NotAfter){
 throw 'The selected current-user certificate must be valid and have a private key.'
}
$sdkTool=if($SignToolPath){Get-Command -Name (Resolve-Path -LiteralPath $SignToolPath).Path -CommandType Application}else{Get-Command signtool.exe -CommandType Application}

$folder=[IO.Path]::GetDirectoryName($outputFile)
if(!(Test-Path -LiteralPath $folder -PathType Container)){throw 'Create the release staging directory first.'}
$staged=Join-Path $folder ('.multipan-sign-'+[guid]::NewGuid().ToString('N')+'.exe')
try {
 [IO.File]::Copy($inputFile.FullName,$staged,$false)
 & $sdkTool.Source sign /s My /sha1 $thumbprint /fd SHA256 /tr $TimestampUri.AbsoluteUri /td SHA256 $staged
 if($LASTEXITCODE -ne 0){throw 'SignTool could not sign and timestamp the executable.'}
 & $sdkTool.Source verify /pa /all /tw $staged
 if($LASTEXITCODE -ne 0){throw 'Authenticode or timestamp verification failed.'}
 $signature=Get-AuthenticodeSignature -LiteralPath $staged
 if($signature.Status -ne 'Valid' -or $signature.SignatureType -ne 'Authenticode' -or
    $signature.SignerCertificate.Thumbprint -ne $thumbprint -or !$signature.TimeStamperCertificate){
  throw 'The signed executable does not match the selected certificate or lacks a timestamp.'
 }
 $digest=(Get-FileHash -LiteralPath $staged -Algorithm SHA256).Hash
 [IO.File]::Move($staged,$outputFile)
 [pscustomobject]@{Path=$outputFile; SHA256=$digest; Signer=$thumbprint}
} finally {
 if(Test-Path -LiteralPath $staged){Remove-Item -LiteralPath $staged}
}
