package main

import (
	"log"
	"net/http"
	"os"

	"my-movie-site/admin"
	"my-movie-site/api"
	"my-movie-site/database"
)

func main() {
	dsn := os.Getenv("DB_PATH")
	if dsn == "" {
		dsn = "movie.db"
	}
	database.Init(dsn)

	h := &admin.Handler{DB: database.DB}
	a := &api.Handler{DB: database.DB}

	mux := http.NewServeMux()
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
