package travel

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/travu"
)

// TravelPassenger is a saved traveler profile reused across bookings.
type TravelPassenger struct {
	ID              uuid.UUID `json:"id"`
	Label           string    `json:"label"`
	IsPrimary       bool      `json:"is_primary"`
	PassengerType   string    `json:"passenger_type"`
	Title           string    `json:"title"`
	FirstName       string    `json:"first_name"`
	MiddleName      string    `json:"middle_name"`
	LastName        string    `json:"last_name"`
	DOB             string    `json:"dob"`
	Sex             string    `json:"sex"`
	Blood           string    `json:"blood"`
	Phone           string    `json:"phone"`
	Email           string    `json:"email"`
	Address         string    `json:"address"`
	City            string    `json:"city"`
	PostalCode      string    `json:"postal_code"`
	Country         string    `json:"country"`
	CountryCode     string    `json:"country_code"`
	PassportNumber  string    `json:"passport_number"`
	PassportCountry string    `json:"passport_country"`
	PassportExpiry  string    `json:"passport_expiry"`
	Nationality     string    `json:"nationality"`
	NextOfKin       string    `json:"next_of_kin"`
	NextOfKinPhone  string    `json:"next_of_kin_phone"`
}

// FullName returns the traveler's assembled full name.
func (p TravelPassenger) FullName() string {
	parts := []string{p.FirstName, p.MiddleName, p.LastName}
	out := make([]string, 0, 3)
	for _, s := range parts {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return strings.Join(out, " ")
}

// HasFlightDetails reports whether the profile has the passport-level fields a
// flight booking requires.
func (p TravelPassenger) HasFlightDetails() (bool, []string) {
	var missing []string
	if strings.TrimSpace(p.PassportNumber) == "" {
		missing = append(missing, "passport_number")
	}
	if strings.TrimSpace(p.PassportExpiry) == "" {
		missing = append(missing, "passport_expiry")
	}
	if strings.TrimSpace(p.DOB) == "" {
		missing = append(missing, "dob")
	}
	if strings.TrimSpace(p.Nationality) == "" && strings.TrimSpace(p.PassportCountry) == "" {
		missing = append(missing, "nationality")
	}
	if strings.TrimSpace(p.Sex) == "" {
		missing = append(missing, "sex")
	}
	return len(missing) == 0, missing
}

// ToBusPassenger maps a saved profile to a Travu bus passenger.
func (p TravelPassenger) ToBusPassenger(isPrimary bool, age string) travu.Passenger {
	return travu.Passenger{
		Title:          p.Title,
		Name:           p.FullName(),
		Age:            age,
		Sex:            p.Sex,
		Phone:          p.Phone,
		Email:          p.Email,
		Blood:          p.Blood,
		NextOfKin:      p.NextOfKin,
		NextOfKinPhone: p.NextOfKinPhone,
		IsPrimary:      isPrimary,
	}
}

// ToFlightPassenger maps a saved profile to a Travu flight passenger.
func (p TravelPassenger) ToFlightPassenger() travu.FlightPassenger {
	passengerType := p.PassengerType
	if passengerType == "" {
		passengerType = "Adult"
	}
	country := p.PassportCountry
	if country == "" {
		country = p.Nationality
	}
	return travu.FlightPassenger{
		PassengerType:   passengerType,
		FirstName:       p.FirstName,
		MiddleName:      p.MiddleName,
		LastName:        p.LastName,
		DOB:             p.DOB,
		Phone:           p.Phone,
		PassportNumber:  p.PassportNumber,
		ExpiryDate:      p.PassportExpiry,
		PassportCountry: country,
		Gender:          p.Sex,
		Title:           p.Title,
		Email:           p.Email,
		Address:         p.Address,
		Country:         p.Country,
		CountryCode:     p.CountryCode,
		City:            p.City,
		PostalCode:      p.PostalCode,
	}
}

// ListPassengers returns a user's saved traveler profiles, primary first.
func (s *Service) ListPassengers(ctx context.Context, userID uuid.UUID) ([]TravelPassenger, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(label,''), is_primary, passenger_type, COALESCE(title,''), first_name, COALESCE(middle_name,''), last_name,
		       COALESCE(dob,''), COALESCE(sex,''), COALESCE(blood,''), COALESCE(phone,''), COALESCE(email,''),
		       COALESCE(address,''), COALESCE(city,''), COALESCE(postal_code,''), COALESCE(country,''), COALESCE(country_code,''),
		       COALESCE(passport_number,''), COALESCE(passport_country,''), COALESCE(passport_expiry,''), COALESCE(nationality,''),
		       COALESCE(next_of_kin,''), COALESCE(next_of_kin_phone,'')
		FROM travel_passengers WHERE user_id=$1 ORDER BY is_primary DESC, created_at ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TravelPassenger
	for rows.Next() {
		var p TravelPassenger
		if err := rows.Scan(&p.ID, &p.Label, &p.IsPrimary, &p.PassengerType, &p.Title, &p.FirstName, &p.MiddleName, &p.LastName,
			&p.DOB, &p.Sex, &p.Blood, &p.Phone, &p.Email, &p.Address, &p.City, &p.PostalCode, &p.Country, &p.CountryCode,
			&p.PassportNumber, &p.PassportCountry, &p.PassportExpiry, &p.Nationality, &p.NextOfKin, &p.NextOfKinPhone); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPrimaryPassenger returns the user's primary traveler profile, or nil when
// none is saved.
func (s *Service) GetPrimaryPassenger(ctx context.Context, userID uuid.UUID) (*TravelPassenger, error) {
	list, err := s.ListPassengers(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].IsPrimary {
			return &list[i], nil
		}
	}
	if len(list) > 0 {
		return &list[0], nil
	}
	return nil, nil
}

// SavePassenger upserts a traveler profile. When isPrimary is set, any existing
// primary for the user is demoted first. Returns the saved profile id.
func (s *Service) SavePassenger(ctx context.Context, userID uuid.UUID, p TravelPassenger) (uuid.UUID, error) {
	if strings.TrimSpace(p.FirstName) == "" || strings.TrimSpace(p.LastName) == "" {
		return uuid.Nil, fmt.Errorf("first and last name are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if p.IsPrimary {
		if _, err := tx.ExecContext(ctx, `UPDATE travel_passengers SET is_primary=FALSE, updated_at=NOW() WHERE user_id=$1 AND is_primary=TRUE`, userID); err != nil {
			return uuid.Nil, err
		}
	}

	passengerType := p.PassengerType
	if passengerType == "" {
		passengerType = "Adult"
	}

	id := p.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO travel_passengers (id, user_id, label, is_primary, passenger_type, title, first_name, middle_name, last_name,
			dob, sex, blood, phone, email, address, city, postal_code, country, country_code,
			passport_number, passport_country, passport_expiry, nationality, next_of_kin, next_of_kin_phone)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)
		ON CONFLICT (id) DO UPDATE SET
			label=EXCLUDED.label, is_primary=EXCLUDED.is_primary, passenger_type=EXCLUDED.passenger_type, title=EXCLUDED.title,
			first_name=EXCLUDED.first_name, middle_name=EXCLUDED.middle_name, last_name=EXCLUDED.last_name, dob=EXCLUDED.dob,
			sex=EXCLUDED.sex, blood=EXCLUDED.blood, phone=EXCLUDED.phone, email=EXCLUDED.email, address=EXCLUDED.address,
			city=EXCLUDED.city, postal_code=EXCLUDED.postal_code, country=EXCLUDED.country, country_code=EXCLUDED.country_code,
			passport_number=EXCLUDED.passport_number, passport_country=EXCLUDED.passport_country, passport_expiry=EXCLUDED.passport_expiry,
			nationality=EXCLUDED.nationality, next_of_kin=EXCLUDED.next_of_kin, next_of_kin_phone=EXCLUDED.next_of_kin_phone, updated_at=NOW()`,
		id, userID, nullStr(p.Label), p.IsPrimary, passengerType, nullStr(p.Title), p.FirstName, nullStr(p.MiddleName), p.LastName,
		nullStr(p.DOB), nullStr(p.Sex), nullStr(p.Blood), nullStr(p.Phone), nullStr(p.Email), nullStr(p.Address), nullStr(p.City),
		nullStr(p.PostalCode), nullStr(p.Country), nullStr(p.CountryCode), nullStr(p.PassportNumber), nullStr(p.PassportCountry),
		nullStr(p.PassportExpiry), nullStr(p.Nationality), nullStr(p.NextOfKin), nullStr(p.NextOfKinPhone)); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// GetPassengerByID returns a single saved profile owned by the user.
func (s *Service) GetPassengerByID(ctx context.Context, userID, passengerID uuid.UUID) (*TravelPassenger, error) {
	list, err := s.ListPassengers(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == passengerID {
			return &list[i], nil
		}
	}
	return nil, sql.ErrNoRows
}
