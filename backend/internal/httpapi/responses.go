package httpapi

import (
	"encoding/json"
	"net/http"
)

// errorBody is the uniform JSON error envelope returned by the API.
type errorBody struct {
	Error string `json:"error"`
}

// writeJSON serializes body as JSON with the given status code.
func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError returns a JSON error with the given status code. The message is
// operator-facing and must never contain secrets or internal detail.
func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, errorBody{Error: message})
}

// internalError logs the underlying cause and returns a generic 500 so error
// detail (which may reference credentials or infrastructure) never leaks to the
// client.
func (s *Server) internalError(w http.ResponseWriter, context string, err error) {
	s.logger.Error(context, "error", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
