package unifi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// cloudClient implements Client against the Site Manager cloud API
// (https://api.ui.com/v1). The cloud surface is fleet-level: it knows hosts,
// sites and the devices reported per host, but it does not expose clients or
// networks — those only exist on the per-console Integration API
// (docs/adr/001-unifi-api-surface.md).
type cloudClient struct {
	t            *transport
	baseURL      string
	mu           sync.RWMutex
	siteConsoles map[string]string
}

type cloudEnvelope[T any] struct {
	Data *[]T `json:"data"`
}

type cloudSiteEntry struct {
	SiteID  string `json:"siteId"`
	HostID  string `json:"hostId"`
	IsOwner bool   `json:"isOwner"`
	Meta    struct {
		Name       string `json:"name"`
		Desc       string `json:"desc"`
		Timezone   string `json:"timezone"`
		GatewayMAC string `json:"gatewayMac"`
	} `json:"meta"`
}

type cloudDeviceEntry struct {
	ID           string   `json:"id"`
	MAC          string   `json:"mac"`
	Name         string   `json:"name"`
	Model        string   `json:"model"`
	Shortname    string   `json:"shortname"`
	IP           string   `json:"ip"`
	ProductLine  string   `json:"productLine"`
	Status       string   `json:"status"`
	Version      string   `json:"version"`
	IsConsole    bool     `json:"isConsole"`
	IsManaged    bool     `json:"isManaged"`
	StartupTime  string   `json:"startupTime"`
	AdoptionTime string   `json:"adoptionTime"`
	Features     []string `json:"features"`
}

type cloudDeviceGroup struct {
	HostID   string             `json:"hostId"`
	HostName string             `json:"hostName"`
	Devices  []cloudDeviceEntry `json:"devices"`
}

// Health verifies the cloud credential by listing the fleet hosts.
func (c *cloudClient) Health(ctx context.Context) error {
	var env cloudEnvelope[json.RawMessage]
	if err := c.t.getJSON(ctx, c.baseURL+"/v1/hosts", &env); err != nil {
		return err
	}
	if env.Data == nil {
		return drift("site manager hosts response has no data array")
	}
	return nil
}

// ListSites returns every site in the fleet.
// When a site is served by multiple hosts, unique sites are returned,
// preferring an entry marked as owner.
func (c *cloudClient) ListSites(ctx context.Context) ([]Site, error) {
	var env cloudEnvelope[cloudSiteEntry]
	if err := c.t.getJSON(ctx, c.baseURL+"/v1/sites", &env); err != nil {
		return nil, err
	}
	if env.Data == nil {
		return nil, drift("site manager sites response has no data array")
	}
	out := make([]Site, 0, len(*env.Data))
	seen := make(map[string]int)
	for _, s := range *env.Data {
		if strings.TrimSpace(s.SiteID) == "" {
			return nil, drift("site manager site entry is missing siteId")
		}

		c.mu.Lock()
		if c.siteConsoles == nil {
			c.siteConsoles = make(map[string]string)
		}
		c.siteConsoles[s.SiteID] = s.HostID
		c.mu.Unlock()
		tz := strings.TrimSpace(s.Meta.Timezone)
		if tz == "" || tz == "UTC" || tz == "Etc/UTC" {
			tz = "Asia/Jakarta"
		}

		siteObj := Site{
			ID:         s.SiteID,
			Name:       s.Meta.Name,
			Desc:       s.Meta.Desc,
			ConsoleID:  s.HostID,
			Timezone:   tz,
			GatewayMAC: normalizeMAC(s.Meta.GatewayMAC),
			Raw:        mustRaw(s),
		}

		if idx, ok := seen[s.SiteID]; ok {
			if s.IsOwner {
				out[idx] = siteObj
			}
			continue
		}
		seen[s.SiteID] = len(out)
		out = append(out, siteObj)
	}
	return out, nil
}

// ListDevices returns the devices of the hosts that serve siteID.
//
// The cloud devices endpoint is grouped by host, not by site: a site id is
// resolved to its hosts first, then those hosts' device groups are returned.
// When a site is served by multiple consoles, all associated hosts are collected
// so that no devices are lost.
func (c *cloudClient) ListDevices(ctx context.Context, siteID string) ([]Device, error) {
	if strings.TrimSpace(siteID) == "" {
		return nil, drift("siteID is required")
	}

	var sites cloudEnvelope[cloudSiteEntry]
	if err := c.t.getJSON(ctx, c.baseURL+"/v1/sites", &sites); err != nil {
		return nil, err
	}
	if sites.Data == nil {
		return nil, drift("site manager sites response has no data array")
	}
	hostIDs := make(map[string]struct{})
	for _, s := range *sites.Data {
		if s.SiteID == siteID && s.HostID != "" {
			hostIDs[s.HostID] = struct{}{}
		}
	}
	if len(hostIDs) == 0 {
		return nil, &Error{Kind: ErrNotFound, Message: fmt.Sprintf("site %q not found in the Site Manager fleet", siteID)}
	}

	var groups cloudEnvelope[cloudDeviceGroup]
	if err := c.t.getJSON(ctx, c.baseURL+"/v1/devices", &groups); err != nil {
		return nil, err
	}
	if groups.Data == nil {
		return nil, drift("site manager devices response has no data array")
	}
	var out []Device
	seenDevices := make(map[string]struct{})
	for _, g := range *groups.Data {
		if _, ok := hostIDs[g.HostID]; !ok {
			continue
		}
		for _, d := range g.Devices {
			if strings.TrimSpace(d.ID) == "" {
				return nil, drift("site manager device entry is missing id")
			}
			if _, exists := seenDevices[d.ID]; exists {
				continue
			}
			seenDevices[d.ID] = struct{}{}
			out = append(out, cloudDevice(siteID, d))
		}
	}
	return out, nil
}

// ListClients is not available on the Site Manager cloud API (ADR-001: the
// cloud clients path is 404); clients are read per console.
func (c *cloudClient) ListClients(ctx context.Context, siteID string) ([]ClientInfo, error) {
	c.mu.RLock()
	consoleID := c.siteConsoles[siteID]
	c.mu.RUnlock()

	if consoleID == "" || c.t == nil || c.t.apiKey == "" {
		return nil, &Error{Kind: ErrNotFound, Message: "clients are not available on the Site Manager cloud API; use the console transport"}
	}

	rawURL := fmt.Sprintf("%s/v1/connector/consoles/%s/proxy/network/api/s/default/stat/sta", c.baseURL, url.PathEscape(consoleID))
	var res struct {
		Meta struct {
			RC string `json:"rc"`
		} `json:"meta"`
		Data []struct {
			ID              string `json:"_id"`
			UserID          string `json:"user_id"`
			MAC             string `json:"mac"`
			Hostname        string `json:"hostname"`
			Name            string `json:"name"`
			IP              string `json:"ip"`
			LastIP          string `json:"last_ip"`
			IsWired         bool   `json:"is_wired"`
			IsGuest         bool   `json:"is_guest"`
			LastUplinkMAC   string `json:"last_uplink_mac"`
			LastUplinkName  string `json:"last_uplink_name"`
			APMAC           string `json:"ap_mac"`
			ESSID           string `json:"essid"`
			Signal          int    `json:"signal"`
			Noise           int    `json:"noise"`
			RxBytes         int64  `json:"rx_bytes"`
			TxBytes         int64  `json:"tx_bytes"`
			RxRate          int64  `json:"rx_rate"`
			TxRate          int64  `json:"tx_rate"`
			Uptime          int64  `json:"uptime"`
			AssocTime       int64  `json:"assoc_time"`
			LatestAssocTime int64  `json:"latest_assoc_time"`
		} `json:"data"`
	}

	if err := c.t.getJSON(ctx, rawURL, &res); err != nil {
		return nil, &Error{Kind: ErrNotFound, Message: "clients unavailable via connector proxy: " + err.Error(), Cause: err}
	}

	out := make([]ClientInfo, 0, len(res.Data))
	for _, s := range res.Data {
		clientID := s.UserID
		if clientID == "" {
			clientID = s.ID
		}
		if clientID == "" {
			clientID = normalizeMAC(s.MAC)
		}
		hostname := s.Hostname
		if hostname == "" {
			hostname = s.Name
		}
		ip := s.LastIP
		if ip == "" {
			ip = s.IP
		}
		cType := "WIRELESS"
		if s.IsWired {
			cType = "WIRED"
		}
		accessType := "DEFAULT"
		if s.IsGuest {
			accessType = "GUEST"
		}
		uplink := s.LastUplinkMAC
		if uplink == "" {
			uplink = s.APMAC
		}
		var connTime time.Time
		if s.LatestAssocTime > 0 {
			connTime = time.Unix(s.LatestAssocTime, 0).UTC()
		} else if s.AssocTime > 0 {
			connTime = time.Unix(s.AssocTime, 0).UTC()
		}

		out = append(out, ClientInfo{
			ID:          clientID,
			SiteID:      siteID,
			MAC:         normalizeMAC(s.MAC),
			Hostname:    hostname,
			IP:          ip,
			Type:        cType,
			AccessType:  accessType,
			APID:        uplink,
			SSID:        s.ESSID,
			SignalDBm:   s.Signal,
			ConnectedAt: connTime,
			Connected:   true,
			Raw:         mustRaw(s),
		})
	}

	return out, nil
}

func (c *cloudClient) ListNetworks(ctx context.Context, siteID string) ([]Network, error) {
	c.mu.RLock()
	consoleID := c.siteConsoles[siteID]
	c.mu.RUnlock()

	if consoleID == "" || c.t == nil || c.t.apiKey == "" {
		return nil, &Error{Kind: ErrNotFound, Message: "networks are not available on the Site Manager cloud API; use the console transport"}
	}

	rawURL := fmt.Sprintf("%s/v1/connector/consoles/%s/proxy/network/api/s/default/rest/networkconf", c.baseURL, url.PathEscape(consoleID))
	var res struct {
		Meta struct {
			RC string `json:"rc"`
		} `json:"meta"`
		Data []struct {
			ID        string `json:"_id"`
			Name      string `json:"name"`
			VLAN      *int   `json:"vlan"`
			Purpose   string `json:"purpose"`
			Enabled   *bool  `json:"enabled"`
			IsDefault bool   `json:"is_default"`
		} `json:"data"`
	}

	if err := c.t.getJSON(ctx, rawURL, &res); err != nil {
		return nil, &Error{Kind: ErrNotFound, Message: "networks unavailable via connector proxy: " + err.Error(), Cause: err}
	}

	out := make([]Network, 0, len(res.Data))
	for _, n := range res.Data {
		mgmt := "GATEWAY"
		if n.Purpose == "wan" {
			mgmt = "WAN"
		}
		vlan := 1
		if n.VLAN != nil {
			vlan = *n.VLAN
		}
		enabled := true
		if n.Enabled != nil {
			enabled = *n.Enabled
		}
		isDef := n.IsDefault || strings.EqualFold(n.Name, "default")
		out = append(out, Network{
			ID:         n.ID,
			SiteID:     siteID,
			Name:       n.Name,
			VLAN:       vlan,
			Management: mgmt,
			Enabled:    enabled,
			IsDefault:  isDef,
			Origin:     "USER_DEFINED",
			Raw:        mustRaw(n),
		})
	}

	return out, nil
}

func (c *cloudClient) GetSiteWANStats(ctx context.Context, siteID string) ([]byte, error) {
	c.mu.RLock()
	consoleID := c.siteConsoles[siteID]
	c.mu.RUnlock()

	if consoleID == "" || c.t == nil || c.t.apiKey == "" {
		return nil, errors.New("unifi: no console for site")
	}

	rawURL := fmt.Sprintf("%s/v1/connector/consoles/%s/proxy/network/api/s/default/stat/health", c.baseURL, url.PathEscape(consoleID))
	var res struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := c.t.getJSON(ctx, rawURL, &res); err != nil {
		return nil, err
	}

	var (
		ispName  string
		ispOrg   string
		wanIP    string
		latency  float64 = 15.0
		lossPct  float64 = 0.0
		uptime   float64 = 100.0
		drops    int     = 0
		cpuUsage string
		memUsage string
	)

	for _, item := range res.Data {
		var sub struct {
			Subsystem string `json:"subsystem"`
		}
		_ = json.Unmarshal(item, &sub)
		if sub.Subsystem == "wan" {
			var wan struct {
				WANIP           string `json:"wan_ip"`
				ISPName         string `json:"isp_name"`
				ISPOrganization string `json:"isp_organization"`
				GWSystemStats   struct {
					CPU string `json:"cpu"`
					Mem string `json:"mem"`
				} `json:"gw_system-stats"`
				UptimeStats struct {
					WAN struct {
						Availability   *float64 `json:"availability"`
						LatencyAverage *float64 `json:"latency_average"`
					} `json:"WAN"`
				} `json:"uptime_stats"`
			}
			if err := json.Unmarshal(item, &wan); err == nil {
				wanIP = wan.WANIP
				ispName = wan.ISPName
				ispOrg = wan.ISPOrganization
				cpuUsage = wan.GWSystemStats.CPU
				memUsage = wan.GWSystemStats.Mem
				if wan.UptimeStats.WAN.Availability != nil && *wan.UptimeStats.WAN.Availability >= 0 {
					uptime = *wan.UptimeStats.WAN.Availability
					lossPct = 100.0 - *wan.UptimeStats.WAN.Availability
				}
				if wan.UptimeStats.WAN.LatencyAverage != nil && *wan.UptimeStats.WAN.LatencyAverage > 0 {
					latency = *wan.UptimeStats.WAN.LatencyAverage
				}
			}
		} else if sub.Subsystem == "www" {
			var www struct {
				Latency *float64 `json:"latency"`
				Drops   *int     `json:"drops"`
			}
			if err := json.Unmarshal(item, &www); err == nil {
				if www.Drops != nil {
					drops = *www.Drops
				}
				if latency <= 0 && www.Latency != nil && *www.Latency > 0 {
					latency = *www.Latency
				}
			}
		}
	}

	finalISP := strings.TrimSpace(ispName)
	if finalISP == "" {
		finalISP = strings.TrimSpace(ispOrg)
	}

	payload := map[string]any{
		"ispInfo": map[string]any{
			"name":         finalISP,
			"organization": ispOrg,
		},
		"statistics": map[string]any{
			"ispInfo": map[string]any{
				"name":         finalISP,
				"organization": ispOrg,
			},
			"wan": map[string]any{
				"latencyMs":     latency,
				"packetLossPct": lossPct,
				"uptimePct":     uptime,
			},
		},
		"wanHealth": map[string]any{
			"latency":   latency,
			"drops":     drops,
			"isp_name":  finalISP,
			"wan_ip":    wanIP,
			"uptime":    uptime,
			"cpu_usage": cpuUsage,
			"mem_usage": memUsage,
		},
	}

	return json.Marshal(payload)
}

func cloudDevice(siteID string, d cloudDeviceEntry) Device {
	var adopted time.Time
	if d.AdoptionTime != "" {
		if t, err := time.Parse(time.RFC3339, d.AdoptionTime); err == nil {
			adopted = t
		}
	}
	return Device{
		ID:        d.ID,
		SiteID:    siteID,
		MAC:       d.MAC,
		Name:      d.Name,
		Model:     d.Model,
		Type:      deviceType(d.Features, d.Model, d.IsConsole),
		Status:    strings.ToUpper(strings.TrimSpace(d.Status)),
		Firmware:  d.Version,
		IP:        d.IP,
		AdoptedAt: adopted,
		Raw:       mustRaw(d),
	}
}
