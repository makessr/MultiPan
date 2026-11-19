# MultiPan 0.78.0 当前用户安装与保留数据卸载

2026-11-17，Windows x64。推进第9项发布流程；不把继续开发当作旧版真实账号验收。用户要求加入定时任务，已创建当前对话heartbeat“继续开发 MultiPan 并交付测试清单”，id=multipan；实际保存配置为每30分钟，ACTIVE，完整阶段交付后提供测试清单。

## 新增流程

- 独立安装EXE，使用现有Go工具链和原透明图标/Windows版本资源。NSIS官方下载节点返回HTML且摘要不符，未运行下载内容，本版不依赖NSIS。
- 当前用户安装到`%LOCALAPPDATA%\Programs\MultiPan`，稳定程序名`MultiPan.exe`。原生确认框显示版本、安装和数据目录，可取消；自动创建开始菜单入口，可选新增桌面快捷方式，使用Windows已知目录支持重定向桌面。
- 内嵌程序、试用指南、第三方说明和SHA256清单，先校验再安装，不含账号状态或下载内容。
- 与主程序使用同一用户状态锁（包括便携版），运行中提示先从托盘退出；暂存、备份旧托管文件再提交，常规提交失败回滚，不递归删除目录。回滚未完整完成保留备份。
- 原位升级/同版修复，不允许安装器降级。不接管非空外来目录，不覆盖手改程序/指南、指向其他程序的同名快捷方式或其他位置的安装登记。
- 当前用户HKCU“应用和功能”卸载入口。仅处理安装清单中核对过的文件及对应入口；保留`%APPDATA%\MultiPan`账号/任务、下载和目录内其他文件。有其他文件时保留安装标识，便于重装。
- 不自动打开客户端、不读写state.enc、不发网盘请求、不下载WebView2。缺WebView2时提示通过微软官方渠道安装。EXE/三文件便携ZIP继续保留，Windows默认`wails3 task package`已切到本项目安装器；旧NSIS模板不是本版发布路径。

## 限制

仅当前用户标准目录，无自选目录/系统级安装、在线自动更新或代码签名，不购买或伪造证书。界面为Windows原生分步确认框，完成后用户自行打开程序。安装程序尚未签名，可能出现系统提示，不降低安全策略。
不承诺断电/强杀每个提交点自动恢复；中断时保留程序目录备份，先核对再修复。卸载自身时使用临时助手并等待原卸载进程句柄退出（最多30秒），避免文件仍占用；助手可能留在系统临时目录，由系统清理，不使用批处理递归删除。
快捷方式/登记失败会明确报告警告，程序可从安装目录打开。干净无WebView2系统、不同用户/重定向/GPO/杀软环境仍需实测，自动验证不代替这些结论。

## 用户测试

1. 打开安装包，先取消，应不安装；再确认并按需选择桌面入口。打开客户端确认0.78.0及原账号/任务还在。
2. 客户端打开或在托盘时再次安装，应提示先退出；退出后同版修复成功，其他文件和账号保持。
3. 从Windows“应用和功能”卸载，确认程序/本程序入口移除，账号任务和下载保留；重装后仍可读取原数据。
4. 手改托管文件或同名快捷方式指向其他程序时，应停止覆盖，先保留该文件。反馈版本/步骤/脱敏提示，不发送state.enc或凭据。

## 文件与验证

新增internal/installation、tools/setup、tools/setupbundle、tools/packageinstaller，scripts/package-installer.ps1、check-installer.ps1；更新Windows打包Taskfile、.gitignore、Go依赖分类、第三方说明及六处版本/README/试用指南/开发计划。默认打包直接Go执行，避免Windows PowerShell执行策略拦截，不修改策略。网盘操作和搜索规则不变，旧EXE/ZIP及原图标保留。
核心测试覆盖篡改/越界载荷、外来目录、手改文件、提交/备份失败回滚、降级拒绝、用户文件/状态保留、运行锁、沙盒注册表与临时真实快捷方式。正式用户桌面、安装和数据未改。
完整build.ps1退出0：Go全测试/vet、TypeScript/Vite93模块、辅助/实际Vue、五家正式EXE登录窗口检查通过；安装核心最终测试通过。wails3 task package APP_NAME=multipan-0.78.0默认Go路径退出0，实际安装器校验通过；check-installer.ps1在独立标记临时根完成安装、模拟0.77已安装布局升级、文件锁下安装/卸载拒绝、同版修复、卸载、重装和用户状态/额外文件SHA保持。没有把“模拟旧版布局”当作旧安装器真实升级验收；也未验证原生交互确认框的自动点击，用户按上文测试。
package-trial三公开文件及ZIP摘要验证通过；安装器内嵌MultiPan.exe摘要与最终全测EXE一致。最终安装器还验证了从安装目录内的真实卸载EXE复制助手、等待父进程退出再卸载的完整交接；随后全Go测试/vet通过，asInvoker权限清单核对通过。证据build-0.78.0-verification.log、build-0.78.0-installer-verification.log、build-0.78.0-installer-final.log、build-0.78.0-installer-lifecycle.log、build-0.78.0-final-go-check.log；最终隔离根%TEMP%/multipan-setup-check-<运行标识>。

- 安装包：releases/MultiPan-0.78.0-windows-x64-setup.exe，11553280字节，SHA256 `D079932CFB6FF3F86610DDC1ED9F13822B7A3598BDCACEA5FDDE765A178E473F`；旁有.sha256文件。
- 便携EXE：bin/multipan-0.78.0.exe，16443904字节，SHA256 `90515A4F9C6D76B7887651BFA050FBB3C305570537E2C806F97A4AE9D556C013`。
- ZIP：releases/MultiPan-0.78.0-windows-x64.zip，7527829字节，SHA256 `51C1E1C37D1031094CC879F6566E11F75E3A5E2C38C28DF82089FAF009A5B4DF`。
