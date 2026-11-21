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

Five: four raised on 2026-11-17 by the automation, the notifications and the mail to come, and one on 2026-11-21 by the export.

| | Question |
| --- | --- |
| K4 | A job that has never run is owed only a time of its plan that has just come (`Job.DueOn`); KIX18 counts its last run from nought, so a job set up at three in the afternoon on a plan for eight runs at once, and the first eight o'clock after that again. A job that has run catches up on a time missed while the application was down in both. Keep it - a job runs at the times it says, and not when it was saved - or run a new job at once as KIX18 does? |
| K5 | The automation screen offers what a step can name and what a job's criteria can take from the lists the ticket rights read: the queues, states, priorities and types through *Read tickets* or *Set up queues*, the accounts through *Read accounts*, the contacts and organisations through *Read customers*. A role given *Set up the automation* without them is offered nothing to choose, though the server takes a step naming any record that is there. Keep it - each right reads what it reads, and the administrator holds them all - or let *Set up the automation* read those lists as well? |
| K6 | The notifications are set up with *Set up queues*, whose description already covers how tickets are sorted and answered, the answer templates and the mailboxes to come. KIX18 files them under Communication, a group of its own beside the ticket setup that its mail accounts and templates belong to as well. Keep them with the queues, or give the communication a right - and later a screen - of its own? |
| K7 | The mail (P10) fetches from mailboxes and sends through a mail server. Sending needs nothing beyond the standard library (`net/smtp`, `net/mail`, `mime/multipart`); fetching needs IMAP or POP3, which the standard library does not speak. Write the client for the part KIX18 uses - log in, list what is unseen, fetch it, mark it seen - or take a library such as github.com/emersion/go-imap v2, a dependency this repository would carry from then on? Until it is decided the knowledge base (P12) and the customer portal (P11) go first. |
| K8 | An account's export hands over everything it wrote - the tickets it opened, its history lines, the changes and items it made - except its entries in the administration log. `CLAUDE.md` keeps that log on neither list on purpose, and its reason covers the erasure: what an account did to how the installation runs must outlive the account. It does not say why the export leaves those entries out, and they are data about the account while it exists. Keep them out, or hand them over as where and when - the moment and the action, without the target or the detail, which name other accounts? |

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
