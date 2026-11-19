package transfer

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gyResponse(code int, data any) *http.Response {
	b, _ := json.Marshal(map[string]any{"code": code, "data": data})
	return quarkResponse(string(b), nil)
}
func TestGuangyaUploadProtocol(t *testing.T) {
	for _, mode := range []string{"rapid", "multipart", "empty", "bad-result", "oss-error"} {
		t.Run(mode, func(t *testing.T) {
			payload := bytes.Repeat([]byte{0x67}, 8<<20+17)
			if mode == "empty" {
				payload = nil
			}
			if mode == "rapid" || mode == "bad-result" {
				payload = []byte("光鸭测试")
			}
			p := filepath.Join(t.TempDir(), "样本.zip")
			os.WriteFile(p, payload, 0600)
			info, _ := os.Stat(p)
			f := File{Path: p, Name: "样本.zip", Size: info.Size(), Modified: info.ModTime().UnixMilli()}
			a := Account{Kind: "guangya", Endpoint: GuangyaAPI, Secret: "synthetic-access-token", Username: "synthetic-device", Folder: "root"}
			committed := false
			uploaded := []byte{}
			parts, preCalls := 0, 0
			merged := false
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.guangyapan.com" {
					if r.URL.Host != "test-bucket.storage.example" || r.URL.Scheme != "https" || r.Header.Get("Cookie") != "" || !strings.HasPrefix(r.Header.Get("Authorization"), "OSS temporary-key:") || r.Header.Get("X-Oss-Security-Token") != "temporary-session" {
						t.Fatal("storage boundary or credentials incorrect")
					}
					if strings.Contains(r.Header.Get("Authorization"), a.Secret) {
						t.Fatal("API bearer leaked")
					}
					if mode == "oss-error" {
						response := quarkResponse(`<Error><Code>AccessDenied</Code><Message>private-signature-secret</Message></Error>`, nil)
						response.StatusCode = 403
						return response, nil
					}
					var b []byte
					if r.Body != nil {
						b, _ = io.ReadAll(r.Body)
					}
					if r.Method == "POST" && r.URL.Query().Has("uploads") {
						return quarkResponse(`<InitiateMultipartUploadResult><Bucket>test-bucket</Bucket><Key>key</Key><UploadId>upload123</UploadId></InitiateMultipartUploadResult>`, nil), nil
					}
					if r.Method == "PUT" {
						parts++
						uploaded = append(uploaded, b...)
						if mode == "empty" {
							committed = true
						}
						return quarkResponse("", http.Header{"Etag": {"\"part-etag\""}}), nil
					}
					if r.Method == "POST" && r.URL.Query().Get("uploadId") != "" {
						merged = true
						committed = true
						return quarkResponse(`<CompleteMultipartUploadResult><Bucket>test-bucket</Bucket><Key>key</Key><ETag>whole</ETag></CompleteMultipartUploadResult>`, nil), nil
					}
					t.Fatal("unexpected OSS operation")
				}
				if r.Header.Get("Authorization") != "Bearer "+a.Secret || r.Header.Get("Did") != a.Username {
					t.Fatal("missing API credentials")
				}
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				switch r.URL.Path {
				case "/userres/v1/file/get_file_list":
					if body["parentId"] != "" {
						t.Fatal("root incorrectly encoded")
					}
					items := []any{}
					if committed {
						size := f.Size
						if mode == "bad-result" {
							size++
						}
						items = append(items, map[string]any{"fileId": "final-id", "fileName": f.Name, "fileSize": size, "resType": 1})
					}
					return gyResponse(0, map[string]any{"total": len(items), "list": items}), nil
				case "/userres/v2/get_res_center_token":
					preCalls++
					h := md5.Sum(payload)
					res := body["res"].(map[string]any)
					if body["capacity"] != float64(2) || res["md5"] != hex.EncodeToString(h[:]) || res["fileSize"] != float64(f.Size) {
						t.Fatal("capability or hash incorrect")
					}
					if mode == "rapid" || mode == "bad-result" {
						committed = true
						return gyResponse(156, map[string]any{"taskId": "task-id"}), nil
					}
					return gyResponse(0, map[string]any{"taskId": "task-id", "provider": 1, "bucketName": "test-bucket", "objectPath": "key", "endPoint": "http://test-bucket.storage.example", "creds": map[string]string{"accessKeyID": "temporary-key", "secretAccessKey": "temporary-secret", "sessionToken": "temporary-session"}}), nil
				case "/userres/v1/file/get_info_by_task_id":
					if !committed {
						t.Fatal("queried result before commit")
					}
					return gyResponse(0, map[string]any{"fileId": "final-id"}), nil
				}
				t.Fatalf("unexpected API %s", r.URL.Path)
				return nil, nil
			})}
			var sent int64
			e := (&HTTPUploader{client}).Upload(context.Background(), a, f, "root", func(n int64) { sent = n })
			if mode == "bad-result" || mode == "oss-error" {
				if e == nil || strings.Contains(e.Error(), "private-signature-secret") {
					t.Fatal("invalid result accepted or error leaked")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if preCalls != 1 || sent != f.Size {
				t.Fatal("replayed create or wrong progress")
			}
			if mode == "multipart" && (!merged || parts != 2 || !bytes.Equal(uploaded, payload)) {
				t.Fatal("multipart bytes/merge")
			}
			if mode == "rapid" && parts != 0 {
				t.Fatal("rapid uploaded content")
			}
		})
	}
}

func TestGuangyaFoldersAndState(t *testing.T) {
	a := Account{Kind: "guangya", Endpoint: GuangyaAPI, Secret: "synthetic-access-token"}
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		return gyResponse(0, map[string]any{"total": 2, "list": []any{map[string]any{"fileId": "folder-a", "fileName": "目录", "resType": 2}, map[string]any{"fileId": "file-a", "fileName": "文件", "resType": 1}}}), nil
	})}
	page, e := ListGuangyaFolders(context.Background(), client, a, "root")
	if e != nil || len(page.Folders) != 1 || page.Folders[0].ID != "folder-a" {
		t.Fatal("folder mapping")
	}
	f := source(t)
	client.Transport = quarkTransport(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "get_file_list") {
			t.Fatal("uploaded despite name conflict")
		}
		return gyResponse(0, map[string]any{"list": []any{map[string]any{"fileId": "conflict", "fileName": f.Name, "resType": 2}}}), nil
	})
	if e := (&HTTPUploader{client}).Upload(context.Background(), a, f, "root", func(int64) {}); e == nil {
		t.Fatal("conflict ignored")
	}
	for _, body := range []string{`{}`, `{"code":117,"data":{}}`} {
		client.Transport = quarkTransport(func(r *http.Request) (*http.Response, error) { return quarkResponse(body, nil), nil })
		if VerifyGuangyaSession(context.Background(), client, a) == nil {
			t.Fatal("invalid response accepted")
		}
	}
	state := filepath.Join(t.TempDir(), "state.enc")
	m, e := NewManager(state, NewHTTPUploader())
	if e != nil {
		t.Fatal(e)
	}
	account, e := m.SaveAccount(AccountInput{Name: "光鸭", Kind: "guangya", Secret: a.Secret, Username: "device-id"})
	if e != nil || account.Folder != "root" || account.Secret != "" {
		t.Fatal("account root or credential exposure")
	}
	if e = m.SetDefaultFolder(account.ID, "folder-a", "根目录 / 目录"); e != nil {
		t.Fatal(e)
	}
	reopened, e := NewManager(state, NewHTTPUploader())
	if e != nil || reopened.accounts[0].Folder != "folder-a" || reopened.accounts[0].Username != "device-id" {
		t.Fatal("state persistence")
	}
	raw, _ := os.ReadFile(state)
	if bytes.Contains(raw, []byte(a.Secret)) {
		t.Fatal("plaintext token")
	}
	m.Close()
	reopened.Close()
}

func TestGuangyaOptionalSuccessCode(t *testing.T) {
	a := Account{Kind: "guangya", Endpoint: GuangyaAPI, Secret: "synthetic-access-token"}
	for _, body := range []string{`{"data":{}}`, `{"data":{"total":0,"list":[]}}`, `{"code":0,"data":{"list":[]}}`} {
		client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
			return quarkResponse(body, nil), nil
		})}
		if err := VerifyGuangyaSession(context.Background(), client, a); err != nil {
			t.Errorf("valid success response %s: %v", body, err)
		}
	}
	for _, body := range []string{`{}`, `null`, `[]`, `<html>synthetic-access-token</html>`, `{"code":null,"data":{}}`, `{"code":"0","data":{}}`, `{"data":null}`, `{"data":[]}`, `{"error":"invalid_token","data":{}}`, `{"error_code":117,"data":{}}`} {
		client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
			return quarkResponse(body, nil), nil
		})}
		err := VerifyGuangyaSession(context.Background(), client, a)
		if err == nil || !strings.Contains(err.Error(), "stage=/userres/v1/file/get_file_list") || strings.Contains(err.Error(), a.Secret) {
			t.Errorf("invalid response accepted or unsafe/unhelpful diagnostic: %v", err)
		}
	}
}
