package unifi

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// consoleClient implements Client against the per-console Integration API,
// reached through the cloud Connector Proxy:
//
//	https://api.ui.com/v1/connector/consoles/{consoleId}/proxy/network/integration/v1
//
// It is the detail transport: site-scoped devices, clients and networks. The
// cloud surface cannot answer clients/networks at all
// (docs/adr/001-unifi-api-surface.md).
type consoleClient struct {
	t         *transport
	baseURL   string
	consoleID string
}

type consoleInfo struct {
	ApplicationVersion string `json:"applicationVersion"`
}

type consolePage[T any] struct {
	Offset     *int `json:"offset"`
	Limit      *int `json:"limit"`
	Count      *int `json:"count"`
	TotalCount *int `json:"totalCount"`
	Data       *[]T `json:"data"`
}

type consoleSiteEntry struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	InternalReference string `json:"internalReference"`
}

type consoleDeviceEntry struct {
	ID              string   `json:"id"`
	MACAddress      string   `json:"macAddress"`
	IPAddress       string   `json:"ipAddress"`
	Name            string   `json:"name"`
	Model           string   `json:"model"`
	State           string   `json:"state"`
	Supported       bool     `json:"supported"`
	FirmwareVersion string   `json:"firmwareVersion"`
	Features        []string `json:"features"`
}

type consoleClientEntry struct {
	Type         string `json:"type"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	ConnectedAt  string `json:"connectedAt"`
	IPAddress    string `json:"ipAddress"`
	MACAddress   string `json:"macAddress"`
	UplinkDevice string `json:"uplinkDeviceId"`
	Access       struct {
		Type string `json:"type"`
	} `json:"access"`
}

type consoleNetworkEntry struct {
	Management string `json:"management"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	VLANID     int    `json:"vlanId"`
	// Metadata, and its configurable flag, are both optional in the payload: a
	// network the console reports without them has no value to publish, which is
	// null rather than "not configurable".
	Metadata *struct {
		Origin       string `json:"origin"`
		Configurable *bool  `json:"configurable"`
	} `json:"metadata"`
	Default bool `json:"default"`
}

// Health verifies the console is reachable through the proxy and speaks a
// supported Integration API version.
func (c *consoleClient) Health(ctx context.Context) error {
	var info consoleInfo
	if err := c.t.getJSON(ctx, c.baseURL+"/info", &info); err != nil {
		return err
	}
	if strings.TrimSpace(info.ApplicationVersion) == "" {
		return drift("console info response is missing applicationVersion")
	}
	return nil
}

func (c *consoleClient) ListSites(ctx context.Context) ([]Site, error) {
	items, err := fetchPages[consoleSiteEntry](ctx, c.t, c.baseURL+"/sites", func(s consoleSiteEntry) error {
		if strings.TrimSpace(s.ID) == "" {
			return drift("console site entry is missing id")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]Site, 0, len(items))
	for _, s := range items {
		out = append(out, Site{ID: s.ID, Name: s.Name, Desc: s.InternalReference, ConsoleID: c.consoleID, Raw: mustRaw(s)})
	}
	return out, nil
}

func (c *consoleClient) ListDevices(ctx context.Context, siteID string) ([]Device, error) {
	if strings.TrimSpace(siteID) == "" {
		return nil, drift("siteID is required")
	}
	items, err := fetchPages[consoleDeviceEntry](ctx, c.t, c.sitePath(siteID, "devices"), func(d consoleDeviceEntry) error {
		if strings.TrimSpace(d.ID) == "" {
			return drift("console device entry is missing id")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(items))
	for _, d := range items {
		out = append(out, consoleDevice(siteID, d))
	}
	return out, nil
}

func (c *consoleClient) ListClients(ctx context.Context, siteID string) ([]ClientInfo, error) {
	if strings.TrimSpace(siteID) == "" {
		return nil, drift("siteID is required")
	}
	items, err := fetchPages[consoleClientEntry](ctx, c.t, c.sitePath(siteID, "clients"), func(cl consoleClientEntry) error {
		if strings.TrimSpace(cl.ID) == "" {
			return drift("console client entry is missing id")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]ClientInfo, 0, len(items))
	for _, cl := range items {
		var connected time.Time
		if cl.ConnectedAt != "" {
			if t, err := time.Parse(time.RFC3339, cl.ConnectedAt); err == nil {
				connected = t
			}
		}
		out = append(out, ClientInfo{
			ID:          cl.ID,
			SiteID:      siteID,
			MAC:         normalizeMAC(cl.MACAddress),
			Hostname:    cl.Name,
			IP:          cl.IPAddress,
			Type:        strings.ToUpper(strings.TrimSpace(cl.Type)),
			AccessType:  strings.ToUpper(strings.TrimSpace(cl.Access.Type)),
			APID:        cl.UplinkDevice,
			ConnectedAt: connected,
			Connected:   true,
			Raw:         mustRaw(cl),
			// SSID and SignalDBm stay zero: Integration API v1 does not
			// expose them (ADR-001 "Konsekuensi", docs/data-model.md §4.5).
		})
	}
	return out, nil
}

func (c *consoleClient) ListNetworks(ctx context.Context, siteID string) ([]Network, error) {
	if strings.TrimSpace(siteID) == "" {
		return nil, drift("siteID is required")
	}
	items, err := fetchPages[consoleNetworkEntry](ctx, c.t, c.sitePath(siteID, "networks"), func(n consoleNetworkEntry) error {
		if strings.TrimSpace(n.ID) == "" {
			return drift("console network entry is missing id")
		}
		if strings.TrimSpace(n.Name) == "" {
			return drift("console network entry is missing name")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]Network, 0, len(items))
	for _, n := range items {
		var origin string
		var configurable *bool
		if n.Metadata != nil {
			origin = strings.ToUpper(strings.TrimSpace(n.Metadata.Origin))
			configurable = n.Metadata.Configurable
		}
		out = append(out, Network{
			ID:           n.ID,
			SiteID:       siteID,
			Name:         n.Name,
			VLAN:         n.VLANID,
			Management:   strings.ToUpper(strings.TrimSpace(n.Management)),
			Enabled:      n.Enabled,
			IsDefault:    n.Default,
			Origin:       origin,
			Configurable: configurable,
			Raw:          mustRaw(n),
		})
	}
	return out, nil
}

func (c *consoleClient) sitePath(siteID string, resource string) string {
	return c.baseURL + "/sites/" + url.PathEscape(siteID) + "/" + resource
}

// fetchPages walks the Integration API's offset/limit pagination until the
// full snapshot is collected. It is bounded by maxPages so a wrong totalCount
// cannot loop forever.
func fetchPages[T any](ctx context.Context, t *transport, endpoint string, validate func(T) error) ([]T, error) {
	const pageLimit = 200
	var out []T
	offset := 0
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("offset", strconv.Itoa(offset))
		q.Set("limit", strconv.Itoa(pageLimit))

		var env consolePage[T]
		if err := t.getJSON(ctx, endpoint+"?"+q.Encode(), &env); err != nil {
			return nil, err
		}
		if env.Data == nil {
			return nil, drift("list response has no data array")
		}
		items := *env.Data
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			if err := validate(item); err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		offset += len(items)
		switch {
		case env.TotalCount != nil:
			// Trust the server's total; a page may be smaller than pageLimit
			// when the server caps it (the Integration API default is 25).
			if offset >= *env.TotalCount {
				return out, nil
			}
		case len(items) < pageLimit:
			// No total to trust: a short page means we reached the end.
			return out, nil
		}
	}
	return out, nil
}

func consoleDevice(siteID string, d consoleDeviceEntry) Device {
	return Device{
		ID:       d.ID,
		SiteID:   siteID,
		MAC:      d.MACAddress,
		Name:     d.Name,
		Model:    d.Model,
		Type:     deviceType(d.Features, d.Model, false),
		Status:   strings.ToUpper(strings.TrimSpace(d.State)),
		Firmware: d.FirmwareVersion,
		IP:       d.IPAddress,
		Raw:      mustRaw(d),
	}
}
