package main

import "time"

// ChatMetadata contains high-level information about the exported chat.
type ChatMetadata struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Username    string    `json:"username,omitempty"`
	Type        string    `json:"type"` // "user", "channel", "supergroup", "group", "saved_messages"
	ExportTime  time.Time `json:"export_time"`
	TotalExport int       `json:"total_messages"`
}

// MediaDetails contains structured metadata and local path for media assets.
type MediaDetails struct {
	Type     string `json:"type"`               // photo, sticker, voice, audio, video, document, animation, poll, contact, location
	Emoji    string `json:"emoji,omitempty"`     // For stickers (e.g. "👍", "😂")
	Path     string `json:"path,omitempty"`      // Local downloaded file path
	FileName string `json:"file_name,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size_bytes,omitempty"`
	Duration int    `json:"duration_sec,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	Caption  string `json:"caption,omitempty"`
	Skipped  string `json:"skipped_reason,omitempty"` // e.g. "exceeds max size limit (25MB > 10MB)"
}

// ReplyDetails captures the contextual snippet of the replied-to message.
type ReplyDetails struct {
	ID   int    `json:"id"`
	From string `json:"from,omitempty"`
	Text string `json:"text,omitempty"`
}

// ForwardDetails captures original source info for forwarded messages.
type ForwardDetails struct {
	From string `json:"from,omitempty"`
	Date string `json:"date,omitempty"`
}

// ExportMessage is the token-optimized, AI-friendly message representation.
type ExportMessage struct {
	ID        int            `json:"id"`
	Time      string         `json:"time"` // Clean timestamp, no redundant unix epoch
	From      string         `json:"from"` // Sender name + username (e.g. "Alice (@alice)")
	SenderID  int64          `json:"sender_id,omitempty"`
	Text      string         `json:"text,omitempty"`
	ReplyTo   *ReplyDetails  `json:"reply_to,omitempty"`
	Forward   *ForwardDetails`json:"forward,omitempty"`
	Media     *MediaDetails  `json:"media,omitempty"`
	Reactions map[string]int `json:"reactions,omitempty"` // Compact {"❤": 1}
	IsEdited  bool           `json:"is_edited,omitempty"`
	IsService bool           `json:"is_service,omitempty"`
}

// FullJSONPayload represents the complete chat export in JSON document format.
type FullJSONPayload struct {
	Chat     ChatMetadata    `json:"chat"`
	Messages []ExportMessage `json:"messages"`
}
