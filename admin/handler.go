package admin

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"sync"

	"gorm.io/gorm"
	"my-movie-site/collector"
	"my-movie-site/models"
)

// Handler holds dependencies for admin HTTP handlers.
type Handler struct {
	DB           *gorm.DB
	CollectorURL string
	Tmpl         *template.Template

	mu      sync.Mutex
	syncing bool
}

// DeleteSource handles DELETE /admin/source/{key}.
// It removes all VideoSource records with the given SourceKey, then cascades to
// delete any Movie that has no remaining VideoSources.
func (h *Handler) DeleteSource(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}

	if err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("source_key = ?", key).Delete(&models.VideoSource{}).Error; err != nil {
			return err
		}
		return cleanOrphanMovies(tx)
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// replaceBaseRequest is the JSON body for ReplaceBase.
type replaceBaseRequest struct {
	SourceKey string `json:"source_key"`
	OldBase   string `json:"old_base"`
	NewBase   string `json:"new_base"`
}

// ReplaceBase handles PUT /admin/source/replace-base.
// It replaces the URL prefix old_base with new_base for all VideoSource records
// whose SourceKey matches source_key.
func (h *Handler) ReplaceBase(w http.ResponseWriter, r *http.Request) {
	var req replaceBaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.SourceKey == "" || req.OldBase == "" {
		http.Error(w, "source_key and old_base are required", http.StatusBadRequest)
		return
	}

	oldBaseLen := len(req.OldBase)
	result := h.DB.Model(&models.VideoSource{}).
		Where("source_key = ? AND SUBSTR(raw_url, 1, ?) = ?", req.SourceKey, oldBaseLen, req.OldBase).
		Update("raw_url", gorm.Expr("? || SUBSTR(raw_url, ?)", req.NewBase, oldBaseLen+1))
	if result.Error != nil {
		http.Error(w, result.Error.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// cleanOrphanMovies deletes any Movie that has no VideoSource records.
// This is the cascade-cleanup hook that runs after sources are removed.
func cleanOrphanMovies(db *gorm.DB) error {
	subQuery := db.Model(&models.VideoSource{}).Select("movie_id")
	return db.Where("id NOT IN (?)", subQuery).Delete(&models.Movie{}).Error
}

// Sync handles POST /admin/sync.
// It asynchronously triggers a full collection run.  A debounce guard prevents
// a second run from starting while one is already in progress; such requests
// receive 409 Conflict.
func (h *Handler) Sync(w http.ResponseWriter, r *http.Request) {
	if h.CollectorURL == "" {
		http.Error(w, "collector not configured", http.StatusServiceUnavailable)
		return
	}

	h.mu.Lock()
	if h.syncing {
		h.mu.Unlock()
		http.Error(w, "sync already in progress", http.StatusConflict)
		return
	}
	h.syncing = true
	h.mu.Unlock()

	go func() {
		defer func() {
			h.mu.Lock()
			h.syncing = false
			h.mu.Unlock()
		}()
		col := collector.New(h.CollectorURL, h.DB)
		if err := col.Run(); err != nil {
			log.Printf("admin: manual sync failed: %v", err)
		} else {
			log.Print("admin: manual sync completed")
		}
	}()

	w.WriteHeader(http.StatusAccepted)
}

// AdminPage handles GET /admin – serves the admin management page.
func (h *Handler) AdminPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.Tmpl.ExecuteTemplate(w, "admin.html", nil); err != nil {
		log.Printf("admin: template error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}
