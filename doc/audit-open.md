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

Two, both raised on 2026-11-17 by the automation.

| | Question |
| --- | --- |
| K4 | A job that has never run is owed only a time of its plan that has just come (`Job.DueOn`); KIX18 counts its last run from nought, so a job set up at three in the afternoon on a plan for eight runs at once, and the first eight o'clock after that again. A job that has run catches up on a time missed while the application was down in both. Keep it - a job runs at the times it says, and not when it was saved - or run a new job at once as KIX18 does? |
| K5 | The automation screen offers what a step can name and what a job's criteria can take from the lists the ticket rights read: the queues, states, priorities and types through *Read tickets* or *Set up queues*, the accounts through *Read accounts*, the contacts and organisations through *Read customers*. A role given *Set up the automation* without them is offered nothing to choose, though the server takes a step naming any record that is there. Keep it - each right reads what it reads, and the administrator holds them all - or let *Set up the automation* read those lists as well? |

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
