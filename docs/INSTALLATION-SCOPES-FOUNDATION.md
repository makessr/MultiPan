# 系统级安装：范围归属与隔离实现检查点

日期：2026-11-18，当前正式交付仍为 **0.98.0**。

本轮完成第7项系统级安装的后端基础和隔离验证，**尚未开放生产系统级安装**。没有新增正式版本、没有覆盖已发布 EXE/ZIP/安装包，也没有执行提权、写 HKLM、改公共快捷方式或访问其他用户的账号数据。开发检查用安装器不作为发布包。

## 已实现

安装标识新增 `scope`，新安装明确保存 `current-user` 或沙盒专用 `machine`。旧标识和旧登记缺少范围时只解释为当前用户；未知值、错误类型、登记与标识范围不一致均拒绝处理。范围是归属信息，不是权限证明或防篡改签名。

公开 `Install` / `Uninstall` 仍只操作当前用户范围。机器范围写入函数不导出；非测试调用只能通过 `ScopedSandbox`，每次核对本机临时目录、规范路径、专用前缀、精确标识内容、固定程序目录与快捷方式路径，拒绝链接/联接。生产安装器没有机器范围开关。模拟机器登记仍使用随机 **HKCU 测试子键**，不能据此声称 HKLM/管理员行为已通过。

安装集成核对登记的 `InstallScope`。Apply 必须有匹配范围的安装标识；Remove 在任何快捷方式写操作前核对登记位置和范围。沙盒的登记位置必须先符合固定根绑定，才允许读目标文件，恶意网络登记不会先触发目录读取。卸载器定位也拒绝另一范围的标识。

载荷 ZIP 不能指定安装范围，避免把载荷清单当成机器安装授权。准备载荷后、实际替换前，再核对路径、原标识、原受管文件摘要与可写性；首次安装若期间出现同名程序或标识，拒绝覆盖。失败回滚保留原范围和原文件，原数据与未知文件保留规则不变。

## 隔离映射

| 项目 | 当前用户模拟 | 机器范围模拟 |
| --- | --- | --- |
| 程序目录 | 随机根/current-user/MultiPan | 随机根/machine/MultiPan |
| 开始菜单/桌面 | 随机根/shell/current-user | 随机根/shell/machine |
| 登记 | HKCU/Software/MultiPan/InstallerTests/随机根摘要/current-user | 同一测试命名空间的 machine 子键 |
| 数据哨兵 | 随机根/profiles/user-a 与 user-b | 同两份哨兵，安装/卸载均保留 |

机器模拟使用普通权限和合成 `MZ` 文件，不执行合成程序。两个 profile 哨兵证明文件未被本轮操作改动，**不代表已经完成两个真实 Windows 用户的权限或 DPAPI 测试**。保留临时根作为验证证据；测试只删除自己随机创建的登记子键，不递归删除未知目录。

## 修改文件

- `internal/installation/scope.go`：范围类型、旧标识兼容与严格归属核对。
- `internal/installation/scoped_sandbox.go`：只在标记临时根运行的双范围能力，固定目录、测试 HKCU 和快捷方式隔离。
- `internal/installation/install.go`：保存范围、阻止跨范围安装卸载，提交前再次检查归属/文件变化。
- `internal/installation/windows.go`：登记范围检查、集成写操作前置核对、沙盒路径绑定。
- `internal/installation/location.go`：当前用户卸载器不接管机器标识。
- `internal/installation/scope_test.go`：新增9个测试入口及矩阵/异常子场景。
- `internal/installation/install_test.go`：集成夹具改为先安装有效归属标识，再测试快捷方式和登记。
- `docs/DEVELOPMENT-PLAN.md` 与本文：阶段结果、边界和下一步。

## 验证结果

1. 旧/当前用户/机器标识与安装、卸载范围矩阵；未知范围及载荷指定范围拒绝。
2. 两套模拟安装同时存在，分别升级、降级拒绝、缺文件修复、卸载、重装；另一范围及两份数据哨兵保持。
3. 真实 Windows 文件读句柄占用拒绝升级和卸载；机器模拟提交中断后回滚，原标识及程序保持。
4. 沙盒标识在提交前丢失、原范围/程序文件在准备期间改变、首次安装期间出现同名文件/标识，均拒绝覆盖。
5. 程序根和快捷方式祖先的真实目录联接拒绝，联接外哨兵不变。
6. 登记范围缺失/错误/未来值/错误注册表类型、登记指向网络路径均拒绝；旧当前用户无 scope 仍能升级和卸载。
7. 全部安装包测试 `go test ./internal/installation -count=1 -timeout 120s` 通过（5.632秒）；后续 `go test ./...` 通过（installation 5.542秒，其余包使用有效测试缓存），`go vet ./...` 退出0。无前端或应用逻辑改动，本轮未重复前端构建或五家登录检查。
8. 用现有嵌入0.98.0载荷编译独立开发安装器 `bin/multipan-scope-foundation-setup-check.exe`，未使用正式安装包输出路径。现有 `scripts/check-installer.ps1` 默认和中文空格自选目录两套生命周期均通过：安装、模拟0.97升级、占用拒绝、修复、已安装卸载器父进程交接、卸载、重装、profile/未知文件保留。

证据：`build-system-scope-foundation.log`（最初8个入口）、`build-system-scope-installation.log`（含新增登记兼容测试的最终全部安装测试）、`build-system-scope-go.log`、`build-system-scope-installer-default.log`、`build-system-scope-installer-custom.log`。

实际安装器沙盒根：

- 默认：`%TEMP%/multipan-setup-check-<运行标识>`。
- 自选：`%TEMP%/multipan-setup-check-<运行标识>`。

正式0.98.0三件产物 SHA256 仍为 B2C425D5…2276B0B、CE90FE19…755A648、78C166D3…8BACBC6（完整值在 RELEASE-0.98.0.md）；两个原 PNG SHA256 仍为483919B8CD3B6D72153FE747EF24484FF1D27830CE31AB277DFD8308BD5C06E2，原图标备份保留。没有重打正式包。

## 开放机器安装前还需完成

1. 受控管理员入口和取消路径。主应用继续 `asInvoker`，不能为了安装而让网盘客户端常态以管理员身份运行；不能从普通用户可改的请求/临时可执行文件直接信任提权后的参数。
2. 使用 Windows Known Folder 解析受保护的 Program Files 和公共入口，机器登记使用固定 HKLM 64位位置，目录归属与 scope 共同核对；不能仅换一个环境变量或在当前用户可写目录放机器程序。
3. 创建及升级时核对目录、父路径、受管文件/标识的所有者和 DACL，普通用户只有所需读取/执行权；处理联接、硬链接及检查到提交期间变化。当前 scope 是公开 JSON，不能代替这些检查。
4. 跨用户/跨会话的程序占用与安装互斥，应用运行期间的程序目录租约；另明确同用户多会话数据锁。目前 `main.go` 和安装器的 `LockProfile` 都使用 `Local\\MultiPan-…`，不能据此声称系统范围排他。一次可写性检查与同会话测试也不能证明没有并发启动窗口。
5. 当前用户与机器版同存时公共入口命名和升级策略，避免覆盖私有入口、不自动迁移或合并账号数据。UAC换用户凭据后不能把管理员的 HKCU 当作原发起用户。
6. 机器卸载器范围/根绑定、受保护的辅助程序交接及明确确认，保留所有用户各自的数据；完成标准/管理员双用户与多会话验证后再发布生产入口。

`main.go` 当前使用每用户 `os.UserConfigDir()`，`vault_windows.go` 的 DPAPI 没有 `CRYPTPROTECT_LOCAL_MACHINE`；本阶段不改变数据位置、加密范围或旧任务。旧已发布0.98.0安装器没有本轮的新范围检查，不得把它当成机器卸载器；当前尚无正式机器安装状态。

官方依据：微软 [安装上下文](https://learn.microsoft.com/en-us/windows/win32/msi/installation-context) 对比每机器/每用户的可见性、公共入口和登记位置；[内核对象命名空间](https://learn.microsoft.com/en-us/windows/win32/termserv/kernel-object-namespaces) 说明 Local 与 Global 的会话边界；[Known Folder 标识](https://learn.microsoft.com/en-us/windows/win32/shell/knownfolderid) 定义系统文件夹标识。前者是 MSI 的规则说明，项目仍是 Go EXE 安装器，不能声称调用 MSI 属性就会自动完成隔离。本文的具体 Go 方案为基于这些规则和现有源码的工程设计。

本阶段没有新增需要用户手动操作的功能，不要求为测试重新安装或创建 Windows 用户。下一阶段先完成跨会话/目录租约和受保护机器目录的实现与隔离测试，再接生产提权与机器入口；签名与自动更新仍未完成。
