// Package lanzoulogin hosts an isolated native WebView2, without Wails bindings.
package lanzoulogin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/go-webview2/pkg/edge"

	"golang.org/x/sys/windows"
)

const LoginURL = "https://up.woozooo.com/account.php?action=login"
const CookieURI = "https://up.woozooo.com/mydisk.php"
const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

type Result struct {
	DeviceID        string `json:"deviceId,omitempty"`
	Cookie          string `json:"cookie,omitempty"`
	UserAgent       string `json:"userAgent,omitempty"`
	Ready           bool   `json:"ready,omitempty"`
	Error           string `json:"error,omitempty"`
	ViewWidth       int32  `json:"viewWidth,omitempty"`
	ViewHeight      int32  `json:"viewHeight,omitempty"`
	ViewVisible     bool   `json:"viewVisible,omitempty"`
	PageLoaded      bool   `json:"pageLoaded,omitempty"`
	LoadingError    string `json:"loadingError,omitempty"`
	DevToolsEnabled bool   `json:"devToolsEnabled,omitempty"`
}

var user32 = windows.NewLazySystemDLL("user32.dll")
var registerClass = user32.NewProc("RegisterClassExW")
var createWindow = user32.NewProc("CreateWindowExW")
var defWindowProc = user32.NewProc("DefWindowProcW")
var destroyWindow = user32.NewProc("DestroyWindow")
var postMessage = user32.NewProc("PostMessageW")
var getMessage = user32.NewProc("GetMessageW")
var translateMessage = user32.NewProc("TranslateMessage")
var dispatchMessage = user32.NewProc("DispatchMessageW")
var postQuit = user32.NewProc("PostQuitMessage")
var setTimer = user32.NewProc("SetTimer")
var showWindow = user32.NewProc("ShowWindow")
var setWindowText = user32.NewProc("SetWindowTextW")

type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type message struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}

func str(s string) *uint16 { p, _ := windows.UTF16PtrFromString(s); return p }

type cookieCallback struct {
	vtbl *[4]uintptr
	done func(uintptr, *edge.ICoreWebView2CookieList)
}

var cookieCallbackVtbl = [4]uintptr{
	syscall.NewCallback(func(this *cookieCallback, iid *windows.GUID, out *unsafe.Pointer) uintptr {
		if out == nil {
			return 0x80004003
		}
		*out = nil
		if iid != nil && *iid == (windows.GUID{Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}) {
			*out = unsafe.Pointer(this)
			return 0
		}
		return 0x80004002
	}),
	syscall.NewCallback(func(*cookieCallback) uintptr { return 1 }),
	syscall.NewCallback(func(*cookieCallback) uintptr { return 1 }),
	syscall.NewCallback(func(this *cookieCallback, code uintptr, list *edge.ICoreWebView2CookieList) uintptr {
		this.done(code, list)
		return 0
	}),
}

func getCookies(manager *edge.ICoreWebView2CookieManager, uri string, handler *cookieCallback) error {
	text, e := windows.UTF16PtrFromString(uri)
	if e != nil {
		return e
	}
	vtable := *(*[12]uintptr)(*(*unsafe.Pointer)(unsafe.Pointer(manager)))
	hr, _, _ := syscall.SyscallN(vtable[5], uintptr(unsafe.Pointer(manager)), uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(handler)))
	if hr != 0 {
		return syscall.Errno(hr)
	}
	return nil
}

// Run is used only by the isolated --lanzou-login child process.
func Run(profile string, onReady func(func(), Result)) (Result, error) {
	return runWithReady(profile, LoginURL, CookieURI, onReady)
}
func RunQuark(profile string, onReady func(func(), Result)) (Result, error) {
	return runBrowser(profile, "https://pan.quark.cn/", "https://pan.quark.cn/", "夸克网盘", validQuarkSession, onReady)
}
func Run123(profile string, onReady func(func(), Result)) (Result, error) {
	return runBrowser(profile, "https://yun.123pan.cn/", "", "123 云盘", func(string) bool { return false }, onReady, "api.123278.com", "api.123panapi.com", "api.123panapi.cn", "yun.123pan.com", "yun.123pan.cn")
}
func RunGuangya(profile string, onReady func(func(), Result)) (Result, error) {
	return runBrowser(profile, "https://www.guangyapan.com/", "", "光鸭云盘", func(string) bool { return false }, onReady, "api.guangyapan.com")
}
func RunBaidu(profile string, onReady func(func(), Result)) (Result, error) {
	return runBrowser(profile, "https://pan.baidu.com/login", "https://pan.baidu.com/disk/main", "百度网盘", validBaiduSession, onReady)
}
// RunCtfile opens the 诚通 web client and returns its bearer token. The retired
// OpenAPI login (HTTP 410) cannot be replaced by cookies: the web front-end
// authenticates API requests with the token kept in sessionStorage.
func RunCtfile(profile string, onReady func(func(), Result)) (Result, error) {
	return runBrowser(profile, "https://my.ctfile.com/login", "", "诚通网盘", validCtfileSession, onReady, "api.ctfile.com")
}
func validCtfileSession(token string) bool {
	token = strings.TrimSpace(token)
	if len(token) < 24 || len(token) > 8192 {
		return false
	}
	return !strings.ContainsAny(token, " \t\r\n")
}
func validBaiduSession(header string) bool {
	req := http.Request{Header: http.Header{"Cookie": {header}}}
	for _, c := range req.Cookies() {
		if c.Name == "BDUSS" && c.Value != "" {
			return true
		}
	}
	return false
}
func run(profile, startURL, cookieURI string) (Result, error) {
	return runWithReady(profile, startURL, cookieURI, nil)
}
func runWithReady(profile, startURL, cookieURI string, onReady func(func(), Result)) (Result, error) {
	return runBrowser(profile, startURL, cookieURI, "蓝奏云", validSession, onReady)
}
func runBrowser(profile, startURL, cookieURI, cloudName string, valid func(string) bool, onReady func(func(), Result), tokenHosts ...string) (Result, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// S_FALSE means COM was already initialized in this apartment (e.g. by Wails).
	// It is successful and still requires a balancing CoUninitialize.
	if e := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); e != nil && e != syscall.Errno(1) {
		return Result{}, e
	}
	defer windows.CoUninitialize()
	browser := edge.NewChromium()
	browser.DataPath = profile
	browser.AdditionalBrowserArgs = []string{"--inprivate"}
	var hwnd uintptr
	var result Result
	var runErr error
	var ready, pending, finished bool
	var statusLabel uintptr
	var pageLoaded bool
	loadStarted := time.Now()
	setStatus := func(text string) {
		if statusLabel != 0 {
			setWindowText.Call(statusLabel, uintptr(unsafe.Pointer(str(text))))
		}
	}
	var handler *cookieCallback
	finish := func() {
		if !finished {
			finished = true
			postMessage.Call(hwnd, 0x0010, 0, 0)
		}
	}
	if len(tokenHosts) > 0 {
		browser.WebResourceRequestedCallback = func(request *edge.ICoreWebView2WebResourceRequest, _ *edge.ICoreWebView2WebResourceRequestedEventArgs) {
			if finished {
				return
			}
			address, e := request.GetUri()
			u, parseErr := url.Parse(address)
			if e != nil || parseErr != nil || !allowedTokenRequest(u, tokenHosts) {
				return
			}
			headers, e := request.GetHeaders()
			if e != nil {
				return
			}
			defer headers.Release()
			auth, e := headers.GetHeader("Authorization")
			if e != nil || !strings.HasPrefix(auth, "Bearer ") {
				return
			}
			token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
			if len(token) < 16 || len(token) > 32768 || strings.ContainsAny(token, "\r\n\t ") {
				return
			}
			device, _ := headers.GetHeader("Did")
			if len(device) > 128 || strings.ContainsAny(device, "\r\n") {
				return
			}
			result = Result{Cookie: token, UserAgent: UserAgent, DeviceID: device}
			finish()
		}
	}
	browser.SetErrorCallback(func(error) {
		runErr = errors.New("登录窗口无法加载，请检查 WebView2 和网络后重试")
		finish()
	})
	callback := &cookieCallback{vtbl: &cookieCallbackVtbl, done: func(code uintptr, list *edge.ICoreWebView2CookieList) {
		pending = false
		if finished || code != 0 || list == nil {
			return
		}
		// The callback receives the cookie list only for the requested login URI.
		cookies := list
		count, e := cookies.GetCount()
		if e != nil || count > 512 {
			return
		}
		var pairs []string
		for i := uint32(0); i < count; i++ {
			cookie, e := cookies.GetItem(i)
			if e != nil {
				continue
			}
			name, en := cookieString(cookie, 3)
			value, ev := cookieString(cookie, 4)
			cookie.Release()
			if en == nil && ev == nil {
				pairs = append(pairs, name+"="+value)
			}
		}
		header := strings.Join(pairs, "; ")
		if valid(header) {
			result = Result{Cookie: header, UserAgent: UserAgent}
			finish()
		}
	}}
	handler = callback
	poll := func() {
		if len(tokenHosts) > 0 {
			return
		}
		if !ready || pending || finished {
			return
		}
		manager, e := browser.GetCookieManager()
		if e != nil {
			return
		}
		defer manager.Release()
		pending = true
		e = getCookies(manager, cookieURI, handler)
		if e != nil {
			pending = false
		}
	}
	proc := syscall.NewCallback(func(h uintptr, msg uint32, w, l uintptr) uintptr {
		switch msg {
		case 0x0005:
			if ready {
				browser.Resize()
				user32.NewProc("MoveWindow").Call(statusLabel, 8, 4, l&0xffff, 28, 1)
			}
			return 0
		case 0x0113:
			if !pageLoaded && time.Since(loadStarted) > 30*time.Second {
				setStatus("页面加载较慢；可点击菜单「刷新页面」或按 F12 查看开发者工具。")
			}
			poll()
			return 0
		case 0x0111:
			if ready {
				switch w & 0xffff {
				case 1001:
					pageLoaded = false
					loadStarted = time.Now()
					setStatus("正在加载" + cloudName + "官方登录页面…")
					browser.Navigate(startURL)
				case 1002:
					browser.OpenDevToolsWindow()
				}
			}
			return 0
		case 0x0010:
			finished = true
			browser.ShuttingDown()
			if ready {
				controller := browser.GetController()
				if controller != nil {
					table := *(*[26]uintptr)(*(*unsafe.Pointer)(unsafe.Pointer(controller)))
					syscall.SyscallN(table[24], uintptr(unsafe.Pointer(controller)))
				}
			}
			destroyWindow.Call(h)
			return 0
		case 0x0002:
			postQuit.Call(0)
			return 0
		}
		r, _, _ := defWindowProc.Call(h, uintptr(msg), w, l)
		return r
	})
	var instance windows.Handle
	if e := windows.GetModuleHandleEx(0, nil, &instance); e != nil {
		return result, e
	}
	// Load the shared application icon embedded by Wails (IDI_APPLICATION).
	icon, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("LoadIconW").Call(uintptr(instance), 32512)
	wc := windowClass{Icon: icon, SmallIcon: icon, Size: uint32(unsafe.Sizeof(windowClass{})), Proc: proc, Instance: uintptr(instance), Name: str("MultiPanLanzouLogin")}
	if r, _, e := registerClass.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return result, e
	}
	hwnd, _, _ = createWindow.Call(0, uintptr(unsafe.Pointer(wc.Name)), uintptr(unsafe.Pointer(str(cloudName+"登录 · 请在此窗口登录，成功后自动返回 MultiPan"))), 0x00CF0000, 100, 80, 1000, 760, 0, 0, uintptr(instance), 0)
	if hwnd == 0 {
		return result, errors.New("无法创建蓝奏云登录窗口")
	}
	showWindow.Call(hwnd, 5)
	menu, _, _ := user32.NewProc("CreateMenu").Call()
	user32.NewProc("AppendMenuW").Call(menu, 0, 1001, uintptr(unsafe.Pointer(str("刷新页面"))))
	user32.NewProc("AppendMenuW").Call(menu, 0, 1002, uintptr(unsafe.Pointer(str("开发者工具 (F12)"))))
	user32.NewProc("SetMenu").Call(hwnd, menu)
	statusLabel, _, _ = createWindow.Call(0, uintptr(unsafe.Pointer(str("STATIC"))), uintptr(unsafe.Pointer(str("正在加载"+cloudName+"官方登录页面…"))), 0x50000000, 8, 4, 950, 28, hwnd, 0, uintptr(instance), 0)
	browser.NavigationCompletedCallback = func(view *edge.ICoreWebView2, args *edge.ICoreWebView2NavigationCompletedEventArgs) {
		if !ready || finished {
			return
		}
		source, _ := view.GetSource()
		if source == "about:blank" || source == "" {
			return
		}
		var success, status int32
		table := *(*[6]uintptr)(*(*unsafe.Pointer)(unsafe.Pointer(args)))
		syscall.SyscallN(table[3], uintptr(unsafe.Pointer(args)), uintptr(unsafe.Pointer(&success)))
		syscall.SyscallN(table[4], uintptr(unsafe.Pointer(args)), uintptr(unsafe.Pointer(&status)))
		if success == 0 {
			text := fmt.Sprintf("网页加载失败（WebView2 错误 %d）；请检查网络，或使用刷新与开发者工具。", status)
			setStatus(text)
			if onReady != nil {
				onReady(func() { postMessage.Call(hwnd, 0x0010, 0, 0) }, Result{LoadingError: text})
			}
			return
		}
		pageLoaded = true
		setStatus(cloudName + "官方登录页面已加载；请自行完成登录，成功后自动返回 MultiPan。")
		if onReady != nil {
			onReady(func() { postMessage.Call(hwnd, 0x0010, 0, 0) }, Result{PageLoaded: true})
		}
		poll()
	}
	if !browser.Embed(hwnd) {
		destroyWindow.Call(hwnd)
		return result, errors.New("无法初始化 WebView2")
	}
	if finished {
		return result, runErr
	}
	ready = true
	for _, host := range tokenHosts {
		browser.AddWebResourceRequestedFilter("*://"+host+"/*", edge.COREWEBVIEW2_WEB_RESOURCE_CONTEXT_ALL)
	}
	settings, e := browser.GetSettings()
	if e != nil {
		return result, e
	}
	defer settings.Release()
	if e = settings.PutUserAgent(UserAgent); e != nil {
		return result, e
	}
	settings.PutIsWebMessageEnabled(false)
	if e := settings.PutAreDevToolsEnabled(true); e != nil {
		return Result{}, errors.New("无法启用开发者工具")
	}
	browser.AcceleratorKeyCallback = func(key uint) bool {
		if key == 0x7b {
			postMessage.Call(hwnd, 0x0111, 1002, 0)
			return true
		}
		return false
	}
	browser.PutIsPasswordAutosaveEnabled(false)
	browser.PutIsGeneralAutofillEnabled(false)
	browser.SetPadding(edge.Rect{Top: 36})
	browser.Resize()
	browser.Navigate(startURL)
	// STARTF_USESHOWWINDOW/SW_HIDE suppresses the first ShowWindow call even
	// when SW_SHOW was requested. The helper hides its console, never this UI.
	showWindow.Call(hwnd, 5)
	if e := browser.Show(); e != nil {
		return Result{}, errors.New("无法显示登录网页控件")
	}
	if visible, _, _ := user32.NewProc("IsWindowVisible").Call(hwnd); visible == 0 {
		return Result{}, errors.New("登录窗口未能显示")
	}
	if onReady != nil {
		controller := browser.GetController()
		bounds, _ := controller.GetBounds()
		var visible int32
		table := *(*[26]uintptr)(*(*unsafe.Pointer)(unsafe.Pointer(controller)))
		syscall.SyscallN(table[3], uintptr(unsafe.Pointer(controller)), uintptr(unsafe.Pointer(&visible)))
		var devTools int32
		settingsTable := *(*[40]uintptr)(*(*unsafe.Pointer)(unsafe.Pointer(settings)))
		syscall.SyscallN(settingsTable[11], uintptr(unsafe.Pointer(settings)), uintptr(unsafe.Pointer(&devTools)))
		state := Result{Ready: true, ViewVisible: visible != 0, DevToolsEnabled: devTools != 0}
		if bounds != nil {
			state.ViewWidth = bounds.Right - bounds.Left
			state.ViewHeight = bounds.Bottom - bounds.Top
		}
		onReady(func() { postMessage.Call(hwnd, 0x0010, 0, 0) }, state)
	}
	setTimer.Call(hwnd, 1, 1000, 0)
	var msg message
	for {
		r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
	runtime.KeepAlive(handler)
	runtime.KeepAlive(callback)
	runtime.KeepAlive(browser)
	if runErr != nil {
		return Result{}, runErr
	}
	if result.Cookie == "" {
		return Result{}, errors.New("登录已取消，未保存账号")
	}
	return result, nil
}

func allowedTokenRequest(u *url.URL, hosts []string) bool {
	if u == nil || u.User != nil {
		return false
	}
	for _, host := range hosts {
		if u.Host != host {
			continue
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && strings.HasPrefix(host, "127.0.0.1:")) {
			continue
		}
		if strings.Contains(u.Path, "/api/") {
			return true
		}
		if host == "api.guangyapan.com" && strings.HasPrefix(u.Path, "/userres/") {
			return true
		}
		// The 诚通 web client keeps every endpoint under /home/.
		if host == "api.ctfile.com" && strings.HasPrefix(u.Path, "/home/") {
			return true
		}
	}
	return false
}

func validQuarkSession(header string) bool {
	r := http.Request{Header: http.Header{"Cookie": []string{header}}}
	pus, puus := false, false
	for _, c := range r.Cookies() {
		if c.Value == "" {
			continue
		}
		if c.Name == "__pus" {
			pus = true
		}
		if c.Name == "__puus" {
			puus = true
		}
	}
	return pus && puus
}

// COM returns CoTaskMem strings. Free each read, including HttpOnly cookies.
func cookieString(cookie *edge.ICoreWebView2Cookie, index int) (string, error) {
	pointer := unsafe.Pointer(cookie)
	vtable := *(*[16]uintptr)(*(*unsafe.Pointer)(pointer))
	var out *uint16
	hr, _, _ := syscall.SyscallN(vtable[index], uintptr(pointer), uintptr(unsafe.Pointer(&out)))
	if hr != 0 {
		return "", syscall.Errno(hr)
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(out))
	return windows.UTF16PtrToString(out), nil
}
func validSession(header string) bool {
	r := &http.Request{Header: http.Header{"Cookie": []string{header}}}
	for _, c := range r.Cookies() {
		if c.Name == "phpdisk_info" && strings.TrimSpace(c.Value) != "" && !strings.ContainsAny(c.Value, "\r\n") {
			return true
		}
	}
	return false
}

// WriteResult never writes credentials to a file or application log.
func WriteResult(w io.Writer, result Result) error { return json.NewEncoder(w).Encode(result) }
func TrustedURI(s string) bool {
	u, e := url.Parse(s)
	return e == nil && u.Scheme == "https" && u.Host == "up.woozooo.com" && u.User == nil
}
