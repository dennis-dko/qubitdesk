# What the audits have left open

The audits' leftovers that have to outlive them: decisions put to the
maintainer - the code is right and the taste is arguable, a fix that buys
little, or one of the critical decisions `CLAUDE.md` names - and measurements
that reading cannot make. They used to live in `AUDIT_REPORT.md`, a git-ignored
scratchpad that is now started fresh for every audit, so they live here.

An item is added in the commit of the read that raises it, and removed in the
commit that settles it, with the decision in that commit's message.

Carried over on 2026-10-09 from the report of the audits since late September.
Left out as settled by then: D-new-9 (a restart and a cold start give one answer
since the restart hands on the environment the process started with), D-new-26
(the installer's prefill behind the token, #284), D6 (MySQL refuses to start in
another zone, #317), D1 and D2 (no such comments and no such splits remain).
What is below was raised and is not recorded as decided anywhere.

## Decisions waiting on the maintainer

Two, raised on 2026-11-16 by the ticket housekeeping, each a place where it
counts more simply than KIX18 does and agrees with it on what ships. Neither is
cheaper in a way anybody could measure, so the question is only which answer
is right.

| | Question |
| --- | --- |
| K1 | `Ticket::StateAfterPending` names one state that every pending-auto state moves on to (`model.ConfigTicketStateAfterPending`, `closed` by default). KIX18 keeps a map from each pending state to a next state of its own, and leaves a ticket in a state the map does not name where it is. With the one pending-auto state that ships the two agree; they part once an installation adds a second - "pending auto reopen", say, whose tickets would be closed instead of opened. Keep the one state, take KIX18's map, or - recommended - make the next state a property of each pending-auto state, by id and chosen in the state's form: as expressive as the map, and untouched by a rename, which leaves KIX18's map naming a state nothing is called any more? |
| K2 | A queue's unlock time counts around the clock (`freeLocks` in the ticket service); KIX18 counts it in working time of its main calendar, so a lock taken on Friday evening outlasts the weekend there and not here. This installation has calendars but no main one. Leave it around the clock, or - recommended - count in a calendar the system configuration names as the installation's own, as KIX18 does with its main calendar, around the clock while none is named? The same calendar would then be the one a service level without a calendar of its own counts in, which is around the clock today. |

## Measurements reading cannot make

Each needs real infrastructure, and none is a change to make on a guess.

- Whether an account can come to depend on a directory entry behind a referral.
  A run collects referrals and does not follow them; `task test:ldap` runs
  OpenLDAP, and this needs a real Active Directory.
- Whether a desktop browser's history keeps the `/?token=` address the
  installer's page was opened with. The page takes it out of the address bar at
  once; headless Chrome records no history, so this needs a real browser.
- The wall time of a first directory run over thousands of accounts on SQLite,
  and on the Pi: one commit per arrival.
