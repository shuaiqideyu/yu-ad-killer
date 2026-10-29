package models

import "time"

// BioHistory 用户简介历史
type BioHistory struct {
	ID         int64     `gorm:"primaryKey;autoIncrement;column:id"`
	UserID     int64     `gorm:"column:user_id;not null;index:idx_bio_history_user_id;index:idx_bio_user_detected,priority:1"`
	Bio        string    `gorm:"column:bio;type:text"`
	DetectedAt time.Time `gorm:"column:detected_at;autoCreateTime;index:idx_bio_user_detected,priority:2"`
}

func (BioHistory) TableName() string {
	return "bio_history"
}
