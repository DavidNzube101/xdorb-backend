package geolocation

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ip2location/ip2location-go/v9"
)

var db *ip2location.DB
var client = &http.Client{Timeout: 5 * time.Second}
var useOnlineAPI bool

// InitDB initializes the IP2Location database, falls back to online API
func InitDB(dbPath string) error {
	if dbPath != "" {
		// Try local DB first
		var err error
		db, err = ip2location.OpenDB(dbPath)
		if err != nil {
			log.Printf("Local DB failed (%s), falling back to online API: %v", dbPath, err)
			useOnlineAPI = true
		} else {
			log.Printf("IP2Location DB loaded successfully from %s", dbPath)
			useOnlineAPI = false
			return nil
		}
	} else {
		useOnlineAPI = true
	}

	// Test online API
	_, err := GetLocation("8.8.8.8")
	if err != nil {
		return fmt.Errorf("failed to test geolocation API: %w", err)
	}
	log.Println("Geolocation online API initialized successfully")
	return nil
}

// CloseDB closes the database if using local DB
func CloseDB() {
	if db != nil && !useOnlineAPI {
		db.Close()
	}
}

// Location represents geolocation data
type Location struct {
	Country   string  `json:"country"`
	City      string  `json:"city"`
	Region    string  `json:"region"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timezone  string  `json:"timezone"`
}

// GetLocation looks up geolocation for an IP address
func GetLocation(ip string) (*Location, error) {
	if !useOnlineAPI && db != nil {
		// Use local DB
		results, err := db.Get_all(ip)
		if err != nil {
			return nil, fmt.Errorf("failed to lookup IP %s in local DB: %w", ip, err)
		}

		return &Location{
			Country:   results.Country_long,
			City:      results.City,
			Region:    results.Region,
			Latitude:  float64(results.Latitude),
			Longitude: float64(results.Longitude),
			Timezone:  results.Timezone,
		}, nil
	}

	// Use online API
	url := fmt.Sprintf("http://ipapi.co/%s/json/", ip)

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to query geolocation API for %s: %w", ip, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("geolocation API returned status %d for %s", resp.StatusCode, ip)
	}

	var apiResp struct {
		CountryName string  `json:"country_name"`
		City        string  `json:"city"`
		Region      string  `json:"region"`
		Latitude    float64 `json:"latitude"`
		Longitude   float64 `json:"longitude"`
		Timezone    string  `json:"timezone"`
		Error       bool    `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode geolocation response for %s: %w", ip, err)
	}

	if apiResp.Error {
		return nil, fmt.Errorf("geolocation API error for %s", ip)
	}

	return &Location{
		Country:   apiResp.CountryName,
		City:      apiResp.City,
		Region:    apiResp.Region,
		Latitude:  apiResp.Latitude,
		Longitude: apiResp.Longitude,
		Timezone:  apiResp.Timezone,
	}, nil
}

// GetLocationString returns a formatted location string
func (l *Location) GetLocationString() string {
	parts := []string{}
	if l.City != "" {
		parts = append(parts, l.City)
	}
	if l.Region != "" {
		parts = append(parts, l.Region)
	}
	if l.Country != "" {
		parts = append(parts, l.Country)
	}
	return strings.Join(parts, ", ")
}
