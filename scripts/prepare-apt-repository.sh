#!/usr/bin/env bash
set -Eeuo pipefail

previous=()
while [[ "${1:-}" == --retain-from ]]; do
  if [[ $# -lt 2 || ${#previous[@]} -ge 2 || ! -d "$2" ]]; then
    printf '请为 --retain-from 指定旧仓库目录，最多两次\n' >&2
    exit 2
  fi
  previous+=("$(realpath -e -- "$2")")
  shift 2
done

if [[ $# -lt 3 || $# -gt 4 ]]; then
  printf '用法：%s [--retain-from 旧仓库] Debian包 [第二个架构的Debian包] GPG签名指纹 新输出目录\n' "$0" >&2
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

publish_index_by_hash() {
  local file="$1" index="$2" sha256 sha512
  read -r sha256 _ < <(sha256sum < "$file")
  read -r sha512 _ < <(sha512sum < "$file")
  install -Dm644 -- "$file" "$index/by-hash/SHA256/$sha256"
  install -Dm644 -- "$file" "$index/by-hash/SHA512/$sha512"
}

release_digest() {
  awk -v section="$1:" -v file="$2" '
    $0 == section { match_section=1; next }
    /^[^[:space:]]/ { match_section=0 }
    match_section && $3 == file { digest=$1; count++ }
    END { if (count == 1) print digest; else if (count > 1) exit 1 }
  ' "$stage/retained-release"
}

retain_snapshot() {
  local source="$1" arch index file archive expected256 expected512 actual256 actual512
  local listing="$stage/retained-Packages" package expected found=0
  gpgv --keyring "$repo/multipan-archive-keyring.gpg" --output - \
    "$source/$suite/InRelease" > "$stage/retained-release"
  grep -Fxq 'Origin: MultiPan' "$stage/retained-release"
  grep -Fxq 'Codename: stable' "$stage/retained-release"
  for arch in amd64 arm64; do
    index="main/binary-$arch"
    expected=$(release_digest SHA256 "$index/Packages")
    if [[ -z "$expected" ]]; then
      continue
    fi
    found=1
    for file in Packages Packages.gz; do
      expected256=$(release_digest SHA256 "$index/$file")
      expected512=$(release_digest SHA512 "$index/$file")
      [[ "$expected256" =~ ^[a-f0-9]{64}$ && "$expected512" =~ ^[a-f0-9]{128}$ ]]
      archive="$stage/retained-$file"
      cp -- "$source/$suite/$index/$file" "$archive"
      read -r actual256 _ < <(sha256sum < "$archive")
      read -r actual512 _ < <(sha512sum < "$archive")
      if [[ "$actual256" != "$expected256" || "$actual512" != "$expected512" ]]; then
        printf '旧仓库索引摘要不匹配：%s/%s\n' "$index" "$file" >&2
        return 1
      fi
      publish_index_by_hash "$archive" "$suite/$index"
    done
    package=$(awk -F ': ' '$1 == "Filename" {value=$2; count++} END {if (count == 1) print value; else exit 1}' "$listing")
    expected=$(awk -F ': ' '$1 == "SHA256" {value=$2; count++} END {if (count == 1) print value; else exit 1}' "$listing")
    if [[ ! "$package" =~ ^$pool/$arch/multipan_[0-9]+\.[0-9]+\.[0-9]+_$arch\.deb$ ||
          ! "$expected" =~ ^[a-f0-9]{64}$ ]]; then
      printf '旧仓库安装包路径或摘要无效\n' >&2
      return 1
    fi
    archive="$stage/retained.deb"
    cp -- "$source/$package" "$archive"
    read -r actual256 _ < <(sha256sum < "$archive")
    if [[ "$actual256" != "$expected" ]]; then
      printf '旧仓库安装包摘要不匹配：%s\n' "$package" >&2
      return 1
    fi
    if [[ -e "$package" ]]; then
      if ! cmp -s -- "$archive" "$package"; then
        printf '同名 Debian 包内容不同：%s\n' "$package" >&2
        return 1
      fi
    else
      install -Dm644 -- "$archive" "$package"
    fi
  done
  if [[ "$found" == 0 ]]; then
    printf '旧仓库缺少可验证的 amd64 或 arm64 索引\n' >&2
    return 1
  fi
}
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
    -o APT::FTPArchive::Release::Acquire-By-Hash=yes \
    -o APT::FTPArchive::Release::MD5=false \
    -o APT::FTPArchive::Release::SHA1=false \
    release "$suite" > "$stage/Release"
  # Add the by-hash copies after generating Release so they do not index themselves.
  for arch in "${architectures[@]}"; do
    index="$suite/main/binary-$arch"
    for file in "$index/Packages" "$index/Packages.gz"; do
      publish_index_by_hash "$file" "$index"
    done
  done
  install -m644 -- "$stage/Release" "$suite/Release"
  gpg --batch --local-user "$signer" --clearsign --output "$suite/InRelease" "$suite/Release"
  gpg --batch --export "$signer" > multipan-archive-keyring.gpg
  test -s multipan-archive-keyring.gpg
  # Retained packages are available to old indexes without entering the new ones.
  for snapshot in "${previous[@]}"; do
    retain_snapshot "$snapshot"
  done
  gpgv --keyring "$repo/multipan-archive-keyring.gpg" "$suite/InRelease"
)
mv -T --no-clobber -- "$repo" "$output"
if [[ -d "$repo" ]]; then
  printf '输出目录已出现，未覆盖\n' >&2
  exit 1
fi
printf '%s\n' "$output"
