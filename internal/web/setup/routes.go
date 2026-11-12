package setup

import "net/http"

// Register registers the wizard. Everything is SuperAdmin only.
func Register(mux *http.ServeMux, s *Handlers, sa func(http.Handler) http.Handler) {
	mux.Handle("GET /admin/setup", sa(http.HandlerFunc(s.setupIndex)))
	mux.Handle("GET /admin/setup/live.json", sa(http.HandlerFunc(s.setupLive)))
	mux.Handle("POST /admin/setup/start", sa(http.HandlerFunc(s.setupStart)))
	mux.Handle("POST /admin/setup/dismiss", sa(http.HandlerFunc(s.setupDismiss)))
	mux.Handle("POST /admin/setup/finish", sa(http.HandlerFunc(s.setupFinish)))
	mux.Handle("GET /admin/setup/{step}", sa(http.HandlerFunc(s.setupShow)))
	mux.Handle("POST /admin/setup/{step}", sa(s.setupAct(wizSave)))
	mux.Handle("POST /admin/setup/{step}/test", sa(s.setupAct(wizTest)))
	mux.Handle("POST /admin/setup/{step}/skip", sa(http.HandlerFunc(s.setupSkip)))
}
