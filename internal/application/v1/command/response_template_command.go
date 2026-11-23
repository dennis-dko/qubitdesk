package command

// ResponseTemplateCommand creates or changes an answer template. On a change an
// absent field stays as it was; ClearQueue offers the template in every queue
// again, which an absent QueueID cannot say.
type ResponseTemplateCommand struct {
	Name       *string
	Subject    *string
	Body       *string
	QueueID    *uint
	ClearQueue bool
	Valid      *bool
}
