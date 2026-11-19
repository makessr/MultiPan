package transfer

import "testing"

func TestBaiduShareURLVariantsAndIndependentAnchor(t *testing.T) {
	base := "https://pan.baidu.com/s/1fixture"
	for _, values := range [][]string{
		{base}, {"http://pan.baidu.com/s/1fixture"}, {"//pan.baidu.com/s/1fixture"}, {"/s/1fixture"}, {"pan.baidu.com/s/1fixture"},
		{base + "?pwd=A1b2"}, {"https://pan.baidu.com/share/init?surl=fixture&pwd=A1b2"}, {"/share/init?surl=fixture"},
		{"fixture", base}, {"1fixture", base}, {"fixture", "", base}, {"", base + "?pwd=A1b2", base}, {"/s/1fixture", base},
	} {
		got, err := resolveBaiduShareURL(values...)
		if err != nil || got != base {
			t.Fatalf("accepted shape %v: %q %v", values, got, err)
		}
	}
	for _, values := range [][]string{
		{""}, {"fixture"}, {"fixture", "1fixture"}, {"other", base}, {base, "https://pan.baidu.com/s/other"},
		{"https://pan.baidu.com.evil.example/s/1fixture"}, {"https://evil.example/s/1fixture", base}, {"https://user@pan.baidu.com/s/1fixture"},
		{base + "?pwd=****"}, {base + "?pwd=A1b2&pwd=C3d4"}, {base + "?redirect=https://evil.example"}, {base + "#fragment"},
		{"https://pan.baidu.com:443/s/1fixture"}, {"https://pan.baidu.com/s/%31fixture"}, {"javascript:fixture"}, {"https://pan.baidu.com/share/init?surl=fixture&surl=other"},
	} {
		if got, err := resolveBaiduShareURL(values...); err == nil || got != "" {
			t.Fatalf("unsafe/ambiguous URL accepted %v: %q", values, got)
		}
	}
}
