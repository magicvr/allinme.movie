package models

import "time"

type Movie struct {
	ID            uint      `gorm:"primaryKey"`
	ThirdPartyID  string    `gorm:"uniqueIndex"`
	Title         string    `gorm:"not null"`
	SubTitle      string
	Poster        string
	Description   string
	Year          string
	Area          string
	Class         string
	UpdateTime    time.Time
}
