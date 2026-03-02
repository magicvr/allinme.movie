package web

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"my-movie-site/models"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&models.Movie{}, &models.VideoSource{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newHandler(t *testing.T, db *gorm.DB) *Handler {
	t.Helper()
	tmpl, err := template.ParseFiles("../templates/index.html")
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	return &Handler{DB: db, Tmpl: tmpl}
}

func seedMovie(t *testing.T, db *gorm.DB, title, poster string, updatedAt time.Time) {
	t.Helper()
	m := models.Movie{
		ThirdPartyID: title,
		Title:        title,
		Poster:       poster,
		UpdateTime:   updatedAt,
	}
	if err := db.Create(&m).Error; err != nil {
		t.Fatalf("seed movie: %v", err)
	}
}

func TestIndex_ReturnsHTMLWithMovies(t *testing.T) {
	db := newTestDB(t)
	seedMovie(t, db, "Inception", "https://example.com/poster.jpg", time.Now())
	seedMovie(t, db, "The Dark Knight", "", time.Now().Add(-time.Hour))

	h := newHandler(t, db)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.Index(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Inception") {
		t.Error("body missing movie title 'Inception'")
	}
	if !strings.Contains(body, "The Dark Knight") {
		t.Error("body missing movie title 'The Dark Knight'")
	}
	if !strings.Contains(body, "最近更新") {
		t.Error("body missing section header '最近更新'")
	}
	if !strings.Contains(body, `type="search"`) {
		t.Error("body missing search input")
	}
}

func TestIndex_EmptyDB(t *testing.T) {
	db := newTestDB(t)
	h := newHandler(t, db)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.Index(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "暂无影片") {
		t.Error("body missing empty-state message")
	}
}

func TestSearch_MatchesTitle(t *testing.T) {
	db := newTestDB(t)
	seedMovie(t, db, "The Dark Knight", "", time.Now())
	seedMovie(t, db, "Inception", "", time.Now())

	h := newHandler(t, db)
	req := httptest.NewRequest(http.MethodGet, "/search?q=dark", nil)
	rr := httptest.NewRecorder()
	h.Search(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "The Dark Knight") {
		t.Error("expected 'The Dark Knight' in results")
	}
	if strings.Contains(body, "Inception") {
		t.Error("did not expect 'Inception' in results for query 'dark'")
	}
}

func TestSearch_EmptyQuery_Redirects(t *testing.T) {
	db := newTestDB(t)
	h := newHandler(t, db)
	req := httptest.NewRequest(http.MethodGet, "/search", nil)
	rr := httptest.NewRecorder()
	h.Search(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/" {
		t.Errorf("Location = %q, want /", rr.Header().Get("Location"))
	}
}
