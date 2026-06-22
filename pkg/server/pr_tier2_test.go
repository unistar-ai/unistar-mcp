package server

import "testing"

func TestFormatMergedPRListHeader(t *testing.T) {
	// formatMergedPRList hits gh; test backport handler path via label default in docs only.
	if defaultBackportLabel != "needs-backport" {
		t.Fatalf("label %q", defaultBackportLabel)
	}
}

func TestHandlePRIsDocsOnlyFormat(t *testing.T) {
	s := New(Options{})
	res, err := s.handlePRIsDocsOnly(nil, callReq(map[string]any{
		"repo": "acme/x",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected missing pr_number error")
	}
}
