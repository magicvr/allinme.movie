package models

// CategoryMap maps a remote category (from a CollectionSource) to a local category ID.
// LocalCategoryID = 0 means the mapping has not been bound yet.
type CategoryMap struct {
	ID              uint   `gorm:"primaryKey"`
	SourceID        uint   `gorm:"not null;uniqueIndex:idx_catmap_source_remote"`
	RemoteTypeID    string `gorm:"not null;uniqueIndex:idx_catmap_source_remote"`
	RemoteName      string // human-readable category name from the remote API class list
	LocalCategoryID uint   `gorm:"default:0"`
}
