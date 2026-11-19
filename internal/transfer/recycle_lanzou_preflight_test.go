package transfer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func testLanzouConfirmation(action, idKey, id string) string {
	return fmt.Sprintf(`<form method="post" action="mydisk.php?item=recycle">
<input type="hidden" name="formhash" value="synthetic_nonce">
<input type="hidden" name="action" value="%s">
<input type="hidden" name="task" value="%s">
<input type="hidden" name="%s" value="%s"></form>`, action, action, idKey, id)
}

func TestLanzouRecycleConfirmationReadOnly(t *testing.T) {
	for _, kind := range []string{"file", "folder"} {
		for _, op := range []lanzouRecycleOperation{lanzouRestore, lanzouPurge} {
			t.Run(kind+"/"+string(op), func(t *testing.T) {
				action, idKey := kind+"_"+string(op), kind+"_id"
				count := 0
				client := &http.Client{Transport: quarkTransport(func(r *http.Request) (*http.Response, error) {
					count++
					q := r.URL.Query()
					if r.Method != "GET" || r.Body != nil || r.URL.Scheme != "https" || r.URL.Host != "up.woozooo.com" ||
						r.URL.Path != "/mydisk.php" || q.Get("item") != "recycle" || q.Get("action") != action ||
						q.Get(idKey) != "123" || len(q) != 3 || r.Header.Get("Cookie") != "synthetic" {
						t.Fatal("unexpected request", r.Method, r.URL)
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(testLanzouConfirmation(action, idKey, "123")))}, nil
				})}
				a := Account{Kind: "lanzou", Endpoint: lanzouRecycleOrigin, Secret: "synthetic"}
				if err := checkLanzouRecycleConfirmation(context.Background(), client, a, kind+":123", op); err != nil || count != 1 {
					t.Fatal("preflight failed", err, count)
				}
				a.Endpoint = "https://pc.woozooo.com"
				if err := checkLanzouRecycleConfirmation(context.Background(), client, a, kind+":123", op); err == nil || count != 1 {
					t.Fatal("unexpected credential dispatch to another host", err, count)
				}
			})
		}
	}
}

func TestLanzouRecycleConfirmationRejectsMismatch(t *testing.T) {
	form := testLanzouConfirmation("file_restore", "file_id", "123")
	cases := map[string]string{
		"wrong ID":         testLanzouConfirmation("file_restore", "file_id", "12"),
		"wrong operation":  testLanzouConfirmation("file_delete_complete", "file_id", "123"),
		"wrong kind":       testLanzouConfirmation("file_restore", "folder_id", "123"),
		"both kinds":       strings.Replace(form, "</form>", `<input type="hidden" name="folder_id" value="123"></form>`, 1),
		"conflicting task": strings.Replace(form, `name="task" value="file_restore"`, `name="task" value="file_delete_complete"`, 1),
		"duplicate token":  strings.Replace(form, "</form>", `<input type="hidden" name="formhash" value="other"></form>`, 1),
		"duplicate form":   form + form,
		"external target":  strings.Replace(form, "mydisk.php?", "https://pc.woozooo.com/mydisk.php?", 1),
		"wrong path":       strings.Replace(form, "mydisk.php?", "account.php?", 1),
		"extra query":      strings.Replace(form, "item=recycle", "item=recycle&amp;action=delete_all", 1),
		"unauthenticated":  `<form action="account.php" method="post"><input type="hidden" name="formhash" value="login"></form>`,
		"comment":          "<!--" + form + "-->",
		"script":           "<script>" + form + "</script>",
		"visible token":    strings.Replace(form, `type="hidden" name="formhash"`, `type="text" name="formhash"`, 1),
	}
	for key, body := range cases {
		t.Run(key, func(t *testing.T) {
			if err := validateLanzouRecycleConfirmation(body, "file_restore", "file_id", "123"); err == nil || strings.Contains(err.Error(), "synthetic_nonce") {
				t.Fatal("did not reject with a redacted error", err)
			}
		})
	}
}
