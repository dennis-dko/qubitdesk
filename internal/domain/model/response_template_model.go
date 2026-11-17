package model

// ResponseTemplate is an answer written once and sent often - KIX18's text
// modules: a subject and a text an agent starts an article from, offered in the
// queue it names or, naming none, in every queue. Both may carry KIX18's
// placeholders, filled for the ticket the template is used on. A retired one is
// offered nowhere.
type ResponseTemplate struct {
	ID      uint
	Name    string
	Subject string
	Body    string
	QueueID *uint
	Valid   bool
}

// OfferedIn says whether the template is offered for a ticket in the queue.
func (t ResponseTemplate) OfferedIn(queueID uint) bool {
	return t.Valid && (t.QueueID == nil || *t.QueueID == queueID)
}

// MaxTemplateBodyLength bounds a template's text. Chosen rather than derived,
// as MaxMacroTextLength is: an answer sent often is a few paragraphs, and a
// longer one is pasted by mistake.
const MaxTemplateBodyLength = 10000
