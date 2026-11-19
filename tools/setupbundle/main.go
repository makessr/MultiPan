package main

import (
	"archive/zip"
	"encoding/json"
	"flag"
	"fmt"
	"multipan/internal/installation"
	"os"
	"path/filepath"
)

func main() {
	exe := flag.String("exe", "", "application executable")
	v := flag.String("version", "", "version")
	out := flag.String("out", "tools/setup/payload/payload.zip", "output")
	flag.Parse()
	if e := build(*exe, *v, *out); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func build(exe, v, out string) error {
	files := map[string][]byte{}
	paths := map[string]string{installation.App: exe, "TRIAL-GUIDE.txt": "TRIAL-GUIDE.txt", "THIRD_PARTY_NOTICES.md": "THIRD_PARTY_NOTICES.md"}
	m := installation.Manifest{Owner: installation.Owner, Version: v, Hashes: map[string]string{}}
	for name, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		files[name] = b
		m.Hashes[name] = installation.Digest(b)
	}
	b, _ := json.Marshal(m)
	files["manifest.json"] = b
	if e := os.MkdirAll(filepath.Dir(out), 0700); e != nil {
		return e
	}
	f, e := os.Create(out)
	if e != nil {
		return e
	}
	w := zip.NewWriter(f)
	for _, n := range append(append([]string{}, installation.PayloadNames...), "manifest.json") {
		s, e := w.Create(n)
		if e != nil {
			f.Close()
			return e
		}
		if _, e = s.Write(files[n]); e != nil {
			f.Close()
			return e
		}
	}
	if e = w.Close(); e != nil {
		f.Close()
		return e
	}
	return f.Close()
}
