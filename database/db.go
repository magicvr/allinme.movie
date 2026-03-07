package database

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"my-movie-site/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

func Init(dsn string) {
	// Ensure the directory for the database file exists.
	if dir := filepath.Dir(dsn); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("failed to create database directory %q: %v", dir, err)
		}
	}

	var err error
	DB, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	sqlDB, err := DB.DB()
	if err != nil {
		log.Fatalf("failed to get underlying sql.DB: %v", err)
	}
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err = DB.AutoMigrate(&models.Movie{}, &models.VideoSource{}, &models.CollectionSource{}, &models.CategoryMap{}, &models.Category{}, &models.Setting{}); err != nil {
		log.Fatalf("failed to auto migrate: %v", err)
	}

	SeedDefaultCategories(DB)
}

// SeedDefaultCategories inserts the standard local categories if the table is
// empty (first-run initialisation).
func SeedDefaultCategories(db *gorm.DB) {
	var count int64
	db.Model(&models.Category{}).Count(&count)
	if count > 0 {
		return
	}
	// Initial seeding of built-in categories removed.
	// Categories are now populated from the collector's base API class list.
	log.Print("database: category table empty — skipping default seed (collector will populate categories)")
}
