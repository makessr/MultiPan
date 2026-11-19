package transfer

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strings"
)

func relayBaiduPage(ctx context.Context, client *http.Client, a Account, parent string) (FilePage, error) {
	rows, e := newBaiduClient(client, a).list(ctx, parent)
	if e != nil {
		return FilePage{}, e
	}
	p := FilePage{CurrentID: parent, Entries: []CloudEntry{}}
	for _, r := range rows {
		if r.Dir == nil || (*r.Dir != 0 && *r.Dir != 1) || path.Dir(r.Path) != parent || r.Name != path.Base(r.Path) {
			return FilePage{}, errors.New("百度源目录名称或类型矛盾，未开始中转")
		}
		revision := ""
		if valid123Folder(string(r.ID)) && r.ID != "0" {
			revision = string(r.ID) + ":" + strings.ToLower(r.MD5)
		}
		p.Entries = append(p.Entries, CloudEntry{ID: r.Path, ObjectID: string(r.ID), Name: r.Name, Folder: *r.Dir == 1, Size: r.Size, revision: revision})
	}
	return p, nil
}

// Only a provider reference that actually matches the original bytes is usable.
// An opaque/transformed MD5 is not decoded or accepted by weakening validation.
func relayBaiduDownload(ctx context.Context, client *http.Client, a Account, d diskRelay, file string, progress func(int64)) (string, error) {
	in := DownloadInput{AccountID: a.ID, ParentID: d.Input.ParentID, EntryID: d.Input.EntryID, Name: d.Input.Name}
	entry, resp, _, e := openBaiduDownload(ctx, client, a, in)
	if e != nil {
		return "", e
	}
	defer resp.Body.Close()
	if entry.ID != d.Input.EntryID || entry.Name != d.Input.Name || entry.Size == nil || *entry.Size != d.Input.Size || relaySourceVersion(a.Kind, entry) != d.ETag {
		return "", errors.New("百度源文件ID、大小或MD5已变化，未上传目标")
	}
	parts := strings.Split(d.ETag, ":")
	if len(parts) != 2 || !validRelayVersion("baidu", d.ETag) {
		return "", errors.New("百度原件版本依据无效，未上传目标")
	}
	return relayWriteMD5(ctx, resp, d, file, parts[1], "百度", progress)
}
