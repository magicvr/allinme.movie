package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"gorm.io/gorm"
	"my-movie-site/models"
)

// Handler holds dependencies for API HTTP handlers.
type Handler struct {
	DB *gorm.DB
}

// moviesResponse is the paginated response for GET /api/movies.
type moviesResponse struct {
	Total int64          `json:"total"`
	Page  int            `json:"page"`
	Size  int            `json:"size"`
	Items []models.Movie `json:"items"`
}

// movieDetail embeds Movie and adds VideoSources.
type movieDetail struct {
	models.Movie
	VideoSources []models.VideoSource `json:"video_sources"`
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var movies []models.Movie
	offset := (page - 1) * size
	if err := db.Offset(offset).Limit(size).Find(&movies).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, "q is required", http.StatusBadRequest)
		return
	}

	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)

	var movies []models.Movie
	if err := h.DB.Where("title LIKE ? ESCAPE '\\'", "%"+escaped+"%").Find(&movies).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var movie models.Movie
	if err := h.DB.First(&movie, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var sources []models.VideoSource
	if err := h.DB.Where("movie_id = ?", id).Find(&sources).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, movieDetail{Movie: movie, VideoSources: sources})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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
