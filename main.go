package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"my-movie-site/admin"
	"my-movie-site/api"
	"my-movie-site/database"
	"my-movie-site/scheduler"
	"my-movie-site/web"
)

func main() {
	dsn := os.Getenv("DB_PATH")
	if dsn == "" {
		dsn = "movie.db"
	}
	database.Init(dsn)

	// Start the cron scheduler if a collector API URL is provided.
	var sched *scheduler.Scheduler
	collectorURL := os.Getenv("COLLECTOR_API_URL")
	if collectorURL != "" {
		cronSchedule := os.Getenv("CRON_SCHEDULE")
		if cronSchedule == "" {
			cronSchedule = "0 */6 * * *"
		}
		sched = scheduler.New(collectorURL, database.DB)
		if err := sched.Start(cronSchedule); err != nil {
			log.Fatalf("failed to start scheduler: %v", err)
		}
	}

	tmplDir := os.Getenv("TEMPLATE_DIR")
	if tmplDir == "" {
		tmplDir = "templates"
	}
	funcs := template.FuncMap{
		"eq":  func(a, b interface{}) bool { return fmt.Sprint(a) == fmt.Sprint(b) },
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
	}
	tmpl := template.Must(template.New("").Funcs(funcs).ParseGlob(tmplDir + "/*.html"))

	siteTitle := os.Getenv("SITE_TITLE")
	if siteTitle == "" {
		siteTitle = "AllInMe Movie"
	}
	h := &admin.Handler{DB: database.DB, CollectorURL: collectorURL, Tmpl: tmpl, SiteTitle: siteTitle}
	a := &api.Handler{DB: database.DB}
	w := &web.Handler{DB: database.DB, Tmpl: tmpl, SiteTitle: siteTitle}

	mux := http.NewServeMux()
	// serve static files from ./static at /static/ (use method-prefixed pattern to match existing route style)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))
	mux.HandleFunc("GET /", w.Index)
	mux.HandleFunc("GET /search", w.Search)
	mux.HandleFunc("GET /movie/{id}", w.Detail)
	mux.HandleFunc("GET /admin", h.AdminPage)
	mux.HandleFunc("DELETE /admin/source/{key}", h.DeleteSource)
	mux.HandleFunc("PUT /admin/source/replace-base", h.ReplaceBase)
	mux.HandleFunc("PUT /admin/source/replace-domain", h.ReplaceDomain)
	mux.HandleFunc("POST /admin/sync", h.Sync)
	mux.HandleFunc("GET /admin/collection-sources", h.ListCollectionSources)
	mux.HandleFunc("POST /admin/collection-sources", h.CreateCollectionSource)
	mux.HandleFunc("DELETE /admin/collection-sources/{id}", h.DeleteCollectionSource)
	mux.HandleFunc("GET /admin/category-mapping", h.CategoryMappingPage)
	mux.HandleFunc("GET /admin/categories", h.ListCategories)
	mux.HandleFunc("POST /admin/categories", h.CreateCategory)
	mux.HandleFunc("GET /api/movies", a.ListMovies)
	mux.HandleFunc("GET /api/search", a.SearchMovies)
	mux.HandleFunc("GET /api/movie/{id}", a.GetMovie)
	mux.HandleFunc("GET /api/categories", a.ListCategories)
	mux.HandleFunc("GET /api/admin/category-maps", a.ListAllCategoryMaps)
	mux.HandleFunc("GET /api/admin/category-maps/unmapped", a.ListUnmappedCategories)
	mux.HandleFunc("PUT /api/admin/category-maps", a.UpdateCategoryMaps)

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	srv := &http.Server{Addr: addr, Handler: mux}

	// Listen for SIGINT/SIGTERM to shut down gracefully.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		log.Print("shutting down…")
		if sched != nil {
			sched.Stop()
		}
		if err := srv.Shutdown(context.Background()); err != nil {
			log.Printf("server shutdown error: %v", err)
		}
	}()

	log.Printf("starting server on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
