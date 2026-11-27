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

## Linux

目标环境为 Ubuntu 24.04 或 Debian 13 的 amd64 和 arm64 桌面，依赖 GTK 4、WebKitGTK 6 和桌面钥匙环。桌面会话须提供已解锁的 Secret Service，例如 GNOME Keyring。

```bash
wails3 task linux:create:deb APP_NAME=multipan
sudo apt install "./bin/multipan_0.99.0_$(dpkg --print-architecture).deb"
```

### 发布 APT 源

1. 在 GitHub 打开 MultiPan 的「Actions → Linux 与 macOS 构建」，进入同一个成功运行记录。从「Artifacts」下载 Linux amd64 和 arm64 产物并解压。确认两个 `.deb` 的版本相同，用各自产物中的 SHA-256 文件核对。把两包放到下面命令所用的路径。
2. 在 Ubuntu 或 Debian 的 Bash 中安装工具。首次发布时生成专用 GPG 签名密钥，并从 `gpg` 的输出复制完整指纹。以后发布沿用这把密钥。可将显示名中的邮箱换成你的发布邮箱。

   ```bash
   sudo apt update
   sudo apt install apt-utils gnupg
   gpg --quick-generate-key 'MultiPan APT <release@example.org>' rsa3072 sign 2y
   gpg --list-secret-keys --fingerprint 'MultiPan APT'
   ```

3. 在 MultiPan 源码根目录运行下面的命令。将指纹和两个包的路径替换为实际值。输出目录须不存在。脚本生成 `pool/`、`dists/` 和 `multipan-archive-keyring.gpg`。GPG 私钥保留在签名机器上。

   ```bash
   apt_fingerprint='替换为完整的40位指纹'
   bash scripts/prepare-apt-repository.sh \
     bin/multipan_0.99.0_amd64.deb bin/multipan_0.99.0_arm64.deb \
     "$apt_fingerprint" bin/apt-repository
   ```

4. 选择一个公开 HTTPS 静态目录发布生成的文件。如果已有静态站点，将整个生成目录的**内容**放到网站的 `/multipan/`。如果选择 GitHub Pages，建一个独立的公开仓库，例如 `makessr/multipan-apt`，将生成目录的内容放在该仓库根目录，在根目录创建空文件 `.nojekyll`。推送到 `main`，在仓库中点击「Settings → Pages → Source → Deploy from a branch」，选择「main → /(root) → Save」。部署完成后，用 Pages 显示的地址访问 `dists/stable/InRelease` 和 `multipan-archive-keyring.gpg`。把实际地址和公钥的完整指纹公布在 MultiPan 的发布说明中。 [GitHub Pages 发布源设置](https://docs.github.com/en/pages/getting-started-with-github-pages/configuring-a-publishing-source-for-your-github-pages-site)

使用 GitHub Pages 时留意站点容量上限，特别是保留多个版本的两种架构安装包时。 [GitHub Pages 限制](https://docs.github.com/en/pages/getting-started-with-github-pages/github-pages-limits)

### 从 APT 源安装

用实际发布地址替换下面的示例地址。运行前半段，确认显示的完整指纹和 MultiPan 发布说明公布的指纹一致，再继续安装。以下示例适用于 amd64 或 arm64 的 Linux 桌面。

```bash
apt_url='https://makessr.github.io/multipan-apt'
curl -fsSL "$apt_url/multipan-archive-keyring.gpg" -o multipan-archive-keyring.gpg
gpg --show-keys --fingerprint multipan-archive-keyring.gpg
```

```bash
sudo install -d -m 0755 /etc/apt/keyrings
sudo install -m 0644 multipan-archive-keyring.gpg /etc/apt/keyrings/multipan.gpg
sudo tee /etc/apt/sources.list.d/multipan.sources >/dev/null <<EOF
Types: deb
URIs: $apt_url/
Suites: stable
Components: main
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/multipan.gpg
EOF
sudo apt update
apt-cache policy multipan
sudo apt install multipan
```

以后执行 `sudo apt update` 和 `sudo apt install --only-upgrade multipan` 即可更新。正式仓库地址和公钥发布前，可以继续从已核对的构建中下载单独的 Debian 包。 [Debian APT 源配置](https://manpages.debian.org/trixie/apt/sources.list.5.en.html)

### 发布下一版

保存每次完整生成的目录作为本机快照。发布下一版时复用同一 GPG 密钥，可通过最多两次 `--retain-from` 保留最近两次签名快照的哈希索引与对应安装包。

```bash
bash scripts/prepare-apt-repository.sh \
  --retain-from bin/apt-repository \
  "$amd64_package" "$arm64_package" "$apt_fingerprint" bin/apt-repository-next
```

使用 GitHub Pages 时，把生成结果放在同一个部署提交中。使用逐文件上传的静态站点时，先上传 `pool/`、`by-hash/` 与常规索引，最后切换 `dists/stable/InRelease`。旧缓存失效前保留对应旧索引和安装包。Linux 与 macOS 构建工作流会用临时密钥在隔离环境核对两架构安装包的下载与摘要。 [Debian 仓库格式](https://wiki.debian.org/DebianRepository/Format#indices_acquisition_via_hashsums_.28by-hash.29)

## macOS 预览版

GitHub Actions 分别构建 Apple Silicon 和 Intel 的 `multipan.app`，按架构提供 ZIP 与 SHA-256 文件。预览包使用临时签名，未经 Apple 公证。下载后，Gatekeeper 可能在首次启动时显示警告或阻止打开。 [Apple 支持](https://support.apple.com/en-us/102445)

在 [Linux 与 macOS 构建](../.github/workflows/desktop-unix.yml) 的产物区选择对应架构。本地可在 macOS 上运行 `wails3 task darwin:package APP_NAME=multipan`。桌面实机验收包括启动、手动添加账号、上传下载及重启后重新打开。
