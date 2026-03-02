package scheduler

import (
	"net/url"
	"testing"
	"time"

	"encoding/json"
	"net/http"
	"net/http/httptest"

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
	// Include Category migration because the collector's SyncClasses may run
	// during scheduler tests and expects the categories table to exist.
	if err := db.AutoMigrate(&models.Movie{}, &models.VideoSource{}, &models.Category{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// fakeAPIServer returns an httptest.Server that records the query params of
// each request and responds with a single-page result containing one movie.
func fakeAPIServer(t *testing.T, received *[]url.Values) *httptest.Server {
	t.Helper()
	type apiMovie struct {
		VodID      int    `json:"vod_id"`
		VodName    string `json:"vod_name"`
		VodPlayURL string `json:"vod_play_url"`
	}
	type apiResponse struct {
		PageCount int        `json:"pagecount"`
		List      []apiMovie `json:"list"`
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*received = append(*received, r.URL.Query())
		resp := apiResponse{
			PageCount: 1,
			List:      []apiMovie{{VodID: 1, VodName: "Sched Movie", VodPlayURL: "HD$http://example.com/hd.m3u8"}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestScheduler_StartStop(t *testing.T) {
	db := newTestDB(t)
	var queries []url.Values
	srv := fakeAPIServer(t, &queries)
	defer srv.Close()

	sched := New(srv.URL+"?", db)
	// Use a schedule that never fires during the test so we only verify
	// start/stop without a side-effecting run.
	if err := sched.Start("0 0 31 2 *"); err != nil { // never fires (Feb 31)
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	sched.Stop()
}

func TestScheduler_RunIncremental_AppendsHParam(t *testing.T) {
	db := newTestDB(t)
	var queries []url.Values
	srv := fakeAPIServer(t, &queries)
	defer srv.Close()

	sched := New(srv.URL+"?", db)
	sched.IncrementalHours = 12

	// Call the internal run method directly so the test is not timing-dependent.
	sched.runIncremental()

	if len(queries) == 0 {
		t.Fatal("expected at least one API request")
	}
	// SyncClasses issues a base request without h. Ensure that detail
	// requests include the h param.
	hasDetail := false
	for _, q := range queries {
		if q.Get("ac") == "detail" {
			hasDetail = true
			if got := q.Get("h"); got != "12" {
				t.Errorf("query h = %q, want %q", got, "12")
			}
		}
	}
	if !hasDetail {
		t.Error("expected at least one detail request with ac=detail")
	}
}

func TestScheduler_RunIncremental_InsertsMovies(t *testing.T) {
	db := newTestDB(t)
	var queries []url.Values
	srv := fakeAPIServer(t, &queries)
	defer srv.Close()

	sched := New(srv.URL+"?", db)
	sched.runIncremental()

	var count int64
	db.Model(&models.Movie{}).Count(&count)
	if count != 1 {
		t.Errorf("movie count = %d, want 1", count)
	}
}
