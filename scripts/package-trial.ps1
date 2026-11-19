param([string]$ApplicationPath='', [string]$OutputDirectory='')
$ErrorActionPreference='Stop'
$repositoryRoot=Split-Path $PSScriptRoot -Parent
$versionSource=Get-Content -LiteralPath (Join-Path $repositoryRoot 'frontend/src/version.ts') -Raw
$versionMatch=[regex]::Match($versionSource,"APP_VERSION\s*=\s*'(\d+\.\d+\.\d+)'")
if(-not $versionMatch.Success){throw 'Invalid trial version'}
$trialVersion=$versionMatch.Groups[1].Value
$exeName='multipan-'+$trialVersion+'.exe'
$exePath=if($ApplicationPath){(Resolve-Path -LiteralPath $ApplicationPath).Path}else{Join-Path $repositoryRoot ('bin/'+$exeName)}
$guidePath=Join-Path $repositoryRoot 'TRIAL-GUIDE.txt'
if(-not (Test-Path -LiteralPath $exePath -PathType Leaf)){throw 'Build executable first'}
if(-not (Test-Path -LiteralPath $guidePath -PathType Leaf)){throw 'Missing trial guide'}
if($ApplicationPath){
 $versionInfo=(Get-Item -LiteralPath $exePath).VersionInfo
 $exeVersion='{0}.{1}.{2}' -f $versionInfo.FileMajorPart,$versionInfo.FileMinorPart,$versionInfo.FileBuildPart
 if($exeVersion -ne $trialVersion){throw 'Executable version does not match the trial version.'}
}
$releaseRoot=if($OutputDirectory){(Resolve-Path -LiteralPath $OutputDirectory).Path}else{Join-Path $repositoryRoot 'releases'}
$stageRoot=Join-Path $releaseRoot ('MultiPan-'+$trialVersion+'-windows-x64')
$archivePath=Join-Path $releaseRoot ('MultiPan-'+$trialVersion+'-windows-x64.zip')
if($OutputDirectory -and ((Test-Path -LiteralPath $stageRoot) -or (Test-Path -LiteralPath $archivePath))){throw 'The release staging package already exists.'}
New-Item -ItemType Directory -Path $stageRoot -Force:(!$OutputDirectory) | Out-Null
$stageExe=Join-Path $stageRoot $exeName
$stageGuide=Join-Path $stageRoot 'TRIAL-GUIDE.txt'
$stageHashes=Join-Path $stageRoot 'SHA256.txt'
Copy-Item -LiteralPath $exePath -Destination $stageExe -Force
Copy-Item -LiteralPath $guidePath -Destination $stageGuide -Force
$expectedHashes=@{}
$expectedHashes[$exeName]=(Get-FileHash -LiteralPath $stageExe -Algorithm SHA256).Hash
$expectedHashes['TRIAL-GUIDE.txt']=(Get-FileHash -LiteralPath $stageGuide -Algorithm SHA256).Hash
$manifest=($expectedHashes.Keys | Sort-Object | ForEach-Object {$expectedHashes[$_]+'  '+$_}) -join [Environment]::NewLine
[IO.File]::WriteAllText($stageHashes,$manifest+[Environment]::NewLine,[Text.UTF8Encoding]::new($false))
$expectedHashes['SHA256.txt']=(Get-FileHash -LiteralPath $stageHashes -Algorithm SHA256).Hash
# Only explicitly named public artifacts enter the archive, never state.enc or the repo.
Compress-Archive -LiteralPath @($stageExe,$stageGuide,$stageHashes) -DestinationPath $archivePath -CompressionLevel Optimal -Force:(!$OutputDirectory)
Add-Type -AssemblyName System.IO.Compression.FileSystem
$archive=[IO.Compression.ZipFile]::OpenRead($archivePath)
try {
 if($archive.Entries.Count -ne 3){throw 'Unexpected archive file count'}
 foreach($entry in $archive.Entries){
  if(-not $expectedHashes.ContainsKey($entry.FullName)){throw 'Unexpected archive entry'}
  $entryStream=$entry.Open();$digest=[Security.Cryptography.SHA256]::Create()
  try{$actual=[BitConverter]::ToString($digest.ComputeHash($entryStream)).Replace('-','')}finally{$entryStream.Dispose();$digest.Dispose()}
  if($actual -ne $expectedHashes[$entry.FullName]){throw 'Archive content checksum mismatch'}
 }
} finally {$archive.Dispose()}
[pscustomobject]@{Archive=$archivePath;Bytes=(Get-Item -LiteralPath $archivePath).Length;SHA256=(Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash;Files=3} | ConvertTo-Json -Compress
