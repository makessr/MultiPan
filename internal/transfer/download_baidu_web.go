package transfer

import (
	"context"
	"crypto/rc4"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
)

type baiduContentDenied struct{ diagnostic string }

func (e *baiduContentDenied) Error() string {
	return "百度文件节点拒绝下载 [BD-DIAG/0.26.4] HTTP=403 " + e.diagnostic + "；未保存正式文件"
}
func baiduWebSign(key, plain string) (string, error) {
	if len(key) == 0 || len(key) > 256 || len(plain) == 0 || len(plain) > 4096 {
		return "", errors.New("百度网页登录下载签名字段无效")
	}
	c, e := rc4.NewCipher([]byte(key))
	if e != nil {
		return "", errors.New("百度网页登录下载签名字段无效")
	}
	out := make([]byte, len(plain))
	c.XORKeyStream(out, []byte(plain))
	return base64.StdEncoding.EncodeToString(out), nil
}
func (c *baiduClient) webDownloadLink(ctx context.Context, id json.Number) (*url.URL, error) {
	if n, e := id.Int64(); e != nil || n <= 0 {
		return nil, errors.New("百度网页登录下载文件ID无效")
	}
	var template struct {
		Result struct {
			Token     string      `json:"bdstoken"`
			Sign1     string      `json:"sign1"`
			Sign3     string      `json:"sign3"`
			Timestamp json.Number `json:"timestamp"`
		} `json:"result"`
	}
	if e := c.api(ctx, "GET", "gettemplatevariable", url.Values{"fields": {`["bdstoken","sign1","sign3","timestamp"]`}}, nil, &template); e != nil {
		return nil, e
	}
	r := template.Result
	if n, e := r.Timestamp.Int64(); e != nil || n <= 0 {
		return nil, errors.New("百度网页下载未返回有效时间戳 [BD-WEB/0.26.4]")
	}
	if len(r.Token) < 8 || len(r.Token) > 512 {
		return nil, errors.New("百度网页下载未返回有效会话 [BD-WEB/0.26.4]")
	}
	c.token = r.Token
	sign, e := baiduWebSign(r.Sign3, r.Sign1)
	if e != nil {
		return nil, e
	}
	var result struct {
		Links []struct {
			ID  json.Number `json:"fs_id"`
			URL string      `json:"dlink"`
		} `json:"dlink"`
	}
	if e = c.api(ctx, "GET", "download", url.Values{"type": {"dlink"}, "fidlist": {"[" + string(id) + "]"}, "sign": {sign}, "timestamp": {string(r.Timestamp)}}, nil, &result); e != nil {
		return nil, e
	}
	if len(result.Links) != 1 || (result.Links[0].ID != "" && result.Links[0].ID != id) {
		return nil, errors.New("百度网页登录下载未返回唯一匹配文件 [BD-WEB/0.26.4]")
	}
	u, e := safePublicDownloadURL(result.Links[0].URL, nil)
	if e != nil {
		return nil, fmt.Errorf("百度网页登录下载地址无效 [BD-WEB/0.26.4]")
	}
	return u, nil
}
