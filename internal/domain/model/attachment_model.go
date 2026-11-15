package model

import (
	"mime"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxAttachmentBytes is the largest file an article may carry. A file is kept in
// the database whole and read back whole, so it is bounded where it is kept,
// whichever road it arrived by - an upload now, a mail later.
const MaxAttachmentBytes = 20 << 20

// MaxAttachmentNameLength covers attachments.filename and content_type, both
// VARCHAR(255).
const MaxAttachmentNameLength = 255

// Attachment is a file that came with an article, kept in the database beside
// it. Content is read only when the file itself is asked for; a listing carries
// the rest. TicketID is the article's ticket, which is how a file is reached.
type Attachment struct {
	ID          uint
	ArticleID   uint
	TicketID    uint
	Filename    string
	ContentType string
	Size        int
	Content     []byte
}

// AttachmentName is the name a file is kept under: the last part of what was
// sent, without a folder a client put in front of it or anything unprintable,
// bounded to its column, and "attachment" where nothing is left of it.
func AttachmentName(sent string) string {
	name := sent[strings.LastIndexAny(sent, `/\`)+1:]
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}

		return r
	}, name))

	if utf8.RuneCountInString(name) > MaxAttachmentNameLength {
		name = string([]rune(name)[:MaxAttachmentNameLength])
	}

	if name == "" || name == "." || name == ".." {
		return "attachment"
	}

	return name
}

// AttachmentType is the media type a file is kept under: the one sent if it
// reads as one, without its parameters, and otherwise plain bytes. It is what
// the file is called, never how it is served - every file goes back as bytes,
// because a page or a script served from this origin would run as this
// application.
func AttachmentType(sent string) string {
	media, _, err := mime.ParseMediaType(sent)
	if err != nil || !strings.Contains(media, "/") || len(media) > MaxAttachmentNameLength {
		return "application/octet-stream"
	}

	return media
}
