package web

import (
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"

	"gorm.io/gorm"
	"my-movie-site/models"
)

// sqlEscaper escapes LIKE special characters for safe SQL fuzzy matching.
var sqlEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Handler serves HTML pages using html/template.
type Handler struct {
	DB   *gorm.DB
	Tmpl *template.Template
}

// pageData is passed to every template render.
type pageData struct {
	Query  string
	Movies []models.Movie
}

// detailPageData is passed to the detail template.
type detailPageData struct {
	Movie   models.Movie
	Sources []models.VideoSource
}

// Index handles GET / – shows the most recently updated movies.
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	var movies []models.Movie
	if err := h.DB.Order("update_time desc").Limit(60).Find(&movies).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, pageData{Movies: movies})
}

// Search handles GET /search?q=xxx – shows fuzzy-matched movies.
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	escaped := sqlEscaper.Replace(q)

	var movies []models.Movie
	if err := h.DB.Where("title LIKE ? ESCAPE '\\'", "%"+escaped+"%").
		Order("update_time desc").Find(&movies).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, pageData{Query: q, Movies: movies})
}

func (h *Handler) render(w http.ResponseWriter, data pageData) {
	h.renderTemplate(w, "index.html", data)
}

func (h *Handler) renderTemplate(w http.ResponseWriter, name string, data any) {
	var buf strings.Builder
	if err := h.Tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("template execute error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(buf.String()))
}

// Detail handles GET /movie/{id} – shows movie metadata and multi-source player.
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var movie models.Movie
	if err := h.DB.Preload("VideoSources").First(&movie, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		log.Printf("db error fetching movie %d: %v", id, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.renderTemplate(w, "detail.html", detailPageData{Movie: movie, Sources: movie.VideoSources})
}
