param(
 [string]$ApplicationPath='',
 [string]$OutputPath='',
 [switch]$Machine,
 [string]$WixPath='wix',
 [string]$WixUIExtension='WixToolset.UI.wixext'
)
$ErrorActionPreference='Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
$activate=Join-Path $env:LOCALAPPDATA 'Programs\MultiPanDev\Activate.ps1'
if(Test-Path -LiteralPath $activate){. $activate}
$arguments=@('run','-buildvcs=false','./tools/packageinstaller')
if($ApplicationPath){$arguments+=@('-exe',(Resolve-Path -LiteralPath $ApplicationPath).Path)}
if($OutputPath){$arguments+=@('-out',$ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutputPath))}
if($Machine){$arguments+=@('-machine','-wix',$WixPath,'-wix-ui',$WixUIExtension)}
& go @arguments
if($LASTEXITCODE -ne 0){throw 'Installer packaging failed'}
