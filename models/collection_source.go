package models

// CollectionSource represents a remote API source used to collect movies.
type CollectionSource struct {
	ID        uint   `gorm:"primaryKey"`
	Name      string `gorm:"not null"`
	APIURL    string `gorm:"not null"`
	SourceKey string `gorm:"uniqueIndex;not null"`
	Enabled   *bool  `gorm:"not null;default:true"`
}
