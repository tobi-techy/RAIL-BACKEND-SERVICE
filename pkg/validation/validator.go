package validation

import (
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/pkg/errors"
)

// Validator wraps the validator library with custom validation rules
type Validator struct {
	validate *validator.Validate
}

// NewValidator creates a new validator instance
func NewValidator() *Validator {
	v := validator.New()

	// Register custom validation rules
	v.RegisterValidation("strong_password", validateStrongPassword)
	v.RegisterValidation("phone_number", validatePhoneNumber)
	v.RegisterValidation("safe_string", validateSafeString)
	v.RegisterValidation("blockchain_address", validateBlockchainAddress)
	v.RegisterValidation("amount", validateAmount)

	return &Validator{validate: v}
}

// Validate validates a struct and returns error if validation fails
func (v *Validator) Validate(s interface{}) error {
	if err := v.validate.Struct(s); err != nil {
		return errors.NewValidationError(err.Error())
	}
	return nil
}

// ValidateJSON validates JSON request body
func (v *Validator) ValidateJSON(c *gin.Context, obj interface{}) bool {
	if err := c.ShouldBindJSON(obj); err != nil {
		common.RespondBadRequest(c, "Invalid JSON format", nil)
		return false
	}

	if err := v.Validate(obj); err != nil {
		common.SendValidationError(c, err.Error(), nil)
		return false
	}

	return true
}

// ValidateURI validates URI parameters
func (v *Validator) ValidateURI(c *gin.Context, obj interface{}) bool {
	if err := c.ShouldBindUri(obj); err != nil {
		common.RespondBadRequest(c, "Invalid URI parameters", nil)
		return false
	}

	if err := v.Validate(obj); err != nil {
		common.SendValidationError(c, err.Error(), nil)
		return false
	}

	return true
}

// ValidateQuery validates query parameters
func (v *Validator) ValidateQuery(c *gin.Context, obj interface{}) bool {
	if err := c.ShouldBindQuery(obj); err != nil {
		common.RespondBadRequest(c, "Invalid query parameters", nil)
		return false
	}

	if err := v.Validate(obj); err != nil {
		common.SendValidationError(c, err.Error(), nil)
		return false
	}

	return true
}

// Custom validation functions

// validateStrongPassword validates password strength
func validateStrongPassword(fl validator.FieldLevel) bool {
	password := fl.Field().String()

	// At least 8 characters
	if len(password) < 8 {
		return false
	}

	// Contains uppercase
	hasUpper := regexp.MustCompile(`[A-Z]`).MatchString(password)
	// Contains lowercase
	hasLower := regexp.MustCompile(`[a-z]`).MatchString(password)
	// Contains number
	hasNumber := regexp.MustCompile(`[0-9]`).MatchString(password)
	// Contains special character
	hasSpecial := regexp.MustCompile(`[!@#$%^&*(),.?":{}|<>]`).MatchString(password)

	return hasUpper && hasLower && hasNumber && hasSpecial
}

// validatePhoneNumber validates phone numbers (E.164 format)
func validatePhoneNumber(fl validator.FieldLevel) bool {
	phone := fl.Field().String()

	// E.164 format: +[country code][number]
	e164Pattern := regexp.MustCompile(`^\+[1-9]\d{1,14}$`)
	return e164Pattern.MatchString(phone)
}

// validateSafeString prevents injection attacks
func validateSafeString(fl validator.FieldLevel) bool {
	str := fl.Field().String()

	// Check for dangerous patterns
	dangerousPatterns := []string{
		"<script", "</script>", "javascript:", "vbscript:",
		"onload=", "onerror=", "onclick=", "onmouseover=",
		"eval(", "alert(", "confirm(", "prompt(",
		"SELECT ", "INSERT ", "UPDATE ", "DELETE ", "DROP ",
		"UNION ", "EXEC ", "EXECUTE ", "CAST ", "CHAR ",
		"<", ">", "\"", "'", "&", "/*", "*/", "--",
	}

	lowerStr := strings.ToLower(str)
	for _, pattern := range dangerousPatterns {
		if strings.Contains(lowerStr, pattern) {
			return false
		}
	}

	return true
}

// validateBlockchainAddress validates blockchain addresses
func validateBlockchainAddress(fl validator.FieldLevel) bool {
	address := fl.Field().String()

	// Ethereum address (0x + 40 hex chars)
	ethPattern := regexp.MustCompile(`^0x[a-fA-F0-9]{40}$`)
	if ethPattern.MatchString(address) {
		return true
	}

	// Bitcoin address (base58)
	btcPattern := regexp.MustCompile(`^[13][a-km-zA-HJ-NP-Z1-9]{25,34}$`)
	if btcPattern.MatchString(address) {
		return true
	}

	// Solana address (base58, 44 chars)
	solPattern := regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{32,44}$`)
	if solPattern.MatchString(address) {
		return true
	}

	return false
}

// validateAmount validates monetary amounts
func validateAmount(fl validator.FieldLevel) bool {
	amount := fl.Field().String()

	// Positive number with optional decimal
	amountPattern := regexp.MustCompile(`^\d+(\.\d{1,8})?$`)
	return amountPattern.MatchString(amount)
}

// ValidationMiddleware creates a validation middleware
func ValidationMiddleware() gin.HandlerFunc {
	v := NewValidator()

	return func(c *gin.Context) {
		// Store validator in context
		c.Set("validator", v)
		c.Next()
	}
}

// GetValidator retrieves validator from context
func GetValidator(c *gin.Context) *Validator {
	if v, exists := c.Get("validator"); exists {
		return v.(*Validator)
	}
	return NewValidator()
}

// Common validation request structures

// PaginationRequest validates pagination parameters
type PaginationRequest struct {
	Page    int    `form:"page" validate:"min=1" json:"page"`
	Limit   int    `form:"limit" validate:"min=1,max=100" json:"limit"`
	OrderBy string `form:"order_by" validate:"omitempty,oneof=created_at updated_at name" json:"order_by"`
	Order   string `form:"order" validate:"omitempty,oneof=asc desc" json:"order"`
}

// DateRangeRequest validates date range parameters
type DateRangeRequest struct {
	StartDate string `form:"start_date" validate:"required,datetime=2006-01-02" json:"start_date"`
	EndDate   string `form:"end_date" validate:"required,datetime=2006-01-02" json:"end_date"`
}

// UUIDRequest validates UUID parameters
type UUIDRequest struct {
	ID string `uri:"id" validate:"required,uuid" json:"id"`
}

// EmailRequest validates email parameters
type EmailRequest struct {
	Email string `form:"email" validate:"required,email" json:"email"`
}

// SearchRequest validates search parameters
type SearchRequest struct {
	Query  string `form:"q" validate:"required,min=1,max=100" json:"q"`
	Filter string `form:"filter" validate:"omitempty,oneof=all active inactive" json:"filter"`
}
