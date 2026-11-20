package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"strings"
	"time"
)

// UploadFingerprint is a local original, not a hash asserted by a provider.
// It is kept in the existing encrypted state, independently of task history.
type UploadFingerprint struct {
	AccountID string `json:"accountId"`
	EntryID   string `json:"entryId"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Revision  string `json:"revision,omitempty"`
	SHA256    string `json:"sha256"`
}

type uploadReceipt struct {
	SHA256      string
	Fingerprint *UploadFingerprint
}

func (u *HTTPUploader) uploadRecorded(ctx context.Context, a Account, f File, folder string, progress func(int64)) (uploadReceipt, error) {
	file, e := openUploadSource(f.Path)
	if e != nil {
		return uploadReceipt{}, e
	}
	defer file.Close()
	if e = unchanged(file, f); e != nil {
		return uploadReceipt{}, e
	}
	hash := sha256.New()
	n, e := io.Copy(hash, ctxReader{ctx, file})
	if e != nil {
		return uploadReceipt{}, errors.New("原件 SHA256 计算中断，未开始上传")
	}
	if n != f.Size {
		return uploadReceipt{}, errors.New("源文件大小已变化，未开始上传")
	}
	if _, e = file.Seek(0, io.SeekStart); e != nil {
		return uploadReceipt{}, errors.New("无法读取源文件")
	}
	ctx = prepareUploadCheckpoint(ctx, a, f, folder, hex.EncodeToString(hash.Sum(nil)))
	if e = u.uploadOpened(ctx, a, f, folder, file, progress); e != nil {
		return uploadReceipt{}, e
	}
	checkpoint(ctx).clear()
	r := uploadReceipt{SHA256: hex.EncodeToString(hash.Sum(nil))}
	if !canRecordUploadFingerprint(ctx, file, f, r.SHA256) {
		return uploadReceipt{}, nil
	}
	// Failure to resolve metadata must not turn a completed upload into a
	// retryable failure: doing so can create duplicates on some providers.
	verify, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p, e := ListAccountFiles(verify, u.Client, a, folder)
	if e == nil {
		if a.Kind == "lanzou" {
			for i := range p.Entries {
				if p.Entries[i].Size == nil && !p.Entries[i].Folder {
					size := f.Size
					p.Entries[i].Size = &size
				}
			}
		}
		r.Fingerprint = bindUploadedOriginal(a.ID, f, r.SHA256, p.Entries)
	}
	return r, nil
}

func bindUploadedOriginal(account string, f File, digest string, entries []CloudEntry) *UploadFingerprint {
	var match *UploadFingerprint
	for _, entry := range entries {
		if entry.Folder || entry.Name != f.Name {
			continue
		}
		// A duplicate same-name entry makes name-based post-upload binding
		// ambiguous even if only one of the entries has the expected size.
		if match != nil || entry.ID == "" || entry.Size == nil || *entry.Size != f.Size {
			return nil
		}
		match = &UploadFingerprint{AccountID: account, EntryID: entry.ID, Name: entry.Name, Size: f.Size, Revision: entry.revision, SHA256: digest}
	}
	return match
}

func originalFor(records []UploadFingerprint, account string, source CloudEntry) string {
	if source.Folder || source.Size == nil {
		return ""
	}
	digest := ""
	for _, r := range records {
		if r.AccountID != account || r.EntryID != source.ID {
			continue
		}
		if digest != "" || r.Name != source.Name || r.Size != *source.Size || r.Revision != source.revision {
			return ""
		}
		b, e := hex.DecodeString(r.SHA256)
		if e != nil || len(b) != sha256.Size {
			return ""
		}
		digest = strings.ToLower(r.SHA256)
	}
	return digest
}

func (m *Manager) putFingerprintLocked(r UploadFingerprint) {
	m.dropFingerprintsLocked(r.AccountID, r.EntryID)
	m.fingerprints = append(m.fingerprints, r)
}

func (m *Manager) dropFingerprintsLocked(account, entry string) {
	next := make([]UploadFingerprint, 0, len(m.fingerprints))
	for _, r := range m.fingerprints {
		if r.AccountID != account || (entry != "" && r.EntryID != entry) {
			next = append(next, r)
		}
	}
	m.fingerprints = next
}
