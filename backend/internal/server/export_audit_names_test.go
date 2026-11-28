package server

import "github.com/publiciallc/go-help-desk/backend/internal/domain/category"

// SetCategoriesForTest swaps the category service the handlers call, so a test
// can put a store that counts its statements in front of the real one.
func (s *Server) SetCategoriesForTest(c *category.Service) { s.categories = c }
