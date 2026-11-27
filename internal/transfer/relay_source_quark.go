package transfer

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func relaySnapshotEligible(kind string, row CloudEntry) bool {
	return row.ID != "" && !row.Folder && row.Size != nil && *row.Size >= 0 &&
		*row.Size <= relaySnapshotMaxFile &&
		(kind == "quark" && relayQuarkSnapshotStamp(row.revision) != "" || kind == "guangya" && row.revision == "")
}

type RelayPreflight struct {
	CurrentFile string `json:"currentFile"`
	Files       int    `json:"files"`
	Completed   int    `json:"completed"`
	ReadBytes   int64  `json:"readBytes"`
	TotalBytes  int64  `json:"totalBytes"`
}

func (m *Manager) RelayPreflight() *RelayPreflight {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.relayPreflight == nil {
		return nil
	}
	copy := *m.relayPreflight
	return &copy
}

func (m *Manager) updateRelayPreflight(progress RelayPreflight) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.relayPreflight != nil {
		*m.relayPreflight = progress
	}
}

// The caller holds m.mu and folderMutationMu. Keeping the latter until
// cleanup prevents one queue request from clearing another snapshot canceler.
func (m *Manager) beginRelaySnapshotLocked(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(withTransferLimits(ctx, m.limits), 6*time.Hour)
	m.relaySnapshotCancel = cancel
	m.relayPreflight = &RelayPreflight{}
	return ctx, func() {
		cancel()
		m.mu.Lock()
		m.relaySnapshotCancel = nil
		m.relayPreflight = nil
		m.mu.Unlock()
	}
}

func readRelaySnapshotRows(ctx context.Context, client *http.Client, a Account, rows []diskRelay, publish func(RelayPreflight)) error {
	readSnapshot := readRelayQuarkSnapshot
	if a.Kind == "guangya" {
		readSnapshot = readRelayGuangyaSnapshot
	}
	var progress RelayPreflight
	for _, row := range rows {
		if !row.Folder {
			progress.Files++
			progress.TotalBytes += row.Input.Size
		}
	}
	var completeBytes int64
	for i := range rows {
		if rows[i].Folder {
			continue
		}
		progress.CurrentFile = rows[i].Input.Name
		progress.ReadBytes = completeBytes
		publish(progress)
		tag, err := readSnapshot(ctx, client, a, rows[i], func(bytes int64) {
			progress.ReadBytes = completeBytes + min(bytes, rows[i].Input.Size)
			publish(progress)
		})
		if err != nil {
			if ctx.Err() != nil {
				return errors.New("原件预读已停止或超时，整批未创建")
			}
			return err
		}
		rows[i].ETag = tag
		completeBytes += rows[i].Input.Size
		progress.ReadBytes = completeBytes
		progress.Completed++
		progress.CurrentFile = ""
		publish(progress)
	}
	return nil
}

// Capture the exact bytes observed before queue creation without retaining a
// cache. Every later read must still match both the version and this digest.
func readRelayQuarkSnapshot(ctx context.Context, client *http.Client, a Account, d diskRelay, progress func(int64)) (string, error) {
	in := d.Input
	row, resp, _, err := openQuarkDownload(ctx, client, a, DownloadInput{
		AccountID: a.ID, ParentID: in.ParentID, EntryID: in.EntryID, Name: in.Name,
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	encoding := resp.Header.Get("Content-Encoding")
	if !relaySnapshotEligible("quark", row) || row.ID != in.EntryID || row.Name != in.Name ||
		*row.Size != in.Size || relayQuarkSnapshotStamp(row.revision) != relayQuarkSnapshotStamp(d.ETag) || resp.StatusCode != http.StatusOK ||
		resp.Uncompressed || (encoding != "" && !strings.EqualFold(encoding, "identity")) ||
		resp.Header.Get("Content-Range") != "" || (resp.ContentLength >= 0 && resp.ContentLength != in.Size) {
		return "", errors.New("夸克原件版本、大小或完整响应无法核对，整批未创建")
	}
	digest, err := relayHash(ctx, resp.Body, in.Size, progress)
	if err != nil {
		return "", errors.New("夸克原件内容读取不完整，整批未创建")
	}
	d.ETag = relayQuarkSnapshotStamp(row.revision) + ":" + digest
	if err := relaySource(ctx, client, a, d); err != nil {
		return "", errors.New("夸克原件在预读期间变化，整批未创建")
	}
	return d.ETag, nil
}

func relayQuarkParts(value string) (string, string) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}
func validRelayQuarkStamp(value string) bool {
	n, e := strconv.ParseInt(value, 10, 64)
	return e == nil && n > 0 && strconv.FormatInt(n, 10) == value
}
func relayQuarkSnapshotStamp(revision string) string {
	if revision == "" || revision == "0" {
		// Copy requires a complete content hash even when the API omits the timestamp.
		return "sha256"
	}
	if validRelayQuarkStamp(revision) {
		return revision
	}
	return ""
}

func validRelayQuarkVersion(value string) bool {
	stamp, sha := relayQuarkParts(value)
	b, e := hex.DecodeString(sha)
	return (stamp == "sha256" || validRelayQuarkStamp(stamp)) && e == nil && len(b) == 32 && sha == strings.ToLower(sha)
}
func relayUsesOriginalPin(kind string) bool { return kind == "quark" || kind == "guangya" }
func relayPinnedSHA(kind, value string) string {
	_, sha := relayQuarkParts(value)
	if kind == "quark" || kind == "guangya" {
		return sha
	}
	return ""
}
func relayPinnedVersion(kind, account string, row CloudEntry, originals []UploadFingerprint) string {
	if kind == "quark" {
		return relayQuarkPinnedVersion(account, row, originals)
	}
	if kind == "guangya" && !row.Folder && row.Size != nil && row.revision == "" {
		sha := originalFor(originals, account, row)
		if sha != "" {
			return "sha256:" + sha
		}
	}
	return ""
}
func relayPinMetaMatches(kind string, row CloudEntry, d diskRelay) bool {
	if kind == "quark" {
		return relayQuarkMetaMatches(row, d)
	}
	return kind == "guangya" && validRelayGuangyaVersion(d.ETag) && row.ID == d.Input.EntryID && row.Name == d.Input.Name && !row.Folder && row.Size != nil && *row.Size == d.Input.Size
}
func relayQuarkPinnedVersion(account string, row CloudEntry, originals []UploadFingerprint) string {
	if row.Folder || row.Size == nil || !validRelayQuarkStamp(row.revision) {
		return ""
	}
	sha := originalFor(originals, account, row)
	if sha == "" {
		return ""
	}
	return row.revision + ":" + sha
}
func relayQuarkMetaMatches(row CloudEntry, d diskRelay) bool {
	stamp, _ := relayQuarkParts(d.ETag)
	return validRelayQuarkVersion(d.ETag) && row.ID == d.Input.EntryID && row.Name == d.Input.Name && !row.Folder && row.Size != nil && *row.Size == d.Input.Size && relayQuarkSnapshotStamp(row.revision) == stamp
}
func relayQuarkPage(ctx context.Context, client *http.Client, a Account, parent string) (FilePage, error) {
	if a.Endpoint != QuarkAPI {
		return FilePage{}, errors.New("夸克中转来源必须使用固定官方接口")
	}
	rows, e := newQuarkClient(client, a).list(ctx, parent)
	if e != nil {
		return FilePage{}, e
	}
	p := FilePage{CurrentID: parent, Entries: []CloudEntry{}}
	for _, r := range rows {
		var parentID string
		if json.Unmarshal(r.ParentID, &parentID) != nil {
			var n json.Number
			if json.Unmarshal(r.ParentID, &n) == nil {
				parentID = string(n)
			}
		}
		if parentID != parent || !validQuarkFolder(r.FID) || r.FID == "0" || r.FID == parent || r.Name == "" || !relayName(r.Name) || r.FileType == nil || (*r.FileType != 0 && *r.FileType != 1) {
			return FilePage{}, errors.New("夸克来源父目录、ID或类型依据缺失，未开始中转")
		}
		p.Entries = append(p.Entries, CloudEntry{ID: r.FID, Name: r.Name, Folder: r.isDir(), Size: r.Size, revision: string(r.UpdatedAt)})
	}
	return p, nil
}
func relayQuarkDirectoryPage(ctx context.Context, client *http.Client, a Account, parent string) (FilePage, error) {
	if !validQuarkFolder(parent) {
		return FilePage{}, errors.New("夸克目录 ID 无效")
	}
	result := FilePage{CurrentID: parent, Entries: []CloudEntry{}}
	seen := map[string]bool{}
	total := -1
	c := newQuarkClient(client, a)
	for page := 1; page <= 5; page++ {
		query := url.Values{"pdir_fid": {parent}, "_page": {strconv.Itoa(page)}, "_size": {"100"},
			"_fetch_total": {"1"}, "_fetch_sub_dirs": {"0"}, "_sort": {"file_type:asc,file_name:asc"},
			"fetch_all_file": {"1"}, "fetch_risk_file_name": {"1"}}
		var rows struct {
			List []quarkEntry `json:"list"`
		}
		meta, err := c.request(ctx, http.MethodGet, "/file/sort", query, nil, &rows)
		if err != nil {
			return FilePage{}, err
		}
		var count struct {
			Total *int `json:"_total"`
		}
		if json.Unmarshal(meta, &count) != nil || count.Total == nil || *count.Total < 0 ||
			rows.List == nil || len(rows.List) > 100 || total >= 0 && total != *count.Total {
			return FilePage{}, errors.New("夸克目录清单或分页总数无法核对")
		}
		total = *count.Total
		for _, row := range rows.List {
			if !validQuarkFolder(row.FID) || row.FID == "0" || row.FID == parent || seen[row.FID] ||
				shareScalar(row.ParentID) != parent || !relayName(row.Name) ||
				row.FileType == nil || (*row.FileType != 0 && *row.FileType != 1) ||
				(!row.isDir() && (row.Size == nil || *row.Size < 0)) {
				return FilePage{}, errors.New("夸克目录项目的身份、父目录、类型或大小无法核对")
			}
			seen[row.FID] = true
			result.Entries = append(result.Entries, CloudEntry{ID: row.FID, Name: row.Name, Folder: row.isDir(), Size: row.Size, revision: string(row.UpdatedAt)})
		}
		if len(seen) == total {
			return result, nil
		}
		if len(seen) > total || len(rows.List) < 100 {
			return FilePage{}, errors.New("夸克目录列表不完整")
		}
	}
	return FilePage{}, errors.New("夸克目录超过 500 项，无法完整核对")
}

func (m *Manager) RelaySourceFiles(ctx context.Context, accountID, parent string) (FilePage, error) {
	return m.relaySourceFiles(ctx, accountID, parent, false)
}

func (m *Manager) RelaySnapshotSourceFiles(ctx context.Context, accountID, parent string) (FilePage, error) {
	return m.relaySourceFiles(ctx, accountID, parent, true)
}

func (m *Manager) relaySourceFiles(ctx context.Context, accountID, parent string, snapshot bool) (FilePage, error) {
	m.mu.Lock()
	a := m.downloadAccountLocked(accountID)
	originals := append([]UploadFingerprint(nil), m.fingerprints...)
	closing := m.closing
	m.mu.Unlock()
	if closing || a.ID == "" {
		return FilePage{}, errors.New("来源账号不可用")
	}
	if snapshot && !relayUsesOriginalPin(a.Kind) {
		return FilePage{}, errors.New("读取网盘原件模式目前支持夸克和光鸭来源")
	}
	p, e := relaySourcePage(ctx, NewHTTPUploader().Client, a, parent)
	if e != nil {
		return FilePage{}, e
	}
	if relayUsesOriginalPin(a.Kind) {
		eligible := []CloudEntry{}
		for _, r := range p.Entries {
			if r.Folder || snapshot && relaySnapshotEligible(a.Kind, r) ||
				!snapshot && relayPinnedVersion(a.Kind, a.ID, r, originals) != "" {
				eligible = append(eligible, r)
			}
		}
		p.Entries = eligible
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || downloadScope(m.downloadAccountLocked(a.ID)) != downloadScope(a) {
		return FilePage{}, errors.New("来源账号已变化，请重新读取")
	}
	return p, nil
}

// JSON MD5 is advisory. The SHA256 is bound to the account and entry and
// comes from a local upload or a complete read before queue creation.
func relayQuarkContent(ctx context.Context, client *http.Client, a Account, d diskRelay, out io.Writer, progress func(int64)) (string, error) {
	in := DownloadInput{AccountID: a.ID, ParentID: d.Input.ParentID, EntryID: d.Input.EntryID, Name: d.Input.Name}
	row, resp, _, e := openQuarkDownload(ctx, client, a, in)
	if e != nil {
		return "", e
	}
	defer resp.Body.Close()
	if !relayQuarkMetaMatches(row, d) || resp.StatusCode != http.StatusOK ||
		resp.Uncompressed || resp.Header.Get("Content-Range") != "" {
		return "", errors.New("夸克原件版本与可信指纹不符，未确认内容")
	}
	return relayPinnedBody(ctx, resp, d, filePinLabel(a.Kind), relayPinnedSHA(a.Kind, d.ETag), out, progress)
}
func filePinLabel(kind string) string {
	if kind == "guangya" {
		return "光鸭"
	}
	return "夸克"
}
func relayPinnedBody(ctx context.Context, resp *http.Response, d diskRelay, label, expected string, out io.Writer, progress func(int64)) (string, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", statusError(resp.StatusCode)
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "" && !strings.EqualFold(enc, "identity") {
		return "", errors.New(label + "来源返回压缩表示")
	}
	transportMD5 := ""
	if values := resp.Header.Values("Content-MD5"); len(values) > 0 {
		b, e := base64.StdEncoding.DecodeString(strings.TrimSpace(values[0]))
		if len(values) != 1 || e != nil || len(b) != 16 {
			return "", errors.New(label + "来源Content-MD5格式异常")
		}
		transportMD5 = hex.EncodeToString(b)
	}
	h := md5.New()
	sha, e := relayHash(ctx, io.TeeReader(resp.Body, io.MultiWriter(out, h)), d.Input.Size, progress)
	if e != nil {
		return "", e
	}
	if sha != expected {
		return "", errors.New(label + "实际SHA256与队列记录的源内容不符")
	}
	if transportMD5 != "" && hex.EncodeToString(h.Sum(nil)) != transportMD5 {
		return "", errors.New(label + "来源Content-MD5与实际字节不符")
	}
	return sha, nil
}
func relayQuarkDownload(ctx context.Context, client *http.Client, a Account, d diskRelay, file string, progress func(int64)) (string, error) {
	f, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", e
	}
	sha, e := relayQuarkContent(ctx, client, a, d, f, progress)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return sha, e
}
func relayFinalSource(ctx context.Context, client *http.Client, a Account, d diskRelay, progress func(int64)) error {
	if e := relaySource(ctx, client, a, d); e != nil {
		return e
	}
	if !relayUsesOriginalPin(a.Kind) {
		return nil
	}
	if _, e := relayPinnedContent(ctx, client, a, d, io.Discard, progress); e != nil {
		return e
	}
	return relaySource(ctx, client, a, d)
}
