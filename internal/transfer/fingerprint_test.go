package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordedUploadAndDownloadIntegrity(t *testing.T) {
	for _, mode := range []string{"match", "same-size-corruption", "new-version", "no-original", "association-failed"} {
		t.Run(mode, func(t *testing.T) {
			f := source(t)
			payload, _ := os.ReadFile(f.Path)
			data := append([]byte(nil), payload...)
			revision := "v1"
			put := false
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case "PUT":
					if writer, e := os.OpenFile(f.Path, os.O_WRONLY, 0); e == nil {
						writer.Close()
						t.Error("source was writable during upload")
					}
					body, _ := io.ReadAll(r.Body)
					if !bytes.Equal(body, payload) {
						t.Error("wrong uploaded bytes")
					}
					put = true
					w.WriteHeader(201)
				case "PROPFIND":
					if !put {
						t.Error("resolved cloud identity before completed upload")
					}
					if mode == "association-failed" {
						w.WriteHeader(503)
						return
					}
					w.WriteHeader(207)
					fmt.Fprintf(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/dav/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response><d:response><d:href>/dav/%s</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:getcontentlength>%d</d:getcontentlength><d:getetag>"%s"</d:getetag></d:prop></d:propstat></d:response></d:multistatus>`, url.PathEscape(f.Name), len(data), revision)
				case "GET":
					w.Write(data)
				default:
					t.Error("unexpected method")
				}
			}))
			defer server.Close()
			a := Account{ID: "account-a", Name: "fixture", Kind: "webdav", Endpoint: server.URL + "/dav", Username: "fixture", Secret: "private-fixture"}
			u := &HTTPUploader{server.Client()}
			statePath := filepath.Join(t.TempDir(), "state.bin")
			manager, e := NewManager(statePath, u)
			if e != nil {
				t.Fatal(e)
			}
			defer manager.Close()
			manager.accounts = []Account{a}
			if _, e = manager.Enqueue([]File{f}, []Target{{AccountID: a.ID, Folder: "/"}}); e != nil {
				t.Fatal(e)
			}
			waitFor(t, func() bool {
				tasks := manager.Snapshot().Tasks
				return len(tasks) == 1 && tasks[0].Status != "running" && tasks[0].Status != "queued"
			})
			task := manager.Snapshot().Tasks[0]
			if task.Status != "success" {
				t.Fatalf("upload failed: %s", task.Message)
			}
			digest := sha256.Sum256(payload)
			if task.SourceSHA256 != hex.EncodeToString(digest[:]) {
				t.Fatal("original digest not captured")
			}
			if mode == "association-failed" {
				if len(manager.fingerprints) != 0 || !strings.Contains(task.Message, "关联未确认") {
					t.Fatal("bound failed metadata")
				}
				return
			}
			if len(manager.fingerprints) != 1 {
				t.Fatal("missing cloud binding")
			}
			// Reload actual manager persistence after a complete upload. The
			// source can disappear after upload without affecting later checks.
			manager.Close()
			m, e := NewManager(statePath, u)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.Remove(f.Path); e != nil {
				t.Fatal(e)
			}
			records := m.fingerprints
			if mode == "same-size-corruption" || mode == "new-version" {
				data[0] ^= 1
			}
			if mode == "new-version" {
				revision = "v2"
			}
			if mode == "no-original" {
				records = nil
			}
			dest := filepath.Join(t.TempDir(), "saved.txt")
			result, e := DownloadAccountFile(context.Background(), server.Client(), a, DownloadInput{ParentID: "/", EntryID: "/" + f.Name, Name: f.Name}, dest, nil, records...)
			if mode == "same-size-corruption" {
				if e == nil || !strings.Contains(e.Error(), "SHA256") {
					t.Fatal("accepted content different from original", e)
				}
				if _, e = os.Stat(dest); !os.IsNotExist(e) {
					t.Fatal("failed verification committed file")
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				want := "unverified"
				if mode == "match" {
					want = "verified"
				}
				if result.Verification != want {
					t.Fatalf("verification=%s want=%s", result.Verification, want)
				}
				if mode == "match" && (result.Warning != "" || !strings.Contains(result.Details, "SHA256")) {
					t.Fatal("original match not reported")
				}
			}
			parts, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".multipan-download-*"))
			if len(parts) != 0 {
				t.Fatal("leaked temporary download")
			}
		})
	}
}

func TestOriginalBindingRejectsAmbiguityAndChangedIdentity(t *testing.T) {
	size := int64(3)
	f := File{Name: "file", Size: size}
	entry := CloudEntry{ID: "cloud", Name: f.Name, Size: &size, revision: "v1"}
	d := sha256.Sum256([]byte("abc"))
	digest := hex.EncodeToString(d[:])
	r := bindUploadedOriginal("a", f, digest, []CloudEntry{entry})
	if r == nil || originalFor([]UploadFingerprint{*r}, "a", entry) != digest {
		t.Fatal("valid binding lost")
	}
	if bindUploadedOriginal("a", f, digest, []CloudEntry{entry, entry}) != nil {
		t.Fatal("ambiguous binding")
	}
	for _, mode := range []string{"account", "id", "name", "size", "revision", "missing-revision", "duplicate", "invalid-hash"} {
		t.Run(mode, func(t *testing.T) {
			a := "a"
			e := entry
			records := []UploadFingerprint{*r}
			switch mode {
			case "account":
				a = "b"
			case "id":
				e.ID = "other"
			case "name":
				e.Name = "other"
			case "size":
				n := int64(4)
				e.Size = &n
			case "revision":
				e.revision = "v2"
			case "missing-revision":
				e.revision = ""
			case "duplicate":
				records = append(records, *r)
			case "invalid-hash":
				records[0].SHA256 = "bad"
			}
			if originalFor(records, a, e) != "" {
				t.Fatal("stale or ambiguous original accepted")
			}
		})
	}
}

func TestRecordedUploadCancellationAndSourceLock(t *testing.T) {
	f := source(t)
	u := &HTTPUploader{Client: &http.Client{Timeout: time.Second}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r, e := u.uploadRecorded(ctx, Account{}, f, "/", func(int64) {}); e == nil || r.SHA256 != "" {
		t.Fatal("cancelled original recorded")
	}
	writer, e := os.OpenFile(f.Path, os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer writer.Close()
	if r, e := u.uploadRecorded(context.Background(), Account{}, f, "/", func(int64) {}); e == nil || r.SHA256 != "" {
		t.Fatal("writable source recorded")
	}
}

func TestOriginalAccountIsolationAndSaveRollback(t *testing.T) {
	m, e := NewManager(filepath.Join(t.TempDir(), "state.bin"), nil)
	if e != nil {
		t.Fatal(e)
	}
	input := AccountInput{Name: "fixture", Kind: "webdav", Endpoint: "https://example.com/dav", Username: "user", Secret: "old", Folder: "/"}
	a, e := m.SaveAccount(input)
	if e != nil {
		t.Fatal(e)
	}
	m.putFingerprintLocked(UploadFingerprint{AccountID: a.ID, EntryID: "one"})
	m.putFingerprintLocked(UploadFingerprint{AccountID: "another", EntryID: "two"})
	input.ID = a.ID
	input.Name = "new display"
	input.Secret = ""
	if _, e = m.SaveAccount(input); e != nil || len(m.fingerprints) != 2 {
		t.Fatal("display edit removed originals", e)
	}
	oldPath := m.path
	m.path = t.TempDir()
	input.Secret = "new"
	if _, e = m.SaveAccount(input); e == nil || len(m.fingerprints) != 2 {
		t.Fatal("failed save lost originals")
	}
	m.path = oldPath
	if _, e = m.SaveAccount(input); e != nil || len(m.fingerprints) != 1 || m.fingerprints[0].AccountID != "another" {
		t.Fatal("session change retained old account originals", e)
	}
	m.putFingerprintLocked(UploadFingerprint{AccountID: a.ID, EntryID: "new"})
	if e = m.DeleteAccount(a.ID); e != nil || len(m.fingerprints) != 1 {
		t.Fatal("removed account retained originals", e)
	}
	s, e := readState(oldPath)
	if e != nil || len(s.Fingerprints) != 1 || s.Fingerprints[0].AccountID != "another" {
		t.Fatal("account isolation not persisted", e)
	}
}
