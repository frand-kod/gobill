package web

import "github.com/frand-kod/gobill/internal/service/provision"

// prov is the provisioning service. It is built per call because SecretKey is set after New.
func (s *Server) prov() *provision.Service {
	return &provision.Service{DB: s.conn, Q: s.Queries, Key: s.SecretKey, Now: s.now}
}
