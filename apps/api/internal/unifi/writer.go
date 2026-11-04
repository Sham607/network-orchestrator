package unifi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RogueAP holds environmental rogue/neighbor AP scan data.
type RogueAP struct {
	BSSID   string `json:"bssid"`
	ESSID   string `json:"essid"`
	Channel int    `json:"channel"`
	Band    string `json:"band"`
	RSSI    int    `json:"rssi"`
	Signal  int    `json:"signal"`
}

// Writer is the interface for executing operational actions against UniFi devices.
type Writer interface {
	RestartDevice(ctx context.Context, consoleID, mac string) error
	TuneRadio(ctx context.Context, consoleID, apID, band string, channel, width int) error
	GetRogueAPs(ctx context.Context, consoleID string) ([]RogueAP, error)
}

type unifiWriter struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewWriter builds a Writer backed by the UniFi Cloud Connector Proxy.
func NewWriter(baseURL, apiKey, caFile string) (Writer, error) {
	if apiKey == "" {
		return nil, errors.New("unifi: api key is required for writer")
	}
	httpClient, err := defaultHTTPClient(caFile)
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeBaseURL(baseURL, defaultCloudBaseURL)
	if err != nil {
		return nil, err
	}
	return &unifiWriter{
		baseURL: normalized,
		apiKey:  apiKey,
		client:  httpClient,
	}, nil
}
func (w *unifiWriter) RestartDevice(ctx context.Context, consoleID, mac string) error {
	if consoleID == "" {
		return errors.New("unifi: consoleID is required for device restart")
	}
	cleanMAC := normalizeMAC(mac)
	if cleanMAC == "" {
		return errors.New("unifi: mac is required for device restart")
	}

	rawURL := fmt.Sprintf("%s/v1/connector/consoles/%s/proxy/network/api/s/default/cmd/devmgr", w.baseURL, consoleID)
	payload := map[string]string{
		"cmd": "restart",
		"mac": cleanMAC,
	}

	return w.sendRequest(ctx, http.MethodPost, rawURL, payload)
}

func (w *unifiWriter) TuneRadio(ctx context.Context, consoleID, apID, band string, channel, width int) error {
	if consoleID == "" || apID == "" {
		return errors.New("unifi: consoleID and apID are required for radio tuning")
	}

	// 1. Fetch AP current device state to get current radio_table
	devURL := fmt.Sprintf("%s/v1/connector/consoles/%s/proxy/network/api/s/default/stat/device", w.baseURL, consoleID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, devURL, nil)
	if err != nil {
		return fmt.Errorf("unifi: cannot build device query: %w", err)
	}
	req.Header.Set("X-API-KEY", w.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("unifi: query device state failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unifi: query device state returned HTTP %d", resp.StatusCode)
	}

	var devResult struct {
		Data []struct {
			ID         string                   `json:"_id"`
			RadioTable []map[string]interface{} `json:"radio_table"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&devResult); err != nil {
		return fmt.Errorf("unifi: decode device state failed: %w", err)
	}

	var targetRadioTable []map[string]interface{}
	for _, d := range devResult.Data {
		if d.ID == apID {
			targetRadioTable = d.RadioTable
			break
		}
	}

	if targetRadioTable == nil {
		return fmt.Errorf("unifi: AP with id %s not found on console %s", apID, consoleID)
	}

	radioKey := "na" // 5 GHz
	if strings.HasPrefix(strings.ToLower(band), "2") {
		radioKey = "ng" // 2.4 GHz
	}

	changed := false
	for _, r := range targetRadioTable {
		if r["radio"] == radioKey {
			r["channel"] = channel
			if width > 0 {
				r["ht"] = width
			}
			changed = true
		}
	}

	if !changed {
		return fmt.Errorf("unifi: radio band %s not found on AP %s", radioKey, apID)
	}

	// 2. PUT updated radio_table to AP
	updURL := fmt.Sprintf("%s/v1/connector/consoles/%s/proxy/network/api/s/default/upd/device/%s", w.baseURL, consoleID, apID)
	updatePayload := map[string]interface{}{
		"radio_table": targetRadioTable,
	}

	return w.sendRequest(ctx, http.MethodPut, updURL, updatePayload)
}

func (w *unifiWriter) sendRequest(ctx context.Context, method, rawURL string, payload any) error {
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var bodyReader io.Reader
	if payload != nil {
		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("unifi: marshal payload failed: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(reqCtx, method, rawURL, bodyReader)
	if err != nil {
		return fmt.Errorf("unifi: cannot build request: %w", err)
	}

	req.Header.Set("X-API-KEY", w.apiKey)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("unifi: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("unifi: upstream error HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return nil
}

func (w *unifiWriter) GetRogueAPs(ctx context.Context, consoleID string) ([]RogueAP, error) {
	if consoleID == "" {
		return nil, errors.New("unifi: consoleID is required for rogue AP scan")
	}
	rawURL := fmt.Sprintf("%s/v1/connector/consoles/%s/proxy/network/api/s/default/stat/rogueap", w.baseURL, consoleID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-KEY", w.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unifi: query rogueap returned HTTP %d", resp.StatusCode)
	}

	var res struct {
		Data []struct {
			BSSID   string `json:"bssid"`
			ESSID   string `json:"essid"`
			Channel int    `json:"channel"`
			Band    string `json:"band"`
			RSSI    int    `json:"rssi"`
			Signal  int    `json:"signal"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	result := make([]RogueAP, len(res.Data))
	for i, r := range res.Data {
		result[i] = RogueAP{
			BSSID:   r.BSSID,
			ESSID:   r.ESSID,
			Channel: r.Channel,
			Band:    r.Band,
			RSSI:    r.RSSI,
			Signal:  r.Signal,
		}
	}
	return result, nil
}
