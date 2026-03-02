package models

import "time"

type Movie struct {
	ID          uint      `gorm:"primaryKey"`
	Title       string    `gorm:"not null"`
	SubTitle    string
	Poster      string
	Description string
	Year        string
	Area        string
	Class       string
	UpdateTime  time.Time
}
