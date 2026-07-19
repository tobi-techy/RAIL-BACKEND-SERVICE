package airbills

// Product codes identify the bill type passed as productCode in /transact.
const (
	ProductAirtime     = "100"
	ProductElectricity = "101"
	ProductData        = "102"
	ProductBetting     = "103"
	ProductCableTV     = "104"
	ProductTransport   = "105"
)

// Payment modes for /transact.
const (
	// PayWithTransfer returns a deposit wallet address to send tokens to. Rail
	// controls the user's Circle wallet, so this is the mode we use — no need to
	// sign an arbitrary Solana transaction on the user's behalf.
	PayWithTransfer = "transfer"
	// PayWithDefault returns a pre-built Solana transaction to sign. Unused.
	PayWithDefault = "default"
)

// Airbills response status codes returned in the top-level `status` field.
const (
	// StatusSuccess indicates the call succeeded.
	StatusSuccess = "00"
	// StatusAlreadyProcessed indicates the transaction was already fulfilled in
	// a prior call. Not an error — the bill was paid; retries are safe.
	StatusAlreadyProcessed = "06"
)

// Network IDs for airtime (100) and data (102) products.
const (
	NetworkMTN     = "01"
	NetworkGlo     = "02"
	Network9mobile = "03"
	NetworkAirtel  = "04"
)

// Supported settlement tokens (both 6-decimal SPL tokens on Solana mainnet).
const (
	TokenUSDC = "USDC"
	TokenUSDT = "USDT"
)

// TransactData carries the product-specific fields for a /transact request.
// Only the fields relevant to the chosen productCode need be populated; unset
// fields are omitted from the JSON body.
type TransactData struct {
	PubKey      string  `json:"pubKey"`
	Token       string  `json:"token"`
	Amount      float64 `json:"amount"`
	PhoneNumber string  `json:"phoneNumber,omitempty"`
	NetworkID   string  `json:"networkId,omitempty"`
	ProdID      string  `json:"prodId,omitempty"`
	MeterNo     string  `json:"meterNo,omitempty"`
	ElectID     string  `json:"electId,omitempty"`
	SmartCardNo string  `json:"smartCardNo,omitempty"`
	CustomerID  string  `json:"customerId,omitempty"`
}

// TransactRequest is the body for POST /transact.
type TransactRequest struct {
	ProductCode string       `json:"productCode"`
	PayWith     string       `json:"payWith"`
	CallbackURL string       `json:"callbackUrl,omitempty"`
	Data        TransactData `json:"data"`
}

// TransactResult is the `data` object returned by /transact. In transfer mode
// Wallet holds the deposit address; in default mode TransactionIx holds the
// base64 Solana transaction (unused here).
type TransactResult struct {
	ID            string  `json:"id"`
	Wallet        string  `json:"wallet"`
	TransactionIx string  `json:"transactionIx"`
	Token         string  `json:"token"`
	TokenMint     string  `json:"tokenMint"`
	AmountInToken float64 `json:"amountInToken"`
}

// TransactResponse is the envelope returned by /transact.
type TransactResponse struct {
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Data    TransactResult `json:"data"`
}

// ProcessRequest is the body for POST /transact/process.
type ProcessRequest struct {
	ProductCode string `json:"productCode"`
	ID          string `json:"id"`
}

// ProcessResponse is the envelope returned by /transact/process. Data varies by
// product and is surfaced raw for receipts/audit.
type ProcessResponse struct {
	Status  string                 `json:"status"`
	Message string                 `json:"message"`
	Data    map[string]interface{} `json:"data"`
}

// Succeeded reports whether the process call fulfilled (or had already
// fulfilled) the bill. Status "06" means a prior call already paid it.
func (r *ProcessResponse) Succeeded() bool {
	return r != nil && (r.Status == StatusSuccess || r.Status == StatusAlreadyProcessed)
}

// Product is a single provider/plan option returned by the /list/* endpoints.
// The lookup endpoints share a common shape; fields absent for a given product
// stay empty.
type Product struct {
	ProdID     string  `json:"prodId"`
	Name       string  `json:"name"`
	Amount     float64 `json:"amount"`
	ProdAmount float64 `json:"prodAmount"`
	ElectID    string  `json:"electId"`
	NetworkID  string  `json:"networkId"`
}

// ListResponse is the envelope returned by the /list/* lookup endpoints.
type ListResponse struct {
	Status  string    `json:"status"`
	Message string    `json:"message"`
	Data    []Product `json:"data"`
}

// NetworkCheckResponse is returned by GET /network-checker.
type NetworkCheckResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		NetworkID string `json:"networkId"`
		Network   string `json:"network"`
	} `json:"data"`
}

// MeterValidateRequest is the body for POST /validate/elect.
type MeterValidateRequest struct {
	MeterNo string `json:"meterNo"`
	ElectID string `json:"electId"`
}

// MeterValidateResponse confirms a meter and returns the account holder name.
type MeterValidateResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		AccountName string `json:"accountName"`
		Address     string `json:"address"`
	} `json:"data"`
}
