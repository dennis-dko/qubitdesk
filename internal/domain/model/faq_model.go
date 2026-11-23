package model

import "time"

// FAQCategory is a heading of the knowledge base, as KIX18 files its FAQ: a
// tree, each category under one or none. A retired one is offered for no new
// article and keeps the ones filed under it.
type FAQCategory struct {
	ID          uint
	ParentID    *uint
	Name        string
	Description string
	Valid       bool
}

// FAQArticle is what the knowledge base says about one thing, and who may read
// it: the staff alone, customers who are signed in, or anybody - KIX18's three
// visibilities. A retired article is shown to nobody but whoever writes the
// knowledge base.
type FAQArticle struct {
	ID         uint
	CategoryID uint
	Title      string
	Body       string
	Keywords   string
	Visibility FAQVisibility

	// Language is the language it is written in, or nothing for an article any
	// reader is shown whatever language they read.
	Language string

	Valid     bool
	CreatedAt time.Time
	UpdatedAt time.Time

	// UpdatedBy is the account that last wrote it - an id without a key, read as a
	// name when shown, so that an account erased leaves the knowledge base as it
	// was.
	UpdatedBy *uint
}

// FAQVisibility is who may read an article, named as KIX18 names its three.
type FAQVisibility string

// Who may read an article: the staff alone, customers signed in to the portal
// as well, or anybody who opens it.
const (
	VisibilityInternal FAQVisibility = "internal"
	VisibilityExternal FAQVisibility = "external"
	VisibilityPublic   FAQVisibility = "public"
)

// FAQVisibilities are every visibility an article can have, the narrowest first.
func FAQVisibilities() []FAQVisibility {
	return []FAQVisibility{VisibilityInternal, VisibilityExternal, VisibilityPublic}
}

// MaxFAQBodyLength bounds what an article says. Chosen rather than derived: an
// article of the knowledge base is a page, not a manual.
const MaxFAQBodyLength = 100000

// MaxFAQArticlesListed bounds how many articles one search hands out. Chosen
// rather than derived: a reader who needs more than that needs better words.
const MaxFAQArticlesListed = 200
