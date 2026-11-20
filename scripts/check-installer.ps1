param([string]$Installer='',[string]$PreviousApplication='bin/multipan-0.77.0.exe',[switch]$CustomDirectory,[string]$ApplicationPath='')
$ErrorActionPreference='Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
$version=[regex]::Match((Get-Content frontend/src/version.ts -Raw),"APP_VERSION\s*=\s*'([^']+)'").Groups[1].Value
if(-not $Installer){$Installer='releases/MultiPan-'+$version+'-windows-x64-setup.exe'}
$Installer=(Resolve-Path -LiteralPath $Installer).Path
$releaseInstaller=$Installer
$root=Join-Path ([IO.Path]::GetTempPath()) ('multipan-setup-check-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $root|Out-Null
[IO.File]::WriteAllText((Join-Path $root '.multipan-setup-test'),'synthetic installer checks')
$program=if($CustomDirectory){Join-Path $root '自选 程序\MultiPan'}else{Join-Path $root 'program'}
function Run-Setup([string]$Action,[int]$Expected=0,[string]$TargetOverride=''){
 $start=[Diagnostics.ProcessStartInfo]::new($Installer)
 $start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
 $start.Arguments='--action '+$Action+' --test-root "'+$root+'" --silent'
 if($TargetOverride){$start.Arguments+=' --install-dir "'+$TargetOverride+'"'}elseif($CustomDirectory -and $Installer -eq $releaseInstaller){$start.Arguments+=' --install-dir "'+$program+'"'}
 $p=[Diagnostics.Process]::Start($start)
 if(-not $p.WaitForExit(45000)){$p.Kill();throw 'Isolated installer timed out'}
 $output=$p.StandardOutput.ReadToEnd();$errors=$p.StandardError.ReadToEnd();$code=$p.ExitCode;$p.Dispose()
 if($code -ne $Expected){throw "Unexpected installer exit $code : $errors"}
 if($Expected -eq 0 -and -not (($output|ConvertFrom-Json).isolated)){throw 'Missing isolated execution marker'}
}
$profile=Join-Path $root 'profile/MultiPan';New-Item -ItemType Directory -Path $profile -Force|Out-Null
$state=Join-Path $profile 'state.enc';[IO.File]::WriteAllText($state,'synthetic encrypted state sentinel')
$stateHash=(Get-FileHash -LiteralPath $state -Algorithm SHA256).Hash
if($CustomDirectory){
 $outside=Join-Path ([IO.Path]::GetTempPath()) ('multipan-outside-'+[guid]::NewGuid().ToString('N'))
 Run-Setup 'install' 1 $outside
 if(Test-Path -LiteralPath $outside){throw 'Isolated installer wrote outside its test root'}
 Run-Setup 'install' 1 (Join-Path $profile 'app')
 if(Test-Path -LiteralPath (Join-Path $profile 'app')){throw 'Installer wrote inside profile data'}
}
Run-Setup 'install'
$app=Join-Path $program 'MultiPan.exe';$marker=Join-Path $program '.multipan-install.json'
if(-not $ApplicationPath){$ApplicationPath='bin/multipan-'+$version+'.exe'}
$expectedApp=(Get-FileHash -LiteralPath $ApplicationPath -Algorithm SHA256).Hash
if((Get-FileHash -LiteralPath $app -Algorithm SHA256).Hash -ne $expectedApp){throw 'Installed application hash mismatch'}
$extra=Join-Path $program 'user-notes.txt';[IO.File]::WriteAllText($extra,'keep unrelated user file')
$extraHash=(Get-FileHash -LiteralPath $extra -Algorithm SHA256).Hash
if(Test-Path -LiteralPath $PreviousApplication){
 Copy-Item -LiteralPath $PreviousApplication -Destination $app -Force
 $owned=Get-Content -LiteralPath $marker -Raw|ConvertFrom-Json
 $owned.version=[Diagnostics.FileVersionInfo]::GetVersionInfo((Resolve-Path -LiteralPath $PreviousApplication).Path).ProductVersion
 $owned.hashes.'MultiPan.exe'=(Get-FileHash -LiteralPath $app -Algorithm SHA256).Hash.ToLowerInvariant()
 [IO.File]::WriteAllText($marker,($owned|ConvertTo-Json -Depth 4),[Text.UTF8Encoding]::new($false))
 Run-Setup 'install'
 if((Get-FileHash -LiteralPath $app -Algorithm SHA256).Hash -ne $expectedApp){throw 'Upgrade did not install current binary'}
}
$lock=[IO.File]::Open($app,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
try {Run-Setup 'install' 1;Run-Setup 'uninstall' 1}finally{$lock.Dispose()}
Run-Setup 'install'
$releaseInstaller=$Installer
$Installer=Join-Path $program 'MultiPan-Uninstall.exe'
try {Run-Setup 'uninstall'}finally{$Installer=$releaseInstaller}
if(Test-Path -LiteralPath $app){throw 'Uninstall left managed executable'}
if(-not (Test-Path -LiteralPath $extra)){throw 'Uninstall removed unrelated file'}
if((Get-FileHash -LiteralPath $extra -Algorithm SHA256).Hash -ne $extraHash){throw 'Uninstall changed unrelated file'}
if((Get-FileHash -LiteralPath $state -Algorithm SHA256).Hash -ne $stateHash){throw 'Profile data changed'}
Run-Setup 'install'
Run-Setup 'uninstall'
[pscustomobject]@{Result='PASS';Root=$root;CustomDirectory=[bool]$CustomDirectory;Checks='install, simulated prior-version upgrade, locked-file refusal, repair, installed-uninstaller parent handoff, uninstall, reinstall, user/profile preservation';ProductionIntegration='not touched'}|ConvertTo-Json -Compress
