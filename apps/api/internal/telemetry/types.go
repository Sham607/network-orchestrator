package telemetry

// MessageType indicates the kind of telemetry event streamed to clients.
type MessageType string

const (
	TypeTick         MessageType = "telemetry_tick"
	TypeLossAlert    MessageType = "loss_alert"
	TypeTrafficSpike MessageType = "traffic_spike_alert"
)

// ClientSpikeMetric describes real-time throughput for a single client device.
type ClientSpikeMetric struct {
	MAC          string  `json:"mac"`
	Hostname     string  `json:"hostname"`
	IPAddress    string  `json:"ip_address"`
	APName       string  `json:"ap_name"`
	DownloadMbps float64 `json:"download_mbps"`
	UploadMbps   float64 `json:"upload_mbps"`
	IsSpike      bool    `json:"is_spike"`
}

// SiteLossMetric describes real-time WAN packet loss and round-trip time.
type SiteLossMetric struct {
	SiteID     string  `json:"site_id"`
	SiteName   string  `json:"site_name"`
	LatencyMs  float64 `json:"latency_ms"`
	LossPct    float64 `json:"loss_pct"`
	Status     string  `json:"status"` // "healthy", "degraded", "critical"
	PrimaryWAN string  `json:"primary_wan"`
}

// TelemetryPayload is the data broadcast over WebSocket every interval.
type TelemetryPayload struct {
	Timestamp      string              `json:"timestamp"`
	GlobalLossPct  float64             `json:"global_loss_pct"`
	AvgLatencyMs   float64             `json:"avg_latency_ms"`
	ActiveSites    int                 `json:"active_sites"`
	TotalClients   int                 `json:"total_clients"`
	Sites          []SiteLossMetric    `json:"sites"`
	TopTalkers     []ClientSpikeMetric `json:"top_talkers"`
	HasSpike       bool                `json:"has_spike"`
	HasLossWarning bool                `json:"has_loss_warning"`
}

// Message is the top-level envelope streamed to WebSocket clients.
type Message struct {
	Type MessageType      `json:"type"`
	Data TelemetryPayload `json:"data"`
}
