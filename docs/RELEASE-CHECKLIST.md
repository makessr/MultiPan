# 发布前检查

本流程适用于正式版和预览版。发布说明以中文写明用户可见变化、验证结果和已知限制。

1. 从专用分支提交发布 PR。每个开发提交同步更新 README；需要升版时同步界面、应用和安装包的版本，以及中文更新记录。较大改动先通过 Issue 对齐范围。
2. 在 PR 中写明本地验证结果，等待所有必需的 CI 检查通过后合并到受保护的 `main`。
3. 等待合并后 `main` 上**确切发布提交**的构建完成。运行以下命令核对该提交及必需的架构任务。多个源码提交构建的附件需要分别核对。

   ```powershell
   $commit = gh api repos/makessr/MultiPan/branches/main --jq .commit.sha
   ./scripts/check-release-readiness.ps1 -Commit $commit
   ```

   Windows 没有 PowerShell 7 时用 `powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/check-release-readiness.ps1 -Commit $commit` 运行，脚本在 5.1 和 7 下结果一致。

   含双架构 APT 验证的提交还须通过「验证双架构 APT 仓库」。

4. 从已核对的构建选择发布文件，并检查版本、系统、架构、SHA-256 和许可证。打包 Linux 版本时，为 amd64 和 arm64 各提供一个 Debian 安装包和对应的 SHA-256 文件。正式在线更新按对应平台要求核对发布签名和更新元数据。macOS 以[未公证预览版](DESKTOP-DISTRIBUTION.md#macos-预览版)分发。准备预发布草稿时，核对 Apple Silicon 和 Intel 的 ZIP 及配套 SHA-256，并在发布说明标注「未公证」。
5. 在 GitHub Releases 创建草稿并核对附件。未签名的预览版标记为预发布，用于手动安装。上传后可用同一脚本加上草稿标签，核对标签、目标 SHA、平台附件和 GitHub SHA-256 摘要。正式标签也检查双架构更新清单及签名文件是否齐全。传入 `-WindowsAssetsDirectory` 可进一步核对 GitHub 摘要和本地正式附件是否一致。产物内容和签名仍按第 4 步核对。确认草稿中的提交和产物都通过检查后再公开。

   示例：`./scripts/check-release-readiness.ps1 -Commit $commit -DraftTag v0.99.0-preview.1`。

6. 发布公开后，等待「发布 APT 仓库」工作流更新 GitHub Pages，再核对 `https://makessr.github.io/MultiPan/dists/stable/InRelease` 的签名和公钥指纹是否为 `97A1 4173 6BE4 B311 C722  2B2F D0C7 755B 6F08 A8C2`。需要重发或补发时在「Actions → 发布 APT 仓库」手动指定标签运行。工作流缺少 `APT_SIGNING_KEY` 会直接失败，不要跳过签名。
