package web

// Tell-a-friend invitation form.

import (
	"net/http"

	"errors"
	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"strings"
)

type friendData struct {
	Plan     db.Plan
	Username string
}

func (s *Server) friendPage(w http.ResponseWriter, r *http.Request, code int, username, errMsg string) {
	p, err := s.queries.GetPlan(r.Context(), pathID(r))
	if err != nil || p.Enabled != 1 || p.Type == "Balance" || p.Billing != "prepaid" {
		http.NotFound(w, r)
		return
	}
	s.prender(w, r, code, "p_friend", Page{Title: "Buy for friend", Error: errMsg, Data: friendData{p, username}})
}

func (s *Server) pFriendForm(w http.ResponseWriter, r *http.Request) {
	s.friendPage(w, r, 200, strings.TrimSpace(r.URL.Query().Get("u")), "")
}

func (s *Server) pFriend(w http.ResponseWriter, r *http.Request) {
	if s.Billing == nil {
		s.fail(w, "portal friend", errors.New("billing not configured"))
		return
	}
	user := strings.TrimSpace(r.PostFormValue("username"))
	err := s.Billing.SendPlan(r.Context(), customerFrom(r).ID, user, pathID(r))
	for _, e := range []error{billing.ErrSelfTransfer, billing.ErrTargetNotFound, billing.ErrInsufficientBalance, billing.ErrInactive,
		billing.ErrTransferDisabled, billing.ErrPlanNotFound, billing.ErrFriendPlanDiffers} {
		if errors.Is(err, e) {
			s.friendPage(w, r, 200, user, s.catalog.T(s.language(), e.Error()))
			return
		}
	}
	if err != nil {
		s.fail(w, "portal friend", err)
		return
	}
	s.flashTo(w, r, "/portal/activation", "Success to send package")
}
