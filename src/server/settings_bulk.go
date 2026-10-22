package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/nkanaev/yarr/src/server/router"
)

func (s *Server) handleBulkSettings(c *router.Context) {
	if c.Req.Method != http.MethodPut {
		c.Out.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		FolderID *int64 `json:"folder_id"`
		Field    string `json:"field"`
		Value    *bool  `json:"value"`
	}
	decoder := json.NewDecoder(c.Req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.Value == nil ||
		(body.Field != "unread_first" && body.Field != "auto_read_scroll") ||
		(body.FolderID != nil && *body.FolderID <= 0) {
		c.Out.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := s.db.BulkUpdateReadingSetting(body.FolderID, body.Field, *body.Value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.Out.WriteHeader(http.StatusNotFound)
		} else {
			log.Print(err)
			c.Out.WriteHeader(http.StatusInternalServerError)
		}
		return
	}
	c.Out.WriteHeader(http.StatusNoContent)
}
