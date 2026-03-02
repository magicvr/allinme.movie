package models

import "time"

type Movie struct {
	ID            uint           `gorm:"primaryKey"`
	ThirdPartyID  string         `gorm:"index"`
	CategoryID    uint
	Title         string         `gorm:"not null;index"`
	SubTitle      string
	Poster        string
	Description   string
	Year          string
	Area          string
	Class         string
	UpdateTime    time.Time
	VideoSources  []VideoSource  `gorm:"foreignKey:MovieID" json:"video_sources,omitempty"`
}
