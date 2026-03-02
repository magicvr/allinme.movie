package collector

import (
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

// newTestDB opens an in-memory SQLite database with the required schema.
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
			got := parseVideoSources(tc.raw, 1)
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
			{VodID: 42, VodName: "Original Title", VodPlayURL: "HD$http://example.com/old.m3u8"},
		},
	})
	defer srv.Close()

	c := New(srv.URL+"?", db)
	if err := c.Run(); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Change the server to return an updated title for the same vod_id.
	srv2 := fakeServer(t, [][]apiMovie{
		{
			{VodID: 42, VodName: "Updated Title", VodPlayURL: "FHD$http://example.com/new.m3u8"},
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
	if movie.Title != "Updated Title" {
		t.Errorf("Title = %q, want %q", movie.Title, "Updated Title")
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
