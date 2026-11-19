package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const lanzouCopyFolderPrefix = "MultiPan-LZ-copy-"
const lanzouCopyMaxBytes = 200_000_000

func (m *Manager) ValidateLanzouCopyBatch(ctx context.Context, rows []MoveBatchEntry) error {
	return m.validateNativeCopyBatch(ctx, NewHTTPUploader().Client, rows, "lanzou", false)
}

func validateLanzouCopyBatchContents(ctx context.Context, client *http.Client, a Account, rows []MoveBatchEntry) error {
	if _, err := listLanzouFolders(ctx, client, a, rows[0].Input.DestinationID, true); err != nil {
		return errors.New("蓝奏目标目录无法完整核对")
	}
	for _, row := range rows {
		in := row.Input
		if row.Folder {
			if _, _, err := planLanzouFolderCopy(ctx, client, a, row); err != nil {
				return fmt.Errorf("%s：%w", in.Name, err)
			}
			continue
		}
		_, resp, err := lanzouCopyDownload(ctx, client, a, in.ParentID, in.EntryID, in.Name)
		if err != nil {
			return fmt.Errorf("%s：%w", in.Name, err)
		}
		resp.Body.Close()
	}
	return ctx.Err()
}

type lanzouCopyProof struct {
	SHA256       string `json:"sha256"`
	FolderID     string `json:"folderId,omitempty"`
	Acknowledged bool   `json:"acknowledged,omitempty"`
}

func validLanzouCopyInput(row MoveBatchEntry) bool {
	in := row.Input
	return in.AccountID != "" && relayName(in.Name) && validFolderID(in.ParentID) &&
		validFolderID(in.DestinationID) && validFolderID(in.EntryID) && in.EntryID != "-1" &&
		in.EntryID != in.ParentID && in.EntryID != in.DestinationID && in.ParentID != in.DestinationID
}

func validSavedLanzouCopy(d diskCopy) bool {
	if d.Direct123 || d.DirectGuangya || d.Pan123 != nil || d.Guangya != nil || !validLanzouCopyInput(d.Input) ||
		validateLanzouFolderName(d.Receipt.OutputFolder) != nil {
		return false
	}
	p := d.Lanzou
	if !d.Submitted {
		return p == nil
	}
	if p == nil || !validRelayGuangyaVersion("sha256:"+p.SHA256) || !validCopyManifest(d) {
		return false
	}
	if d.Input.Folder && !validLanzouFolderManifest(d.Manifest, d.Source.ID) ||
		!d.Input.Folder && (*d.Source.Size <= 0 || *d.Source.Size > lanzouCopyMaxBytes) {
		return false
	}
	if p.FolderID != "" && (!validFolderID(p.FolderID) || p.FolderID == "-1" ||
		p.FolderID == d.Input.Input.ParentID || p.FolderID == d.Input.Input.DestinationID) {
		return false
	}
	return (!p.Acknowledged || p.FolderID != "") && (d.Receipt.State != "confirmed" || p.Acknowledged)
}

func lanzouCopyDownload(ctx context.Context, client *http.Client, a Account, parent, id, name string) (CloudEntry, *http.Response, error) {
	row, resp, _, err := openLanzouDownload(ctx, client, a, DownloadInput{AccountID: a.ID, ParentID: parent, EntryID: id, Name: name})
	if err != nil {
		return CloudEntry{}, nil, err
	}
	size := resp.ContentLength
	encoding := resp.Header.Get("Content-Encoding")
	if resp.StatusCode != http.StatusOK || resp.Uncompressed || encoding != "" && !strings.EqualFold(encoding, "identity") ||
		resp.Header.Get("Content-Range") != "" || size <= 0 || size > lanzouCopyMaxBytes {
		resp.Body.Close()
		return CloudEntry{}, nil, errors.New("蓝奏复制需要返回完整大小的非空文件（最多 200 MB）")
	}
	row.Size = &size
	return row, resp, nil
}

func startLanzouCopy(ctx context.Context, client *http.Client, a Account, d diskCopy, persist func(diskCopy) error) (CloudEntry, error) {
	in := d.Input.Input
	target, err := listLanzouFolders(ctx, client, a, in.DestinationID, true)
	if err != nil {
		return CloudEntry{}, errors.New("蓝奏目标目录无法完整核对，未复制")
	}
	for _, folder := range target.Folders {
		if strings.EqualFold(folder.Name, d.Receipt.OutputFolder) {
			return CloudEntry{}, errors.New("蓝奏目标已有同名子目录，未复制")
		}
	}
	if d.Input.Folder {
		return startLanzouFolderCopy(ctx, client, a, d, persist)
	}
	source, file, checksum, err := cacheLanzouCopyFile(ctx, client, a, in.ParentID, in.EntryID, in.Name)
	if err != nil {
		return CloudEntry{}, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	d.Source = source
	d.Manifest = []CopyManifestItem{{Size: *source.Size}}
	d.Lanzou = &lanzouCopyProof{SHA256: checksum}
	d.Submitted = true
	if err = persist(d); err != nil {
		return CloudEntry{}, err
	}
	folder, err := createLanzouFolder(ctx, client, a, in.DestinationID, d.Receipt.OutputFolder, false)
	if err != nil {
		return CloudEntry{}, err
	}
	d.Lanzou.FolderID = folder.ID
	if !validSavedLanzouCopy(d) {
		return CloudEntry{}, errors.New("蓝奏新目录身份不符，未上传")
	}
	if err = persist(d); err != nil {
		return CloudEntry{}, err
	}
	entries, err := lanzouCopyTarget(ctx, client, a, d)
	if err != nil || len(entries) != 0 {
		return CloudEntry{}, errors.New("蓝奏目标子目录未确认为空，未上传")
	}
	if err = uploadCachedLanzouFile(ctx, client, a, source, folder.ID, file); err != nil {
		return CloudEntry{}, err
	}
	d.Lanzou.Acknowledged = true
	if err = persist(d); err != nil {
		return CloudEntry{}, err
	}
	return checkLanzouCopy(ctx, client, a, d)
}

func cacheLanzouCopyFile(ctx context.Context, client *http.Client, a Account, parent, id, name string) (CloudEntry, *os.File, string, error) {
	row, resp, err := lanzouCopyDownload(ctx, client, a, parent, id, name)
	if err != nil {
		return CloudEntry{}, nil, "", err
	}
	defer resp.Body.Close()
	file, err := os.CreateTemp("", "multipan-lanzou-copy-*")
	if err != nil {
		return CloudEntry{}, nil, "", errors.New("无法创建本机临时副本")
	}
	keep := false
	defer func() {
		if !keep {
			file.Close()
			os.Remove(file.Name())
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(ctxReader{ctx, resp.Body}, *row.Size+1))
	if err != nil || size != *row.Size {
		return CloudEntry{}, nil, "", errors.New("未完整取得蓝奏源文件")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return CloudEntry{}, nil, "", errors.New("无法读取本机临时副本")
	}
	keep = true
	return row, file, hex.EncodeToString(hash.Sum(nil)), nil
}

func uploadCachedLanzouFile(ctx context.Context, client *http.Client, a Account, row CloudEntry, destination string, file *os.File) error {
	info, err := file.Stat()
	if err != nil || info.Size() != *row.Size {
		return errors.New("无法核对本机临时副本")
	}
	return (&HTTPUploader{Client: client}).lanzou(ctx, a, File{Name: row.Name, Size: *row.Size, Modified: info.ModTime().UnixMilli()}, destination, file, func(int64) {})
}

func lanzouCopyTarget(ctx context.Context, client *http.Client, a Account, d diskCopy) ([]CloudEntry, error) {
	folders, err := listLanzouFolders(ctx, client, a, d.Input.Input.DestinationID, true)
	if err != nil {
		return nil, err
	}
	for _, folder := range folders.Folders {
		if folder.ID == d.Lanzou.FolderID && folder.Name == d.Receipt.OutputFolder {
			child, err := listLanzouFolders(ctx, client, a, folder.ID, true)
			if err != nil || len(child.Folders) != 0 || len(child.Breadcrumbs) != len(folders.Breadcrumbs)+1 ||
				child.Breadcrumbs[len(child.Breadcrumbs)-2].ID != d.Input.Input.DestinationID ||
				child.Breadcrumbs[len(child.Breadcrumbs)-1] != folder {
				return nil, errors.New("蓝奏副本目录层级或子目录状态不符")
			}
			return readLanzouFiles(ctx, client, a, folder.ID, true)
		}
	}
	return nil, errors.New("蓝奏副本目录名称或位置已变化")
}

func checkLanzouCopy(ctx context.Context, client *http.Client, a Account, d diskCopy) (CloudEntry, error) {
	if !validSavedLanzouCopy(d) || d.Lanzou == nil || !d.Lanzou.Acknowledged {
		return CloudEntry{}, errors.New("缺少蓝奏上传受理回执，请核对目标目录")
	}
	if d.Input.Folder {
		return checkLanzouFolderCopy(ctx, client, a, d)
	}
	rows, err := lanzouCopyTarget(ctx, client, a, d)
	if err != nil || len(rows) != 1 || rows[0].ID == d.Source.ID || rows[0].Name != d.Source.Name {
		return CloudEntry{}, errors.New("无法核对蓝奏副本的身份或名称")
	}
	for _, item := range []struct{ parent, id string }{
		{d.Input.Input.ParentID, d.Source.ID}, {d.Lanzou.FolderID, rows[0].ID},
	} {
		row, resp, err := lanzouCopyDownload(ctx, client, a, item.parent, item.id, d.Source.Name)
		if err != nil {
			return CloudEntry{}, err
		}
		digest, err := relayHash(ctx, resp.Body, *d.Source.Size, nil)
		resp.Body.Close()
		if err != nil || *row.Size != *d.Source.Size || digest != d.Lanzou.SHA256 {
			return CloudEntry{}, errors.New("蓝奏源文件或副本的完整内容无法核对")
		}
	}
	rows[0].Size = d.Source.Size
	return rows[0], nil
}
