package transfer

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type quarkTransport func(*http.Request) (*http.Response, error)

func (f quarkTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if e := r.Context().Err(); e != nil {
		return nil, e
	}
	return f(r)
}
func quarkResponse(body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = http.Header{}
	}
	return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(strings.NewReader(body))}
}
func quarkData(data any) string {
	b, _ := json.Marshal(map[string]any{"status": 200, "code": 0, "data": data})
	return string(b)
}
func TestQuarkUploadRapidAndMultipart(t *testing.T) {
	for _, rapid := range []bool{true, false} {
		t.Run(strconv.FormatBool(rapid), func(t *testing.T) {
			f := source(t)
			payload, _ := os.ReadFile(f.Path)
			var uploaded []byte
			finished, merged := false, false
			preCalls, partCalls := 0, 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasPrefix(r.URL.Host, "test-bucket.") {
					if r.Header.Get("Cookie") != "" {
						t.Error("account Cookie leaked to OSS")
					}
					if r.Header.Get("Authorization") != "OSS signed" {
						t.Error("missing storage authorization")
					}
					body, _ := io.ReadAll(r.Body)
					if int64(len(body)) != r.ContentLength {
						t.Error("wrong content length")
					}
					if r.Method == "PUT" {
						partCalls++
						if r.URL.Query().Get("partNumber") != strconv.Itoa(partCalls) {
							t.Error("wrong part ordering")
						}
						uploaded = append(uploaded, body...)
						return quarkResponse("", http.Header{"Etag": {fmt.Sprintf("\"part%d\"", partCalls)}}), nil
					}
					var document struct {
						Parts []struct {
							Number int    `xml:"PartNumber"`
							ETag   string `xml:"ETag"`
						} `xml:"Part"`
					}
					if xml.Unmarshal(body, &document) != nil || len(document.Parts) != partCalls {
						t.Error("invalid multipart completion")
					}
					sum := md5.Sum(body)
					if r.Header.Get("Content-MD5") != base64.StdEncoding.EncodeToString(sum[:]) {
						t.Error("wrong completion checksum")
					}
					merged = true
					return quarkResponse("<CompleteMultipartUploadResult/>", nil), nil
				}
				if r.URL.Host != "drive.quark.cn" {
					t.Error("unexpected API host")
				}
				if r.URL.Query().Get("pr") != "ucpro" || r.URL.Query().Get("fr") != "pc" {
					t.Error("missing client parameters")
				}
				var body map[string]any
				if r.Body != nil {
					json.NewDecoder(r.Body).Decode(&body)
				}
				switch r.URL.Path {
				case "/1/clouddrive/file/sort":
					if r.URL.Query().Get("pdir_fid") != "folder123" {
						t.Error("selected folder lost")
					}
					return quarkResponse(quarkData(map[string]any{"list": []any{}}), http.Header{"Set-Cookie": {"__puus=updated; Path=/"}}), nil
				case "/1/clouddrive/file/upload/pre":
					preCalls++
					if !strings.Contains(r.Header.Get("Cookie"), "__puus=updated") {
						t.Error("refreshed Cookie not used")
					}
					if body["pdir_fid"] != "folder123" || body["file_name"] != f.Name {
						t.Error("wrong upload destination")
					}
					return quarkResponse(`{"status":200,"code":0,"data":{"task_id":"task","obj_key":"object","upload_id":"upload","bucket":"test-bucket","upload_url":"http://storage-upload.quark.example","auth_info":"auth","callback":{}},"metadata":{"part_size":7}}`, nil), nil
				case "/1/clouddrive/file/update/hash":
					m, s := md5.Sum(payload), sha1.Sum(payload)
					if body["md5"] != hex.EncodeToString(m[:]) || body["sha1"] != hex.EncodeToString(s[:]) {
						t.Error("wrong hashes")
					}
					return quarkResponse(quarkData(map[string]any{"finish": rapid}), nil), nil
				case "/1/clouddrive/file/upload/auth":
					if body["task_id"] != "task" || body["auth_info"] != "auth" || body["auth_meta"] == "" {
						t.Error("bad signing request")
					}
					return quarkResponse(quarkData(map[string]any{"auth_key": "OSS signed"}), nil), nil
				case "/1/clouddrive/file/upload/finish":
					if !merged {
						t.Error("finish before merge")
					}
					finished = true
					return quarkResponse(quarkData(map[string]any{}), nil), nil
				}
				t.Errorf("unexpected API request %s", r.URL.Path)
				return nil, fmt.Errorf("unexpected route")
			})}
			u := &HTTPUploader{Client: client}
			last := int64(0)
			e := u.Upload(context.Background(), Account{Kind: "quark", Endpoint: QuarkAPI, Secret: "__pus=synthetic; __puus=original"}, f, "folder123", func(n int64) {
				if n < last || n > f.Size {
					t.Error("invalid upload progress")
				}
				last = n
			})
			if e != nil {
				t.Fatal(e)
			}
			if preCalls != 1 || last != f.Size {
				t.Fatal("upload did not complete")
			}
			if rapid {
				if partCalls != 0 || finished {
					t.Fatal("rapid upload should not send file bytes")
				}
			} else if !finished || !merged || !bytes.Equal(uploaded, payload) {
				t.Fatal("multipart payload or finalization missing")
			}
		})
	}
}
func TestQuarkFoldersPagination(t *testing.T) {
	pages := 0
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		pages++
		items := []map[string]any{}
		if pages == 1 {
			for i := 0; i < 200; i++ {
				items = append(items, map[string]any{"fid": fmt.Sprintf("f%d", i), "file_name": fmt.Sprintf("目录%d", i), "dir": true})
			}
		} else {
			items = append(items, map[string]any{"fid": "last", "file_name": "最后一个", "file_type": 0}, map[string]any{"fid": "file", "file_name": "file.txt", "file_type": 1})
		}
		return quarkResponse(quarkData(map[string]any{"list": items}), nil), nil
	})}
	page, e := ListQuarkFolders(context.Background(), client, Account{Kind: "quark", Endpoint: QuarkAPI}, "0")
	if e != nil || len(page.Folders) != 201 || pages != 2 || page.Breadcrumbs[0].ID != "0" {
		t.Fatalf("bad paging: folders=%d pages=%d error=%v", len(page.Folders), pages, e)
	}
}
func TestQuarkConflictAndExpiredLogin(t *testing.T) {
	f := source(t)
	for _, expired := range []bool{true, false} {
		calls := 0
		client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if expired {
				return quarkResponse(`{"status":401,"code":31001,"message":"sensitive-server-message"}`, nil), nil
			}
			return quarkResponse(quarkData(map[string]any{"list": []any{map[string]any{"fid": "same", "file_name": f.Name, "file_type": 1}}}), nil), nil
		})}
		e := (&HTTPUploader{Client: client}).Upload(context.Background(), Account{Kind: "quark", Endpoint: QuarkAPI}, f, "0", func(int64) {})
		if e == nil || calls != 1 || strings.Contains(e.Error(), "sensitive-server-message") {
			t.Fatal("conflict/auth failure should stop before upload and avoid raw server text")
		}
	}
}
func TestQuarkStorageHostValidation(t *testing.T) {
	for _, host := range []string{"https://127.0.0.1", "https://localhost", "https://node.local", "ftp://oss-cn-test.aliyuncs.com", "https://-invalid.example.com", "https://oss-cn-test.aliyuncs.com/path", "https://user:password@oss-cn-test.aliyuncs.com", "https://oss-cn-test.aliyuncs.com/?token=secret"} {
		if _, e := quarkObjectURL(quarkPre{Bucket: "bucket", UploadURL: host, ObjKey: "file"}); e == nil {
			t.Fatalf("accepted untrusted storage endpoint %s", host)
		}
	}
}
func TestQuarkDelegatedNodeAndRedactedDiagnostics(t *testing.T) {
	got, e := quarkObjectURL(quarkPre{Bucket: "bucket", UploadURL: "http://upload-node.storage.example", ObjKey: "object"})
	if e != nil || got != "https://bucket.upload-node.storage.example/object" {
		t.Fatalf("authenticated delegated node rejected: %v", e)
	}
	_, e = quarkObjectURL(quarkPre{Bucket: "bucket", UploadURL: "https://user:password@node.example/path?token=secret", ObjKey: "private-object"})
	if e == nil || !strings.Contains(e.Error(), "QK-NODE-FORMAT/0.4.2") {
		t.Fatal("missing useful diagnosis")
	}
	for _, secret := range []string{"password", "token", "secret", "private-object"} {
		if strings.Contains(e.Error(), secret) {
			t.Fatal("sensitive value in diagnosis")
		}
	}
}
func TestQuarkRejectsCustomAPIOrigin(t *testing.T) {
	f := source(t)
	e := NewHTTPUploader().Upload(context.Background(), Account{Kind: "quark", Endpoint: "https://not-quark.example"}, f, "0", func(int64) {})
	if e == nil || !strings.Contains(e.Error(), "QK-API-ORIGIN") {
		t.Fatal("arbitrary API origin accepted")
	}
}
func TestQuarkStorageEndpointNormalization(t *testing.T) {
	for _, node := range []string{"http://oss-cn-test.aliyuncs.com", "https://oss-cn-test.aliyuncs.com/", "oss-cn-test.aliyuncs.com", "//oss-cn-test.aliyuncs.com", "https://bucket.oss-cn-test.aliyuncs.com", " http://oss-cn-test.aliyuncs.com/ "} {
		t.Run(node, func(t *testing.T) {
			got, e := quarkObjectURL(quarkPre{Bucket: "bucket", UploadURL: node, ObjKey: "目录/file #1.zip"})
			if e != nil || got != "https://bucket.oss-cn-test.aliyuncs.com/%E7%9B%AE%E5%BD%95/file%20%231.zip" {
				t.Fatalf("normalization failed: %q %v", got, e)
			}
		})
	}
}
func TestQuarkAccountRootAndFolderPersistence(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.enc")
	m, e := NewManager(state, nil)
	if e != nil {
		t.Fatal(e)
	}
	a, e := m.SaveAccount(AccountInput{Name: "test quark", Kind: "quark", Endpoint: "https://not-trusted.example", Secret: "__pus=synthetic; __puus=synthetic"})
	if e != nil {
		t.Fatal(e)
	}
	if a.Folder != "0" || a.Endpoint != QuarkAPI {
		t.Fatal("quark defaults are wrong")
	}
	if e = m.SetDefaultFolder(a.ID, "ab12cd34", "根目录 / 资料"); e != nil {
		t.Fatal(e)
	}
	restored, e := NewManager(state, nil)
	if e != nil || restored.Snapshot().Accounts[0].Folder != "ab12cd34" {
		t.Fatal("opaque Quark folder ID did not persist")
	}
}
