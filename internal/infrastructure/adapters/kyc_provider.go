package adapters

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/text/language"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// KYCProviderConfig holds KYC provider configuration
type KYCProviderConfig struct {
	Provider    string
	APIKey      string
	APISecret   string
	BaseURL     string
	Environment string // "development", "staging", "production"
	CallbackURL string
	UserAgent   string
	LevelName   string
}

const (
	maxKYCDocumentSize    = 20 << 20 // 20MB
	sumsubTokenTTLSeconds = 15 * 60
	sumsubWebBaseURL      = "https://web.sumsub.com"
	sumsubDefaultLevel    = "basic-kyc"
)

// KYCProvider implements the KYC provider interface
type KYCProvider struct {
	logger     *zap.Logger
	config     KYCProviderConfig
	httpClient *http.Client
	fileClient *http.Client
	provider   string
}

// NewKYCProvider creates a new KYC provider
func NewKYCProvider(logger *zap.Logger, config KYCProviderConfig) (*KYCProvider, error) {
	provider := strings.ToLower(strings.TrimSpace(config.Provider))
	if provider == "" {
		return nil, fmt.Errorf("kyc provider is required")
	}

	switch provider {
	case "sumsub":
		if strings.TrimSpace(config.APIKey) == "" {
			return nil, fmt.Errorf("sumsub api key is required")
		}
		if strings.TrimSpace(config.APISecret) == "" {
			return nil, fmt.Errorf("sumsub api secret is required")
		}
		if strings.TrimSpace(config.BaseURL) == "" {
			return nil, fmt.Errorf("sumsub base url is required")
		}
	case "jumio":
		if strings.TrimSpace(config.APIKey) == "" {
			return nil, fmt.Errorf("jumio api key is required")
		}
		if strings.TrimSpace(config.APISecret) == "" {
			return nil, fmt.Errorf("jumio api secret is required")
		}
		if strings.TrimSpace(config.BaseURL) == "" {
			return nil, fmt.Errorf("jumio base url is required")
		}
	default:
		return nil, fmt.Errorf("unsupported kyc provider: %s", provider)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	fileClient := &http.Client{Timeout: 30 * time.Second}

	return &KYCProvider{
		logger:     logger,
		config:     config,
		httpClient: httpClient,
		fileClient: fileClient,
		provider:   provider,
	}, nil
}

// Jumio API models
type JumioInitiateRequest struct {
	CustomerInternalReference string `json:"customerInternalReference"`
	UserReference             string `json:"userReference"`
	WorkflowDefinition        struct {
		Key     string `json:"key"`
		Version string `json:"version,omitempty"`
	} `json:"workflowDefinition"`
	CallbackURL   string `json:"callbackUrl,omitempty"`
	SuccessURL    string `json:"successUrl,omitempty"`
	ErrorURL      string `json:"errorUrl,omitempty"`
	TokenLifetime string `json:"tokenLifetime,omitempty"`
}

type JumioInitiateResponse struct {
	Timestamp string `json:"timestamp"`
	Account   struct {
		ID string `json:"id"`
	} `json:"account"`
	WorkflowExecution struct {
		ID                        string `json:"id"`
		Status                    string `json:"status"`
		CustomerInternalReference string `json:"customerInternalReference"`
		UserReference             string `json:"userReference"`
	} `json:"workflowExecution"`
	Web struct {
		Href string `json:"href"`
	} `json:"web"`
	SDK struct {
		Token string `json:"token"`
	} `json:"sdk"`
}

type JumioStatusResponse struct {
	Timestamp string `json:"timestamp"`
	Account   struct {
		ID string `json:"id"`
	} `json:"account"`
	WorkflowExecution struct {
		ID                        string `json:"id"`
		Status                    string `json:"status"`
		CustomerInternalReference string `json:"customerInternalReference"`
		UserReference             string `json:"userReference"`
		DefinitionKey             string `json:"definitionKey"`
		Credentials               []struct {
			ID       string `json:"id"`
			Category string `json:"category"`
			Parts    []struct {
				Classifier string `json:"classifier"`
				Validity   string `json:"validity"`
			} `json:"parts"`
		} `json:"credentials"`
	} `json:"workflowExecution"`
}

type JumioErrorResponse struct {
	Timestamp string `json:"timestamp"`
	TraceID   string `json:"traceId"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Details   []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Field   string `json:"field,omitempty"`
	} `json:"details,omitempty"`
}

func (e JumioErrorResponse) Error() string {
	return fmt.Sprintf("Jumio API error %s: %s", e.Code, e.Message)
}

type sumsubApplicantResponse struct {
	ID             string `json:"id"`
	ExternalUserID string `json:"externalUserId"`
}

type sumsubStatusResponse struct {
	ReviewStatus string `json:"reviewStatus"`
	ReviewResult struct {
		ReviewAnswer string `json:"reviewAnswer"`
		RejectLabels []struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"rejectLabels"`
	} `json:"reviewResult"`
}

type sumsubAccessTokenResponse struct {
	Token string `json:"token"`
}

// makeJumioRequest makes an HTTP request to Jumio API
func (k *KYCProvider) makeJumioRequest(ctx context.Context, method, endpoint string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, k.config.BaseURL+endpoint, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set authentication headers
	req.SetBasicAuth(k.config.APIKey, k.config.APISecret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", k.config.UserAgent)

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}

	return resp, nil
}

func (k *KYCProvider) EnsureApplicant(ctx context.Context, userID uuid.UUID, personalInfo *entities.KYCPersonalInfo) (string, error) {
	switch k.provider {
	case "sumsub":
		return k.ensureSumsubApplicant(ctx, userID, personalInfo)
	default:
		return "", fmt.Errorf("ensure applicant not supported for provider %s", k.provider)
	}
}

func (k *KYCProvider) sumsubLevelCandidates() []string {
	configured := strings.TrimSpace(k.config.LevelName)
	candidates := make([]string, 0, 2)

	if configured != "" {
		candidates = append(candidates, configured)
	}

	if configured == "" || !strings.EqualFold(configured, sumsubDefaultLevel) {
		candidates = append(candidates, sumsubDefaultLevel)
	}

	return candidates
}

// SubmitKYC submits KYC documents to the provider
func (k *KYCProvider) SubmitKYC(ctx context.Context, userID uuid.UUID, documents []entities.KYCDocumentUpload, personalInfo *entities.KYCPersonalInfo) (string, error) {
	k.logger.Info("Submitting KYC documents",
		zap.String("user_id", userID.String()),
		zap.Int("document_count", len(documents)))

	switch k.provider {
	case "sumsub":
		return k.submitKYCToSumsub(ctx, userID, documents, personalInfo)
	case "jumio":
		return k.submitKYCToJumio(ctx, userID, documents, personalInfo)
	default:
		return "", fmt.Errorf("unsupported kyc provider: %s", k.provider)
	}
}

func (k *KYCProvider) submitKYCToJumio(ctx context.Context, userID uuid.UUID, documents []entities.KYCDocumentUpload, personalInfo *entities.KYCPersonalInfo) (string, error) {
	request := JumioInitiateRequest{
		CustomerInternalReference: userID.String(),
		UserReference:             fmt.Sprintf("user_%s", userID.String()[:8]),
		CallbackURL:               k.config.CallbackURL,
		TokenLifetime:             "30m",
	}

	request.WorkflowDefinition.Key = "id_verification"
	if len(documents) > 1 {
		request.WorkflowDefinition.Key = "id_and_identity_verification"
	}

	resp, err := k.makeJumioRequest(ctx, http.MethodPost, "/api/v4/workflow/initiate", request)
	if err != nil {
		k.logger.Error("Failed to initiate Jumio workflow",
			zap.String("user_id", userID.String()),
			zap.Error(err))
		return "", fmt.Errorf("failed to initiate KYC workflow: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var jumioErr JumioErrorResponse
		if err := json.Unmarshal(respBody, &jumioErr); err == nil {
			k.logger.Error("Jumio API error",
				zap.String("user_id", userID.String()),
				zap.String("error_code", jumioErr.Code),
				zap.String("error_message", jumioErr.Message))
			return "", jumioErr
		}
		return "", fmt.Errorf("KYC API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var jumioResp JumioInitiateResponse
	if err := json.Unmarshal(respBody, &jumioResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	k.logger.Info("KYC workflow initiated successfully",
		zap.String("user_id", userID.String()),
		zap.String("workflow_id", jumioResp.WorkflowExecution.ID),
		zap.String("status", jumioResp.WorkflowExecution.Status))

	return jumioResp.WorkflowExecution.ID, nil
}

func (k *KYCProvider) submitKYCToSumsub(ctx context.Context, userID uuid.UUID, documents []entities.KYCDocumentUpload, personalInfo *entities.KYCPersonalInfo) (string, error) {
	applicantID, err := k.ensureSumsubApplicant(ctx, userID, personalInfo)
	if err != nil {
		return "", err
	}

	for _, doc := range documents {
		if err := k.uploadSumsubDocument(ctx, applicantID, doc, personalInfo); err != nil {
			return "", err
		}
	}

	if err := k.markSumsubApplicantPending(ctx, applicantID); err != nil {
		return "", err
	}

	return applicantID, nil
}

func (k *KYCProvider) ensureSumsubApplicant(ctx context.Context, userID uuid.UUID, personalInfo *entities.KYCPersonalInfo) (string, error) {
	payload := map[string]any{
		"externalUserId": userID.String(),
	}

	info := map[string]any{}
	if personalInfo != nil {
		if personalInfo.FirstName != "" {
			info["firstName"] = personalInfo.FirstName
		}
		if personalInfo.LastName != "" {
			info["lastName"] = personalInfo.LastName
		}
		if personalInfo.DateOfBirth != nil {
			info["dob"] = personalInfo.DateOfBirth.Format("2006-01-02")
		}
		if personalInfo.Country != "" {
			info["country"] = sumsubCountryCode(personalInfo.Country)
		}
		if personalInfo.Address != nil {
			address := map[string]any{
				"street":   personalInfo.Address.Street,
				"town":     personalInfo.Address.City,
				"postCode": personalInfo.Address.PostalCode,
				"country":  sumsubCountryCode(personalInfo.Address.Country),
			}
			if personalInfo.Address.State != "" {
				address["state"] = personalInfo.Address.State
			}
			info["address"] = address
		}
	}

	if len(info) > 0 {
		payload["info"] = info
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal sumsub applicant payload: %w", err)
	}

	candidates := k.sumsubLevelCandidates()

	for idx, levelName := range candidates {
		endpoint := fmt.Sprintf("/resources/applicants?levelName=%s", url.QueryEscape(levelName))
		resp, err := k.makeSumsubRequest(ctx, http.MethodPost, endpoint, body, "application/json")
		if err != nil {
			return "", err
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("failed to read sumsub applicant response: %w", err)
		}

		if resp.StatusCode == http.StatusConflict {
			k.logger.Info("Sumsub applicant already exists",
				zap.String("user_id", userID.String()),
				zap.String("level_name", levelName))
			return k.lookupSumsubApplicant(ctx, userID.String())
		}

		if resp.StatusCode >= 400 {
			if resp.StatusCode == http.StatusNotFound && sumsubLevelNotFound(respBody) && idx+1 < len(candidates) {
				k.logger.Warn("Sumsub level not found, retrying with fallback",
					zap.String("user_id", userID.String()),
					zap.String("level_name", levelName),
					zap.String("fallback_level", candidates[idx+1]))
				continue
			}

			k.logger.Error("Failed to create Sumsub applicant",
				zap.String("user_id", userID.String()),
				zap.Int("status_code", resp.StatusCode),
				zap.String("response_body", string(respBody)),
				zap.String("level_name", levelName))
			return "", fmt.Errorf("sumsub applicant creation failed: status %d", resp.StatusCode)
		}

		var applicant sumsubApplicantResponse
		if err := json.Unmarshal(respBody, &applicant); err != nil {
			return "", fmt.Errorf("failed to parse sumsub applicant response: %w", err)
		}

		if applicant.ID == "" {
			return "", fmt.Errorf("sumsub applicant response missing id")
		}

		k.logger.Info("Sumsub applicant initialized",
			zap.String("user_id", userID.String()),
			zap.String("applicant_id", applicant.ID),
			zap.String("level_name", levelName))

		return applicant.ID, nil
	}

	return "", fmt.Errorf("sumsub applicant creation failed: exhausted level candidates")
}

func (k *KYCProvider) lookupSumsubApplicant(ctx context.Context, externalUserID string) (string, error) {
	endpoint := fmt.Sprintf("/resources/applicants/-;externalUserId=%s", url.PathEscape(externalUserID))
	resp, err := k.makeSumsubRequest(ctx, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read sumsub applicant lookup response: %w", err)
	}

	if resp.StatusCode >= 400 {
		k.logger.Error("Failed to lookup Sumsub applicant",
			zap.String("external_user_id", externalUserID),
			zap.Int("status_code", resp.StatusCode),
			zap.String("response_body", string(respBody)))
		return "", fmt.Errorf("sumsub applicant lookup failed: status %d", resp.StatusCode)
	}

	var applicant sumsubApplicantResponse
	if err := json.Unmarshal(respBody, &applicant); err != nil {
		return "", fmt.Errorf("failed to parse sumsub applicant lookup response: %w", err)
	}

	if applicant.ID == "" {
		return "", fmt.Errorf("sumsub applicant lookup returned empty id")
	}

	return applicant.ID, nil
}

func (k *KYCProvider) uploadSumsubDocument(ctx context.Context, applicantID string, doc entities.KYCDocumentUpload, personalInfo *entities.KYCPersonalInfo) error {
	if strings.TrimSpace(doc.FileURL) == "" {
		return fmt.Errorf("document file URL is empty")
	}

	downloadReq, err := http.NewRequestWithContext(ctx, http.MethodGet, doc.FileURL, nil)
	if err != nil {
		return fmt.Errorf("failed to prepare document download request: %w", err)
	}

	resp, err := k.fileClient.Do(downloadReq)
	if err != nil {
		return fmt.Errorf("failed to download document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("failed to download document: status %d", resp.StatusCode)
	}

	limitedReader := io.LimitReader(resp.Body, maxKYCDocumentSize)
	fileBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		return fmt.Errorf("failed to read document content: %w", err)
	}

	metadata := map[string]any{
		"idDocType": mapSumsubDocType(doc.Type),
	}
	if personalInfo != nil && personalInfo.Country != "" {
		metadata["country"] = sumsubCountryCode(personalInfo.Country)
	}

	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal document metadata: %w", err)
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("metadata", string(metadataBytes)); err != nil {
		return fmt.Errorf("failed to write document metadata: %w", err)
	}

	fileName := fmt.Sprintf("%s_%d", strings.ToLower(mapSumsubDocType(doc.Type)), time.Now().Unix())
	if parsed, err := url.Parse(doc.FileURL); err == nil {
		if base := path.Base(parsed.Path); base != "" && base != "." && base != "/" {
			fileName = base
		}
	}

	fileWriter, err := writer.CreateFormFile("content", fileName)
	if err != nil {
		return fmt.Errorf("failed to create multipart file field: %w", err)
	}

	if _, err := fileWriter.Write(fileBytes); err != nil {
		return fmt.Errorf("failed to write document content: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to finalize multipart payload: %w", err)
	}

	endpoint := fmt.Sprintf("/resources/applicants/%s/info/idDoc", applicantID)
	respUpload, err := k.makeSumsubRequest(ctx, http.MethodPost, endpoint, buf.Bytes(), writer.FormDataContentType())
	if err != nil {
		return err
	}
	defer respUpload.Body.Close()

	respBody, err := io.ReadAll(respUpload.Body)
	if err != nil {
		return fmt.Errorf("failed to read sumsub document upload response: %w", err)
	}

	if respUpload.StatusCode >= 400 {
		k.logger.Error("Sumsub document upload failed",
			zap.String("applicant_id", applicantID),
			zap.Int("status_code", respUpload.StatusCode),
			zap.String("response_body", string(respBody)))
		return fmt.Errorf("sumsub document upload failed: status %d", respUpload.StatusCode)
	}

	k.logger.Info("Uploaded document to Sumsub",
		zap.String("applicant_id", applicantID),
		zap.String("document_type", doc.Type))

	return nil
}

func (k *KYCProvider) markSumsubApplicantPending(ctx context.Context, applicantID string) error {
	resp, err := k.makeSumsubRequest(ctx, http.MethodPost, fmt.Sprintf("/resources/applicants/%s/status/pending", applicantID), nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		k.logger.Error("Failed to mark Sumsub applicant pending",
			zap.String("applicant_id", applicantID),
			zap.Int("status_code", resp.StatusCode),
			zap.String("response_body", string(respBody)))
		return fmt.Errorf("sumsub pending update failed: status %d", resp.StatusCode)
	}

	k.logger.Info("Sumsub applicant moved to pending",
		zap.String("applicant_id", applicantID))

	return nil
}

func (k *KYCProvider) makeSumsubRequest(ctx context.Context, method, endpoint string, body []byte, contentType string) (*http.Response, error) {
	if contentType == "" {
		contentType = "application/json"
	}
	if body == nil {
		body = []byte{}
	}

	fullURL := strings.TrimRight(k.config.BaseURL, "/") + endpoint

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create sumsub request: %w", err)
	}

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	signaturePayload := ts + strings.ToUpper(method) + endpoint + string(body)
	mac := hmac.New(sha256.New, []byte(k.config.APISecret))
	mac.Write([]byte(signaturePayload))
	signature := hex.EncodeToString(mac.Sum(nil))

	req.Header.Set("X-App-Token", k.config.APIKey)
	req.Header.Set("X-App-Access-Ts", ts)
	req.Header.Set("X-App-Access-Sig", signature)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if k.config.UserAgent != "" {
		req.Header.Set("User-Agent", k.config.UserAgent)
	}

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute sumsub request: %w", err)
	}

	return resp, nil
}

// GetKYCStatus retrieves the current KYC status from the provider
func (k *KYCProvider) GetKYCStatus(ctx context.Context, providerRef string) (*entities.KYCSubmission, error) {
	k.logger.Info("Getting KYC status from provider",
		zap.String("provider_ref", providerRef))

	switch k.provider {
	case "sumsub":
		return k.getSumsubKYCStatus(ctx, providerRef)
	case "jumio":
		return k.getJumioKYCStatus(ctx, providerRef)
	default:
		return nil, fmt.Errorf("kyc status lookup not supported for provider %s", k.provider)
	}
}

func (k *KYCProvider) getJumioKYCStatus(ctx context.Context, providerRef string) (*entities.KYCSubmission, error) {
	endpoint := fmt.Sprintf("/api/v4/workflow/executions/%s", providerRef)
	resp, err := k.makeJumioRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get KYC status: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var jumioErr JumioErrorResponse
		if err := json.Unmarshal(respBody, &jumioErr); err == nil {
			k.logger.Error("Jumio API error",
				zap.String("provider_ref", providerRef),
				zap.String("error_code", jumioErr.Code),
				zap.String("error_message", jumioErr.Message))
			return nil, jumioErr
		}
		return nil, fmt.Errorf("KYC API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var jumioResp JumioStatusResponse
	if err := json.Unmarshal(respBody, &jumioResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	status := entities.KYCStatusProcessing
	rejectionReasons := make([]string, 0)

	switch strings.ToUpper(jumioResp.WorkflowExecution.Status) {
	case "PENDING":
		status = entities.KYCStatusPending
	case "PROCESSING":
		status = entities.KYCStatusProcessing
	case "PASSED":
		status = entities.KYCStatusApproved
	case "FAILED", "REJECTED":
		status = entities.KYCStatusRejected
		for _, cred := range jumioResp.WorkflowExecution.Credentials {
			for _, part := range cred.Parts {
				if strings.ToUpper(part.Validity) != "PASSED" {
					rejectionReasons = append(rejectionReasons, fmt.Sprintf("%s:%s", cred.Category, part.Classifier))
				}
			}
		}
	default:
		status = entities.KYCStatusProcessing
	}

	verificationData := map[string]any{
		"status":        jumioResp.WorkflowExecution.Status,
		"workflow_id":   jumioResp.WorkflowExecution.ID,
		"definitionKey": jumioResp.WorkflowExecution.DefinitionKey,
	}

	submission := &entities.KYCSubmission{
		Provider:         "jumio",
		ProviderRef:      providerRef,
		Status:           status,
		RejectionReasons: rejectionReasons,
		VerificationData: verificationData,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	k.logger.Info("KYC status retrieved from Jumio",
		zap.String("provider_ref", providerRef),
		zap.String("status", string(status)))

	return submission, nil
}

func (k *KYCProvider) getSumsubKYCStatus(ctx context.Context, providerRef string) (*entities.KYCSubmission, error) {
	endpoint := fmt.Sprintf("/resources/applicants/%s/status", providerRef)
	resp, err := k.makeSumsubRequest(ctx, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read sumsub status response: %w", err)
	}

	if resp.StatusCode >= 400 {
		k.logger.Error("Sumsub status request failed",
			zap.String("provider_ref", providerRef),
			zap.Int("status_code", resp.StatusCode),
			zap.String("response_body", string(respBody)))
		return nil, fmt.Errorf("sumsub status request failed: status %d", resp.StatusCode)
	}

	var statusResp sumsubStatusResponse
	if err := json.Unmarshal(respBody, &statusResp); err != nil {
		return nil, fmt.Errorf("failed to parse sumsub status response: %w", err)
	}

	status := entities.KYCStatusProcessing
	rejectionReasons := make([]string, 0)

	answer := strings.ToUpper(statusResp.ReviewResult.ReviewAnswer)
	if strings.EqualFold(statusResp.ReviewStatus, "completed") {
		switch answer {
		case "GREEN":
			status = entities.KYCStatusApproved
		case "RED":
			status = entities.KYCStatusRejected
		default:
			status = entities.KYCStatusProcessing
		}
	}

	for _, label := range statusResp.ReviewResult.RejectLabels {
		if label.Description != "" {
			rejectionReasons = append(rejectionReasons, label.Description)
		} else if label.Code != "" {
			rejectionReasons = append(rejectionReasons, label.Code)
		}
	}

	verificationData := map[string]any{
		"reviewStatus": statusResp.ReviewStatus,
		"reviewAnswer": statusResp.ReviewResult.ReviewAnswer,
	}

	submission := &entities.KYCSubmission{
		Provider:         "sumsub",
		ProviderRef:      providerRef,
		Status:           status,
		RejectionReasons: rejectionReasons,
		VerificationData: verificationData,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	k.logger.Info("KYC status retrieved from Sumsub",
		zap.String("provider_ref", providerRef),
		zap.String("status", string(status)))

	return submission, nil
}

// GenerateKYCURL generates a URL for users to complete KYC verification
func (k *KYCProvider) GenerateKYCURL(ctx context.Context, userID uuid.UUID) (string, error) {
	k.logger.Info("Generating KYC URL",
		zap.String("user_id", userID.String()))

	switch k.provider {
	case "sumsub":
		return k.generateSumsubKYCURL(ctx, userID)
	case "jumio":
		return k.generateJumioKYCURL(ctx, userID)
	default:
		return "", fmt.Errorf("unsupported KYC provider: %s", k.provider)
	}
}

func (k *KYCProvider) generateJumioKYCURL(ctx context.Context, userID uuid.UUID) (string, error) {
	request := JumioInitiateRequest{
		CustomerInternalReference: userID.String(),
		UserReference:             fmt.Sprintf("user_%s", userID.String()[:8]),
		CallbackURL:               k.config.CallbackURL,
		TokenLifetime:             "30m",
	}
	request.WorkflowDefinition.Key = "id_verification"

	resp, err := k.makeJumioRequest(ctx, http.MethodPost, "/api/v4/workflow/initiate", request)
	if err != nil {
		return "", fmt.Errorf("failed to initiate Jumio workflow: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read Jumio response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var jumioErr JumioErrorResponse
		if err := json.Unmarshal(respBody, &jumioErr); err == nil {
			return "", jumioErr
		}
		return "", fmt.Errorf("jumio KYC URL error: status %d", resp.StatusCode)
	}

	var jumioResp JumioInitiateResponse
	if err := json.Unmarshal(respBody, &jumioResp); err != nil {
		return "", fmt.Errorf("failed to parse Jumio response: %w", err)
	}

	return jumioResp.Web.Href, nil
}

func (k *KYCProvider) generateSumsubKYCURL(ctx context.Context, userID uuid.UUID) (string, error) {
	applicantID, err := k.ensureSumsubApplicant(ctx, userID, nil)
	if err != nil {
		return "", err
	}

	token, err := k.generateSumsubAccessToken(ctx, applicantID)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s/#/clientToken/%s", strings.TrimRight(sumsubWebBaseURL, "/"), token), nil
}

func (k *KYCProvider) generateSumsubAccessToken(ctx context.Context, applicantID string) (string, error) {
	candidates := k.sumsubLevelCandidates()

	for idx, levelName := range candidates {
		payload := map[string]any{
			"userId":       applicantID,
			"levelName":    levelName,
			"ttlInSeconds": sumsubTokenTTLSeconds,
		}

		body, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("failed to marshal access token payload: %w", err)
		}

		resp, err := k.makeSumsubRequest(ctx, http.MethodPost, "/resources/accessTokens", body, "application/json")
		if err != nil {
			return "", err
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("failed to read sumsub token response: %w", err)
		}

		if resp.StatusCode >= 400 {
			if resp.StatusCode == http.StatusNotFound && sumsubLevelNotFound(respBody) && idx+1 < len(candidates) {
				k.logger.Warn("Sumsub level not found while generating access token, retrying with fallback",
					zap.String("applicant_id", applicantID),
					zap.String("level_name", levelName),
					zap.String("fallback_level", candidates[idx+1]))
				continue
			}

			k.logger.Error("Failed to generate Sumsub access token",
				zap.String("applicant_id", applicantID),
				zap.Int("status_code", resp.StatusCode),
				zap.String("response_body", string(respBody)),
				zap.String("level_name", levelName))
			return "", fmt.Errorf("sumsub access token error: status %d", resp.StatusCode)
		}

		var tokenResp sumsubAccessTokenResponse
		if err := json.Unmarshal(respBody, &tokenResp); err != nil {
			return "", fmt.Errorf("failed to parse sumsub token response: %w", err)
		}

		if strings.TrimSpace(tokenResp.Token) == "" {
			return "", fmt.Errorf("sumsub returned empty access token")
		}

		return tokenResp.Token, nil
	}

	return "", fmt.Errorf("sumsub access token error: exhausted level candidates")
}

func sumsubLevelNotFound(respBody []byte) bool {
	body := strings.ToLower(string(respBody))
	return strings.Contains(body, "level") && strings.Contains(body, "not found")
}

func sumsubCountryCode(country string) string {
	code := strings.ToUpper(strings.TrimSpace(country))
	if code == "" {
		return ""
	}

	// If already alpha-3, keep as is
	if len(code) == 3 {
		return code
	}

	region, err := language.ParseRegion(code)
	if err != nil {
		return code
	}

	if iso3 := region.ISO3(); iso3 != "" {
		return iso3
	}

	return code
}

func mapSumsubDocType(docType string) string {
	switch strings.ToLower(docType) {
	case "passport":
		return "PASSPORT"
	case "drivers_license", "driver_license", "driving_license":
		return "DRIVERS_LICENSE"
	case "id_card", "national_id":
		return "ID_CARD"
	default:
		return "OTHER"
	}
}
