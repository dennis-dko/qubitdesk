package command

// FAQCategoryCommand creates or changes a category of the knowledge base. On a
// change an absent field stays as it was; ClearParent puts the category at the
// top again, which an absent ParentID cannot say.
type FAQCategoryCommand struct {
	Name        *string
	Description *string
	ParentID    *uint
	ClearParent bool
	Valid       *bool
}

// FAQArticleCommand creates or changes an article of the knowledge base. On a
// change an absent field stays as it was.
type FAQArticleCommand struct {
	CategoryID *uint
	Title      *string
	Body       *string
	Keywords   *string
	Visibility *string
	Language   *string
	Valid      *bool
}
