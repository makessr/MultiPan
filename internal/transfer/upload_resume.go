package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

type uploadResumeKey struct{}
type uploadCheckpoint struct {
	Identity    string
	Meta        json.RawMessage
	Parts       map[int]string
	MultipartID string
	Chunk       int64
	Merged      bool
	path        string
}

func withUploadResumePath(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, uploadResumeKey{}, path)
}
func prepareUploadCheckpoint(ctx context.Context, a Account, f File, folder, sha string) context.Context {
	path, _ := ctx.Value(uploadResumeKey{}).(string)
	if path == "" {
		return ctx
	}
	raw, _ := json.Marshal([]any{downloadScope(a), f.Path, f.Name, f.Size, f.Modified, folder, sha})
	sum := sha256.Sum256(raw)
	identity := hex.EncodeToString(sum[:])
	cp := &uploadCheckpoint{Identity: identity, Parts: map[int]string{}, path: path}
	if info, e := os.Stat(path); e == nil && info.Size() < 2<<20 {
		if encrypted, e := os.ReadFile(path); e == nil {
			if plain, e := unprotect(encrypted); e == nil {
				var old uploadCheckpoint
				if json.Unmarshal(plain, &old) == nil && old.Identity == identity {
					cp = &old
					cp.path = path
					if cp.Parts == nil {
						cp.Parts = map[int]string{}
					}
				}
			}
		}
	}
	return context.WithValue(ctx, uploadResumeKey{}, cp)
}
func checkpoint(ctx context.Context) *uploadCheckpoint {
	cp, _ := ctx.Value(uploadResumeKey{}).(*uploadCheckpoint)
	if cp == nil {
		return &uploadCheckpoint{Parts: map[int]string{}}
	}
	return cp
}
func (cp *uploadCheckpoint) save() error {
	if cp == nil || cp.path == "" {
		return nil
	}
	plain, e := json.Marshal(cp)
	if e != nil {
		return e
	}
	encrypted, e := protect(plain)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(cp.path), 0700); e != nil {
		return e
	}
	tmp, e := os.CreateTemp(filepath.Dir(cp.path), "upload-checkpoint-*.tmp")
	if e != nil {
		return e
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, e = tmp.Write(encrypted); e != nil {
		tmp.Close()
		return e
	}
	if e = tmp.Sync(); e != nil {
		tmp.Close()
		return e
	}
	if e = tmp.Close(); e != nil {
		return e
	}
	return os.Rename(name, cp.path)
}
func (cp *uploadCheckpoint) clear() {
	if cp != nil {
		_ = os.Remove(cp.path)
	}
}
