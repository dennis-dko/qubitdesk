package service

import (
	"context"
	"fmt"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/i18n"
)

// demoChangeServices are what the demo's changes are written through, the
// catalog their scales, states and types are found in by name, and what links
// a change to the ticket and the item it concerns.
type demoChangeServices struct {
	changes    *ChangeApplicationService
	workOrders *WorkOrderApplicationService
	catalog    repository.GeneralCatalogRepository
	links      *LinkApplicationService
}

// demoChange is one change of the demo: what it is and why, how it is ranked,
// the states it is moved on to once its work orders are in, the work orders it
// is carried out in, and the ticket and the item it concerns - the ticket by
// the key of its title, the item by the key the demo gave it.
type demoChange struct {
	title, description, justification demoText
	category, impact                  string
	moves                             []string
	orders                            []demoWorkOrder
	ticket, item                      string
}

// demoWorkOrder is one work order of a demo change: what is to be done and of
// which type, when it is planned - counted from the start of the day after the
// demo runs, because a plan lies ahead - the states it is moved on to, and
// what its report says once it is done.
type demoWorkOrder struct {
	title, instruction, report demoText
	kind                       string
	from, length               time.Duration
	moves                      []string
}

// demoChanges are the changes the demo writes: one under way about the
// certificate the portal warns of, and one still requested about the VPN.
var demoChanges = []demoChange{
	{
		title: demoText{"demo.change.certificate.title", "Renew the certificate of Harbourline's web server"},
		description: demoText{"demo.change.certificate.description",
			"The certificate the customer portal is served with runs out. A new one is ordered, installed on the " +
				"web server and checked from outside."},
		justification: demoText{"demo.change.certificate.justification",
			"Customers are warned before they sign in, and some browsers refuse the portal once it has run out."},
		category: "3-normal", impact: "4-high", moves: []string{"pending-approval", "approved", "in-progress"},
		ticket: "demo.ticket.certificate.title", item: "server",
		orders: []demoWorkOrder{
			{title: demoText{"demo.workorder.approve.title", "Approve the renewal"}, kind: "approval",
				instruction: demoText{"demo.workorder.approve.instruction",
					"Confirm the names on the certificate and that it may be installed tomorrow morning."},
				report: demoText{"demo.workorder.approve.report", "Approved; the names stay as they are."},
				moves:  []string{"accepted", "ready", "in-progress", "closed"}},
			{title: demoText{"demo.workorder.install.title", "Install the new certificate"}, kind: "workorder",
				instruction: demoText{"demo.workorder.install.instruction",
					"Install the certificate with its chain on the web server and reload it before opening time."},
				from: 7 * time.Hour, length: time.Hour, moves: []string{"accepted"}},
			{title: demoText{"demo.workorder.check.title", "Check the portal from outside"}, kind: "pir",
				instruction: demoText{"demo.workorder.check.instruction",
					"Open the customer portal from outside the network and make sure no browser warns any more."},
				from: 8 * time.Hour, length: 30 * time.Minute},
		},
	},
	{
		title: demoText{"demo.change.vpn.title", "Replace the VPN gateway"},
		description: demoText{"demo.change.vpn.description",
			"The VPN gateway drops connections once more than a few are open, and is replaced by a current model."},
		justification: demoText{"demo.change.vpn.justification",
			"Colleagues working from home lose their connection every few minutes."},
		category: "4-high", impact: "3-normal", ticket: "demo.ticket.vpn.title",
		orders: []demoWorkOrder{
			{title: demoText{"demo.workorder.offers.title", "Compare three offers"}, kind: "decision",
				instruction: demoText{"demo.workorder.offers.instruction",
					"Ask three suppliers for a gateway that holds two hundred connections, and compare their prices " +
						"and their support."},
				from: 2*24*time.Hour + 9*time.Hour, length: 3 * time.Hour},
		},
	},
}

// seedChanges adds the changes: each with its work orders, moved on as far as
// it has come, and linked to the ticket and the item it concerns. The built-in
// administrator manages and builds them and is given their work orders,
// because the demo makes no other account.
func (s *DemoService) seedChanges(
	ctx context.Context,
	caller *Principal,
	t i18n.Translator,
	tickets map[string]uint,
	items map[string]uint,
	summary *DemoSummary,
) error {
	if s.changes == nil {
		return nil
	}

	named := func(class, name string) (uint, error) { return catalogNamed(ctx, s.changes.catalog, class, name) }
	now := s.now()
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())

	for _, change := range demoChanges {
		category, err := named(model.CatalogChangeCategory, change.category)
		if err != nil {
			return err
		}

		impact, err := named(model.CatalogChangeImpact, change.impact)
		if err != nil {
			return err
		}

		made, err := s.changes.changes.Create(ctx, caller, ChangeCommand{
			Title: ptr(change.title.in(t)), Description: ptr(change.description.in(t)),
			Justification: ptr(change.justification.in(t)), CategoryID: &category, ImpactID: &impact,
			ManagerID: &caller.User.ID, BuilderID: &caller.User.ID,
		})
		if err != nil {
			return fmt.Errorf("adding the change %s: %w", change.title.key, err)
		}

		summary.Changes++

		for _, order := range change.orders {
			if err := s.seedWorkOrder(ctx, caller, t, made.ID, order, tomorrow); err != nil {
				return fmt.Errorf("adding a work order of the change %s: %w", change.title.key, err)
			}

			summary.WorkOrders++
		}

		for _, move := range change.moves {
			state, err := named(model.CatalogChangeState, move)
			if err != nil {
				return err
			}

			if _, err := s.changes.changes.Update(ctx, caller, made.ID, ChangeCommand{StateID: &state}); err != nil {
				return fmt.Errorf("moving the change %s on to %s: %w", change.title.key, move, err)
			}
		}

		for kind, id := range map[model.LinkObject]uint{
			model.LinkTicket: tickets[change.ticket], model.LinkConfigItem: items[change.item],
		} {
			if id == 0 {
				continue
			}

			if _, err := s.changes.links.Create(ctx, caller, LinkCommand{SourceObject: model.LinkChange,
				SourceID: made.ID, TargetObject: kind, TargetID: id, Type: model.LinkNormal}); err != nil {
				return fmt.Errorf("linking the change %s: %w", change.title.key, err)
			}
		}
	}

	return nil
}

// seedWorkOrder adds one work order of a demo change and moves it on, the last
// move carrying its report where it has one.
func (s *DemoService) seedWorkOrder(
	ctx context.Context,
	caller *Principal,
	t i18n.Translator,
	changeID uint,
	order demoWorkOrder,
	tomorrow time.Time,
) error {
	kind, err := catalogNamed(ctx, s.changes.catalog, model.CatalogWorkOrderType, order.kind)
	if err != nil {
		return err
	}

	add := WorkOrderCommand{Title: ptr(order.title.in(t)), Instruction: ptr(order.instruction.in(t)), TypeID: &kind,
		AgentID: &caller.User.ID}

	if order.length > 0 {
		add.PlannedStartAt = ptr(tomorrow.Add(order.from))
		add.PlannedEndAt = ptr(tomorrow.Add(order.from + order.length))
		add.PlannedMinutes = ptr(int(order.length.Minutes()))
	}

	made, err := s.changes.workOrders.Create(ctx, caller, changeID, add)
	if err != nil {
		return err
	}

	for i, move := range order.moves {
		state, err := catalogNamed(ctx, s.changes.catalog, model.CatalogWorkOrderState, move)
		if err != nil {
			return err
		}

		step := WorkOrderCommand{StateID: &state}
		if i == len(order.moves)-1 && order.report.key != "" {
			step.Report = ptr(order.report.in(t))
		}

		if _, err := s.changes.workOrders.Update(ctx, caller, made.ID, step); err != nil {
			return fmt.Errorf("moving %s on to %s: %w", order.title.key, move, err)
		}
	}

	return nil
}
