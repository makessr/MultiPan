package transfer

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path"
	"strings"
)

func relayBaiduUpload(ctx context.Context, client *http.Client, a Account, d diskRelay, spool *os.File, progress func(int64)) error {
	info, e := spool.Stat()
	if e != nil {
		return e
	}
	f := File{Path: spool.Name(), Name: d.Input.Name, Size: d.Input.Size, Modified: info.ModTime().UnixMilli()}
	return (&HTTPUploader{Client: client}).baidu(ctx, a, f, d.Input.DestinationID, spool, progress)
}
func relayBaiduTarget(ctx context.Context, client *http.Client, a Account, d diskRelay) (CloudEntry, error) {
	p, e := relayBaiduPage(ctx, client, a, d.Input.DestinationID)
	if e != nil {
		return CloudEntry{}, e
	}
	var found CloudEntry
	count := 0
	for _, r := range p.Entries {
		if strings.EqualFold(r.Name, d.Input.Name) {
			count++
			found = r
		}
	}
	if count != 1 || found.Name != d.Input.Name || found.ID != path.Join(d.Input.DestinationID, d.Input.Name) || found.Folder || found.Size == nil || *found.Size != d.Input.Size || !valid123Folder(found.ObjectID) || found.ObjectID == "0" {
		return CloudEntry{}, errors.New("百度目标缺少唯一原名、文件ID、类型和大小匹配项目，保持待核对")
	}
	return found, nil
}

// The source already has a verified local SHA256. A target's opaque provider
// MD5 can be a version label, but is never substituted for the content readback.
// The stricter original-MD5 requirement for Baidu sources is unchanged.
func relayBaiduReadback(ctx context.Context, client *http.Client, a Account, d diskRelay) error {
	before, e := relayBaiduTarget(ctx, client, a, d)
	if e != nil {
		return e
	}
	in := DownloadInput{AccountID: a.ID, ParentID: d.Input.DestinationID, EntryID: before.ID, Name: before.Name}
	entry, resp, _, e := openBaiduDownload(ctx, client, a, in)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if entry.ID != before.ID || entry.Name != before.Name || entry.Size == nil || *entry.Size != d.Input.Size || relaySourceVersion("baidu", entry) != before.revision {
		return errors.New("百度目标下载文件ID或版本已变化，保持待核对")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusError(resp.StatusCode)
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "" && !strings.EqualFold(enc, "identity") {
		return errors.New("百度目标返回压缩表示，保持待核对")
	}
	digest, e := relayHash(ctx, resp.Body, d.Input.Size, nil)
	if e != nil {
		return e
	}
	if digest != d.Job.SHA256 {
		return errors.New("百度目标完整SHA256与原始副本不符，保持待核对")
	}
	after, e := relayBaiduTarget(ctx, client, a, d)
	if e != nil {
		return e
	}
	if after.ObjectID != before.ObjectID || after.revision != before.revision {
		return errors.New("百度目标在回读期间被替换或版本变化，保持待核对")
	}
	return nil
}
