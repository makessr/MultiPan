package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBaiduOriginalURLSurvivesEncryptedRestart(t *testing.T) {
	m, err := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if err != nil {
		t.Fatal(err)
	}
	a := Account{ID: "a", Kind: "baidu", Endpoint: BaiduAPI, Secret: "BDUSS=fixture", Name: "fixture", Folder: "/"}
	op := &shareOperation{in: ShareInput{AccountID: "a", ParentID: "/", EntryID: "/fixture", Name: "fixture", Action: "create", Password: "A1b2"}, scope: downloadScope(a), key: "fixture-key", share: "456", baiduSource: "123", baiduURL: "https://pan.baidu.com/s/1fixture", created: time.Now(), result: ShareResult{ID: "fixture-receipt", Kind: "baidu", Name: "fixture", State: "unknown", CanCheck: true}}
	m.mu.Lock()
	m.accounts = []Account{a}
	m.shares = map[string]*shareOperation{op.result.ID: op}
	op.saved = snapshotShare(op)
	err = m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	restored, err := NewManager(m.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	saved := restored.shares[op.result.ID]
	if saved == nil || saved.baiduURL != op.baiduURL || saved.share != op.share {
		t.Fatal("original URL identity lost on restart")
	}
	got, err := resolveBaiduShareURL("fixture", "", saved.baiduURL)
	if err != nil || got != op.baiduURL {
		t.Fatal("restored anchor cannot resolve token", got, err)
	}
}

func TestShareProviderStopsWhenCheckpointCannotSave(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Path != "/1/clouddrive/share" {
			t.Fatal("continued after checkpoint failure", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"task_id":"original-task"}}`))}, nil
	})}
	op := &shareOperation{in: ShareInput{EntryID: "fid", Name: "fixture", Action: "create"}, checkpoint: func() error { return errors.New("fixture disk failure") }}
	r := shareQuark(context.Background(), client, Account{Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture"}, op, false)
	if requests != 1 || r.State != "unknown" || op.task != "original-task" || r.URL != "" {
		t.Fatal("checkpoint failure lost original identity or claimed success", r)
	}
}

func TestLanzouOriginalShareURLSurvivesEncryptedRestart(t *testing.T) {
	m, err := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if err != nil {
		t.Fatal(err)
	}
	a := Account{ID: "a", Kind: "lanzou", Endpoint: lanzouRecycleOrigin, Secret: "fixture"}
	m.accounts = []Account{a}
	op := &shareOperation{
		in:    ShareInput{AccountID: "a", ParentID: "-1", EntryID: "123", Name: "fixture", Action: "password", Password: "New1"},
		scope: downloadScope(a), key: "fixture", created: time.Now(),
		result:    ShareResult{ID: "receipt", Kind: "lanzou", Name: "fixture", State: "working"},
		lanzouURL: "https://example.lanzoub.com/i123",
	}
	m.shares["receipt"] = op
	m.attachShareCheckpoint(op)
	if err = op.saveCheckpoint(); err != nil {
		t.Fatal(err)
	}
	m.Close()
	reopened, err := NewManager(m.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.shares["receipt"]; got == nil || got.lanzouURL != op.lanzouURL {
		t.Fatal("original share URL was lost")
	}
	rows := reopened.ShareHistory()
	if len(rows) != 1 || rows[0].Result.State != "unknown" ||
		rows[0].Result.URL != "" || rows[0].Result.Password != "" || !rows[0].Result.CanCheck {
		t.Fatal("uncertain share exposed its original link or cannot be checked", rows)
	}
}

func TestLanzouPasswordChangeSupersedesOnlyItsOldCopiesBeforeWrite(t *testing.T) {
	m, err := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a := Account{ID: "a", Kind: "lanzou", Endpoint: lanzouRecycleOrigin, Secret: "fixture"}
	m.accounts = []Account{a}
	in := ShareInput{AccountID: a.ID, ParentID: "-1", EntryID: "123", Name: "fixture", Action: "password", Password: "New1"}
	link := "https://example.lanzoub.com/i123"
	makeOp := func(id, action, state string, folder bool) *shareOperation {
		input := in
		input.Action, input.Folder = action, folder
		return &shareOperation{in: input, scope: downloadScope(a), key: id, created: time.Now(),
			result: ShareResult{ID: id, Kind: a.Kind, State: state, URL: link, Password: "Old1", CanCheck: true}}
	}
	op := makeOp("edit", "password", "working", false)
	prior := makeOp("prior", "get", "ready", false)
	other := makeOp("other", "get", "ready", true)
	prior.in.Password = ""
	raw, err := json.Marshal(prior.in)
	if err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256(append([]byte(prior.scope), raw...))
	prior.key = hex.EncodeToString(key[:])
	for _, row := range []*shareOperation{op, prior, other} {
		row.saved = snapshotShare(row)
		m.shares[row.result.ID] = row
	}
	m.attachShareCheckpoint(op)

	writes := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("task") == "23" {
			writes++
			d, err := readState(m.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range d.Shares {
				if row.Result.ID == "prior" && (!row.Superseded || row.Result.URL != "" || row.Result.Password != "") {
					t.Fatal("old link or code remained copyable when the write began")
				}
				if row.Result.ID == "other" && (row.Superseded || row.Result.State != "ready") {
					t.Fatal("a folder with the same ID in its own namespace was changed")
				}
			}
			return nil, errors.New("fixture lost write response")
		}
		return quarkResponse(`{"zt":1,"info":{"is_newd":"https://example.lanzoub.com","f_id":"i123","onof":1,"pwd":"Old1"}}`, nil), nil
	})}

	path := m.path
	m.path = t.TempDir()
	got := shareLanzou(context.Background(), client, a, op, false)
	if got.State != "failed" || writes != 0 || prior.superseded || prior.result.State != "ready" {
		t.Fatal("failed checkpoint changed the previous result or sent a write", got.State, writes)
	}
	m.path = path
	got = shareLanzou(context.Background(), client, a, op, false)
	if got.State != "unknown" || writes != 1 || !prior.superseded || prior.result.CanCheck {
		t.Fatal("an ambiguous edit left the old result usable", got.State, writes)
	}
	m.renaming = map[string]bool{a.ID: true}
	if _, err := m.ShareEntry(context.Background(), prior.in); err == nil || !strings.Contains(err.Error(), "正在修改文件或处理分享") {
		t.Fatal("a new read returned the obsolete receipt instead of attempting to refresh", err)
	}
}

func TestShareCheckpointEncryptedRestartAndClear(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.enc")
	m, err := NewManager(statePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := Account{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture-auth", Name: "fixture"}
	m.accounts = []Account{a}
	in := ShareInput{AccountID: "a", ParentID: "0", EntryID: "fid", Name: "fixture.txt", Action: "create"}
	op := &shareOperation{in: in, scope: downloadScope(a), key: "fixture-key", created: time.Now(), running: true,
		result: ShareResult{ID: "receipt", Kind: "quark", Name: in.Name, State: "working"}}
	m.shares["receipt"] = op
	m.attachShareCheckpoint(op)
	op.task = "fixture-provider-task"
	if err = op.saveCheckpoint(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{a.Secret, op.task, in.Name, op.key} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("state contains plaintext operation data")
		}
	}
	restarted, err := NewManager(statePath, nil) // Simulates an exit before the worker finalizes.
	if err != nil {
		t.Fatal(err)
	}
	r := restarted.ShareHistory()
	if len(r) != 1 || r[0].Result.State != "unknown" || !r[0].Result.CanCheck || r[0].Result.URL != "" || restarted.shares["receipt"].running {
		t.Fatal("in-flight operation did not restore safely", r)
	}
	if restarted.shares["receipt"].task != op.task {
		t.Fatal("original provider identity lost")
	}
	if n, e := restarted.ClearShareHistory(); e != nil || n != 0 {
		t.Fatal("uncertain receipt discarded", n, e)
	}
	op.running = false
	op.result.State = "ready"
	op.result.URL = "https://pan.quark.cn/s/fixture"
	if err = op.saveCheckpoint(); err != nil {
		t.Fatal(err)
	}
	restarted, err = NewManager(statePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.ShareHistory()[0].Result.URL != op.result.URL {
		t.Fatal("ready link lost")
	}
	restarted.accounts[0].Secret = "other-auth"
	if n, e := restarted.ClearShareHistory(); n != 0 || e != nil {
		t.Fatal("changed-source receipt discarded", n, e)
	}
	if _, e := restarted.CheckShare(context.Background(), "receipt"); e == nil {
		t.Fatal("changed-source receipt exposed")
	}
	restarted.accounts[0] = a
	restarted.path = t.TempDir() // Failed atomic replacement must roll back cleanup.
	if n, e := restarted.ClearShareHistory(); e == nil || n != 0 || len(restarted.shares) != 1 {
		t.Fatal("failed clear lost history")
	}
	restarted.path = statePath
	if n, e := restarted.ClearShareHistory(); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	loaded, err := NewManager(statePath, nil)
	if err != nil || len(loaded.ShareHistory()) != 0 {
		t.Fatal("clear not durable", err)
	}
}

func TestShareInitialPersistenceFailureDoesNotSubmit(t *testing.T) {
	m, err := NewManager(filepath.Join(t.TempDir(), "state.enc"), nil)
	if err != nil {
		t.Fatal(err)
	}
	m.path = t.TempDir()
	m.accounts = []Account{{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture", Name: "fixture"}}
	_, err = m.ShareEntry(context.Background(), ShareInput{AccountID: "a", ParentID: "0", EntryID: "fid", Name: "fixture.txt", Action: "create"})
	if err == nil || len(m.shares) != 0 || len(m.renaming) != 0 {
		t.Fatal("write-ahead failure did not stop operation", err)
	}
}

func TestShareRestoreRejectsCorruptReceiptsWithoutOverwrite(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.enc")
	d := diskShare{Input: ShareInput{AccountID: "a"}, Scope: "scope", Key: "key", Created: time.Now(), Result: ShareResult{ID: "id", State: "working"}}
	for _, rows := range [][]diskShare{{d, d}, {{}}, {func() diskShare { v := d; v.Result.State = "invalid"; return v }()}} {
		if err := writeState(statePath, diskState{Schema: 1, Shares: rows}); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(statePath)
		if _, err := NewManager(statePath, nil); err == nil {
			t.Fatal("corrupt receipt accepted")
		}
		after, _ := os.ReadFile(statePath)
		if !bytes.Equal(before, after) {
			t.Fatal("corrupt data overwritten")
		}
	}
}
