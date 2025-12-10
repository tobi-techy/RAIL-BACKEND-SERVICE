package security

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type GeoSecurityService struct {
	db     *sql.DB
	redis  *redis.Client
	logger *zap.Logger
	client *http.Client
	apiKey string // For IP geolocation API
}

type GeoLocation struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	IPAddress   string
	CountryCode string
	Region      string
	City        string
	Latitude    float64
	Longitude   float64
	IsVPN       bool
	IsProxy     bool
	IsTor       bool
	RiskScore   float64
	CreatedAt   time.Time
}

type GeoCheckResult struct {
	Allowed       bool
	Location      *GeoLocation
	BlockReason   string
	RiskFactors   []string
	RiskScore     float64
	IsAnomaly     bool
	RequiresMFA   bool
}

// High-risk countries (OFAC sanctioned, etc.)
var highRiskCountries = map[string]bool{
	"KP": true, // North Korea
	"IR": true, // Iran
	"SY": true, // Syria
	"CU": true, // Cuba
	"RU": true, // Russia (partial)
}

func NewGeoSecurityService(db *sql.DB, redis *redis.Client, logger *zap.Logger, apiKey string) *GeoSecurityService {
	return &GeoSecurityService{
		db:     db,
		redis:  redis,
		logger: logger,
		client: &http.Client{Timeout: 5 * time.Second},
		apiKey: apiKey,
	}
}

// CheckIP performs comprehensive IP security check
func (s *GeoSecurityService) CheckIP(ctx context.Context, userID uuid.UUID, ipAddress string) (*GeoCheckResult, error) {
	result := &GeoCheckResult{
		Allowed:     true,
		RiskFactors: []string{},
	}

	// Validate IP format
	ip := net.ParseIP(ipAddress)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP address")
	}

	// Skip checks for private IPs
	if ip.IsPrivate() || ip.IsLoopback() {
		result.Location = &GeoLocation{IPAddress: ipAddress, CountryCode: "XX"}
		return result, nil
	}

	// Get geolocation data
	location, err := s.getGeoLocation(ctx, ipAddress)
	if err != nil {
		s.logger.Warn("Failed to get geolocation", zap.Error(err))
		location = &GeoLocation{IPAddress: ipAddress}
	}
	result.Location = location

	// Check blocked countries
	if s.isCountryBlocked(ctx, location.CountryCode) {
		result.Allowed = false
		result.BlockReason = "country_blocked"
		result.RiskFactors = append(result.RiskFactors, "blocked_country")
		return result, nil
	}

	// Check high-risk countries
	if highRiskCountries[location.CountryCode] {
		result.RiskFactors = append(result.RiskFactors, "high_risk_country")
		result.RiskScore += 0.3
		result.RequiresMFA = true
	}

	// Check VPN/Proxy/Tor
	if location.IsVPN {
		result.RiskFactors = append(result.RiskFactors, "vpn_detected")
		result.RiskScore += 0.2
	}
	if location.IsProxy {
		result.RiskFactors = append(result.RiskFactors, "proxy_detected")
		result.RiskScore += 0.25
	}
	if location.IsTor {
		result.RiskFactors = append(result.RiskFactors, "tor_detected")
		result.RiskScore += 0.4
		result.RequiresMFA = true
	}

	// Check for location anomaly
	if userID != uuid.Nil {
		anomaly, err := s.checkLocationAnomaly(ctx, userID, location)
		if err == nil && anomaly {
			result.IsAnomaly = true
			result.RiskFactors = append(result.RiskFactors, "location_anomaly")
			result.RiskScore += 0.3
			result.RequiresMFA = true
		}

		// Store location for future analysis
		s.storeLocation(ctx, userID, location)
	}

	// Block if risk score too high
	if result.RiskScore > 0.8 {
		result.Allowed = false
		result.BlockReason = "high_risk_score"
	}

	return result, nil
}

// getGeoLocation fetches geolocation data for an IP
func (s *GeoSecurityService) getGeoLocation(ctx context.Context, ipAddress string) (*GeoLocation, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("geo:%s", ipAddress)
	cached, err := s.redis.Get(ctx, cacheKey).Result()
	if err == nil {
		var location GeoLocation
		if json.Unmarshal([]byte(cached), &location) == nil {
			return &location, nil
		}
	}

	location := &GeoLocation{IPAddress: ipAddress}

	// Use IP geolocation API (ip-api.com free tier or similar)
	if s.apiKey != "" {
		location = s.fetchFromAPI(ctx, ipAddress)
	} else {
		// Fallback: use free ip-api.com
		location = s.fetchFromFreeAPI(ctx, ipAddress)
	}

	// Cache for 24 hours
	if data, err := json.Marshal(location); err == nil {
		s.redis.Set(ctx, cacheKey, data, 24*time.Hour)
	}

	return location, nil
}

func (s *GeoSecurityService) fetchFromFreeAPI(ctx context.Context, ipAddress string) *GeoLocation {
	location := &GeoLocation{IPAddress: ipAddress}

	url := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,country,countryCode,region,city,lat,lon,proxy,hosting", ipAddress)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return location
	}
	defer resp.Body.Close()

	var data struct {
		Status      string  `json:"status"`
		CountryCode string  `json:"countryCode"`
		Region      string  `json:"region"`
		City        string  `json:"city"`
		Lat         float64 `json:"lat"`
		Lon         float64 `json:"lon"`
		Proxy       bool    `json:"proxy"`
		Hosting     bool    `json:"hosting"`
	}

	if json.NewDecoder(resp.Body).Decode(&data) == nil && data.Status == "success" {
		location.CountryCode = data.CountryCode
		location.Region = data.Region
		location.City = data.City
		location.Latitude = data.Lat
		location.Longitude = data.Lon
		location.IsProxy = data.Proxy
		location.IsVPN = data.Hosting // Hosting/datacenter IPs often indicate VPN
	}

	return location
}

func (s *GeoSecurityService) fetchFromAPI(ctx context.Context, ipAddress string) *GeoLocation {
	// Implement paid API integration (ipinfo.io, maxmind, etc.)
	return s.fetchFromFreeAPI(ctx, ipAddress)
}

// checkLocationAnomaly detects unusual location changes
func (s *GeoSecurityService) checkLocationAnomaly(ctx context.Context, userID uuid.UUID, current *GeoLocation) (bool, error) {
	// Get last known location
	var lastLat, lastLon float64
	var lastTime time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT latitude, longitude, created_at FROM geo_locations 
		WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1`,
		userID).Scan(&lastLat, &lastLon, &lastTime)
	
	if err == sql.ErrNoRows {
		return false, nil // First login, no anomaly
	}
	if err != nil {
		return false, err
	}

	// Calculate distance
	distance := haversineDistance(lastLat, lastLon, current.Latitude, current.Longitude)
	timeDiff := time.Since(lastTime).Hours()

	// Impossible travel detection: >500km in <1 hour
	if timeDiff < 1 && distance > 500 {
		s.logger.Warn("Impossible travel detected",
			zap.String("user_id", userID.String()),
			zap.Float64("distance_km", distance),
			zap.Float64("hours", timeDiff))
		return true, nil
	}

	// Significant location change: different country
	var lastCountry string
	s.db.QueryRowContext(ctx, `
		SELECT country_code FROM geo_locations 
		WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1`,
		userID).Scan(&lastCountry)

	if lastCountry != "" && lastCountry != current.CountryCode {
		// Check if this country is in user's typical countries
		var typicalCountries []string
		s.db.QueryRowContext(ctx, `
			SELECT typical_countries FROM user_behavior_patterns WHERE user_id = $1`,
			userID).Scan(&typicalCountries)

		isTypical := false
		for _, c := range typicalCountries {
			if c == current.CountryCode {
				isTypical = true
				break
			}
		}

		if !isTypical {
			return true, nil
		}
	}

	return false, nil
}

// storeLocation saves location for future analysis
func (s *GeoSecurityService) storeLocation(ctx context.Context, userID uuid.UUID, location *GeoLocation) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO geo_locations (user_id, ip_address, country_code, region, city, latitude, longitude, is_vpn, is_proxy, is_tor, risk_score)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		userID, location.IPAddress, location.CountryCode, location.Region, location.City,
		location.Latitude, location.Longitude, location.IsVPN, location.IsProxy, location.IsTor, location.RiskScore)
	if err != nil {
		s.logger.Error("Failed to store location", zap.Error(err))
	}
}

// isCountryBlocked checks if a country is blocked
func (s *GeoSecurityService) isCountryBlocked(ctx context.Context, countryCode string) bool {
	if countryCode == "" {
		return false
	}

	// Check cache
	cacheKey := fmt.Sprintf("blocked_country:%s", countryCode)
	if blocked, err := s.redis.Get(ctx, cacheKey).Result(); err == nil {
		return blocked == "1"
	}

	// Check database
	var exists bool
	s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM blocked_countries WHERE country_code = $1)", countryCode).Scan(&exists)

	// Cache result
	val := "0"
	if exists {
		val = "1"
	}
	s.redis.Set(ctx, cacheKey, val, 1*time.Hour)

	return exists
}

// BlockCountry adds a country to the blocklist
func (s *GeoSecurityService) BlockCountry(ctx context.Context, countryCode, countryName, reason, blockedBy string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO blocked_countries (country_code, country_name, reason, blocked_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (country_code) DO UPDATE SET reason = EXCLUDED.reason`,
		countryCode, countryName, reason, blockedBy)
	if err != nil {
		return err
	}

	// Invalidate cache
	s.redis.Del(ctx, fmt.Sprintf("blocked_country:%s", countryCode))

	s.logger.Info("Country blocked",
		zap.String("country_code", countryCode),
		zap.String("reason", reason))

	return nil
}

// UnblockCountry removes a country from the blocklist
func (s *GeoSecurityService) UnblockCountry(ctx context.Context, countryCode string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM blocked_countries WHERE country_code = $1", countryCode)
	if err != nil {
		return err
	}

	s.redis.Del(ctx, fmt.Sprintf("blocked_country:%s", countryCode))
	return nil
}

// GetBlockedCountries returns all blocked countries
func (s *GeoSecurityService) GetBlockedCountries(ctx context.Context) ([]map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT country_code, country_name, reason FROM blocked_countries")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var countries []map[string]string
	for rows.Next() {
		var code, name, reason string
		if rows.Scan(&code, &name, &reason) == nil {
			countries = append(countries, map[string]string{
				"country_code": code,
				"country_name": name,
				"reason":       reason,
			})
		}
	}

	return countries, nil
}

// haversineDistance calculates distance between two coordinates in km
func haversineDistance(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371 // Earth's radius in km

	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return R * c
}
