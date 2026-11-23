package model

// Macro is a named run of steps taken on a ticket, in order: what a job does to
// every ticket it finds, as KIX18's macros are. A retired macro is skipped by
// the jobs that name it rather than refused, the way a retired queue is kept
// where it already stands.
type Macro struct {
	ID          uint
	Name        string
	Description string
	Valid       bool
	Actions     []MacroAction
}

// MacroAction is one step of a macro: what it does, and the values it does it
// with, by the parameter names its type defines.
type MacroAction struct {
	Type       MacroActionType
	Parameters map[string]string
}

// MacroActionType is what a step does to a ticket, named as KIX18 names its
// ticket macro actions so that somebody coming from KIX finds each under the
// word they know - TeamSet is the queue, which KIX18 calls a team.
type MacroActionType string

// The steps a macro can take on a ticket. Each goes through the rules a
// person's change goes through - a value no longer offered is not chosen, a
// state that waits needs its moment - and is written into the history as the
// application's.
const (
	ActionStateSet        MacroActionType = "StateSet"
	ActionPrioritySet     MacroActionType = "PrioritySet"
	ActionTypeSet         MacroActionType = "TypeSet"
	ActionTeamSet         MacroActionType = "TeamSet"
	ActionOwnerSet        MacroActionType = "OwnerSet"
	ActionResponsibleSet  MacroActionType = "ResponsibleSet"
	ActionLockSet         MacroActionType = "LockSet"
	ActionTitleSet        MacroActionType = "TitleSet"
	ActionContactSet      MacroActionType = "ContactSet"
	ActionOrganisationSet MacroActionType = "OrganisationSet"
	ActionArticleCreate   MacroActionType = "ArticleCreate"
	ActionTicketDelete    MacroActionType = "TicketDelete"
)

// MacroParameterKind is what a parameter of a step holds, which decides how it
// is checked and how a form asks for it: a record by its id, chosen from a
// list, or a value typed in.
type MacroParameterKind string

// The kinds of value a step's parameter holds.
const (
	ParameterState        MacroParameterKind = "state"
	ParameterPriority     MacroParameterKind = "priority"
	ParameterType         MacroParameterKind = "type"
	ParameterQueue        MacroParameterKind = "queue"
	ParameterUser         MacroParameterKind = "user"
	ParameterContact      MacroParameterKind = "contact"
	ParameterOrganisation MacroParameterKind = "organisation"
	ParameterChannel      MacroParameterKind = "channel"
	ParameterText         MacroParameterKind = "text"
	ParameterLongText     MacroParameterKind = "longText"
	ParameterSwitch       MacroParameterKind = "switch"
	ParameterMinutes      MacroParameterKind = "minutes"
)

// MacroParameter is one value a step reads. A record is named by its id rather
// than by its name, as KIX18 names it: a rename leaves an id standing, where it
// leaves a name pointing at nothing. Max bounds the length of a text and the
// number of minutes, and is the bound the same value has where a person enters
// it.
type MacroParameter struct {
	Name     string
	Kind     MacroParameterKind
	Required bool
	Max      int
}

// MacroActionDefinition is a step a macro can take, with the parameters it
// reads in the order a form asks for them.
type MacroActionDefinition struct {
	Type       MacroActionType
	Parameters []MacroParameter
}

// MacroActionDefinitions are every step there is. A person, a contact or an
// organisation left out clears the ticket's, which is the one way a step takes
// something away; the minutes a state is set with are how far ahead a waiting
// state waits from the moment the step runs, wanted for such a state and
// refused for any other.
func MacroActionDefinitions() []MacroActionDefinition {
	return []MacroActionDefinition{
		{ActionStateSet, []MacroParameter{
			{Name: "stateId", Kind: ParameterState, Required: true},
			{Name: "pendingMinutes", Kind: ParameterMinutes, Max: MaxServiceMinutes},
		}},
		{ActionPrioritySet, []MacroParameter{{Name: "priorityId", Kind: ParameterPriority, Required: true}}},
		{ActionTypeSet, []MacroParameter{{Name: "typeId", Kind: ParameterType, Required: true}}},
		{ActionTeamSet, []MacroParameter{{Name: "queueId", Kind: ParameterQueue, Required: true}}},
		{ActionOwnerSet, []MacroParameter{{Name: "ownerId", Kind: ParameterUser}}},
		{ActionResponsibleSet, []MacroParameter{{Name: "responsibleId", Kind: ParameterUser}}},
		{ActionLockSet, []MacroParameter{{Name: "locked", Kind: ParameterSwitch, Required: true}}},
		{ActionTitleSet, []MacroParameter{
			{Name: "title", Kind: ParameterText, Required: true, Max: MaxTicketTitleLength},
		}},
		{ActionContactSet, []MacroParameter{{Name: "contactId", Kind: ParameterContact}}},
		{ActionOrganisationSet, []MacroParameter{{Name: "organisationId", Kind: ParameterOrganisation}}},
		{ActionArticleCreate, []MacroParameter{
			{Name: "channel", Kind: ParameterChannel, Required: true},
			{Name: "subject", Kind: ParameterText, Max: MaxTicketTitleLength},
			{Name: "body", Kind: ParameterLongText, Required: true, Max: MaxMacroTextLength},
			{Name: "customerVisible", Kind: ParameterSwitch},
			{Name: "minutes", Kind: ParameterMinutes, Max: MaxArticleMinutes},
		}},
		{ActionTicketDelete, nil},
	}
}

// MacroActionDefinitionOf is the definition of a step, and whether there is one.
func MacroActionDefinitionOf(kind MacroActionType) (MacroActionDefinition, bool) {
	for _, definition := range MacroActionDefinitions() {
		if definition.Type == kind {
			return definition, true
		}
	}

	return MacroActionDefinition{}, false
}

// MaxMacroActions bounds the steps of one macro. Chosen rather than derived: a
// macro of more steps than a form can show at once is several macros.
const MaxMacroActions = 50

// MaxMacroTextLength bounds what a step writes into a ticket. Chosen rather than
// derived: what a job writes into every ticket it finds is a paragraph, not a
// document, and a longer one is pasted by mistake.
const MaxMacroTextLength = 10000
