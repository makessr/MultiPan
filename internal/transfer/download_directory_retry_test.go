package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func retryFixture(t *testing.T) (*Manager, DownloadInput, BatchDownloadResult) {
	t.Helper()
	in := DownloadInput{AccountID: "a", ParentID: "/", EntryID: "/R", Name: "R"}
	tree := folderFixture()
	plan, e := planDAVDownload(context.Background(), in, func(_ context.Context, id string) (FilePage, error) { return FilePage{Entries: tree[id]}, nil }, nil)
	if e != nil {
		t.Fatal(e)
	}
	result, e := runDirectoryDownload(context.Background(), plan, t.TempDir(), func(_ context.Context, in DownloadInput, dest string, _ func(DownloadProgress)) (DownloadResult, error) {
		if in.Name != "a.txt" {
			return DownloadResult{}, errors.New("fixture first attempt failed")
		}
		if e := os.WriteFile(dest, []byte("original"), 0600); e != nil {
			return DownloadResult{}, e
		}
		return DownloadResult{Path: dest, Bytes: 8, Verification: "unverified", Details: "original saved result"}, nil
	}, nil)
	if e != nil {
		t.Fatal(e)
	}
	state := result.retry
	state.id = "opaque-retry"
	state.input = in
	state.kind = "webdav"
	state.endpoint = "https://dav.example"
	state.username = "fixture-user"
	state.items = append([]BatchDownloadItem(nil), result.Items...)
	m := &Manager{accounts: []Account{{ID: "a", Kind: state.kind, Endpoint: state.endpoint, Username: state.username, Secret: "private-fixture"}}, directoryRetry: state}
	return m, in, result
}

func exclusiveRetryWrite(_ context.Context, in DownloadInput, dest string, _ func(DownloadProgress)) (DownloadResult, error) {
	h, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return DownloadResult{}, e
	}
	_, e = h.Write([]byte(in.Name))
	ce := h.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return DownloadResult{}, e
	}
	return DownloadResult{Path: dest, Verification: "verified"}, nil
}

func TestFolderRetryPreservesSavedAndOriginalIndices(t *testing.T) {
	m, in, first := retryFixture(t)
	var names []string
	var indices []int
	result, e := m.retryDownloadFolder(context.Background(), in, "opaque-retry", func(ctx context.Context, in DownloadInput, dest string, p func(DownloadProgress)) (DownloadResult, error) {
		names = append(names, in.Name)
		return exclusiveRetryWrite(ctx, in, dest, p)
	}, func(event BatchDownloadEvent) {
		if event.Item != nil {
			indices = append(indices, event.Index)
		}
	})
	if e != nil || !reflect.DeepEqual(names, []string{"b.txt", "c.txt"}) || !reflect.DeepEqual(indices, []int{2, 3}) {
		t.Fatalf("retry=%+v names=%v events=%v err=%v", result, names, indices, e)
	}
	if result.Items[0] != first.Items[0] || result.Directory != first.Directory || result.RetryID != "" || m.directoryRetry != nil {
		t.Fatal("saved result changed or completed retry retained")
	}
	b, _ := os.ReadFile(first.Items[0].Result.Path)
	if string(b) != "original" {
		t.Fatal("saved file overwritten")
	}
	for _, item := range result.Items {
		if item.Status != "saved" {
			t.Fatal(item)
		}
	}
	if _, e = m.RetryDownloadFolder(context.Background(), in, "opaque-retry", nil); e == nil {
		t.Fatal("completed retry accepted")
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), "private-fixture") || strings.Contains(string(encoded), "owned") || strings.Contains(string(encoded), "fixture-user") {
		t.Fatal("internal state serialized")
	}
}

func TestFolderRetryCanceledAndResumeAgain(t *testing.T) {
	m, in, first := retryFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	r, e := m.retryDownloadFolder(ctx, in, "opaque-retry", func(context.Context, DownloadInput, string, func(DownloadProgress)) (DownloadResult, error) {
		calls++
		cancel()
		return DownloadResult{}, context.Canceled
	}, nil)
	if e != nil || !r.Cancelled || calls != 1 || r.Items[0] != first.Items[0] || r.Items[1].Status != "cancelled" || r.Items[2].Status != "skipped" || r.RetryID != "opaque-retry" {
		t.Fatalf("bad cancel %+v %v", r, e)
	}
	r, e = m.retryDownloadFolder(context.Background(), in, r.RetryID, exclusiveRetryWrite, nil)
	if e != nil || hasDirectoryPending(r.Items) {
		t.Fatalf("second retry failed %+v %v", r, e)
	}
}

func TestFolderRetryGuardsAndCollision(t *testing.T) {
	for _, mode := range []string{"wrong-token", "changed-source", "deleted-account", "changed-endpoint", "changed-provider", "changed-user", "closing", "missing-directory", "replaced-directory", "replaced-root", "existing-file", "busy", "new-session"} {
		t.Run(mode, func(t *testing.T) {
			m, in, first := retryFixture(t)
			token := "opaque-retry"
			dir := filepath.Join(first.Directory, "sub")
			switch mode {
			case "wrong-token":
				token = "other"
			case "changed-source":
				in.EntryID = "/elsewhere"
			case "deleted-account":
				m.accounts = nil
			case "changed-endpoint":
				m.accounts[0].Endpoint = "https://other.example"
			case "changed-provider":
				m.accounts[0].Kind = "quark"
			case "changed-user":
				m.accounts[0].Username = "other"
			case "closing":
				m.closing = true
			case "missing-directory":
				if e := os.Remove(dir); e != nil {
					t.Fatal(e)
				}
			case "replaced-directory":
				if e := os.Rename(dir, dir+"-old"); e != nil {
					t.Fatal(e)
				}
				if e := os.Mkdir(dir, 0700); e != nil {
					t.Fatal(e)
				}
			case "replaced-root":
				if e := os.Rename(first.Directory, first.Directory+"-old"); e != nil {
					t.Fatal(e)
				}
				if e := os.Mkdir(first.Directory, 0700); e != nil {
					t.Fatal(e)
				}
			case "existing-file":
				if e := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("keep"), 0600); e != nil {
					t.Fatal(e)
				}
			case "busy":
				m.directoryDownloadMu.Lock()
				defer m.directoryDownloadMu.Unlock()
			case "new-session":
				m.directoryRetry = nil
			}
			calls := 0
			r, e := m.retryDownloadFolder(context.Background(), in, token, func(ctx context.Context, in DownloadInput, dest string, p func(DownloadProgress)) (DownloadResult, error) {
				calls++
				return exclusiveRetryWrite(ctx, in, dest, p)
			}, nil)
			if mode == "existing-file" {
				b, _ := os.ReadFile(filepath.Join(dir, "b.txt"))
				if e != nil || string(b) != "keep" || r.Items[1].Status != "failed" || r.Items[2].Status != "saved" || r.RetryID == "" {
					t.Fatalf("collision did not preserve/continue %+v %v", r, e)
				}
			} else if e == nil || calls != 0 {
				t.Fatalf("guard accepted retry calls=%d err=%v", calls, e)
			}
		})
	}
}
