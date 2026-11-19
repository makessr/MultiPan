package transfer

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCtDAVFoldersAndUpload(t *testing.T) {
	a := Account{Kind: "ctdav", Endpoint: "https://dav.example/dav", Username: "dav-user", Secret: "synthetic-password"}
	client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
		user, password, ok := r.BasicAuth()
		if !ok || user != a.Username || password != a.Secret || r.Header.Get("Cookie") != "" {
			t.Fatal("incorrect DAV authentication")
		}
		if r.Method != "PROPFIND" || r.Header.Get("Depth") != "1" || r.URL.Path != "/dav/" {
			t.Fatal("wrong directory request")
		}
		response := quarkResponse(`<d:multistatus xmlns:d="DAV:"><d:response><d:href>/dav/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response><d:response><d:href>/dav/%E5%AD%90%E7%9B%AE%E5%BD%95/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response><d:response><d:href>https://other.example/dav/escape/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`, nil)
		response.StatusCode = 207
		return response, nil
	})}
	p, e := ListDAVFolders(context.Background(), client, a, "/")
	if e != nil || len(p.Folders) != 1 || p.Folders[0].ID != "/子目录" {
		t.Fatalf("directory decoding %v %+v", e, p)
	}
	f := source(t)
	var uploaded []byte
	created := false
	client.Transport = quarkTransport(func(r *http.Request) (*http.Response, error) {
		user, _, ok := r.BasicAuth()
		if !ok || user != a.Username || r.Header.Get("Cookie") != "" {
			t.Fatal("DAV upload credentials")
		}
		result := quarkResponse("", nil)
		if r.Method == "MKCOL" {
			created = true
			result.StatusCode = 201
			return result, nil
		}
		if r.Method != "PUT" || r.Header.Get("If-None-Match") != "*" || r.URL.Path != "/dav/子目录/"+f.Name {
			t.Fatal("wrong upload destination or overwrite policy")
		}
		uploaded, _ = io.ReadAll(r.Body)
		result.StatusCode = 201
		return result, nil
	})
	if e := (&HTTPUploader{client}).Upload(context.Background(), a, f, "/子目录", func(int64) {}); e != nil || !created || int64(len(uploaded)) != f.Size {
		t.Fatalf("upload %v", e)
	}
	client.Transport = quarkTransport(func(r *http.Request) (*http.Response, error) {
		res := quarkResponse("", nil)
		res.StatusCode = 401
		return res, nil
	})
	if _, e := ListDAVFolders(context.Background(), client, a, "/"); e == nil {
		t.Fatal("invalid credentials accepted")
	}
	a.Endpoint = "http://dav.example"
	if _, e := ListDAVFolders(context.Background(), client, a, "/"); e == nil {
		t.Fatal("cleartext endpoint accepted")
	}
	if _, e := ListDAVFolders(context.Background(), client, a, "/../escape"); e == nil || !strings.Contains(e.Error(), "目录") {
		t.Fatal("invalid folder accepted")
	}
}
