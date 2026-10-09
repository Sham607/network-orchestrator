package unifi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// cloudClient implements Client against the Site Manager cloud API
// (https://api.ui.com/v1). The cloud surface is fleet-level: it knows hosts,
// sites and the devices reported per host, but it does not expose clients or
// networks — those only exist on the per-console Integration API
// (docs/adr/001-unifi-api-surface.md).
type cloudClient struct {
	t       *transport
	baseURL string
}

type cloudEnvelope[T any] struct {
	Data *[]T `json:"data"`
}

type cloudSiteEntry struct {
	SiteID string `json:"siteId"`
	HostID string `json:"hostId"`
	Meta   struct {
		Name string `json:"name"`
		Desc string `json:"desc"`
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
func (c *cloudClient) ListSites(ctx context.Context) ([]Site, error) {
	var env cloudEnvelope[cloudSiteEntry]
	if err := c.t.getJSON(ctx, c.baseURL+"/v1/sites", &env); err != nil {
		return nil, err
	}
	if env.Data == nil {
		return nil, drift("site manager sites response has no data array")
	}
	out := make([]Site, 0, len(*env.Data))
	for _, s := range *env.Data {
		if strings.TrimSpace(s.SiteID) == "" {
			return nil, drift("site manager site entry is missing siteId")
		}
		out = append(out, Site{ID: s.SiteID, Name: s.Meta.Name, Desc: s.Meta.Desc, ConsoleID: s.HostID, Raw: mustRaw(s)})
	}
	return out, nil
}

// ListDevices returns the devices of the host that owns siteID.
//
// The cloud devices endpoint is grouped by host, not by site: a site id is
// resolved to its host first, then that host's device group is returned. On a
// host that serves several sites the groups are shared, which is a limitation
// of the cloud surface, not of this adapter.
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
	hostID := ""
	for _, s := range *sites.Data {
		if s.SiteID == siteID {
			hostID = s.HostID
			break
		}
	}
	if hostID == "" {
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
	for _, g := range *groups.Data {
		if g.HostID != hostID {
			continue
		}
		for _, d := range g.Devices {
			if strings.TrimSpace(d.ID) == "" {
				return nil, drift("site manager device entry is missing id")
			}
			out = append(out, cloudDevice(siteID, d))
		}
	}
	return out, nil
}

// ListClients is not available on the Site Manager cloud API (ADR-001: the
// cloud clients path is 404); clients are read per console.
func (c *cloudClient) ListClients(context.Context, string) ([]ClientInfo, error) {
	return nil, &Error{Kind: ErrNotFound, Message: "clients are not available on the Site Manager cloud API; use the console transport"}
}

// ListNetworks is not available on the Site Manager cloud API; networks are
// read per console.
func (c *cloudClient) ListNetworks(context.Context, string) ([]Network, error) {
	return nil, &Error{Kind: ErrNotFound, Message: "networks are not available on the Site Manager cloud API; use the console transport"}
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
