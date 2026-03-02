package web

import (
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"

	"my-movie-site/models"

	"gorm.io/gorm"
)

// sqlEscaper escapes LIKE special characters for safe SQL fuzzy matching.
var sqlEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Handler serves HTML pages using html/template.
type Handler struct {
	DB        *gorm.DB
	Tmpl      *template.Template
	SiteTitle string
}

// pageData is passed to every template render.
type pageData struct {
	Query        string
	Movies       []models.Movie
	Categories   []models.Category // top-level categories with Children preloaded
	ActiveCat    uint              // selected parent category ID (from ?cat=)
	ActiveSubCat uint              // selected sub-category ID (from ?sub_cat=)
	SiteTitle    string
}

// detailPageData is passed to the detail template.
type detailPageData struct {
	Movie     models.Movie
	Sources   []models.VideoSource
	SiteTitle string
}

// Index handles GET / – shows the most recently updated movies.
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	categories := h.loadCategoryTree()

	db := h.DB.Order("update_time desc").Limit(60)
	var activeCat, activeSubCat uint

	if subCatIDStr := r.URL.Query().Get("sub_cat"); subCatIDStr != "" {
		if subCatID, err := strconv.ParseUint(subCatIDStr, 10, 64); err == nil && subCatID > 0 {
			activeSubCat = uint(subCatID)
			db = db.Where("category_id = ?", subCatID)
		}
	}

	if catIDStr := r.URL.Query().Get("cat"); catIDStr != "" {
		if catID, err := strconv.ParseUint(catIDStr, 10, 64); err == nil && catID > 0 {
			activeCat = uint(catID)
			if activeSubCat == 0 {
				// Show all movies whose category is a direct child of this parent,
				// or the parent itself (covers categories with no sub-divisions).
				db = db.Where(
					"category_id IN (SELECT id FROM categories WHERE parent_id = ? AND enabled = 1) OR category_id = ?",
					catID, catID,
				)
			}
		}
	} else if catIDStr := r.URL.Query().Get("category_id"); catIDStr != "" {
		// Backward-compatible parameter kept for existing links.
		if catID, err := strconv.ParseUint(catIDStr, 10, 64); err == nil && catID > 0 {
			db = db.Where("category_id = ?", catID)
		}
	}

	var movies []models.Movie
	if err := db.Find(&movies).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, pageData{
		Movies:       movies,
		Categories:   categories,
		ActiveCat:    activeCat,
		ActiveSubCat: activeSubCat,
		SiteTitle:    h.SiteTitle,
	})
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

	h.render(w, pageData{Query: q, Movies: movies, Categories: h.loadCategoryTree(), SiteTitle: h.SiteTitle})
}

// loadCategoryTree returns all enabled top-level categories with their enabled
// children pre-loaded.  Errors are logged and an empty slice is returned so
// that callers can degrade gracefully.
func (h *Handler) loadCategoryTree() []models.Category {
	var categories []models.Category
	if err := h.DB.Where("enabled = ? AND parent_id = 0", true).
		Preload("Children", "enabled = ?", true).
		Find(&categories).Error; err != nil {
		log.Printf("web: load category tree: %v", err)
	}
	return categories
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

	h.renderTemplate(w, "detail.html", detailPageData{Movie: movie, Sources: movie.VideoSources, SiteTitle: h.SiteTitle})
}
