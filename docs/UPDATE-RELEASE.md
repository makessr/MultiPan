# 在线更新发布流程

MultiPan 参考 [ZhanDu 发布流水线](https://github.com/makessr/ZhanDu/blob/main/docs/release-pipeline.md)，先准备和验签，再在 GitHub 创建草稿，核对后公开。普通 Actions 构建继续提供预览产物。正式更新使用 MultiPan 自己的签名格式和公钥。

## 当前 Windows 分发方式

Windows x64 和 ARM64 采用无证书发布，提供独立安装包及 SHA-256，在 GitHub Releases 手动下载和升级。1.0.0 起按项目决定不采购代码签名证书：客户端会拒绝安装未带 Authenticode 签名的更新包，所以本版仍不提供客户端自动更新，升级请手动下载覆盖安装。按[发布前检查](RELEASE-CHECKLIST.md)核对构建、草稿附件和架构。

以下为留存的正式在线更新流程。恢复该渠道时，需要满足已有客户端的 Authenticode、时间戳和更新清单验签要求，即先取得 CA 签发的代码签名证书并按上一节签名。

## 更新渠道

客户端通过 [MultiPan 最新发布](https://github.com/makessr/MultiPan/releases/latest) 确认一次 `vX.Y.Z` 标签，随后按客户端架构从同一个标签读取对应的清单、签名和安装包。

| 架构 | 清单 | 分离签名 | 安装包 |
| --- | --- | --- | --- |
| Windows x64 | `update.json` | `update.sig` | `MultiPan-X.Y.Z-windows-x64-setup.exe` |
| Windows ARM64 | `update-windows-arm64.json` | `update-windows-arm64.sig` | `MultiPan-X.Y.Z-windows-arm64-setup.exe` |

客户端核对标签与清单版本和架构，拒绝降级，并在下载后核对长度、SHA-256、PE 架构和 Windows 信任链。首次公开前，客户端会提示渠道尚无可用版本。此前未配置更新源的版本需要手动安装一次内置新渠道和公钥的版本。

能连接 GitHub 时，客户端从官方渠道确定最新标签。获取清单和签名时依次尝试 gh-proxy.com、ghproxy.net 和 GitHub 直连。若无法直连解析最新标签，则以镜像提供且通过 Ed25519 验证的清单版本确定安装包标签。下载安装包时优先使用上次通过清单核对的镜像，其余镜像继续候补，GitHub 直连兜底。所有线路均须通过安装包摘要和 Windows 签名核对。镜像只接收公开的 Release 下载地址。

## 与 ZhanDu 的签名对照

ZhanDu 的 Windows 发布工作流用 Tauri 私钥生成更新签名。已从 v0.7.0 GitHub Release 下载并用 Windows 核对 x64 EXE、ARM64 EXE 和 x64 MSI，三件均返回 Authenticode 状态 `NotSigned`。ZhanDu 的更新签名可作为流程参考。MultiPan 已配置用于更新清单的 Ed25519 公钥，正式 Windows 安装包仍需通过当前发行规则要求的 Authenticode 和时间戳验证。本次在当前 Windows 代码签名证书存储及 ZhanDu 发布流程中未找到可直接用于 MultiPan 的 Authenticode 签名来源。 [ZhanDu 发布工作流](https://github.com/makessr/ZhanDu/blob/main/.github/workflows/release.yml) · [ZhanDu v0.7.0](https://github.com/makessr/ZhanDu/releases/tag/v0.7.0)

Linux APT 的正式仓库地址与仓库签名公钥独立配置。见[桌面版发布说明](DESKTOP-DISTRIBUTION.md)。

## 准备正式包

1. 在中文发布 PR 中同步更新 `frontend/src/version.ts`、`build/windows/info.json`、`build/config.yml` 和 `CHANGELOG.md`。合并后用项目构建脚本生成并验证 EXE，再用安装器脚本打包。
2. 使用 `scripts/sign-release-executable.ps1` 对最终安装包做 Authenticode 签名和时间戳。按上表命名两个架构的安装包。
3. 用双架构准备脚本生成发行附件。传入两个已完成 Authenticode 签名和时间戳的安装包、仓库外的 Ed25519 私钥、一个较低的已安装版本，以及尚不存在的输出目录。脚本使用客户端预置公钥分别验证首次安装发行源和较低版本升级。双架构均通过后生成两个安装包、两个 SHA-256、两个更新清单和两份分离签名。

```powershell
./scripts/prepare-windows-release.ps1 -X64Installer $signedX64 -Arm64Installer $signedArm64 `
  -PrivateKey $privateKey -PreviousVersion $previous -OutputDirectory $output
```

可使用 Go 验证器独立核对从草稿取回的文件。以下为 ARM64 示例，x64 改用 `-arch amd64`、`update.json` 和 `update.sig`。

```powershell
$channel = Get-Content build/update-channel.json -Raw | ConvertFrom-Json
go run ./tools/verifyupdate -installed-version $previous `
  -arch arm64 -publisher-key $channel.publisherKey -installer $installer `
  -manifest (Join-Path $metadata 'update-windows-arm64.json') `
  -signature (Join-Path $metadata 'update-windows-arm64.sig') -require-authenticode
```

没有已安装版本时，用 `-first-install-version 0.99.0`（替换为核对的正式版本）代替 `-installed-version`。此模式只核对本机附件，要求精确匹配指定发行版本，并始终验证 Windows Authenticode。可信 Ed25519 公钥取自已核对的源码或可信发行记录。

发布私钥存放在仓库外，客户端及 GitHub Release 只需要公钥、分离签名和产物。已有信任公钥的客户端更换公钥时，需要由旧密钥签署的过渡版本。

## 创建草稿与公开

1. 为已验证的发布提交创建并推送 `vX.Y.Z` 标签。在 GitHub Releases 创建草稿，使用中文版本说明，并将两个架构的八件产物上传到同一个标签。
2. 对正式草稿运行 `./scripts/check-release-readiness.ps1 -Commit $commit -DraftTag $tag -WindowsAssetsDirectory $output`，逐件核对 GitHub 返回的 SHA-256 与本机验签产物。也可从草稿下载附件并单独核对。确认两个架构的版本、清单、签名和安装包后，将草稿公开为最新正式版本。
3. 用下方命令验证线上清单。在已安装的较低版本中检查更新，再验证下载和手动启动安装器。

```powershell
go run ./tools/verifyupdate -release-url $channel.releaseURL `
  -arch arm64 -publisher-key $channel.publisherKey -installed-version $previous
```

清单默认有效七天，最长三十天。若同一版本需要继续作为最新更新，须在过期前用同一安装包和发布密钥重新生成并核对清单与签名，再更新这两件资产。采用不可变 Release 时，通过新的正式版本续接。
