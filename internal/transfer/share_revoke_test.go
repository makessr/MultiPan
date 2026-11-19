package transfer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func revokeFixtureHandle(t *testing.T, a Account) string {
	t.Helper()
	raw, _ := json.Marshal(managedShareHandle{AccountID: a.ID, Scope: downloadScope(a), ProviderID: "private-share", Name: "fixture.zip", URL: "https://pan.quark.cn/s/public"})
	sealed, e := protect(raw)
	if e != nil {
		t.Fatal(e)
	}
	return base64.RawURLEncoding.EncodeToString(sealed)
}

func TestShareRevocationWriteAheadRestartDedupAndReadOnlyRecovery(t *testing.T) {
	for _, mode := range []string{"success", "lost-response", "pending", "preflight-failed", "changed-target", "absent", "checkpoint-failed", "initial-save-failed"} {
		t.Run(mode, func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), "state.enc")
			m, e := NewManager(statePath, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { m.Close() }()
			a := Account{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture-auth", Name: "fixture"}
			m.accounts = []Account{a}
			handle := revokeFixtureHandle(t, a)
			created := &shareOperation{in: ShareInput{AccountID: a.ID}, scope: downloadScope(a), key: "fixture", share: "private-share", created: time.Now(), result: ShareResult{ID: "creation", Name: "fixture.zip", Kind: "quark", State: "ready", URL: "https://pan.quark.cn/s/public", CanCheck: true}}
			created.saved = snapshotShare(created)
			m.shares["creation"] = created
			var mu sync.Mutex
			posts := 0
			present := mode != "absent"
			followFail := false
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("Cookie") != "fixture-auth" {
					t.Error("wrong credential")
				}
				switch r.URL.Path {
				case "/1/clouddrive/share/mypage/detail":
					if r.Method != "GET" || r.URL.Query().Get("_size") != "50" {
						t.Error("unbounded query")
					}
					if mode == "preflight-failed" || followFail {
						w.WriteHeader(503)
						return
					}
					if mode == "checkpoint-failed" {
						m.mu.Lock()
						m.path = t.TempDir()
						m.mu.Unlock()
					}
					if present {
						name := "fixture.zip"
						if mode == "changed-target" {
							name = "other.zip"
						}
						fmt.Fprintf(w, `{"code":0,"data":{"list":[{"share_id":"private-share","title":%q,"share_url":"https://pan.quark.cn/s/public"}]}}`, name)
					} else {
						fmt.Fprint(w, `{"code":0,"data":{"list":[]}}`)
					}
				case "/1/clouddrive/share/delete":
					posts++
					var payload struct {
						IDs []string `json:"share_ids"`
					}
					if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload.IDs) != 1 || payload.IDs[0] != "private-share" {
						t.Error("wrong revoke target")
					}
					disk, err := readState(statePath)
					if err != nil || len(disk.ShareRevokes) != 1 || !disk.ShareRevokes[0].Submitted {
						t.Error("remote write preceded durable intent")
					}
					if mode == "pending" {
						followFail = true
					} else {
						present = false
					}
					if mode == "lost-response" {
						w.WriteHeader(503)
						return
					}
					fmt.Fprint(w, `{"code":0}`)
				default:
					t.Error("unexpected cloud mutation", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			transport.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			old := http.DefaultTransport
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = old }()
			if mode == "initial-save-failed" {
				m.path = t.TempDir()
			}
			r, err := m.RevokeManagedShare(context.Background(), handle)
			mu.Lock()
			count := posts
			mu.Unlock()
			if mode == "initial-save-failed" {
				if err == nil || count != 0 || len(m.shareRevokes) != 0 {
					t.Fatal("initial save failure submitted revoke", r, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := "confirmed"
			switch mode {
			case "pending":
				expected = "unknown"
			case "preflight-failed", "changed-target", "absent", "checkpoint-failed":
				expected = "not_sent"
			}
			if r.State != expected {
				t.Fatal(mode, r)
			}
			wanted := 1
			if expected == "not_sent" {
				wanted = 0
			}
			if count != wanted {
				t.Fatal("unexpected post count", count)
			}
			if len(m.renaming) != 0 {
				t.Fatal("reservation leaked")
			}
			if mode == "checkpoint-failed" {
				m.path = statePath
				m.Close()
			}
			raw, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"private-share", "fixture-auth", "fixture.zip"} {
				if bytes.Contains(raw, []byte(secret)) {
					t.Fatal("plaintext operation data")
				}
			}
			m.Close()
			m, err = NewManager(statePath, nil)
			if err != nil {
				t.Fatal(err)
			}
			again, err := m.RevokeManagedShare(context.Background(), handle)
			if err != nil || again.ID != r.ID {
				t.Fatal("receipt not reused", again, err)
			}
			if expected == "unknown" {
				if _, e := m.ManagedShareLink(context.Background(), handle); e == nil {
					t.Fatal("pending revoke copied old link")
				}
				if m.ShareHistory()[0].Result.URL != "" {
					t.Fatal("pending revoke exposed local cached link")
				}
				if n, e := m.ClearShareHistory(); n != 0 || e != nil {
					t.Fatal("pending creation receipt discarded")
				}
				if n, e := m.ClearShareRevocations(a.ID); n != 0 || e != nil {
					t.Fatal("uncertain revoke discarded")
				}
				mu.Lock()
				present = false
				followFail = false
				mu.Unlock()
				checked, e := m.CheckShareRevocation(context.Background(), r.ID)
				if e != nil || checked.State != "confirmed" {
					t.Fatal("original result not recovered", checked, e)
				}
				expected = "confirmed"
			}
			if expected == "confirmed" {
				if m.ShareHistory()[0].Result.URL != "" {
					t.Fatal("revoked cached link exposed")
				}
				m.accounts[0].Secret = "changed"
				if _, e := m.CheckShareRevocation(context.Background(), r.ID); e == nil {
					t.Fatal("changed source reused revoke")
				}
				m.accounts[0] = a
			}
			m.path = t.TempDir()
			if n, e := m.ClearShareRevocations(a.ID); e == nil || n != 0 || len(m.shareRevokes) != 1 {
				t.Fatal("failed clear lost receipt")
			}
			m.path = statePath
			if n, e := m.ClearShareRevocations(a.ID); e != nil || n != 1 {
				t.Fatal("confirmed clear failed", n, e)
			}
			if expected == "confirmed" && m.ShareHistory()[0].Result.URL != "" {
				t.Fatal("clear resurrected old link")
			}
			m.Close()
			m, err = NewManager(statePath, nil)
			if err != nil {
				t.Fatal(err)
			}
			if expected == "confirmed" {
				old, e := m.CheckShare(context.Background(), "creation")
				if e != nil || old.State != "failed" || old.CanCheck || old.URL != "" {
					t.Fatal("restart resurrected cleared revoke", old, e)
				}
			}
			mu.Lock()
			after := posts
			mu.Unlock()
			if after != wanted {
				t.Fatal("replayed revoke", after, wanted)
			}
		})
	}
}

func TestShareRevocationRestorePreservesSubmitBoundaryAndCorruptState(t *testing.T) {
	base := diskShareRevocation{Source: managedShareHandle{AccountID: "a", Scope: "scope", ProviderID: "private", Name: "fixture"}, Result: ShareRevocationResult{ID: "id", State: "working", Created: time.Now()}}
	for _, submitted := range []bool{false, true} {
		d := base
		d.Submitted = submitted
		m := &Manager{}
		if e := m.restoreShareRevocations([]diskShareRevocation{d}); e != nil {
			t.Fatal(e)
		}
		r := m.shareRevokes["id"].saved.Result
		if submitted && (r.State != "unknown" || !r.CanCheck) || !submitted && (r.State != "not_sent" || r.CanCheck) {
			t.Fatal("wrong recovery boundary", r)
		}
	}
	for _, rows := range [][]diskShareRevocation{{base, base}, {{}}, {func() diskShareRevocation { d := base; d.Result.State = "confirmed"; return d }()}} {
		m := &Manager{}
		if e := m.restoreShareRevocations(rows); e == nil {
			t.Fatal("corrupt receipt accepted")
		}
	}
}

func TestShareRevocationShutdownCancelsWithoutReplay(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.enc")
	m, e := NewManager(statePath, nil)
	if e != nil {
		t.Fatal(e)
	}
	a := Account{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture-auth", Name: "fixture"}
	m.accounts = []Account{a}
	handle := revokeFixtureHandle(t, a)
	started := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/1/clouddrive/share/delete" {
			io.Copy(io.Discard, r.Body)
			started <- struct{}{}
			<-r.Context().Done()
			return
		}
		if r.URL.Path != "/1/clouddrive/share/mypage/detail" {
			t.Error("unexpected path")
		}
		fmt.Fprint(w, `{"code":0,"data":{"list":[{"share_id":"private-share","title":"fixture.zip","share_url":"https://pan.quark.cn/s/public"}]}}`)
	}))
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	transport.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
	old := http.DefaultTransport
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = old }()
	done := make(chan ShareRevocationResult, 1)
	go func() { r, _ := m.RevokeManagedShare(context.Background(), handle); done <- r }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("revoke did not start")
	}
	m.Close()
	select {
	case r := <-done:
		if r.State != "unknown" {
			t.Fatal("shutdown claimed a definitive result", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not cancel revoke")
	}
	restarted, e := NewManager(statePath, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	r, e := restarted.RevokeManagedShare(context.Background(), handle)
	if e != nil || r.State != "unknown" || !r.CanCheck || len(restarted.renaming) != 0 {
		t.Fatal("restart discarded original revoke", r, e)
	}
	select {
	case <-started:
		t.Fatal("shutdown recovery replayed revoke")
	default:
	}
}

func TestShareRevocationBoundedScanNeverTreatsTruncationAsAbsence(t *testing.T) {
	requests := 0
	a := Account{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture"}
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		var rows []map[string]any
		for i := 0; i < 50; i++ {
			rows = append(rows, map[string]any{"share_id": fmt.Sprintf("other-%d-%d", requests, i)})
		}
		raw, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"list": rows}})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}
	if present, e := quarkObserveShare(context.Background(), client, a, managedShareHandle{ProviderID: "not-there"}); e == nil || present || requests != 10 {
		t.Fatal("truncated scan reported absence", requests, e)
	}
}

func TestShareRevocationBatchReadsEachSourcePageOnce(t *testing.T) {
	a := Account{ID: "fixture", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture"}
	sources := []managedShareHandle{
		{AccountID: a.ID, Scope: downloadScope(a), Kind: a.Kind, ProviderID: "one", Name: "同名", Page: 1},
		{AccountID: a.ID, Scope: downloadScope(a), Kind: a.Kind, ProviderID: "two", Name: "同名", Page: 2},
		{AccountID: a.ID, Scope: downloadScope(a), Kind: a.Kind, ProviderID: "three", Name: "另一项", Page: 2},
	}
	for _, stale := range []bool{false, true} {
		var pages []string
		client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet || r.URL.Path != "/1/clouddrive/share/mypage/detail" {
				t.Fatal("preflight must only list shares", r.Method, r.URL)
			}
			page := r.URL.Query().Get("_page")
			pages = append(pages, page)
			var rows []map[string]string
			for _, source := range sources {
				if fmt.Sprint(source.Page) == page {
					name := source.Name
					if stale && source.ProviderID == "three" {
						name = "已改名"
					}
					rows = append(rows, map[string]string{"share_id": source.ProviderID, "title": name})
				}
			}
			raw, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"list": rows}})
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
		})}
		err := validateShareRevocationPages(context.Background(), client, a, sources)
		if (err != nil) != stale || strings.Join(pages, ",") != "1,2" {
			t.Fatalf("stale=%v, pages=%v, err=%v", stale, pages, err)
		}
	}
}

func TestShareRevocationResumeUsesIdentityAfterPageShift(t *testing.T) {
	a := Account{ID: "fixture", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture"}
	source := managedShareHandle{
		AccountID: a.ID, Scope: downloadScope(a), Kind: a.Kind, ProviderID: "original",
		Name: "同名", URL: "https://pan.quark.cn/s/original", Page: 2, QuarkFilter: "valid",
	}
	for _, mode := range []string{"moved", "missing", "changed"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.Path != "/1/clouddrive/share/mypage/detail" ||
					r.URL.Query().Get("_page") != "1" || r.URL.Query().Has("_filter") {
					t.Fatal("resume should only recheck the original account list", r.Method, r.URL)
				}
				reads++
				rows := []map[string]string{{"share_id": "other", "title": source.Name, "pwd_id": "other"}}
				if mode != "missing" {
					key := "original"
					if mode == "changed" {
						key = "replacement"
					}
					rows = append(rows, map[string]string{"share_id": "original", "title": source.Name, "pwd_id": key})
				}
				raw, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"list": rows}})
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})}
			err := validateResumedShareRevocations(context.Background(), client, a, []managedShareHandle{source})
			if reads != 1 || (err == nil) != (mode == "moved") {
				t.Fatalf("reads = %d, err = %v", reads, err)
			}
		})
	}
}

func TestShareRevocationResumeUsesCurrent123Cursor(t *testing.T) {
	a := Account{ID: "fixture", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture"}
	source := managedShareHandle{
		AccountID: a.ID, Scope: downloadScope(a), Kind: a.Kind,
		ProviderID: "22", Name: "原分享", Page: 3, Cursor: "old-cursor",
	}
	var cursors []string
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/b/api/share/list" {
			t.Fatal("unexpected request", r.Method, r.URL)
		}
		cursor := r.URL.Query().Get("next")
		cursors = append(cursors, cursor)
		body := `{"code":0,"data":{"InfoList":[{"ShareId":11,"ShareName":"另一项"}],"Next":17}}`
		if cursor == "17" {
			body = `{"code":0,"data":{"InfoList":[{"ShareId":22,"ShareName":"原分享"}],"Next":-1}}`
		} else if cursor != "0" {
			t.Fatal("used stale cursor", cursor)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if err := validateResumedShareRevocations(context.Background(), client, a, []managedShareHandle{source}); err != nil ||
		strings.Join(cursors, ",") != "0,17" {
		t.Fatalf("cursors=%v err=%v", cursors, err)
	}
}

func TestQuarkShareRevocationRejectsInconsistentPages(t *testing.T) {
	a := Account{ID: "a", Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture"}
	for _, tt := range []struct {
		name      string
		lastTotal *int
		duplicate bool
		wantError bool
	}{
		{name: "stable total", lastTotal: new(51)},
		{name: "changed total", lastTotal: new(52), wantError: true},
		{name: "missing total", wantError: true},
		{name: "duplicate across pages", lastTotal: new(51), duplicate: true, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var pages []string
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				page := r.URL.Query().Get("_page")
				pages = append(pages, page)
				rows := make([]map[string]string, 0, 50)
				metadata := map[string]any{}
				switch page {
				case "1":
					metadata["_total"] = 51
					for i := 1; i <= 50; i++ {
						rows = append(rows, map[string]string{"share_id": fmt.Sprint(i)})
					}
				case "2":
					if tt.lastTotal != nil {
						metadata["_total"] = *tt.lastTotal
					}
					id := "51"
					if tt.duplicate {
						id = "50"
					}
					rows = append(rows, map[string]string{"share_id": id})
					if tt.lastTotal != nil && *tt.lastTotal == 52 {
						rows = append(rows, map[string]string{"share_id": "52"})
					}
				default:
					t.Fatalf("unexpected page %s", page)
				}
				raw, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"list": rows}, "metadata": metadata})
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})}
			present, err := quarkObserveShare(context.Background(), client, a, managedShareHandle{ProviderID: "not-there"})
			if present || (err != nil) != tt.wantError || strings.Join(pages, ",") != "1,2" {
				t.Fatalf("pages=%v, present=%v, err=%v", pages, present, err)
			}
		})
	}
}
