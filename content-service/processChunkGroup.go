package main

import (
	"time"

	"gorm.io/gorm"
)

// ProcessedChunkGroup maps a user-submitted group of TTS chunks to a reusable audio file.
type ProcessedChunkGroup struct {
	ID        uint   `gorm:"primaryKey"`
	BookID    uint   `gorm:"index"`
	StartIdx  int    `gorm:"not null"` // Inclusive
	EndIdx    int    `gorm:"not null"` // Inclusive
	AudioPath string `gorm:"not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

// checkIfChunkGroupProcessed returns the audio path if an identical chunk group is already processed.
func checkIfChunkGroupProcessed(bookID uint, start, end int) (string, bool) {
	var group ProcessedChunkGroup
	err := db.Where("book_id = ? AND start_idx = ? AND end_idx = ?", bookID, start, end).First(&group).Error
	if err == nil {
		return group.AudioPath, true
	}
	return "", false
}

// saveProcessedChunkGroup persists a new group to the DB.
func saveProcessedChunkGroup(bookID uint, start, end int, path string) error {
	group := ProcessedChunkGroup{
		BookID:    bookID,
		StartIdx:  start,
		EndIdx:    end,
		AudioPath: path,
	}
	return db.Create(&group).Error
}


