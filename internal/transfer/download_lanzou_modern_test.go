package transfer

import (
	"net/url"
	"testing"
)

func TestLanzouModernAjax(t *testing.T) {
	page := `var wp_sign='fixture';var ajaxdata='0NOP';var domain1='https://apifile.woozooo.com/ajaxfile.php?file=123';var domain2='https://apifile.lanzouw.com/ajaxfile.php?file=123';$.ajax({url : dom_ajaxs,data:{'sign':wp_sign,'websignkey':ajaxdata,'kd':kdns}});`
	base, _ := url.Parse("https://share.example/fn")
	form := url.Values{"sign": {"fixture"}, "signs": {"0NOP"}}
	endpoint, e := lzModernAjax(page, base, form)
	if e != nil || endpoint.Host != "apifile.woozooo.com" || endpoint.Path != "/ajaxfile.php" || endpoint.Query().Get("file") != "123" || form.Get("kd") != "0" || form.Get("websignkey") != "0NOP" {
		t.Fatal("modern AJAX unresolved", e)
	}
	bad := `var domain2='https://foreign.example/ajaxfile.php?file=123';url:dom_ajaxs;`
	if _, e = lzModernAjax(bad, base, form); e == nil {
		t.Fatal("untrusted cross-origin AJAX accepted")
	}
}
