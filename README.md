<div align="center">

# <img src="assets/logo.svg" width="30" alt="tdl-aiexport"> tdl-aiexport

**Token-optimized, machine-readable & multimodal Telegram chat exporter for [`tdl`](https://github.com/iyear/tdl)**

[![release](https://img.shields.io/github/v/release/AminZibayi/tdl-aiexport?style=flat-square&color=blue)](https://github.com/AminZibayi/tdl-aiexport/releases) [![license](https://img.shields.io/badge/license-MIT-blue.svg?style=flat-square)](https://github.com/AminZibayi/tdl-aiexport/blob/main/LICENSE) [![go](https://img.shields.io/badge/Go-1.23%2B-blue?style=flat-square&logo=go&logoColor=white)](https://go.dev/) [![ci](https://img.shields.io/github/actions/workflow/status/AminZibayi/tdl-aiexport/ci.yml?branch=main&style=flat-square&label=CI)](https://github.com/AminZibayi/tdl-aiexport/actions) [![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen?style=flat-square)](https://github.com/AminZibayi/tdl-aiexport/pulls)

Transforms Telegram conversations into token-dense, context-aware datasets ready for Large Language Models (Claude, GPT, Llama, DeepSeek), Vision-Language Models (VLMs), and Audio Transcription Pipelines (Whisper).

[**Quick Start**](#quick-start) · [**Highlights**](#highlights) · [**Formats**](#formats) · [**CLI Options**](#cli-options) · [**Examples**](#examples) · [**License**](#license)

</div>

---

## Highlights

- **Token-dense for LLM context:** Drops redundant Unix epoch integers, serializes compact key-value reactions (`{"❤": 1}`), flattens sender identity, and strips empty fields to maximize context window efficiency.
- **Unified media pipeline:** When `--download-media` is enabled, media assets (photos, stickers, voice notes, audio, video) are downloaded locally, and relative paths are embedded directly into your chosen format (`jsonl`, `json`, or `markdown`).
- **Selective filtering & size caps:** Granularly include or exclude media types (`--media-types photo,sticker,voice`, `--exclude-media video`) and guard bandwidth with per-asset download thresholds (`--max-media-size 10MB`).
- **Full multimodal & sticker support:** Embeds semantic sticker emojis (`[Sticker: 👍]`) for text LLMs, downloads WebP/JPEG for vision models (VLMs), and downloads OGG audio for speech-to-text models.
- **Chronological conversation flow:** Automatically sorts messages oldest-to-newest so AI models process conversations naturally from beginning to end.
- **Rich context preservation:** Inlines parent message IDs, author names, and quote snippets into replies, and preserves forwarded origins.
- **Native `tdl` integration:** Hooks seamlessly into `tdl`'s official extension ecosystem and authenticated MTProto session.

---

## Quick Start

### 1. Install via `tdl`

Once published on GitHub, install directly with `tdl`:

```bash
tdl extension install AminZibayi/tdl-aiexport
```

Or build and install locally from source:

```bash
git clone https://github.com/AminZibayi/tdl-aiexport.git
cd tdl-aiexport
go build -o tdl-aiexport.exe
tdl extension install --force ./tdl-aiexport.exe
```

### 2. Export a Chat

```bash
tdl aiexport -c alice -n 100 -f jsonl -o chat.jsonl
```

> **Note for PowerShell Users:** In PowerShell, `@` is the array splatting operator. Pass usernames without `@` (e.g. `-c alice`) or quote them (`-c '@alice'`).

---

## Formats

| Format | CLI Flag | Description | Best For |
|---|---|---|---|
| **JSONL** | `-f jsonl` *(default)* | 1 compact JSON object per line with embedded media references | Streaming, RAG pipelines, Vector DBs, LangChain |
| **JSON** | `-f json` | Complete document array with chat metadata and message tree | API backends, full backups, data interchange |
| **Markdown** | `-f markdown` *(or `transcript`)* | Clean dialogue transcript with inline media (`![Photo](path)`), audio links, and quotes | Direct copy-paste into ChatGPT, Claude, Gemini |

### JSONL Sample

```jsonl
{"_type":"chat_metadata","chat":{"id":12345678,"title":"Dev Team","type":"supergroup","export_time":"2026-10-03T12:00:00Z","total_messages":2}}
{"id":101,"time":"2026-10-03 12:00:00","from":"Alice (@alice)","text":"Has anyone tested the new MTProto rate limiter?"}
{"id":102,"time":"2026-10-03 12:01:15","from":"Bob (@bob)","text":"Yes, works cleanly. [Sticker: 👍]","reply_to":{"id":101,"from":"Alice (@alice)","text":"Has anyone tested the new MTProto..."},"media":{"type":"sticker","emoji":"👍","path":"media/102_sticker.webp"},"reactions":{"❤":2}}
```

### Markdown Sample

````markdown
# Chat Export: Dev Team

- **Chat ID:** `12345678` | **Type:** `supergroup`
- **Exported At:** 2026-10-03 12:00:00 UTC | **Messages:** 2

---

### [101] Alice (@alice) · 2026-10-03 12:00:00

Has anyone tested the new MTProto rate limiter?

---

### [102] Bob (@bob) · 2026-10-03 12:01:15 (replying to [101] Alice (@alice))

> *Alice (@alice): Has anyone tested the new MTProto...*

Yes, works cleanly.

![Sticker: 👍](media/102_sticker.webp)

Reactions: ❤ 2

---
````

---

## CLI Options

| Flag | Short | Default | Description |
|---|---|---|---|
| `--chat` | `-c` | `""` | Target chat: username, numeric ID, link, or empty for Saved Messages |
| `--output` | `-o` | *(stdout)* | Output file path (`-` or empty writes to stdout) |
| `--format` | `-f` | `jsonl` | Output format: `jsonl` (default), `json`, or `markdown` |
| `--limit` | `-n` | `0` | Max messages to export (`0` = all available) |
| `--download-media` | `-d` | `false` | Download media assets locally and embed relative path references |
| `--media-dir` | | `media` | Directory to store downloaded media assets |
| `--media-types` | | `all` | Comma-separated media types to include (`photo,sticker,voice,audio,video,document,all`) |
| `--exclude-media` | | `""` | Comma-separated media types to exclude from download |
| `--max-media-size` | | `20MB` | Max download size threshold per asset (e.g. `10MB`, `500KB`, `0` for unlimited) |
| `--topic` | | `0` | Forum topic ID or message reply thread ID |
| `--since` | | `""` | Filter messages after date (`YYYY-MM-DD` or Unix timestamp) |
| `--until` | | `""` | Filter messages before date (`YYYY-MM-DD` or Unix timestamp) |
| `--from-id` | | `0` | Minimum message ID (inclusive) |
| `--to-id` | | `0` | Maximum message ID (inclusive) |
| `--reverse` | | `false` | Reverse order (newest-first instead of chronological) |
| `--skip-service` | | `true` | Skip service/system events (joins, pins, title changes) |
| `--text-only` | | `false` | Export only messages containing text |
| `--with-media` | | `true` | Include media metadata and paths |
| `--verbose` | `-v` | `false` | Enable verbose debug logging |

---

## Examples

### 1. Token-Dense JSONL Export for RAG & Vector Embeddings
```bash
tdl aiexport -c golang_chat -n 1000 -f jsonl -o golang_chat.jsonl
```

### 2. Export with Selective Media & Download Threshold (VLMs)
Downloads photos, stickers, and voice notes capped at 10MB each:
```bash
tdl aiexport -c mygroup -n 100 -f jsonl -d --media-types photo,sticker,voice --max-media-size 10MB -o export.jsonl
```

### 3. Markdown Transcript for Prompt Context
```bash
tdl aiexport -c mychannel -n 50 -f markdown -d --media-types photo,sticker -o transcript.md
```

### 4. Forum Topic / Group Thread Export
```bash
tdl aiexport -c -100123456789 --topic 251011 -f jsonl -o topic.jsonl
```

### 5. Date Range Filtering
```bash
tdl aiexport -c mygroup --since 2026-01-01 --until 2026-03-01 -f jsonl -o q1_archive.jsonl
```

### 6. Exclude Heavy Media Types
```bash
tdl aiexport -c mygroup -n 200 -d --exclude-media video,document -o chat.jsonl
```

---

## Sponsoring

If you find this extension useful, consider supporting development:

[![Sponsor](https://img.shields.io/badge/Sponsor-black?style=for-the-badge&logo=githubsponsors&logoColor=white)](https://github.com/sponsors/AminZibayi)

---

## License

[MIT](LICENSE) © [Amin Zibayi](https://github.com/AminZibayi)
