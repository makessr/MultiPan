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
func openRelayBaiduSource(ctx context.Context, client *http.Client, a Account, d diskRelay) (*http.Response, string, error) {
	if !validRelayVersion("baidu", d.ETag) {
		return nil, "", errors.New("百度原件版本依据无效")
	}
	in := DownloadInput{AccountID: a.ID, ParentID: d.Input.ParentID, EntryID: d.Input.EntryID, Name: d.Input.Name}
	entry, resp, _, e := openBaiduDownload(ctx, client, a, in)
	if e != nil {
		return nil, "", e
	}
	if entry.ID != d.Input.EntryID || entry.Name != d.Input.Name || entry.Size == nil || *entry.Size != d.Input.Size || relaySourceVersion(a.Kind, entry) != d.ETag {
		resp.Body.Close()
		return nil, "", errors.New("百度源文件ID、大小或MD5已变化")
	}
	_, md5, _ := strings.Cut(d.ETag, ":")
	return resp, md5, nil
}

func relayBaiduDownload(ctx context.Context, client *http.Client, a Account, d diskRelay, file string, progress func(int64)) (string, error) {
	resp, md5, err := openRelayBaiduSource(ctx, client, a, d)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return relayWriteMD5(ctx, resp, d, file, md5, "百度", progress)
}

func relayMoveBaiduOriginal(ctx context.Context, client *http.Client, a Account, d diskRelay) error {
	resp, _, err := openRelayBaiduSource(ctx, client, a, d)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	enc := resp.Header.Get("Content-Encoding")
	if resp.StatusCode != http.StatusOK || resp.Uncompressed || resp.Header.Get("Content-Range") != "" ||
		enc != "" && !strings.EqualFold(enc, "identity") {
		return errors.New("百度源文件未返回完整字节")
	}
	hash, err := relayHash(ctx, resp.Body, d.Input.Size, nil)
	if err != nil || hash != d.Job.SHA256 {
		return errors.New("百度源文件 SHA-256 与原副本不符")
	}
	if err := relaySource(ctx, client, a, d); err != nil {
		return err
	}
	source := relayFileTrashSource(d)
	live, err := trashSourceLive(ctx, client, a, source)
	if err != nil {
		return err
	}
	found := false
	for _, row := range live {
		if row.OriginalPath != source.Original.OriginalPath {
			continue
		}
		if found || !sameRecycleRestoreEntry(row, source.Original) {
			return errors.New("百度原路径对应的文件已变化，未移入回收站")
		}
		found = true
	}
	if !found {
		return errors.New("百度原目录无法确认原文件 ID 和大小，未移入回收站")
	}
	return nil
}
