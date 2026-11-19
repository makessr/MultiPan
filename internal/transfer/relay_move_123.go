package transfer

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

func relayMove123Original(ctx context.Context, client *http.Client, a Account, d diskRelay) error {
	in := DownloadInput{AccountID: a.ID, ParentID: d.Input.ParentID, EntryID: d.Input.EntryID, Name: d.Input.Name}
	source, resp, expected, err := open123Download(ctx, client, a, in)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if source.ID != in.EntryID || source.Name != in.Name || source.Folder || source.Size == nil ||
		*source.Size != d.Input.Size || strings.ToLower(expected) != d.ETag || relaySourceVersion(a.Kind, source) != d.ETag ||
		resp.StatusCode != http.StatusOK ||
		resp.Header.Get("Content-Encoding") != "" && !strings.EqualFold(resp.Header.Get("Content-Encoding"), "identity") {
		return errors.New("123 源文件身份、大小或版本无法核对，未移入回收站")
	}
	hash, err := relayHash(ctx, resp.Body, d.Input.Size, nil)
	if err != nil || hash != d.Job.SHA256 {
		return errors.New("123 源文件完整内容无法与副本匹配，未移入回收站")
	}
	// The trash request operates on an ID. Re-read metadata after the long download.
	return relaySource(ctx, client, a, d)
}
