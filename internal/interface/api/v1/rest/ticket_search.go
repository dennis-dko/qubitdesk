package rest

import (
	"strings"
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// ticketListFilter reads what a listing asks for. owner=me is the caller, which
// is what a screen offering "my tickets" means and cannot name by id without
// first asking who it is.
func ticketListFilter(c *gofr.Context, caller *service.Principal) (service.TicketListFilter, error) {
	queue, err := queryUint(c, "queue")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	states, err := queryUints(c, "state")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	owner, err := queryAgent(c, caller, "owner")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	searched, err := ticketSearch(c, caller)
	if err != nil {
		return service.TicketListFilter{}, err
	}

	limit, err := strictInt(c.Param("limit"), "limit")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	page, err := strictInt(c.Param("page"), "page")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	contact, err := queryUint(c, "contact")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	organisation, err := queryUint(c, "organisation")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	searched.QueueID, searched.StateIDs, searched.Unfinished, searched.OwnerID = queue, states,
		c.Param("unfinished") == "true", owner
	searched.ContactID, searched.OrganisationID, searched.Search = contact, organisation, c.Param("q")
	searched.Limit, searched.Page = limit, page

	return searched, nil
}

// ticketSearch reads what a listing of tickets asks for beyond its queue,
// states, owner and customer: the criteria of the ticket search. A value it
// does not know is refused rather than passed over, because a search that
// quietly ignores a criterion answers a question nobody asked.
func ticketSearch(c *gofr.Context, caller *service.Principal) (service.TicketListFilter, error) {
	var (
		filter service.TicketListFilter
		err    error
	)

	if filter.PriorityIDs, err = queryUints(c, "priority"); err != nil {
		return filter, err
	}

	if filter.TypeIDs, err = queryUints(c, "type"); err != nil {
		return filter, err
	}

	if filter.ResponsibleID, err = queryAgent(c, caller, "responsible"); err != nil {
		return filter, err
	}

	if filter.ServiceLevelID, err = queryUint(c, "serviceLevel"); err != nil {
		return filter, err
	}

	switch c.Param("locked") {
	case "":
	case "true", "false":
		locked := c.Param("locked") == "true"
		filter.Locked = &locked
	default:
		return filter, apperror.InvalidFields("locked")
	}

	for _, flag := range []struct {
		name    string
		set     func()
		refused error
	}{
		{"watched", func() {
			if caller.User != nil {
				filter.WatcherID = caller.User.ID
			}
		}, apperror.InvalidFields("watched")},
		{"overdue", func() { filter.Overdue = true }, apperror.InvalidFields("overdue")},
	} {
		switch c.Param(flag.name) {
		case "":
		case "true":
			flag.set()
		default:
			return filter, flag.refused
		}
	}

	for _, days := range []struct {
		into        *service.Days
		first, last string
	}{{&filter.Created, "createdFrom", "createdTo"}, {&filter.Changed, "changedFrom", "changedTo"}} {
		if days.into.First, err = queryDay(c, days.first); err != nil {
			return filter, err
		}

		if days.into.Last, err = queryDay(c, days.last); err != nil {
			return filter, err
		}
	}

	filter.Text = c.Param("text")

	switch c.Param("sort") {
	case "", "changed":
	case "created":
		filter.Sort.By = service.TicketsByCreated
	case "priority":
		filter.Sort.By = service.TicketsByPriority
	default:
		return filter, apperror.InvalidFields("sort")
	}

	switch c.Param("order") {
	case "", "desc":
	case "asc":
		filter.Sort.Ascending = true
	default:
		return filter, apperror.InvalidFields("order")
	}

	return filter, nil
}

// queryUints reads a query parameter holding ids separated by commas.
func queryUints(c *gofr.Context, name string) ([]uint, error) {
	var ids []uint

	for _, raw := range strings.Split(c.Param(name), ",") {
		if raw = strings.TrimSpace(raw); raw == "" {
			continue
		}

		id, err := parseUint(raw, name)
		if err != nil {
			return nil, err
		}

		ids = append(ids, id)
	}

	return ids, nil
}

// queryAgent reads a query parameter naming an account: by id, or "me" for
// the caller - which names nobody where the caller is no account.
func queryAgent(c *gofr.Context, caller *service.Principal, name string) (uint, error) {
	switch raw := strings.TrimSpace(c.Param(name)); raw {
	case "":
		return 0, nil
	case "me":
		if caller.User != nil {
			return caller.User.ID, nil
		}

		return 0, nil
	default:
		return parseUint(raw, name)
	}
}

// queryDay reads a query parameter holding a calendar day, YYYY-MM-DD, as
// model.CalendarDay's midnight UTC.
func queryDay(c *gofr.Context, name string) (*time.Time, error) {
	raw := strings.TrimSpace(c.Param(name))
	if raw == "" {
		return nil, nil
	}

	day, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return nil, apperror.Invalidf("%s %q must be YYYY-MM-DD", name, raw).WithCode("dateFormat", raw)
	}

	day = model.CalendarDay(day)

	return &day, nil
}
