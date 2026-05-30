package statement

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

const (
	MaxFileSize      = 20 * 1024 * 1024 // 20MB
	MaxPageCount     = 100
	ScannedThreshold = 50 // avg chars per page below this = scanned
)

type PDFExtractResult struct {
	Text      string   // full concatenated text
	Pages     []string // text per page (best-effort)
	PageCount int
	IsScanned bool
}

// ExtractText reads a PDF and extracts text content.
func ExtractText(r io.ReadSeeker, fileSize int64) (*PDFExtractResult, error) {
	if fileSize > MaxFileSize {
		return nil, fmt.Errorf("file too large: %d bytes (max %d)", fileSize, MaxFileSize)
	}

	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed

	// Get page count
	ctx, err := api.ReadValidateAndOptimize(r, conf)
	if err != nil {
		return nil, fmt.Errorf("read pdf: %w", err)
	}
	pageCount := ctx.PageCount
	if pageCount > MaxPageCount {
		return nil, fmt.Errorf("too many pages: %d (max %d)", pageCount, MaxPageCount)
	}

	// Extract content to temp dir
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "rail-pdf-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	err = api.ExtractContent(r, tmpDir, "stmt", nil, conf)
	if err != nil {
		// Content extraction failed — likely scanned PDF
		return &PDFExtractResult{PageCount: pageCount, IsScanned: true}, nil
	}

	// Read and reconstruct text from extracted content files
	var allText strings.Builder
	var pages []string

	entries, _ := os.ReadDir(tmpDir)
	// Sort entries by name to maintain page order
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(tmpDir, entry.Name()))
		if err != nil {
			continue
		}
		// Reconstruct readable text from PDF content stream
		pageText := reconstructText(string(data))
		pages = append(pages, pageText)
		allText.WriteString(pageText)
		allText.WriteString("\n---PAGE BREAK---\n")
	}

	fullText := allText.String()
	totalChars := len(strings.TrimSpace(fullText))
	avgCharsPerPage := 0
	if pageCount > 0 {
		avgCharsPerPage = totalChars / pageCount
	}

	return &PDFExtractResult{
		Text:      fullText,
		Pages:     pages,
		PageCount: pageCount,
		IsScanned: avgCharsPerPage < ScannedThreshold,
	}, nil
}

// IsGarbageText checks if extracted text is mostly non-readable (garbled encoding).
// Returns true if >40% of characters are non-printable or non-ASCII control chars.
func IsGarbageText(text string) bool {
	if len(text) == 0 {
		return true
	}
	garbage := 0
	total := 0
	for _, r := range text {
		total++
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			garbage++
		} else if r > 126 && r < 160 {
			garbage++
		}
	}
	if total == 0 {
		return true
	}
	return float64(garbage)/float64(total) > 0.4
}

// ExtractTextFromBytes is a convenience wrapper.
func ExtractTextFromBytes(data []byte) (*PDFExtractResult, error) {
	return ExtractText(bytes.NewReader(data), int64(len(data)))
}

// reconstructText takes raw PDF content stream text and produces readable lines.
// PDF content streams contain operators like:
//   BT /F1 10 Tf 72 700 Td (Hello World) Tj ET
// We extract text between parentheses and use Td/Tm operators to detect line breaks.
func reconstructText(raw string) string {
	// Strategy 1: Extract text from Tj/TJ operators (parenthesized strings)
	extracted := extractParenthesizedText(raw)
	if len(extracted) > 0 {
		return strings.Join(extracted, "\n")
	}
	// Strategy 2: If no parenthesized text found, return raw with cleanup
	return cleanRawContent(raw)
}

// textFragment holds a piece of text with its approximate Y position for sorting.
type textFragment struct {
	text string
	y    float64
	x    float64
}

var (
	// Match text in parentheses: (some text)
	parenTextRe = regexp.MustCompile(`\(([^)]*)\)`)
	// Match Td operator for position: x y Td
	tdRe = regexp.MustCompile(`([-\d.]+)\s+([-\d.]+)\s+Td`)
	// Match Tm operator for position matrix
	tmRe = regexp.MustCompile(`[-\d.]+\s+[-\d.]+\s+[-\d.]+\s+[-\d.]+\s+([-\d.]+)\s+([-\d.]+)\s+Tm`)
)

func extractParenthesizedText(raw string) []string {
	matches := parenTextRe.FindAllStringSubmatch(raw, -1)
	if len(matches) == 0 {
		return nil
	}

	var lines []string
	var currentLine strings.Builder

	for _, m := range matches {
		text := unescapePDFString(m[1])
		if text == "" {
			continue
		}
		// Heuristic: if text ends with common line-ending patterns, flush
		currentLine.WriteString(text)
	}

	// Split accumulated text by common patterns
	fullText := currentLine.String()
	if fullText == "" {
		return nil
	}

	// Split on double spaces (common in bank statements for column separation)
	// and newlines
	rawLines := strings.Split(fullText, "\n")
	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}

	if len(lines) == 0 && fullText != "" {
		lines = append(lines, fullText)
	}

	return lines
}

func unescapePDFString(s string) string {
	s = strings.ReplaceAll(s, "\\n", "\n")
	s = strings.ReplaceAll(s, "\\r", "\r")
	s = strings.ReplaceAll(s, "\\t", "\t")
	s = strings.ReplaceAll(s, "\\(", "(")
	s = strings.ReplaceAll(s, "\\)", ")")
	s = strings.ReplaceAll(s, "\\\\", "\\")
	return s
}

func cleanRawContent(raw string) string {
	// Remove PDF operators, keep only readable text
	lines := strings.Split(raw, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip lines that are purely PDF operators
		if line == "" || line == "BT" || line == "ET" || line == "q" || line == "Q" {
			continue
		}
		// Skip lines that are just numbers (coordinates)
		if isNumericLine(line) {
			continue
		}
		cleaned = append(cleaned, line)
	}
	return strings.Join(cleaned, "\n")
}

func isNumericLine(s string) bool {
	for _, c := range s {
		if c != ' ' && c != '.' && c != '-' && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
