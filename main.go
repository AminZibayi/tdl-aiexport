package main

import (
	"context"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/iyear/tdl/extension"
	"github.com/spf13/pflag"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	chatTarget    string
	outputFile    string
	formatType    string
	limitCount    int
	topicID       int
	sinceDate     string
	untilDate     string
	fromID        int
	toID          int
	reverseOrder  bool
	skipService   bool
	textOnly      bool
	withMediaInfo bool
	downloadMedia bool
	mediaDir      string
	mediaTypes    string
	excludeMedia  string
	maxMediaSize  string
	verbose       bool
	updateMode    bool
	overwriteMode bool
)

func init() {
	pflag.StringVarP(&chatTarget, "chat", "c", "", "Chat ID, @username, or public link (leave empty for Saved Messages)")
	pflag.StringVarP(&outputFile, "output", "o", "", "Output file path (default: stdout or <chat>.<format>)")
	pflag.StringVarP(&formatType, "format", "f", "jsonl", "Export format: jsonl (default), json, markdown")
	pflag.IntVarP(&limitCount, "limit", "n", 0, "Max number of messages to export (0 = all available)")
	pflag.IntVar(&topicID, "topic", 0, "Forum topic ID or message reply thread ID")
	pflag.StringVar(&sinceDate, "since", "", "Filter messages since date/time (ISO 8601 e.g. 2026-01-01, or Unix timestamp)")
	pflag.StringVar(&untilDate, "until", "", "Filter messages until date/time (ISO 8601 e.g. 2026-02-01, or Unix timestamp)")
	pflag.IntVar(&fromID, "from-id", 0, "Minimum message ID (inclusive)")
	pflag.IntVar(&toID, "to-id", 0, "Maximum message ID (inclusive)")
	pflag.BoolVar(&reverseOrder, "reverse", false, "Output newest first (default is false: chronological oldest-to-newest for AI)")
	pflag.BoolVar(&skipService, "skip-service", true, "Skip Telegram service/system messages (pins, joins, etc.)")
	pflag.BoolVar(&textOnly, "text-only", false, "Export only messages with textual content/captions")
	pflag.BoolVar(&withMediaInfo, "with-media", true, "Include media metadata (file name, size, type, mime)")
	pflag.BoolVarP(&downloadMedia, "download-media", "d", false, "Download media files locally and embed relative path references in export")
	pflag.StringVar(&mediaDir, "media-dir", "media", "Directory to store downloaded media files")
	pflag.StringVar(&mediaTypes, "media-types", "all", "Comma-separated media types to include (photo,sticker,voice,audio,video,document,all)")
	pflag.StringVar(&excludeMedia, "exclude-media", "", "Comma-separated media types to exclude from download (e.g. video,document)")
	pflag.StringVar(&maxMediaSize, "max-media-size", "20MB", "Max download size threshold per asset (e.g. 10MB, 500KB, 0 for unlimited)")
	pflag.BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")
	pflag.BoolVarP(&updateMode, "update", "u", true, "Automatically detect last exported message in existing output file and update with new messages (default: true)")
	pflag.BoolVar(&overwriteMode, "overwrite", false, "Overwrite existing export file completely instead of updating incrementally")

	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "tdl-aiexport - Machine-readable & AI-friendly Telegram chat exporter\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  tdl [tdl-global-flags] aiexport [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		pflag.PrintDefaults()
	}

	pflag.Parse()
}

func main() {
	conf := zap.NewDevelopmentConfig()
	if !verbose {
		conf.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	} else {
		conf.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	logger, err := conf.Build(zap.AddCaller())
	if err != nil {
		logger = zap.NewNop()
	}

	extension.New(extension.Options{
		Logger: logger,
	})(func(ctx context.Context, ext *extension.Extension) error {
		opts := ExporterOptions{
			ChatTarget:    chatTarget,
			OutputFile:    outputFile,
			Format:        formatType,
			Limit:         limitCount,
			TopicID:       topicID,
			Since:         sinceDate,
			Until:         untilDate,
			FromID:        fromID,
			ToID:          toID,
			Reverse:       reverseOrder,
			SkipService:   skipService,
			TextOnly:      textOnly,
			WithMedia:     withMediaInfo,
			DownloadMedia: downloadMedia,
			MediaDir:      mediaDir,
			MediaTypes:    mediaTypes,
			ExcludeMedia:  excludeMedia,
			MaxMediaSize:  maxMediaSize,
			UpdateMode:    updateMode,
			Overwrite:     overwriteMode,
		}

		exporter := NewExporter(ext, opts)
		if err := exporter.Run(ctx); err != nil {
			color.Red("Export failed: %v", err)
			return err
		}

		return nil
	})
}
