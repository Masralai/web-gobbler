package worker

import (
	"testing"

	apiv2 "github.com/Masralai/web-gobbler/internal/api/v2"
)

func TestExtractTypesFor(t *testing.T) {
	got := extractTypesFor(apiv2.Formats{{Type: "markdown"}, {Type: "rawHtml"}, {Type: "links"}})
	want := []string{"markdown", "raw_html", "links"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if got := extractTypesFor(nil); len(got) != 1 || got[0] != "markdown" {
		t.Fatalf("default = %v", got)
	}
}
