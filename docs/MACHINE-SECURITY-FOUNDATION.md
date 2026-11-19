# 机器安装目录：只读安全核对基础

## 中断事务只读核对（2026-11-19）

隔离构建的安装工具可通过 `--inspect-machine-checkpoint --test-root <已标记临时根> --silent` 输出 JSON。报告具有 `present` 字段。存在有效检查点时提供 `previousVersion` 和逐文件的 `originalPresent`、`installed`、`staging`、`backup`。位置状态为 `missing`、`previous`、`staged`、`both` 或 `other`，表示读取时文件状态及与检查点清单的匹配情况。

诊断复用隔离目录权限策略、非跟随重解析点句柄及身份快照。暂存和备份名限固定前缀加 32 位十六进制，待核对文件名来自已有受管清单。读取过程保留目录与检查点句柄，结束前重新核对路径和对象快照。旧安装标识按清单结构比对，其余内容按 SHA-256 比对。损坏的检查点或读取异常会中止诊断。当前诊断报告用于定位已观察到的状态，后续恢复还需在写入前重新建立可信证据和处理登记、入口等状态。

安装工具通过 `go build`，隔离测试程序通过 `go test -c` 编译。未执行测试、安装或卸载。

## 安装进程中断保护（2026-11-18）

`machine_checkpoint_windows.go` 在移动第一个受管文件前，用受保护的独占句柄创建并同步检查点。记录包含暂存和备份目录名、旧清单、参与备份的文件名和新版摘要。成功提交或明确回滚后才移除。机器范围安装和卸载遇到已有检查点会拒绝写入，包括检查点损坏或无法读取的情况。回滚不完整时保留检查点、暂存和备份。[Windows 文件刷新](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-flushfilebuffers)

在独立 Windows 测试进程中，分别在移走第一个旧文件和放入第一个新文件后提前退出。重开后检查点、两版文件证据和额外文件都保留，重试安装和卸载被拦截。另测成功后检查点移除以及部分写入的 JSON。定向重复三轮 2.127 秒。证据见 `build-machine-checkpoint-repeat.log` 和 `build-machine-checkpoint-go.log`。

本轮覆盖进程退出，实际断电的文件系统元数据顺序未验证。后续恢复需要检查记录、目录和文件内容一致后再允许明确恢复，记录路径不能直接用于写操作。正式机器入口仍待联调。

## 管理员辅助程序验证（2026-11-18）

`machine_helper_windows.go` 新增启动前验证。机器候选路径由 Windows Known Folder 派生，文件名固定。验证要求同时匹配由可信发行策略提供的完整文件 SHA256 和 Windows Authenticode 策略。验签只使用本机缓存，不弹窗。文件与祖先目录在验证和启动交接期间保持打开，拒绝普通替换、写入和改名。启用管道许可前，核对调用方提供的进程句柄所对应的映像路径、会话和管理员令牌。管理员 SID 必须启用且令牌必须已提升。[WinVerifyTrust](https://learn.microsoft.com/en-us/windows/win32/api/wintrust/nf-wintrust-winverifytrust) · [签名策略和缓存配置](https://learn.microsoft.com/en-us/windows/win32/api/wintrust/ns-wintrust-wintrust_data)

`machine_helper_windows_test.go` 覆盖空摘要、摘要不符、普通用户可写 ACL、硬链接、未签名样本、锁定期间写入和替换拒绝、释放后可改名、进程映像和会话不匹配，以及普通进程在进入管道前被拒绝。用本地 x/sys 测试数据中的嵌入签名 PE 验证了真实签名成功路径，该参考文件只读核对，未执行。进程映像测试执行了本轮自建的测试子进程，并显式模拟验签成功。定向三轮全过，耗时 1.821 秒。日志见 `build-machine-helper-tests.log` 和 `build-machine-helper-go.log`。

当前仍为开发验证。首次安装需要确定受信任的启动入口和发行摘要来源。随后才能验证真正的辅助程序启动、DLL 搜索环境、UAC 取消和跨账号查询。映像路径比较依赖启动和交接期间持有文件，覆盖文件替换，进程内映射和 DLL 仍需独立控制。没有新的生产安装入口或手测项目。

## 安装进程交接（2026-11-18）

`machine_handoff_windows.go` 新增本机命名管道交接。双方核对 Windows 返回的对端 PID，并与已知进程的创建时间、会话和用户 SID 比较。每次交接使用随机挑战，双方从各自持有的目标目录、机器范围、版本、操作和载荷摘要生成定长消息，核对后才发出交接许可。管道拒绝远程客户端，消息长度和等待时间有上限。交接回复只确认许可，安装结果由辅助进程退出状态和安装检查另行确认。[对端进程识别](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-getnamedpipeclientprocessid) · [命名管道权限](https://learn.microsoft.com/en-us/windows/win32/ipc/named-pipe-security-and-access-rights)

`machine_handoff_windows_test.go` 运行独立 Windows 子进程。成功用例在已标记的临时根调用上一阶段的受保护安装，复核目标文件后走隔离卸载。版本、载荷、目录、操作、对端进程、超长消息和旧挑战不匹配均拒绝，目标目录保持缺失。另测无对端超时。证据见 `build-machine-handoff-tests.log`、`build-machine-handoff-repeat.log` 和 `build-machine-handoff-go.log`。

测试使用同一 Windows 用户和合成载荷。真实提权、辅助 EXE 可信来源、跨用户令牌查询、取消 UAC 和正式入口仍待完成。后续启动器须单独建立对辅助程序和令牌的信任，再接入交接协议。生产安装器没有本轮新增入口。

## 文件句柄提交与回滚（2026-11-18）

`machine_commit_windows.go` 将机器安装隔离试验接到 Windows 文件句柄操作。安装器先独占打开原文件和暂存文件，用各自的句柄核对身份、权限和内容；安装标识按清单结构比较。备份、提交、回滚与清理使用这些句柄。文件替换使用禁止覆盖的重命名，遇到意外同名文件时保留它，并尝试恢复原文件。暂存清理也核对对象身份后才按句柄标记删除。

验证机对非空 `RootDirectory` 参数返回错误 87，因此目标路径取自已持有的目录句柄，并以绝对路径传给 Win32。源始终通过同一文件句柄操作。这个实现适用于已验证的隔离环境，目标仍由 Windows 解析路径，整批操作也需要逐个移动文件。正式管理员交接、真实普通用户读执行权限、机器卸载和异常断电恢复仍待验证。[FILE_RENAME_INFO](https://learn.microsoft.com/en-us/windows/win32/api/winbase/ns-winbase-file_rename_info) · [SetFileInformationByHandle](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-setfileinformationbyhandle)

定向测试覆盖原文件在另一个句柄中的写入和改名拒绝，提交时出现同名文件后的回滚与冲突文件保留，以及首次安装、升级和修复。测试日志为 `build-machine-handle-tests.log`。全部 Go 测试、静态检查及当前用户安装器默认和自选目录流程见 `build-machine-handle-go.log`、`build-machine-handle-default.log` 和 `build-machine-handle-custom.log`。

## 暂存与回滚验证（2026-11-18）

新增 `internal/installation/machine_stage_windows.go` 和对应测试，修改 `internal/installation/install.go`，以现有安装流程驱动隔离试验。试验绑定已标记临时根。程序根、暂存和备份目录创建时写入受保护的 DACL，文件使用独占新建句柄写入，同一柄回读 SHA256。父目录、程序根、暂存和备份目录持有禁止共享删除的句柄。创建使用 Windows 提供的安全属性。[CreateDirectoryW](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createdirectoryw) · [CreateFileW](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew)

提交前重新核对原目录与受管对象的身份和权限，以及暂存文件的身份、权限、摘要和备份目标是否为空。移动拒绝覆盖同名对象，并重新核对移动后的文件。中途失败时恢复可核对的备份，无法完整恢复时保留备份。试验所用描述符允许普通用户读取执行，真实普通用户令牌的验证仍待完成。对象移动仍经路径调用，这次验证覆盖目录句柄与复核流程，持续持有所有文件句柄直至移动仍待实现。卸载通过现有隔离流程验证。

Windows 定向测试通过（2.195 秒），覆盖首次安装、根目录改名拒绝、暂存篡改拒绝、替换中途失败回滚、升级、缺文件修复、卸载与额外文件保留。全部 Go 测试及 vet 通过，安装包测试 6.481 秒。以当前 0.99.0 载荷编译独立检查安装器，现有 `check-installer.ps1` 的默认目录和中文空格自选目录流程均通过，涵盖模拟升级、占用拦截、修复、卸载器交接与重装。证据见 `build-machine-stage-go.log`、`build-machine-stage-default.log` 和 `build-machine-stage-custom.log`。正式试用包仍为 0.99.0，本阶段没有新的手测项目。

核验日期：2026-11-18。正式交付仍0.99.0。本阶段是未发布的内部基础，没有新安装入口或用户手测事项。

新增 `internal/installation/machine_security_windows.go` 和对应测试文件。生产调用入口尚未接入，所有函数均为包内私有；未提权、未写HKLM、未改Program Files或生产安装，没有重打0.99.0产物。

## 实现范围

- 未来机器程序候选目录只由 `KnownFolderPath(FOLDERID_ProgramFilesX64)` 派生固定 `MultiPan` 子目录，不能从环境变量、账号输入或自选路径推导。限定规范本机固定盘，拒绝其他目录、网络/设备路径、链接和目录联接。
- 生产信任的所有者/修改授权身份仅SYSTEM、Administrators和本机解析的TrustedInstaller服务身份；解析失败即拒绝，不把当前登录用户或仅有管理员用户名当作机器目录归属。
- 使用同一个非跟随重解析点的Windows句柄读取对象属性、文件ID、所有者和DACL。根必须受保护DACL；缺失或NULL DACL拒绝，未知ACE类型/权限位/标志拒绝。空但非NULL的DACL与NULL不同，允许前者作为“未授予普通身份修改”的结果，**不证明程序对普通用户可运行**。
- 非信任身份获得任一写数据/追加/创建、删除、删除子项、改属性/EA、改DACL/所有者或通用写/全部权限都拒绝。检查祖先直到卷根的结构替换权限，最终父目录另检查内容创建权。策略保守：deny不抵消不安全allow，inherit-only写授权也不忽略；可能拒绝合法但复杂的企业ACL，不尝试自动改权限或声称等价于完整AccessCheck。
- 快照包含卷号/文件ID、文件大小/写入时间和权限摘要。重新打开同一批固定对象，发现身份、权限、类型、文件存在性或硬链接数变化即拒绝。目录时间不入快照，避免合法创建暂存子项使父目录时间变化造成误判。受管文件缺失可记录，但后续突然出现会使原快照失效。
- 只看固定程序根、祖先和既有受管文件名，不枚举其他用户数据、未知文件内容或凭据，不向UI/日志输出所有者SID或完整SDDL。

这仍是**只读检查及前后对比**，没有持续持有目录树或文件句柄，不解决最后一次检查到真实写入之间的竞态；内容版本仍须原安装清单SHA校验，文件元数据不能代替字节证明。普通用户运行可用性、权限实际生效以及真实Program Files环境仍需后续验证。

## 隔离测试

`machine_security_windows_test.go` 包含4个测试入口：

1. 15种安全描述符：可信所有者、普通读执行、非NULL空ACL、不可信/缺所有者、缺/NULL ACL、非保护根、普通全权、deny+allow、inherit-only、object ACE、未知权限位；另外逐项覆盖10种修改权，特别包括DELETE_CHILD、WRITE_DAC和WRITE_OWNER。
2. 12种真实文件/权限快照场景：不变、根/父/文件ACL变化、仍满足策略但SDDL改变、大小改变、同字节新文件ID、删除、硬链接、类型替换、原缺失后新出现、根目录替换。
3. 缺失候选根可记录，随后目录联接拒绝，联接外哨兵不变。
4. 固定候选/祖先边界、不可信实际所有者拒绝、Known Folder只读解析。

真实ACL设置仅在本测试新建的临时夹具内，测试私有策略临时信任当前用户，模拟可管理对象；测试边界限制在夹具根，**生产包装函数仍检查到卷根，不信任当前用户**。不转移所有者、不申请管理员权限、不创建其他Windows账号。该夹具不代表真实机器目录/双用户验证。

最初定向4入口通过1.404秒（当时快照10场景）；补充安全ACL变化和大小变化后，`go test ./...` 最终全部通过，installation7.103秒，其余未改包使用有效缓存，`go vet ./...`退出0。日志 `build-machine-security-foundation.log` 和 `build-machine-security-go.log`；vet无诊断。最初本地编译发现x/sys未暴露FILE_DELETE_CHILD常量，按微软定义0x40加入命名常量后通过，未放宽策略。没有前端/运行入口改动，本轮不重复前端和安装包生命周期检查。

0.99.0 EXE/ZIP/setup摘要分别仍为F69EBD44…AE2E734、1637EB02…BAFC3E7、69749BDA…0B9143D；两份原PNG仍为483919B8CD3B6D72153FE747EF24484FF1D27830CE31AB277DFD8308BD5C06E2，备份保持。

## 下一步

先在隔离根实现带显式安全描述符的目录/文件创建与持有句柄的写入流程，验证阶段/备份/回滚对象继承、所有者及硬链接规则，并补普通权限可读执行的验证；不能只在普通用户可写目录运行本快照就标记为机器安全。随后再接受控提权、固定HKLM64与公共入口、原用户隔离和受保护卸载交接。没有真实双用户/多会话验收前，不开放生产机器安装。本版没有新的用户操作要求。

官方依据：[GetSecurityInfo](https://learn.microsoft.com/en-us/windows/win32/api/aclapi/nf-aclapi-getsecurityinfo)（按句柄读取，不能自行消除安全描述符变化竞态）、[新对象的DACL](https://learn.microsoft.com/en-us/windows/win32/secauthz/dacl-for-a-new-object)（继承与受保护DACL）、[文件访问权常量](https://learn.microsoft.com/en-us/windows/win32/fileio/file-access-rights-constants)（各修改位含0x40），以及[访问权和掩码](https://learn.microsoft.com/en-us/windows/win32/secauthz/access-rights-and-access-masks)。保守允许列表和快照组合为项目工程设计，不是微软对该安装器的安全认证。
