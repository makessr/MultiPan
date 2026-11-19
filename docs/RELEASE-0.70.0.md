# MultiPan 0.70.0：123回收站还原完整流程

2026-11-17 Windows x64。实现、完整构建/自动检查、打包及隔离UI演示通过，真实账号待用户验收。旧版产物与未提交工作保留；不代表0.69.0等旧功能已获用户验收。

## 改动
123回收站从只读升级为文件/文件夹单项及本页最多50项逐项批量还原，复用统一确认、停止后续、逐项结果和批量原结果核对。百度原流程保留，夸克仍仅浏览，无删除/清空操作。
新增ParentFileId读取与原目录ID展示；缺原目录/类型/文件大小、非法标识及来源不完整时不签发还原句柄，不猜完整路径。DPAPI句柄绑定用途、账号来源、页码及项目快照，123与百度用途分离，兼容旧百度回执。
提交前重读原页核对同ID/名称/类型/大小/原目录ID，读取原目录检查冲突；写前保存回执和提交检查点。最多一次POST /a/api/file/trash，operation=false，fileTrashInfoList严格为单元素FileId列表，数值ID不经浮点转换。独立/a/api签名路径，仅此接口使用，其他123接口保持/b/api。
HTTP成功或回收站消失不作为成功：只在原目录ID下核对同ID/名称/类型及文件大小后确认。缺ACK/异步未完成/类型或父目录不符均保留未知，重启只读检查，不重发。原目录核对限制50页/5000项，重复ID/错误父目录/缺字段拒绝确认。

## 限制
123显示原目录ID，不显示猜测的完整路径。读取失败/同名占用不提交；合法空列表本身不能独立证明父目录仍存在。原目录已删除时服务端还原行为仍需验收；如果回退到其他目录，本程序不会标记原位置已确认，也不会自动搬回。目录仅确认自身，内部内容需另查。写前检查和服务端操作仍存在官网并发窗口。队列首个非confirmed或异常停止，未开始部分不跨重启自动续跑。

## 协议证据
- 作者实现：https://github.com/123panNextGen/123pan/blob/fluent-dev/src/app/api/session_file.py 的trash_file/restore_file，明确/a/api路径、仅FileId列表和operation=false；作者说明完整dict会返回成功但静默忽略。
- 作者抓包文档：https://github.com/crmmc/123pan-open/blob/main/doc/api_web.md 的列表字段ParentFileId/Type/Size及回收站接口。不是平台稳定性承诺。

## 检查
scripts/build.ps1退出0：Go全部测试/vet、Vue TS/Vite生产构建、辅助/实际Vue检查、五家正式EXE登录启动检查通过。新TLS合成14场景：成功/目录/缺ACK/pending/错ID/错大小/错父目录/错类型/缺大小/原记录变更/冲突/父目录不可读/初始回执失败/提交检查点失败；覆盖精确ID、单POST、去重、重启回执、未知不可清理、仅检查、来源变化和清理失败回滚。另测根目录ID=0、缺原位置/缺大小/类型未知和跨provider句柄拒绝；实际Vue增加123批量成功覆盖。
隔离IAB演示：123混选、原目录ID确认/取消回焦、未知后后项不提交、核对转确认。releases/ui-0.70.0/123-restore-confirm.png和123-restore-result.png均合成数据，未操作真实账号或文件，预览已停。
package-trial.ps1：ZIP仅EXE/说明/SHA256三公开文件且校验通过。日志build-0.70.0-verification.log。

## 修改文件
internal/transfer/recycle.go、recycle_restore.go、pan123.go；新增recycle_restore_123.go、recycle_restore_123_test.go。frontend/src/RecycleBin.vue、tools/check-cloud-drag.mjs、tools/ui-preview/api.ts。版本/说明：frontend/src/version.ts、scripts/build.ps1、build/config.yml、build/windows/info.json、README.md、TRIAL-GUIDE.txt、docs/DEVELOPMENT-PLAN.md及本说明。构建生成bindings/dist/平台图标及新EXE/ZIP/日志。用户选定透明图标继续使用。

## 复测
1. 退出旧版运行0.70.0，在123官网准备可丢弃文件和文件夹放入回收站；打开回收站选择123，核对原目录ID和名称。缺原信息时按钮应禁用。
2. 选择两项，确认框先取消，官网应未变化；再确认后逐项还原，核对官网原位置和文件内容，目录内部另查。
3. 处理中停止后续，当前项核对后未开始项不操作。待确认只检查原结果，重启后仍保留回执；不要清理未知记录后重复还原。
4. 同名冲突/目录不可读应未提交且停止后续。若原目录已删除，请先用官网处理，勿在正式文件上试验服务端回退行为。
5. 百度原批量还原回归，夸克仍无还原按钮。反馈网盘、版本、步骤和脱敏错误文字，不提供凭据或state.enc。

下一步继续夸克还原/逐盘删除的协议核对与完整流程；删除/回收站阶段尚未完成。
产物：bin/multipan-0.70.0.exe，16114176字节，SHA256 15F96513DCBDF0FA0FC3D5BBF693360F0FE45B78A180F547627606FA3F3EFCEE；releases/MultiPan-0.70.0-windows-x64.zip，7416066字节，SHA256 B5B8F8F6FCE306A323D6148591908E9C88B9D8DBDF806800FF8EB4D6B33DCDB9。
