package transfer

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// The allocation ID is not necessarily the final object ID. The current web
// completion response carries the authoritative mapping in file_info.
func Test123FinalObjectMapping(t *testing.T) {
	for _, mode := range []string{"complete", "stale-list", "poll", "ready", "nested-reuse", "existing"} {
		t.Run(mode, func(t *testing.T) {
			f := source(t)
			payload, _ := os.ReadFile(f.Path)
			sum := md5.Sum(payload)
			digest := hex.EncodeToString(sum[:])
			a := Account{Kind: "pan123", Endpoint: Pan123API, Secret: "synthetic-account-bearer-token"}
			final := map[string]any{"FileId": 99, "ParentFileId": 42, "FileName": f.Name, "Size": f.Size, "Etag": digest, "Type": 0}
			prepared, completed := false, false
			puts, posts, polls := 0, 0, 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "storage.example" {
					puts++
					b, _ := io.ReadAll(r.Body)
					if string(b) != string(payload) {
						t.Error("bytes changed")
					}
					return quarkResponse("", nil), nil
				}
				data := any(map[string]any{})
				switch r.URL.Path {
				case "/b/api/file/list/new":
					entries := []any{}
					if (completed && mode != "stale-list") || mode == "existing" || (mode == "nested-reuse" && prepared) {
						entries = append(entries, final)
					}
					data = map[string]any{"InfoList": entries, "Next": "-1", "Total": len(entries)}
				case "/b/api/file/upload_request":
					posts++
					prepared = true
					pre := map[string]any{"FileId": 9, "Key": "private-object-key", "Bucket": "test", "UploadId": "private-upload-id", "StorageNode": "node", "Reuse": false}
					if mode == "ready" {
						pre["UploadFileStatus"] = 255
					}
					if mode == "nested-reuse" {
						pre = map[string]any{"FileId": 0, "Reuse": true, "Info": final}
					}
					data = pre
				case "/b/api/file/s3_upload_object/auth":
					data = map[string]any{"presignedUrls": map[string]string{"1": "https://storage.example/key?signature=private-signature"}}
				case "/b/api/file/upload_complete/v2":
					completed = true
					if mode == "complete" || mode == "stale-list" {
						data = map[string]any{"file_info": final}
					}
				case "/b/api/file/upload_complete/result":
					polls++
					completed = true
					if r.Method != "GET" || r.URL.Query().Get("fileId") != "9" || r.URL.Query().Get("key") != "private-object-key" {
						t.Error("lost result context")
					}
					data = map[string]any{"file_info": final}
				default:
					t.Fatalf("unexpected API %s", r.URL.Path)
				}
				return quarkResponse(quarkData(data), nil), nil
			})}
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			var sent int64
			e := (&HTTPUploader{client}).Upload(ctx, a, f, "42", func(n int64) { sent = n })
			if e != nil {
				t.Fatal(e)
			}
			if sent != f.Size {
				t.Error("verified result progress missing")
			}
			if (mode == "existing" || mode == "nested-reuse" || mode == "ready") && puts != 0 {
				t.Fatal("uploaded an already materialized object")
			}
			if mode == "existing" && posts != 0 {
				t.Fatal("created duplicate during retry")
			}
			if (mode == "poll" || mode == "ready") && polls != 1 {
				t.Fatal("did not query final result")
			}
		})
	}
}

func Test123TerminalMetadataMustMatch(t *testing.T) {
	f := source(t)
	b, _ := os.ReadFile(f.Path)
	h := md5.Sum(b)
	digest := hex.EncodeToString(h[:])
	for _, field := range []string{"FileId", "ParentFileId", "FileName", "Size", "Etag", "Type"} {
		t.Run(field, func(t *testing.T) {
			info := map[string]any{"FileId": 99, "ParentFileId": 42, "FileName": f.Name, "Size": f.Size, "Etag": digest, "Type": 0}
			switch field {
			case "FileId":
				info[field] = 0
			case "ParentFileId":
				info[field] = 43
			case "FileName":
				info[field] = "other"
			case "Size":
				info[field] = f.Size + 1
			case "Etag":
				info[field] = "wrong"
			case "Type":
				info[field] = 1
			}
			calls := 0
			c := pan123Client{&http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return quarkResponse(quarkData(map[string]any{"file_info": info}), nil), nil
			})}, Account{Endpoint: Pan123API, Secret: "synthetic-account-bearer-token"}}
			e := c.complete123(context.Background(), pan123Pre{FileID: "9"}, "42", f, digest, false, true)
			if e == nil || !strings.Contains(e.Error(), "RESULT-MISMATCH") || calls != 1 {
				t.Fatalf("accepted mismatching %s", field)
			}
		})
	}
}

func Test123PendingResultBoundedAndRedacted(t *testing.T) {
	calls := 0
	c := pan123Client{&http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return quarkResponse(quarkData(map[string]any{"duration": 86400}), nil), nil
	})}, Account{Endpoint: Pan123API, Secret: "synthetic-account-bearer-token"}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	e := c.complete123(ctx, pan123Pre{FileID: "9", Key: "private-key", UploadID: "private-upload"}, "42", File{}, "", false, true)
	if e == nil || !strings.Contains(e.Error(), "RESULT-PENDING") || strings.Contains(e.Error(), "private-") || calls != 2 {
		t.Fatalf("polling did not stop safely: %v calls=%d", e, calls)
	}
}

func Test123ExistingContentMismatch(t *testing.T) {
	f := source(t)
	a := Account{Kind: "pan123", Endpoint: Pan123API, Secret: "synthetic-account-bearer-token"}
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/file/list/new") {
			t.Fatal("overwrote conflicting object")
		}
		b, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"InfoList": []any{map[string]any{"FileId": 99, "FileName": f.Name, "Size": f.Size, "Etag": "different", "Type": 0}}, "Next": "-1"}})
		return quarkResponse(string(b), nil), nil
	})}
	if e := (&HTTPUploader{client}).Upload(context.Background(), a, f, "42", func(int64) {}); e == nil {
		t.Fatal("mismatching content accepted")
	}
}
