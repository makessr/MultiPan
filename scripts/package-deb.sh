#!/usr/bin/env bash
set -Eeuo pipefail

if [[ $# != 3 ]]; then
  printf '用法：%s 二进制 版本 输出目录\n' "$0" >&2
  exit 2
fi
binary=$1
version=$2
output=$3
if [[ ! -f "$binary" || ! -x "$binary" || ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf '无效的 Linux 二进制或版本\n' >&2
  exit 2
fi

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
arch=$(dpkg --print-architecture)
case "$arch" in
  amd64|arm64) ;;
  *) printf '暂不支持此 Debian 架构：%s\n' "$arch" >&2; exit 2 ;;
esac
mkdir -p -- "$output"
output=$(realpath -- "$output")
stage=$(mktemp -d)
staged_package=
trap 'rm -rf -- "$stage"; if [[ -n "$staged_package" ]]; then rm -f -- "$staged_package"; fi' EXIT
package="$stage/package"
install -Dm755 -- "$binary" "$package/usr/bin/multipan"
install -Dm644 -- "$root/build/appicon.png" "$package/usr/share/pixmaps/multipan.png"
install -Dm644 -- "$root/LICENSE" "$package/usr/share/doc/multipan/LICENSE"
install -Dm644 -- "$root/THIRD_PARTY_NOTICES.md" "$package/usr/share/doc/multipan/copyright"
mkdir -p -- "$package/DEBIAN" "$package/usr/share/applications"
chmod 0755 "$package" "$package/DEBIAN"

cat > "$package/DEBIAN/control" <<EOF
Package: multipan
Version: $version
Architecture: $arch
Section: utils
Priority: optional
Maintainer: MultiPan <46600469+makessr@users.noreply.github.com>
Homepage: https://github.com/makessr/MultiPan
Depends: libgtk-4-1, libwebkitgtk-6.0-4, xdg-utils
Recommends: gnome-keyring
Description: MultiPan desktop cloud drive client
 Manage files and upload to multiple cloud drives.
EOF
cat > "$package/usr/share/applications/multipan.desktop" <<'EOF'
[Desktop Entry]
Type=Application
Name=MultiPan
Exec=multipan
Icon=multipan
Terminal=false
Categories=Network;FileTransfer;
EOF

name="multipan_${version}_${arch}.deb"
dpkg-deb --root-owner-group --build "$package" "$stage/$name"
staged_package=$(mktemp -- "$output/.multipan-deb.XXXXXXXX")
install -m644 -- "$stage/$name" "$staged_package"
mv -T --no-clobber -- "$staged_package" "$output/$name"
if [[ -e "$staged_package" ]]; then
  printf '输出安装包已存在，未覆盖\n' >&2
  exit 1
fi
printf '%s\n' "$output/$name"
