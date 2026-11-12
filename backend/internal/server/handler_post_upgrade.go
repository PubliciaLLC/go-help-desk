package server

import "net/http"

// GET /api/v1/admin/post-upgrade
//
// The one number the post-upgrade wizard cannot get from the settings dump:
// how many accounts are still on a password hash made below the current bcrypt
// cost and would have to reset their password when the grace period ends
// (#453). Administrators are exempt and not counted. Whether email is set up,
// which decides if those reset links can be mailed, is already in
// /admin/security-warnings.
func (s *Server) handlePostUpgrade(w http.ResponseWriter, r *http.Request) {
	n, err := s.users.AccountsOnOldPasswordHash(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]int{"old_password_accounts": n})
}
