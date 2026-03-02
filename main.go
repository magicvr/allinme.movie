package main

import (
	"html/template"
	"log"
	"net/http"
	"os"

	"my-movie-site/admin"
	"my-movie-site/api"
	"my-movie-site/database"
	"my-movie-site/web"
)

func main() {
	dsn := os.Getenv("DB_PATH")
	if dsn == "" {
		dsn = "movie.db"
	}
	database.Init(dsn)

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
	log.Printf("starting server on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
