package httpapi

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

const (
	bootstrapSessionCookie = "nvoken_bootstrap_session"
	bootstrapCSRFCookie    = "nvoken_bootstrap_csrf"
)

type IdentityService interface {
	CurrentAccount(context.Context, domain.RuntimeAuthContext) (services.AccountIdentity, error)
	ListCredentials(context.Context, domain.RuntimeAuthContext) ([]domain.Credential, error)
	GetCredential(context.Context, domain.RuntimeAuthContext, string) (domain.Credential, error)
	CreateMachineCredential(context.Context, domain.RuntimeAuthContext, string, services.CredentialCreateInput) (services.CredentialIssuanceResult, error)
	RotateMachineCredential(context.Context, domain.RuntimeAuthContext, string, string, time.Duration) (services.CredentialIssuanceResult, error)
	RevokeCredential(context.Context, domain.RuntimeAuthContext, string) (domain.Credential, error)
	StartDeviceAuthorization(context.Context, services.DeviceCodeInput) (services.DeviceCodeResult, error)
	PollDeviceAuthorization(context.Context, string) (services.CredentialIssuanceResult, error)
	CreateBootstrapBrowserSession(context.Context, string) (services.BootstrapBrowserSession, error)
	ResolveBrowserSession(context.Context, string, string) (domain.BrowserSession, error)
	DeviceApproval(context.Context, string, domain.BrowserSession) (services.DeviceApprovalView, error)
	ConfirmDeviceAuthorization(context.Context, string, bool, domain.BrowserSession) error
}

type credentialResponse struct {
	ID                    string                    `json:"id"`
	Kind                  domain.CredentialKind     `json:"kind"`
	Name                  string                    `json:"name"`
	Prefix                string                    `json:"prefix"`
	Status                domain.CredentialStatus   `json:"status"`
	Profile               *domain.CredentialProfile `json:"profile,omitempty"`
	RoleCap               *domain.CredentialProfile `json:"role_cap,omitempty"`
	OwnerSubjectID        *string                   `json:"owner_subject_id,omitempty"`
	CreatorSubjectID      *string                   `json:"creator_subject_id,omitempty"`
	CreatorCredentialID   *string                   `json:"creator_credential_id,omitempty"`
	TenantConstraint      *string                   `json:"tenant_key,omitempty"`
	SessionConstraint     *string                   `json:"session_id,omitempty"`
	OperationConstraints  []domain.RuntimeOperation `json:"operations"`
	ExpiresAt             *time.Time                `json:"expires_at,omitempty"`
	RotatedFromID         *string                   `json:"rotated_from_id,omitempty"`
	RotationOverlapEndsAt *time.Time                `json:"rotation_overlap_ends_at,omitempty"`
	RevokedAt             *time.Time                `json:"revoked_at,omitempty"`
	LastUsedAt            *time.Time                `json:"last_used_at,omitempty"`
	CreatedAt             time.Time                 `json:"created_at"`
	UpdatedAt             time.Time                 `json:"updated_at"`
}

type credentialIssuanceResponse struct {
	Credential        credentialResponse `json:"credential"`
	Secret            string             `json:"secret"`
	DeliveryExpiresAt time.Time          `json:"delivery_expires_at"`
	Replayed          bool               `json:"replayed"`
}

func credentialResponseFromDomain(value domain.Credential) credentialResponse {
	operations := value.OperationConstraints
	if operations == nil {
		operations = []domain.RuntimeOperation{}
	}
	return credentialResponse{
		ID:                    value.ID,
		Kind:                  value.Kind,
		Name:                  value.Name,
		Prefix:                value.Prefix,
		Status:                value.Status,
		Profile:               value.Profile,
		RoleCap:               value.RoleCap,
		OwnerSubjectID:        value.OwnerSubjectID,
		CreatorSubjectID:      value.CreatorSubjectID,
		CreatorCredentialID:   value.CreatorCredentialID,
		TenantConstraint:      value.TenantConstraint,
		SessionConstraint:     value.SessionConstraint,
		OperationConstraints:  operations,
		ExpiresAt:             value.ExpiresAt,
		RotatedFromID:         value.RotatedFromID,
		RotationOverlapEndsAt: value.RotationOverlapEndsAt,
		RevokedAt:             value.RevokedAt,
		LastUsedAt:            value.LastUsedAt,
		CreatedAt:             value.CreatedAt,
		UpdatedAt:             value.UpdatedAt,
	}
}

func credentialIssuanceResponseFromService(value services.CredentialIssuanceResult) credentialIssuanceResponse {
	return credentialIssuanceResponse{
		Credential:        credentialResponseFromDomain(value.Credential),
		Secret:            value.Secret,
		DeliveryExpiresAt: value.ExpiresAt,
		Replayed:          value.Replayed,
	}
}

func (h *handler) getCurrentAccount(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	auth, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	if h.identity == nil {
		h.writeError(w, requestID, identityUnavailable())
		return
	}
	current, err := h.identity.CurrentAccount(r.Context(), auth)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	response := map[string]any{
		"id":         current.Account.ID,
		"created_at": current.Account.CreatedAt,
		"authentication": map[string]any{
			"credential_id":     current.Credential.ID,
			"credential_kind":   current.Credential.Kind,
			"effective_profile": current.EffectiveRole,
			"tenant_key":        current.Credential.TenantConstraint,
			"session_id":        current.Credential.SessionConstraint,
			"operations":        current.Operations,
			"method":            current.Method,
			"assurance":         current.Assurance,
		},
	}
	if current.Subject != nil {
		response["subject"] = map[string]any{
			"id": current.Subject.ID, "issuer": current.Subject.Issuer, "subject": current.Subject.Subject,
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) credentials(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listCredentials(w, r)
	case http.MethodPost:
		h.createCredential(w, r)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{
			Code:      "invalid_request",
			Message:   "The request method is not allowed.",
			RequestID: requestIDFromContext(r.Context()),
		})
	}
}

func (h *handler) listCredentials(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	auth, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	if h.identity == nil {
		h.writeError(w, requestID, identityUnavailable())
		return
	}
	items, err := h.identity.ListCredentials(r.Context(), auth)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	responses := make([]credentialResponse, len(items))
	for i, item := range items {
		responses[i] = credentialResponseFromDomain(item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": responses})
}

func (h *handler) createCredential(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	auth, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	if h.identity == nil {
		h.writeError(w, requestID, identityUnavailable())
		return
	}
	key, err := oneHeader(r, "Idempotency-Key")
	if err != nil {
		h.writeError(w, requestID, invalidIdentityHeader(err))
		return
	}
	var input services.CredentialCreateInput
	if err := decodeBoundedStrictJSON(w, r, &input, 32<<10); err != nil {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeInvalidRequest, Message: err.Error()})
		return
	}
	result, err := h.identity.CreateMachineCredential(r.Context(), auth, key, input)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, credentialIssuanceResponseFromService(result))
}

func (h *handler) getCredential(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	auth, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	if h.identity == nil {
		h.writeError(w, requestID, identityUnavailable())
		return
	}
	credential, err := h.identity.GetCredential(r.Context(), auth, r.PathValue("credential_id"))
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	writeJSON(w, http.StatusOK, credentialResponseFromDomain(credential))
}

func (h *handler) rotateCredential(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	auth, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	if h.identity == nil {
		h.writeError(w, requestID, identityUnavailable())
		return
	}
	key, err := oneHeader(r, "Idempotency-Key")
	if err != nil {
		h.writeError(w, requestID, invalidIdentityHeader(err))
		return
	}
	var body struct {
		OverlapSeconds int64 `json:"overlap_seconds"`
	}
	if err := decodeBoundedStrictJSON(w, r, &body, 4<<10); err != nil {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeInvalidRequest, Message: err.Error()})
		return
	}
	if body.OverlapSeconds < 0 || body.OverlapSeconds > int64((24*time.Hour)/time.Second) {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeInvalidRequest, Message: "overlap_seconds must be between zero and 86400"})
		return
	}
	result, err := h.identity.RotateMachineCredential(
		r.Context(), auth, r.PathValue("credential_id"), key, time.Duration(body.OverlapSeconds)*time.Second,
	)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, credentialIssuanceResponseFromService(result))
}

func (h *handler) revokeCredential(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	auth, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	if h.identity == nil {
		h.writeError(w, requestID, identityUnavailable())
		return
	}
	if r.ContentLength > 0 {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeInvalidRequest, Message: "The revocation request body must be empty."})
		return
	}
	credential, err := h.identity.RevokeCredential(r.Context(), auth, r.PathValue("credential_id"))
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	writeJSON(w, http.StatusOK, credentialResponseFromDomain(credential))
}

func (h *handler) startDeviceAuthorization(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	if !h.deviceCodeLimiter.Allow(clientAddress(r, h.trustForwardedClientIP), time.Now()) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Code: "rate_limited", Message: "Too many device authorization requests.", RequestID: requestID})
		return
	}
	if h.identity == nil {
		h.writeError(w, requestID, identityUnavailable())
		return
	}
	var input services.DeviceCodeInput
	if err := decodeBoundedStrictJSON(w, r, &input, 16<<10); err != nil {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeInvalidRequest, Message: err.Error()})
		return
	}
	result, err := h.identity.StartDeviceAuthorization(r.Context(), input)
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	if strings.HasPrefix(result.VerificationURI, "/") {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		result.VerificationURI = scheme + "://" + r.Host + result.VerificationURI
		result.VerificationURIComplete = result.VerificationURI + "?user_code=" + result.UserCode
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) pollDeviceAuthorization(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	if h.identity == nil {
		h.writeError(w, requestID, identityUnavailable())
		return
	}
	if err := r.ParseForm(); err != nil {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeInvalidRequest, Message: "The device token request is invalid."})
		return
	}
	grantType := r.Form.Get("grant_type")
	if grantType != "urn:ietf:params:oauth:grant-type:device_code" {
		writeDeviceError(w, services.DeviceUnsupportedGrantType)
		return
	}
	result, err := h.identity.PollDeviceAuthorization(r.Context(), r.Form.Get("device_code"))
	var flow *services.DeviceFlowError
	if errors.As(err, &flow) {
		writeDeviceError(w, flow.Code)
		return
	}
	if err != nil {
		h.writeError(w, requestID, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": result.Secret, "token_type": "Bearer",
		"credential_id": result.Credential.ID, "account_id": result.Credential.AccountID,
	})
}

func writeDeviceError(w http.ResponseWriter, code services.DeviceTokenError) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": code})
}

func (h *handler) createBootstrapSession(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	if !h.bootstrapLimiter.Allow(clientAddress(r, h.trustForwardedClientIP), time.Now()) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Code: "rate_limited", Message: "Too many bootstrap authentication attempts.", RequestID: requestID})
		return
	}
	if h.identity == nil {
		h.writeError(w, requestID, identityUnavailable())
		return
	}
	var secret, userCode string
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err == nil {
			secret = r.Form.Get("bootstrap_secret")
			userCode = r.Form.Get("user_code")
		}
	} else {
		var input struct {
			BootstrapSecret string `json:"bootstrap_secret"`
		}
		if err := decodeBoundedStrictJSON(w, r, &input, 8<<10); err != nil {
			h.writeError(w, requestID, &services.PublicError{Code: services.CodeInvalidRequest, Message: err.Error()})
			return
		}
		secret = input.BootstrapSecret
	}
	session, err := h.identity.CreateBootstrapBrowserSession(r.Context(), secret)
	if err != nil {
		h.writeError(w, requestID, &authenticationError{cause: err})
		return
	}
	maxAge := int(time.Until(session.ExpiresAt).Seconds())
	secureCookie := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     bootstrapSessionCookie,
		Value:    session.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookie,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     bootstrapCSRFCookie,
		Value:    session.CSRFToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookie,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
	w.Header().Set("Cache-Control", "no-store")
	if userCode != "" {
		http.Redirect(w, r, "/auth/device?user_code="+url.QueryEscape(userCode), http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"csrf_token": session.CSRFToken, "expires_at": session.ExpiresAt})
}

func (h *handler) deviceApprovalPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	userCode := r.URL.Query().Get("user_code")
	if userCode == "" {
		renderDevicePage(w, devicePageData{Login: true, Error: "Enter the code shown by the nvoken CLI."})
		return
	}
	sessionCookie, err := r.Cookie(bootstrapSessionCookie)
	if err != nil {
		renderDevicePage(w, devicePageData{Login: true, UserCode: userCode})
		return
	}
	session, err := h.identity.ResolveBrowserSession(r.Context(), sessionCookie.Value, "")
	if err != nil {
		renderDevicePage(w, devicePageData{Login: true, UserCode: userCode})
		return
	}
	view, err := h.identity.DeviceApproval(r.Context(), userCode, session)
	if err != nil {
		renderDevicePage(w, devicePageData{Error: "This device code is invalid or expired."})
		return
	}
	csrfCookie, _ := r.Cookie(bootstrapCSRFCookie)
	csrf := ""
	if csrfCookie != nil {
		csrf = csrfCookie.Value
	}
	renderDevicePage(w, devicePageData{
		UserCode:    userCode,
		CSRF:        csrf,
		AccountID:   view.AccountID,
		Subject:     view.Approver.Subject,
		DeviceLabel: view.DeviceLabel,
		RoleCap:     string(view.RoleCap),
		TenantKey:   view.TenantConstraint,
		SessionID:   view.SessionConstraint,
	})
}

func (h *handler) confirmDeviceAuthorization(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFromContext(r.Context())
	if !h.confirmationLimiter.Allow(clientAddress(r, h.trustForwardedClientIP), time.Now()) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Code: "rate_limited", Message: "Too many device confirmation attempts.", RequestID: requestID})
		return
	}
	if h.identity == nil {
		h.writeError(w, requestID, identityUnavailable())
		return
	}
	if err := r.ParseForm(); err != nil {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeInvalidRequest, Message: "The confirmation request is invalid."})
		return
	}
	if r.Form.Get("csrf_token") == "" {
		h.writeError(w, requestID, &authenticationError{})
		return
	}
	sessionCookie, err := r.Cookie(bootstrapSessionCookie)
	if err != nil {
		h.writeError(w, requestID, &authenticationError{})
		return
	}
	session, err := h.identity.ResolveBrowserSession(r.Context(), sessionCookie.Value, r.Form.Get("csrf_token"))
	if err != nil {
		h.writeError(w, requestID, &authenticationError{})
		return
	}
	decision := r.Form.Get("decision")
	if decision != "approve" && decision != "deny" {
		h.writeError(w, requestID, &services.PublicError{Code: services.CodeInvalidRequest, Message: "decision must be approve or deny"})
		return
	}
	if err := h.identity.ConfirmDeviceAuthorization(r.Context(), r.Form.Get("user_code"), decision == "approve", session); err != nil {
		h.writeError(w, requestID, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if strings.Contains(r.Header.Get("Accept"), "text/html") || strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		renderDevicePage(w, devicePageData{Complete: true})
		return
	}
	status := "denied"
	if decision == "approve" {
		status = "approved"
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status})
}

type devicePageData struct {
	Login       bool
	Complete    bool
	Error       string
	UserCode    string
	CSRF        string
	AccountID   string
	Subject     string
	DeviceLabel string
	RoleCap     string
	TenantKey   *string
	SessionID   *string
}

var devicePage = template.Must(template.New("device").Parse(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Authorize nvoken CLI</title><style>body{font:16px system-ui;max-width:36rem;margin:10vh auto;padding:0 1.5rem;color:#171717}main{border:1px solid #ddd;border-radius:14px;padding:2rem}label{display:block;margin:1rem 0 .4rem}input,button{font:inherit;padding:.7rem;border-radius:8px;border:1px solid #aaa}input{width:100%;box-sizing:border-box}button{cursor:pointer}.approve{background:#171717;color:white}.row{display:flex;gap:.75rem;margin-top:1.5rem}dt{font-weight:600}dd{margin:0 0 .8rem}.error{color:#a00}</style></head><body><main>{{if .Complete}}<h1>Device authorization complete</h1><p>You can return to the nvoken CLI.</p>{{else if .Login}}<h1>Authorize nvoken CLI</h1>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}<form method="post" action="/v1/auth/bootstrap/session"><label>Device code</label><input name="user_code" value="{{.UserCode}}" required><label>Bootstrap Owner secret</label><input name="bootstrap_secret" type="password" required><button class="approve" type="submit">Continue</button></form>{{else if .Error}}<h1>Unable to authorize</h1><p class="error">{{.Error}}</p>{{else}}<h1>Approve this device?</h1><dl><dt>Account</dt><dd>{{.AccountID}}</dd><dt>Approving principal</dt><dd>{{.Subject}}</dd><dt>Device</dt><dd>{{.DeviceLabel}}</dd><dt>Permission cap</dt><dd>{{.RoleCap}}</dd>{{if .TenantKey}}<dt>Tenant</dt><dd>{{.TenantKey}}</dd>{{end}}{{if .SessionID}}<dt>Session</dt><dd>{{.SessionID}}</dd>{{end}}</dl><form method="post" action="/v1/auth/device/confirm"><input type="hidden" name="user_code" value="{{.UserCode}}"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><div class="row"><button class="approve" name="decision" value="approve">Approve</button><button name="decision" value="deny">Deny</button></div></form>{{end}}</main></body></html>`))

func renderDevicePage(w http.ResponseWriter, data devicePageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := devicePage.Execute(w, data); err != nil {
		http.Error(w, "render device approval", http.StatusInternalServerError)
	}
}

func oneHeader(r *http.Request, name string) (string, error) {
	values := r.Header.Values(name)
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", fmt.Errorf("%s must be provided exactly once", name)
	}
	return values[0], nil
}

func invalidIdentityHeader(err error) error {
	return &services.PublicError{Code: services.CodeInvalidRequest, Message: err.Error()}
}

func identityUnavailable() error {
	return &services.PublicError{Code: services.CodeUnavailable, Message: "The identity service is temporarily unavailable."}
}

type attemptWindow struct {
	started time.Time
	count   int
}

type attemptLimiter struct {
	mu        sync.Mutex
	limit     int
	duration  time.Duration
	windows   map[string]attemptWindow
	lastPrune time.Time
}

const (
	maximumAttemptLimiterWindows = 4096
	overflowAttemptLimiterKey    = "<overflow>"
)

func newAttemptLimiter(limit int, duration time.Duration) *attemptLimiter {
	return &attemptLimiter{
		limit:    limit,
		duration: duration,
		windows:  map[string]attemptWindow{},
	}
}

func (l *attemptLimiter) Allow(key string, now time.Time) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lastPrune.IsZero() || now.Sub(l.lastPrune) >= l.duration {
		for existingKey, existingWindow := range l.windows {
			if !now.Before(existingWindow.started.Add(l.duration)) {
				delete(l.windows, existingKey)
			}
		}
		l.lastPrune = now
	}
	if key == "" {
		key = "<unknown>"
	}
	if _, exists := l.windows[key]; !exists && len(l.windows) >= maximumAttemptLimiterWindows-1 {
		key = overflowAttemptLimiterKey
	}
	window := l.windows[key]
	if window.started.IsZero() || now.Before(window.started) || now.Sub(window.started) >= l.duration {
		window = attemptWindow{started: now}
	}
	window.count++
	l.windows[key] = window
	return window.count <= l.limit
}

func clientAddress(r *http.Request, trustForwarded bool) string {
	if trustForwarded {
		for _, header := range r.Header.Values("X-Forwarded-For") {
			for address := range strings.SplitSeq(header, ",") {
				if ip := net.ParseIP(strings.TrimSpace(address)); ip != nil {
					return ip.String()
				}
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
		return host
	}
	return r.RemoteAddr
}
