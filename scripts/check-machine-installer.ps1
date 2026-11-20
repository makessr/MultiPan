param(
 [Parameter(Mandatory)][string]$Installer,
 [Parameter(Mandatory)][string]$ApplicationPath,
 [Parameter(Mandatory)][ValidateSet('x64','arm64')][string]$Architecture,
 [switch]$Extract
)
$ErrorActionPreference='Stop'
$msi=(Resolve-Path -LiteralPath $Installer).Path
$app=(Resolve-Path -LiteralPath $ApplicationPath).Path
$engine=New-Object -ComObject WindowsInstaller.Installer
$db=$engine.OpenDatabase($msi,0)

function Read-MsiTable([string]$Table,[string[]]$Columns) {
 $identifier={ param($Value) [string][char]96+$Value+[char]96 }
 $fields=($Columns | ForEach-Object { & $identifier $_ }) -join ', '
 $view=$db.OpenView("SELECT $fields FROM $(& $identifier $Table)")
 try {
  [void]$view.Execute()
  while($record=$view.Fetch()){
   $row=[ordered]@{}
   for($i=0;$i -lt $Columns.Count;$i++){
    $row[$Columns[$i]]=$record.StringData($i+1)
   }
   [pscustomobject]$row
  }
 } finally {
  [void]$view.Close()
 }
}

$properties=@{}
Read-MsiTable 'Property' @('Property','Value') | ForEach-Object { $properties[$_.Property]=$_.Value }
$version=[Diagnostics.FileVersionInfo]::GetVersionInfo($app).FileVersion
$template=$db.SummaryInformation(0).Property(7)
$expectedTemplate=if($Architecture -eq 'x64'){'x64;2052'}else{'Arm64;2052'}
if($template -ine $expectedTemplate -or $properties.ALLUSERS -ne '1' -or $properties.ProductVersion -ne $version -or
   $properties.UpgradeCode -ine '{A049AB4B-D0E9-47E1-9378-995384F6A84E}' -or !$properties.ARPPRODUCTICON) {
 throw "MSI architecture, per-machine scope, version or product identity mismatch: $template"
}

$directories=Read-MsiTable 'Directory' @('Directory','Directory_Parent','DefaultDir')
$root=$directories | Where-Object Directory -eq 'InstallFolder'
$programFiles=$directories | Where-Object Directory -eq 'ProgramFiles6432Folder'
if($root.Directory_Parent -ne 'ProgramFiles6432Folder' -or $programFiles.Directory_Parent -ne 'ProgramFiles64Folder' -or $root.DefaultDir -ne 'MultiPan'){throw 'Unexpected machine installation directory'}
$files=Read-MsiTable 'File' @('File','FileName','FileSize')
$appFile=$files | Where-Object File -eq 'ApplicationExe'
if($files.Count -ne 3 -or $appFile.FileName -notmatch '(^|\|)MultiPan\.exe$' -or [long]$appFile.FileSize -ne (Get-Item -LiteralPath $app).Length){
 throw 'MSI payload differs from the verified application'
}

$shortcuts=Read-MsiTable 'Shortcut' @('Shortcut','Directory_','Target','WkDir','Icon_')
foreach($entry in @(@{Id='MultiPanStart'; Directory='ProgramMenuFolder'},@{Id='MultiPanDesktop'; Directory='DesktopFolder'})) {
 $shortcut=$shortcuts | Where-Object Shortcut -eq $entry.Id
 if($shortcut.Directory_ -ne $entry.Directory -or $shortcut.Target -cne '[#ApplicationExe]' -or
    $shortcut.WkDir -ne 'InstallFolder' -or !$shortcut.Icon_){throw "Unexpected shortcut $($entry.Id)"}
}
$features=Read-MsiTable 'Feature' @('Feature','Level')
$desktop=Read-MsiTable 'FeatureComponents' @('Feature_','Component_') | Where-Object Component_ -eq 'DesktopLink'
if(($features | Where-Object Feature -eq 'Core').Level -ne '1' -or
   ($features | Where-Object Feature -eq 'PublicDesktop').Level -ne '2' -or $desktop.Feature_ -ne 'PublicDesktop'){
 throw 'The public desktop shortcut must be a separate opt-in feature'
}
$sequence=@{}
Read-MsiTable 'InstallExecuteSequence' @('Action','Sequence') | ForEach-Object { $sequence[$_.Action]=[int]$_.Sequence }
if(!$sequence.RemoveExistingProducts -or $sequence.RemoveExistingProducts -le $sequence.InstallInitialize -or
   $sequence.RemoveExistingProducts -ge $sequence.InstallFiles){
 throw 'Old versions must be removed within the Windows Installer transaction'
}
$arp=Read-MsiTable 'CustomAction' @('Action','Type','Source','Target') | Where-Object Source -eq 'ARPINSTALLLOCATION'
if($arp.Type -ne '51' -or $arp.Target -cne '[InstallFolder]' -or !$sequence[$arp.Action]){
 throw 'The machine uninstall registration must include the resolved installation directory'
}
$launch=Read-MsiTable 'LaunchCondition' @('Condition')
$searches=(Read-MsiTable 'AppSearch' @('Property')).Property
if(!$sequence.AppSearch -or $sequence.AppSearch -ge $sequence.LaunchConditions -or
   !$sequence.FindRelatedProducts -or $sequence.FindRelatedProducts -ge $sequence.LaunchConditions){
 throw 'Collision and upgrade detection must precede launch conditions'
}
foreach($property in @('EXISTINGMACHINEFOLDER','EXISTINGSTARTLINK','EXISTINGDESKTOPLINK')){
 if($property -notin $searches -or -not ($launch.Condition -match $property)){throw "Missing collision check $property"}
}
$expectedHash=(Get-Content -LiteralPath ($msi+'.sha256') -Raw).Trim().Split(' ')[0]
if((Get-FileHash -LiteralPath $msi -Algorithm SHA256).Hash -ne $expectedHash){throw 'MSI checksum mismatch'}

if($Extract){
 # An administrative image only extracts into a new fixture directory. It
 # does not register or install the product on the host.
 $scratch=Join-Path ([IO.Path]::GetTempPath()) ('multipan-msi-image-'+[guid]::NewGuid().ToString('N'))
 $image=Join-Path $scratch 'image'
 $log=Join-Path $scratch 'extract.log'
 New-Item -ItemType Directory -Path $scratch | Out-Null
 $fixtureFolders=@{
  ProgramFiles64Folder=(Join-Path $scratch 'program')
  ProgramMenuFolder=(Join-Path $scratch 'menu')
  DesktopFolder=(Join-Path $scratch 'desktop')
 }
 $program=Join-Path $fixtureFolders.ProgramFiles64Folder 'MultiPan'
 $menu=Join-Path $fixtureFolders.ProgramMenuFolder 'MultiPan（所有用户）.lnk'
 $desktop=Join-Path $fixtureFolders.DesktopFolder 'MultiPan（所有用户）.lnk'
 foreach($fixture in $fixtureFolders.Values){
  New-Item -ItemType Directory -Path $fixture | Out-Null
 }
 $collisionCondition=($launch | Where-Object Condition -Match 'EXISTINGMACHINEFOLDER').Condition
 $searchFixture={
  param([bool]$HasCollision)
  $session=$engine.OpenPackage($msi,1)
  try {
   foreach($property in $fixtureFolders.Keys){
    [void]$session.GetType().InvokeMember('Property',[Reflection.BindingFlags]::SetProperty,$null,$session,
     @([string]$property,[string]($fixtureFolders[$property]+'\')))
   }
   if($session.DoAction('AppSearch') -ne 1){throw 'AppSearch failed in the test session'}
   $condition=$session.EvaluateCondition($collisionCondition)
   if($condition -ne [int](!$HasCollision)){throw "Unexpected collision condition result $condition"}
   if($HasCollision){
    foreach($property in @('EXISTINGMACHINEFOLDER','EXISTINGSTARTLINK','EXISTINGDESKTOPLINK')){
     if(!$session.Property($property)){throw "Collision fixture not found for $property"}
    }
   }
  } finally {
   [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($session)
  }
 }
 & $searchFixture $false
 New-Item -ItemType Directory -Path $program | Out-Null
 foreach($link in @($menu,$desktop)){Set-Content -LiteralPath $link -Value 'foreign shortcut'}
 & $searchFixture $true
 $start=Start-Process -FilePath (Join-Path ([Environment]::SystemDirectory) 'msiexec.exe') -ArgumentList @(
  '/a',('"{0}"' -f $msi),'/qn','/norestart',('TARGETDIR="{0}"' -f $image),'/l*v',('"{0}"' -f $log)
 ) -PassThru -Wait -WindowStyle Hidden
 if($start.ExitCode -ne 0){throw "MSI extraction failed ($($start.ExitCode)). Log: $log"}
 $extracted=@(Get-ChildItem -LiteralPath $image -Filter 'MultiPan.exe' -File -Recurse)
 if($extracted.Count -ne 1 -or (Get-FileHash -LiteralPath $extracted[0].FullName).Hash -ne (Get-FileHash -LiteralPath $app).Hash){
  throw "Extracted application differs from source. Log: $log"
 }
}
"Machine MSI verified: $Architecture $version; extraction=$Extract"
