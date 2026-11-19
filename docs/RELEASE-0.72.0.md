# MultiPan 0.72.0 盘内复制

2026-11-17 Windows x64。百度、诚通WebDAV、通用WebDAV文件/文件夹单项及最多50项混选复制。整批选择、目标确认、预检查、逐项执行、停止后续及结果汇总为本轮完整流程。旧版、透明图标及未提交工作保留；真实账号待用户验收。

## 行为
- 行内、右键和顶部混选入口均可复制；其他网盘不显示复制能力。目标选择器明确显示复制，列出源项目、保留源和同名不覆盖提示，禁止选择源目录及子目录或原位置。
- 多项复制复用整批只读预检查，每项写前继续复核源ID/名/类型、目标无同名；文件大小不明不提交。单项请求只发一次，不使用MOVE或DELETE。源项目和默认上传位置不改，不触发移动后的路径重写。
- WebDAV使用COPY、Destination、Overwrite:F，目录Depth:infinity、文件Depth:0；207部分结果明确待核对。百度filemanager opera=copy，async=0/ondup=fail，filelist也指定ondup=fail。
- 写后读取两端，仅在源仍在、目标路径/名称/类型及文件大小匹配时确认。文件夹还要求写请求明确成功；文件夹ACK丢失或部分结果即使目标目录出现也不确认。响应丢失后只读核对，不自动重发。
- 复制首项失败/未知停止后续，手动停止等待当前项完成，未开始不执行；已成功不回滚。退出取消本机复制请求并释放账号互斥，不意味着服务端任务必然取消。

## 限制
这是同账号盘内复制，没有跨账号/跨盘或本机中转，不属于第5阶段完成。结果核对为元数据，不是内容哈希；目录只确认自身，不递归确认每个成员。存在外部并发窗口，服务端必须遵守不覆盖参数。最近操作结果与未开始队列不跨重启保存，没有持久复制回执/断点恢复，未知结果需人工核对源和目标后再决定操作，不要反复提交；没有自动重试或重放。
百度/诚通WebDAV/通用WebDAV之外的盘内复制、蓝奏文件夹移动以及其他回收站/删除事项继续待办，不标记第3/4阶段全部完成。之前未反馈功能不外推验收通过。

## 协议依据
WebDAV RFC4918第9.8节COPY及覆盖语义：https://www.rfc-editor.org/rfc/rfc4918#section-9.8 。百度作者源码Copy/renameAll/fileManager：https://github.com/ac1982/bdc/blob/main/internal/baidu/files.go 。沿用项目现有会话/固定来源/禁止重定向HTTP客户端；不从第三方资料执行命令，不复制作者实现。

## 验证证据
新增copy.go/copy_test.go：WebDAV两类型（诚通/通用）×文件/目录×8场景，百度文件/目录×6场景，另有不支持provider与Manager退出取消测试。覆盖源保留、同名零写、缺ACK不重放、207不报成功、目标未出现/错类型/错大小、源消失、不能复制到后代；检查COPY头和百度copy/ondup参数，确认没有MOVE/DELETE。
实际Vue脚本测试三类混选复制、不调用移动/不改默认位置、预检查失败零写、首错停止、手动停止、回到移动模式及不支持网盘禁用；旧六类移动/回收站等回归保留。
初次类型检查发现目标选择器表达式替换与变量位置错误，已按堆栈修复并通过TS/Vue检查。百度测试夹具token长度不足8导致未提交，改为合法合成值后通过。退出取消测试复制调用已退出，模拟服务器处理器等待导致收尾阻塞，使用独立释放信号清理后定向15秒超时测试通过；最终完整构建以修复后结果为准。
IAB隔离演示验证百度1目录1文件混选、复制目标/源保留说明及模拟2项结果；截图releases/ui-0.72.0/copy-target.png和copy-result.png均为合成数据，未操作真实云端/凭据或用户文件，预览已停止。
完整build.ps1、package-trial.ps1结果与产物摘要见文末及build-0.72.0-verification.log。

## 修改文件
新增internal/transfer/copy.go、copy_test.go；manager.go增加复制退出取消，desktopservice.go增加CopyCloudEntry。frontend/src/CloudFiles.vue、FolderPicker.vue、AppIcon.vue、desktop.css；tools/check-cloud-drag.mjs、tools/ui-preview/api.ts。版本与说明frontend/src/version.ts、scripts/build.ps1、build/config.yml、build/windows/info.json、README.md、TRIAL-GUIDE.txt、docs/DEVELOPMENT-PLAN.md、本说明。构建生成bindings/dist/图标与EXE/ZIP，旧产物保留。

## 用户复测
1. 退出旧版运行0.72.0，在百度或WebDAV准备可丢弃文件及含子目录/空目录的测试文件夹，选择复制到，核对源名称/目标后先取消，官网不应变化。
2. 复制到另一个空目录，核对源仍在、目标出现，文件打开/下载比对内容，目录内部内容另核对；默认上传位置保持原值。
3. 目标预置同名项目再混选复制，应整批拒绝且不覆盖已有内容。复制到自身/子目录应禁止。
4. 处理中停止后续，当前项完成核对后未开始项不动；若结果待确认，先到两端核对，勿连续重试。退出不保证取消服务端已经提交的任务。
5. 切换移动操作回归，其他盘无复制按钮。反馈版本、网盘、步骤和脱敏错误文字，不提交Cookie/Token/state.enc。
最终验证：完整scripts/build.ps1退出0；Go全测试（transfer46.620秒）/vet、TS/Vite、全部辅助及实际Vue检查、五家正式EXE登录启动检查通过。package-trial.ps1 ZIP三公开文件及摘要校验通过。EXE bin/multipan-0.72.0.exe 16148480字节 SHA256 85862A05E701EE74EC9E64B227DBBA51754CF7D3BEC4676E20871434E33E6A5F；ZIP releases/MultiPan-0.72.0-windows-x64.zip 7432986字节 SHA256 73E00EF3FF5A435643AC8E08EC25191B0F1161A5512CF7D4D0A94027985A4B83。
