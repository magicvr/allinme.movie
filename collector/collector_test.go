package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"my-movie-site/models"
)

// boolPtr returns a pointer to the given bool value.
func boolPtr(b bool) *bool { return &b }

// newTestDB opens an in-memory SQLite database with the required schema.
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

// fakeServer builds an httptest.Server that serves multi-page responses.
func fakeServer(t *testing.T, pages [][]apiMovie) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pgStr := r.URL.Query().Get("pg")
		pg := 1
		fmt.Sscanf(pgStr, "%d", &pg)
		if pg < 1 || pg > len(pages) {
			pg = 1
		}
		resp := apiResponse{
			PageCount: len(pages),
			List:      pages[pg-1],
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestParseVideoSources(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantKeys []string
		wantURLs []string
	}{
		{
			name:     "single source",
			raw:      "高清$http://example.com/hd.m3u8",
			wantKeys: []string{"高清"},
			wantURLs: []string{"http://example.com/hd.m3u8"},
		},
		{
			name:     "multiple sources",
			raw:      "高清$http://example.com/hd.m3u8#超清$http://example.com/fhd.m3u8",
			wantKeys: []string{"高清", "超清"},
			wantURLs: []string{"http://example.com/hd.m3u8", "http://example.com/fhd.m3u8"},
		},
		{
			name:     "empty string",
			raw:      "",
			wantKeys: nil,
		},
		{
			name:     "malformed entry skipped",
			raw:      "bad_entry#高清$http://example.com/hd.m3u8",
			wantKeys: []string{"高清"},
			wantURLs: []string{"http://example.com/hd.m3u8"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseVideoSources(tc.raw, 1, 0, "")
			if len(got) != len(tc.wantKeys) {
				t.Fatalf("got %d sources, want %d", len(got), len(tc.wantKeys))
			}
			for i, src := range got {
				if src.SourceKey != tc.wantKeys[i] {
					t.Errorf("[%d] SourceKey = %q, want %q", i, src.SourceKey, tc.wantKeys[i])
				}
				if src.RawURL != tc.wantURLs[i] {
					t.Errorf("[%d] RawURL = %q, want %q", i, src.RawURL, tc.wantURLs[i])
				}
			}
		})
	}
}

func TestCollectorRun_SinglePage(t *testing.T) {
	db := newTestDB(t)
	srv := fakeServer(t, [][]apiMovie{
		{
			{VodID: 1, VodName: "Test Movie", VodPlayURL: "高清$http://example.com/hd.m3u8"},
		},
	})
	defer srv.Close()

	c := New(srv.URL+"?", db)
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var count int64
	db.Model(&models.Movie{}).Count(&count)
	if count != 1 {
		t.Errorf("movie count = %d, want 1", count)
	}

	var src models.VideoSource
	db.First(&src)
	if src.SourceKey != "高清" {
		t.Errorf("SourceKey = %q, want 高清", src.SourceKey)
	}
}

func TestCollectorRun_MultiPage(t *testing.T) {
	db := newTestDB(t)
	srv := fakeServer(t, [][]apiMovie{
		{
			{VodID: 1, VodName: "Movie One", VodPlayURL: "HD$http://example.com/1.m3u8"},
		},
		{
			{VodID: 2, VodName: "Movie Two", VodPlayURL: "SD$http://example.com/2.m3u8"},
		},
	})
	defer srv.Close()

	c := New(srv.URL+"?", db)
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var count int64
	db.Model(&models.Movie{}).Count(&count)
	if count != 2 {
		t.Errorf("movie count = %d, want 2", count)
	}
}

func TestCollectorRun_Upsert(t *testing.T) {
	db := newTestDB(t)
	srv := fakeServer(t, [][]apiMovie{
		{
			{VodID: 42, VodName: "Same Title", VodPic: "http://old.com/poster.jpg", VodPlayURL: "HD$http://example.com/old.m3u8"},
		},
	})
	defer srv.Close()

	c := New(srv.URL+"?", db)
	if err := c.Run(); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Second run: same title, updated poster and play URL.
	srv2 := fakeServer(t, [][]apiMovie{
		{
			{VodID: 42, VodName: "Same Title", VodPic: "http://new.com/poster.jpg", VodPlayURL: "FHD$http://example.com/new.m3u8"},
		},
	})
	defer srv2.Close()

	c2 := New(srv2.URL+"?", db)
	if err := c2.Run(); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	var count int64
	db.Model(&models.Movie{}).Count(&count)
	if count != 1 {
		t.Errorf("movie count = %d after upsert, want 1", count)
	}

	var movie models.Movie
	db.First(&movie)
	if movie.Title != "Same Title" {
		t.Errorf("Title = %q, want %q (title must not change)", movie.Title, "Same Title")
	}
	if movie.Poster != "http://new.com/poster.jpg" {
		t.Errorf("Poster = %q, want %q", movie.Poster, "http://new.com/poster.jpg")
	}

	var srcCount int64
	db.Model(&models.VideoSource{}).Where("movie_id = ?", movie.ID).Count(&srcCount)
	if srcCount != 1 {
		t.Errorf("video source count = %d after upsert, want 1", srcCount)
	}

	var src models.VideoSource
	db.Where("movie_id = ?", movie.ID).First(&src)
	if src.SourceKey != "FHD" {
		t.Errorf("SourceKey = %q, want FHD", src.SourceKey)
	}
}

func TestCollectorRun_SkipsMalformedMovie(t *testing.T) {
	db := newTestDB(t)
	srv := fakeServer(t, [][]apiMovie{
		{
			{VodID: 0, VodName: "", VodPlayURL: ""},           // should be skipped (empty name)
			{VodID: 2, VodName: "Good Movie", VodPlayURL: ""}, // should be deleted (no video sources)
		},
	})
	defer srv.Close()

	c := New(srv.URL+"?", db)
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var count int64
	db.Model(&models.Movie{}).Count(&count)
	if count != 0 {
		t.Errorf("movie count = %d, want 0", count)
	}
}

func TestNewIncremental_AppendsHParam(t *testing.T) {
	db := newTestDB(t)
	var receivedQueries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQueries = append(receivedQueries, r.URL.Query())
		resp := apiResponse{
			PageCount: 1,
			List:      []apiMovie{{VodID: 1, VodName: "Incremental Movie", VodPlayURL: "HD$http://example.com/hd.m3u8"}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewIncremental(srv.URL+"?", db, 24)
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(receivedQueries) == 0 {
		t.Fatal("no API requests made")
	}
	for i, q := range receivedQueries {
		if got := q.Get("h"); got != "24" {
			t.Errorf("request %d: h param = %q, want \"24\"", i, got)
		}
	}
}

// TestMultiSourceAggregation verifies that two different CollectionSources with
// the same movie title end up as a single Movie with two VideoSource records.
func TestMultiSourceAggregation(t *testing.T) {
	db := newTestDB(t)

	srvA := fakeServer(t, [][]apiMovie{
		{{VodID: 1, VodName: "Shared Movie", VodPlayURL: "HD$http://a.example.com/hd.m3u8"}},
	})
	defer srvA.Close()

	srvB := fakeServer(t, [][]apiMovie{
		{{VodID: 99, VodName: "Shared Movie", VodPlayURL: "SD$http://b.example.com/sd.m3u8"}},
	})
	defer srvB.Close()

	// Create two CollectionSource records.
	srcA := models.CollectionSource{Name: "Source A", APIURL: srvA.URL + "?", SourceKey: "source_a", Enabled: boolPtr(true)}
	srcB := models.CollectionSource{Name: "Source B", APIURL: srvB.URL + "?", SourceKey: "source_b", Enabled: boolPtr(true)}
	db.Create(&srcA)
	db.Create(&srcB)

	cA := New(srvA.URL+"?", db)
	cA.CollectionSourceID = srcA.ID
	cA.SourceName = srcA.Name
	if err := cA.Run(); err != nil {
		t.Fatalf("Run source A: %v", err)
	}

	cB := New(srvB.URL+"?", db)
	cB.CollectionSourceID = srcB.ID
	cB.SourceName = srcB.Name
	if err := cB.Run(); err != nil {
		t.Fatalf("Run source B: %v", err)
	}

	// There should be exactly one Movie.
	var movieCount int64
	db.Model(&models.Movie{}).Count(&movieCount)
	if movieCount != 1 {
		t.Errorf("movie count = %d, want 1 (shared)", movieCount)
	}

	// There should be two VideoSources under that movie.
	var vsCount int64
	db.Model(&models.VideoSource{}).Count(&vsCount)
	if vsCount != 2 {
		t.Errorf("video source count = %d, want 2", vsCount)
	}

	// Each VideoSource should carry the SourceName from its collector.
	var vsA, vsB models.VideoSource
	db.Where("collection_source_id = ?", srcA.ID).First(&vsA)
	db.Where("collection_source_id = ?", srcB.ID).First(&vsB)
	if vsA.SourceName != "Source A" {
		t.Errorf("vsA.SourceName = %q, want Source A", vsA.SourceName)
	}
	if vsB.SourceName != "Source B" {
		t.Errorf("vsB.SourceName = %q, want Source B", vsB.SourceName)
	}
}

// TestMultiSourceCleanup verifies that removing a source's links only deletes
// that source's VideoSources and only removes the Movie when it has no sources
// left.
func TestMultiSourceCleanup(t *testing.T) {
	db := newTestDB(t)

	srvFull := fakeServer(t, [][]apiMovie{
		{{VodID: 1, VodName: "Cleanup Movie", VodPlayURL: "HD$http://a.example.com/hd.m3u8"}},
	})
	defer srvFull.Close()

	srvEmpty := fakeServer(t, [][]apiMovie{
		{{VodID: 1, VodName: "Cleanup Movie", VodPlayURL: ""}}, // source removed its links
	})
	defer srvEmpty.Close()

	srcA := models.CollectionSource{Name: "A", APIURL: srvFull.URL + "?", SourceKey: "a", Enabled: boolPtr(true)}
	srcB := models.CollectionSource{Name: "B", APIURL: srvFull.URL + "?", SourceKey: "b", Enabled: boolPtr(true)}
	db.Create(&srcA)
	db.Create(&srcB)

	// Both sources initially provide the movie.
	for _, src := range []models.CollectionSource{srcA, srcB} {
		c := New(src.APIURL, db)
		c.CollectionSourceID = src.ID
		c.SourceName = src.Name
		if err := c.Run(); err != nil {
			t.Fatalf("initial Run for %s: %v", src.Name, err)
		}
	}

	var vsCount int64
	db.Model(&models.VideoSource{}).Count(&vsCount)
	if vsCount != 2 {
		t.Fatalf("setup: expected 2 video sources, got %d", vsCount)
	}

	// Source A now returns the movie with no play URL (link removed).
	cA := New(srvEmpty.URL+"?", db)
	cA.CollectionSourceID = srcA.ID
	cA.SourceName = srcA.Name
	if err := cA.Run(); err != nil {
		t.Fatalf("cleanup Run for source A: %v", err)
	}

	// Only source B's VideoSource should remain; the Movie should survive.
	db.Model(&models.VideoSource{}).Count(&vsCount)
	if vsCount != 1 {
		t.Errorf("video source count = %d, want 1 (only B remains)", vsCount)
	}
	var movieCount int64
	db.Model(&models.Movie{}).Count(&movieCount)
	if movieCount != 1 {
		t.Errorf("movie count = %d, want 1 (B still has it)", movieCount)
	}

	// Now source B also removes its links; the Movie should be deleted.
	cB := New(srvEmpty.URL+"?", db)
	cB.CollectionSourceID = srcB.ID
	cB.SourceName = srcB.Name
	if err := cB.Run(); err != nil {
		t.Fatalf("cleanup Run for source B: %v", err)
	}

	db.Model(&models.VideoSource{}).Count(&vsCount)
	db.Model(&models.Movie{}).Count(&movieCount)
	if vsCount != 0 {
		t.Errorf("video source count = %d, want 0", vsCount)
	}
	if movieCount != 0 {
		t.Errorf("movie count = %d, want 0 (orphan deleted)", movieCount)
	}
}

// TestGetLocalCategoryID_CreatesMissingMapping verifies that an unknown remote
// type gets a placeholder CategoryMap row with LocalCategoryID = 0.
func TestGetLocalCategoryID_CreatesMissingMapping(t *testing.T) {
	db := newTestDB(t)
	c := &Collector{DB: db}

	id := c.GetLocalCategoryID(1, "action")
	if id != 0 {
		t.Errorf("expected 0 for unmapped category, got %d", id)
	}

	// The placeholder row must exist now.
	var cm models.CategoryMap
	if err := db.Where("source_id = ? AND remote_type_id = ?", 1, "action").First(&cm).Error; err != nil {
		t.Fatalf("CategoryMap row not created: %v", err)
	}
	if cm.LocalCategoryID != 0 {
		t.Errorf("LocalCategoryID = %d, want 0", cm.LocalCategoryID)
	}
}

// TestRunAllFromDB verifies that RunAllFromDB runs all enabled sources and
// skips disabled ones.
func TestRunAllFromDB(t *testing.T) {
	db := newTestDB(t)

	srvEnabled := fakeServer(t, [][]apiMovie{
		{{VodID: 1, VodName: "Enabled Movie", VodPlayURL: "HD$http://example.com/hd.m3u8"}},
	})
	defer srvEnabled.Close()

	srvDisabled := fakeServer(t, [][]apiMovie{
		{{VodID: 2, VodName: "Disabled Movie", VodPlayURL: "HD$http://example.com/hd2.m3u8"}},
	})
	defer srvDisabled.Close()

	db.Create(&models.CollectionSource{Name: "Enabled", APIURL: srvEnabled.URL + "?", SourceKey: "enabled", Enabled: boolPtr(true)})
	db.Create(&models.CollectionSource{Name: "Disabled", APIURL: srvDisabled.URL + "?", SourceKey: "disabled", Enabled: boolPtr(false)})

	if err := RunAllFromDB(context.Background(), db); err != nil {
		t.Fatalf("RunAllFromDB: %v", err)
	}

	var movieCount int64
	db.Model(&models.Movie{}).Count(&movieCount)
	if movieCount != 1 {
		t.Errorf("movie count = %d, want 1 (only enabled source runs)", movieCount)
	}

	var movie models.Movie
	db.First(&movie)
	if movie.Title != "Enabled Movie" {
		t.Errorf("movie title = %q, want Enabled Movie", movie.Title)
	}
}
