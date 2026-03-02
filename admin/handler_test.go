package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"my-movie-site/models"
)

// boolPtr returns a pointer to the given bool value.
func boolPtr(b bool) *bool { return &b }

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&models.Movie{}, &models.VideoSource{}, &models.CollectionSource{}, &models.CategoryMap{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// seedMovie inserts a Movie and returns its ID.
func seedMovie(t *testing.T, db *gorm.DB, thirdPartyID, title string) uint {
	t.Helper()
	m := models.Movie{ThirdPartyID: thirdPartyID, Title: title}
	if err := db.Create(&m).Error; err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	return m.ID
}

// seedSource inserts a VideoSource and returns its ID.
func seedSource(t *testing.T, db *gorm.DB, movieID uint, key, rawURL string) uint {
	t.Helper()
	s := models.VideoSource{MovieID: movieID, SourceKey: key, RawURL: rawURL}
	if err := db.Create(&s).Error; err != nil {
		t.Fatalf("seed source: %v", err)
	}
	return s.ID
}

// newMux builds a ServeMux with admin routes registered.
func newMux(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /admin/source/{key}", h.DeleteSource)
	mux.HandleFunc("PUT /admin/source/replace-base", h.ReplaceBase)
	mux.HandleFunc("POST /admin/sync", h.Sync)
	mux.HandleFunc("GET /admin/collection-sources", h.ListCollectionSources)
	mux.HandleFunc("POST /admin/collection-sources", h.CreateCollectionSource)
	mux.HandleFunc("DELETE /admin/collection-sources/{id}", h.DeleteCollectionSource)
	return mux
}

func TestDeleteSource_RemovesSourcesAndOrphanMovies(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	// Movie with only "foo" sources → should be deleted after DELETE /admin/source/foo
	mid1 := seedMovie(t, db, "1", "Movie One")
	seedSource(t, db, mid1, "foo", "http://example.com/1.m3u8")

	// Movie with "foo" and "bar" sources → should survive (still has "bar")
	mid2 := seedMovie(t, db, "2", "Movie Two")
	seedSource(t, db, mid2, "foo", "http://example.com/2.m3u8")
	seedSource(t, db, mid2, "bar", "http://example.com/2b.m3u8")

	req := httptest.NewRequest(http.MethodDelete, "/admin/source/foo", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}

	// All "foo" sources should be gone
	var srcCount int64
	db.Model(&models.VideoSource{}).Where("source_key = ?", "foo").Count(&srcCount)
	if srcCount != 0 {
		t.Errorf("foo sources = %d, want 0", srcCount)
	}

	// Movie One had only "foo" sources → should be deleted
	var movieCount int64
	db.Model(&models.Movie{}).Where("id = ?", mid1).Count(&movieCount)
	if movieCount != 0 {
		t.Errorf("Movie One should have been deleted (orphan), but still exists")
	}

	// Movie Two still has "bar" sources → should survive
	db.Model(&models.Movie{}).Where("id = ?", mid2).Count(&movieCount)
	if movieCount != 1 {
		t.Errorf("Movie Two should still exist, got count %d", movieCount)
	}
}

func TestDeleteSource_NonExistentKeyReturnsNoContent(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	req := httptest.NewRequest(http.MethodDelete, "/admin/source/nonexistent", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestReplaceBase_ReplacesMatchingURLs(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	mid := seedMovie(t, db, "1", "Movie One")
	sid1 := seedSource(t, db, mid, "hd", "http://old.cdn.com/path/1.m3u8")
	sid2 := seedSource(t, db, mid, "hd", "http://old.cdn.com/path/2.m3u8")
	// source with a different key – should not be touched
	sid3 := seedSource(t, db, mid, "sd", "http://old.cdn.com/path/3.m3u8")

	body, _ := json.Marshal(replaceBaseRequest{
		SourceKey: "hd",
		OldBase:   "http://old.cdn.com",
		NewBase:   "https://new.cdn.com",
	})
	req := httptest.NewRequest(http.MethodPut, "/admin/source/replace-base", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}

	var s1, s2, s3 models.VideoSource
	db.First(&s1, sid1)
	db.First(&s2, sid2)
	db.First(&s3, sid3)

	if s1.RawURL != "https://new.cdn.com/path/1.m3u8" {
		t.Errorf("s1.RawURL = %q, want https://new.cdn.com/path/1.m3u8", s1.RawURL)
	}
	if s2.RawURL != "https://new.cdn.com/path/2.m3u8" {
		t.Errorf("s2.RawURL = %q, want https://new.cdn.com/path/2.m3u8", s2.RawURL)
	}
	// sd key should be unchanged
	if s3.RawURL != "http://old.cdn.com/path/3.m3u8" {
		t.Errorf("s3.RawURL = %q, want unchanged http://old.cdn.com/path/3.m3u8", s3.RawURL)
	}
}

func TestReplaceBase_OnlyReplacesPrefix(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	mid := seedMovie(t, db, "1", "Movie")
	// URL does NOT start with old_base – should not be modified
	sid := seedSource(t, db, mid, "hd", "http://other.cdn.com/path/1.m3u8")

	body, _ := json.Marshal(replaceBaseRequest{
		SourceKey: "hd",
		OldBase:   "http://old.cdn.com",
		NewBase:   "https://new.cdn.com",
	})
	req := httptest.NewRequest(http.MethodPut, "/admin/source/replace-base", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}

	var s models.VideoSource
	db.First(&s, sid)
	if s.RawURL != "http://other.cdn.com/path/1.m3u8" {
		t.Errorf("RawURL should be unchanged, got %q", s.RawURL)
	}
}

func TestReplaceBase_MissingFieldsReturnsBadRequest(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	cases := []replaceBaseRequest{
		{SourceKey: "", OldBase: "http://old.cdn.com", NewBase: "https://new.cdn.com"},
		{SourceKey: "hd", OldBase: "", NewBase: "https://new.cdn.com"},
	}
	for _, tc := range cases {
		body, _ := json.Marshal(tc)
		req := httptest.NewRequest(http.MethodPut, "/admin/source/replace-base", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d for %+v", rr.Code, tc)
		}
	}
}

func TestCleanOrphanMovies(t *testing.T) {
	db := newTestDB(t)

	// Movie with a source → should survive
	mid1 := seedMovie(t, db, "1", "Has Source")
	seedSource(t, db, mid1, "hd", "http://example.com/1.m3u8")

	// Movie without any source → should be deleted
	seedMovie(t, db, "2", "No Source")

	if err := cleanOrphanMovies(db); err != nil {
		t.Fatalf("cleanOrphanMovies: %v", err)
	}

	var count int64
	db.Model(&models.Movie{}).Count(&count)
	if count != 1 {
		t.Errorf("movie count = %d, want 1", count)
	}

	var surviving models.Movie
	db.First(&surviving)
	if surviving.Title != "Has Source" {
		t.Errorf("surviving movie = %q, want %q", surviving.Title, "Has Source")
	}
}

func TestSync_NoCollectorURLReturnsServiceUnavailable(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db, CollectorURL: ""}
	mux := newMux(h)

	req := httptest.NewRequest(http.MethodPost, "/admin/sync", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

func TestSync_WhileRunningReturnsConflict(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db, CollectorURL: "http://example.com"}

	// Simulate a sync already in progress.
	h.mu.Lock()
	h.syncing = true
	h.mu.Unlock()

	mux := newMux(h)
	req := httptest.NewRequest(http.MethodPost, "/admin/sync", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusConflict)
	}
}

// ---- CollectionSource CRUD tests ----

func TestCreateAndListCollectionSources(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	body, _ := json.Marshal(collectionSourceRequest{
		Name:      "Test Source",
		APIURL:    "http://api.example.com/v1",
		SourceKey: "test_src",
		Enabled:   boolPtr(true),
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/collection-sources", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", rr.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/admin/collection-sources", nil)
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rr2.Code)
	}

	var sources []models.CollectionSource
	if err := json.NewDecoder(rr2.Body).Decode(&sources); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("len = %d, want 1", len(sources))
	}
	if sources[0].Name != "Test Source" {
		t.Errorf("Name = %q, want Test Source", sources[0].Name)
	}
}

func TestDeleteCollectionSource(t *testing.T) {
	db := newTestDB(t)
	h := &Handler{DB: db}
	mux := newMux(h)

	src := models.CollectionSource{Name: "ToDelete", APIURL: "http://x.com", SourceKey: "x", Enabled: boolPtr(true)}
	db.Create(&src)

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/admin/collection-sources/%d", src.ID), nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rr.Code)
	}

	var count int64
	db.Model(&models.CollectionSource{}).Count(&count)
	if count != 0 {
		t.Errorf("source count = %d, want 0", count)
	}
}
