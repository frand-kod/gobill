package web

import (
	"io/fs"
	"net/http"

	gobill "github.com/frand-kod/gobill"
)

// licenses serves the license texts the binary carries as plain text. It sits outside the session
// and maintenance chain, next to /health, so it always answers.
func licenses(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, name := range []string{"NOTICE", "LICENSE", "THIRD_PARTY_LICENSES.txt"} {
		b, err := fs.ReadFile(gobill.Legal, name)
		if err != nil {
			http.Error(w, "license text missing", http.StatusInternalServerError)
			return
		}
		w.Write(b)
		w.Write([]byte("\n"))
	}
}
