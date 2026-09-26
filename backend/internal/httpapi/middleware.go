package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/mugiwaraluffy56/knull/backend/internal/auth"
	"github.com/mugiwaraluffy56/knull/backend/internal/operators"
)

type contextKey string

const operatorContextKey contextKey = "operator"

// requireOperator rejects any request that does not carry a valid operator
// session. Enforcement is entirely server-side: the cookie is opaque and the
// session is resolved against Redis, so a client cannot forge authorization.
func (s *Server) requireOperator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.sessions == nil || s.operators == nil {
			writeError(w, http.StatusServiceUnavailable, "authentication is not configured")
			return
		}

		cookie, err := r.Cookie(auth.SessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		operatorID, err := s.sessions.Resolve(r.Context(), cookie.Value)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		op, err := s.operators.Get(r.Context(), operatorID)
		if err != nil {
			if errors.Is(err, operators.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			s.logger.Error("resolve operator", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		ctx := context.WithValue(r.Context(), operatorContextKey, op)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// operatorFrom returns the operator attached by requireOperator.
func operatorFrom(ctx context.Context) (operators.Operator, bool) {
	op, ok := ctx.Value(operatorContextKey).(operators.Operator)
	return op, ok
}
