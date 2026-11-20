#!/usr/bin/env bash
set -Eeuo pipefail

if [[ $# -lt 3 || $# -gt 4 ]]; then
  printf '用法：%s Debian包 [第二个架构的Debian包] GPG签名指纹 新输出目录\n' "$0" >&2
  exit 2
fi

signer="${@: -2:1}"
output="${@: -1}"
packages=("${@:1:$#-2}")
if [[ ! "$signer" =~ ^[[:xdigit:]]{40}$ || -e "$output" || -L "$output" ]]; then
  printf '请提供完整签名指纹和未占用的输出目录\n' >&2
  exit 2
fi

parent=$(realpath -e -- "$(dirname -- "$output")")
output="$parent/$(basename -- "$output")"
stage=$(mktemp -d -- "$parent/.multipan-apt.XXXXXXXX")
trap '[[ ! -d "$stage" ]] || rm -rf -- "$stage"' EXIT
repo="$stage/repository"
pool=pool/main/m/multipan
suite=dists/stable
declare -a architectures=()
release_version=
for input in "${packages[@]}"; do
  package=$(realpath -e -- "$input")
  version=$(dpkg-deb -f "$package" Version)
  arch=$(dpkg-deb -f "$package" Architecture)
  if [[ $(dpkg-deb -f "$package" Package) != multipan || ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ||
        ( "$arch" != amd64 && "$arch" != arm64 ) || ( -n "$release_version" && "$version" != "$release_version" ) ||
        -d "$repo/$pool/$arch" ]]; then
    printf '请提供版本相同、架构各异的 MultiPan Debian 包\n' >&2
    exit 2
  fi
  release_version=$version
  architectures+=("$arch")
  install -Dm644 -- "$package" "$repo/$pool/$arch/multipan_${version}_${arch}.deb"
done
(
  cd -- "$repo"
  for arch in "${architectures[@]}"; do
    index="$suite/main/binary-$arch"
    mkdir -p -- "$index"
    apt-ftparchive packages "$pool/$arch" > "$index/Packages"
    gzip -n -c -- "$index/Packages" > "$index/Packages.gz"
  done
  apt-ftparchive \
    -o APT::FTPArchive::Release::Origin=MultiPan \
    -o APT::FTPArchive::Release::Label=MultiPan \
    -o APT::FTPArchive::Release::Suite=stable \
    -o APT::FTPArchive::Release::Codename=stable \
    -o "APT::FTPArchive::Release::Architectures=${architectures[*]}" \
    -o APT::FTPArchive::Release::Components=main \
    release "$suite" > "$stage/Release"
  install -m644 -- "$stage/Release" "$suite/Release"
  gpg --batch --local-user "$signer" --clearsign --output "$suite/InRelease" "$suite/Release"
  gpg --batch --export "$signer" > multipan-archive-keyring.gpg
  test -s multipan-archive-keyring.gpg
  gpgv --keyring "$repo/multipan-archive-keyring.gpg" "$suite/InRelease"
)
mv -T --no-clobber -- "$repo" "$output"
if [[ -d "$repo" ]]; then
  printf '输出目录已出现，未覆盖\n' >&2
  exit 1
fi
printf '%s\n' "$output"
