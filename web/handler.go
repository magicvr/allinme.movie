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
	Page         int
	TotalPages   int
	TotalCount   int
	PerPage      int
	PageNumbers  []int
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

	// pagination
	perPage := 24
	// support per_page query parameter (clamped)
	if pp := r.URL.Query().Get("per_page"); pp != "" {
		if n, err := strconv.Atoi(pp); err == nil && n > 0 {
			if n > 200 {
				n = 200
			}
			perPage = n
		}
	}

	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if pi, err := strconv.Atoi(p); err == nil && pi > 0 {
			page = pi
		}
	}

	var activeCat, activeSubCat uint

	// build a filtered query for counting and fetching
	filtered := h.DB.Model(&models.Movie{})

	if subCatIDStr := r.URL.Query().Get("sub_cat"); subCatIDStr != "" {
		if subCatID, err := strconv.ParseUint(subCatIDStr, 10, 64); err == nil && subCatID > 0 {
			activeSubCat = uint(subCatID)
			filtered = filtered.Where("category_id = ?", subCatID)
		}
	}

	if catIDStr := r.URL.Query().Get("cat"); catIDStr != "" {
		if catID, err := strconv.ParseUint(catIDStr, 10, 64); err == nil && catID > 0 {
			activeCat = uint(catID)
			if activeSubCat == 0 {
				// Show all movies whose category is a direct child of this parent,
				// or the parent itself (covers categories with no sub-divisions).
				filtered = filtered.Where(
					"category_id IN (SELECT id FROM categories WHERE parent_id = ? AND enabled = 1) OR category_id = ?",
					catID, catID,
				)
			}
		}
	} else if catIDStr := r.URL.Query().Get("category_id"); catIDStr != "" {
		// Backward-compatible parameter kept for existing links.
		if catID, err := strconv.ParseUint(catIDStr, 10, 64); err == nil && catID > 0 {
			filtered = filtered.Where("category_id = ?", catID)
		}
	}

	var total int64
	if err := filtered.Count(&total).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	totalPages := 1
	if total > 0 {
		totalPages = int((total + int64(perPage) - 1) / int64(perPage))
	}
	if page > totalPages {
		page = totalPages
	}
	if page < 1 {
		page = 1
	}

	offset := (page - 1) * perPage

	var movies []models.Movie
	if err := filtered.Order("update_time desc").Offset(offset).Limit(perPage).Find(&movies).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// compute page numbers to display (max 7)
	maxDisplay := 7
	var pageNumbers []int
	if totalPages <= maxDisplay {
		for i := 1; i <= totalPages; i++ {
			pageNumbers = append(pageNumbers, i)
		}
	} else {
		half := maxDisplay / 2
		start := page - half
		end := start + maxDisplay - 1
		if start < 1 {
			start = 1
			end = maxDisplay
		}
		if end > totalPages {
			end = totalPages
			start = end - maxDisplay + 1
		}
		for i := start; i <= end; i++ {
			pageNumbers = append(pageNumbers, i)
		}
	}

	h.render(w, pageData{
		Movies:       movies,
		Categories:   categories,
		ActiveCat:    activeCat,
		ActiveSubCat: activeSubCat,
		SiteTitle:    h.SiteTitle,
		Page:         page,
		TotalPages:   totalPages,
		TotalCount:   int(total),
		PerPage:      perPage,
		PageNumbers:  pageNumbers,
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

	// Prepare category context so detail template can render the same nav as index.
	var activeCat, activeSubCat uint
	if subCatIDStr := r.URL.Query().Get("sub_cat"); subCatIDStr != "" {
		if subCatID, err := strconv.ParseUint(subCatIDStr, 10, 64); err == nil && subCatID > 0 {
			activeSubCat = uint(subCatID)
		}
	}
	if catIDStr := r.URL.Query().Get("cat"); catIDStr != "" {
		if catID, err := strconv.ParseUint(catIDStr, 10, 64); err == nil && catID > 0 {
			activeCat = uint(catID)
		}
	}

	h.renderTemplate(w, "detail.html", struct {
		Movie        models.Movie
		Sources      []models.VideoSource
		SiteTitle    string
		Categories   []models.Category
		ActiveCat    uint
		ActiveSubCat uint
	}{
		Movie:        movie,
		Sources:      movie.VideoSources,
		SiteTitle:    h.SiteTitle,
		Categories:   h.loadCategoryTree(),
		ActiveCat:    activeCat,
		ActiveSubCat: activeSubCat,
	})
}
