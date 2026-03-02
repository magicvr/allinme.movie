package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

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

func seedMovie(t *testing.T, db *gorm.DB, thirdPartyID, title, class, area, year string) uint {
	t.Helper()
	m := models.Movie{ThirdPartyID: thirdPartyID, Title: title, Class: class, Area: area, Year: year}
	if err := db.Create(&m).Error; err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	return m.ID
}

func seedSource(t *testing.T, db *gorm.DB, movieID uint, key, rawURL string) uint {
	t.Helper()
	s := models.VideoSource{MovieID: movieID, SourceKey: key, RawURL: rawURL}
	if err := db.Create(&s).Error; err != nil {
		t.Fatalf("seed source: %v", err)
	}
	return s.ID
}

func newMux(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/movies", h.ListMovies)
	mux.HandleFunc("GET /api/search", h.SearchMovies)
	mux.HandleFunc("GET /api/movie/{id}", h.GetMovie)
	return mux
}

// ---- ListMovies tests ----

func TestListMovies_Pagination(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	for i := 0; i < 5; i++ {
		seedMovie(t, db, strconv.Itoa(i), "Movie "+strconv.Itoa(i), "", "", "")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/movies?page=2&size=2", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp moviesResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 5 {
		t.Errorf("total = %d, want 5", resp.Total)
	}
	if resp.Page != 2 {
		t.Errorf("page = %d, want 2", resp.Page)
	}
	if len(resp.Items) != 2 {
		t.Errorf("items len = %d, want 2", len(resp.Items))
	}
}

func TestListMovies_FilterByClass(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	seedMovie(t, db, "1", "Action Movie", "action", "US", "2023")
	seedMovie(t, db, "2", "Drama Movie", "drama", "CN", "2022")

	req := httptest.NewRequest(http.MethodGet, "/api/movies?class=action", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp moviesResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("total = %d, want 1", resp.Total)
	}
	if resp.Items[0].Class != "action" {
		t.Errorf("class = %q, want action", resp.Items[0].Class)
	}
}

func TestListMovies_FilterByArea(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	seedMovie(t, db, "1", "US Movie", "action", "US", "2023")
	seedMovie(t, db, "2", "CN Movie", "drama", "CN", "2022")

	req := httptest.NewRequest(http.MethodGet, "/api/movies?area=CN", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	var resp moviesResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 || resp.Items[0].Area != "CN" {
		t.Errorf("expected 1 CN movie, got total=%d", resp.Total)
	}
}

func TestListMovies_FilterByYear(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	seedMovie(t, db, "1", "Old Movie", "action", "US", "2020")
	seedMovie(t, db, "2", "New Movie", "drama", "CN", "2023")

	req := httptest.NewRequest(http.MethodGet, "/api/movies?year=2023", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	var resp moviesResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 || resp.Items[0].Year != "2023" {
		t.Errorf("expected 1 movie with year 2023, got total=%d", resp.Total)
	}
}

// ---- SearchMovies tests ----

func TestSearchMovies_FuzzyTitle(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	seedMovie(t, db, "1", "The Dark Knight", "action", "US", "2008")
	seedMovie(t, db, "2", "Dark Shadows", "horror", "US", "2012")
	seedMovie(t, db, "3", "Inception", "sci-fi", "US", "2010")

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=dark", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var movies []models.Movie
	if err := json.NewDecoder(rr.Body).Decode(&movies); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(movies) != 2 {
		t.Errorf("len = %d, want 2", len(movies))
	}
}

func TestSearchMovies_MissingQ(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	req := httptest.NewRequest(http.MethodGet, "/api/search", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// ---- GetMovie tests ----

func TestGetMovie_WithSources(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	mid := seedMovie(t, db, "1", "Test Movie", "action", "US", "2023")
	seedSource(t, db, mid, "hd", "http://example.com/1.m3u8")
	seedSource(t, db, mid, "sd", "http://example.com/2.m3u8")

	req := httptest.NewRequest(http.MethodGet, "/api/movie/"+strconv.FormatUint(uint64(mid), 10), nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var detail movieDetail
	if err := json.NewDecoder(rr.Body).Decode(&detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.Title != "Test Movie" {
		t.Errorf("title = %q, want Test Movie", detail.Title)
	}
	if len(detail.VideoSources) != 2 {
		t.Errorf("sources len = %d, want 2", len(detail.VideoSources))
	}
}

func TestGetMovie_NotFound(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	req := httptest.NewRequest(http.MethodGet, "/api/movie/9999", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestGetMovie_InvalidID(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	req := httptest.NewRequest(http.MethodGet, "/api/movie/abc", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}
