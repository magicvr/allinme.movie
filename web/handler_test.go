package web

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	tmpl, err := template.ParseGlob("../templates/*.html")
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

func seedMovieWithSources(t *testing.T, db *gorm.DB, title string, sources []models.VideoSource) models.Movie {
	t.Helper()
	m := models.Movie{ThirdPartyID: title, Title: title, Poster: "https://example.com/poster.jpg", Year: "2024"}
	if err := db.Create(&m).Error; err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	for i := range sources {
		sources[i].MovieID = m.ID
		if err := db.Create(&sources[i]).Error; err != nil {
			t.Fatalf("seed source: %v", err)
		}
	}
	return m
}

func newMux(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /movie/{id}", h.Detail)
	return mux
}

func TestDetail_ShowsMetadataAndSources(t *testing.T) {
	db := newTestDB(t)
	sources := []models.VideoSource{
		{SourceKey: "zy1", RawURL: "http://example.com/1.m3u8"},
		{SourceKey: "zy2", RawURL: "http://example.com/2.m3u8"},
	}
	m := seedMovieWithSources(t, db, "Test Movie", sources)

	h := newHandler(t, db)
	mux := newMux(h)
	req := httptest.NewRequest(http.MethodGet, "/movie/"+strconv.FormatUint(uint64(m.ID), 10), nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Test Movie") {
		t.Error("body missing movie title")
	}
	if !strings.Contains(body, "2024") {
		t.Error("body missing movie year")
	}
	if !strings.Contains(body, "zy1") {
		t.Error("body missing source key 'zy1'")
	}
	if !strings.Contains(body, "zy2") {
		t.Error("body missing source key 'zy2'")
	}
	if !strings.Contains(body, "http://example.com/1.m3u8") {
		t.Error("body missing m3u8 URL for zy1")
	}
	if !strings.Contains(body, "artplayer") {
		t.Error("body missing ArtPlayer container")
	}
}

func TestDetail_NotFound(t *testing.T) {
	db := newTestDB(t)
	h := newHandler(t, db)
	mux := newMux(h)
	req := httptest.NewRequest(http.MethodGet, "/movie/9999", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestDetail_InvalidID(t *testing.T) {
	db := newTestDB(t)
	h := newHandler(t, db)
	mux := newMux(h)
	req := httptest.NewRequest(http.MethodGet, "/movie/abc", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestDetail_NoSources(t *testing.T) {
	db := newTestDB(t)
	m := seedMovieWithSources(t, db, "Empty Movie", nil)

	h := newHandler(t, db)
	mux := newMux(h)
	req := httptest.NewRequest(http.MethodGet, "/movie/"+strconv.FormatUint(uint64(m.ID), 10), nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Empty Movie") {
		t.Error("body missing movie title")
	}
	// No source buttons should be present
	if strings.Contains(body, "data-url=") {
		t.Error("body should not contain source buttons when there are no sources")
	}
}
