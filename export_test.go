package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		wantErr  bool
	}{
		{"", 0, false},
		{"0", 0, false},
		{"500B", 500, false},
		{"10KB", 10 * 1024, false},
		{"5K", 5 * 1024, false},
		{"20MB", 20 * 1024 * 1024, false},
		{"2M", 2 * 1024 * 1024, false},
		{"1GB", 1 * 1024 * 1024 * 1024, false},
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		got, err := parseByteSize(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseByteSize(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.expected {
			t.Errorf("parseByteSize(%q) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestSanitizeNameForAI(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Alice (@alice)", "alice"},
		{"Bob (@bob_user)", "bob_user"},
		{"John Doe", "JohnDoe"},
		{"@developer", "developer"},
		{"", "user"},
		{"Special!@#Characters", "SpecialCharacters"},
	}

	for _, tt := range tests {
		got := sanitizeNameForAI(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeNameForAI(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestExtractCompactReactions(t *testing.T) {
	reactions := tg.MessageReactions{
		Results: []tg.ReactionCount{
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "👍"},
				Count:    3,
			},
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
				Count:    5,
			},
		},
	}

	got := extractCompactReactions(reactions)
	if got == nil {
		t.Fatal("expected reactions map, got nil")
	}
	if got["👍"] != 3 || got["❤️"] != 5 {
		t.Errorf("unexpected reactions map: %+v", got)
	}
}

func TestFormatters(t *testing.T) {
	meta := ChatMetadata{
		ID:          12345678,
		Title:       "Test Chat",
		Username:    "testchat",
		Type:        "supergroup",
		ExportTime:  time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
		TotalExport: 2,
	}

	messages := []*ExportMessage{
		{
			ID:   1,
			Time: "2026-10-03 12:00:00",
			From: "Alice (@alice)",
			Text: "Hello world",
		},
		{
			ID:   2,
			Time: "2026-10-03 12:01:00",
			From: "Bob (@bob)",
			Text: "Reply message",
			ReplyTo: &ReplyDetails{
				ID:   1,
				From: "Alice (@alice)",
				Text: "Hello world",
			},
			Reactions: map[string]int{"👍": 1},
		},
	}

	opts := ExporterOptions{}
	formatter := NewFormatter(opts)

	// 1. Test JSONL
	var bufJSONL bytes.Buffer
	if err := formatter.formatJSONL(&bufJSONL, meta, messages); err != nil {
		t.Fatalf("formatJSONL failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(bufJSONL.String()), "\n")
	if len(lines) != 3 { // 1 header + 2 messages
		t.Errorf("expected 3 JSONL lines, got %d", len(lines))
	}

	// 2. Test Full JSON
	var bufJSON bytes.Buffer
	if err := formatter.formatFullJSON(&bufJSON, meta, messages); err != nil {
		t.Fatalf("formatFullJSON failed: %v", err)
	}
	if !strings.Contains(bufJSON.String(), `"title": "Test Chat"`) {
		t.Errorf("full JSON missing chat title")
	}

	// 3. Test Markdown
	var bufMD bytes.Buffer
	if err := formatter.formatMarkdown(&bufMD, meta, messages); err != nil {
		t.Fatalf("formatMarkdown failed: %v", err)
	}
	mdStr := bufMD.String()
	if !strings.Contains(mdStr, "# Chat Export: Test Chat") {
		t.Errorf("markdown missing title header")
	}
	if !strings.Contains(mdStr, "### [1] Alice (@alice)") {
		t.Errorf("markdown missing message header")
	}
	if !strings.Contains(mdStr, "Reactions: 👍 1") {
		t.Errorf("markdown missing reactions")
	}
}
