package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Verify an authenticated read before treating a captured browser session as logged in.
func VerifyLanzouSession(ctx context.Context, client *http.Client, a Account) error {
	values := url.Values{"task": {"47"}, "folder_id": {"-1"}}
	req, e := http.NewRequestWithContext(ctx, "POST", a.Endpoint+"/doupload.php", strings.NewReader(values.Encode()))
	if e != nil {
		return errors.New("登录验证地址无效")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", a.Secret)
	req.Header.Set("User-Agent", a.UserAgent)
	req.Header.Set("Referer", a.Endpoint+"/mydisk.php")
	resp, e := client.Do(req)
	if e != nil {
		return errors.New("无法验证蓝奏云登录，请检查网络后重新登录")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("蓝奏云尚未接受登录，请重新登录")
	}
	var result struct {
		ZT json.RawMessage `json:"zt"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil {
		return errors.New("登录验证未通过，请在登录窗口完成安全验证后重试")
	}
	if code := strings.Trim(string(result.ZT), "\""); code != "1" && code != "2" {
		return errors.New("蓝奏云登录验证未通过，请重新登录")
	}
	return nil
}
