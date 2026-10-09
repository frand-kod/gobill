package web

// Customer inbox.

import (
	"database/sql"
	"net/http"

	"github.com/frand-kod/gobill/internal/db"
)

// ---- customer inbox (old mail.php) ----

type inboxData struct {
	List []db.CustomersInbox
	View *db.CustomersInbox
}

func (s *Server) pInbox(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListInboxByCustomer(r.Context(), db.ListInboxByCustomerParams{CustomerID: customerFrom(r).ID, Limit: 100})
	if err != nil {
		s.fail(w, "portal inbox", err)
		return
	}
	s.prender(w, r, 200, "p_inbox", Page{Title: "Inbox", Data: inboxData{List: rows}})
}

// pInboxView shows one message of this customer and marks it read.
func (s *Server) pInboxView(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r)
	m, err := s.queries.GetInboxMessage(r.Context(), db.GetInboxMessageParams{ID: pathID(r), CustomerID: c.ID})
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "portal inbox view", err)
		return
	}
	if err := s.queries.MarkInboxRead(r.Context(), db.MarkInboxReadParams{ID: m.ID, CustomerID: c.ID}); err != nil {
		s.fail(w, "portal inbox read", err)
		return
	}
	s.prender(w, r, 200, "p_inbox", Page{Title: "Inbox", Data: inboxData{View: &m}})
}
