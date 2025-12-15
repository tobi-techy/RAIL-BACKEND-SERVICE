package due

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/retry"
)

// Config represents Due API configuration
type Config struct {
	APIKey     string
	AccountID  string
	BaseURL    string
	Timeout    time.Duration
	MaxRetries int
}

// Client represents a Due API client
type Client struct {
	config     Config
	httpClient *http.Client
	logger     *logger.Logger
}

// NewClient creates a new Due API client
func NewClient(config Config, logger *logger.Logger) *Client {
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	if config.BaseURL == "" {
		config.BaseURL = "https://api.due.network"
	}

	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}

	httpClient := &http.Client{
		Timeout: config.Timeout,
	}

	return &Client{
		config:     config,
		httpClient: httpClient,
		logger:     logger,
	}
}

// CreateVirtualAccountRequest represents a request to create a virtual account via Due API
type CreateVirtualAccountRequest struct {
	Destination  string `json:"destination"`  // Crypto address or recipient ID for settlement
	SchemaIn     string `json:"schemaIn"`     // Input payment method (bank_sepa, bank_us, evm, tron)
	CurrencyIn   string `json:"currencyIn"`   // Input currency (EUR, USD, USDC, USDT)
	RailOut      string `json:"railOut"`      // Settlement rail (ethereum, polygon, sepa, ach)
	CurrencyOut  string `json:"currencyOut"`  // Output currency (USDC, EURC, EUR, USD)
	Reference    string `json:"reference"`    // Unique reference for tracking
}

// VirtualAccountDetails contains the receiving account details
type VirtualAccountDetails struct {
	IBAN            string `json:"IBAN,omitempty"`
	AccountNumber   string `json:"accountNumber,omitempty"`
	RoutingNumber   string `json:"routingNumber,omitempty"`
	BankName        string `json:"bankName,omitempty"`
	BeneficiaryName string `json:"beneficiaryName,omitempty"`
	Address         string `json:"address,omitempty"` // For crypto virtual accounts
}

// CreateVirtualAccountResponse represents the response from creating a virtual account
type CreateVirtualAccountResponse struct {
	OwnerID       string                `json:"ownerId"`
	DestinationID string                `json:"destinationId"`
	SchemaIn      string                `json:"schemaIn"`
	CurrencyIn    string                `json:"currencyIn"`
	RailOut       string                `json:"railOut"`
	CurrencyOut   string                `json:"currencyOut"`
	Nonce         string                `json:"nonce"` // Your reference (stored as unique identifier)
	Details       VirtualAccountDetails `json:"details"`
	IsActive      bool                  `json:"isActive"`
	CreatedAt     string                `json:"createdAt"`
}

// CreateAccount creates a Due account
func (c *Client) CreateAccount(ctx context.Context, req *entities.CreateAccountRequest) (*entities.CreateAccountResponse, error) {
	c.logger.Info("Creating Due account", "email", req.Email, "type", req.Type)

	var response entities.CreateAccountResponse
	if err := c.doRequest(ctx, "POST", "accounts", req, &response); err != nil {
		c.logger.Error("Failed to create Due account", "error", err)
		return nil, fmt.Errorf("create account failed: %w", err)
	}

	c.logger.Info("Created Due account", "account_id", response.AccountID, "status", response.Status)
	return &response, nil
}

// GetAccount retrieves a Due account by ID
func (c *Client) GetAccount(ctx context.Context, accountID string) (*entities.CreateAccountResponse, error) {
	endpoint := fmt.Sprintf("accounts/%s", accountID)
	var response entities.CreateAccountResponse
	if err := c.doRequest(ctx, "GET", endpoint, nil, &response); err != nil {
		return nil, fmt.Errorf("get account failed: %w", err)
	}
	return &response, nil
}

// GetAccountCategories retrieves available account categories
func (c *Client) GetAccountCategories(ctx context.Context) (*AccountCategoriesResponse, error) {
	var response AccountCategoriesResponse
	if err := c.doRequestWithRetry(ctx, "GET", "account_categories", nil, &response); err != nil {
		return nil, fmt.Errorf("get account categories failed: %w", err)
	}
	return &response, nil
}

// LinkWallet links a wallet to a Due account
func (c *Client) LinkWallet(ctx context.Context, req *LinkWalletRequest) (*LinkWalletResponse, error) {
	c.logger.Info("Linking wallet to Due account", "address", req.Address)

	var response LinkWalletResponse
	if err := c.doRequest(ctx, "POST", "wallets", req, &response); err != nil {
		c.logger.Error("Failed to link wallet", "error", err)
		return nil, fmt.Errorf("link wallet failed: %w", err)
	}

	c.logger.Info("Linked wallet successfully", "wallet_id", response.ID)
	return &response, nil
}

// CreateRecipient creates a bank recipient for USD settlement
func (c *Client) CreateRecipient(ctx context.Context, req *CreateRecipientRequest) (*CreateRecipientResponse, error) {
	c.logger.Info("Creating recipient", "name", req.Name)

	var response CreateRecipientResponse
	if err := c.doRequest(ctx, "POST", "recipients", req, &response); err != nil {
		c.logger.Error("Failed to create recipient", "error", err)
		return nil, fmt.Errorf("create recipient failed: %w", err)
	}

	c.logger.Info("Created recipient successfully", "recipient_id", response.ID)
	return &response, nil
}

// CreateVirtualAccount creates a virtual account via Due API
func (c *Client) CreateVirtualAccount(ctx context.Context, req *CreateVirtualAccountRequest) (*CreateVirtualAccountResponse, error) {
	c.logger.Info("Creating virtual account via Due API",
		"destination", req.Destination,
		"schema_in", req.SchemaIn,
		"reference", req.Reference)

	var response CreateVirtualAccountResponse
	if err := c.doRequest(ctx, "POST", "virtual_accounts", req, &response); err != nil {
		c.logger.Error("Failed to create virtual account",
			"reference", req.Reference,
			"error", err)
		return nil, fmt.Errorf("create virtual account failed: %w", err)
	}

	c.logger.Info("Created virtual account successfully",
		"nonce", response.Nonce,
		"destination_id", response.DestinationID,
		"is_active", response.IsActive)

	return &response, nil
}

// GetVirtualAccount retrieves a virtual account by reference key
func (c *Client) GetVirtualAccount(ctx context.Context, reference string) (*CreateVirtualAccountResponse, error) {
	endpoint := fmt.Sprintf("virtual_accounts/%s", reference)
	
	var response CreateVirtualAccountResponse
	if err := c.doRequest(ctx, "GET", endpoint, nil, &response); err != nil {
		c.logger.Error("Failed to get virtual account",
			"reference", reference,
			"error", err)
		return nil, fmt.Errorf("get virtual account failed: %w", err)
	}

	return &response, nil
}

// doRequest performs an HTTP request to the Due API
func (c *Client) doRequest(ctx context.Context, method, endpoint string, body, response interface{}) error {
	// Standardize: always add /v1 prefix unless it's a dev endpoint
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}
	if !strings.HasPrefix(endpoint, "/v1/") && !strings.HasPrefix(endpoint, "/dev/") {
		endpoint = "/v1" + endpoint
	}
	fullURL := c.config.BaseURL + endpoint

	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers as per Due API documentation
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	req.Header.Set("Due-Account-Id", c.config.AccountID)

	c.logger.Debug("Sending Due API request",
		"method", method,
		"url", fullURL)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	c.logger.Debug("Received Due API response",
		"status_code", resp.StatusCode,
		"body_size", len(respBody))

	// Check for error responses
	if resp.StatusCode >= 400 {
		var errResp ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Message != "" {
			errResp.StatusCode = resp.StatusCode
			return &errResp
		}
		return fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	// Parse response if a response object is provided
	if response != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, response); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}

// Config returns the client configuration
func (c *Client) Config() Config {
	return c.config
}

// ListRecipients retrieves all recipients with pagination
func (c *Client) ListRecipients(ctx context.Context, limit, offset int) (*ListRecipientsResponse, error) {
	params := url.Values{}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		params.Set("offset", strconv.Itoa(offset))
	}

	endpoint := "recipients"
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}

	var response ListRecipientsResponse
	if err := c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response); err != nil {
		return nil, fmt.Errorf("list recipients failed: %w", err)
	}
	return &response, nil
}

// GetRecipient retrieves a recipient by ID
func (c *Client) GetRecipient(ctx context.Context, recipientID string) (*CreateRecipientResponse, error) {
	endpoint := fmt.Sprintf("recipients/%s", recipientID)
	var response CreateRecipientResponse
	if err := c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response); err != nil {
		return nil, fmt.Errorf("get recipient failed: %w", err)
	}
	return &response, nil
}

// ListVirtualAccounts retrieves all virtual accounts with required filters
func (c *Client) ListVirtualAccounts(ctx context.Context, filters *VirtualAccountFilters) (*ListVirtualAccountsResponse, error) {
	if filters == nil {
		return nil, fmt.Errorf("filters are required")
	}
	if filters.Destination == "" || filters.SchemaIn == "" || filters.CurrencyIn == "" || filters.RailOut == "" || filters.CurrencyOut == "" {
		return nil, fmt.Errorf("destination, schemaIn, currencyIn, railOut, and currencyOut are required")
	}

	params := url.Values{}
	params.Set("destination", filters.Destination)
	params.Set("schemaIn", filters.SchemaIn)
	params.Set("currencyIn", filters.CurrencyIn)
	params.Set("railOut", filters.RailOut)
	params.Set("currencyOut", filters.CurrencyOut)
	if filters.Reference != "" {
		params.Set("reference", filters.Reference)
	}

	endpoint := "virtual_accounts?" + params.Encode()

	var response ListVirtualAccountsResponse
	if err := c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response); err != nil {
		return nil, fmt.Errorf("list virtual accounts failed: %w", err)
	}
	return &response, nil
}

// ListTransfers retrieves transfers with pagination and filters
func (c *Client) ListTransfers(ctx context.Context, filters *TransferFilters) (*ListTransfersResponse, error) {
	params := url.Values{}
	if filters != nil {
		if filters.Limit > 0 {
			params.Set("limit", strconv.Itoa(filters.Limit))
		}
		if filters.Order != "" {
			params.Set("order", filters.Order)
		}
		if filters.Status != "" {
			params.Set("status", string(filters.Status))
		}
	}

	endpoint := "transfers"
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}

	var response ListTransfersResponse
	if err := c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response); err != nil {
		return nil, fmt.Errorf("list transfers failed: %w", err)
	}
	return &response, nil
}

// GetChannels retrieves available payment channels
func (c *Client) GetChannels(ctx context.Context) (*ChannelsResponse, error) {
	var response ChannelsResponse
	if err := c.doRequestWithRetry(ctx, "GET", "channels", nil, &response); err != nil {
		return nil, fmt.Errorf("get channels failed: %w", err)
	}
	return &response, nil
}

// CreateQuote creates a quote for a transfer
func (c *Client) CreateQuote(ctx context.Context, req *CreateQuoteRequest) (*QuoteResponse, error) {
	c.logger.Info("Creating transfer quote",
		"sender", req.Sender,
		"recipient", req.Recipient,
		"amount", req.Amount)

	var response QuoteResponse
	if err := c.doRequestWithRetry(ctx, "POST", "transfers/quote", req, &response); err != nil {
		c.logger.Error("Failed to create quote", "error", err)
		return nil, fmt.Errorf("create quote failed: %w", err)
	}

	c.logger.Info("Created quote", "quote_id", response.ID)
	return &response, nil
}

// ListWallets retrieves all linked wallets
func (c *Client) ListWallets(ctx context.Context) (*ListWalletsResponse, error) {
	var response ListWalletsResponse
	if err := c.doRequestWithRetry(ctx, "GET", "wallets", nil, &response); err != nil {
		return nil, fmt.Errorf("list wallets failed: %w", err)
	}
	return &response, nil
}

// GetWallet retrieves a wallet by ID
func (c *Client) GetWallet(ctx context.Context, walletID string) (*LinkWalletResponse, error) {
	endpoint := fmt.Sprintf("wallets/%s", walletID)
	var response LinkWalletResponse
	if err := c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response); err != nil {
		return nil, fmt.Errorf("get wallet failed: %w", err)
	}
	return &response, nil
}

// GetWalletBalance retrieves wallet balances
func (c *Client) GetWalletBalance(ctx context.Context, walletID string) (*WalletBalanceResponse, error) {
	endpoint := fmt.Sprintf("wallets/%s/balance", walletID)
	var response WalletBalanceResponse
	if err := c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response); err != nil {
		return nil, fmt.Errorf("get wallet balance failed: %w", err)
	}
	return &response, nil
}

// CreateTransferIntent creates a transfer intent
func (c *Client) CreateTransferIntent(ctx context.Context, transferID string, req *TransferIntentRequest) (*TransferIntentResponse, error) {
	endpoint := fmt.Sprintf("transfers/%s/transfer_intent", transferID)
	var response TransferIntentResponse
	if err := c.doRequestWithRetry(ctx, "POST", endpoint, req, &response); err != nil {
		return nil, fmt.Errorf("create transfer intent failed: %w", err)
	}
	return &response, nil
}

// SubmitTransferIntent submits a transfer intent
func (c *Client) SubmitTransferIntent(ctx context.Context, req *SubmitTransferIntentRequest) (*TransferIntentResponse, error) {
	var response TransferIntentResponse
	if err := c.doRequestWithRetry(ctx, "POST", "transfer_intents/submit", req, &response); err != nil {
		return nil, fmt.Errorf("submit transfer intent failed: %w", err)
	}
	return &response, nil
}

// CreateWebhookEndpoint creates a webhook endpoint
func (c *Client) CreateWebhookEndpoint(ctx context.Context, req *CreateWebhookRequest) (*WebhookEndpointResponse, error) {
	c.logger.Info("Creating webhook endpoint", "url", req.URL)

	var response WebhookEndpointResponse
	if err := c.doRequestWithRetry(ctx, "POST", "webhook_endpoints", req, &response); err != nil {
		c.logger.Error("Failed to create webhook endpoint", "error", err)
		return nil, fmt.Errorf("create webhook endpoint failed: %w", err)
	}

	c.logger.Info("Created webhook endpoint", "id", response.ID)
	return &response, nil
}

// ListWebhookEndpoints retrieves all webhook endpoints
func (c *Client) ListWebhookEndpoints(ctx context.Context) (*ListWebhookEndpointsResponse, error) {
	var response ListWebhookEndpointsResponse
	if err := c.doRequestWithRetry(ctx, "GET", "webhook_endpoints", nil, &response); err != nil {
		return nil, fmt.Errorf("list webhook endpoints failed: %w", err)
	}
	return &response, nil
}

// DeleteWebhookEndpoint deletes a webhook endpoint
func (c *Client) DeleteWebhookEndpoint(ctx context.Context, webhookID string) error {
	endpoint := fmt.Sprintf("webhook_endpoints/%s", webhookID)
	if err := c.doRequestWithRetry(ctx, "DELETE", endpoint, nil, nil); err != nil {
		return fmt.Errorf("delete webhook endpoint failed: %w", err)
	}
	return nil
}

// ListWebhookEvents retrieves webhook events
func (c *Client) ListWebhookEvents(ctx context.Context, filters *WebhookEventFilters) (*ListWebhookEventsResponse, error) {
	params := url.Values{}
	if filters != nil {
		if filters.Limit > 0 {
			params.Set("limit", strconv.Itoa(filters.Limit))
		}
		if filters.EventType != "" {
			params.Set("eventType", filters.EventType)
		}
		if filters.StartDate != "" {
			params.Set("startDate", filters.StartDate)
		}
		if filters.EndDate != "" {
			params.Set("endDate", filters.EndDate)
		}
	}

	endpoint := "webhook_events"
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}

	var response ListWebhookEventsResponse
	if err := c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response); err != nil {
		return nil, fmt.Errorf("list webhook events failed: %w", err)
	}
	return &response, nil
}

// doRequestWithRetry performs HTTP request with retry logic
func (c *Client) doRequestWithRetry(ctx context.Context, method, endpoint string, body, response interface{}) error {
	retryConfig := retry.RetryConfig{
		MaxAttempts: c.config.MaxRetries,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    5 * time.Second,
		Multiplier:  2.0,
	}

	retryableFunc := func() error {
		return c.doRequest(ctx, method, endpoint, body, response)
	}

	isRetryable := func(err error) bool {
		if err == nil {
			return false
		}
		if apiErr, ok := err.(*ErrorResponse); ok {
			return apiErr.StatusCode >= 500
		}
		// Retry on network errors and 5xx status codes
		errStr := err.Error()
		return strings.Contains(errStr, "connection refused") ||
			strings.Contains(errStr, "timeout") ||
			strings.Contains(errStr, "status 5")
	}

	return retry.WithExponentialBackoff(ctx, retryConfig, retryableFunc, isRetryable)
}

// GetKYCStatus retrieves current KYC status
func (c *Client) GetKYCStatus(ctx context.Context, accountID string) (*KYCStatusResponse, error) {
	endpoint := "kyc"

	var response KYCStatusResponse
	if err := c.doRequestWithAccountID(ctx, "GET", endpoint, accountID, nil, &response); err != nil {
		c.logger.Error("Failed to get KYC status", "error", err)
		return nil, fmt.Errorf("get KYC status failed: %w", err)
	}

	c.logger.Info("Retrieved KYC status", "status", response.Status)
	return &response, nil
}

// InitiateKYC initiates KYC process programmatically
func (c *Client) InitiateKYC(ctx context.Context, accountID string) (*KYCInitiateResponse, error) {
	endpoint := "kyc"

	var response KYCInitiateResponse
	if err := c.doRequestWithAccountID(ctx, "POST", endpoint, accountID, nil, &response); err != nil {
		c.logger.Error("Failed to initiate KYC", "error", err)
		return nil, fmt.Errorf("initiate KYC failed: %w", err)
	}

	c.logger.Info("Initiated KYC process", "applicant_id", response.ApplicantID)
	return &response, nil
}

// CreateTransfer creates a transfer for USDC to USD conversion
func (c *Client) CreateTransfer(ctx context.Context, req *CreateTransferRequest) (*CreateTransferResponse, error) {
	c.logger.Info("Creating transfer",
		"source", req.SourceID,
		"destination", req.DestinationID,
		"amount", req.Amount)

	var response CreateTransferResponse
	if err := c.doRequest(ctx, "POST", "transfers", req, &response); err != nil {
		c.logger.Error("Failed to create transfer", "error", err)
		return nil, fmt.Errorf("create transfer failed: %w", err)
	}

	c.logger.Info("Created transfer", "transfer_id", response.ID, "status", response.Status)
	return &response, nil
}

// GetTransfer retrieves transfer details by ID
func (c *Client) GetTransfer(ctx context.Context, transferID string) (*CreateTransferResponse, error) {
	endpoint := fmt.Sprintf("transfers/%s", transferID)

	var response CreateTransferResponse
	if err := c.doRequest(ctx, "GET", endpoint, nil, &response); err != nil {
		c.logger.Error("Failed to get transfer", "transfer_id", transferID, "error", err)
		return nil, fmt.Errorf("get transfer failed: %w", err)
	}

	return &response, nil
}

// AcceptTermsOfService accepts Terms of Service for an account
func (c *Client) AcceptTermsOfService(ctx context.Context, accountID, tosToken string) (*TOSAcceptResponse, error) {
	endpoint := fmt.Sprintf("tos/%s", tosToken)

	var response TOSAcceptResponse
	if err := c.doRequestWithAccountID(ctx, "POST", endpoint, accountID, nil, &response); err != nil {
		c.logger.Error("Failed to accept ToS", "error", err)
		return nil, fmt.Errorf("accept ToS failed: %w", err)
	}

	c.logger.Info("Accepted Terms of Service", "account_id", accountID)
	return &response, nil
}

// GetKYCLink gets standard KYC/KYB link
func (c *Client) GetKYCLink(ctx context.Context, accountID string) (*KYCLinkResponse, error) {
	var response KYCLinkResponse
	if err := c.doRequestWithAccountID(ctx, "GET", "kyc", accountID, nil, &response); err != nil {
		return nil, fmt.Errorf("get KYC link failed: %w", err)
	}
	return &response, nil
}

// CreateKYCSession creates a KYC/KYB access session
func (c *Client) CreateKYCSession(ctx context.Context, accountID string, req *KYCSessionRequest) (*KYCSessionResponse, error) {
	var response KYCSessionResponse
	if err := c.doRequestWithAccountID(ctx, "POST", "kyc/session", accountID, req, &response); err != nil {
		return nil, fmt.Errorf("create KYC session failed: %w", err)
	}
	return &response, nil
}

// InitializeVaultCredentials initializes vault credentials
func (c *Client) InitializeVaultCredentials(ctx context.Context, req *InitCredentialsRequest) (*InitCredentialsResponse, error) {
	var response InitCredentialsResponse
	if err := c.doRequestWithRetry(ctx, "POST", "vaults/credentials/init", req, &response); err != nil {
		return nil, fmt.Errorf("initialize credentials failed: %w", err)
	}
	return &response, nil
}

// CreateVaultCredentials creates vault credentials
func (c *Client) CreateVaultCredentials(ctx context.Context, req *CreateCredentialsRequest) (*CredentialsResponse, error) {
	var response CredentialsResponse
	if err := c.doRequestWithRetry(ctx, "POST", "vaults/credentials", req, &response); err != nil {
		return nil, fmt.Errorf("create credentials failed: %w", err)
	}
	return &response, nil
}

// CreateVault creates a new vault
func (c *Client) CreateVault(ctx context.Context, req *CreateVaultRequest) (*VaultResponse, error) {
	var response VaultResponse
	if err := c.doRequestWithRetry(ctx, "POST", "vaults", req, &response); err != nil {
		return nil, fmt.Errorf("create vault failed: %w", err)
	}
	return &response, nil
}

// SignWithVault signs a transaction with vault
func (c *Client) SignWithVault(ctx context.Context, req *SignRequest) (*SignResponse, error) {
	var response SignResponse
	if err := c.doRequestWithRetry(ctx, "POST", "vaults/sign", req, &response); err != nil {
		return nil, fmt.Errorf("sign with vault failed: %w", err)
	}
	return &response, nil
}

// GetFXMarkets retrieves available FX markets
func (c *Client) GetFXMarkets(ctx context.Context) (*FXMarketsResponse, error) {
	var response FXMarketsResponse
	if err := c.doRequestWithRetry(ctx, "GET", "/fx/markets", nil, &response); err != nil {
		return nil, fmt.Errorf("get FX markets failed: %w", err)
	}
	return &response, nil
}

// UpdateVirtualAccount updates a virtual account
func (c *Client) UpdateVirtualAccount(ctx context.Context, key string, req *UpdateVirtualAccountRequest) (*CreateVirtualAccountResponse, error) {
	endpoint := fmt.Sprintf("virtual_accounts/%s", key)
	var response CreateVirtualAccountResponse
	if err := c.doRequestWithRetry(ctx, "POST", endpoint, req, &response); err != nil {
		return nil, fmt.Errorf("update virtual account failed: %w", err)
	}
	return &response, nil
}

// CreateFundingAddress creates a funding address for a transfer
func (c *Client) CreateFundingAddress(ctx context.Context, transferID string, req *FundingAddressRequest) (*FundingAddressResponse, error) {
	endpoint := fmt.Sprintf("transfers/%s/funding_address", transferID)
	var response FundingAddressResponse
	if err := c.doRequestWithRetry(ctx, "POST", endpoint, req, &response); err != nil {
		return nil, fmt.Errorf("create funding address failed: %w", err)
	}
	return &response, nil
}

// GetTOSData retrieves Terms of Service data
func (c *Client) GetTOSData(ctx context.Context, token string) (*TOSDataResponse, error) {
	endpoint := fmt.Sprintf("tos/%s", token)
	var response TOSDataResponse
	if err := c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response); err != nil {
		return nil, fmt.Errorf("get TOS data failed: %w", err)
	}
	return &response, nil
}

// ListFinancialInstitutions lists financial institutions by country and schema
func (c *Client) ListFinancialInstitutions(ctx context.Context, country, schema string) (*FinancialInstitutionsResponse, error) {
	endpoint := fmt.Sprintf("financial_institutions/%s/%s", country, schema)
	var response FinancialInstitutionsResponse
	if err := c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response); err != nil {
		return nil, fmt.Errorf("list financial institutions failed: %w", err)
	}
	return &response, nil
}

// GetFinancialInstitution gets a specific financial institution
func (c *Client) GetFinancialInstitution(ctx context.Context, institutionID string) (*FinancialInstitution, error) {
	endpoint := fmt.Sprintf("financial_institutions/%s", institutionID)
	var response FinancialInstitution
	if err := c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response); err != nil {
		return nil, fmt.Errorf("get financial institution failed: %w", err)
	}
	return &response, nil
}

// CreateOnRampTransfer creates an on-ramp transfer
func (c *Client) CreateOnRampTransfer(ctx context.Context, req *OnRampTransferRequest) (*OnRampTransferResponse, error) {
	var response OnRampTransferResponse
	if err := c.doRequestWithRetry(ctx, "POST", "transfers", req, &response); err != nil {
		return nil, fmt.Errorf("create on-ramp transfer failed: %w", err)
	}
	return &response, nil
}

// CreateOnRampQuote creates an on-ramp quote
func (c *Client) CreateOnRampQuote(ctx context.Context, req *OnRampQuoteRequest) (*OnRampQuoteResponse, error) {
	var response OnRampQuoteResponse
	if err := c.doRequestWithRetry(ctx, "POST", "transfers/quote", req, &response); err != nil {
		return nil, fmt.Errorf("create on-ramp quote failed: %w", err)
	}
	return &response, nil
}

// SimulatePayIn simulates a pay-in for testing (sandbox only)
func (c *Client) SimulatePayIn(ctx context.Context, req *SimulatePayInRequest) (*SimulatePayInResponse, error) {
	var response SimulatePayInResponse
	if err := c.doRequestWithRetry(ctx, "POST", "/dev/payin", req, &response); err != nil {
		return nil, fmt.Errorf("simulate pay-in failed: %w", err)
	}
	return &response, nil
}

// CreateFXQuote creates an FX quote
func (c *Client) CreateFXQuote(ctx context.Context, req *FXQuoteRequest) (*FXQuoteResponse, error) {
	var response FXQuoteResponse
	if err := c.doRequestWithRetry(ctx, "POST", "/fx/quote", req, &response); err != nil {
		return nil, fmt.Errorf("create FX quote failed: %w", err)
	}
	return &response, nil
}

// doRequestWithAccountID performs HTTP request with Due-Account-Id header
func (c *Client) doRequestWithAccountID(ctx context.Context, method, endpoint, accountID string, body, response interface{}) error {
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}
	if !strings.HasPrefix(endpoint, "/v1/") && !strings.HasPrefix(endpoint, "/dev/") {
		endpoint = "/v1" + endpoint
	}
	fullURL := c.config.BaseURL + endpoint

	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers as per Due API documentation
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	req.Header.Set("Due-Account-Id", accountID)

	c.logger.Debug("Sending Due API request with account ID",
		"method", method,
		"url", fullURL,
		"account_id", accountID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	c.logger.Debug("Received Due API response",
		"status_code", resp.StatusCode,
		"body_size", len(respBody))

	// Check for error responses
	if resp.StatusCode >= 400 {
		return fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	// Parse response if a response object is provided
	if response != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, response); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}