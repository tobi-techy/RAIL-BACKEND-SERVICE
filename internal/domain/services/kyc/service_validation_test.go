package kyc

import (
	"testing"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

func TestValidateRequestAcceptsValidPayload(t *testing.T) {
	svc := &Service{}
	req := &entities.KYCSubmitRequest{
		TaxID:           "123-45-6789",
		TaxIDType:       "ssn",
		IssuingCountry:  "USA",
		IDDocumentFront: "data:image/jpeg;base64,/9j/",
	}

	if err := svc.validateRequest(req); err != nil {
		t.Fatalf("validateRequest returned error for valid payload: %v", err)
	}
}

func TestValidateRequestRejectsInvalidIssuingCountry(t *testing.T) {
	svc := &Service{}
	req := &entities.KYCSubmitRequest{
		TaxID:           "123-45-6789",
		TaxIDType:       "ssn",
		IssuingCountry:  "US",
		IDDocumentFront: "data:image/jpeg;base64,/9j/",
	}

	err := svc.validateRequest(req)
	if err != ErrInvalidIssuingCountry {
		t.Fatalf("expected ErrInvalidIssuingCountry, got %v", err)
	}
}

func TestValidateRequestRejectsMissingFrontDocument(t *testing.T) {
	svc := &Service{}
	req := &entities.KYCSubmitRequest{
		TaxID:          "123-45-6789",
		TaxIDType:      "ssn",
		IssuingCountry: "USA",
	}

	err := svc.validateRequest(req)
	if err != ErrMissingDocumentFront {
		t.Fatalf("expected ErrMissingDocumentFront, got %v", err)
	}
}

func TestValidateRequestRejectsUnsupportedImageType(t *testing.T) {
	svc := &Service{}
	req := &entities.KYCSubmitRequest{
		TaxID:           "123-45-6789",
		TaxIDType:       "ssn",
		IssuingCountry:  "USA",
		IDDocumentFront: "data:image/gif;base64,R0lGODlhAQABAIAAAAUEBA==",
	}

	err := svc.validateRequest(req)
	if err != ErrInvalidImage {
		t.Fatalf("expected ErrInvalidImage, got %v", err)
	}
}

func TestCollectMissingKYCProfileFields(t *testing.T) {
	dob := time.Now().AddDate(-30, 0, 0)
	profile := &entities.UserProfile{
		FirstName:         strPtr("Jane"),
		LastName:          strPtr("Doe"),
		DateOfBirth:       &dob,
		Phone:             strPtr("+15555551234"),
		AddressStreet:     strPtr("123 Main St"),
		AddressCity:       strPtr("New York"),
		AddressPostalCode: strPtr("10001"),
		AddressCountry:    strPtr("US"),
	}

	missing := collectMissingKYCProfileFields(profile)
	if len(missing) != 0 {
		t.Fatalf("expected no missing fields, got %v", missing)
	}
}

func TestCollectMissingKYCProfileFieldsWhenEmpty(t *testing.T) {
	missing := collectMissingKYCProfileFields(&entities.UserProfile{})
	if len(missing) == 0 {
		t.Fatalf("expected missing fields, got none")
	}
}

func strPtr(v string) *string {
	return &v
}
