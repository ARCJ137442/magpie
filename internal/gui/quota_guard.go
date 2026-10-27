package gui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/quotaguard"
)

// quotaGuardRoutes belongs to the authenticated GUI/admin handler, not to
// /v1 or a caller's Gateway Key. No request includes or returns a raw API key.
func quotaGuardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/providers/quota-guard", func(w http.ResponseWriter, r *http.Request) {
		p, err := provider.GuardCredential(r.URL.Query().Get("provider"), r.URL.Query().Get("key"))
		if err != nil {
			guardAPIError(w, 404, "credential_missing")
			return
		}
		v, err := provider.QuotaGuardOf(p)
		if err != nil {
			guardAPIError(w, 503, "guard_state_unavailable")
			return
		}
		writeJSON(w, v)
	})
	mux.HandleFunc("POST /api/providers/quota-guard", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Provider string             `json:"provider"`
			Key      string             `json:"key"`
			Action   string             `json:"action"`
			Revision uint64             `json:"revision"`
			Policy   *quotaguard.Policy `json:"policy,omitempty"`
			Until    *time.Time         `json:"until,omitempty"`
			Confirm  bool               `json:"confirm,omitempty"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		dec.DisallowUnknownFields()
		if dec.Decode(&req) != nil || dec.Decode(new(any)) != io.EOF {
			guardAPIError(w, 400, "invalid_request")
			return
		}
		p, err := provider.GuardCredential(req.Provider, req.Key)
		if err != nil {
			guardAPIError(w, 404, "credential_missing")
			return
		}
		var result any
		switch req.Action {
		case "refresh":
			if req.Policy != nil {
				// Preview only needs source mapping, not a finished rule form.
				req.Policy.Enabled = false
				req.Policy.Rules = nil
				if err = req.Policy.Validate(); err != nil {
					guardAPIError(w, 400, "invalid_policy")
					return
				}
			}
			snapshots, readErr := provider.RefreshQuotaGuard(r.Context(), p, req.Policy)
			view, stateErr := provider.QuotaGuardOf(p)
			if stateErr != nil {
				guardAPIError(w, 503, "guard_state_unavailable")
				return
			}
			// A partial read remains visible and explicitly incomplete; raw
			// vendor errors may contain a URL/key and never reach this API.
			result = struct {
				View       provider.QuotaGuardView `json:"view"`
				Snapshots  []quotaguard.Snapshot   `json:"snapshots"`
				Incomplete bool                    `json:"incomplete"`
			}{view, snapshots, readErr != nil}
		case "configure":
			if req.Policy == nil {
				guardAPIError(w, 400, "invalid_policy")
				return
			}
			result, err = provider.ConfigureQuotaGuard(p, *req.Policy, req.Revision)
		case "override", "revoke", "rearm":
			if !req.Confirm {
				guardAPIError(w, 400, "human_confirmation_required")
				return
			}
			result, err = provider.ActQuotaGuard(p, req.Action, req.Until, req.Revision)
		default:
			guardAPIError(w, 400, "unknown_action")
			return
		}
		if errors.Is(err, quotaguard.ErrConflict) {
			guardAPIError(w, 409, "revision_conflict")
			return
		}
		if err != nil {
			guardAPIError(w, 400, "guard_action_refused")
			return
		}
		writeJSON(w, result)
	})
}

func guardAPIError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": code, "code": code})
}
