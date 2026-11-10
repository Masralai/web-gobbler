package v2

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestDocxMarkdown(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	w, err := z.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`<?xml version="1.0"?><w:document xmlns:w="x"><w:body><w:p><w:r><w:t>Hello</w:t></w:r></w:p><w:p><w:r><w:t>World</w:t></w:r></w:p></w:body></w:document>`))
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := docxMarkdown(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hello\n\nWorld" {
		t.Fatalf("got %q", got)
	}
}
