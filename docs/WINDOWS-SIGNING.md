# Windows 代码签名

MultiPan 的 Windows 产物可以带 Authenticode 签名与 RFC 3161 时间戳。签名是可选的开关：仓库没有配置证书时，CI 仍会构建并发布未签名产物，`scripts/build.ps1` 也不会因为缺少证书而失败。

1.0.0 正式版按项目决定不采购证书，因此该版本的安装包与可执行文件均未签名，系统可能提示「未知发布者」，客户端也会拒绝安装未签名的更新包（自动更新因此暂不提供）。本节的链路保留给日后启用签名时使用。

## 需要什么证书

- 一张代码签名证书（OV 或 EV），需要能以 PFX 形式导出私钥；EV 证书通常绑定硬件令牌，若只能通过云签名服务使用，需要另接对应的签名工具。
- 证书必须由受 Windows 信任的根 CA 签发：`signtool verify /pa` 对自签名证书一定失败，自签名只能用于本地流程自检。

## 在 CI 中启用

1. 把 PFX 转成 base64 单行文本：

   ```powershell
   [Convert]::ToBase64String([IO.File]::ReadAllBytes('C:\path\codesign.pfx'))
   ```

2. 在仓库设置里添加两个 Actions secret：
   - `MULTIPAN_SIGN_PFX_BASE64`：上一步的 base64 文本
   - `MULTIPAN_SIGN_PFX_PASSWORD`：PFX 口令（PFX 没有口令时留空）

3. 之后每次 Windows 构建会在安装包验证之后自动签名 `bin/multipan.exe`、`bin/MultiPan-setup.exe`、`bin/MultiPan-all-users.msi`，并按签名后的字节重写各自的 `.sha256`。签名和校验失败会直接让任务失败，不会产出「看起来成功」的未签名包。

未配置 `MULTIPAN_SIGN_PFX_BASE64` 时，任务会在步骤摘要里注明本次产物未签名。

## 本地签名与核对

```powershell
./scripts/sign-windows-bundle.ps1 -Path bin/multipan.exe, bin/MultiPan-setup.exe, bin/MultiPan-all-users.msi `
  -PfxPath C:\keys\codesign.pfx -PfxPassword '<口令>'
```

也可以不导出 PFX，改用证书存储里的指纹：`-CertificateThumbprint <40 位指纹>`。

核对签名：

```powershell
Get-AuthenticodeSignature -LiteralPath bin\MultiPan-setup.exe | Format-List Status, StatusMessage, SignatureType, SignerCertificate, TimeStamperCertificate
signtool verify /pa /all /tw bin\MultiPan-setup.exe
```

期望 `Status` 为 `Valid`、`SignatureType` 为 `Authenticode`、`TimeStamperCertificate` 非空。

签名会改变文件内容，因此**任何** `.sha256` 都必须在签名之后重新计算；脚本已经自动处理，手工签名时要自己重算。

## 流程自检

`scripts/check-signing-pipeline.ps1` 用一次性的自签名证书给临时生成的未签名 EXE 签名，断言签名者、时间戳行为与 `.sha256` 重写结果，不触碰仓库文件。`scripts/build.ps1` 在检测到 `signtool.exe`（PATH 或 Windows SDK）时会自动运行它，因此签名脚本的回归会在日常构建里暴露。

## 签名之后

- 正式发布仍要走 `scripts/prepare-windows-release.ps1`：它要求安装包带可信 Authenticode 签名和时间戳，并据此生成签名的在线更新清单。
- 签名只解决「来源可验证」，不直接消除 SmartScreen 提示；新证书需要积累下载量与时间，EV 证书或云签名服务通常能更快建立信誉。
- 已知限制：`windows-11-arm` 运行器若不包含 Windows SDK，签名步骤会因找不到 `signtool.exe` 失败；届时可用 `-SignToolPath` 指定工具路径，或改用云签名服务。
