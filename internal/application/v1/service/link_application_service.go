package service

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// LinkApplicationService keeps the links between records - a ticket and a
// configuration item, or two items - as KIX18 links its objects.
//
// A link is made through the record it starts from, which the caller has to be
// able to change, to one the caller can read. Reading a record's links leaves
// out every end the caller may not see, as that record would be not found to
// them: a link to a ticket in a queue they do not reach says nothing about it,
// not even that it is there. Undoing a link takes the right to change the
// record it is undone through. The rights themselves are the handler's to ask,
// because which ones depends on the kinds a request names; here a record the
// caller lacks the right to read is simply one they do not see.
type LinkApplicationService struct {
	links   repository.LinkRepository
	tickets *TicketApplicationService
	items   *ConfigItemApplicationService
	changes *ChangeApplicationService
	now     func() time.Time
}

// NewLinkApplicationService joins records through the services that say who
// may see and change them.
func NewLinkApplicationService(
	links repository.LinkRepository,
	tickets *TicketApplicationService,
	items *ConfigItemApplicationService,
) *LinkApplicationService {
	return &LinkApplicationService{links: links, tickets: tickets, items: items, now: time.Now}
}

// WithChanges lets links reach changes. Optional: without it a change is no
// record a link can name.
func (s *LinkApplicationService) WithChanges(changes *ChangeApplicationService) *LinkApplicationService {
	s.changes = changes

	return s
}

// LinkCommand is a link as somebody makes it: from the record they change, to
// the one it joins, in a type.
type LinkCommand struct {
	SourceObject model.LinkObject
	SourceID     uint
	TargetObject model.LinkObject
	TargetID     uint
	Type         model.LinkType
}

// LinkedRecord is a link as one of its ends sees it: the record at the other
// end and what it is called, and whether the end asked about is the link's
// source, which decides how a directed link reads.
type LinkedRecord struct {
	Link    *model.Link
	Object  model.LinkObject
	ID      uint
	Label   string
	Outward bool
}

// Of is the links of a record the caller can read, each to a record the caller
// can read as well.
func (s *LinkApplicationService) Of(
	ctx context.Context,
	caller *Principal,
	object model.LinkObject,
	id uint,
) ([]*LinkedRecord, error) {
	if _, err := s.label(ctx, caller, object, id, false); err != nil {
		return nil, err
	}

	links, err := s.links.Of(ctx, object, id)
	if err != nil {
		return nil, err
	}

	records := make([]*LinkedRecord, 0, len(links))

	for _, link := range links {
		record, err := s.seenFrom(ctx, caller, link, object, id)
		if apperror.KindOf(err) == apperror.KindNotFound {
			continue
		}

		if err != nil {
			return nil, err
		}

		records = append(records, &record)
	}

	return records, nil
}

// Create links two records, and answers the link as its source sees it.
func (s *LinkApplicationService) Create(ctx context.Context, caller *Principal, cmd LinkCommand) (*LinkedRecord, error) {
	var invalid []string

	if !slices.Contains(model.LinkObjects(), cmd.SourceObject) || cmd.SourceID == 0 {
		invalid = append(invalid, "sourceObject")
	}

	if !slices.Contains(model.LinkObjects(), cmd.TargetObject) || cmd.TargetID == 0 {
		invalid = append(invalid, "targetObject")
	}

	if !slices.Contains(model.LinkTypes(), cmd.Type) {
		invalid = append(invalid, "linkType")
	}

	if len(invalid) > 0 {
		return nil, apperror.InvalidFields(invalid...)
	}

	if cmd.SourceObject == cmd.TargetObject && cmd.SourceID == cmd.TargetID {
		return nil, apperror.Invalidf("a record is not linked to itself").WithCode("linkToItself")
	}

	if _, err := s.label(ctx, caller, cmd.SourceObject, cmd.SourceID, true); err != nil {
		return nil, err
	}

	if _, err := s.label(ctx, caller, cmd.TargetObject, cmd.TargetID, false); err != nil {
		return nil, err
	}

	existing, err := s.links.Of(ctx, cmd.SourceObject, cmd.SourceID)
	if err != nil {
		return nil, err
	}

	if slices.ContainsFunc(existing, func(link *model.Link) bool { return link.Joins(cmd.TargetObject, cmd.TargetID) }) {
		return nil, apperror.Conflictf("the two are joined already").WithCode("linkTaken")
	}

	var author uint
	if id := callerID(caller); id != nil {
		author = *id
	}

	link, err := s.links.Create(ctx, &model.Link{
		SourceObject: cmd.SourceObject, SourceID: cmd.SourceID, TargetObject: cmd.TargetObject,
		TargetID: cmd.TargetID, Type: cmd.Type, CreatedAt: s.now().UTC(), CreatedBy: author,
	})
	if err != nil {
		return nil, err
	}

	record, err := s.seenFrom(ctx, caller, link, cmd.SourceObject, cmd.SourceID)
	if err != nil {
		return nil, err
	}

	return &record, nil
}

// Delete undoes a link through one of its ends, which the caller has to be
// able to change. A link that does not have the record at an end is not found
// through it.
func (s *LinkApplicationService) Delete(
	ctx context.Context,
	caller *Principal,
	object model.LinkObject,
	id, linkID uint,
) error {
	link, err := s.links.GetByID(ctx, linkID)
	if err != nil {
		return err
	}

	if !link.Joins(object, id) {
		return apperror.NotFound("link", strconv.FormatUint(uint64(linkID), 10))
	}

	if _, err := s.label(ctx, caller, object, id, true); err != nil {
		return err
	}

	return s.links.Delete(ctx, linkID)
}

// seenFrom is a link as the record at one of its ends sees it.
func (s *LinkApplicationService) seenFrom(
	ctx context.Context,
	caller *Principal,
	link *model.Link,
	object model.LinkObject,
	id uint,
) (LinkedRecord, error) {
	record := LinkedRecord{Link: link, Object: link.TargetObject, ID: link.TargetID, Outward: true}

	if link.SourceObject != object || link.SourceID != id {
		record = LinkedRecord{Link: link, Object: link.SourceObject, ID: link.SourceID}
	}

	label, err := s.label(ctx, caller, record.Object, record.ID, false)
	record.Label = label

	return record, err
}

// label is what a record is called, where the caller may read it - or change
// it, where change is asked. A record that is not there, or not there for the
// caller, is not found.
func (s *LinkApplicationService) label(
	ctx context.Context,
	caller *Principal,
	object model.LinkObject,
	id uint,
	change bool,
) (string, error) {
	switch object {
	case model.LinkTicket:
		right := model.PermTicketRead
		if change {
			right = model.PermTicketWrite
		}

		if !caller.Can(right) {
			return "", apperror.NotFound("ticket", strconv.FormatUint(uint64(id), 10))
		}

		ticket, _, err := s.tickets.reached(ctx, caller, id, change)
		if err != nil {
			return "", err
		}

		return ticket.Number + " " + ticket.Title, nil
	case model.LinkConfigItem:
		right := model.PermAssetRead
		if change {
			right = model.PermAssetWrite
		}

		if !caller.Can(right) {
			return "", apperror.NotFound("configitem", strconv.FormatUint(uint64(id), 10))
		}

		item, err := s.items.items.GetByID(ctx, id)
		if err != nil {
			return "", err
		}

		return item.Number + " " + item.Name, nil
	case model.LinkChange:
		right := model.PermChangeRead
		if change {
			right = model.PermChangeWrite
		}

		if s.changes == nil || !caller.Can(right) {
			return "", apperror.NotFound("change", strconv.FormatUint(uint64(id), 10))
		}

		found, err := s.changes.Get(ctx, id)
		if err != nil {
			return "", err
		}

		return found.Number + " " + found.Title, nil
	}

	return "", apperror.InvalidFields("object")
}
