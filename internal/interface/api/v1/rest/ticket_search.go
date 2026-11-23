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
	return listingFilter(c.Param, caller)
}

// listingFilter reads what a listing asks for through param, which answers a
// parameter's value by its name: the request's own, or a saved search's, so
// that a search is read when it is saved exactly as it will be when it is run.
func listingFilter(param func(string) string, caller *service.Principal) (service.TicketListFilter, error) {
	queue, err := paramUint(param, "queue")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	states, err := queryUints(param, "state")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	owner, err := queryAgent(param, caller, "owner")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	searched, err := ticketSearch(param, caller)
	if err != nil {
		return service.TicketListFilter{}, err
	}

	limit, err := strictInt(param("limit"), "limit")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	page, err := strictInt(param("page"), "page")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	contact, err := paramUint(param, "contact")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	organisation, err := paramUint(param, "organisation")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	searched.QueueID, searched.StateIDs, searched.Unfinished, searched.OwnerID = queue, states,
		param("unfinished") == "true", owner
	searched.ContactID, searched.OrganisationID, searched.Search = contact, organisation, param("q")
	searched.Limit, searched.Page = limit, page

	return searched, nil
}

// ticketSearch reads what a listing of tickets asks for beyond its queue,
// states, owner and customer: the criteria of the ticket search. A value it
// does not know is refused rather than passed over, because a search that
// quietly ignores a criterion answers a question nobody asked.
func ticketSearch(param func(string) string, caller *service.Principal) (service.TicketListFilter, error) {
	var (
		filter service.TicketListFilter
		err    error
	)

	if filter.PriorityIDs, err = queryUints(param, "priority"); err != nil {
		return filter, err
	}

	if filter.TypeIDs, err = queryUints(param, "type"); err != nil {
		return filter, err
	}

	if filter.ResponsibleID, err = queryAgent(param, caller, "responsible"); err != nil {
		return filter, err
	}

	if filter.ServiceLevelID, err = paramUint(param, "serviceLevel"); err != nil {
		return filter, err
	}

	switch param("locked") {
	case "":
	case "true", "false":
		locked := param("locked") == "true"
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
		switch param(flag.name) {
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
		if days.into.First, err = queryDay(param, days.first); err != nil {
			return filter, err
		}

		if days.into.Last, err = queryDay(param, days.last); err != nil {
			return filter, err
		}
	}

	filter.Text = param("text")

	switch param("sort") {
	case "", "changed":
	case "created":
		filter.Sort.By = service.TicketsByCreated
	case "priority":
		filter.Sort.By = service.TicketsByPriority
	default:
		return filter, apperror.InvalidFields("sort")
	}

	switch param("order") {
	case "", "desc":
	case "asc":
		filter.Sort.Ascending = true
	default:
		return filter, apperror.InvalidFields("order")
	}

	return filter, nil
}

// paramUint reads an optional id, 0 where it is absent.
func paramUint(param func(string) string, name string) (uint, error) {
	raw := strings.TrimSpace(param(name))
	if raw == "" {
		return 0, nil
	}

	return parseUint(raw, name)
}

// queryUints reads a query parameter holding ids separated by commas.
func queryUints(param func(string) string, name string) ([]uint, error) {
	var ids []uint

	for _, raw := range strings.Split(param(name), ",") {
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
func queryAgent(param func(string) string, caller *service.Principal, name string) (uint, error) {
	switch raw := strings.TrimSpace(param(name)); raw {
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
func queryDay(param func(string) string, name string) (*time.Time, error) {
	raw := strings.TrimSpace(param(name))
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
