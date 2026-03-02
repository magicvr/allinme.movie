package main

import (
	"context"
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
	tmpl := template.Must(template.ParseGlob(tmplDir + "/*.html"))

	h := &admin.Handler{DB: database.DB}
	a := &api.Handler{DB: database.DB}
	w := &web.Handler{DB: database.DB, Tmpl: tmpl}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", w.Index)
	mux.HandleFunc("GET /search", w.Search)
	mux.HandleFunc("GET /movie/{id}", w.Detail)
	mux.HandleFunc("DELETE /admin/source/{key}", h.DeleteSource)
	mux.HandleFunc("PUT /admin/source/replace-base", h.ReplaceBase)
	mux.HandleFunc("GET /api/movies", a.ListMovies)
	mux.HandleFunc("GET /api/search", a.SearchMovies)
	mux.HandleFunc("GET /api/movie/{id}", a.GetMovie)

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
