package transfer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyLanzouSession(t *testing.T) {
	for _, code := range []string{"1", "2", "9", "0", "<html>"} {
		t.Run(code, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/doupload.php" || r.Header.Get("Cookie") != "phpdisk_info=synthetic" || r.Header.Get("User-Agent") != "testUA" {
					t.Error("invalid verification request")
				}
				r.ParseForm()
				if r.Form.Get("task") != "47" || r.Form.Get("folder_id") != "-1" {
					t.Error("verification should be read-only folder listing")
				}
				if code == "<html>" {
					w.Write([]byte(code))
					return
				}
				w.Write([]byte(`{"zt":` + code + `}`))
			}))
			defer s.Close()
			err := VerifyLanzouSession(context.Background(), s.Client(), Account{Endpoint: s.URL, Secret: "phpdisk_info=synthetic", UserAgent: "testUA"})
			if (err == nil) != (code == "1" || code == "2") {
				t.Fatalf("unexpected verification result %v", err)
			}
		})
	}
}
