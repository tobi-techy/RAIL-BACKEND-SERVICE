package document

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"io"
	"strconv"
	"strings"
)

// Bounds on the scan. These are the "cheap and bounded" half of the routing
// decision: we read a text layer to answer one yes/no question, and anything
// outside these limits is not something we can classify, so we hand the file to
// the pipeline that already owns it.
const (
	// maxPDFScanBytes bounds how much of the file we walk.
	maxPDFScanBytes = 8 << 20
	// maxPDFStreamBytes bounds one inflated content stream, so a malformed or
	// hostile file cannot inflate into memory. Content streams are usually tens
	// of KB.
	maxPDFStreamBytes = 1 << 20
	// maxPDFStreams bounds how many streams we inflate.
	maxPDFStreams = 256
)

// PDFText returns the best-effort text a PDF draws, without rendering it.
//
// The document pipeline's OCR engine accepts images only, so a PDF arriving in
// chat would otherwise be classified blind. This walks the file's stream
// objects, inflates the Flate-encoded ones, and pulls the literal strings out of
// the text-showing operators (Tj / TJ / ' / "). That is enough to tell a
// distributor invoice or a delivery note from a bank statement — which is all
// Go routes on. The figures a user sees always come from the reader, never from
// this function.
//
// Deliberately forgiving: an encrypted, exotic, or corrupt PDF yields "" and the
// caller keeps its previous behaviour. It never returns an error.
func PDFText(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if len(data) > maxPDFScanBytes {
		data = data[:maxPDFScanBytes]
	}

	var out strings.Builder
	streams := 0
	for i := 0; i < len(data); {
		rel := bytes.Index(data[i:], []byte("stream"))
		if rel < 0 {
			break
		}
		start := i + rel
		// "endstream" contains "stream"; the check below would otherwise treat
		// the tail of one stream as the head of another.
		if start > 0 && data[start-1] == 'd' {
			i = start + len("stream")
			continue
		}
		body := start + len("stream")
		// The keyword is followed by CRLF or LF, never by CR alone.
		if body < len(data) && data[body] == '\r' {
			body++
		}
		if body < len(data) && data[body] == '\n' {
			body++
		}
		end := bytes.Index(data[body:], []byte("endstream"))
		if end < 0 {
			break
		}
		raw := data[body : body+end]
		i = body + end + len("endstream")

		compressed := bytes.Contains(lastN(data, start, 256), []byte("/FlateDecode"))
		content := raw
		if compressed {
			if inflated, ok := inflate(raw); ok {
				content = inflated
			} else {
				// A stream we cannot inflate is not text we can read.
				continue
			}
		}
		// Skip binary streams (images, fonts) — only content streams draw text.
		if !bytes.Contains(content, []byte("BT")) && !bytes.Contains(content, []byte("Tj")) {
			continue
		}
		out.WriteString(pdfStreamText(content))
		out.WriteByte('\n')
		streams++
		if streams >= maxPDFStreams {
			break
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

// ClassifyPDF classifies a PDF from its own text layer. It answers DocUnknown
// when the file carries no readable text, which the caller reads as "not a
// shop document" and routes exactly as it did before.
func ClassifyPDF(data []byte) DocType {
	text := PDFText(data)
	if strings.TrimSpace(text) == "" {
		return DocUnknown
	}
	return NewRuleClassifier().Classify(text, nil)
}

// lastN returns up to n bytes immediately before off — enough to see the
// stream dictionary that precedes the "stream" keyword.
func lastN(data []byte, off, n int) []byte {
	start := off - n
	if start < 0 {
		start = 0
	}
	if off > len(data) {
		off = len(data)
	}
	return data[start:off]
}

// inflate decodes a Flate stream, zlib-wrapped (the spec) or raw (some writers).
// Bounded, never fatal.
func inflate(raw []byte) ([]byte, bool) {
	raw = bytes.TrimSuffix(raw, []byte("\r\n"))
	if zr, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
		defer zr.Close()
		if out, err := io.ReadAll(io.LimitReader(zr, maxPDFStreamBytes)); err == nil && len(out) > 0 {
			return out, true
		}
	}
	fr := flate.NewReader(bytes.NewReader(raw))
	defer fr.Close()
	if out, err := io.ReadAll(io.LimitReader(fr, maxPDFStreamBytes)); err == nil && len(out) > 0 {
		return out, true
	}
	return nil, false
}

// pdfStreamText pulls the drawn strings out of one decoded content stream.
// Line-breaking operators become spaces so tokens never run together: the
// classifier matches phrases like "delivery note" and "opening stock".
func pdfStreamText(s []byte) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		switch s[i] {
		case '(':
			lit, next := pdfLiteralString(s, i)
			out.WriteString(lit)
			out.WriteByte(' ')
			i = next
		case '<':
			if i+1 < len(s) && s[i+1] == '<' {
				i += 2 // dictionary — not drawn text
				continue
			}
			hexStr, next := pdfHexString(s, i)
			out.WriteString(hexStr)
			out.WriteByte(' ')
			i = next
		case 'T':
			// Td/TD/T* move the text cursor: a visual break, so a word break.
			if i+2 < len(s) && (bytes.HasPrefix(s[i:], []byte("Td")) ||
				bytes.HasPrefix(s[i:], []byte("TD")) ||
				bytes.HasPrefix(s[i:], []byte("T*"))) {
				out.WriteByte(' ')
			}
			i++
		case 'E':
			if bytes.HasPrefix(s[i:], []byte("ET")) {
				out.WriteByte(' ')
			}
			i++
		default:
			i++
		}
	}
	return out.String()
}

// pdfLiteralString reads a "(...)" string starting at s[i] == '(' and returns it
// with PDF escapes resolved, plus the index just past the closing paren.
func pdfLiteralString(s []byte, i int) (string, int) {
	var out strings.Builder
	depth := 0
	for ; i < len(s); i++ {
		ch := s[i]
		switch ch {
		case '\\':
			if i+1 >= len(s) {
				return out.String(), len(s)
			}
			i++
			switch esc := s[i]; esc {
			case 'n':
				out.WriteByte('\n')
			case 'r':
				out.WriteByte('\r')
			case 't':
				out.WriteByte('\t')
			case 'b', 'f':
				out.WriteByte(' ')
			case '(', ')', '\\':
				out.WriteByte(esc)
			default:
				// Octal escape (\ddd) or a literal the writer over-escaped.
				if esc >= '0' && esc <= '7' {
					j, val := i, 0
					for ; j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '7'; j++ {
						val = val*8 + int(s[j]-'0')
					}
					out.WriteByte(byte(val))
					i = j - 1
				} else {
					out.WriteByte(esc)
				}
			}
		case '(':
			depth++
			out.WriteByte(ch)
		case ')':
			if depth == 0 {
				return out.String(), i + 1
			}
			depth--
			out.WriteByte(ch)
		default:
			out.WriteByte(ch)
		}
	}
	return out.String(), len(s)
}

// pdfHexString reads a "<...>" string starting at s[i] == '<'.
func pdfHexString(s []byte, i int) (string, int) {
	var hex strings.Builder
	for j := i + 1; j < len(s); j++ {
		switch c := s[j]; {
		case c == '>':
			return decodePDFHex(hex.String()), j + 1
		case isHexDigit(c):
			hex.WriteByte(c)
		case c == ' ' || c == '\n' || c == '\r' || c == '\t':
			// whitespace is allowed between digit pairs
		default:
			return decodePDFHex(hex.String()), j
		}
	}
	return decodePDFHex(hex.String()), len(s)
}

func decodePDFHex(hexStr string) string {
	if len(hexStr)%2 == 1 {
		hexStr += "0"
	}
	var out strings.Builder
	for i := 0; i+1 < len(hexStr); i += 2 {
		if v, err := strconv.ParseUint(hexStr[i:i+2], 16, 8); err == nil {
			out.WriteByte(byte(v))
		}
	}
	return out.String()
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
