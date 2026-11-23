package model

import (
	"strings"
	"testing"
)

// A file is kept under the last part of the name it was sent with, without a
// folder or anything unprintable, and under a media type only where what was
// sent reads as one.
func TestAnAttachmentIsKeptUnderAPlainNameAndType(t *testing.T) {
	for sent, want := range map[string]string{
		"report.pdf":                      "report.pdf",
		`C:\Users\ann\report.pdf`:         "report.pdf",
		"../../etc/passwd":                "passwd",
		"  scan\x00 1.png ":               "scan 1.png",
		"":                                "attachment",
		"..":                              "attachment",
		"folder/":                         "attachment",
		strings.Repeat("a", 300) + ".txt": strings.Repeat("a", 255),
	} {
		if got := AttachmentName(sent); got != want {
			t.Errorf("a file sent as %q is kept as %q, want %q", sent, got, want)
		}
	}

	for sent, want := range map[string]string{
		"text/plain; charset=utf-8": "text/plain",
		"IMAGE/PNG":                 "image/png",
		"nonsense":                  "application/octet-stream",
		"":                          "application/octet-stream",
	} {
		if got := AttachmentType(sent); got != want {
			t.Errorf("a file sent as %q is kept as the type %q, want %q", sent, got, want)
		}
	}
}
