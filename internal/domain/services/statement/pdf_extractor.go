package statement

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func init() {
	// pdfcpu requires a writable HOME to create its config directory.
	// In scratch/distroless containers, /home may not exist or be read-only.
	// Try HOME first, then /tmp, then current directory.
	home := os.Getenv("HOME")
	if home != "" && isWritable(home) {
		return
	}
	if isWritable("/tmp") {
		os.Setenv("HOME", "/tmp")
		return
	}
	// Last resort: use current working directory
	if wd, err := os.Getwd(); err == nil && isWritable(wd) {
		os.Setenv("HOME", wd)
	}
}

func isWritable(path string) bool {
	f, err := os.CreateTemp(path, ".rail-test-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

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

// textFragment holds a piece of text with its approximate Y position for sorting.
type textFragment struct {
	text string
	y    float64
	x    float64
}

// PDF content stream operators to filter from raw text.
var pdfOperators = regexp.MustCompile(`\b(?:BT|ET|Tf|Td|Tm|T\*|Do|cm|w|J|j|M|q|Q|cs|SC|sc|SCN|scn|RG|rg|G|g|ri|gs|n|S|s|f|F|f\*|B|B\*|b|b\*|W|W\*|sh|BX|EX|MP|DP|BMC|BDC|EMC|Tc|Tw|Tz|TL|Ts|Tb|Tr|Ts|\d+\s+scn|cm|d0|d1|SetTextRise|shfill)\b`)

var (
	// Match text in parentheses: (some text)
	parenTextRe = regexp.MustCompile(`\(([^)]*)\)`)
	// Match TJ array: [(text) num (text) ...] TJ
	tjArrayRe   = regexp.MustCompile(`\[([^\]]*)\]\s*TJ`)
	// Match Td operator for position: x y Td
	tdRe = regexp.MustCompile(`([-\d.]+)\s+([-\d.]+)\s+Td`)
	// Match Tm operator for position matrix — extract Ty and Tx
	tmRe = regexp.MustCompile(`([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s+Tm`)
)

// reconstructText takes raw PDF content stream text and produces readable lines.
// Handles Tj (single string) and TJ (array of strings with positioning) operators,
// and uses Td/Tm position data to detect line breaks and column boundaries.
func reconstructText(raw string) string {
	fragments := extractTextFragments(raw)
	if len(fragments) == 0 {
		return cleanRawContent(raw)
	}

	// Sort by Y (descending — top to bottom), then X (ascending — left to right)
	sort.Slice(fragments, func(i, j int) bool {
		if fragments[i].y != fragments[j].y {
			return fragments[i].y > fragments[j].y
		}
		return fragments[i].x < fragments[j].x
	})

	var lines []string
	var currentLine strings.Builder
	var lastY, lastX float64
	if len(fragments) > 0 {
		lastY = fragments[0].y
		lastX = fragments[0].x
	}

	for _, f := range fragments {
		// If Y changed significantly (new line), flush current line
		if f.y < lastY-5 {
			if currentLine.Len() > 0 {
				lines = append(lines, strings.TrimSpace(currentLine.String()))
				currentLine.Reset()
			}
			lastX = 0
		} else if f.x > lastX+20 {
			// Same line, significant X gap — insert column separator (tab)
			currentLine.WriteString("\t")
		}
		currentLine.WriteString(f.text)
		currentLine.WriteString(" ")
		lastY = f.y
		lastX = f.x
	}

	if currentLine.Len() > 0 {
		lines = append(lines, strings.TrimSpace(currentLine.String()))
	}

	if len(lines) == 0 {
		return cleanRawContent(raw)
	}
	return strings.Join(lines, "\n")
}

// extractTextFragments extracts text fragments with their positions from PDF content streams.
// Handles both Tj (single string) and TJ (array of strings with positioning offsets) operators.
func extractTextFragments(raw string) []textFragment {
	var fragments []textFragment
	lastTmX := 0.0
	lastTmY := 0.0

	// First pass: track position changes from Td/Tm operators
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Check for Tm (matrix — absolute positioning)
		if tmMatch := tmRe.FindStringSubmatch(line); tmMatch != nil {
			lastTmX, _ = parseFloat64(tmMatch[5])
			lastTmY, _ = parseFloat64(tmMatch[6])
			continue
		}

		// Check for Td (relative positioning)
		if tdMatch := tdRe.FindStringSubmatch(line); tdMatch != nil {
			dx, _ := parseFloat64(tdMatch[1])
			dy, _ := parseFloat64(tdMatch[2])
			lastTmX += dx
			lastTmY += dy
		}

		// Extract text from Tj operator: (text) Tj
		if strings.Contains(line, "Tj") && !strings.Contains(line, "TJ") {
			if m := parenTextRe.FindStringSubmatch(line); m != nil {
				text := unescapePDFString(m[1])
				if text != "" {
					fragments = append(fragments, textFragment{
						text: text,
						x:    lastTmX,
						y:    lastTmY,
					})
				}
			}
			continue
		}

		// Extract text from TJ operator: [(text) num (text) ...] TJ
		if strings.Contains(line, "TJ") {
			if m := tjArrayRe.FindStringSubmatch(line); m != nil {
				tjText := extractTJText(m[1])
				if tjText != "" {
					fragments = append(fragments, textFragment{
						text: tjText,
						x:    lastTmX,
						y:    lastTmY,
					})
				}
			}
			continue
		}

		// Also catch Tj when it's on same line with text (no newline before operator)
		if m := parenTextRe.FindStringSubmatch(line); m != nil && !strings.Contains(line, "TJ") {
			text := unescapePDFString(m[1])
			if text != "" {
				fragments = append(fragments, textFragment{
					text: text,
					x:    lastTmX,
					y:    lastTmY,
				})
			}
		}
	}

	return fragments
}

// extractTJText parses the content of a TJ array (between the brackets).
// TJ arrays contain alternating strings and positioning numbers:
//   (Hello) -20 (World) 30 (!)
func extractTJText(inner string) string {
	var parts []string
	// Find all parenthesized strings inside the TJ array
	matches := parenTextRe.FindAllStringSubmatch(inner, -1)
	for _, m := range matches {
		parts = append(parts, unescapePDFString(m[1]))
	}
	return strings.Join(parts, "")
}

func parseFloat64(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
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
	lines := strings.Split(raw, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Remove lines that are purely PDF operators or coordinates
		if pdfOperators.MatchString(line) && !containsReadableText(line) {
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

// containsReadableText returns true if the line has printable, non-operator content.
func containsReadableText(s string) bool {
	// Check if there's at least one word character outside PDF operators
	cleaned := pdfOperators.ReplaceAllString(s, "")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return false
	}
	// Must have at least some alphabetic content
	for _, r := range cleaned {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}
