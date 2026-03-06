package admin

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"golang.org/x/net/publicsuffix"

	"my-movie-site/collector"
	"my-movie-site/models"

	"gorm.io/gorm"
)

// Handler holds dependencies for admin HTTP handlers.
type Handler struct {
	DB           *gorm.DB
	CollectorURL string
	Tmpl         *template.Template
	SiteTitle    string

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

// replaceDomainRequest is the JSON body for ReplaceDomain.
type replaceDomainRequest struct {
	SourceKey string `json:"source_key,omitempty"`
	NewBase   string `json:"new_base"`
}

// replaceDomainResponse reports how many rows were examined and updated.
type replaceDomainResponse struct {
	Scanned int `json:"scanned"`
	Updated int `json:"updated"`
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

// ReplaceDomain handles PUT /admin/source/replace-domain.
// It replaces the base domain portion of stored VideoSource.raw_url while
// preserving any existing subdomain parts. If SourceKey is provided in the
// request, only VideoSource records with that source_key are updated; otherwise
// all records are processed.
func (h *Handler) ReplaceDomain(w http.ResponseWriter, r *http.Request) {
	var req replaceDomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.NewBase == "" {
		http.Error(w, "new_base is required", http.StatusBadRequest)
		return
	}

	// Validate new base using publicsuffix: it must be an effective TLD+1
	// (i.e. a registrable base domain like example.com or example.co.uk).
	e, err := publicsuffix.EffectiveTLDPlusOne(req.NewBase)
	if err != nil || e != req.NewBase {
		http.Error(w, "new_base must be a registrable base domain like example.com or example.co.uk", http.StatusBadRequest)
		return
	}

	var sources []models.VideoSource
	q := h.DB
	if req.SourceKey != "" {
		q = q.Where("source_key = ?", req.SourceKey)
	}
	if err := q.Find(&sources).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	scanned := 0
	updated := 0
	for _, s := range sources {
		scanned++
		raw := s.RawURL
		if raw == "" {
			continue
		}
		// try parsing as absolute URL; if fails, skip
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}
		host := u.Host
		port := ""
		if strings.Contains(host, ":") {
			parts := strings.Split(host, ":")
			host = parts[0]
			port = parts[1]
		}
		// Determine the original host's effective TLD+1 (registrable base)
		origBase, err := publicsuffix.EffectiveTLDPlusOne(host)
		if err != nil {
			// skip IPs, localhost, or otherwise unparsable domains
			continue
		}
		// Keep only the left-most single subdomain label (e.g. play.oldcdn.example.com -> keep "play")
		sub := ""
		if host != origBase {
			labels := strings.Split(host, ".")
			origLabels := strings.Split(origBase, ".")
			subCount := len(labels) - len(origLabels)
			if subCount > 0 {
				sub = strings.Join(labels[:subCount], ".")
			}
		}
		newHost := req.NewBase
		if sub != "" {
			newHost = sub + "." + req.NewBase
		}
		if port != "" {
			newHost = newHost + ":" + port
		}
		// If host unchanged, skip
		if newHost == u.Host {
			continue
		}
		u.Host = newHost

		newRaw := u.String()
		// update DB record
		if err := h.DB.Model(&models.VideoSource{}).Where("id = ?", s.ID).Update("raw_url", newRaw).Error; err != nil {
			log.Printf("admin: failed to update video source %d: %v", s.ID, err)
			continue
		}
		updated++
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(replaceDomainResponse{Scanned: scanned, Updated: updated})
}

// cleanOrphanMovies deletes any Movie that has no VideoSource records.
// This is the cascade-cleanup hook that runs after sources are removed.
func cleanOrphanMovies(db *gorm.DB) error {
	subQuery := db.Model(&models.VideoSource{}).Select("movie_id")
	return db.Where("id NOT IN (?)", subQuery).Delete(&models.Movie{}).Error
}

// Sync handles POST /admin/sync.
// If CollectionSources are configured in the database they are all run in
// sequence; otherwise falls back to the single CollectorURL (legacy mode).
// A debounce guard prevents a second run from starting while one is already
// in progress; such requests receive 409 Conflict.
func (h *Handler) Sync(w http.ResponseWriter, r *http.Request) {
	var dbSourceCount int64
	h.DB.Model(&models.CollectionSource{}).Where("enabled = ?", true).Count(&dbSourceCount)

	if dbSourceCount == 0 && h.CollectorURL == "" {
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
		defer func() {
			if r := recover(); r != nil {
				log.Printf("admin: sync recovered from panic: %v", r)
			}
		}()

		if dbSourceCount > 0 {
			if err := collector.RunAllFromDB(r.Context(), h.DB); err != nil {
				log.Printf("admin: multi-source sync failed: %v", err)
			} else {
				log.Print("admin: multi-source sync completed")
			}
			return
		}

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
	data := struct{ SiteTitle string }{SiteTitle: h.SiteTitle}
	if err := h.Tmpl.ExecuteTemplate(w, "admin.html", data); err != nil {
		log.Printf("admin: template error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// collectionSourceRequest is the JSON body for CreateCollectionSource.
type collectionSourceRequest struct {
	Name      string `json:"name"`
	APIURL    string `json:"api_url"`
	SourceKey string `json:"source_key"`
	Enabled   *bool  `json:"enabled"`
}

// ListCollectionSources handles GET /admin/collection-sources.
func (h *Handler) ListCollectionSources(w http.ResponseWriter, r *http.Request) {
	var sources []models.CollectionSource
	if err := h.DB.Find(&sources).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(sources); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// CreateCollectionSource handles POST /admin/collection-sources.
func (h *Handler) CreateCollectionSource(w http.ResponseWriter, r *http.Request) {
	var req collectionSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.APIURL == "" || req.SourceKey == "" {
		http.Error(w, "name, api_url, and source_key are required", http.StatusBadRequest)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	src := models.CollectionSource{
		Name:      req.Name,
		APIURL:    req.APIURL,
		SourceKey: req.SourceKey,
		Enabled:   &enabled,
	}
	if err := h.DB.Create(&src).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(src)
}

// CategoryMappingPage handles GET /admin/category-mapping.
// Serves the category mapping management page.
func (h *Handler) CategoryMappingPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := struct{ SiteTitle string }{SiteTitle: h.SiteTitle}
	if err := h.Tmpl.ExecuteTemplate(w, "category_mapping.html", data); err != nil {
		log.Printf("admin: template error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// categoryRequest is the JSON body for CreateCategory.
type categoryRequest struct {
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled"`
}

// ListCategories handles GET /admin/categories.
// Returns all local categories as JSON.
func (h *Handler) ListCategories(w http.ResponseWriter, r *http.Request) {
	var cats []models.Category
	if err := h.DB.Find(&cats).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cats)
}

// CreateCategory handles POST /admin/categories.
// Creates a new local category.
func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	var req categoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	cat := models.Category{Name: req.Name, Enabled: enabled}
	if err := h.DB.Create(&cat).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(cat)
}

// DeleteCollectionSource handles DELETE /admin/collection-sources/{id}.
func (h *Handler) DeleteCollectionSource(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if err := h.DB.Delete(&models.CollectionSource{}, idStr).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
