package transfer

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func sha256Hex(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func hmac256(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}
func s3Escape(s string) string             { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
func s3CanonicalQuery(q url.Values) string { return strings.ReplaceAll(q.Encode(), "+", "%20") }
func s3Path(s string) string {
	parts := strings.Split(s, "/")
	for i := range parts {
		parts[i] = s3Escape(parts[i])
	}
	return strings.Join(parts, "/")
}

// AWS Signature V4, region=123pan, service=s3. Only temporary storage
// credentials are used here; the account bearer token never leaves the API.
func sign123S3(req *http.Request, pre pan123Pre, payloadHash string, now time.Time) {
	stamp := now.UTC().Format("20060102T150405Z")
	day := now.UTC().Format("20060102")
	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("X-Amz-Security-Token", pre.SessionToken)
	headers := "host:" + req.URL.Host + "\nx-amz-content-sha256:" + payloadHash + "\nx-amz-date:" + stamp + "\nx-amz-security-token:" + pre.SessionToken + "\n"
	names := "host;x-amz-content-sha256;x-amz-date;x-amz-security-token"
	canonical := strings.Join([]string{req.Method, s3Path(req.URL.Path), s3CanonicalQuery(req.URL.Query()), headers, names, payloadHash}, "\n")
	scope := day + "/123pan/s3/aws4_request"
	key := hmac256([]byte("AWS4"+pre.SecretKey), day)
	key = hmac256(key, "123pan")
	key = hmac256(key, "s3")
	key = hmac256(key, "aws4_request")
	signature := hex.EncodeToString(hmac256(key, "AWS4-HMAC-SHA256\n"+stamp+"\n"+scope+"\n"+sha256Hex([]byte(canonical))))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+pre.AccessKey+"/"+scope+", SignedHeaders="+names+", Signature="+signature)
}
func (c pan123Client) s3(ctx context.Context, pre pan123Pre, method string, q url.Values, body []byte) ([]byte, string, error) {
	raw := strings.TrimRight(pre.Endpoint, "/")
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	if strings.HasPrefix(raw, "http://") {
		raw = "https://" + strings.TrimPrefix(raw, "http://")
	}
	target, e := pan123StorageURL(raw)
	if e != nil {
		return nil, "", e
	}
	target.Path = strings.TrimRight(target.Path, "/") + "/" + pre.Bucket + "/" + strings.TrimLeft(pre.Key, "/")
	target.RawQuery = s3CanonicalQuery(q)
	req, e := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if e != nil {
		return nil, "", errors.New("123 S3 地址无效")
	}
	sign123S3(req, pre, sha256Hex(body), time.Now())
	resp, e := c.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fmt.Errorf("123 S3 连接失败 [123-S3-CONNECT/0.5.1] host=%s", target.Hostname())
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("123 S3 请求失败 [123-S3-HTTP/0.5.1] host=%s HTTP %d", target.Hostname(), resp.StatusCode)
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if e != nil {
		return nil, "", errors.New("123 S3 响应读取失败")
	}
	return data, resp.Header.Get("ETag"), nil
}
func (c pan123Client) uploadS3(ctx context.Context, pre pan123Pre, file *os.File, size int64, progress func(int64)) error {
	if size <= pan123Chunk {
		data, e := io.ReadAll(io.NewSectionReader(file, 0, size))
		if e != nil {
			return e
		}
		_, _, e = c.s3(ctx, pre, "PUT", nil, data)
		if e == nil {
			progress(size)
		}
		return e
	}
	cp := checkpoint(ctx)
	var data []byte
	var e error
	if cp.MultipartID == "" {
		data, _, e = c.s3(ctx, pre, "POST", url.Values{"uploads": {""}}, nil)
	} else {
		data = []byte("<InitiateMultipartUploadResult><UploadId>" + cp.MultipartID + "</UploadId></InitiateMultipartUploadResult>")
	}
	if e != nil {
		return e
	}
	var initiated struct {
		ID string `xml:"UploadId"`
	}
	if xml.Unmarshal(data, &initiated) != nil || initiated.ID == "" {
		return errors.New("123 S3 分片初始化失败 [123-S3-INIT/0.5.1]")
	}
	cp.MultipartID = initiated.ID
	if e = cp.save(); e != nil {
		return e
	}
	type part struct {
		Number int64  `xml:"PartNumber"`
		ETag   string `xml:"ETag"`
	}
	complete := struct {
		XMLName xml.Name `xml:"CompleteMultipartUpload"`
		Parts   []part   `xml:"Part"`
	}{}
	parts := (size + pan123Chunk - 1) / pan123Chunk
	if parts > 10000 {
		return errors.New("123 S3 分片数量超出限制")
	}
	for n := int64(1); n <= parts; n++ {
		if e = ctx.Err(); e != nil {
			return e
		}
		offset := (n - 1) * pan123Chunk
		if etag, ok := cp.Parts[int(n)]; ok {
			complete.Parts = append(complete.Parts, part{n, etag})
			progress(min(offset+pan123Chunk, size))
			continue
		}
		length := min(pan123Chunk, size-offset)
		data, e = io.ReadAll(io.NewSectionReader(file, offset, length))
		if e != nil {
			return e
		}
		_, etag, err := c.s3(ctx, pre, "PUT", url.Values{"uploadId": {initiated.ID}, "partNumber": {strconv.FormatInt(n, 10)}}, data)
		if err != nil {
			return err
		}
		if etag == "" {
			return errors.New("123 S3 未确认分片 [123-S3-ETAG/0.5.1]")
		}
		complete.Parts = append(complete.Parts, part{n, etag})
		cp.Parts[int(n)] = etag
		if e = cp.save(); e != nil {
			return e
		}
		progress(offset + length)
	}
	body, e := xml.Marshal(complete)
	if e != nil {
		return e
	}
	data, _, e = c.s3(ctx, pre, "POST", url.Values{"uploadId": {initiated.ID}}, body)
	if e != nil {
		return e
	}
	var result struct {
		XMLName xml.Name
		ETag    string `xml:"ETag"`
	}
	if xml.Unmarshal(data, &result) != nil || result.XMLName.Local != "CompleteMultipartUploadResult" || result.ETag == "" {
		return errors.New("123 S3 合并未确认 [123-S3-COMPLETE/0.5.1]")
	}
	return nil
}
