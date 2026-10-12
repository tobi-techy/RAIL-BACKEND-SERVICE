package document

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
	"testing"
)

// buildPDF assembles a minimal single-page PDF whose content stream carries the
// given text-showing operators. compress controls whether the stream is
// Flate-encoded, which is what a real writer does and what the scanner has to
// inflate. No dependency on a PDF library: the scanner is the thing under test,
// so the fixture must be bytes it has to actually parse.
func buildPDF(content string, compress bool) []byte {
	stream := []byte("BT /F1 12 Tf 50 700 Td " + content + " ET")
	filter := ""
	if compress {
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		_, _ = zw.Write(stream)
		_ = zw.Close()
		stream = buf.Bytes()
		filter = " /Filter /FlateDecode"
	}

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	objects := []string{
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n",
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n",
		"3 0 obj\n<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>\nendobj\n",
		fmt.Sprintf("4 0 obj\n<< /Length %d%s >>\nstream\n", len(stream), filter),
	}
	for _, obj := range objects {
		out.WriteString(obj)
	}
	out.Write(stream)
	out.WriteString("\nendstream\nendobj\n")
	out.WriteString("trailer\n<< /Root 1 0 R >>\n%%EOF\n")
	return out.Bytes()
}

// show renders one text-showing operator with a literal string.
func show(text string) string {
	return "(" + strings.ReplaceAll(text, ")", "") + ") Tj "
}

func TestPDFTextReadsInflatedContentStream(t *testing.T) {
	content := show("WIGMORE NIGERIA LTD") +
		show("Invoice Number WGT-2610-0417") +
		show("Due Date 17/10/2026")

	got := PDFText(buildPDF(content, true))
	for _, want := range []string{"WIGMORE", "Invoice", "Number", "WGT-2610-0417"} {
		if !strings.Contains(got, want) {
			t.Fatalf("PDFText() = %q, want it to contain %q", got, want)
		}
	}
}

func TestPDFTextReadsRawContentStream(t *testing.T) {
	got := PDFText(buildPDF(show("HELLO TRADER"), false))
	if !strings.Contains(got, "HELLO") {
		t.Fatalf("PDFText() = %q, want the uncompressed stream read", got)
	}
}

func TestPDFTextNeverPanicsOnJunk(t *testing.T) {
	cases := map[string][]byte{
		"empty":        nil,
		"not a pdf":    []byte("just some text, no streams here"),
		"broken flate": []byte("1 0 obj\n<< /Filter /FlateDecode >>\nstream\nnot really flate\nendstream\nendobj\n"),
		"truncated":    []byte("1 0 obj\n<< /Filter /FlateDecode >>\nstream\nx\x9c"),
		"binary":       bytes.Repeat([]byte{0x00, 0xff, 0x10}, 512),
	}
	for name, data := range cases {
		if got := PDFText(data); got != "" && strings.ContainsAny(got, "\x00") {
			t.Fatalf("%s: PDFText() leaked binary: %q", name, got)
		}
	}
}

func TestClassifyPDF(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    DocType
	}{
		{
			name: "distributor invoice",
			content: show("WIGMORE NIGERIA LIMITED") +
				show("Invoice Number WGT-2610-0417") +
				show("Bill To Mama Tobi Stores") +
				show("Payment Terms 30 days"),
			want: DocInvoice,
		},
		{
			name: "delivery note",
			content: show("DELIVERY NOTE") +
				show("Goods Received in Good Condition") +
				show("Received By") +
				show("Delivered By"),
			want: DocDeliveryNote,
		},
		{
			name: "stock book",
			content: show("OPENING STOCK") +
				show("GOODS SUPPLIED") +
				show("SOLD") +
				show("BALANCE LEFT"),
			want: DocStockBook,
		},
		{
			name: "bank statement stays a statement",
			content: show("Account Number 0123456") +
				show("Statement Period 01/09/2026 - 30/09/2026") +
				show("Opening Balance") +
				show("Value Date") +
				show("Closing Balance"),
			want: DocBankStatement,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyPDF(buildPDF(tt.content, true)); got != tt.want {
				t.Fatalf("ClassifyPDF() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("no text layer is unknown", func(t *testing.T) {
		if got := ClassifyPDF(buildPDF(show(""), true)); got != DocUnknown {
			t.Fatalf("ClassifyPDF() = %v, want unknown", got)
		}
		if got := ClassifyPDF(nil); got != DocUnknown {
			t.Fatalf("ClassifyPDF(nil) = %v, want unknown", got)
		}
	})
}
