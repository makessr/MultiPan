package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
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
	installer := flag.String("installer", "", "final Windows installer file")
	version := flag.String("version", "", "release version")
	keyFile := flag.String("private-key", "", "local PKCS#8 Ed25519 private key (PEM)")
	output := flag.String("output-dir", "", "new directory for update.json and update.sig")
	validity := flag.Duration("validity", 7*24*time.Hour, "manifest validity, up to 720h")
	requireAuthenticode := flag.Bool("require-authenticode", false, "require a trusted embedded Windows installer signature before signing the manifest")
	flag.Parse()
	if *installer == "" || *version == "" || *keyFile == "" || *output == "" || flag.NArg() != 0 {
		flag.Usage()
		return errors.New("请指定最终安装包、版本、发布密钥路径和新输出目录")
	}
	dir, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		return errors.New("更新签名输出目录已存在或无法核对")
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
	key, err := readPublisherKey(*keyFile)
	if err != nil {
		return err
	}
	defer clear(key)
	raw, signature, err := installation.SignUpdateManifest(asset, filepath.Base(*installer), *version, time.Now(), *validity, key)
	if err != nil {
		return err
	}
	if err := writeSignedUpdate(dir, raw, signature); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{
		"directory": dir, "version": *version, "publisherKey": hex.EncodeToString(key.Public().(ed25519.PublicKey)),
	})
}

func openRegular(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil, errors.New("无法读取指定的本地文件")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("无法读取指定的本地文件")
	}
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = f.Close()
		return nil, errors.New("指定文件已变化")
	}
	return f, nil
}

func readPublisherKey(path string) (ed25519.PrivateKey, error) {
	invalid := errors.New("发布密钥需要是 PKCS#8 PEM 格式的 Ed25519 私钥")
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	const maxPEM = 4 << 10
	raw, err := io.ReadAll(io.LimitReader(f, maxPEM+1))
	defer clear(raw)
	if err != nil || len(raw) > maxPEM || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("-----BEGIN PRIVATE KEY-----")) {
		return nil, invalid
	}
	block, extra := pem.Decode(raw)
	if block == nil {
		return nil, invalid
	}
	defer clear(block.Bytes)
	if block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(extra)) != 0 {
		return nil, invalid
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	key, ok := parsed.(ed25519.PrivateKey)
	if err != nil || !ok {
		return nil, invalid
	}
	return key, nil
}

func writeSignedUpdate(dir string, raw, signature []byte) error {
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	var created []string
	success := false
	defer func() {
		if success {
			return
		}
		for _, path := range created {
			_ = os.Remove(path)
		}
		_ = os.Remove(dir)
	}()
	for _, output := range []struct {
		name string
		data []byte
	}{{installation.UpdateManifestName, raw}, {installation.UpdateSignatureName, signature}} {
		path := filepath.Join(dir, output.name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		created = append(created, path)
		_, err = f.Write(output.data)
		if err == nil {
			err = f.Sync()
		}
		if err = errors.Join(err, f.Close()); err != nil {
			return err
		}
	}
	success = true
	return nil
}
