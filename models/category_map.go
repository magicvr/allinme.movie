package models

// CategoryMap maps a remote category (from a CollectionSource) to a local category ID.
// LocalCategoryID = 0 means the mapping has not been bound yet.
// RemoteTypePID stores the remote API type_pid so callers can identify parent
// categories and implement fallback resolution.
type CategoryMap struct {
	ID              uint   `gorm:"primaryKey"`
	SourceID        uint   `gorm:"not null;uniqueIndex:idx_catmap_source_remote"`
	RemoteTypeID    string `gorm:"not null;uniqueIndex:idx_catmap_source_remote"`
	RemoteName      string // human-readable category name from the remote API class list
	RemoteTypePID   int    `gorm:"default:0"` // parent type_id from the remote API class list
	LocalCategoryID uint   `gorm:"default:0"`
}
