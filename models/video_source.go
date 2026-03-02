package models

type VideoSource struct {
	ID        uint   `gorm:"primaryKey"`
	MovieID   uint   `gorm:"not null;index"`
	SourceKey string `gorm:"not null"`
	RawURL    string `gorm:"not null"`
	BaseURL   string
}
