package transfer

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
)

func validRelayTarget(a Account, directory string) bool {
	if a.Kind == "quark" {
		return a.Endpoint == QuarkAPI && validQuarkFolder(directory)
	}
	if a.Kind == "guangya" {
		return a.Endpoint == GuangyaAPI && (directory == guangyaRoot || validGuangyaCopyFileID(directory))
	}
	if a.Kind == "baidu" {
		return a.Endpoint == BaiduAPI && validBaiduPath(directory)
	}
	if a.Kind == "pan123" {
		return a.Endpoint == Pan123API && valid123Folder(directory)
	}
	return relayProvider(a) && validBaiduPath(directory)
}
func relayTargetAcceptsEmpty(kind string) bool {
	return kind != "baidu" && kind != "guangya" && kind != "quark"
}

func validRelaySavedTarget(d diskRelay) bool {
	kind := d.TargetKind
	if !relayTargetAcceptsEmpty(kind) && d.Input.Size == 0 && !d.Folder {
		return false
	}
	if relayTargetNeedsFileID(kind) {
		if d.ParentJobID != "" {
			return validBaiduPath(d.Input.DestinationID) && d.Input.DestinationID != "/"
		}
		return validRelayTarget(relayDirectorySavedAccount(kind), d.Input.DestinationID)
	}
	if kind == "pan123" && d.ParentJobID != "" {
		return validBaiduPath(d.Input.DestinationID) && d.Input.DestinationID != "/"
	}
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
	return validRelayTarget(a, d.Input.DestinationID)
}

// The caller owns a locked, SHA256-checked local original and has durably saved
// Submitted before calling. Use the existing native upload with no resumable
// path: a submitted relay can only inspect its result, never start over.
func relay123Upload(ctx context.Context, client *http.Client, a Account, d diskRelay, spool *os.File, progress func(int64)) error {
	info, e := spool.Stat()
	if e != nil {
		return e
	}
	f := File{Path: spool.Name(), Name: d.Input.Name, Size: d.Input.Size, Modified: info.ModTime().UnixMilli()}
	return (&HTTPUploader{Client: client}).pan123(ctx, a, f, relayTargetParent(d), spool, progress)
}
func relay123Readback(ctx context.Context, client *http.Client, a Account, d diskRelay) error {
	p, e := relaySourcePage(ctx, client, a, relayTargetParent(d))
	if e != nil {
		return e
	}
	var found CloudEntry
	count := 0
	for _, r := range p.Entries {
		if strings.EqualFold(r.Name, d.Input.Name) {
			count++
			found = r
		}
	}
	if count != 1 || found.Name != d.Input.Name || found.Folder || found.Size == nil || *found.Size != d.Input.Size || !validRelayVersion("pan123", relaySourceVersion("pan123", found)) {
		return errors.New("123目标缺少唯一原名、大小和MD5匹配项目，保持待核对")
	}
	in := DownloadInput{AccountID: a.ID, ParentID: relayTargetParent(d), EntryID: found.ID, Name: found.Name}
	entry, resp, expected, e := open123Download(ctx, client, a, in)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if entry.ID != found.ID || entry.Size == nil || *entry.Size != d.Input.Size || entry.Name != found.Name || !strings.EqualFold(expected, relaySourceVersion("pan123", found)) {
		return errors.New("123目标下载版本已变化，保持待核对")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusError(resp.StatusCode)
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "" && !strings.EqualFold(enc, "identity") {
		return errors.New("123目标返回压缩表示，保持待核对")
	}
	h := md5.New()
	digest, e := relayHash(ctx, io.TeeReader(resp.Body, h), d.Input.Size, nil)
	if e != nil {
		return e
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), expected) || digest != d.Job.SHA256 {
		return errors.New("123目标完整字节MD5/SHA256与原件不符，保持待核对")
	}
	return nil
}
