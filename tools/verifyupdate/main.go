package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"multipan/internal/installation"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	metadata := flag.String("manifest", "", "local release manifest")
	signature := flag.String("signature", "", "local detached Ed25519 signature")
	publisher := flag.String("publisher-key", "", "trusted Ed25519 public key in hex")
	current := flag.String("installed-version", "", "installed MultiPan version")
	installer := flag.String("installer", "", "local installer to verify")
	releaseURL := flag.String("release-url", "", "trusted HTTPS release directory to check")
	stageDir := flag.String("stage-dir", "", "existing absolute directory for a verified update installer")
	requireAuthenticode := flag.Bool("require-authenticode", false, "verify the embedded Windows signature of a local or staged installer")
	flag.Parse()
	if *publisher == "" || *current == "" || flag.NArg() != 0 {
		flag.Usage()
		return errors.New("请指定可信发布公钥和当前版本")
	}
	key, err := hex.DecodeString(*publisher)
	if err != nil {
		return errors.New("可信发布公钥格式无效")
	}
	if *releaseURL != "" {
		if *metadata != "" || *signature != "" || *installer != "" {
			return errors.New("在线检查和本地安装包核对请分别执行")
		}
		if *requireAuthenticode && *stageDir == "" {
			return errors.New("在线 Authenticode 核对需要指定下载暂存目录")
		}
		update, err := installation.CheckUpdateOrigin(context.Background(), *releaseURL, key, *current)
		if errors.Is(err, installation.ErrSameUpdateVersion) {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "current", "version": *current})
		}
		if err != nil {
			return err
		}
		if *stageDir != "" {
			stage := update.StageInstaller
			if *requireAuthenticode {
				stage = update.StageSignedInstaller
			}
			path, err := stage(context.Background(), *stageDir)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]string{
				"status": "staged", "version": update.Version(), "asset": update.AssetName(), "path": path,
			})
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{
			"status": "available", "version": update.Version(), "asset": update.AssetName(),
		})
	}
	if *stageDir != "" {
		return errors.New("下载暂存需要指定 HTTPS 更新源")
	}
	if *metadata == "" || *signature == "" || *installer == "" {
		return errors.New("离线核对需要更新清单、分离签名和安装包")
	}
	manifest, err := openRegular(*metadata)
	if err != nil {
		return err
	}
	defer manifest.Close()
	sig, err := openRegular(*signature)
	if err != nil {
		return err
	}
	defer sig.Close()
	update, err := installation.VerifyUpdateManifest(manifest, sig, key, *current, time.Now())
	if err != nil {
		return err
	}
	if filepath.Base(*installer) != update.AssetName() {
		return errors.New("安装包文件名与签名清单不一致")
	}
	openInstaller := openRegular
	if *requireAuthenticode {
		openInstaller = installation.OpenSignedUpdateInstaller
	}
	asset, err := openInstaller(*installer)
	if err != nil {
		return err
	}
	defer asset.Close()
	if err := update.VerifyInstaller(asset); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"version": update.Version(), "asset": update.AssetName()})
}

func openRegular(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("更新核对仅接受本地普通文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	observed, err := file.Stat()
	if err != nil || !os.SameFile(info, observed) || !observed.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("更新核对文件已变化或不是普通文件")
	}
	return file, nil
}
