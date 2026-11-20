param(
 [Parameter(Mandatory)][ValidatePattern('^[0-9a-fA-F]{40}$')][string]$Commit,
 [ValidatePattern('^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$')][string]$Repository='makessr/MultiPan'
)

$ErrorActionPreference = 'Stop'
$Commit = $Commit.ToLowerInvariant()

function Read-GitHubJson {
 param([string[]]$CommandArguments)
 $response = & gh @CommandArguments
 if ($LASTEXITCODE -ne 0) { throw 'GitHub 核对失败，无法确认发布条件。' }
 return (ConvertFrom-Json -InputObject ($response -join "`n") -NoEnumerate)
}

function Confirm-Workflow {
 param([string]$Workflow, [string[]]$RequiredJobs)

 $runs = Read-GitHubJson -CommandArguments @(
  'run','list','--repo',$Repository,'--workflow',$Workflow,'--commit',$Commit,
  '--branch','main','--event','push','--status','completed','--limit','10',
  '--json','databaseId,headSha,conclusion,url'
 )
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

$comparison = Read-GitHubJson -CommandArguments @('api',"repos/$Repository/compare/$Commit...main")
if ($comparison.status -notin @('ahead','identical') -or $comparison.merge_base_commit.sha -ne $Commit) {
 throw '发布提交必须已合入 main。'
}

$tree = Read-GitHubJson -CommandArguments @('api',"repos/$Repository/git/trees/$($Commit)?recursive=1")
if ($tree.truncated) { throw '无法读取提交的完整工作流清单。' }
$windows = Confirm-Workflow -Workflow 'windows.yml' -RequiredJobs @('Windows x64 构建与验证','Windows arm64 构建与验证')
$desktop = $null
if ($tree.tree.path -contains '.github/workflows/desktop-unix.yml') {
 $desktop = Confirm-Workflow -Workflow 'desktop-unix.yml' -RequiredJobs @('Linux amd64','Linux arm64','macOS Apple Silicon','macOS Intel')
}

[pscustomobject]@{
 Repository=$Repository
 Commit=$Commit
 WindowsCI=$windows
 LinuxMacOSCI=$desktop
}
