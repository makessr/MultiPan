package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"time"
)

func copyAccountEntry(ctx context.Context, client *http.Client, a Account, row MoveBatchEntry, checkpoint ...func(CloudEntry, []CopyManifestItem) error) (CloudEntry, error) {
	if a.Kind != "baidu" && a.Kind != "webdav" && a.Kind != "ctdav" {
		return CloudEntry{}, errors.New("盘内复制当前支持百度和WebDAV")
	}
	in := row.Input
	if !validBaiduPath(in.EntryID) || !validBaiduPath(in.ParentID) || !validBaiduPath(in.DestinationID) || in.EntryID == "/" || path.Dir(in.EntryID) != in.ParentID || path.Base(in.EntryID) != in.Name {
		return CloudEntry{}, errors.New("复制路径无效，请重新浏览目录")
	}
	if e := validateAccountMoveBatch(ctx, client, a, []MoveBatchEntry{row}); e != nil {
		return CloudEntry{}, fmt.Errorf("复制预检查失败：%w", e)
	}
	before, e := ListAccountFiles(ctx, client, a, in.ParentID)
	if e != nil {
		return CloudEntry{}, e
	}
	var source CloudEntry
	for _, r := range before.Entries {
		if r.ID == in.EntryID && r.Folder == row.Folder && r.Name == in.Name {
			source = r
		}
	}
	if source.ID == "" || !row.Folder && (source.Size == nil || *source.Size < 0) {
		return CloudEntry{}, errors.New("原文件大小或来源无法确认，未提交复制")
	}
	manifest, e := copyManifest(ctx, client, a, source)
	if e != nil {
		return CloudEntry{}, e
	}
	if len(checkpoint) > 0 {
		if e = checkpoint[0](source, manifest); e != nil {
			return CloudEntry{}, e
		}
	}
	destination := path.Join(in.DestinationID, in.Name)
	var requestErr error
	if a.Kind == "baidu" {
		c := newBaiduClient(client, a)
		if e = c.session(ctx); e != nil {
			return CloudEntry{}, e
		}
		payload, _ := json.Marshal([]map[string]string{{"path": in.EntryID, "dest": in.DestinationID, "newname": in.Name, "ondup": "fail"}})
		requestErr = c.api(ctx, "POST", "filemanager", url.Values{"opera": {"copy"}}, url.Values{"async": {"0"}, "ondup": {"fail"}, "filelist": {string(payload)}}, nil)
	} else {
		base, e := url.Parse(a.Endpoint)
		if e != nil {
			return CloudEntry{}, e
		}
		src, dst := *base, *base
		src.Path = path.Join(base.Path, in.EntryID)
		src.RawPath = ""
		dst.Path = path.Join(base.Path, destination)
		dst.RawPath = ""
		if row.Folder {
			src.Path += "/"
			dst.Path += "/"
		}
		req, e := http.NewRequestWithContext(ctx, "COPY", src.String(), nil)
		if e != nil {
			return CloudEntry{}, e
		}
		req.SetBasicAuth(a.Username, a.Secret)
		req.Header.Set("Destination", dst.String())
		req.Header.Set("Overwrite", "F")
		if row.Folder {
			req.Header.Set("Depth", "infinity")
		} else {
			req.Header.Set("Depth", "0")
		}
		resp, e := client.Do(req)
		if e != nil {
			requestErr = e
		} else {
			resp.Body.Close()
			if resp.StatusCode == http.StatusMultiStatus {
				return CloudEntry{}, errors.New("复制返回部分结果，请检查原目录、目标目录及内容，程序不会自动重试")
			}
			if resp.StatusCode != http.StatusCreated {
				requestErr = fmt.Errorf("WebDAV COPY HTTP=%d", resp.StatusCode)
			}
		}
	}
	uncertain := errors.New("复制结果待核对，请检查原目录和目标目录；程序不会自动重试。文件夹内部内容需另行检查")
	for attempt := 0; attempt < 3; attempt++ {
		result, e := checkCopySnapshot(ctx, client, a, row, source, manifest)
		if e == nil {
			return result, nil
		}
		if requestErr != nil || attempt == 2 {
			break
		}
		select {
		case <-ctx.Done():
			return CloudEntry{}, uncertain
		case <-time.After(350 * time.Millisecond):
		}
	}
	return CloudEntry{}, uncertain
}
