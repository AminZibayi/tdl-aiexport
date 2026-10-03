package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/go-faster/errors"
)

type Formatter struct {
	opts ExporterOptions
}

func NewFormatter(opts ExporterOptions) *Formatter {
	return &Formatter{opts: opts}
}

func (f *Formatter) Format(w io.Writer, meta ChatMetadata, messages []*ExportMessage) error {
	switch strings.ToLower(f.opts.Format) {
	case "jsonl", "ndjson":
		return f.formatJSONL(w, meta, messages)
	case "json":
		return f.formatFullJSON(w, meta, messages)
	case "markdown", "md", "transcript", "text":
		return f.formatMarkdown(w, meta, messages)
	default:
		return fmt.Errorf("unsupported format %q, available: jsonl, json, markdown", f.opts.Format)
	}
}

func (f *Formatter) formatJSONL(w io.Writer, meta ChatMetadata, messages []*ExportMessage) error {
	metaLine, err := json.Marshal(map[string]interface{}{
		"_type": "chat_metadata",
		"chat":  meta,
	})
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}
	if _, err := fmt.Fprintln(w, string(metaLine)); err != nil {
		return err
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	for _, msg := range messages {
		if err := enc.Encode(msg); err != nil {
			return errors.Wrap(err, "encode jsonl message")
		}
	}
	return nil
}

func (f *Formatter) formatFullJSON(w io.Writer, meta ChatMetadata, messages []*ExportMessage) error {
	payload := FullJSONPayload{
		Chat:     meta,
		Messages: make([]ExportMessage, len(messages)),
	}
	for i, m := range messages {
		payload.Messages[i] = *m
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(payload)
}

func (f *Formatter) formatMarkdown(w io.Writer, meta ChatMetadata, messages []*ExportMessage) error {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Chat Export: %s\n\n", meta.Title))
	sb.WriteString(fmt.Sprintf("- **Chat ID:** `%d` | **Type:** `%s`\n", meta.ID, meta.Type))
	if meta.Username != "" {
		sb.WriteString(fmt.Sprintf("- **Username:** @%s\n", meta.Username))
	}
	sb.WriteString(fmt.Sprintf("- **Exported At:** %s | **Messages:** %d\n\n", meta.ExportTime.Format("2006-01-02 15:04:05 UTC"), len(messages)))
	sb.WriteString("---\n\n")

	for _, m := range messages {
		if m.IsService {
			sb.WriteString(fmt.Sprintf("*[%s] %s*\n\n", m.Time, m.Text))
			continue
		}

		replyTag := ""
		if m.ReplyTo != nil && m.ReplyTo.ID > 0 {
			if m.ReplyTo.From != "" {
				replyTag = fmt.Sprintf(" (replying to [%d] %s)", m.ReplyTo.ID, m.ReplyTo.From)
			} else {
				replyTag = fmt.Sprintf(" (replying to [%d])", m.ReplyTo.ID)
			}
		}

		sb.WriteString(fmt.Sprintf("### [%d] %s · %s%s\n\n", m.ID, m.From, m.Time, replyTag))

		if m.Forward != nil && m.Forward.From != "" {
			sb.WriteString(fmt.Sprintf("> *Forwarded from %s*\n", m.Forward.From))
		}

		if m.ReplyTo != nil && m.ReplyTo.Text != "" {
			sb.WriteString(fmt.Sprintf("> *%s: %s*\n\n", m.ReplyTo.From, m.ReplyTo.Text))
		}

		if strings.TrimSpace(m.Text) != "" {
			sb.WriteString(m.Text)
			sb.WriteString("\n\n")
		}

		// Render media references inline in Markdown
		if m.Media != nil {
			var mediaLine string
			mediaPath := m.Media.Path

			switch m.Media.Type {
			case "photo":
				if mediaPath != "" {
					mediaLine = fmt.Sprintf("![Photo](%s)\n\n", mediaPath)
				} else if m.Media.Skipped != "" {
					mediaLine = fmt.Sprintf("📷 *[Photo (%s) - skipped: %s]*\n\n", formatBytes(m.Media.Size), m.Media.Skipped)
				}
			case "sticker":
				label := "Sticker"
				if m.Media.Emoji != "" {
					label = fmt.Sprintf("Sticker: %s", m.Media.Emoji)
				}
				if mediaPath != "" {
					mediaLine = fmt.Sprintf("![%s](%s)\n\n", label, mediaPath)
				} else if m.Media.Skipped != "" {
					mediaLine = fmt.Sprintf("🎨 *[%s - skipped: %s]*\n\n", label, m.Media.Skipped)
				}
			case "voice":
				if mediaPath != "" {
					mediaLine = fmt.Sprintf("🎤 [Voice Note (%ds)](%s)\n\n", m.Media.Duration, mediaPath)
				} else if m.Media.Skipped != "" {
					mediaLine = fmt.Sprintf("🎤 *[Voice Note (%ds) - skipped: %s]*\n\n", m.Media.Duration, m.Media.Skipped)
				}
			case "audio":
				title := m.Media.FileName
				if title == "" {
					title = "Audio"
				}
				if mediaPath != "" {
					mediaLine = fmt.Sprintf("🎵 [%s (%ds)](%s)\n\n", title, m.Media.Duration, mediaPath)
				} else if m.Media.Skipped != "" {
					mediaLine = fmt.Sprintf("🎵 *[%s - skipped: %s]*\n\n", title, m.Media.Skipped)
				}
			case "video":
				if mediaPath != "" {
					mediaLine = fmt.Sprintf("🎬 [Video (%ds)](%s)\n\n", m.Media.Duration, mediaPath)
				} else if m.Media.Skipped != "" {
					mediaLine = fmt.Sprintf("🎬 *[Video (%ds, %s) - skipped: %s]*\n\n", m.Media.Duration, formatBytes(m.Media.Size), m.Media.Skipped)
				}
			case "document":
				name := m.Media.FileName
				if name == "" {
					name = "Document"
				}
				if mediaPath != "" {
					mediaLine = fmt.Sprintf("📄 [%s (%s)](%s)\n\n", name, formatBytes(m.Media.Size), mediaPath)
				} else if m.Media.Skipped != "" {
					mediaLine = fmt.Sprintf("📄 *[%s - skipped: %s]*\n\n", name, m.Media.Skipped)
				}
			}

			if mediaLine != "" {
				sb.WriteString(mediaLine)
			}
		}

		if len(m.Reactions) > 0 {
			var rList []string
			for emoji, count := range m.Reactions {
				rList = append(rList, fmt.Sprintf("%s %d", emoji, count))
			}
			sb.WriteString(fmt.Sprintf("Reactions: %s\n\n", strings.Join(rList, "  ")))
		}

		sb.WriteString("---\n\n")
	}

	_, err := io.WriteString(w, sb.String())
	return err
}
func sanitizeNameForAI(s string) string {
	if idx := strings.Index(s, "(@"); idx >= 0 {
		end := strings.Index(s[idx:], ")")
		if end > 2 {
			return s[idx+2 : idx+end]
		}
	}
	var clean strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			clean.WriteRune(r)
		}
	}
	res := clean.String()
	if len(res) > 64 {
		res = res[:64]
	}
	if res == "" {
		res = "user"
	}
	return res
}
