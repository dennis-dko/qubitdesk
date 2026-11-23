package model

import "time"

// TicketSearch is a search of the ticket list an agent saved under a name of
// their own. Query is the listing's own query, in the words the listing reads
// it in, and it is read the listing's way both when it is saved and whenever it
// is run - so a saved search asks for nothing the list could not answer.
type TicketSearch struct {
	ID        uint
	UserID    uint
	Name      string
	Query     string
	CreatedAt time.Time
}

// MaxTicketSearchName and MaxTicketSearchQuery bound a saved search's name and
// query, as their columns do.
const (
	MaxTicketSearchName  = 120
	MaxTicketSearchQuery = 2000
)

// MaxTicketSearches bounds the searches one account keeps. Chosen rather than
// derived: past fifty a list of searches is searched itself.
const MaxTicketSearches = 50
