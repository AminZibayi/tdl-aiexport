package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

func TestNormalizeChatTarget(t *testing.T) {
	tests := []struct {
		input      string
		wantID     int64
		wantIsNum  bool
		wantNorm   string
	}{
		{"", 0, false, ""},
		{"me", 0, false, ""},
		{"self", 0, false, ""},
		{"1234567890", 1234567890, true, "1234567890"},
		{"-1001234567890", 1234567890, true, "1234567890"},
		{"-123456789", 123456789, true, "123456789"},
		{"https://t.me/c/1234567890/151", 1234567890, true, "1234567890"},
		{"t.me/c/1234567890", 1234567890, true, "1234567890"},
		{"@Telegram", 0, false, "Telegram"},
		{"https://t.me/Telegram/193", 0, false, "Telegram"},
		{"https://t.me/s/Telegram", 0, false, "Telegram"},
		{"+989123456789", 0, false, "+989123456789"},
	}

	for _, tt := range tests {
		gotID, gotIsNum, gotNorm := normalizeChatTarget(tt.input)
		if gotID != tt.wantID || gotIsNum != tt.wantIsNum || gotNorm != tt.wantNorm {
			t.Errorf("normalizeChatTarget(%q) = (%d, %v, %q), want (%d, %v, %q)",
				tt.input, gotID, gotIsNum, gotNorm, tt.wantID, tt.wantIsNum, tt.wantNorm)
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

func TestResolveOutputFileName(t *testing.T) {
	chat := ChatMetadata{Title: "MyChat", ID: 123}

	tests := []struct {
		outputFile string
		format     string
		want       string
	}{
		{"", "jsonl", ""},
		{"-", "jsonl", ""},
		{"./export/chat.jsonl", "jsonl", "./export/chat.jsonl"},
		{"./export/chat", "jsonl", "./export/chat.jsonl"},
		{"export/chat", "markdown", "export/chat.md"},
		{"chat", "json", "chat.json"},
		{"chat.txt", "json", "chat.txt"},
	}

	for _, tt := range tests {
		exp := &Exporter{opts: ExporterOptions{OutputFile: tt.outputFile, Format: tt.format}}
		got := exp.resolveOutputFileName(chat)
		if got != tt.want {
			t.Errorf("resolveOutputFileName(%q, %q) = %q, want %q", tt.outputFile, tt.format, got, tt.want)
		}
	}
}

func TestInspectAndUpdateExistingExport_JSONL(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "chat.jsonl")

	exp := &Exporter{
		opts:     ExporterOptions{Format: "jsonl"},
		msgCache: make(map[int]*ExportMessage),
	}

	// 1. Inspect empty/non-existent returns error
	_, err := exp.inspectExistingExport(filePath, "jsonl")
	if err == nil {
		t.Fatal("expected error on non-existent file")
	}

	// 2. Create initial JSONL export with 2 messages
	meta := ChatMetadata{
		ID:          123456789,
		Title:       "Alice",
		Username:    "alice_user",
		TotalExport: 2,
	}
	initialMsgs := []*ExportMessage{
		{ID: 100, Time: "2026-10-01 10:00:00", From: "Alice", Text: "Hello"},
		{ID: 105, Time: "2026-10-01 10:05:00", From: "Bob", Text: "Hey there"},
	}
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("create test file: %v", err)
	}
	formatter := NewFormatter(exp.opts)
	if err := formatter.formatJSONL(f, meta, initialMsgs); err != nil {
		t.Fatalf("format initial jsonl: %v", err)
	}
	f.Close()

	// 3. Inspect existing export
	info, err := exp.inspectExistingExport(filePath, "jsonl")
	if err != nil {
		t.Fatalf("inspect existing jsonl: %v", err)
	}
	if info.TotalMsgs != 2 {
		t.Errorf("expected TotalMsgs=2, got %d", info.TotalMsgs)
	}
	if info.LastMsgID != 105 {
		t.Errorf("expected LastMsgID=105, got %d", info.LastMsgID)
	}
	if _, ok := info.ExistingIDs[100]; !ok {
		t.Errorf("expected ExistingIDs to contain 100")
	}
	if _, ok := info.ExistingIDs[105]; !ok {
		t.Errorf("expected ExistingIDs to contain 105")
	}
	if exp.msgCache[105] == nil || exp.msgCache[105].Text != "Hey there" {
		t.Errorf("expected msgCache[105] to be cached for reply enrichment")
	}

	// 4. Update with new messages (IDs 106 and 110)
	newMsgs := []*ExportMessage{
		{ID: 106, Time: "2026-10-02 11:00:00", From: "Alice", Text: "New message"},
		{ID: 110, Time: "2026-10-02 11:30:00", From: "Bob", Text: "Another one"},
	}
	if err := exp.updateExistingExport(filePath, meta, info, newMsgs); err != nil {
		t.Fatalf("update existing export: %v", err)
	}

	// 5. Re-inspect to confirm merged count and last ID
	exp2 := &Exporter{opts: ExporterOptions{Format: "jsonl"}, msgCache: make(map[int]*ExportMessage)}
	info2, err := exp2.inspectExistingExport(filePath, "jsonl")
	if err != nil {
		t.Fatalf("re-inspect jsonl: %v", err)
	}
	if info2.TotalMsgs != 4 {
		t.Errorf("expected TotalMsgs=4 after update, got %d", info2.TotalMsgs)
	}
	if info2.LastMsgID != 110 {
		t.Errorf("expected LastMsgID=110, got %d", info2.LastMsgID)
	}
	if info2.Metadata == nil || info2.Metadata.TotalExport != 4 {
		t.Errorf("expected metadata TotalExport=4, got %+v", info2.Metadata)
	}
}

func TestInspectAndUpdateExistingExport_JSON(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "chat.json")

	exp := &Exporter{
		opts:     ExporterOptions{Format: "json"},
		msgCache: make(map[int]*ExportMessage),
	}

	meta := ChatMetadata{ID: 123, Title: "Test Chat", Type: "channel"}
	initialMsgs := []*ExportMessage{
		{ID: 50, Time: "2026-09-01 10:00:00", From: "Admin", Text: "Welcome"},
	}
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("create test file: %v", err)
	}
	formatter := NewFormatter(exp.opts)
	if err := formatter.formatFullJSON(f, meta, initialMsgs); err != nil {
		t.Fatalf("format initial json: %v", err)
	}
	f.Close()

	info, err := exp.inspectExistingExport(filePath, "json")
	if err != nil {
		t.Fatalf("inspect json: %v", err)
	}
	if info.TotalMsgs != 1 || info.LastMsgID != 50 {
		t.Errorf("unexpected inspect info: %+v", info)
	}

	newMsgs := []*ExportMessage{
		{ID: 55, Time: "2026-09-02 10:00:00", From: "Admin", Text: "Update 1"},
	}
	if err := exp.updateExistingExport(filePath, meta, info, newMsgs); err != nil {
		t.Fatalf("update json: %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read updated json: %v", err)
	}
	var payload FullJSONPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal updated json: %v", err)
	}
	if payload.Chat.TotalExport != 2 || len(payload.Messages) != 2 {
		t.Errorf("expected 2 messages in payload, got %d (%d)", payload.Chat.TotalExport, len(payload.Messages))
	}
}

func TestInspectAndUpdateExistingExport_Markdown(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "chat.md")

	exp := &Exporter{
		opts:     ExporterOptions{Format: "markdown"},
		msgCache: make(map[int]*ExportMessage),
	}

	meta := ChatMetadata{ID: 789, Title: "MD Chat", Type: "group"}
	initialMsgs := []*ExportMessage{
		{ID: 1, Time: "2026-01-01 00:00:00", From: "User1", Text: "First"},
	}
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("create test file: %v", err)
	}
	formatter := NewFormatter(exp.opts)
	if err := formatter.formatMarkdown(f, meta, initialMsgs); err != nil {
		t.Fatalf("format initial md: %v", err)
	}
	f.Close()

	info, err := exp.inspectExistingExport(filePath, "markdown")
	if err != nil {
		t.Fatalf("inspect md: %v", err)
	}
	if info.TotalMsgs != 1 || info.LastMsgID != 1 {
		t.Errorf("unexpected md info: %+v", info)
	}

	newMsgs := []*ExportMessage{
		{ID: 2, Time: "2026-01-01 00:01:00", From: "User2", Text: "Second"},
	}
	if err := exp.updateExistingExport(filePath, meta, info, newMsgs); err != nil {
		t.Fatalf("update md: %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read updated md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "**Messages:** 2") {
		t.Errorf("expected header to contain '- **Messages:** 2', got:\n%s", content)
	}
	if !strings.Contains(content, "### [1] User1") || !strings.Contains(content, "### [2] User2") {
		t.Errorf("expected both messages in markdown, got:\n%s", content)
	}
}
