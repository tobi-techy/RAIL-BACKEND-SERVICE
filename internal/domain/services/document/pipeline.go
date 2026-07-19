package document

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// Pipeline orchestrates OCR -> classify -> extract -> validate -> enrich.
// Each dependency is an interface so any stage can be swapped independently.
type Pipeline struct {
	ocr        OCREngine
	classifier Classifier
	extractor  FieldExtractor
	validator  Validator
	enricher   Enricher // optional; nil disables LLM enrichment
	logger     *zap.Logger

	// minConfidence gates whether we trust extraction below the OCR floor.
	minOCRConfidence float64
}

// PipelineConfig configures the document pipeline.
type PipelineConfig struct {
	OCR              OCREngine
	Classifier       Classifier
	Extractor        FieldExtractor
	Validator        Validator
	Enricher         Enricher
	MinOCRConfidence float64
	Logger           *zap.Logger
}

// NewPipeline builds a pipeline, filling in default rule-based stages when omitted.
func NewPipeline(cfg PipelineConfig) *Pipeline {
	if cfg.Classifier == nil {
		cfg.Classifier = NewRuleClassifier()
	}
	if cfg.Extractor == nil {
		cfg.Extractor = NewRuleFieldExtractor()
	}
	if cfg.Validator == nil {
		cfg.Validator = NewRuleValidator()
	}
	if cfg.MinOCRConfidence == 0 {
		cfg.MinOCRConfidence = 0.5
	}
	return &Pipeline{
		ocr:              cfg.OCR,
		classifier:       cfg.Classifier,
		extractor:        cfg.Extractor,
		validator:        cfg.Validator,
		enricher:         cfg.Enricher,
		logger:           cfg.Logger,
		minOCRConfidence: cfg.MinOCRConfidence,
	}
}

// Result is the full pipeline output for one document.
type Result struct {
	OCR        *OCRResult
	Type       DocType
	Fields     *Fields
	Validation *Validation
	Enrichment *Enrichment
}

// Process runs the full pipeline. It does not persist anything — callers own storage.
func (p *Pipeline) Process(ctx context.Context, data []byte, mimeType string) (*Result, error) {
	if p.ocr == nil {
		return nil, fmt.Errorf("no OCR engine configured")
	}

	ocrRes, err := p.ocr.Recognize(ctx, data, mimeType)
	if err != nil {
		return nil, fmt.Errorf("ocr: %w", err)
	}
	if ocrRes.Text == "" {
		return nil, fmt.Errorf("ocr produced no text")
	}

	docType := p.classifier.Classify(ocrRes.Text, ocrRes.Lines)

	fields, err := p.extractor.Extract(docType, ocrRes.Text, ocrRes.Lines)
	if err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}

	validation := p.validator.Validate(docType, fields)

	result := &Result{
		OCR:        ocrRes,
		Type:       docType,
		Fields:     fields,
		Validation: validation,
	}

	if p.logger != nil {
		p.logger.Info("document processed",
			zap.String("type", string(docType)),
			zap.Float64("ocr_confidence", ocrRes.MeanConfidence),
			zap.Bool("valid", validation.Passed),
			zap.Float64("validation_confidence", validation.Confidence),
		)
	}

	// Only spend LLM tokens on documents that passed deterministic validation.
	if p.enricher != nil && validation.Passed {
		enr, err := p.enricher.Categorize(ctx, fields)
		if err != nil {
			if p.logger != nil {
				p.logger.Warn("enrichment failed, continuing without it", zap.Error(err))
			}
		} else {
			result.Enrichment = enr
		}
	}

	return result, nil
}
