package models

import "time"

// PNode represents a storage provider node
type PNode struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Status          string    `json:"status"` // active, inactive, warning
	Uptime          float64   `json:"uptime"`
	Latency         int       `json:"latency"` // milliseconds
	Validations     int       `json:"validations"`
	Rewards         float64   `json:"rewards"`
	Location        string    `json:"location"`
	Region          string    `json:"region"`
	StorageUsed     int64     `json:"storageUsed"`     // bytes
	StorageCapacity int64     `json:"storageCapacity"` // bytes
	LastSeen        time.Time `json:"lastSeen"`
	Performance     float64   `json:"performance"`
	Stake           float64   `json:"stake"`
	RiskScore       float64   `json:"riskScore"`
}

// DashboardStats represents dashboard overview statistics
type DashboardStats struct {
	TotalNodes     int     `json:"totalNodes"`
	ActiveNodes    int     `json:"activeNodes"`
	NetworkHealth  float64 `json:"networkHealth"`
	TotalRewards   float64 `json:"totalRewards"`
	AverageLatency float64 `json:"averageLatency"`
	ValidationRate float64 `json:"validationRate"`
	Timestamp      int64   `json:"timestamp"`
}

// PNodeHistory represents historical metrics for a pNode
type PNodeHistory struct {
	Timestamp   int64   `json:"timestamp"`
	Latency     int     `json:"latency"`
	Uptime      float64 `json:"uptime"`
	StorageUsed int64   `json:"storageUsed"`
	Rewards     float64 `json:"rewards"`
}

// PeerInfo represents information about a connected peer
type PeerInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Latency int    `json:"latency"`
}

// Alert represents a pNode alert/notification
type Alert struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
	Severity  string `json:"severity"` // low, medium, high
}

// HeatmapPoint represents a point on the network heatmap
type HeatmapPoint struct {
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	Intensity float64 `json:"intensity"`
	NodeCount int     `json:"nodeCount"`
	Region    string  `json:"region"`
	AvgUptime float64 `json:"avgUptime"`
}

// APIResponse represents a standard API response
type APIResponse struct {
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
}

// HealthStatus represents the health check response
type HealthStatus struct {
	Status    string            `json:"status"`
	Uptime    string            `json:"uptime"`
	Services  map[string]string `json:"services"`
	Timestamp int64             `json:"timestamp"`
}
