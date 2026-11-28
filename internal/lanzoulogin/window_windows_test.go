package lanzoulogin

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/windows"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBaiduSessionCookies(t *testing.T) {
	for _, h := range []string{"", "BDUSS_BFESS=other", "notBDUSS=other", "BDUSS=", "STOKEN=only"} {
		if validBaiduSession(h) {
			t.Fatal("incomplete Baidu cookie accepted")
		}
	}
	if !validBaiduSession("BDUSS=synthetic; STOKEN=synthetic") {
		t.Fatal("Baidu session not detected")
	}
}
func TestNativeBaiduCookieCapture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "BDUSS", Value: "synthetic-baidu-session", Path: "/", HttpOnly: true})
		http.SetCookie(w, &http.Cookie{Name: "STOKEN", Value: "synthetic-pan-token", Path: "/", HttpOnly: true})
		w.Write([]byte(`<html><body>Baidu synthetic login</body></html>`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBaiduBrowserHelper$")
	cmd.Env = append(os.Environ(), "MULTIPAN_TEST_BAIDU_URL="+server.URL, "MULTIPAN_TEST_PROFILE="+testProfile(t))
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("Baidu cookie bridge failed: %v", e)
	}
	var result Result
	if json.Unmarshal(out, &result) != nil || !validBaiduSession(result.Cookie) || !strings.Contains(result.Cookie, "STOKEN=synthetic-pan-token") {
		t.Fatal("did not capture Baidu HttpOnly cookies")
	}
}
func TestBaiduBrowserHelper(t *testing.T) {
	u := os.Getenv("MULTIPAN_TEST_BAIDU_URL")
	if u == "" {
		return
	}
	result, e := runBrowser(os.Getenv("MULTIPAN_TEST_PROFILE"), u, u, "百度网盘", validBaiduSession, nil)
	if e != nil {
		os.Exit(2)
	}
	WriteResult(os.Stdout, result)
	os.Exit(0)
}

func TestNative123TokenCapture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`<html><body>123 synthetic login<script>fetch('/api/user/info',{headers:{Authorization:'Bearer synthetic-123-session-token',Did:'synthetic-device-id'}})</script></body></html>`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^Test123BrowserHelper$")
	cmd.Env = append(os.Environ(), "MULTIPAN_TEST_123_URL="+server.URL, "MULTIPAN_TEST_PROFILE="+testProfile(t))
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("native 123 bridge failed: %v timeout=%v", e, ctx.Err())
	}
	var result Result
	if json.Unmarshal(out, &result) != nil || result.Cookie != "synthetic-123-session-token" || result.DeviceID != "synthetic-device-id" {
		t.Fatal("123 token capture failed")
	}
}
func Test123BrowserHelper(t *testing.T) {
	u := os.Getenv("MULTIPAN_TEST_123_URL")
	if u == "" {
		return
	}
	parsed, _ := url.Parse(u)
	result, e := runBrowser(os.Getenv("MULTIPAN_TEST_PROFILE"), u, "", "123 云盘", func(string) bool { return false }, nil, parsed.Host)
	if e != nil {
		os.Exit(2)
	}
	WriteResult(os.Stdout, result)
	os.Exit(0)
}
func Test123CaptureHostBoundary(t *testing.T) {
	for _, raw := range []string{"http://yun.123pan.com/api/user/info", "https://yun.123pan.com.evil.example/api/user/info", "https://yun.123pan.com:443/api/user/info", "https://yun.123pan.com/not-api"} {
		u, _ := url.Parse(raw)
		if allowedTokenRequest(u, []string{"yun.123pan.com"}) {
			t.Fatal("untrusted request accepted")
		}
	}
	u, _ := url.Parse("https://yun.123pan.com/b/api/user/info")
	if !allowedTokenRequest(u, []string{"yun.123pan.com"}) {
		t.Fatal("official API rejected")
	}
}

func TestCtfileCapturePathBoundary(t *testing.T) {
	for _, raw := range []string{"http://api.ctfile.com/home/file/browse/list", "https://api.ctfile.com.evil.example/home/file/browse/list", "https://api.ctfile.com/other/list", "https://api.ctfile.com/homex/list"} {
		u, _ := url.Parse(raw)
		if allowedTokenRequest(u, []string{"api.ctfile.com"}) {
			t.Fatalf("untrusted 诚通 request accepted: %s", raw)
		}
	}
	for _, raw := range []string{"https://api.ctfile.com/home/user/auth/login", "https://api.ctfile.com/home/file/browse/list"} {
		u, _ := url.Parse(raw)
		if !allowedTokenRequest(u, []string{"api.ctfile.com"}) {
			t.Fatalf("official 诚通 request rejected: %s", raw)
		}
	}
}

func TestStorageTokenUnwrapsScriptResult(t *testing.T) {
	quoted, e := json.Marshal(`{"token":"abcdefghijklmnopqrstuvwx"}`)
	if e != nil {
		t.Fatal(e)
	}
	if got := storageToken(string(quoted)); got != "abcdefghijklmnopqrstuvwx" {
		t.Fatalf("storageToken(%s) = %q", quoted, got)
	}
	blank, _ := json.Marshal(`{"token":""}`)
	if got := storageToken(string(blank)); got != "" {
		t.Fatalf("expected empty token, got %q", got)
	}
	if got := storageToken("not json"); got != "" {
		t.Fatalf("expected empty token for invalid payload, got %q", got)
	}
	if got := storageToken(`"plain token"`); got != "" {
		t.Fatalf("expected empty token for payload without JSON body, got %q", got)
	}
}

func TestSessionCookieValidation(t *testing.T) {
	for _, s := range []string{"", "PHPSESSID=abc", "notphpdisk_info=x", "phpdisk_info="} {
		if validSession(s) {
			t.Fatalf("accepted invalid cookie %q", s)
		}
	}
	if !validSession("PHPSESSID=x; phpdisk_info=synthetic") {
		t.Fatal("valid session rejected")
	}
}
func TestQuarkSessionCookies(t *testing.T) {
	for _, h := range []string{"", "__pus=a", "__puus=b", "not__pus=a; __puus=b", "__pus=; __puus=b"} {
		if validQuarkSession(h) {
			t.Fatal("incomplete Quark session accepted")
		}
	}
	if !validQuarkSession("__pus=synthetic; __puus=synthetic") {
		t.Fatal("Quark session not recognized")
	}
}
func TestNativeBrowserHttpOnlyCapture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "phpdisk_info", Value: "synthetic-login-test", Path: "/", HttpOnly: true})
		w.Write([]byte("<html><body>MultiPan local login integration test</body></html>"))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBrowserHelper$")
	cmd.Env = append(os.Environ(), "MULTIPAN_TEST_LOGIN_URL="+server.URL, "MULTIPAN_TEST_PROFILE="+testProfile(t))
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("native login bridge failed: %v (timeout=%v), output=%s", e, ctx.Err(), out)
	}
	var result Result
	if json.Unmarshal(out, &result) != nil || result.Cookie != "phpdisk_info=synthetic-login-test" || result.UserAgent != UserAgent {
		t.Fatal("did not capture expected HttpOnly cookie and user agent")
	}
}
func TestBrowserHelper(t *testing.T) {
	u := os.Getenv("MULTIPAN_TEST_LOGIN_URL")
	if u == "" {
		return
	}
	// Match the real desktop executable: Wails initializes COM before login starts.
	runtime.LockOSThread()
	if e := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); e != nil {
		os.Exit(3)
	}
	defer windows.CoUninitialize()
	result, e := run(os.Getenv("MULTIPAN_TEST_PROFILE"), u, u)
	if e != nil {
		os.Exit(2)
	}
	WriteResult(os.Stdout, result)
	os.Exit(0)
}

func TestNativeLoginWithoutSessionCanBeCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "PHPSESSID", Value: "not-an-auth-session", Path: "/", HttpOnly: true})
		w.Write([]byte("<html><body>Cancellation integration test</body></html>"))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBrowserHelper$")
	cmd.Env = append(os.Environ(), "MULTIPAN_TEST_LOGIN_URL="+server.URL, "MULTIPAN_TEST_PROFILE="+testProfile(t))
	out, err := cmd.Output()
	if err == nil || ctx.Err() == nil || len(out) != 0 {
		t.Fatal("unauthenticated login should remain pending until canceled")
	}
}

func testProfile(t *testing.T) string {
	root := t.TempDir()
	profile := filepath.Join(root, "browser-profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 25; i++ {
			if os.RemoveAll(profile) == nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Error("temporary WebView2 profile still in use")
	})
	return profile
}
