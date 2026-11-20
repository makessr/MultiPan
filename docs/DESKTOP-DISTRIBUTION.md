# 桌面版分发

项目共用同一套桌面界面和传输逻辑。Linux 用 Secret Service，macOS 用系统钥匙串存储本机数据的加密密钥。Windows 的 DPAPI 文件需要保存在原系统。Linux 和 macOS 通过「手动导入登录信息」添加网盘账号，WebDAV 沿用用户名和密码。

## Windows 全用户 MSI

在 Windows 构建的同次产物中选择 x64 或 ARM64，核对 `MultiPan-all-users.msi.sha256`，双击 MSI 并确认 Windows UAC。安装向导默认安装程序和公共开始菜单中的「MultiPan（所有用户）」。在功能选择页可启用「公共桌面快捷方式」。程序放在系统的 64 位 Program Files 下的 MultiPan 目录。首次安装发现已有同名目录或快捷方式时会停止，便于先确认其归属。

Windows Installer 负责系统范围登记、同版本修复、版本升级和卸载。升级前退出所有用户会话中的 MultiPan，再运行新版本的同架构 MSI。卸载入口为「设置 → 应用 → 已安装的应用 → MultiPan（所有用户）→ 卸载」。各 Windows 用户账号的网盘配置和传输记录仍保存在各自的配置目录中。无证书预览通过人工下载和升级。

手动构建时使用 WiX 5.0.2 及同版本的 `WixToolset.UI.wixext`，先构建所需架构的客户端，再运行以下命令。构建自动读取客户端 PE 版本及架构，生成 MSI 和 SHA-256。检查脚本读取 MSI 数据库，`-Extract` 在可运行该架构 Windows Installer 的机器上验证冲突检测和内嵌文件。

```powershell
$env:DOTNET_ROLL_FORWARD = 'Major'
dotnet tool install --global wix --version 5.0.2
wix extension add --global WixToolset.UI.wixext/5.0.2
./scripts/package-installer.ps1 -Machine -ApplicationPath bin/multipan.exe -OutputPath bin/MultiPan-all-users.msi
./scripts/check-machine-installer.ps1 -Installer bin/MultiPan-all-users.msi -ApplicationPath bin/multipan.exe -Architecture x64 -Extract
```

当前 Windows MSI 的产品登记由 Windows Installer 管理。此前 Go 系统级安装夹具中的 `--inspect-machine-integration` 用来检查该夹具所采用的固定登记布局。 [Windows Installer 安装范围](https://learn.microsoft.com/en-us/windows/win32/msi/installation-context)

### 全用户安装验收

- [ ] **P06 Windows 全用户 MSI（x64、ARM64 各一轮）**。在测试虚拟机核对 `.msi.sha256` 后双击对应 MSI。首次到 UAC 时点「否」，确认「已安装的应用」没有新增条目。重跑并允许提权，在功能页先保留公共桌面默认关闭，完成安装。打开「设置 → 应用 → 已安装的应用」，确认有「MultiPan（所有用户）」。切换到普通 Windows 用户，从开始菜单点「MultiPan（所有用户）」，确认能启动、公共桌面没有快捷方式、账号配置属于当前用户。回到管理员会话，运行同一 MSI 进入维护并启用「公共桌面快捷方式」，再用普通用户核对桌面入口。用后续版本的同架构 MSI 验证升级。最后从「已安装的应用」卸载，确认公共快捷方式和程序文件被移除，两个用户各自的配置记录仍可读取。另在干净虚拟机预置同名安装目录或公共链接，验证首次安装拒绝覆盖。

## Linux

目标环境为 Ubuntu 24.04 或 Debian 13 的 amd64 和 arm64 桌面，依赖 GTK 4、WebKitGTK 6 和桌面钥匙环。桌面会话须提供已解锁的 Secret Service，例如 GNOME Keyring。

```bash
wails3 task linux:create:deb APP_NAME=multipan
sudo apt install "./bin/multipan_0.99.0_$(dpkg --print-architecture).deb"
```

发布方在 Linux 安装 `apt-utils` 和 `gnupg`，从已验证的 Debian 包生成 APT 仓库目录。命令中的 GPG 指纹对应发布方保管的仓库签名密钥。

```bash
bash scripts/prepare-apt-repository.sh \
  bin/multipan_0.99.0_amd64.deb bin/multipan_0.99.0_arm64.deb \
  "$APT_SIGNING_FINGERPRINT" bin/apt-repository
```

将生成的仓库目录完整部署至 HTTPS 静态站点，并通过可信渠道公布公钥及指纹。安装方核对公钥后，把它保存为 `/etc/apt/keyrings/multipan.gpg`。在 `/etc/apt/sources.list.d/multipan.sources` 填写实际发布地址。

```text
Types: deb
URIs: https://YOUR-APT-HOST/multipan/
Suites: stable
Components: main
Architectures: amd64 arm64
Signed-By: /etc/apt/keyrings/multipan.gpg
```

运行 `sudo apt update` 和 `sudo apt install multipan`。后续更新由 APT 管理。仓库地址和正式签名公钥需随首次发布提供。

## macOS

GitHub Actions 在 Apple Silicon 和 Intel 的独立原生 runner 上构建 `multipan.app`，校验应用包并打成 ZIP。本地构建可在 macOS 上运行 `wails3 task darwin:package APP_NAME=multipan`。当前开发构建使用临时签名，面向普通用户的正式分发需用 Developer ID 签名，并完成 Apple 公证与 stapling 后发布。

桌面工作流为 [Linux 与 macOS 构建](../.github/workflows/desktop-unix.yml)。正式包发布前还需实机验收启动、手动添加账号、上传与下载、退出后重新打开及版本升级。
