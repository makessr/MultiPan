# MultiPan

<img src="assets/branding/multipan-icon.png" alt="MultiPan 图标" width="88">

Windows 多网盘桌面客户端。选择一次本地文件或文件夹，即可上传到多个网盘。文件在本机与网盘间直接传输，无需自建服务器。

[![Windows 构建](https://github.com/makessr/MultiPan/actions/workflows/windows.yml/badge.svg)](https://github.com/makessr/MultiPan/actions/workflows/windows.yml)

## 核心能力

| 功能 | 可以做什么 |
| --- | --- |
| 多网盘上传 | 为各账号选择目标目录。选定一个目录后可查找其他网盘中的相近目录，并在上传前核对同名冲突。 |
| 文件管理 | 按账号浏览目录、搜索、下载和移动文件。复制与分享能力随网盘而异。 |
| 传输队列 | 管理上传和下载任务，设置并发与合计限速。在来源和会话满足条件时可断点续传。 |
| 跨网盘中转 | 经本机缓存复制文件或目录。支持的文件可在核对目标内容后搬运。 |
| 分享管理 | 在支持的网盘创建、复制、管理已有分享，或在本机查看操作回执。 |

已接入蓝奏云、夸克网盘、123 云盘、百度网盘、光鸭云盘、诚通 WebDAV 和通用 WebDAV。

## 下载与安装

支持 Windows 10/11 x64，需要 Microsoft Edge WebView2 Runtime。

1. 打开 [Windows 构建](https://github.com/makessr/MultiPan/actions/workflows/windows.yml)，选择 `main` 分支最近一次通过的构建。
2. 登录 GitHub 后，在该次运行的产物区下载 `MultiPan-Windows-x64` 并解压。
3. 运行 `multipan.exe` 直接试用，或用 `MultiPan-setup.exe` 安装到当前用户。压缩包内附 SHA-256 摘要和[试用指南](TRIAL-GUIDE.txt)。

Actions 产物为未签名的预览构建。首次使用时，在左侧「我的网盘」添加账号，然后打开「多网盘上传」选择本地文件和目标目录。

## 开发计划

| 优先级 | 方向 | 接下来要做的事 |
| --- | --- | --- |
| 当前优先 | 分享管理 | 核对夸克、123 和百度仍待接入的已有分享设置，以及蓝奏已发布分享列表和独立撤销所需的接口依据。 |
| 暂缓 | 删除与回收站 | 独立回收站页面暂缓。文件管理保留可恢复的删除。 |
| 待推进 | 盘内文件操作 | 补夸克原生复制、蓝奏文件夹移动，以及光鸭单文件直复制到已有目录。 |
| 待推进 | 跨网盘复制与搬运 | 扩大夸克和光鸭来源范围，逐步接入更多来源的单文件搬运和目录搬运。 |
| 待推进 | 登录自动续期 | 核对各网盘的续期依据。123 已有实验性续期和只读恢复，待真实账号验收。 |
| 待推进 | 诚通公开分享 | 核对 WebDAV 账号与公开分享所需独立登录会话之间的授权方式。 |
| 待推进 | 正式发布 | 配置可信更新源、发布公钥和 Windows 签名，完善系统级安装。 |

完整范围、进展和验收记录见[开发计划](docs/DEVELOPMENT-PLAN.md)。按账号进行的真实网盘操作仍需手动验收。

## 从源码构建

技术栈为 Go、Wails 3、Vue 和 TypeScript。需要 Windows x64、WebView2、Go 1.26 或更新版本，以及 Node.js 26。

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
./scripts/build.ps1 -AppName multipan
./scripts/package-installer.ps1 -ApplicationPath bin/multipan.exe -OutputPath bin/MultiPan-setup.exe
```

请确认 `wails3` 所在的 Go 工具目录已加入 `PATH`。构建脚本生成图标和 Wails 绑定，运行 Go 测试、Go vet 和前端专项检查。本地默认额外检查五家网盘的登录窗口能否打开。CI 使用同一构建脚本，跳过需要交互桌面的登录窗口检查，并打包、核对安装器及上传产物。

## 数据与贡献

- 账号和任务状态保存在本机，使用 Windows DPAPI 加密。传输缓存和操作回执也由本机管理。
- GitHub PR 标题、正文和版本更新记录以中文为主。PR 请写明变更、验证结果和仍需手动验收的场景。
- 查看[更新记录](CHANGELOG.md)、[第三方许可说明](THIRD_PARTY_NOTICES.md)和[历史记录](docs/HISTORY-0.46-0.62.md)。
