package service

import (
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/i18n"
)

// demoText is one thing the demo writes: its key in the dictionaries, under
// demo., and its English, which stands here.
type demoText struct{ key, english string }

// in is the text in the translator's language.
func (d demoText) in(t i18n.Translator) string { return t.Text(d.key, d.english) }

// demoOrganisation is one of the demo's customers and the people who call on
// its behalf. A name is a name, the same in every language, and every address
// is under .example, which is reserved for exactly this.
type demoOrganisation struct {
	name, number, domain string
	contacts             []string
}

var demoOrganisations = []demoOrganisation{
	{name: "Brightwater Bakery", number: "1001", domain: "brightwater.example",
		contacts: []string{"Anna Becker", "Jonas Weber"}},
	{name: "Harbourline Logistics", number: "1002", domain: "harbourline.example",
		contacts: []string{"Lea Hoffmann", "Tom Fischer"}},
	{name: "Fernhill Architects", number: "1003", domain: "fernhill.example",
		contacts: []string{"Mia Schulz", "Paul Wagner"}},
}

// demoLevel is the service level the demo's queue starts its tickets with: a
// first answer within four hours, the next within a day, and a solution within
// three, counted around the clock while the installation names no calendar.
var demoLevel = struct {
	name, description demoText
	first, update     int
	solution          int
}{
	name:        demoText{"demo.level.name", "Standard support"},
	description: demoText{"demo.level.description", "What the service desk promises every customer"},
	first:       4 * 60, update: 24 * 60, solution: 3 * 24 * 60,
}

// demoCategory is one of the knowledge base's categories.
type demoCategory struct{ name, description demoText }

var demoCategories = []demoCategory{
	{demoText{"demo.faq.hardware.name", "Hardware"},
		demoText{"demo.faq.hardware.description", "Printers, screens and everything else on a desk"}},
	{demoText{"demo.faq.access.name", "Accounts and access"},
		demoText{"demo.faq.access.description", "Signing in, passwords and the VPN"}},
}

// demoArticle is one article of the knowledge base, in the category it names
// by its place in demoCategories.
type demoArticle struct {
	category              int
	visibility            model.FAQVisibility
	title, body, keywords demoText
}

var demoArticles = []demoArticle{
	{0, model.VisibilityExternal,
		demoText{"demo.faq.blankpages.title", "The printer prints blank pages"},
		demoText{"demo.faq.blankpages.body", "Most often the toner is empty or the protective strip of a new " +
			"cartridge is still in place. Open the front cover, take the cartridge out and pull the strip off, then " +
			"print a test page from the printer's menu. If the page is still blank, open a ticket and say where the " +
			"printer stands."},
		demoText{"demo.faq.blankpages.keywords", "printer, toner, blank"}},
	{1, model.VisibilityPublic,
		demoText{"demo.faq.password.title", "How do I reset my password?"},
		demoText{"demo.faq.password.body", "Call the service desk or write to it from your work address. We " +
			"confirm who you are and give you a password for one sign-in; choose your own straight after it."},
		demoText{"demo.faq.password.keywords", "password, sign-in"}},
	{1, model.VisibilityExternal,
		demoText{"demo.faq.vpn.title", "Connecting to the VPN from home"},
		demoText{"demo.faq.vpn.body", "Start the VPN client, choose the profile named after your company and " +
			"sign in with your usual account. If the connection drops every few minutes, try a cable instead of the " +
			"Wi-Fi or move closer to the router, and tell us if it keeps happening."},
		demoText{"demo.faq.vpn.keywords", "VPN, home office"}},
	{0, model.VisibilityInternal,
		demoText{"demo.faq.starter.title", "Equipment for a new employee"},
		demoText{"demo.faq.starter.body", "Order the laptop two weeks ahead, set it up with the standard image, " +
			"create the account and the VPN profile, and hand everything over on the first morning together with a " +
			"password for one sign-in."},
		demoText{"demo.faq.starter.keywords", "laptop, onboarding"}},
}

// demoTemplate is one answer template, offered in every queue.
type demoTemplate struct{ name, subject, body demoText }

var demoTemplates = []demoTemplate{
	{demoText{"demo.template.received.name", "Acknowledge receipt"},
		demoText{"demo.template.received.subject", "<KIX_TICKET_Title>"},
		demoText{"demo.template.received.body", "Hello <KIX_TICKET_Contact>,\n\nthank you for your message. We " +
			"have recorded it as ticket <KIX_TICKET_Number> and will get back to you as soon as we know more.\n\n" +
			"Kind regards\n<KIX_TICKET_Owner>"}},
	{demoText{"demo.template.details.name", "Ask for details"},
		demoText{"demo.template.details.subject", "A question about <KIX_TICKET_Title>"},
		demoText{"demo.template.details.body", "Hello <KIX_TICKET_Contact>,\n\nto help you further we need a " +
			"few details: since when does it happen, on which device, and is there an error message? A screenshot " +
			"helps as well.\n\nKind regards\n<KIX_TICKET_Owner>"}},
}

// demoStep is what happened to a demo ticket a while after it was opened: an
// article written, the state it was put in, or both. A step with no body
// writes no article; one putting the ticket into a waiting state says how long
// after the demo runs the wait ends.
type demoStep struct {
	ago           time.Duration
	channel       model.Channel
	visible       bool
	subject, body demoText
	minutes       int
	state         string
	waitFor       time.Duration
}

// demoTicket is one ticket of the demo: what it is about and for whom, how long
// before the demo it was opened and with what words - a call taken down, which
// the customer reads in the portal as the ticket's first article - and what
// happened to it since. Its values are the shipped ones, by the names the code
// finds them by.
type demoTicket struct {
	title, opening demoText
	kind, priority string
	state          string
	contact        string
	opened         time.Duration
	minutes        int
	owned, watched bool
	steps          []demoStep

	// about is the key of the demo item the ticket concerns, linked to it.
	about string
}

var demoTickets = []demoTicket{
	{title: demoText{"demo.ticket.printer.title", "The printer on the second floor prints blank pages"},
		opening: demoText{"demo.ticket.printer.opening", "Anna Becker called: since this morning the printer on " +
			"the second floor prints every page blank. A new toner cartridge went in yesterday."},
		kind: "incident", priority: model.PriorityNormal, state: model.StateOpen, contact: "Anna Becker",
		opened: 28 * time.Hour, minutes: 5, owned: true,
		steps: []demoStep{
			{ago: 27*time.Hour + 30*time.Minute, channel: model.ChannelNote, minutes: 10,
				subject: demoText{"demo.ticket.printer.note.subject", "Probably the cartridge"},
				body: demoText{"demo.ticket.printer.note", "Most likely the protective strip of the new cartridge. " +
					"Asked the caretaker to have a look on his round."}},
			{ago: 27 * time.Hour, channel: model.ChannelNote, visible: true, minutes: 15,
				subject: demoText{"demo.ticket.printer.answer.subject", "Your printer"},
				body: demoText{"demo.ticket.printer.answer", "Hello Anna, the new cartridge probably still has " +
					"its protective strip. The article \"The printer prints blank pages\" in our knowledge base shows " +
					"how to pull it off. Please let us know whether the printer works again."}},
		}},
	{title: demoText{"demo.ticket.laptop.title", "A laptop for a new colleague"},
		opening: demoText{"demo.ticket.laptop.opening", "Jonas Weber called: a new colleague starts on the first " +
			"of next month and needs a laptop with the usual software and a VPN profile."},
		kind: "service-request", priority: model.PriorityNormal, state: model.StateNew, contact: "Jonas Weber",
		opened: 3 * time.Hour, minutes: 5},
	{title: demoText{"demo.ticket.vpn.title", "The VPN connection drops every few minutes"},
		opening: demoText{"demo.ticket.vpn.opening", "Lea Hoffmann reports that her VPN connection from home " +
			"drops every few minutes since yesterday; her colleagues in the office are not affected."},
		kind: "incident", priority: "high", state: model.StateOpen, contact: "Lea Hoffmann",
		opened: 26 * time.Hour, minutes: 5, owned: true,
		steps: []demoStep{
			{ago: 25 * time.Hour, channel: model.ChannelNote, visible: true, minutes: 10,
				subject: demoText{"demo.ticket.vpn.answer.subject", "Your VPN connection"},
				body: demoText{"demo.ticket.vpn.answer", "Hello Lea, could you try a cable instead of the " +
					"Wi-Fi for an hour? If the connection still drops, please tell us when it happened, and we will " +
					"look into the logs."}},
			{ago: 2 * time.Hour, channel: model.ChannelPhone, visible: true, minutes: 20,
				subject: demoText{"demo.ticket.vpn.call.subject", "Called back"},
				body: demoText{"demo.ticket.vpn.call", "Called Lea back: with a cable the connection holds. " +
					"Her Wi-Fi router is being replaced, and she will tell us once the new one is there."},
				state: "pending-reminder", waitFor: 2 * 24 * time.Hour},
		}},
	{title: demoText{"demo.ticket.share.title", "Access to the accounting share"},
		opening: demoText{"demo.ticket.share.opening", "Tom Fischer asks for read and write access to the " +
			"accounting team's shared drive, for himself and a temporary colleague."},
		kind: "service-request", priority: "low", state: model.StateOpen, contact: "Tom Fischer",
		opened: 50 * time.Hour, minutes: 5, owned: true,
		steps: []demoStep{
			{ago: 48 * time.Hour, channel: model.ChannelNote, visible: true, minutes: 5,
				subject: demoText{"demo.ticket.share.answer.subject", "Your access request"},
				body: demoText{"demo.ticket.share.answer", "Hello Tom, access to that share needs the approval " +
					"of the head of accounting. We have asked for it and will set the access up as soon as it arrives."},
				state: "pending-reminder", waitFor: 24 * time.Hour},
		}},
	{title: demoText{"demo.ticket.signature.title", "The new logo in the e-mail signature"},
		opening: demoText{"demo.ticket.signature.opening", "Mia Schulz asks for the new company logo in " +
			"everybody's e-mail signature, at the size of the old one."},
		kind: "service-request", priority: model.PriorityNormal, state: model.StateOpen, contact: "Mia Schulz",
		opened: 98 * time.Hour, minutes: 5, owned: true,
		steps: []demoStep{
			{ago: 95 * time.Hour, channel: model.ChannelNote, minutes: 30,
				subject: demoText{"demo.ticket.signature.note.subject", "Signature template"},
				body: demoText{"demo.ticket.signature.note", "Updated the signature template on the mail server; " +
					"every mailbox has it within the hour."}},
			{ago: 94 * time.Hour, channel: model.ChannelNote, visible: true, minutes: 5,
				subject: demoText{"demo.ticket.signature.answer.subject", "Your e-mail signature"},
				body: demoText{"demo.ticket.signature.answer", "Hello Mia, the new logo is in every signature " +
					"now. Please tell us if anything looks wrong."},
				state: model.StateClosed},
		}},
	{title: demoText{"demo.ticket.monitor.title", "The second monitor flickers"},
		opening: demoText{"demo.ticket.monitor.opening", "Paul Wagner says his second monitor flickers every few " +
			"seconds."},
		kind: "incident", priority: "low", state: model.StateOpen, contact: "Paul Wagner",
		opened: 120 * time.Hour, minutes: 5, owned: true,
		steps: []demoStep{
			{ago: 119 * time.Hour, channel: model.ChannelNote, minutes: 15,
				subject: demoText{"demo.ticket.monitor.note.subject", "Cable swapped"},
				body: demoText{"demo.ticket.monitor.note", "Swapped the display cable for a new one; the flicker is " +
					"gone."}},
			{ago: 118 * time.Hour, channel: model.ChannelNote, visible: true, minutes: 5,
				subject: demoText{"demo.ticket.monitor.answer.subject", "Your monitor"},
				body: demoText{"demo.ticket.monitor.answer", "Hello Paul, the cable was the cause and has been " +
					"replaced. We are closing this ticket; just answer it if the flicker comes back."},
				state: model.StateClosed},
		}},
	{title: demoText{"demo.ticket.certificate.title", "The customer portal warns of an expired certificate"},
		opening: demoText{"demo.ticket.certificate.opening", "Tom Fischer reports that Harbourline's customer " +
			"portal shows a warning about an expired certificate since this morning, and customers are calling."},
		kind: "incident", priority: "very-high", state: model.StateOpen, contact: "Tom Fischer",
		opened: 6 * time.Hour, minutes: 5, owned: true, watched: true, about: "server"},
	{title: demoText{"demo.ticket.toner.title", "Toner for the meeting room printer"},
		opening: demoText{"demo.ticket.toner.opening", "Anna Becker asks for two black toner cartridges for the " +
			"printer in the meeting room."},
		kind: "service-request", priority: "very-low", state: model.StateNew, contact: "Anna Becker",
		opened: 40 * time.Minute, minutes: 5},
}

// demoItem is one of the demo's configuration items: its class, its name, and
// what it holds by the keys of where each value stands. Everything else its
// class's definition asks for occurs as often as it has to, empty. A value
// naming a record says which by a mark the seed reads: @contact: and
// @organisation: a demo customer by name, @item: an item above it in the table,
// @list: a value of the list the attribute picks from, and @day: a day that many
// days from the demo's.
type demoItem struct {
	key, class string
	name       demoText
	values     map[string]string
}

var demoItems = []demoItem{
	{key: "shop", class: "location", name: demoText{"demo.item.shop.name", "Brightwater Bakery, main shop"},
		values: map[string]string{
			"SectionAddress::Street": "Mill Lane 4", "SectionAddress::City": "Millbrook",
			"SectionAddress::ZIP": "10115",
		}},
	{key: "laptop", class: "computer", name: demoText{"demo.item.laptop.name", "Anna Becker's laptop"},
		values: map[string]string{
			"SectionGeneral::Type": "@list:laptop", "SectionGeneral::Vendor": "Acme",
			"SectionGeneral::Model": "Book 14", "SectionGeneral::Location": "@item:shop",
			"SectionOwner::OwnerOrganisation": "@organisation:Brightwater Bakery",
			"SectionOwner::OwnerContact":      "@contact:Anna Becker",
			"SectionHardware::SerialNumber":   "BW-0042", "SectionSoftware::OperatingSystem": "Linux",
			"SectionWarranty::FirstUsageDate": "@day:-270",
		}},
	{key: "server", class: "computer", name: demoText{"demo.item.server.name", "Harbourline's web server"},
		values: map[string]string{
			"SectionGeneral::Type": "@list:server", "SectionGeneral::Vendor": "Acme",
			"SectionGeneral::Model":           "Rack 2U",
			"SectionOwner::OwnerOrganisation": "@organisation:Harbourline Logistics",
			"SectionHardware::SerialNumber":   "HL-0007", "SectionSoftware::OperatingSystem": "Linux",
			"SectionWarranty::FirstUsageDate": "@day:-700",
		}},
}

// demoWords is every text the demo writes, for the test that holds the
// dictionaries to them.
func demoWords() []demoText {
	words := []demoText{demoLevel.name, demoLevel.description}

	for _, c := range demoCategories {
		words = append(words, c.name, c.description)
	}

	for _, a := range demoArticles {
		words = append(words, a.title, a.body, a.keywords)
	}

	for _, template := range demoTemplates {
		words = append(words, template.name, template.subject, template.body)
	}

	for _, item := range demoItems {
		words = append(words, item.name)
	}

	for _, change := range demoChanges {
		words = append(words, change.title, change.description, change.justification)

		for _, order := range change.orders {
			words = append(words, order.title, order.instruction)

			if order.report.key != "" {
				words = append(words, order.report)
			}
		}
	}

	for _, ticket := range demoTickets {
		words = append(words, ticket.title, ticket.opening)

		for _, step := range ticket.steps {
			if step.body.key != "" {
				words = append(words, step.subject, step.body)
			}
		}
	}

	return words
}
