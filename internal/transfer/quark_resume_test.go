package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestQuarkInterruptedUploadReusesConfirmedPartsAfterCheckpointReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.bin")
	os.WriteFile(path, []byte("abcdefghijkl"), 0600)
	f, _ := Inspect(path)
	a := Account{Kind: "quark", Endpoint: QuarkAPI, Secret: "fixture-cookie"}
	cpPath := filepath.Join(t.TempDir(), "checkpoint.enc")
	preCalls := 0
	failSecond := true
	var parts []int
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.Host, "fixture-bucket.") {
			if r.Header.Get("Cookie") != "" {
				t.Fatal("account cookie leaked")
			}
			if r.Method == "PUT" {
				n, _ := strconv.Atoi(r.URL.Query().Get("partNumber"))
				parts = append(parts, n)
				body, _ := io.ReadAll(r.Body)
				if string(body) != string([]byte("abcdefghijkl")[(n-1)*4:n*4]) {
					t.Error("wrong resumed segment")
				}
				if n == 2 && failSecond {
					failSecond = false
					return nil, errors.New("fixture network interruption")
				}
				return quarkResponse("", http.Header{"Etag": {`"etag-` + strconv.Itoa(n) + `"`}}), nil
			}
			body, _ := io.ReadAll(r.Body)
			for _, n := range []string{"1", "2", "3"} {
				if !strings.Contains(string(body), "<PartNumber>"+n+"</PartNumber>") {
					t.Error("merge missing part")
				}
			}
			return quarkResponse("<CompleteMultipartUploadResult/>", nil), nil
		}
		switch r.URL.Path {
		case "/1/clouddrive/file/sort":
			return quarkResponse(quarkData(map[string]any{"list": []any{}}), nil), nil
		case "/1/clouddrive/file/upload/pre":
			preCalls++
			return quarkResponse(`{"status":200,"code":0,"data":{"task_id":"task","obj_key":"object","upload_id":"upload","bucket":"fixture-bucket","upload_url":"https://storage.example.invalid","auth_info":"fixture-auth","callback":{}},"metadata":{"part_size":4}}`, nil), nil
		case "/1/clouddrive/file/update/hash":
			return quarkResponse(quarkData(map[string]any{"finish": false}), nil), nil
		case "/1/clouddrive/file/upload/auth":
			return quarkResponse(quarkData(map[string]any{"auth_key": "OSS fixture"}), nil), nil
		case "/1/clouddrive/file/upload/finish":
			return quarkResponse(quarkData(map[string]any{}), nil), nil
		}
		t.Fatalf("unexpected route %s", r.URL.Path)
		return nil, nil
	})}
	uploader := &HTTPUploader{Client: client}
	prepare := func() context.Context {
		return prepareUploadCheckpoint(withUploadResumePath(context.Background(), cpPath), a, f, "target", "source-sha-fixture")
	}
	if e := uploader.Upload(prepare(), a, f, "target", func(int64) {}); e == nil {
		t.Fatal("first upload should interrupt")
	}
	encrypted, _ := os.ReadFile(cpPath)
	plain, e := unprotect(encrypted)
	if e != nil {
		t.Fatal(e)
	}
	var cp uploadCheckpoint
	json.Unmarshal(plain, &cp)
	if cp.Parts[1] == "" || cp.Parts[2] != "" {
		t.Fatal("unconfirmed part checkpointed")
	}
	if e := uploader.Upload(prepare(), a, f, "target", func(int64) {}); e != nil {
		t.Fatal(e)
	}
	if preCalls != 1 {
		t.Fatal("resumed upload created new session")
	}
	if len(parts) != 4 || parts[0] != 1 || parts[1] != 2 || parts[2] != 2 || parts[3] != 3 {
		t.Fatalf("incorrect retransmissions: %v", parts)
	}
}
