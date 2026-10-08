package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/secret"
)

func nasFields(v, e map[string]string, editing bool) []field {
	pw := text("secret", "Shared Secret", v, e).as("password").req()
	if editing {
		pw.Required = false
		pw.Hint = "Leave empty to keep the current secret"
	}
	pw.Value = "" // never rendered back
	ma := text("require_message_auth", "Require Message-Authenticator", v, e).as("checkbox")
	ma.Checked = v["require_message_auth"] == "1"
	ma.Hint = "Drop Access-Requests without Message-Authenticator (RouterOS: require-message-auth)"
	out := section([]field{
		text("name", "NAS Name", v, e).req(),
		text("ip", "IP / CIDR", v, e).req().hint("e.g. 10.0.0.1 or 10.0.0.0/24"),
		text("description", "Description", v, e), ma,
	}, "NAS", "")
	return append(out, section([]field{pw}, "RADIUS secret", "")...)
}

// validNASAddr accepts a single IP or a CIDR prefix.
func validNASAddr(s string) bool {
	if _, err := netip.ParsePrefix(s); err == nil {
		return true
	}
	_, err := netip.ParseAddr(s)
	return err == nil
}

func (s *Server) nasList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListNAS(r.Context())
	if err != nil {
		s.fail(w, "list nas", err)
		return
	}
	lp := listPage{Heading: "NAS", Base: "/admin/nas", CanCreate: true, CanEdit: true,
		Cols: []string{"Name", "IP / CIDR", "Description"}}
	for _, n := range rows {
		lp.Rows = append(lp.Rows, listRow{n.ID, []string{n.Name, n.Ip, n.Description}})
	}
	s.renderList(w, r, lp)
}

func (s *Server) nasNew(w http.ResponseWriter, r *http.Request) {
	s.renderForm(w, r, 200, formPage{"Add NAS", "/admin/nas", "/admin/nas", nasFields(nil, nil, false)})
}

func (s *Server) nasEdit(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	n, err := s.queries.GetNAS(r.Context(), id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get nas", err)
		return
	}
	v := map[string]string{"name": n.Name, "ip": n.Ip, "description": n.Description, "require_message_auth": fmt.Sprint(n.RequireMessageAuth)}
	s.renderForm(w, r, 200, formPage{"Edit NAS", fmt.Sprint("/admin/nas/", id), "/admin/nas", nasFields(v, nil, true)})
}

func (s *Server) nasSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	v := formVals(r, "name", "ip", "description", "require_message_auth")
	pass := r.PostFormValue("secret")
	e := map[string]string{}
	for _, k := range []string{"name", "ip"} {
		if v[k] == "" {
			e[k] = "This field is required"
		}
	}
	if v["ip"] != "" && !validNASAddr(v["ip"]) {
		e["ip"] = "Enter a valid IP address or CIDR"
	}
	var enc []byte
	if id != 0 {
		n, err := s.queries.GetNAS(r.Context(), id)
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		} else if err != nil {
			s.fail(w, "get nas", err)
			return
		}
		enc = n.SecretEnc
	} else if pass == "" {
		e["secret"] = "This field is required"
	}
	if len(e) == 0 {
		if pass != "" {
			var err error
			if enc, err = secret.Seal(s.SecretKey, []byte(pass)); err != nil {
				s.fail(w, "seal nas secret", err)
				return
			}
		}
		var err error
		var rma int64
		if v["require_message_auth"] == "1" {
			rma = 1
		}
		if id == 0 {
			_, err = s.queries.CreateNAS(r.Context(), db.CreateNASParams{Name: v["name"], Ip: v["ip"], SecretEnc: enc, Description: v["description"], RequireMessageAuth: rma})
		} else {
			err = s.queries.UpdateNAS(r.Context(), db.UpdateNASParams{Name: v["name"], Ip: v["ip"], SecretEnc: enc, Description: v["description"], RequireMessageAuth: rma, ID: id})
		}
		switch {
		case isUnique(err):
			if strings.Contains(err.Error(), "nas.ip") {
				e["ip"] = "IP already exists"
			} else {
				e["name"] = "Name already exists"
			}
		case err != nil:
			s.fail(w, "save nas", err)
			return
		default:
			act, msg := "nas.create", "Data Created Successfully"
			if id != 0 {
				act, msg = "nas.update", "Data Updated Successfully"
			}
			s.done(w, r, "/admin/nas", msg, act, v["name"])
			return
		}
	}
	action, head := "/admin/nas", "Add NAS"
	if id != 0 {
		action, head = fmt.Sprint("/admin/nas/", id), "Edit NAS"
	}
	s.renderForm(w, r, http.StatusUnprocessableEntity, formPage{head, action, "/admin/nas", nasFields(v, e, id != 0)})
}

func (s *Server) nasDelete(w http.ResponseWriter, r *http.Request) {
	n, _ := s.queries.GetNAS(r.Context(), pathID(r))
	s.remove(w, r, "/admin/nas", "nas", "NAS is in use", n.Name, func(id int64) error {
		return s.queries.DeleteNAS(r.Context(), id)
	})
}
