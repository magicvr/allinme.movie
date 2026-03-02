package models

// Category represents a local (standardised) movie category.
// LocalCategoryID in CategoryMap references this model's ID.
// ParentID = 0 means this is a top-level category; a non-zero ParentID links
// to the ID of the parent Category, forming a two-level hierarchy.
type Category struct {
	ID       uint       `gorm:"primaryKey"`
	ParentID uint       `gorm:"default:0;index;uniqueIndex:idx_cat_name_parent"`
	Name     string     `gorm:"not null;uniqueIndex:idx_cat_name_parent"`
	Enabled  bool       `gorm:"not null;default:true"`
	Children []Category `gorm:"foreignKey:ParentID"`
}
