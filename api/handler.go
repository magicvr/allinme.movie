package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"my-movie-site/models"

	"gorm.io/gorm"
)

// Handler holds dependencies for API HTTP handlers.
type Handler struct {
	DB *gorm.DB
}

// GetParseInterface handles GET /api/parse-interface and returns {url: "..."}
func (h *Handler) GetParseInterface(w http.ResponseWriter, r *http.Request) {
	var s models.Setting
	pi := ""
	if err := h.DB.First(&s, "key = ?", "parse_interface").Error; err == nil {
		pi = s.Value
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": pi})
}

// moviesResponse is the paginated response for GET /api/movies.
type moviesResponse struct {
	Total int64          `json:"total"`
	Page  int            `json:"page"`
	Size  int            `json:"size"`
	Items []models.Movie `json:"items"`
}

// movieDetail embeds Movie for the detail endpoint response.
type movieDetail struct {
	models.Movie
}

// categoryMapUpdate is used in the bulk-update request body.
type categoryMapUpdate struct {
	ID              uint `json:"id"`
	LocalCategoryID uint `json:"local_category_id"`
}

// ListMovies handles GET /api/movies.
// Query params: page (default 1), size (default 20), class, area, year.
func (h *Handler) ListMovies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page := parseIntDefault(q.Get("page"), 1)
	size := parseIntDefault(q.Get("size"), 20)
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 1
	}

	db := h.DB.Model(&models.Movie{})
	if v := q.Get("class"); v != "" {
		db = db.Where("class = ?", v)
	}
	if v := q.Get("area"); v != "" {
		db = db.Where("area = ?", v)
	}
	if v := q.Get("year"); v != "" {
		db = db.Where("year = ?", v)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var movies []models.Movie
	offset := (page - 1) * size
	if err := db.Offset(offset).Limit(size).Find(&movies).Error; err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, moviesResponse{
		Total: total,
		Page:  page,
		Size:  size,
		Items: movies,
	})
}

// SearchMovies handles GET /api/search?q=xxx.
// Performs a fuzzy match on the Title field.
func (h *Handler) SearchMovies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeJSONError(w, "q is required", http.StatusBadRequest)
		return
	}

	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)

	var movies []models.Movie
	if err := h.DB.Where("title LIKE ? ESCAPE '\\'", "%"+escaped+"%").Find(&movies).Error; err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, movies)
}

// GetMovie handles GET /api/movie/{id}.
// Returns the movie with all its VideoSource records.
func (h *Handler) GetMovie(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		writeJSONError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var movie models.Movie
	if err := h.DB.Preload("VideoSources").First(&movie, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			writeJSONError(w, "not found", http.StatusNotFound)
			return
		}
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, movieDetail{Movie: movie})
}

// ListUnmappedCategories handles GET /api/admin/category-maps/unmapped.
// Returns all CategoryMap entries where LocalCategoryID = 0 (to-be-bound).
func (h *Handler) ListUnmappedCategories(w http.ResponseWriter, r *http.Request) {
	var maps []models.CategoryMap
	if err := h.DB.Where("local_category_id = 0").Find(&maps).Error; err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, maps)
}

// UpdateCategoryMaps handles PUT /api/admin/category-maps.
// Accepts a JSON array of {id, local_category_id} objects and bulk-updates them.
func (h *Handler) UpdateCategoryMaps(w http.ResponseWriter, r *http.Request) {
	var updates []categoryMapUpdate
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeJSONError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	for _, u := range updates {
		if err := h.DB.Model(&models.CategoryMap{}).
			Where("id = ?", u.ID).
			Update("local_category_id", u.LocalCategoryID).Error; err != nil {
			writeJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListAllCategoryMaps handles GET /api/admin/category-maps.
// Returns all CategoryMap entries (both mapped and unmapped).
func (h *Handler) ListAllCategoryMaps(w http.ResponseWriter, r *http.Request) {
	var maps []models.CategoryMap
	if err := h.DB.Find(&maps).Error; err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, maps)
}

// ListCategories handles GET /api/categories.
// Returns all enabled top-level local categories with their enabled children
// pre-loaded, forming a two-level tree for the public nav bar.
func (h *Handler) ListCategories(w http.ResponseWriter, r *http.Request) {
	var cats []models.Category
	if err := h.DB.Where("enabled = ? AND parent_id = 0", true).
		Preload("Children", "enabled = ?", true).
		Find(&cats).Error; err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, cats)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeJSONError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
