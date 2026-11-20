# Linux 与 macOS 桌面版

项目共用同一套桌面界面和传输逻辑。Linux 用 Secret Service，macOS 用系统钥匙串存储本机数据的加密密钥。Windows 的 DPAPI 文件需要保存在原系统。Linux 和 macOS 通过「手动导入登录信息」添加网盘账号，WebDAV 沿用用户名和密码。

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
