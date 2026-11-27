param(
 [Parameter(Mandatory)][ValidatePattern('^[0-9a-fA-F]{40}$')][string]$Commit,
 [ValidatePattern('^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$')][string]$Repository='makessr/MultiPan',
 [ValidatePattern('^v[0-9]+\.[0-9]+\.[0-9]+(?:-preview\.[0-9]+)?$')][string]$DraftTag,
 [string]$WindowsAssetsDirectory
)

$ErrorActionPreference = 'Stop'
$Commit = $Commit.ToLowerInvariant()
if ($WindowsAssetsDirectory) {
 if (!$DraftTag -or $DraftTag.Contains('-preview.')) { throw '本机 Windows 正式产物仅可与正式版草稿核对。' }
 $WindowsAssetsDirectory = (Resolve-Path -LiteralPath $WindowsAssetsDirectory).Path
}

function ConvertFrom-ResponseJson {
 param([string]$Json)
 if ($PSVersionTable.PSVersion.Major -ge 6) { return (ConvertFrom-Json -InputObject $Json -NoEnumerate) }
 return (ConvertFrom-Json -InputObject $Json)
}

function Read-GitHubJson {
 param([string[]]$CommandArguments)
 $response = & gh @CommandArguments
 if ($LASTEXITCODE -ne 0) { throw 'GitHub 核对失败，无法确认发布条件。' }
 return (ConvertFrom-ResponseJson -Json ($response -join "`n"))
}

function Confirm-Workflow {
 param([string]$Workflow, [string[]]$RequiredJobs)

 $runs = @(Read-GitHubJson -CommandArguments @(
  'run','list','--repo',$Repository,'--workflow',$Workflow,'--commit',$Commit,
  '--branch','main','--event','push','--status','completed','--limit','10',
  '--json','databaseId,headSha,conclusion,url'
 ))
 foreach ($run in $runs) {
  if ($run.headSha -ne $Commit -or $run.conclusion -ne 'success') { continue }
  $details = Read-GitHubJson -CommandArguments @(
   'run','view',[string]$run.databaseId,'--repo',$Repository,'--json','jobs'
  )
  $successful = @($details.jobs | Where-Object conclusion -eq 'success' | ForEach-Object name)
  $missing = @($RequiredJobs | Where-Object { $_ -notin $successful })
  if ($missing.Count -eq 0) { return $run.url }
 }
 throw "提交 $Commit 在 main 上缺少 $Workflow 的完整成功结果。所需任务：$($RequiredJobs -join '、')"
}

function Confirm-DraftAssets {
 param([bool]$IncludeUnix)

 $draft = Read-GitHubJson -CommandArguments @(
  'release','view',$DraftTag,'--repo',$Repository,'--json','tagName,targetCommitish,isDraft,isPrerelease,assets'
 )
 if (!$draft.isDraft -or $draft.tagName -cne $DraftTag -or $draft.targetCommitish -cne $Commit) {
  throw '草稿标签或目标提交与本次检查不匹配。'
 }
 $version = $DraftTag.Substring(1).Split('-')[0]
 $preview = $DraftTag.Contains('-preview.')
 if ($preview -ne $draft.isPrerelease) { throw '草稿的预发布标记与标签不一致。' }

 $packages = @("MultiPan-$version-windows-arm64-setup.exe")
 if ($preview) {
  $packages += @('MultiPan-setup.exe','multipan.exe','multipan-windows-arm64.exe')
 } else {
  $packages += "MultiPan-$version-windows-x64-setup.exe"
 }
 if ($IncludeUnix) {
  foreach ($arch in @('amd64','arm64')) {
   $packages += ('multipan_{0}_{1}.deb' -f $version,$arch)
   if ($preview) { $packages += "MultiPan-$version-$arch-macOS.zip" }
  }
 }
 $required = @('LICENSE','THIRD_PARTY_NOTICES.md')
 foreach ($package in $packages) { $required += @($package,"$package.sha256") }
 if (!$preview) { $required += @('update.json','update.sig','update-windows-arm64.json','update-windows-arm64.sig') }
 $verified = @{}
 foreach ($asset in @($draft.assets)) {
  if ($asset.state -eq 'uploaded' -and $asset.size -gt 0 -and $asset.digest -match '^sha256:[0-9a-f]{64}$') {
   $verified[$asset.name] = $asset.digest
  }
 }
 $missing = @($required | Where-Object { !$verified.ContainsKey($_) })
 if ($missing.Count) { throw "草稿缺少附件或 GitHub 摘要：$($missing -join '、')" }
 if ($WindowsAssetsDirectory) {
  foreach ($name in $required | Where-Object { $_ -match '(?:-setup\.exe(?:\.sha256)?|^update(?:-windows-arm64)?\.(?:json|sig))$' }) {
   $path = Join-Path $WindowsAssetsDirectory $name
   if (!(Test-Path -LiteralPath $path -PathType Leaf)) { throw "本机正式产物缺少 $name" }
   $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash
   if ("sha256:$hash" -ne $verified[$name]) { throw "草稿附件与本机验签产物的 SHA-256 不一致：$name" }
  }
 }
 return $required.Count
}

$comparison = Read-GitHubJson -CommandArguments @('api',"repos/$Repository/compare/$Commit...main")
if ($comparison.status -notin @('ahead','identical') -or $comparison.merge_base_commit.sha -ne $Commit) {
 throw '发布提交必须已合入 main。'
}

$tree = Read-GitHubJson -CommandArguments @('api',"repos/$Repository/git/trees/$($Commit)?recursive=1")
if ($tree.truncated) { throw '无法读取提交的完整工作流清单。' }
$paths = @($tree.tree.path)
$windows = Confirm-Workflow -Workflow 'windows.yml' -RequiredJobs @('Windows x64 构建与验证','Windows arm64 构建与验证')
$desktop = $null
if ($paths -contains '.github/workflows/desktop-unix.yml') {
 $desktopJobs = @('Linux amd64','Linux arm64','macOS Apple Silicon','macOS Intel')
 if ($paths -contains 'scripts/check-apt-repository.sh') { $desktopJobs += '验证双架构 APT 仓库' }
 $desktop = Confirm-Workflow -Workflow 'desktop-unix.yml' -RequiredJobs $desktopJobs
}

$draftCount = if ($DraftTag) { Confirm-DraftAssets -IncludeUnix ($null -ne $desktop) }

[pscustomobject]@{
 Repository=$Repository
 Commit=$Commit
 WindowsCI=$windows
 LinuxMacOSCI=$desktop
 DraftTag=$DraftTag
 VerifiedDraftAssets=$draftCount
}
