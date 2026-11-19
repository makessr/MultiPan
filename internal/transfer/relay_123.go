package transfer

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
)

func validRelaySource(a Account, in RelayInput) bool {
	if a.Kind == "guangya" {
		return a.Endpoint == GuangyaAPI && validQuarkFolder(in.ParentID) && validQuarkFolder(in.EntryID) && in.EntryID != "0" && in.EntryID != guangyaRoot && in.EntryID != in.ParentID
	}
	if a.Kind == "quark" {
		return a.Endpoint == QuarkAPI && validQuarkFolder(in.ParentID) && validQuarkFolder(in.EntryID) && in.EntryID != "0" && in.EntryID != in.ParentID
	}
	if a.Kind == "pan123" {
		return a.Endpoint == Pan123API && valid123Folder(in.ParentID) && valid123Folder(in.EntryID) && in.EntryID != "0" && in.EntryID != in.ParentID
	}
	if a.Kind == "baidu" {
		return a.Endpoint == BaiduAPI && validBaiduPath(in.ParentID) && validBaiduPath(in.EntryID) && in.EntryID != "/" && path.Dir(in.EntryID) == in.ParentID && path.Base(in.EntryID) == in.Name
	}
	return relayProvider(a) && validBaiduPath(in.ParentID) && validBaiduPath(in.EntryID) && in.EntryID != "/" && path.Dir(in.EntryID) == in.ParentID && path.Base(in.EntryID) == in.Name
}
func validRelayVersion(kind, value string) bool {
	if kind == "guangya" {
		return validRelayGuangyaVersion(value)
	}
	if kind == "quark" {
		return validRelayQuarkVersion(value)
	}
	if kind == "baidu" {
		parts := strings.Split(value, ":")
		return len(parts) == 2 && valid123Folder(parts[0]) && parts[0] != "0" && validRelayVersion("pan123", parts[1])
	}
	if kind == "pan123" {
		b, e := hex.DecodeString(value)
		return e == nil && len(b) == md5.Size && value == strings.ToLower(value)
	}
	return (kind == "" || kind == "webdav" || kind == "ctdav") && strongRelayTag(value)
}
func relaySourceVersion(kind string, row CloudEntry) string {
	if kind == "baidu" {
		return strings.ToLower(row.revision)
	}
	if kind == "pan123" {
		return strings.ToLower(row.revision)
	}
	return relayTag(row)
}
func validRelaySavedSource(d diskRelay) bool {
	if d.Folder {
		return validRelayDirectoryStamp(d.SourceKind, d.ETag) && validRelaySource(relayDirectorySavedAccount(d.SourceKind), d.Input)
	}
	if !validRelayVersion(d.SourceKind, d.ETag) {
		return false
	}
	kind := d.SourceKind
	if kind == "" {
		kind = "webdav"
	}
	a := Account{Kind: kind}
	if kind == "pan123" {
		a.Endpoint = Pan123API
	}
	if kind == "baidu" {
		a.Endpoint = BaiduAPI
	}
	if kind == "quark" {
		a.Endpoint = QuarkAPI
	}
	if kind == "guangya" {
		a.Endpoint = GuangyaAPI
	}
	return validRelaySource(a, d.Input)
}
func relaySourcePage(ctx context.Context, client *http.Client, a Account, parent string) (FilePage, error) {
	if a.Kind == "guangya" {
		return relayGuangyaPage(ctx, client, a, parent)
	}
	if a.Kind == "quark" {
		return relayQuarkPage(ctx, client, a, parent)
	}
	if a.Kind == "baidu" {
		return relayBaiduPage(ctx, client, a, parent)
	}
	if a.Kind != "pan123" {
		return ListAccountFiles(ctx, client, a, parent)
	}
	rows, e := (pan123Client{client, a}).list(ctx, parent)
	if e != nil {
		return FilePage{}, e
	}
	p := FilePage{CurrentID: parent, Entries: []CloudEntry{}}
	for _, r := range rows {
		if string(r.ParentID) != parent || (r.Type != 0 && r.Type != 1) {
			return FilePage{}, errors.New("123源目录缺少可靠父目录或类型信息，未启动中转")
		}
		size := r.Size
		p.Entries = append(p.Entries, CloudEntry{ID: string(r.ID), Name: r.Name, Folder: r.Type == 1, Size: &size, revision: r.MD5})
	}
	return p, nil
}

// A CDN may not implement If-Match. For 123, bind the downloaded bytes to the
// captured provider MD5 instead, then retain a SHA256 of the actual local copy.
// Neither a signed URL nor API credentials is persisted in a relay receipt.
func relay123Download(ctx context.Context, client *http.Client, a Account, d diskRelay, file string, progress func(int64)) (string, error) {
	in := DownloadInput{AccountID: a.ID, ParentID: d.Input.ParentID, EntryID: d.Input.EntryID, Name: d.Input.Name}
	source, resp, expected, e := open123Download(ctx, client, a, in)
	if e != nil {
		return "", e
	}
	defer resp.Body.Close()
	if source.ID != d.Input.EntryID || source.Name != d.Input.Name || source.Size == nil || *source.Size != d.Input.Size || strings.ToLower(expected) != d.ETag || relaySourceVersion(a.Kind, source) != d.ETag {
		return "", errors.New("123下载版本与排队原件不符，未上传目标")
	}
	return relayWriteMD5(ctx, resp, d, file, d.ETag, "123", progress)
}

func relayWriteMD5(ctx context.Context, resp *http.Response, d diskRelay, file, expected, label string, progress func(int64)) (string, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", statusError(resp.StatusCode)
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "" && !strings.EqualFold(enc, "identity") {
		return "", errors.New(label + "下载返回压缩表示，未上传目标")
	}
	f, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", e
	}
	h := md5.New()
	digest, e := relayHash(ctx, io.TeeReader(resp.Body, io.MultiWriter(f, h)), d.Input.Size, progress)
	if e == nil && hex.EncodeToString(h.Sum(nil)) != expected {
		e = errors.New(label + "原件MD5与下载字节不符（可能不是原始内容MD5），未上传目标")
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return digest, e
}
