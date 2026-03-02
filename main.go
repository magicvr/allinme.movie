package main

import (
	"log"
	"net/http"
	"os"

	"my-movie-site/admin"
	"my-movie-site/database"
)

func main() {
	dsn := os.Getenv("DB_PATH")
	if dsn == "" {
		dsn = "movie.db"
	}
	database.Init(dsn)

	h := &admin.Handler{DB: database.DB}

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /admin/source/{key}", h.DeleteSource)
	mux.HandleFunc("PUT /admin/source/replace-base", h.ReplaceBase)

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("starting server on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
