# tdl-aiexport

A machine-readable and AI-friendly Telegram chat export extension for [`tdl`](https://github.com/iyear/tdl).

**License:** [MIT License](LICENSE)

---

## Why `tdl-aiexport`?

The built-in `tdl chat export` tool was designed for `tdl dl` (file downloads), producing a minimal JSON format that skips sender identities, strips reply chains, outputs messages newest-first, and lacks asset references for vision/audio models.

`tdl-aiexport` transforms Telegram conversations into **token-optimized,
context-aware datasets** structured for **LLMs & RAG Pipelines**,
**Vision-Language Models (VLMs)**, and **Audio Models**

---

## Key Features

### 1. Token-Optimized for LLM Context

- **Single Normalized Timestamp**: Drops duplicate Unix epoch integers; retains a single clean `time` string (`"2026-07-24 08:04:51"`).
- **Compact Reactions**: Serializes emoji reactions as key-value pairs (e.g. `{"❤": 1}`) instead of bloated arrays of objects (`[{"emoji":"❤","count":1}]`), saving ~70% of reaction tokens.
- **Flattened Sender Info**: Uses direct `from` strings (`"Alice (@alice)"`) without repetitive dictionary wrapping.

### 2. Media Asset Downloading & Filtering

- **Unified Media References**: When `--download-media` (`-d`) is enabled, files are downloaded to `media/` and their relative paths (`media/101_photo.jpg`) are embedded directly into your chosen format (`jsonl`, `json`, or `markdown`).
- **Media Type Inclusion/Exclusion**:
  - Filter which media types to download with `--media-types` (e.g. `photo,sticker,voice`) or exclude with `--exclude-media` (e.g. `video,document`).
- **Per-Asset Size Threshold**:
  - Guard your bandwidth and disk space with `--max-media-size` (e.g. `10MB`, `500KB`, `0` = unlimited).
  - If a file exceeds the limit, the payload download is skipped, but rich metadata and a notice (`skipped_reason`) are preserved so models still understand the message context.
- **Stickers & Voice Notes**:
  - Stickers: Semantic emoji fallback (`[Sticker: 👍]`) for text LLMs + local WebP download for vision models.
  - Voice notes: Duration and MIME info (`[Voice Message (14s)]`) + local OGG download for audio transcription models.

### 3. Chronological Conversation Flow

- Automatically sorts messages oldest-to-newest so AI models process conversations naturally from beginning to end (use `--reverse` for newest-first).

### 4. Rich Conversation Context

- **Reply Chains**: Inlines the parent message ID, author, and quote snippet directly into the message.
- **Forwarded Sources**: Preserves original sender name and date.

---

## Formats

| Format       | Flag                              | Description                                                                             | Best For                                |
| ------------ | --------------------------------- | --------------------------------------------------------------------------------------- | --------------------------------------- |
| **JSONL**    | `-f jsonl` _(default)_            | 1 compact JSON object per line with embedded media path/metadata                        | Streaming, RAG, Vector DBs, LangChain   |
| **JSON**     | `-f json`                         | Complete document array with chat metadata and messages                                 | Traditional API backends, archiving     |
| **Markdown** | `-f markdown` _(or `transcript`)_ | Dialogue transcript with inline media (`![Photo](path)`), audio links, and reply quotes | Direct copy-paste into ChatGPT / Claude |

---

## Installation

### Prerequisites

- [Go](https://go.dev/doc/install) 1.21+
- [tdl](https://docs.iyear.me/tdl/) installed and logged in (`tdl login`)

### Build & Install

```bash
git clone https://github.com/AminZibayi/tdl-aiexport.git
cd tdl-aiexport
go build -o tdl-aiexport.exe
tdl extension install --force ./tdl-aiexport.exe
```

Verify installation:

```bash
tdl extension list
```

---

## Usage Examples

> **Note for PowerShell Users**: In PowerShell, `@` is the array splatting operator. Pass usernames without `@` (e.g. `-c alice`) or quote them (`-c '@alice'`).

### 1. Token-Dense JSONL Export (Default)

```bash
tdl aiexport -c alice -n 100 -f jsonl -o export.jsonl
```

### 2. Export with Media Downloads (Photos, Stickers, Voice) with 10MB Threshold

```bash
tdl aiexport -c mygroup -n 50 -f jsonl -d --media-types photo,sticker,voice --max-media-size 10MB -o export.jsonl
```

### 3. Export to Markdown with Inline Media Links

```bash
tdl aiexport -c mychannel -n 50 -f markdown -d --media-types photo,sticker -o transcript.md
```

### 4. Exclude Heavy Media (Videos & Documents)

```bash
tdl aiexport -c mygroup -n 200 -d --exclude-media video,document
```

---

## Available Flags

| Flag               | Short | Default    | Description                                                                             |
| ------------------ | ----- | ---------- | --------------------------------------------------------------------------------------- |
| `--chat`           | `-c`  | _(empty)_  | Chat ID, username, or link (leave empty for Saved Messages)                             |
| `--output`         | `-o`  | _(stdout)_ | Output file path                                                                        |
| `--format`         | `-f`  | `jsonl`    | `jsonl` (default), `json`, `markdown`                                                   |
| `--limit`          | `-n`  | `0`        | Max messages to export (`0` = all)                                                      |
| `--download-media` | `-d`  | `false`    | Download media files locally and embed relative path references                         |
| `--media-dir`      |       | `media`    | Directory to save downloaded media files                                                |
| `--media-types`    |       | `all`      | Comma-separated media types to include (`photo,sticker,voice,audio,video,document,all`) |
| `--exclude-media`  |       | `""`       | Comma-separated media types to exclude from download                                    |
| `--max-media-size` |       | `20MB`     | Max download size threshold per asset (e.g. `10MB`, `500KB`, `0` for unlimited)         |
| `--topic`          |       | `0`        | Forum topic ID or reply thread ID                                                       |
| `--since`          |       | `""`       | Filter messages after date (`YYYY-MM-DD` or Unix ts)                                    |
| `--until`          |       | `""`       | Filter messages before date (`YYYY-MM-DD` or Unix ts)                                   |
| `--from-id`        |       | `0`        | Minimum message ID (inclusive)                                                          |
| `--to-id`          |       | `0`        | Maximum message ID (inclusive)                                                          |
| `--reverse`        |       | `false`    | Reverse order (newest-first instead of chronological)                                   |
| `--skip-service`   |       | `true`     | Skip service/system messages (joins, pins, etc.)                                        |
| `--text-only`      |       | `false`    | Export only messages with text                                                          |
| `--with-media`     |       | `true`     | Include media metadata and paths                                                        |
| `--verbose`        | `-v`  | `false`    | Enable verbose debug logging                                                            |

---

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
