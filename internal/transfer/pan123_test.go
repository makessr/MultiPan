package transfer

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func Test123UploadProtocols(t *testing.T) {
	for _, mode := range []string{"rapid", "rapid-zero", "signed", "multipart", "slice32", "s3", "s3multi", "empty", "completion-error"} {
		t.Run(mode, func(t *testing.T) {
			payload := []byte("123 上传内容\x00\xff")
			if mode == "multipart" || mode == "s3multi" {
				payload = bytes.Repeat([]byte{0x73}, int(pan123Chunk)+19)
			}
			if mode == "empty" {
				payload = nil
			}
			if mode == "slice32" {
				payload = bytes.Repeat([]byte{0x73}, 32<<20+19)
			}
			path := filepath.Join(t.TempDir(), "示例.zip")
			if e := os.WriteFile(path, payload, 0600); e != nil {
				t.Fatal(e)
			}
			info, _ := os.Stat(path)
			f := File{Path: path, Name: "示例.zip", Size: info.Size(), Modified: info.ModTime().UnixMilli()}
			a := Account{Kind: "pan123", Endpoint: Pan123API, Secret: "synthetic-account-bearer-token", Folder: "42"}
			uploaded := []byte{}
			finished := false
			parts := 0
			merged := false
			prepared := false
			rapid := strings.HasPrefix(mode, "rapid")
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.123278.com" {
					if r.URL.Scheme != "https" || r.Header.Get("Cookie") != "" || strings.Contains(r.Header.Get("Authorization"), a.Secret) {
						t.Fatal("storage transport leaked session or used HTTP")
					}
					b, _ := io.ReadAll(r.Body)
					if r.Method == "PUT" {
						uploaded = append(uploaded, b...)
						parts++
						if int64(len(b)) != r.ContentLength {
							t.Error("content length")
						}
					}
					if strings.HasPrefix(mode, "s3") {
						if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=temporary/") || r.Header.Get("X-Amz-Security-Token") != "temporary-session" {
							t.Error("S3 signing headers")
						}
						if r.URL.Path != "/test-bucket/object/key" {
							t.Error("S3 path")
						}
						if r.Method == "POST" && r.URL.Query().Has("uploads") {
							return quarkResponse(`<InitiateMultipartUploadResult><UploadId>multi123</UploadId></InitiateMultipartUploadResult>`, nil), nil
						}
						if r.Method == "POST" && r.URL.Query().Get("uploadId") != "" {
							merged = true
							if !bytes.Contains(b, []byte("<PartNumber>2</PartNumber>")) {
								t.Error("missing last part")
							}
							return quarkResponse(`<CompleteMultipartUploadResult><ETag>whole</ETag></CompleteMultipartUploadResult>`, nil), nil
						}
					} else if r.Header.Get("Authorization") != "" || r.URL.RawQuery != "signed=keep%2Bexact%2Fvalue" {
						t.Error("signed URL changed or account auth leaked")
					}
					return quarkResponse("", http.Header{"Etag": {"\"part-etag\""}}), nil
				}
				if r.Header.Get("Authorization") != "Bearer "+a.Secret || r.Header.Get("platform") != "web" {
					t.Fatal("API auth missing")
				}
				var body map[string]any
				if r.Body != nil {
					json.NewDecoder(r.Body).Decode(&body)
				}
				data := any(map[string]any{})
				switch r.URL.Path {
				case "/b/api/file/list/new":
					if r.URL.Query().Get("parentFileId") != "42" {
						t.Error("folder ignored")
					}
					data = map[string]any{"InfoList": []any{}, "Total": 0, "Next": "-1"}
					if finished || (rapid && prepared) {
						h := md5.Sum(payload)
						data = map[string]any{"InfoList": []any{map[string]any{"FileId": 9223372036854775000, "FileName": f.Name, "Size": f.Size, "Etag": hex.EncodeToString(h[:]), "Type": 0}}, "Total": 1, "Next": "-1"}
					}
				case "/b/api/file/upload_request":
					prepared = true
					h := md5.Sum(payload)
					if body["etag"] != hex.EncodeToString(h[:]) || body["duplicate"] != float64(1) || body["parentFileId"] != float64(42) {
						t.Error("hash, no-overwrite or parent wrong")
					}
					pre := map[string]any{"FileId": 9223372036854775000, "Reuse": rapid, "Bucket": "test-bucket", "Key": "object/key", "UploadId": "pre-upload", "StorageNode": "node"}
					if mode == "rapid-zero" {
						pre["FileId"] = 0
					}
					if mode == "slice32" {
						pre["SliceSize"] = "33554432"
					}
					if strings.HasPrefix(mode, "s3") {
						pre["EndPoint"] = "http://different-storage.example"
						pre["AccessKeyId"] = "temporary"
						pre["SecretAccessKey"] = "temp-secret"
						pre["SessionToken"] = "temporary-session"
					}
					data = pre
				case "/b/api/file/s3_upload_object/auth", "/b/api/file/s3_repare_upload_parts_batch":
					multipart := mode == "multipart" || mode == "slice32"
					if strings.HasSuffix(r.URL.Path, "batch") != multipart {
						t.Error("wrong signing mode")
					}
					n := int(body["partNumberStart"].(float64))
					if body["partNumberEnd"] != float64(n+1) {
						t.Error("exclusive part end")
					}
					data = map[string]any{"presignedUrls": map[string]string{strconv.Itoa(n): "http://delegated-storage.example/object?signed=keep%2Bexact%2Fvalue"}}
				case "/b/api/file/upload_complete/v2", "/b/api/file/upload_complete":
					if mode == "completion-error" {
						return quarkResponse(`{"code":500,"data":{}}`, nil), nil
					}
					finished = true
					if len(uploaded) != len(payload) {
						t.Error("completed before upload")
					}
					if body["fileId"] != float64(9223372036854775000) {
						t.Error("file ID missing")
					}
					h := md5.Sum(payload)
					data = map[string]any{"file_info": map[string]any{"FileId": 9223372036854775000, "ParentFileId": 42, "FileName": f.Name, "Size": f.Size, "Etag": hex.EncodeToString(h[:]), "Type": 0}}
				default:
					t.Fatalf("unexpected API %s", r.URL.Path)
				}
				return quarkResponse(quarkData(data), nil), nil
			})}
			var sent int64
			e := (&HTTPUploader{Client: client}).Upload(context.Background(), a, f, "42", func(n int64) { sent = n })
			if mode == "completion-error" {
				if e == nil {
					t.Fatal("completion failure accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if mode == "slice32" && parts != 2 {
				t.Fatal("server slice size ignored")
			}
			if rapid {
				if finished || parts != 0 {
					t.Fatal("rapid uploaded bytes")
				}
			} else if !finished || !bytes.Equal(uploaded, payload) {
				t.Fatal("upload not finalized or bytes differ")
			}
			if mode == "s3multi" && !merged {
				t.Fatal("multipart was not merged")
			}
			if sent != f.Size {
				t.Fatal("progress")
			}
		})
	}
}
func Test123FailuresAndFolders(t *testing.T) {
	a := Account{Kind: "pan123", Endpoint: Pan123API, Secret: "synthetic-account-bearer-token", Folder: "0"}
	calls := 0
	c := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("Page") == "1" {
			return quarkResponse(quarkData(map[string]any{"InfoList": []any{map[string]any{"FileId": "9223372036854775000", "FileName": "大目录", "Type": 1}, map[string]any{"FileId": 1, "FileName": "file", "Type": 0}}, "Next": "next", "Total": 3}), nil), nil
		}
		return quarkResponse(quarkData(map[string]any{"InfoList": []any{map[string]any{"FileId": 2, "FileName": "目录2", "Type": 1}}, "Next": "-1", "Total": 3}), nil), nil
	})}
	p, e := List123Folders(context.Background(), c, a, "0")
	if e != nil || len(p.Folders) != 2 || p.Folders[0].ID != "9223372036854775000" || calls != 2 {
		t.Fatalf("pagination/ID precision: %v", e)
	}
	c.Transport = quarkTransport(func(r *http.Request) (*http.Response, error) {
		return quarkResponse(`{"code":401,"data":{}}`, nil), nil
	})
	if Verify123Session(context.Background(), c, a) == nil {
		t.Fatal("expired accepted")
	}
	c.Transport = quarkTransport(func(r *http.Request) (*http.Response, error) { return quarkResponse(`{"data":{}}`, nil), nil })
	if Verify123Session(context.Background(), c, a) == nil {
		t.Fatal("missing code accepted")
	}
	c.Transport = quarkTransport(func(r *http.Request) (*http.Response, error) { return nil, fmt.Errorf("private?token=DO-NOT-LEAK") })
	if e = Verify123Session(context.Background(), c, a); e == nil || strings.Contains(e.Error(), "DO-NOT-LEAK") {
		t.Fatal("unsafe network error")
	}
	for _, s := range []string{"https://user:secret@storage.example/a", "https://127.0.0.1/a", "https://foo.local/a", "https://storage.example/a#frag"} {
		if _, e := pan123StorageURL(s); e == nil {
			t.Fatalf("unsafe storage accepted %s", s)
		}
	}
	a.Endpoint = "https://custom.example"
	if Verify123Session(context.Background(), c, a) == nil {
		t.Fatal("custom API accepted")
	}
}
func Test123ConflictAndCompletionFailure(t *testing.T) {
	f := source(t)
	a := Account{Kind: "pan123", Endpoint: Pan123API, Secret: "synthetic-account-bearer-token"}
	c := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/file/list/new") {
			t.Fatal("created upload despite conflict")
		}
		return quarkResponse(quarkData(map[string]any{"InfoList": []any{map[string]any{"FileId": 1, "FileName": f.Name, "Type": 0}}, "Next": "-1"}), nil), nil
	})}
	if e := (&HTTPUploader{c}).Upload(context.Background(), a, f, "0", func(int64) {}); e == nil || !strings.Contains(e.Error(), "同名") {
		t.Fatal("conflict ignored")
	}
}
func Test123AccountEncryptedPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.enc")
	m, e := NewManager(path, NewHTTPUploader())
	if e != nil {
		t.Fatal(e)
	}
	a, e := m.SaveAccount(AccountInput{Kind: "pan123", Name: "123", Secret: "Bearer synthetic-account-bearer-token"})
	if e != nil {
		t.Fatal(e)
	}
	if a.Folder != "0" || a.Secret != "" {
		t.Fatal("root or secret exposure")
	}
	if e = m.SetDefaultFolder(a.ID, "9223372036854775000", "根目录 / 大目录"); e != nil {
		t.Fatal(e)
	}
	reopened, e := NewManager(path, NewHTTPUploader())
	if e != nil {
		t.Fatal(e)
	}
	if reopened.accounts[0].Secret != "synthetic-account-bearer-token" || reopened.accounts[0].Folder != "9223372036854775000" {
		t.Fatal("state mismatch")
	}
	b, _ := os.ReadFile(path)
	if bytes.Contains(b, []byte("synthetic-account")) {
		t.Fatal("plaintext secret")
	}
	m.Close()
	reopened.Close()
}
func Test123ConfirmationRejectsWrongObject(t *testing.T) {
	a := Account{Kind: "pan123", Endpoint: Pan123API, Secret: "synthetic-account-bearer-token"}
	c := pan123Client{&http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		return quarkResponse(quarkData(map[string]any{"InfoList": []any{map[string]any{"FileId": 123, "FileName": "same.zip", "Size": 10, "Etag": "wrong-md5", "Type": 0}}, "Next": "-1"}), nil), nil
	})}, a}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if e := c.confirm(ctx, "0", "123", File{Name: "same.zip", Size: 10}, "expected-md5"); e == nil {
		t.Fatal("wrong content accepted as uploaded")
	}
}
func Test123SignatureTimezone(t *testing.T) {
	instant := time.Unix(1794700000, 0)
	k, v := pan123Signature("/b/api/user/info", instant, "42")
	k2, v2 := pan123Signature("/b/api/user/info", instant.In(time.FixedZone("other", -7*3600)), "42")
	if k != k2 || v != v2 || !strings.HasPrefix(v, "1794700000-42-") {
		t.Fatal("timezone changes signing")
	}
	q := url.Values{"file": {"文件 空格+"}}
	if strings.Contains(s3CanonicalQuery(q), "+") {
		t.Fatal("S3 query escaping")
	}
}
