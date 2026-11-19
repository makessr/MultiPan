# MultiPan 0.74.1 搜索范围限制

2026-11-17 Windows x64。用户明确要求最多根目录→目录→目录，不递归整个网盘。搜索现在仅读取根目录（depth=0）、一级目录（depth=1）及二级目录（depth=2）。这些目录直接包含的文件可匹配；二级目录列出的第三级目录名称也不纳入匹配，更不会进入第三级读取内容。

界面从全盘搜索改为目录搜索，标明最多两级子目录。按声明范围扫描完成属于正常完成，不把预期的更深目录排除报告为失败；超时、数量上限、读取错误等原部分结果提示保留。单账号和全部账号均遵守这个范围。其他搜索取消、路径匹配、分页和打开所在目录保持。

修改internal/transfer/search.go（常量cloudSearchMaxDepth=2、结果与递归边界、范围完成提示）、search_test.go（原深度测试调整和边界访问测试）、frontend/src/GlobalSearch.vue、Workspace.vue及版本/说明frontend/src/version.ts、scripts/build.ps1、build/config.yml、build/windows/info.json、README.md、TRIAL-GUIDE.txt、docs/DEVELOPMENT-PLAN.md。旧产物保留，不覆盖未提交工作。

新增TestCloudSearchStopsAtTwoSubdirectoryLevels覆盖路径型WebDAV和ID型夸克：根/一级/二级文件均匹配，恰好访问三个目录，第三级目录及名称不搜索。其他八类provider搜索和旧复制/移动回归按完整构建检查。最终结果见build-0.74.1-verification.log及文末摘要。

用户复测：用可丢弃目录构造根目录/A/B/C，分别在根、A、B、C放不同名称文件。根/A/B内文件应可搜索，C及其中的文件不应匹配；打开结果应到正确父目录。选择全部账号时也只扫描各账号这三层目录范围。真实账号验收仍待用户确认。
最终验证：完整scripts/build.ps1退出0，Go全测试（transfer51.179秒）/vet、TS/Vite、全部辅助及实际Vue回归、五家正式EXE登录启动通过；package-trial.ps1三公开文件及摘要校验通过。EXE bin/multipan-0.74.1.exe 16242688字节 SHA256 CC10227CAF1193639003A28A877076DADD1D1065A3E529F631174C20A72A0977；ZIP releases/MultiPan-0.74.1-windows-x64.zip 7462192字节 SHA256 12C630CEB105CA45F83864E316E95173172CDB1087A016049C6988F1235493E0。旧0.74.0 EXE校验未变。
