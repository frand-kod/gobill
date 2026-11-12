package adminx

// Friendly error messages for router, plan and save failures.

import (
	"net/http"

	"strings"
)

// Plain-language messages (English catalog keys) for failures the operator can act on.
const (
	MsgRouterTimeout = "The router did not answer in time. Check that it is turned on, the IP address is right and no firewall blocks the API port."
	MsgRouterRefused = "The router refused the connection. On the MikroTik turn on the API service (port 8728) and check the port number here."
	MsgRouterHost    = "The router address was not found. Check the IP address or host name."
	MsgRouterLogin   = "The router did not accept the username or password. Check the API user on the MikroTik."
	MsgRouterOther   = "Could not reach the router. Check the IP address, port, username and password."
	MsgNASOther      = "The router did not confirm the disconnect. Check that it is online and that its CoA port and secret match this NAS."
	MsgPlanMissing   = "This plan was not found. Pick a plan from the list."
	MsgPlanOff       = "This plan is turned off. Turn it on under Service Plan first."
	MsgPlanBad       = "This plan cannot be used right now. Check the plan and its router under Service Plan."
	MsgNoBalance     = "The customer's balance is not enough. Add balance first, or choose Cash."
	MsgRechargeFail  = "The recharge could not be saved and nothing was charged. Try again; if it keeps happening, check Logs."
	MsgNotSynced     = "Recharge saved and the payment is recorded, but the router did not take the change. Check the router is online, then press Sync on this customer."
)

// RouterErr maps a raw connection error to one of the MsgRouter* messages, or other.
func RouterErr(err error, other string) string {
	e := strings.ToLower(err.Error())
	switch {
	case strings.Contains(e, "timeout") || strings.Contains(e, "timed out") || strings.Contains(e, "deadline"):
		return MsgRouterTimeout
	case strings.Contains(e, "refused"):
		return MsgRouterRefused
	case strings.Contains(e, "no such host") || strings.Contains(e, "lookup"):
		return MsgRouterHost
	case strings.Contains(e, "invalid user") || strings.Contains(e, "cannot log in") || strings.Contains(e, "password"):
		return MsgRouterLogin
	}
	return other
}

// PutFailure stores a plain error for the next page and keeps the raw text for technicians
// (shown in a small <details>). Raw errors come from dial/login failures and carry no secrets.
func (s *Base) PutFailure(r *http.Request, msg string, raw error) {
	s.Sessions.Put(r.Context(), "error", msg)
	if raw != nil {
		d := raw.Error()
		if len(d) > 300 {
			d = d[:300]
		}
		s.Sessions.Put(r.Context(), "detail", d)
	}
}

// PlanErrMsg explains why a plan id cannot be recharged: missing, switched off, or otherwise unusable.
func PlanErrMsg(found bool, enabled int64) string {
	switch {
	case !found:
		return MsgPlanMissing
	case enabled != 1:
		return MsgPlanOff
	}
	return MsgPlanBad
}
