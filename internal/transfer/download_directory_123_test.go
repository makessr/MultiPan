package transfer

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test123DirectoryProtocolAndContent(t *testing.T) {
	for _, mode := range []string{"normal", "optional-parent", "no-md5", "status210", "wrong-parent", "cycle", "repeated-id", "bad-type", "negative-size", "collision", "stale-root", "expired", "scan-error", "changed-file", "checksum", "cancel-transfer"} {
		t.Run(mode, func(t *testing.T) {
			const largeID = "9007199254740993"
			mdA := md5.Sum([]byte("aaa"))
			mdB := md5.Sum([]byte("bbb"))
			etagA, etagB := hex.EncodeToString(mdA[:]), hex.EncodeToString(mdB[:])
			if mode == "no-md5" {
				etagB = ""
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := Account{ID: "p", Kind: "pan123", Endpoint: Pan123API, Secret: "fixture-token"}
			scanDone := false
			calls := 0
			client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
				if e := r.Context().Err(); e != nil {
					return nil, e
				}
				if r.URL.Host == "api.123278.com" {
					if r.Header.Get("Authorization") != "Bearer fixture-token" {
						t.Error("API auth missing")
					}
					if r.Method == "GET" {
						id := r.URL.Query().Get("parentFileId")
						if mode == "expired" {
							res := quarkResponse("", nil)
							res.StatusCode = 401
							return res, nil
						}
						if mode == "scan-error" && id == "200" {
							return quarkResponse(`{"code":500,"message":"fixture"}`, nil), nil
						}
						entry := func(fid, name, parent string, typ int, size int64, etag string) map[string]any {
							m := map[string]any{"FileId": json.Number(fid), "FileName": name, "ParentFileId": json.Number(parent), "Type": typ, "Size": size, "Etag": etag, "S3KeyFlag": "opaque-key"}
							if mode == "optional-parent" {
								delete(m, "ParentFileId")
							}
							return m
						}
						var rows []any
						switch id {
						case "0":
							rows = []any{entry("100", "Root", "0", 1, 0, "")}
						case "100":
							one := entry(largeID, "alpha.txt", "100", 0, 3, etagA)
							if mode == "wrong-parent" {
								one["ParentFileId"] = 999
							}
							if mode == "bad-type" {
								one["Type"] = 2
							}
							if mode == "negative-size" {
								one["Size"] = -1
							}
							if mode == "changed-file" && scanDone {
								one["FileName"] = "new.txt"
							}
							rows = []any{one, entry("200", "Sub", "100", 1, 0, ""), entry("300", "Empty", "100", 1, 0, "")}
							if mode == "cycle" {
								rows = append(rows, entry("100", "Loop", "100", 1, 0, ""))
							}
							if mode == "collision" {
								rows = append(rows, entry("400", "ALPHA.TXT", "100", 0, 3, etagA))
							}
						case "200":
							rows = []any{entry("500", "beta.txt", "200", 0, 3, etagB)}
							if mode == "repeated-id" {
								rows = append(rows, entry(largeID, "again.txt", "200", 0, 3, etagA))
							}
						case "300":
							rows = []any{}
						default:
							t.Error("unexpected parent query")
						}
						return jsonResponse(map[string]any{"code": 0, "data": map[string]any{"Next": -1, "InfoList": rows}}), nil
					}
					var body map[string]any
					decoder := json.NewDecoder(r.Body)
					decoder.UseNumber()
					decoder.Decode(&body)
					fid := body["fileId"]
					if r.URL.Path != "/b/api/file/download_info" || body["s3keyFlag"] != "opaque-key" {
						t.Error("wrong download request")
					}
					part := "b"
					if fid == json.Number(largeID) {
						part = "a"
						if body["etag"] != etagA || body["fileName"] != "alpha.txt" {
							t.Error("large-ID metadata mismatch")
						}
					} else if fid != json.Number("500") {
						t.Error("file ID precision lost")
					}
					return jsonResponse(map[string]any{"code": 0, "data": map[string]any{"DownloadUrl": "https://cdn.example/" + part}}), nil
				}
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("credentials leaked to content")
				}
				data := "aaa"
				if r.URL.Path == "/b" {
					data = "bbb"
				}
				if mode == "checksum" && r.URL.Path == "/a" {
					data = "AAA"
				}
				res := quarkResponse(data, nil)
				res.ContentLength = 3
				if mode == "status210" {
					res.StatusCode = 210
				}
				return res, nil
			})}
			sha := sha256.Sum256([]byte("aaa"))
			originals := []UploadFingerprint{{AccountID: "p", EntryID: largeID, Name: "alpha.txt", Size: 3, Revision: etagA, SHA256: hex.EncodeToString(sha[:])}}
			if mode == "checksum" {
				originals = nil
			}
			parent := t.TempDir()
			name := "Root"
			if mode == "stale-root" {
				name = "changed"
			}
			result, e := download123Directory(ctx, client, a, DownloadInput{AccountID: "p", ParentID: "0", EntryID: "100", Name: name}, parent, func(ctx context.Context, in DownloadInput, dest string, p func(DownloadProgress)) (DownloadResult, error) {
				scanDone = true
				calls++
				return DownloadAccountFile(ctx, client, a, in, dest, func(progress DownloadProgress) {
					p(progress)
					if mode == "cancel-transfer" && progress.Received > 0 {
						cancel()
					}
				}, originals...)
			}, nil)
			valid := mode == "normal" || mode == "optional-parent" || mode == "no-md5" || mode == "status210" || mode == "changed-file" || mode == "checksum" || mode == "cancel-transfer"
			if !valid {
				if e == nil || calls != 0 {
					t.Fatal("invalid tree reached content", e)
				}
				entries, _ := os.ReadDir(parent)
				if len(entries) != 0 {
					t.Fatal("scan failure created root")
				}
				return
			}
			if e != nil || len(result.Items) != 2 {
				t.Fatal("folder failed", e)
			}
			if _, e = os.Stat(filepath.Join(parent, "Root", "Empty")); e != nil {
				t.Fatal("empty directory lost")
			}
			if mode == "cancel-transfer" {
				if !result.Cancelled || calls != 1 || result.Items[1].Status != "skipped" {
					t.Fatal("cancel continued")
				}
				return
			}
			if mode == "changed-file" || mode == "checksum" {
				if result.Items[0].Status != "failed" || result.Items[1].Status != "saved" {
					t.Fatal("failed file not isolated")
				}
				if mode == "checksum" && !strings.Contains(result.Items[0].Message, "MD5") {
					t.Fatal("123 strong MD5 check lost")
				}
				return
			}
			if result.Items[0].Result.Verification != "verified" || !strings.Contains(result.Items[0].Result.Details, "SHA256") {
				t.Fatal("original SHA not checked")
			}
			want := "verified"
			if mode == "no-md5" {
				want = "unverified"
			}
			if result.Items[1].Result.Verification != want {
				t.Fatal("incorrect hash classification")
			}
			for rel, want := range map[string]string{"alpha.txt": "aaa", filepath.Join("Sub", "beta.txt"): "bbb"} {
				data, e := os.ReadFile(filepath.Join(parent, "Root", rel))
				if e != nil || string(data) != want {
					t.Fatal("wrong saved bytes", e)
				}
			}
		})
	}
}
