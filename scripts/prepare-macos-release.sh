#!/usr/bin/env bash
set -Eeuo pipefail

if [[ $# -ne 6 ]]; then
  printf '用法：%s 应用.app 版本 amd64|arm64 DeveloperID证书指纹 钥匙串公证配置 新输出目录\n' "$0" >&2
  exit 2
fi

bundle="$1"
version="$2"
arch="$3"
identity="$4"
profile="$5"
output="$6"
if [[ $(uname -s) != Darwin || ! -d "$bundle" || -L "$bundle" || "$bundle" != *.app ||
      ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ || ! "$identity" =~ ^[A-F0-9]{40}$ ||
      -z "$profile" || -z "$output" ]]; then
  printf '需要 macOS、有效的应用包与版本、完整的大写证书指纹和钥匙串配置\n' >&2
  exit 2
fi
case "$arch" in
  amd64) mach_arch=x86_64 ;;
  arm64) mach_arch=arm64 ;;
  *) printf '架构须为 amd64 或 arm64\n' >&2; exit 2 ;;
esac
source_app=$(cd -P -- "$bundle" && pwd)
parent=$(cd -P -- "$(dirname -- "$output")" && pwd)
output="$parent/$(basename -- "$output")"
if [[ -e "$output" || -L "$output" || "$parent/" == "$source_app/"* ]]; then
  printf '输出须为应用包外尚未占用的目录\n' >&2
  exit 2
fi

plist="$source_app/Contents/Info.plist"
if [[ $(plutil -extract CFBundleIdentifier raw -o - "$plist") != app.multipan.desktop ||
      $(plutil -extract CFBundleExecutable raw -o - "$plist") != multipan ||
      $(plutil -extract CFBundleShortVersionString raw -o - "$plist") != "$version" ||
      $(plutil -extract CFBundleVersion raw -o - "$plist") != "$version" ||
      $(lipo -archs "$source_app/Contents/MacOS/multipan") != "$mach_arch" ]]; then
  printf '应用包的标识、版本或独立架构不匹配\n' >&2
  exit 2
fi
identities=$(security find-identity -v -p codesigning)
if ! awk -v expected="$identity" '$2 == expected && $0 ~ /"Developer ID Application: / { found=1 } END { exit !found }' <<< "$identities"; then
  printf '钥匙串中没有匹配的 Developer ID Application 签名身份\n' >&2
  exit 2
fi

scratch=$(mktemp -d "$parent/.multipan-notary.XXXXXXXX")
trap 'rm -rf -- "$scratch"' EXIT
app="$scratch/multipan.app"
package="MultiPan-$version-$arch-macOS.zip"
ditto "$source_app" "$app"
codesign --force --timestamp --options runtime --sign "$identity" "$app"
codesign --verify --strict --deep "$app"

ditto -c -k --sequesterRsrc --keepParent "$app" "$scratch/submission.zip"
notary_args=(--keychain-profile "$profile")
if [[ -n "${MULTIPAN_NOTARY_KEYCHAIN:-}" ]]; then
  notary_args+=(--keychain "$MULTIPAN_NOTARY_KEYCHAIN")
fi
notary_exit=0
xcrun notarytool submit "$scratch/submission.zip" "${notary_args[@]}" --wait --output-format json > "$scratch/submission.json" || notary_exit=$?
submission=$(plutil -extract id raw -o - "$scratch/submission.json" 2>/dev/null) || submission=unknown
status=$(plutil -extract status raw -o - "$scratch/submission.json" 2>/dev/null) || status=unknown
if [[ "$notary_exit" -ne 0 || "$status" != Accepted || ! "$submission" =~ ^[0-9A-Fa-f-]{36}$ ]]; then
  printf 'Apple 公证未通过。状态 %s，提交 ID %s\n' "$status" "$submission" >&2
  exit 1
fi
xcrun stapler staple "$app"
xcrun stapler validate "$app"
codesign --verify --strict --deep "$app"
spctl --assess --type execute "$app"

ditto -c -k --sequesterRsrc --keepParent "$app" "$scratch/$package"
(
  cd -- "$scratch"
  shasum -a 256 "$package" > "$package.sha256"
)
mkdir -- "$output"
mv -- "$scratch/$package" "$scratch/$package.sha256" "$output"
printf '已准备公证发行包 %s，Apple 提交 ID 为 %s\n' "$output/$package" "$submission"
