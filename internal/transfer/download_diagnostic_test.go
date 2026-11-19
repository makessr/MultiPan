package transfer

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDownloadDiagnosticRedaction(t *testing.T) {
	for _, item := range []struct{ body, want string }{
		{`{"error_code":31064,"error_msg":"hit black userlist https://example.com/file?token=private-value"}`, "reason=account-policy"},
		{`{"error_code":31326,"error_msg":"illeage dlna private-value"}`, "reason=dlna-policy"},
		{`{"error_code":"https://private-value","error_msg":"Cookie=private-value"}`, "code=未知"},
		{`<html>private-value</html>`, "body=html"},
	} {
		res := &http.Response{Body: io.NopCloser(strings.NewReader(item.body)), Header: http.Header{"Content-Type": {"text/html"}}}
		message := baiduForbiddenDiagnostic(res)
		if !strings.Contains(message, item.want) || strings.Contains(message, "private-value") || strings.Contains(message, "https://") {
			t.Fatal("unsafe diagnostic", message)
		}
	}
	_, e := lzHTML(&http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("private-value"))})
	if e == nil || !strings.Contains(e.Error(), "HTTP=403") || strings.Contains(e.Error(), "private-value") {
		t.Fatal("unsafe page diagnostic", e)
	}
}
