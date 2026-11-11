package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sham607/network-orchestrator/apps/api/internal/audit"
	"github.com/Sham607/network-orchestrator/apps/api/internal/config"
	"github.com/Sham607/network-orchestrator/apps/api/internal/identity"
	"github.com/Sham607/network-orchestrator/apps/api/internal/inventory"
)

type fakeIdentity struct {
	users map[string]struct {
		pass string
		user identity.User
	}
	sessions map[string]identity.Session
	revoked  map[string]bool
}

func newFakeIdentity() *fakeIdentity {
	return &fakeIdentity{
		users: map[string]struct {
			pass string
			user identity.User
		}{
			"admin@kantor.com": {
				pass: "correct-horse-battery",
				user: identity.User{ID: "8f14e45f-ea0d-4c1b-9a3e-1f2a3b4c5d6e", Email: "admin@kantor.com", Role: identity.RoleAdmin},
			},
			"viewer@kantor.com": {
				pass: "viewer-password-123",
				user: identity.User{ID: "11111111-2222-4333-8444-555555555555", Email: "viewer@kantor.com", Role: identity.RoleViewer},
			},
		},
		sessions: map[string]identity.Session{},
		revoked:  map[string]bool{},
	}
}

func (f *fakeIdentity) Authenticate(_ context.Context, email, password, ip, _ string) (string, identity.Session, error) {
	entry, ok := f.users[email]
	if !ok || entry.pass != password {
		return "", identity.Session{}, identity.ErrInvalidCredentials
	}
	raw, hash, err := identity.GenerateSessionToken()
	if err != nil {
		return "", identity.Session{}, err
	}
	sess := identity.Session{
		ID:        "9a14e45f-ea0d-4c1b-9a3e-1f2a3b4c5d6e",
		User:      entry.user,
		CSRFToken: identity.DeriveCSRFToken(hash, "9a14e45f-ea0d-4c1b-9a3e-1f2a3b4c5d6e"),
		ExpiresAt: time.Now().UTC().Add(time.Hour),
		IP:        ip,
	}
	f.sessions[raw] = sess
	return raw, sess, nil
}

func (f *fakeIdentity) ValidateSession(_ context.Context, rawToken string) (identity.Session, error) {
	if f.revoked[rawToken] {
		return identity.Session{}, identity.ErrSessionExpired
	}
	s, ok := f.sessions[rawToken]
	if !ok {
		return identity.Session{}, identity.ErrSessionExpired
	}
	return s, nil
}

func (f *fakeIdentity) RevokeSession(_ context.Context, rawToken string) error {
	f.revoked[rawToken] = true
	delete(f.sessions, rawToken)
	return nil
}

func (f *fakeIdentity) EnsureBootstrapAdmin(_ context.Context, _, _ string) error {
	return nil
}

type fakeAudit struct {
	entries []audit.Entry
}

func (a *fakeAudit) Record(_ context.Context, e audit.Entry) error {
	a.entries = append(a.entries, e)
	return nil
}

func (a *fakeAudit) RecordAction(_ context.Context, e audit.Entry) (int64, time.Time, error) {
	a.entries = append(a.entries, e)
	return int64(len(a.entries)), time.Now().UTC(), nil
}

func TestAuthLoginMeLogoutAndContractCompliance(t *testing.T) {
	idSvc := newFakeIdentity()
	aud := &fakeAudit{}
	router := NewRouter(Dependencies{
		Config: config.Config{
			AuthEnabled: true,
			UnifiMode:   config.UnifiModeCloud,
		},
		Identity:  idSvc,
		Audit:     aud,
		Inventory: &fakeInventory{},
	})

	// 1. Protected endpoint rejects unauthenticated request when AuthEnabled=true
	unauthRR := get(t, router, "/api/v1/sites")
	if unauthRR.Code != http.StatusUnauthorized {
		t.Fatalf("GET /sites without cookie: status = %d, want 401", unauthRR.Code)
	}

	// 2. Public probe /health remains accessible without cookie
	healthRR := get(t, router, "/api/v1/health")
	if healthRR.Code != http.StatusOK {
		t.Fatalf("GET /health without cookie: status = %d, want 200", healthRR.Code)
	}

	// 3. Login with valid credentials sets cookie and matches contract
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		bytes.NewBufferString(`{"email":"admin@kantor.com","password":"correct-horse-battery"}`))
	loginRR := httptest.NewRecorder()
	router.ServeHTTP(loginRR, loginReq)

	if loginRR.Code != http.StatusOK {
		t.Fatalf("POST /auth/login: status = %d, body = %s", loginRR.Code, loginRR.Body.String())
	}
	assertMatchesContract(t, "LoginResponse", loginRR.Body.String())

	cookies := loginRR.Result().Cookies()
	var sessionCookie, csrfCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "session" {
			sessionCookie = c
		}
		if c.Name == "csrf_token" {
			csrfCookie = c
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly {
		t.Fatalf("expected HttpOnly session cookie, got %+v", sessionCookie)
	}
	if csrfCookie == nil || csrfCookie.Value == "" {
		t.Fatalf("expected csrf_token cookie, got %+v", csrfCookie)
	}

	// 4. GET /me with session cookie returns operator profile matching User schema
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	meReq.AddCookie(sessionCookie)
	meRR := httptest.NewRecorder()
	router.ServeHTTP(meRR, meReq)

	if meRR.Code != http.StatusOK {
		t.Fatalf("GET /me: status = %d, body = %s", meRR.Code, meRR.Body.String())
	}
	assertMatchesContract(t, "User", meRR.Body.String())

	// 5. Logout revokes session
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	logoutRR := httptest.NewRecorder()
	router.ServeHTTP(logoutRR, logoutReq)
	if logoutRR.Code != http.StatusNoContent {
		t.Fatalf("POST /auth/logout: status = %d, want 204", logoutRR.Code)
	}

	// 6. Re-using revoked session is rejected with 401
	meAfterRR := httptest.NewRecorder()
	router.ServeHTTP(meAfterRR, meReq)
	if meAfterRR.Code != http.StatusUnauthorized {
		t.Fatalf("GET /me after logout: status = %d, want 401", meAfterRR.Code)
	}
}

func TestDeviceActionRBACAndAuditAttribution(t *testing.T) {
	idSvc := newFakeIdentity()
	aud := &fakeAudit{}
	apID := "2a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6d"
	siteID := "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	name := "AP Lantai 2"
	mac := "74:83:c2:11:22:33"

	inv := &fakeInventory{
		getDevice: inventory.Device{
			ID:            apID,
			SiteID:        siteID,
			ConsoleID:     "console-1",
			UnifiDeviceID: "unifi-ap-1",
			Name:          &name,
			MAC:           &mac,
			DeviceType:    "ap",
			SyncedAt:      time.Now().UTC(),
		},
		getDeviceFound: true,
	}

	router := NewRouter(Dependencies{
		Config: config.Config{
			AuthEnabled:  true,
			WriteEnabled: true,
			UnifiMode:    config.UnifiModeLocal,
		},
		Identity:  idSvc,
		Audit:     aud,
		Inventory: inv,
	})

	// Viewer cannot run RESTART
	viewerTok, viewerSess, err := idSvc.Authenticate(context.Background(), "viewer@kantor.com", "viewer-password-123", "10.0.0.10", "test")
	if err != nil {
		t.Fatalf("auth viewer: %v", err)
	}
	viewerReq := httptest.NewRequest(http.MethodPost, "/api/v1/devices/"+apID+"/actions",
		bytes.NewBufferString(`{"action":"RESTART"}`))
	viewerReq.AddCookie(&http.Cookie{Name: "session", Value: viewerTok})
	viewerReq.Header.Set("X-CSRF-Token", viewerSess.CSRFToken)
	viewerRR := httptest.NewRecorder()
	router.ServeHTTP(viewerRR, viewerReq)
	if viewerRR.Code != http.StatusForbidden {
		t.Fatalf("viewer POST /devices/{id}/actions: status = %d, want 403", viewerRR.Code)
	}

	// Admin with valid CSRF succeeds and records real operator identity in audit_log
	adminTok, adminSess, err := idSvc.Authenticate(context.Background(), "admin@kantor.com", "correct-horse-battery", "10.0.0.99", "test")
	if err != nil {
		t.Fatalf("auth admin: %v", err)
	}
	adminReq := httptest.NewRequest(http.MethodPost, "/api/v1/devices/"+apID+"/actions",
		bytes.NewBufferString(`{"action":"TUNE_RADIO","band":"5g","channel":149,"width":40}`))
	adminReq.AddCookie(&http.Cookie{Name: "session", Value: adminTok})
	adminReq.Header.Set("X-CSRF-Token", adminSess.CSRFToken)
	adminRR := httptest.NewRecorder()
	router.ServeHTTP(adminRR, adminReq)
	if adminRR.Code != http.StatusAccepted {
		t.Fatalf("admin POST /devices/{id}/actions: status = %d, body = %s", adminRR.Code, adminRR.Body.String())
	}

	lastAudit := aud.entries[len(aud.entries)-1]
	if lastAudit.ActorLabel != "admin@kantor.com" {
		t.Errorf("audit ActorLabel = %q, want admin@kantor.com", lastAudit.ActorLabel)
	}
	if lastAudit.ActorUserID == nil || *lastAudit.ActorUserID != adminSess.User.ID {
		t.Errorf("audit ActorUserID = %v, want %s", lastAudit.ActorUserID, adminSess.User.ID)
	}
}

func TestDeviceRadiosAndWANHealthAreDerivedFromCache(t *testing.T) {
	siteID := "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	gwName := "UDM Pro Jakarta"
	gwModel := "UDM-Pro"
	gwStatus := "ONLINE"
	gwMAC := "aa:bb:cc:11:22:33"
	std := "802.11ax"
	freq := 5.0
	width := 40
	ch := 149

	inv := &fakeInventory{
		getSite: inventory.Site{
			ID:         siteID,
			Name:       "Jakarta HQ",
			GatewayMAC: &gwMAC,
			SyncedAt:   time.Now().UTC(),
		},
		getSiteFound: true,
		devices: inventory.DevicePage{
			Total: 1,
			Items: []inventory.Device{
				{
					ID:         "2a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6d",
					SiteID:     siteID,
					Name:       &gwName,
					Model:      &gwModel,
					MAC:        &gwMAC,
					DeviceType: "gateway",
					Status:     &gwStatus,
					Radios: []inventory.Radio{
						{
							WlanStandard:    &std,
							FrequencyGHz:    &freq,
							ChannelWidthMHz: &width,
							Channel:         &ch,
						},
					},
					SyncedAt: time.Now().UTC(),
				},
			},
		},
	}

	router := listRouter(t, inv)

	// 1. Device list exposes normalized radios
	devRR := get(t, router, "/api/v1/devices")
	if devRR.Code != http.StatusOK {
		t.Fatalf("GET /devices: status = %d", devRR.Code)
	}
	assertMatchesContract(t, "DeviceList", devRR.Body.String())
	var devList deviceListBody
	if err := json.Unmarshal(devRR.Body.Bytes(), &devList); err != nil {
		t.Fatalf("decode DeviceList: %v", err)
	}
	if len(devList.Data) != 1 || len(devList.Data[0].Radios) != 1 || *devList.Data[0].Radios[0].Channel != 149 {
		t.Fatalf("device radios = %+v, want channel 149", devList.Data[0].Radios)
	}

	// 2. WAN health uses cached gateway name/model rather than hardcoded Astinet string
	wanRR := get(t, router, "/api/v1/sites/"+siteID+"/wan-health")
	if wanRR.Code != http.StatusOK {
		t.Fatalf("GET /sites/{id}/wan-health: status = %d", wanRR.Code)
	}
	var wan WANHealthResponse
	if err := json.Unmarshal(wanRR.Body.Bytes(), &wan); err != nil {
		t.Fatalf("decode WANHealthResponse: %v", err)
	}
	if wan.PrimaryWAN != "UDM Pro Jakarta (UDM-Pro)" {
		t.Errorf("PrimaryWAN = %q, want 'UDM Pro Jakarta (UDM-Pro)'", wan.PrimaryWAN)
	}
}
