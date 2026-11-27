#!/usr/bin/env bash
set -Eeuo pipefail

if [[ $# -ne 2 ]]; then
  printf '用法：%s amd64.deb arm64.deb\n' "$0" >&2
  exit 2
fi

amd64=$(realpath -e -- "$1")
arm64=$(realpath -e -- "$2")
version=$(dpkg-deb -f "$amd64" Version)
[[ $(dpkg-deb -f "$amd64" Architecture) == amd64 ]]
[[ $(dpkg-deb -f "$arm64" Architecture) == arm64 ]]

scratch=$(mktemp -d /tmp/multipan-apt-check.XXXXXXXX)
trap 'rm -rf -- "$scratch"' EXIT
chmod 755 "$scratch"
export GNUPGHOME="$scratch/gnupg"
install -d -m700 -- "$GNUPGHOME"
gpg --batch --pinentry-mode loopback --passphrase '' \
  --quick-generate-key 'MultiPan APT CI <apt-ci@invalid.example>' ed25519 sign 1d
keys=$(gpg --batch --with-colons --list-secret-keys)
fingerprint=$(awk -F: '$1 == "fpr" {print $10}' <<< "$keys")
repo="$scratch/repository"
prepare="$(dirname -- "$0")/prepare-apt-repository.sh"
for old in 0.0.0 0.0.1; do
  for arch in amd64 arm64; do
    fixture="$scratch/fixture-$old-$arch"
    mkdir -p -- "$fixture/DEBIAN"
    printf 'Package: multipan\nVersion: %s\nArchitecture: %s\nMaintainer: MultiPan CI <apt-ci@invalid.example>\nDescription: APT retention fixture\n' \
      "$old" "$arch" > "$fixture/DEBIAN/control"
    dpkg-deb --build "$fixture" "$scratch/multipan_${old}_$arch.deb"
  done
  bash "$prepare" "$scratch/multipan_${old}_amd64.deb" "$scratch/multipan_${old}_arm64.deb" "$fingerprint" "$scratch/snapshot-$old"
done
bash "$prepare" --retain-from "$scratch/snapshot-0.0.1" --retain-from "$scratch/snapshot-0.0.0" \
  "$amd64" "$arm64" "$fingerprint" "$repo"

mkdir -p -- "$scratch/lists" "$scratch/downloads" "$scratch/corrupt" "$scratch/empty"
: > "$scratch/status"
printf 'deb [arch=amd64,arm64 signed-by=%s] file:%s stable main\n' \
  "$repo/multipan-archive-keyring.gpg" "$repo" > "$scratch/sources.list"
cat > "$scratch/apt.conf" <<EOF
Dir::Etc::Parts "$scratch/empty";
Dir::Etc::Main "/dev/null";
Dir::Etc::SourceList "$scratch/sources.list";
Dir::Etc::SourceParts "$scratch/empty";
Dir::State::Lists "$scratch/lists";
Dir::State::Status "$scratch/status";
Dir::Cache::PkgCache "";
Dir::Cache::SrcPkgCache "";
APT::Architecture "amd64";
APT::Architectures { "amd64"; "arm64"; };
Acquire::Languages "none";
EOF
export APT_CONFIG="$scratch/apt.conf"
grep -Fxq 'Acquire-By-Hash: yes' "$repo/dists/stable/Release"
# Hide mutable names to prove APT reads the signed, content-addressed indexes.
for arch in amd64 arm64; do
  index="$repo/dists/stable/main/binary-$arch"
  mv -- "$index/Packages" "$scratch/Packages-$arch"
  mv -- "$index/Packages.gz" "$scratch/Packages-$arch.gz"
done
apt-get -o APT::Update::Error-Mode=any update

(
  cd -- "$scratch/downloads"
  apt-get download "multipan:amd64=$version"
  apt-get download "multipan:arm64=$version"
  cmp -- "$amd64" "multipan_${version}_amd64.deb"
  cmp -- "$arm64" "multipan_${version}_arm64.deb"
)

# A signed index must not validate a package that changed after publication.
printf 'integrity check\n' >> "$repo/pool/main/m/multipan/arm64/multipan_${version}_arm64.deb"
(
  cd -- "$scratch/corrupt"
  if LC_ALL=C apt-get download "multipan:arm64=$version" > "$scratch/corrupt.log" 2>&1; then
    printf 'APT 接受了摘要不匹配的包\n' >&2
    exit 1
  fi
  if ! grep -Eq 'File has unexpected size|Hash Sum mismatch' "$scratch/corrupt.log"; then
    cat -- "$scratch/corrupt.log" >&2
    exit 1
  fi
)
# Simulate a client receiving a cached InRelease after the new pool is deployed.
for old in 0.0.0 0.0.1; do
  cp -- "$scratch/snapshot-$old/dists/stable/InRelease" "$repo/dists/stable/InRelease"
  old_lists="$scratch/lists-$old"
  mkdir -p -- "$old_lists" "$scratch/old-$old"
  apt-get -o "Dir::State::Lists=$old_lists" -o APT::Update::Error-Mode=any update
  (
    cd -- "$scratch/old-$old"
    for arch in amd64 arm64; do
      apt-get -o "Dir::State::Lists=$old_lists" download "multipan:$arch=$old"
      cmp -- "$scratch/multipan_${old}_$arch.deb" "multipan_${old}_$arch.deb"
    done
  )
done

# Independently signed snapshots must agree on packages with the same URL.
fixture="$scratch/fixture-0.0.1-amd64"
printf 'different build\n' > "$fixture/marker"
dpkg-deb --build "$fixture" "$scratch/conflicting.deb"
bash "$prepare" "$scratch/conflicting.deb" "$fingerprint" "$scratch/conflict-snapshot"
if bash "$prepare" --retain-from "$scratch/snapshot-0.0.1" --retain-from "$scratch/conflict-snapshot" \
  "$amd64" "$arm64" "$fingerprint" "$scratch/collision" > "$scratch/collision.log" 2>&1; then
  printf '仓库生成脚本接受了内容不同的同名包\n' >&2
  exit 1
fi
grep -Fq '同名 Debian 包内容不同' "$scratch/collision.log"
[[ ! -e "$scratch/collision" ]]

printf 'corrupt previous pool\n' >> "$scratch/snapshot-0.0.1/pool/main/m/multipan/arm64/multipan_0.0.1_arm64.deb"
if bash "$prepare" --retain-from "$scratch/snapshot-0.0.1" \
  "$amd64" "$arm64" "$fingerprint" "$scratch/rejected" > "$scratch/retention.log" 2>&1; then
  printf '仓库生成脚本接受了被篡改的旧包\n' >&2
  exit 1
fi
grep -Fq '旧仓库安装包摘要不匹配' "$scratch/retention.log"
[[ ! -e "$scratch/rejected" ]]
printf 'APT 已验证双架构索引、两代旧版本下载和篡改拒绝\n'
