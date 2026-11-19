package transfer

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
)

func validRelayGuangyaVersion(value string) bool {
	marker, sha := relayQuarkParts(value)
	b, e := hex.DecodeString(sha)
	return marker == "sha256" && e == nil && len(b) == 32 && sha == strings.ToLower(sha)
}
func relayGuangyaPage(ctx context.Context, client *http.Client, a Account, parent string) (FilePage, error) {
	return readGuangyaRelayPage(ctx, client, a, parent, false)
}

func readGuangyaRelayPage(ctx context.Context, client *http.Client, a Account, parent string, strict bool) (FilePage, error) {
	if !validQuarkFolder(parent) {
		return FilePage{}, errors.New("光鸭来源目录ID无效")
	}
	pid := parent
	if pid == guangyaRoot {
		pid = ""
	}
	p := FilePage{CurrentID: parent, Entries: []CloudEntry{}}
	seen := map[string]bool{}
	total := -1
	maxPages := 100
	if strict {
		maxPages = 5
	}
	for page := 0; page < maxPages; page++ {
		var data struct {
			List []struct {
				ID     string  `json:"fileId"`
				Parent *string `json:"parentId"`
				Name   string  `json:"fileName"`
				Size   *int64  `json:"fileSize"`
				Type   *int    `json:"resType"`
			} `json:"list"`
			Total *int `json:"total"`
		}
		if _, e := (gyClient{client, a}).call(ctx, "/userres/v1/file/get_file_list", map[string]any{"parentId": pid, "page": page, "pageSize": 100, "orderBy": 3, "sortType": 1}, &data); e != nil {
			return FilePage{}, e
		}
		if strict && (data.List == nil || len(data.List) > 100 || data.Total == nil || total >= 0 && *data.Total != total) {
			return FilePage{}, errors.New("光鸭目录清单或分页总数无法核对")
		}
		for _, r := range data.List {
			if r.Parent == nil || *r.Parent != pid || !validQuarkFolder(r.ID) || r.ID == "0" || r.ID == guangyaRoot || r.ID == parent || seen[r.ID] || !relayName(r.Name) || r.Type == nil || (*r.Type != 1 && *r.Type != 2) || *r.Type == 1 && (r.Size == nil || *r.Size < 0) {
				return FilePage{}, errors.New("光鸭来源父目录、类型、大小或ID依据缺失/矛盾")
			}
			seen[r.ID] = true
			p.Entries = append(p.Entries, CloudEntry{ID: r.ID, Name: r.Name, Folder: *r.Type == 2, Size: r.Size})
		}
		total = 0
		if data.Total != nil {
			total = *data.Total
		}
		if total < 0 || strict && (len(seen) > total || len(data.List) < 100 && len(seen) != total) {
			return FilePage{}, errors.New("光鸭目录数量无效或列表不完整")
		}
		if len(data.List) < 100 {
			if total > len(seen) {
				return FilePage{}, errors.New("光鸭来源目录列表不完整，未列出可复制项")
			}
			return p, nil
		}
		if total > 0 && len(seen) >= total {
			return p, nil
		}
	}
	return FilePage{}, errors.New("光鸭目录超过可核对页数，请拆分目录")
}

func readRelayGuangyaSnapshot(ctx context.Context, client *http.Client, a Account, d diskRelay, progress func(int64)) (string, error) {
	in := d.Input
	entry := CloudEntry{ID: in.EntryID, Name: in.Name, Size: &in.Size}
	digest, err := readGuangyaFileHash(ctx, client, a, in.ParentID, entry, progress)
	if err != nil {
		return "", err
	}
	d.ETag = "sha256:" + digest
	if err := relaySource(ctx, client, a, d); err != nil {
		return "", errors.New("光鸭原件在预读期间变化，整批未创建")
	}
	return d.ETag, nil
}

func relayPinnedContent(ctx context.Context, client *http.Client, a Account, d diskRelay, out io.Writer, progress func(int64)) (string, error) {
	if a.Kind == "quark" {
		return relayQuarkContent(ctx, client, a, d, out, progress)
	}
	if a.Kind != "guangya" {
		return "", errors.New("可信原件来源类型无效")
	}
	in := DownloadInput{AccountID: a.ID, ParentID: d.Input.ParentID, EntryID: d.Input.EntryID, Name: d.Input.Name}
	row, resp, _, e := openGuangyaDownload(ctx, client, a, in)
	if e != nil {
		return "", e
	}
	defer resp.Body.Close()
	if !relayPinMetaMatches(a.Kind, row, d) {
		return "", errors.New("光鸭来源ID、名称或大小与可信原件不符")
	}
	return relayPinnedBody(ctx, resp, d, "光鸭", relayPinnedSHA(a.Kind, d.ETag), out, progress)
}
func relayGuangyaDownload(ctx context.Context, client *http.Client, a Account, d diskRelay, file string, progress func(int64)) (string, error) {
	f, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", e
	}
	sha, e := relayPinnedContent(ctx, client, a, d, f, progress)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return sha, e
}
