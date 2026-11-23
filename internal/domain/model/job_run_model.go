package model

import "time"

// RunTrigger is what started a job's run.
type RunTrigger string

// The ways a run starts: somebody asking for it, a time-based plan's time
// coming, an event-based plan's event happening to a ticket.
const (
	RunManual RunTrigger = "manual"
	RunTime   RunTrigger = "time"
	RunEvent  RunTrigger = "event"
)

// JobRun is what one run of a job did: how many tickets it found, on how many
// every step was taken and on how many one was refused, and a line for each
// refusal and each macro it passed over - KIX18's job run and its automation
// log.
type JobRun struct {
	ID         uint
	JobID      uint
	Trigger    RunTrigger
	StartedAt  time.Time
	FinishedAt time.Time
	Tickets    int
	Changed    int
	Failed     int
	Lines      []RunLine
}

// RunLine is one thing a run says about a ticket and a step: the refusal's code,
// the values its sentence is filled with and its English, for a screen to say
// in its reader's words - or a macro passed over. TicketID and MacroID are ids
// without a key, because the ticket may have been deleted by the run itself
// and the macro since.
type RunLine struct {
	TicketID *uint
	MacroID  *uint
	Step     int
	Code     string
	Values   []any
	Message  string
	At       time.Time
}

// MaxRunTickets bounds the tickets one run takes, the oldest first. Chosen
// rather than derived: a job meant for more tickets at once than this takes the
// rest on its next run, and a run that took every ticket of a large
// installation would hold the scheduler for minutes.
const MaxRunTickets = 500

// RunsKeptFor is how long a run's record is kept before the daily cleanup
// removes it. Chosen rather than derived: long enough to look back over a
// month of what the automation did.
const RunsKeptFor = 30 * 24 * time.Hour
