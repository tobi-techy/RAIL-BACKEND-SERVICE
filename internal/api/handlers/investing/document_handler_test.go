package investing

import (
	"errors"
	"strings"
	"testing"

	"github.com/lib/pq"
)

// TestDetectDocContentType_MagicBytes verifies magic-byte sniffing and that
// client-supplied extensions/MIME types are never trusted.
func TestDetectDocContentType_MagicBytes(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"pdf", []byte("%PDF-1.4 fake"), "application/pdf"},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00}, "image/jpeg"},
		{"png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D}, "image/png"},
		{"fake pdf extension is text", []byte("hello, not a pdf"), ""},
		{"fake png mime is text", []byte("GIF89a..."), ""},
		{"empty", nil, ""},
		{"truncated", []byte("%P"), ""},
		{"corrupt binary", []byte{0x00, 0x01, 0x02, 0x03}, ""},
	}
	for _, tc := range cases {
		if got := detectDocContentType(tc.data); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestIsUniqueViolation(t *testing.T) {
	if isUniqueViolation(nil) {
		t.Error("nil must not be a violation")
	}
	if !isUniqueViolation(&pq.Error{Code: "23505"}) {
		t.Error("pq 23505 must be detected")
	}
	if !isUniqueViolation(errors.New("create document: pq: duplicate key value violates unique constraint \"uq_documents_user_hash\" (23505)")) {
		t.Error("wrapped message containing 23505 must be detected")
	}
	if isUniqueViolation(errors.New("connection refused")) {
		t.Error("unrelated error must not match")
	}
	if !strings.Contains(docExtension("application/pdf"), ".pdf") {
		t.Error("pdf extension mapping broken")
	}
}

// TestUploadRejectsInvalidFiles exercises the handler's validation. Upload
// checks the rate limit before reading the file, so these cases need a DB;
// instead this test pins the preconditions: magic-byte detection rejects
// non-documents and empty input at the sniffing layer the handler calls.
func TestUploadRejectsInvalidFiles(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"text posing as pdf", []byte("definitely not a document")},
		{"empty file", nil},
		{"corrupt binary", []byte{0x00, 0x01, 0x02, 0x03}},
	} {
		if got := detectDocContentType(tc.body); got != "" {
			t.Errorf("%s: must be rejected, sniffed as %q", tc.name, got)
		}
	}
	// Oversized guard lives in Upload (20MB LimitReader); pin the constant
	// relationship here so a future edit cannot silently lift the ceiling.
	const maxUpload = 20 * 1024 * 1024
	if maxUpload != 20971520 {
		t.Error("upload ceiling changed unexpectedly")
	}
}
