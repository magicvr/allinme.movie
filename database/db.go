package database

import (
	"log"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"my-movie-site/models"
)

var DB *gorm.DB

func Init(dsn string) {
	var err error
	DB, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	if err = DB.AutoMigrate(&models.Movie{}, &models.VideoSource{}, &models.CollectionSource{}, &models.CategoryMap{}); err != nil {
		log.Fatalf("failed to auto migrate: %v", err)
	}
}
