package document

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

type mockOCR struct {
	result *OCRResult
	err    error
}

func (m *mockOCR) Recognize(_ context.Context, _ []byte, _ string) (*OCRResult, error) {
	return m.result, m.err
}

func TestPipelineProcessReceipt(t *testing.T) {
	ocr := &mockOCR{result: &OCRResult{
		Text:           "STARBUCKS\nSubtotal 15.00\nVAT 3.90\nTotal 18.90",
		MeanConfidence: 0.95,
		Engine:         "paddleocr",
	}}
	p := NewPipeline(PipelineConfig{OCR: ocr, Logger: zap.NewNop()})

	res, err := p.Process(context.Background(), []byte("x"), "image/jpeg")
	if err != nil {
		t.Fatalf("Process error: %v", err)
	}
	if res.Type != DocReceipt {
		t.Errorf("type = %v, want receipt", res.Type)
	}
	if res.Validation == nil || !res.Validation.Passed {
		t.Errorf("expected passed validation, got %+v", res.Validation)
	}
}

func TestPipelineProcessOCRError(t *testing.T) {
	p := NewPipeline(PipelineConfig{OCR: &mockOCR{err: errors.New("boom")}, Logger: zap.NewNop()})
	if _, err := p.Process(context.Background(), []byte("x"), "image/jpeg"); err == nil {
		t.Error("expected error when OCR fails")
	}
}

func TestPipelineProcessEmptyText(t *testing.T) {
	p := NewPipeline(PipelineConfig{OCR: &mockOCR{result: &OCRResult{Text: ""}}, Logger: zap.NewNop()})
	if _, err := p.Process(context.Background(), []byte("x"), "image/jpeg"); err == nil {
		t.Error("expected error when OCR text is empty")
	}
}

func TestPipelineNoEngine(t *testing.T) {
	p := NewPipeline(PipelineConfig{Logger: zap.NewNop()})
	if _, err := p.Process(context.Background(), []byte("x"), "image/jpeg"); err == nil {
		t.Error("expected error with no OCR engine")
	}
}
