package geolocation

import (
	"fmt"
	"log"
	"strings"

	"github.com/ip2location/ip2location-go/v9"
)

var db *ip2location.DB

// InitDB initializes the IP2Location database
func InitDB(dbPath string) error {
	var err error
	db, err = ip2location.OpenDB(dbPath)
	if err != nil {
		log.Printf("Warning: Failed to load IP2Location DB from %s: %v", dbPath, err)
		return err
	}
	log.Println("IP2Location DB loaded successfully")
	return nil
}

// CloseDB closes the database
func CloseDB() {
	if db != nil {
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
	if db == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	results, err := db.Get_all(ip)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup IP %s: %w", ip, err)
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
