package models

type VideoSource struct {
	ID                 uint   `gorm:"primaryKey"`
	MovieID            uint   `gorm:"not null;index"`
	CollectionSourceID uint   `gorm:"index"`
	SourceKey          string `gorm:"not null"`
	SourceName         string
	RawURL             string `gorm:"not null"`
	BaseURL            string
}
