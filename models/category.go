package models

// Category represents a local (standardised) movie category.
// LocalCategoryID in CategoryMap references this model's ID.
type Category struct {
	ID      uint   `gorm:"primaryKey"`
	Name    string `gorm:"not null;uniqueIndex"`
	Enabled bool   `gorm:"not null;default:true"`
}
