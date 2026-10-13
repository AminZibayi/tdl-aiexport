package main
import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"github.com/gotd/td/constant"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/extension"
	"go.uber.org/zap"
)

type ExporterOptions struct {
	ChatTarget    string
	OutputFile    string
	Format        string
	Limit         int
	TopicID       int
	Since         string
	Until         string
	FromID        int
	ToID          int
	Reverse       bool
	SkipService   bool
	TextOnly      bool
	WithMedia     bool
	DownloadMedia bool
	MediaDir      string
	MediaTypes    string // e.g. "photo,sticker,voice" or "all"
	ExcludeMedia  string // e.g. "video,document"
	MaxMediaSize  string // e.g. "10MB", "500KB", "0" (unlimited)
	UpdateMode    bool
	Overwrite     bool
}

type Exporter struct {
	ext                *extension.Extension
	opts               ExporterOptions
	manager            *peers.Manager
	currentEntities    peer.Entities
	senderCache        map[int64]string
	msgCache           map[int]*ExportMessage
	includedMediaTypes map[string]bool
	excludedMediaTypes []string
	maxMediaSizeBytes  int64
}

func NewExporter(ext *extension.Extension, opts ExporterOptions) *Exporter {
	incMap := make(map[string]bool)
	for _, t := range strings.Split(opts.MediaTypes, ",") {
		t = strings.TrimSpace(strings.ToLower(t))
		if t != "" {
			incMap[t] = true
		}
	}
	if len(incMap) == 0 {
		incMap["all"] = true
	}

	var excList []string
	for _, t := range strings.Split(opts.ExcludeMedia, ",") {
		t = strings.TrimSpace(strings.ToLower(t))
		if t != "" {
			excList = append(excList, t)
		}
	}

	maxBytes, err := parseByteSize(opts.MaxMediaSize)
	if err != nil {
		maxBytes = 0
	}

	return &Exporter{
		ext:                ext,
		opts:               opts,
		senderCache:        make(map[int64]string),
		msgCache:           make(map[int]*ExportMessage),
		includedMediaTypes: incMap,
		excludedMediaTypes: excList,
		maxMediaSizeBytes:  maxBytes,
	}
}

func (e *Exporter) Run(ctx context.Context) error {
	client := e.ext.Client()
	e.manager = peers.Options{
		Storage: &peers.InmemoryStorage{},
		Cache:   &peers.InmemoryCache{},
		Logger:  e.ext.Log(),
	}.Build(client.API())

	// 1. Resolve Target Chat
	peer, err := e.resolveTargetChat(ctx)
	if err != nil {
		return errors.Wrap(err, "resolve chat")
	}

	chatMeta := e.buildChatMetadata(peer)
	color.Cyan("Exporting chat: %s (ID: %d, Type: %s)", chatMeta.Title, chatMeta.ID, chatMeta.Type)

	// Detect existing export to automatically resume from last exported message
	targetFile := e.resolveOutputFileName(chatMeta)
	var existing *ExistingExportInfo

	if targetFile != "" && !e.opts.Overwrite && e.opts.UpdateMode {
		if _, err := os.Stat(targetFile); err == nil {
			info, err := e.inspectExistingExport(targetFile, e.opts.Format)
			if err == nil && info != nil && info.LastMsgID > 0 {
				existing = info
				if e.opts.FromID == 0 {
					e.opts.FromID = existing.LastMsgID + 1
					color.Cyan("Found existing export at %s (%d messages, last ID: %d)", targetFile, existing.TotalMsgs, existing.LastMsgID)
					color.Cyan("Automatically resuming from message ID %d...", e.opts.FromID)
				} else {
					color.Cyan("Found existing export at %s (%d messages, last ID: %d). Using explicit --from-id %d", targetFile, existing.TotalMsgs, existing.LastMsgID, e.opts.FromID)
				}
			}
		}
	}

	if e.opts.DownloadMedia {
		if err := os.MkdirAll(e.opts.MediaDir, 0o755); err != nil {
			return errors.Wrap(err, "create media dir")
		}
		limitStr := "unlimited"
		if e.maxMediaSizeBytes > 0 {
			limitStr = formatBytes(e.maxMediaSizeBytes)
		}
		color.Yellow("Media download enabled -> saving to %s/ (max per file: %s)", e.opts.MediaDir, limitStr)
	}

	// 2. Parse time filters
	sinceTs, untilTs, err := e.parseTimeRange()
	if err != nil {
		return err
	}

	// 3. Construct Message Query
	var q messages.Query
	if e.opts.TopicID != 0 {
		q = query.NewQuery(client.API()).Messages().GetReplies(peer.InputPeer()).MsgID(e.opts.TopicID)
	} else {
		q = query.NewQuery(client.API()).Messages().GetHistory(peer.InputPeer())
	}

	iter := messages.NewIterator(q, 100)

	if e.opts.ToID > 0 {
		iter = iter.OffsetID(e.opts.ToID + 1)
	} else if untilTs > 0 {
		iter = iter.OffsetDate(int(untilTs) + 1)
	}
	// 4. Fetch and process messages
	color.Blue("Fetching messages from Telegram...")
	rawMessages := make([]*ExportMessage, 0, 256)
	count := 0

	for iter.Next(ctx) {
		val := iter.Value()
		e.currentEntities = val.Entities
		msgClass := val.Msg

		msgDate := msgClass.GetDate()
		msgID := msgClass.GetID()

		if sinceTs > 0 && int64(msgDate) < sinceTs {
			break
		}
		if e.opts.FromID > 0 && msgID < e.opts.FromID {
			break
		}

		if untilTs > 0 && int64(msgDate) > untilTs {
			continue
		}
		if e.opts.ToID > 0 && msgID > e.opts.ToID {
			continue
		}

		exportMsg, err := e.processRawMessage(ctx, peer, msgClass)
		if err != nil {
			e.ext.Log().Debug("skip message parse error", zap.Error(err), zap.Int("id", msgID))
			continue
		}
		if exportMsg == nil {
			continue
		}

		if e.opts.SkipService && exportMsg.IsService {
			continue
		}
		if e.opts.TextOnly && strings.TrimSpace(exportMsg.Text) == "" {
			continue
		}

		rawMessages = append(rawMessages, exportMsg)
		e.msgCache[exportMsg.ID] = exportMsg

		count++
		if count%50 == 0 {
			fmt.Printf("\rCollected %d messages...", count)
		}

		if e.opts.Limit > 0 && count >= e.opts.Limit {
			break
		}
	}

	if err := iter.Err(); err != nil {
		return errors.Wrap(err, "iterate messages")
	}

	fmt.Printf("\rCollected total %d messages.\n", len(rawMessages))

	// Filter out messages already present in the existing export to avoid duplicates
	if existing != nil && len(existing.ExistingIDs) > 0 {
		filtered := make([]*ExportMessage, 0, len(rawMessages))
		for _, m := range rawMessages {
			if _, exists := existing.ExistingIDs[m.ID]; !exists {
				filtered = append(filtered, m)
			}
		}
		rawMessages = filtered
	}

	if len(rawMessages) == 0 {
		if existing != nil && existing.TotalMsgs > 0 {
			color.Green("✓ Export is already up to date (%d messages in %s, last ID: %d)", existing.TotalMsgs, targetFile, existing.LastMsgID)
			return nil
		}
		color.Yellow("No messages matched the export criteria.")
		return nil
	}

	// 5. Chronological order (oldest to newest for natural conversation context)
	if !e.opts.Reverse {
		sort.Slice(rawMessages, func(i, j int) bool {
			return rawMessages[i].ID < rawMessages[j].ID
		})
	}

	// 6. Enrich reply context from cached messages
	e.enrichReplyContext(rawMessages)

	// 7. Write Output: update existing export or write fresh
	if existing != nil && existing.TotalMsgs > 0 && targetFile != "" {
		if err := e.updateExistingExport(targetFile, chatMeta, existing, rawMessages); err != nil {
			return errors.Wrap(err, "update existing export")
		}
		color.Green("✓ Export updated successfully (+%d new messages, total: %d in %s)", len(rawMessages), existing.TotalMsgs+len(rawMessages), targetFile)
		return nil
	}

	chatMeta.TotalExport = len(rawMessages)
	outWriter, closeFn, err := e.prepareOutputWriter(chatMeta)
	if err != nil {
		return err
	}
	defer closeFn()

	formatter := NewFormatter(e.opts)
	if err := formatter.Format(outWriter, chatMeta, rawMessages); err != nil {
		return errors.Wrap(err, "format output")
	}

	color.Green("✓ Export completed successfully (%d messages, format: %s)", len(rawMessages), e.opts.Format)
	return nil
}

type ExistingExportInfo struct {
	Path        string
	Format      string
	LastMsgID   int
	TotalMsgs   int
	ExistingIDs map[int]struct{}
	Metadata    *ChatMetadata
}

func (e *Exporter) resolveOutputFileName(chat ChatMetadata) string {
	if e.opts.OutputFile == "" || e.opts.OutputFile == "-" {
		return ""
	}

	filename := e.opts.OutputFile
	base := filepath.Base(filename)
	if !strings.Contains(base, ".") {
		ext := e.opts.Format
		if ext == "markdown" || ext == "transcript" {
			ext = "md"
		}
		filename = fmt.Sprintf("%s.%s", filename, ext)
	}
	return filename
}

func (e *Exporter) inspectExistingExport(filePath string, format string) (*ExistingExportInfo, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if stat.Size() == 0 {
		return &ExistingExportInfo{
			Path:        filePath,
			Format:      format,
			ExistingIDs: make(map[int]struct{}),
		}, nil
	}

	info := &ExistingExportInfo{
		Path:        filePath,
		Format:      format,
		ExistingIDs: make(map[int]struct{}),
	}

	fmtLower := strings.ToLower(format)
	switch {
	case fmtLower == "jsonl" || fmtLower == "ndjson" || strings.HasSuffix(filePath, ".jsonl") || strings.HasSuffix(filePath, ".ndjson"):
		err = e.inspectExistingJSONL(f, info)
	case fmtLower == "json" || strings.HasSuffix(filePath, ".json"):
		err = e.inspectExistingJSON(f, info)
	case fmtLower == "markdown" || fmtLower == "md" || strings.HasSuffix(filePath, ".md"):
		err = e.inspectExistingMarkdown(f, info)
	default:
		err = e.inspectExistingJSONL(f, info)
	}

	return info, err
}

func (e *Exporter) inspectExistingJSONL(f *os.File, info *ExistingExportInfo) error {
	reader := bufio.NewReaderSize(f, 64*1024)
	isFirstLine := true

	for {
		line, err := reader.ReadString('\n')
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			if isFirstLine {
				isFirstLine = false
				if strings.Contains(trimmed, `"_type"`) && strings.Contains(trimmed, `"chat_metadata"`) {
					var header struct {
						Chat ChatMetadata `json:"chat"`
					}
					if json.Unmarshal([]byte(trimmed), &header) == nil {
						info.Metadata = &header.Chat
					}
					continue
				}
			}

			var snippet struct {
				ID   int    `json:"id"`
				From string `json:"from"`
				Text string `json:"text"`
			}
			if json.Unmarshal([]byte(trimmed), &snippet) == nil && snippet.ID > 0 {
				info.TotalMsgs++
				info.ExistingIDs[snippet.ID] = struct{}{}
				if snippet.ID > info.LastMsgID {
					info.LastMsgID = snippet.ID
				}
				e.msgCache[snippet.ID] = &ExportMessage{
					ID:   snippet.ID,
					From: snippet.From,
					Text: snippet.Text,
				}
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
	}
	return nil
}

func (e *Exporter) inspectExistingJSON(f *os.File, info *ExistingExportInfo) error {
	var payload FullJSONPayload
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		return err
	}
	info.Metadata = &payload.Chat
	info.TotalMsgs = len(payload.Messages)
	for _, m := range payload.Messages {
		info.ExistingIDs[m.ID] = struct{}{}
		if m.ID > info.LastMsgID {
			info.LastMsgID = m.ID
		}
		msgCopy := m
		e.msgCache[m.ID] = &msgCopy
	}
	return nil
}

func (e *Exporter) inspectExistingMarkdown(f *os.File, info *ExistingExportInfo) error {
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := reader.ReadString('\n')
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "### [") {
			after := trimmed[5:]
			closeIdx := strings.IndexByte(after, ']')
			if closeIdx > 0 {
				if id, perr := strconv.Atoi(after[:closeIdx]); perr == nil && id > 0 {
					info.TotalMsgs++
					info.ExistingIDs[id] = struct{}{}
					if id > info.LastMsgID {
						info.LastMsgID = id
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
	}
	return nil
}

func replaceFile(src, dst string) error {
	bak := dst + ".bak"
	_ = os.Remove(bak)

	if err := os.Rename(dst, bak); err == nil {
		if err := os.Rename(src, dst); err != nil {
			_ = os.Rename(bak, dst)
			return err
		}
		_ = os.Remove(bak)
		return nil
	}

	_ = os.Remove(dst)
	return os.Rename(src, dst)
}

func (e *Exporter) updateExistingExport(targetFile string, chatMeta ChatMetadata, existing *ExistingExportInfo, newMessages []*ExportMessage) error {
	fmtLower := strings.ToLower(e.opts.Format)
	switch {
	case fmtLower == "jsonl" || fmtLower == "ndjson" || strings.HasSuffix(targetFile, ".jsonl") || strings.HasSuffix(targetFile, ".ndjson"):
		return e.updateExistingJSONL(targetFile, chatMeta, existing, newMessages)
	case fmtLower == "json" || strings.HasSuffix(targetFile, ".json"):
		return e.updateExistingJSON(targetFile, chatMeta, existing, newMessages)
	case fmtLower == "markdown" || fmtLower == "md" || strings.HasSuffix(targetFile, ".md"):
		return e.updateExistingMarkdown(targetFile, chatMeta, existing, newMessages)
	default:
		return e.updateExistingJSONL(targetFile, chatMeta, existing, newMessages)
	}
}

func (e *Exporter) updateExistingJSONL(targetFile string, chatMeta ChatMetadata, existing *ExistingExportInfo, newMessages []*ExportMessage) error {
	src, err := os.Open(targetFile)
	if err != nil {
		return err
	}
	defer src.Close()

	tmpPath := targetFile + ".tmp"
	tmp, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	writeFailed := true
	defer func() {
		_ = tmp.Close()
		if writeFailed {
			_ = os.Remove(tmpPath)
		}
	}()

	chatMeta.TotalExport = existing.TotalMsgs + len(newMessages)
	chatMeta.ExportTime = time.Now().UTC()
	metaLine, err := json.Marshal(map[string]interface{}{
		"_type": "chat_metadata",
		"chat":  chatMeta,
	})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(tmp, string(metaLine)); err != nil {
		return err
	}

	reader := bufio.NewReaderSize(src, 64*1024)
	isFirstLine := true
	for {
		line, err := reader.ReadString('\n')
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			if isFirstLine {
				isFirstLine = false
				if strings.Contains(trimmed, `"_type"`) && strings.Contains(trimmed, `"chat_metadata"`) {
					continue
				}
			}
			if _, werr := fmt.Fprintln(tmp, trimmed); werr != nil {
				return werr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
	}

	enc := json.NewEncoder(tmp)
	enc.SetEscapeHTML(false)
	for _, m := range newMessages {
		if err := enc.Encode(m); err != nil {
			return err
		}
	}

	writeFailed = false
	_ = tmp.Close()
	_ = src.Close()

	return replaceFile(tmpPath, targetFile)
}

func (e *Exporter) updateExistingJSON(targetFile string, chatMeta ChatMetadata, existing *ExistingExportInfo, newMessages []*ExportMessage) error {
	data, err := os.ReadFile(targetFile)
	if err != nil {
		return err
	}

	var payload FullJSONPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}

	seen := make(map[int]bool)
	for _, m := range payload.Messages {
		seen[m.ID] = true
	}

	for _, m := range newMessages {
		if !seen[m.ID] {
			payload.Messages = append(payload.Messages, *m)
			seen[m.ID] = true
		}
	}

	if !e.opts.Reverse {
		sort.Slice(payload.Messages, func(i, j int) bool {
			return payload.Messages[i].ID < payload.Messages[j].ID
		})
	}

	chatMeta.TotalExport = len(payload.Messages)
	chatMeta.ExportTime = time.Now().UTC()
	payload.Chat = chatMeta

	tmpPath := targetFile + ".tmp"
	tmp, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath)

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		_ = tmp.Close()
		return err
	}
	_ = tmp.Close()

	return replaceFile(tmpPath, targetFile)
}

func (e *Exporter) updateExistingMarkdown(targetFile string, chatMeta ChatMetadata, existing *ExistingExportInfo, newMessages []*ExportMessage) error {
	data, err := os.ReadFile(targetFile)
	if err != nil {
		return err
	}

	content := string(data)
	body := content
	if sepIdx := strings.Index(content, "---\n\n"); sepIdx != -1 {
		body = content[sepIdx+len("---\n\n"):]
	}

	chatMeta.TotalExport = existing.TotalMsgs + len(newMessages)
	chatMeta.ExportTime = time.Now().UTC()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Chat Export: %s\n\n", chatMeta.Title))
	sb.WriteString(fmt.Sprintf("- **Chat ID:** `%d` | **Type:** `%s`\n", chatMeta.ID, chatMeta.Type))
	if chatMeta.Username != "" {
		sb.WriteString(fmt.Sprintf("- **Username:** @%s\n", chatMeta.Username))
	}
	sb.WriteString(fmt.Sprintf("- **Exported At:** %s | **Messages:** %d\n\n", chatMeta.ExportTime.Format("2006-01-02 15:04:05 UTC"), chatMeta.TotalExport))
	sb.WriteString("---\n\n")

	sb.WriteString(body)
	if !strings.HasSuffix(body, "\n\n") && !strings.HasSuffix(body, "---\n\n") {
		sb.WriteString("\n\n")
	}

	var newMsgsBuf strings.Builder
	formatter := NewFormatter(e.opts)
	for _, m := range newMessages {
		formatter.renderMarkdownMessage(&newMsgsBuf, m)
	}
	sb.WriteString(newMsgsBuf.String())

	tmpPath := targetFile + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(sb.String()), 0o644); err != nil {
		return err
	}
	defer os.Remove(tmpPath)

	return replaceFile(tmpPath, targetFile)
}

func normalizeChatTarget(target string) (numericID int64, isNumeric bool, normalized string) {
	target = strings.TrimSpace(target)
	if target == "" || strings.EqualFold(target, "me") || strings.EqualFold(target, "self") {
		return 0, false, ""
	}

	if !strings.HasPrefix(target, "+") {
		if id, err := strconv.ParseInt(target, 10, 64); err == nil && id != 0 {
			if id < 0 {
				tdID := constant.TDLibPeerID(id)
				if tdID.IsChannel() || tdID.IsChat() {
					plain := tdID.ToPlain()
					return plain, true, strconv.FormatInt(plain, 10)
				}
				plain := -id
				return plain, true, strconv.FormatInt(plain, 10)
			}
			return id, true, strconv.FormatInt(id, 10)
		}
	}

	trimmed := strings.TrimPrefix(target, "@")
	trimmed = strings.TrimPrefix(trimmed, "https://t.me/")
	trimmed = strings.TrimPrefix(trimmed, "http://t.me/")
	trimmed = strings.TrimPrefix(trimmed, "t.me/")
	trimmed = strings.TrimPrefix(trimmed, "/")

	if strings.HasPrefix(trimmed, "c/") {
		rest := strings.TrimPrefix(trimmed, "c/")
		parts := strings.SplitN(rest, "/", 2)
		if id, err := strconv.ParseInt(parts[0], 10, 64); err == nil && id > 0 {
			return id, true, strconv.FormatInt(id, 10)
		}
	}

	if strings.HasPrefix(trimmed, "s/") {
		trimmed = strings.TrimPrefix(trimmed, "s/")
	}
	if !strings.HasPrefix(trimmed, "+") && !strings.HasPrefix(trimmed, "joinchat/") {
		if idx := strings.IndexByte(trimmed, '/'); idx > 0 {
			trimmed = trimmed[:idx]
		}
		if idx := strings.IndexByte(trimmed, '?'); idx > 0 {
			trimmed = trimmed[:idx]
		}
	}

	return 0, false, trimmed
}

func (e *Exporter) applyPeerEntities(ctx context.Context, ent peer.Entities) {
	usersMap := ent.Users()
	chatsMap := ent.Chats()
	channelsMap := ent.Channels()
	if len(usersMap) == 0 && len(chatsMap) == 0 && len(channelsMap) == 0 {
		return
	}

	users := make([]tg.UserClass, 0, len(usersMap))
	for _, u := range usersMap {
		users = append(users, u)
	}

	chats := make([]tg.ChatClass, 0, len(chatsMap)+len(channelsMap))
	for _, c := range chatsMap {
		chats = append(chats, c)
	}
	for _, ch := range channelsMap {
		chats = append(chats, ch)
	}

	_ = e.manager.Apply(ctx, users, chats)
}

func (e *Exporter) findPeerInEntities(ctx context.Context, ent peer.Entities, id int64) (peers.Peer, bool) {
	if ch, ok := ent.Channels()[id]; ok {
		e.applyPeerEntities(ctx, ent)
		return e.manager.Channel(ch), true
	}
	if u, ok := ent.Users()[id]; ok {
		e.applyPeerEntities(ctx, ent)
		return e.manager.User(u), true
	}
	if c, ok := ent.Chats()[id]; ok {
		e.applyPeerEntities(ctx, ent)
		return e.manager.Chat(c), true
	}
	return nil, false
}

func (e *Exporter) resolveNumericChat(ctx context.Context, plainID int64) (peers.Peer, error) {
	if self, err := e.manager.Self(ctx); err == nil && self.ID() == plainID {
		return self, nil
	}

	dlgIter := query.GetDialogs(e.ext.Client().API()).BatchSize(100).Iter()
	for dlgIter.Next(ctx) {
		elem := dlgIter.Value()
		if p, ok := e.findPeerInEntities(ctx, elem.Entities, plainID); ok {
			return p, nil
		}
	}

	if contacts, err := e.ext.Client().API().ContactsGetContacts(ctx, 0); err == nil {
		if c, ok := contacts.(*tg.ContactsContacts); ok {
			_ = e.manager.Apply(ctx, c.Users, nil)
			for _, userClass := range c.Users {
				if u, ok := userClass.AsNotEmpty(); ok && u.ID == plainID {
					return e.manager.User(u), nil
				}
			}
		}
	}

	p, err := tutil.GetInputPeer(ctx, e.manager, strconv.FormatInt(plainID, 10))
	if err == nil {
		return p, nil
	}

	return nil, fmt.Errorf("failed to resolve chat %q: %w", e.opts.ChatTarget, err)
}

func (e *Exporter) resolveTargetChat(ctx context.Context) (peers.Peer, error) {
	numericID, isNumeric, normalized := normalizeChatTarget(e.opts.ChatTarget)
	if normalized == "" && !isNumeric {
		return e.manager.Self(ctx)
	}

	if isNumeric {
		return e.resolveNumericChat(ctx, numericID)
	}

	target := strings.TrimSpace(e.opts.ChatTarget)
	if p, err := e.manager.Resolve(ctx, target); err == nil {
		return p, nil
	}

	p, err := tutil.GetInputPeer(ctx, e.manager, normalized)
	if err == nil {
		return p, nil
	}

	lower := strings.ToLower(normalized)
	if p, err2 := tutil.GetInputPeer(ctx, e.manager, lower); err2 == nil {
		return p, nil
	}

	return nil, fmt.Errorf("failed to resolve chat %q: %w", target, err)
}

func (e *Exporter) buildChatMetadata(p peers.Peer) ChatMetadata {
	meta := ChatMetadata{
		ID:         p.ID(),
		Title:      p.VisibleName(),
		ExportTime: time.Now().UTC(),
	}

	switch v := p.(type) {
	case peers.User:
		meta.Type = "user"
		if username, ok := v.Username(); ok {
			meta.Username = username
		}
		if v.Self() {
			meta.Type = "saved_messages"
			meta.Title = "Saved Messages"
		}
	case peers.Channel:
		if v.IsBroadcast() {
			meta.Type = "channel"
		} else {
			meta.Type = "supergroup"
		}
		if username, ok := v.Username(); ok {
			meta.Username = username
		}
	case peers.Chat:
		meta.Type = "group"
	default:
		meta.Type = "chat"
	}

	return meta
}

func (e *Exporter) parseTimeRange() (int64, int64, error) {
	var sinceTs, untilTs int64

	parseDate := func(s string) (int64, error) {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, nil
		}
		if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
			return ts, nil
		}
		layouts := []string{
			time.RFC3339,
			"2006-01-02 15:04:05",
			"2006-01-02",
		}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, s); err == nil {
				return t.Unix(), nil
			}
		}
		return 0, fmt.Errorf("unable to parse date %q", s)
	}

	var err error
	sinceTs, err = parseDate(e.opts.Since)
	if err != nil {
		return 0, 0, err
	}

	untilTs, err = parseDate(e.opts.Until)
	if err != nil {
		return 0, 0, err
	}

	return sinceTs, untilTs, nil
}

func (e *Exporter) processRawMessage(ctx context.Context, defaultPeer peers.Peer, msgClass tg.NotEmptyMessage) (*ExportMessage, error) {
	switch m := msgClass.(type) {
	case *tg.Message:
		return e.parseRegularMessage(ctx, defaultPeer, m)
	case *tg.MessageService:
		return e.parseServiceMessage(ctx, defaultPeer, m)
	default:
		return nil, nil
	}
}

func (e *Exporter) parseRegularMessage(ctx context.Context, defaultPeer peers.Peer, m *tg.Message) (*ExportMessage, error) {
	senderName, senderID := e.resolveSender(ctx, defaultPeer, m.FromID)
	msgTime := time.Unix(int64(m.Date), 0).UTC().Format("2006-01-02 15:04:05")

	text := m.Message

	var media *MediaDetails
	if e.opts.WithMedia && m.Media != nil {
		media = e.extractAndDownloadMedia(ctx, m.ID, m.Media)
	}

	// Semantic text fallback for LLMs when text is empty
	if strings.TrimSpace(text) == "" && media != nil {
		switch media.Type {
		case "sticker":
			if media.Emoji != "" {
				text = fmt.Sprintf("[Sticker: %s]", media.Emoji)
			} else {
				text = "[Sticker]"
			}
		case "voice":
			text = fmt.Sprintf("[Voice Message (%ds)]", media.Duration)
		case "photo":
			if media.Caption != "" {
				text = media.Caption
			} else {
				text = "[Photo]"
			}
		case "video":
			if media.Caption != "" {
				text = media.Caption
			} else {
				text = fmt.Sprintf("[Video (%ds)]", media.Duration)
			}
		case "audio":
			if media.FileName != "" {
				text = fmt.Sprintf("[Audio: %s (%ds)]", media.FileName, media.Duration)
			} else {
				text = fmt.Sprintf("[Audio (%ds)]", media.Duration)
			}
		case "document":
			text = fmt.Sprintf("[Document: %s]", media.FileName)
		case "poll":
			text = fmt.Sprintf("[Poll: %s]", media.Caption)
		case "location":
			text = "[Shared Location]"
		case "contact":
			text = fmt.Sprintf("[Contact: %s]", media.Caption)
		}
	}

	var reply *ReplyDetails
	if m.ReplyTo != nil {
		if header, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
			reply = &ReplyDetails{
				ID: header.ReplyToMsgID,
			}
		}
	}

	var forward *ForwardDetails
	if fwd, ok := m.GetFwdFrom(); ok {
		forward = &ForwardDetails{
			Date: time.Unix(int64(fwd.Date), 0).UTC().Format("2006-01-02 15:04:05"),
		}
		if fwd.FromName != "" {
			forward.From = fwd.FromName
		}
		if fwd.FromID != nil {
			fwdSender, _ := e.resolveSender(ctx, defaultPeer, fwd.FromID)
			forward.From = fwdSender
		}
	}

	reactions := extractCompactReactions(m.Reactions)

	return &ExportMessage{
		ID:        m.ID,
		Time:      msgTime,
		From:      senderName,
		SenderID:  senderID,
		Text:      text,
		ReplyTo:   reply,
		Forward:   forward,
		Media:     media,
		Reactions: reactions,
		IsEdited:  m.EditDate > 0,
		IsService: false,
	}, nil
}

func (e *Exporter) parseServiceMessage(ctx context.Context, defaultPeer peers.Peer, m *tg.MessageService) (*ExportMessage, error) {
	senderName, senderID := e.resolveSender(ctx, defaultPeer, m.FromID)
	msgTime := time.Unix(int64(m.Date), 0).UTC().Format("2006-01-02 15:04:05")
	serviceDesc, _ := describeServiceAction(m.Action)

	return &ExportMessage{
		ID:        m.ID,
		Time:      msgTime,
		From:      senderName,
		SenderID:  senderID,
		Text:      fmt.Sprintf("[System: %s]", serviceDesc),
		IsService: true,
	}, nil
}

func (e *Exporter) resolveSender(ctx context.Context, defaultPeer peers.Peer, peer tg.PeerClass) (string, int64) {
	if peer == nil {
		return defaultPeer.VisibleName(), defaultPeer.ID()
	}

	id := tutil.GetPeerID(peer)
	if id == 0 {
		return "Unknown", 0
	}

	if name, cached := e.senderCache[id]; cached {
		return name, id
	}

	e.applyPeerEntities(ctx, e.currentEntities)

	var name string
	switch p := peer.(type) {
	case *tg.PeerUser:
		if u, err := e.manager.ResolveUserID(ctx, p.UserID); err == nil {
			displayName := u.VisibleName()
			if username, ok := u.Username(); ok {
				name = fmt.Sprintf("%s (@%s)", displayName, username)
			} else {
				name = displayName
			}
		}
	case *tg.PeerChannel:
		if ch, err := e.manager.ResolveChannelID(ctx, p.ChannelID); err == nil {
			displayName := ch.VisibleName()
			if username, ok := ch.Username(); ok {
				name = fmt.Sprintf("%s (@%s)", displayName, username)
			} else {
				name = displayName
			}
		}
	case *tg.PeerChat:
		if chat, err := e.manager.ResolveChatID(ctx, p.ChatID); err == nil {
			name = chat.VisibleName()
		}
	}

	if name == "" {
		name = fmt.Sprintf("User_%d", id)
	}

	e.senderCache[id] = name
	return name, id
}

func (e *Exporter) enrichReplyContext(messages []*ExportMessage) {
	for _, msg := range messages {
		if msg.ReplyTo == nil || msg.ReplyTo.ID == 0 {
			continue
		}

		if parent, ok := e.msgCache[msg.ReplyTo.ID]; ok {
			msg.ReplyTo.From = parent.From
			snippet := parent.Text
			if len(snippet) > 80 {
				snippet = snippet[:77] + "..."
			}
			msg.ReplyTo.Text = snippet
		}
	}
}

func (e *Exporter) shouldDownloadMedia(mediaType string, sizeBytes int64) (bool, string) {
	if !e.opts.DownloadMedia {
		return false, ""
	}

	// 1. Check excluded types
	for _, ex := range e.excludedMediaTypes {
		if strings.EqualFold(ex, mediaType) {
			return false, "type excluded by user"
		}
	}

	// 2. Check included types
	if len(e.includedMediaTypes) > 0 && !e.includedMediaTypes["all"] {
		if !e.includedMediaTypes[strings.ToLower(mediaType)] {
			return false, "type not in included list"
		}
	}

	// 3. Check per-asset size limit
	if e.maxMediaSizeBytes > 0 && sizeBytes > e.maxMediaSizeBytes {
		return false, fmt.Sprintf("exceeds max size limit (%s > %s)", formatBytes(sizeBytes), formatBytes(e.maxMediaSizeBytes))
	}

	return true, ""
}

func (e *Exporter) extractAndDownloadMedia(ctx context.Context, msgID int, mediaClass tg.MessageMediaClass) *MediaDetails {
	if mediaClass == nil {
		return nil
	}

	client := e.ext.Client().API()
	down := downloader.NewDownloader()

	switch m := mediaClass.(type) {
	case *tg.MessageMediaPhoto:
		photo, ok := m.Photo.(*tg.Photo)
		if !ok {
			return &MediaDetails{Type: "photo"}
		}

		details := &MediaDetails{
			Type:     "photo",
			MimeType: "image/jpeg",
		}

		var largestType string
		var estimatedSize int64
		for _, s := range photo.Sizes {
			switch sz := s.(type) {
			case *tg.PhotoSize:
				largestType = sz.Type
				details.Width = sz.W
				details.Height = sz.H
				estimatedSize = int64(sz.Size)
			case *tg.PhotoSizeProgressive:
				largestType = sz.Type
				details.Width = sz.W
				details.Height = sz.H
				for _, szVal := range sz.Sizes {
					if int64(szVal) > estimatedSize {
						estimatedSize = int64(szVal)
					}
				}
			}
		}
		details.Size = estimatedSize

		canDownload, reason := e.shouldDownloadMedia("photo", estimatedSize)
		if canDownload && largestType != "" {
			fileName := fmt.Sprintf("%d_photo.jpg", msgID)
			filePath := filepath.Join(e.opts.MediaDir, fileName)
			loc := &tg.InputPhotoFileLocation{
				ID:            photo.ID,
				AccessHash:    photo.AccessHash,
				FileReference: photo.FileReference,
				ThumbSize:     largestType,
			}
			if _, err := down.Download(client, loc).ToPath(ctx, filePath); err == nil {
				details.Path = filepath.ToSlash(filePath)
			}
		} else if reason != "" {
			details.Skipped = reason
		}

		return details

	case *tg.MessageMediaDocument:
		doc, ok := m.Document.(*tg.Document)
		if !ok {
			return &MediaDetails{Type: "document"}
		}

		details := &MediaDetails{
			Type:     "document",
			MimeType: doc.MimeType,
			Size:     doc.Size,
		}

		ext := ".bin"
		for _, attr := range doc.Attributes {
			switch a := attr.(type) {
			case *tg.DocumentAttributeFilename:
				details.FileName = a.FileName
				ext = filepath.Ext(a.FileName)
			case *tg.DocumentAttributeSticker:
				details.Type = "sticker"
				details.Emoji = a.Alt
				ext = ".webp"
			case *tg.DocumentAttributeAudio:
				details.Duration = a.Duration
				if a.Voice {
					details.Type = "voice"
					ext = ".ogg"
				} else {
					details.Type = "audio"
					details.FileName = a.Title
					ext = ".mp3"
				}
			case *tg.DocumentAttributeVideo:
				details.Type = "video"
				details.Duration = int(a.Duration)
				details.Width = a.W
				details.Height = a.H
				ext = ".mp4"
			}
		}

		canDownload, reason := e.shouldDownloadMedia(details.Type, doc.Size)
		if canDownload {
			fileName := fmt.Sprintf("%d_%s%s", msgID, details.Type, ext)
			if details.FileName != "" && details.Type == "document" {
				fileName = fmt.Sprintf("%d_%s", msgID, details.FileName)
			}
			filePath := filepath.Join(e.opts.MediaDir, fileName)
			loc := doc.AsInputDocumentFileLocation()
			if _, err := down.Download(client, loc).ToPath(ctx, filePath); err == nil {
				details.Path = filepath.ToSlash(filePath)
			}
		} else if reason != "" {
			details.Skipped = reason
		}

		return details

	case *tg.MessageMediaPoll:
		return &MediaDetails{
			Type:    "poll",
			Caption: m.Poll.Question.Text,
		}

	case *tg.MessageMediaContact:
		return &MediaDetails{
			Type:    "contact",
			Caption: fmt.Sprintf("%s %s (%s)", m.FirstName, m.LastName, m.PhoneNumber),
		}

	case *tg.MessageMediaGeo, *tg.MessageMediaVenue:
		return &MediaDetails{Type: "location"}

	case *tg.MessageMediaWebPage:
		if wp, ok := m.Webpage.(*tg.WebPage); ok {
			return &MediaDetails{
				Type:     "webpage",
				FileName: wp.Title,
				Caption:  wp.URL,
			}
		}
		return &MediaDetails{Type: "webpage"}

	default:
		return &MediaDetails{Type: "other"}
	}
}

func extractCompactReactions(reactions tg.MessageReactions) map[string]int {
	if len(reactions.Results) == 0 {
		return nil
	}
	res := make(map[string]int)
	for _, r := range reactions.Results {
		if emojiReaction, ok := r.Reaction.(*tg.ReactionEmoji); ok {
			res[emojiReaction.Emoticon] = r.Count
		}
	}
	return res
}

func (e *Exporter) prepareOutputWriter(chat ChatMetadata) (io.Writer, func(), error) {
	filename := e.resolveOutputFileName(chat)
	if filename == "" {
		return os.Stdout, func() {}, nil
	}

	if dir := filepath.Dir(filename); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, errors.Wrap(err, "create output directory")
		}
	}

	f, err := os.Create(filename)
	if err != nil {
		return nil, nil, errors.Wrap(err, "create output file")
	}

	closeFn := func() {
		_ = f.Close()
	}

	color.Cyan("Writing export to: %s", filename)
	return f, closeFn, nil
}

func describeServiceAction(action tg.MessageActionClass) (string, string) {
	if action == nil {
		return "System event", "generic"
	}
	switch a := action.(type) {
	case *tg.MessageActionChatCreate:
		return fmt.Sprintf("Chat created: %q", a.Title), "chat_create"
	case *tg.MessageActionChatEditTitle:
		return fmt.Sprintf("Chat title changed: %q", a.Title), "title_change"
	case *tg.MessageActionChatAddUser:
		return "User joined chat", "user_join"
	case *tg.MessageActionChatDeleteUser:
		return "User left chat", "user_leave"
	case *tg.MessageActionPinMessage:
		return "Message was pinned", "pin"
	default:
		return "Service event", "service"
	}
}

func parseByteSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" || s == "0" {
		return 0, nil
	}
	multi := int64(1)
	if strings.HasSuffix(s, "GB") || strings.HasSuffix(s, "G") {
		multi = 1024 * 1024 * 1024
		s = strings.TrimRight(s, "GB")
	} else if strings.HasSuffix(s, "MB") || strings.HasSuffix(s, "M") {
		multi = 1024 * 1024
		s = strings.TrimRight(s, "MB")
	} else if strings.HasSuffix(s, "KB") || strings.HasSuffix(s, "K") {
		multi = 1024
		s = strings.TrimRight(s, "KB")
	} else if strings.HasSuffix(s, "B") {
		s = strings.TrimRight(s, "B")
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q (use e.g. 10MB, 500KB)", s)
	}
	return v * multi, nil
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
