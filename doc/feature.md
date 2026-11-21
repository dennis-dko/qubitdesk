# Features

What the application does, and the rules it keeps while doing it.

The ticket system itself is being built: the queues, the tickets, the customers
they are for, service levels, the automation, the knowledge base and the
customer portal are here, and mail comes next. What follows is what an
installation already has.

## What is included

| Area | Implementation |
| --- | --- |
| Storage | SQLite (pure Go, no cgo), PostgreSQL or MySQL - chosen in the installer, never defaulted |
| Concurrency | SQLite runs in write-ahead logging, so a save and a page load do not refuse each other |
| Schema | GoFr migrations, applied automatically at start-up |
| API | REST under `/api/v1`, documented at `/api-docs` |
| Web interface | Embedded via `go:embed`, vanilla JS with no build step |
| Screens in tabs | *My account* is sorted into working time, sign-in, apps and calendar, and privacy and help; *Settings* into general, sign-in, operation and logs. A tab with nothing on it for an account is not offered, a screen comes back on the tab it was left on, and whatever points at a card - a banner's link, the guided tour - opens its tab |
| Access control | RBAC with roles administered at run time; bcrypt password hashes |
| Sign-in | Session cookies, optional passkeys and TOTP two-factor per user, a browser's Kerberos ticket where a keytab and a directory are configured, and an OpenID Connect provider - Keycloak, Authentik, Entra ID, Google - for the accounts their owners linked to it |
| Live log | The process log, filterable and searchable, for whoever administers |
| Administration log | Who created, changed or deleted an account, a role, a ticket state, priority or type, saved a setting, switched maintenance mode, made or downloaded a backup or restarted the application, kept across restarts and readable under *Settings* by whoever administers |
| Backups | The whole installation in one file sealed with `BACKUP_PASSPHRASE`, made under *Settings* now and downloaded, or on a schedule and kept on the server, the oldest going past the number set; restored through the installer into an empty database of any of the three dialects |
| A way back in | `go-ticket-system-web recover` on the server gives the built-in administrator a one-time password and takes its second factor off; an administrator removes a lost second factor from anybody else's account |
| Tickets | Under *Tickets*, the tickets of the queues the reader's role reaches, what is still to do first, narrowed by queue, to the reader's own and by words in the title or number. A ticket is opened with a title, its queue and what was said, and starts new, normal and unclassified unless told otherwise, owed what its queue's service level promises - the moments it is owed shown with it, and marked overdue in the list once one has passed unkept; opened from the list by its number it shows what was written in it, takes a note or a call with the minutes worked and the files that come with it - at most 20 MB each, saved again under the names they came with - is corrected in the form that opened it, locked - which makes whoever locks a ticket nobody owns its owner, which a move to another queue releases unless the installation says otherwise, and which frees itself once it has run as long as the queue allows, counted from the lock or from the last answer the customer saw in the installation's working hours where it names a calendar of its own - set to wait for a moment, after which a ticket in a state that moves on by itself goes to the state that state names - closed for the one that ships - and loses its lock where that closes it, watched, and tells what happened to it by the names things had then. A ticket of a queue the role only reads is shown and not offered for change, and one of a queue the role does not reach is not there at all |
| Customers | Under *Customers*, the organisations tickets come from - each with its number where it has one, its address and its website, shown as a link - and the people at them, found by name or address and narrowed to one organisation: seen by whoever holds *See customers* and changed by whoever holds *Create and change customers*, whichever queue a ticket for them landed in. A ticket is for a contact, found by typing in the form that opens it, and for the contact's organisation unless corrected, and it names both. An organisation contacts or tickets belong to is retired rather than deleted; deleting a contact tickets are for asks a second time, saying how many, and keeps the tickets, for nobody. Whoever changes customers gives a contact access to the customer portal from the contact's row: an account under the contact's name and address, which reaches the portal and nothing else, with a password for one sign-in - a new one is given the same way and ends the sessions the account had open. The account follows the contact's name and address and goes with the contact, or when the access is taken away; whether the contact sees their organisation's tickets in the portal as well as their own is a switch on the contact, off unless somebody turns it on. An account a contact signs in with is given no role that administers the installation, whichever way it would get there |
| Ticket setup | The queues tickets are sorted to - a tree, each with the roles that reach it to read its tickets or to work in it, and nobody else, and saying whether customers open tickets in it from the portal, as the shipped Service Desk does - and the states, priorities and types tickets are sorted by, a waiting state with the state it moves a ticket on to, the service levels a ticket is owed - minutes until the first answer, between answers - counted from the last answer the customer saw, or from the first of what they wrote since, and owed by nobody while a ticket waits or is finished - and until the solution, counted in the level's calendar, in the installation's where it names none, or around the clock - the calendars their minutes are counted in - the hours of each weekday with their breaks, less the public holidays of a place and free days of its own, once or every year - and the state types a queue's unlock time frees a lock in, new and open always among them, and the answer templates - KIX18's text modules, each offered in its queue or in every queue and filled for the ticket an article is started from it in - administered under *Ticket setup* by whoever holds *Set up queues*. Shipped with KIX18's values - seven states, one of each type, five priorities and three types - named in the reader's language; an installation adds its own, and retires a value tickets carry rather than deleting it. What the application moves tickets into by itself - the states new, open, closed and merged, the priority normal, the type unclassified - stays as it shipped |
| Customer portal | For a contact given access, under *My tickets*: the tickets opened for them - and their organisation's where the contact is said to see them - those still being worked on first and the finished ones apart, each opened to read what the service desk answered, with its files, and to answer it with files of their own; and a ticket opened there, in the queue the portal offers or, where it offers several, the one chosen. An answer opens a waiting ticket again, and a finished one as its queue says - opened again, refused, or a new ticket for the answer. Nothing of how the service desk works a ticket - its queue, its owner, its lock or what it is owed - and no internal note or its files are shown there |
| Knowledge base | Under *Knowledge base*, KIX18's FAQ: articles filed in a tree of categories, searched by the words in their title, keywords or text and by category, each saying who may read it - the staff alone, customers signed in as well, or anybody - and for the staff unless told otherwise. Whoever holds *Write the knowledge base* writes and corrects articles and categories and alone sees an article made invalid; a category holding articles is made invalid rather than deleted. Outside the staff it is read, never what is retired: a customer finds in the portal what is meant for customers and what is public, and anybody finds what is public from the sign-in screen, which offers it only where something is - each searching by words and by category, shown only the categories that hold something they may read |
| General catalog | Under *Assets*, for whoever holds *Set up the general catalog*: KIX18's lists the configuration items will pick from - their classes, the deployment and incident states with what each means to the code, and lists of the installation's own begun by adding their first value. Shipped values are named in the reader's words; a value nobody should pick any more is retired from its row, never deleted |
| Class definitions | Under *Assets*, beside the general catalog: what a configuration item of each class holds, written as KIX18 writes it - sections, texts, dates, a value of the catalog, another item, a contact or an organisation, each with how often it occurs and attributes of its own - and checked whole on the server. A change is kept as a new version beside the ones before it, so an item stays readable the way it was written; the shipped classes start with KIX18's definitions |
| Automation | Under *Automation*, for whoever holds *Set up the automation*: macros - runs of KIX18's ticket steps, from setting the state to deleting the ticket, put together a step at a time and each checked as it is saved - execution plans that start jobs at times of day on the weekdays they name, in the installation's timezone, or when something happens to a ticket, and jobs that run their macros on the tickets their criteria take - by queue, state, priority, type and lock - when a plan says so or when somebody runs one there. A job acts as the application, so every step meets the rules a person's change meets and goes into the ticket's history as the system's; a step it could not take leaves the ticket where it stood and is shown with its reason in the reader's words, and every run is kept for thirty days |
| System configuration | Under *Settings*, how the installation behaves, option by option, each under the name KIX18 gives it and with its default - the word a ticket's number is written after and what stands between them, and whether moving a ticket to another queue releases its lock - changed by whoever holds *Administer the installation* and put back to its default with one press. Only what was changed is kept, so an option nobody touched follows a new default after an update |
| Version | The running build in the footer of every page |
| API access | Personal tokens, scoped by the owner's current role |
| Android app | The installation's own web interface in an app, so everything the browser does in the same design, connected by scanning the code a new token is shown with, which signs it in; released with every version as `go-ticket-system-mobile_<version>.apk`, signed with one key so each release installs over the last |
| Notifications | Set up under *Ticket setup* by whoever holds *Set up queues*, as KIX18 sets up its ticket notifications: the ticket events one is sent on, the tickets it is about, and who is told - the owner, the responsible agent, those watching the ticket, those who chose its queue, those who work in or read it - with a subject and a text per language, KIX18's placeholders filled in each agent's words, once a day at most where it says so. Nobody is told about their own change, the built-in administrator never, and nobody about a ticket they cannot read; a job's change is news like a person's. Each agent finds what they were told under *Tickets*, the unread first to the eye, and reads it by opening the ticket, and under *My account* switches off the ones that let them and chooses the queues they follow; pushing it to a browser is switched on under *Settings*, off as shipped, and each person lets a browser have it under *My account*, where it arrives even with no page open and one that arrives late is shown only while still unread |
| Public holidays | The public holidays of seventeen countries - Austria, Belgium, Czechia, Denmark, Finland, France, Germany, Ireland, Italy, Luxembourg, the Netherlands, Norway, Poland, Portugal, Sweden, the United Kingdom and the United States - worked out in the binary rather than fetched from anywhere. A country is chosen first, Germany standing ready, and a second picker follows where its holidays differ: a German state or a British nation always, and Alsace, Moselle, South Tyrol, the Azores or Madeira where they keep a day or two more than the rest of their country. The installation chooses under *Settings*. A holiday a country moves off a weekend is kept on the weekday it moves to, and said to be. Nothing reads them yet: they are kept for the service hours a service level is measured in |
| Everything about me | Everything the installation keeps about your account, as a ZIP with a JSON file per part - exactly what erasing it would delete or release, without the hashes, seeds and keys - from *My account*, for anybody signed in |
| Transport | Optional HTTPS with automatic Let's Encrypt certificates |
| Installable | A manifest and icons per installation - its own name, and Go-Ticket-System's mark - so the page installs as an app on a telephone or a desktop, with a shortcut to *My account*. Browsers offer this over HTTPS or on localhost only. *My account* shows the installation's address beside the offer, as text and as a QR code, to open it on a telephone by scanning it rather than typing it |
| Offline | The installed application opens without a network from the shell it kept, and says the installation cannot be reached rather than showing a dead page. Nothing the installation answers is ever shown from a copy. Over HTTPS or on localhost only, like installing |
| Operations | Health and liveness endpoints, Prometheus metrics, tracing (all from GoFr), administered under *Settings* |

## Timezones

An administrator sets one **instance-wide** zone under *Settings*; an
individual can override it under *My account*. An empty personal setting means
"follow the instance", which is the normal case — that way changing the
instance zone moves everyone who has not deliberately opted out.

This is not a display detail. The zone decides which **calendar day** a moment
falls on, which is the day a ticket's number is taken from and the day a service
level's hours are counted in. Zones are validated when saved, because a
plausible but wrong name like `Europe/Munich` would otherwise be stored and then
fall back to UTC at every use, with nothing on screen to show it.

The tz database is compiled into the binary (`time/tzdata`), so zones resolve
identically on a scratch or distroless container with no zoneinfo files of its
own.

## Everything about you

Under **My account**, *Everything about me* downloads what this installation
keeps about your account as a ZIP: a JSON file per part - the account, the
tickets you own or are responsible for, the contact you sign in as, the tickets
you watch, your notifications and the queues you are told about, the articles
you wrote and the time you noted on tickets, and the personal tokens, passkeys,
sessions, push subscriptions and provider links you have - each an array of rows
with the columns as they are stored, and `about.json` saying what the archive
is. It is the right of access and of portability (GDPR articles 15 and 20), open
to anybody signed in whatever their role lets them do here, and only ever about
their own account.

It holds exactly what erasing the account would delete or release, read from the
same list of tables, so nothing can be erased that could not have been handed
over first - and with it the articles and the time, which erasing leaves in the
tickets because they are the organisation's record. An article is handed over as
where and when it was written rather than as its text: an answer quotes the
customer it answers, and the export of one person is not the place for another's
words. What it leaves out is what would be a way in for whoever finds the file:
the password's hash, the second factor's seed, the hashes of tokens and
sessions, and a passkey's key material. About those it says that they exist,
when they were made and when they were last used.

## Spreadsheets

The accounts and the roles go out and come back as a real **.xlsx** workbook,
from the tab they belong to: accounts with their role and whether the password
lives in the directory; roles with a column per permission holding yes or no.
Names rather than identifiers, because a column of ids is not something anybody
can fill in by hand.

A column per right rather than one cell listing them, which is what makes the
roles sheet honest. A list in a cell reads well and imports badly: a typo in
`tickets:read, tickets:wrote` is a right silently dropped, and nothing about
it looks wrong until somebody cannot open a screen. A column is a question with
two answers, and a heading naming a right this application does not enforce is
refused by that name — which also catches a file exported from a different
installation. A file may leave rights out; a column somebody deleted is simply
a right the import does not touch.

Two tables deliberately have no sheet. A token's secret exists once, at the
moment it is created, and is not in the database to export. A passkey is bound
to the device holding it and means nothing anywhere else.

The column headings are written in the language the export was asked for, and a file
exported in any of them imports again: the heading row is skipped by position, and
translated values are recognised in every language.

Not comma-separated text. A CSV is what Excel mangles: the separator depends on the
machine's locale, dates are re-interpreted on opening, and a cell containing a
semicolon quietly becomes two columns — none of it recoverable once somebody has
saved over the file.

The import is **two steps, and all or nothing**:

1. *Check the file* reads it and shows every row — which would be written, which
   would not, and why, named by its line in the sheet so you know where to look. It
   writes nothing.
2. *Import* is only offered when every row can be written, and runs in one
   transaction.

A file half-imported leaves nobody able to say which half, or which rows came
from it, which is why a single refused row refuses the file. Every row goes
through the rules the API enforces — by calling them, not by restating them.

Accounts are matched on the mail address and are **changed**, not created: a new
account needs a password, and a password that arrives in a spreadsheet is a password
that gets mailed around. Roles are matched on the name and are both — everything a
role is fits in a row, so a file can describe one that does not exist yet without
carrying anything that has no business being in a spreadsheet. The roles the
application ships with are the exception the role editor already makes: their
rights cannot be changed from a cell any more than from the screen.

Why a row was refused is written in the reader's language, like the headings above it
and the values in it. The reasons are a fixed set, so each one travels as a code with
whatever its sentence names — the row number, the offending word — rather than as a
sentence assembled on the server in English.

## The logo, and where it is drawn

**The logo stands at the left of the bar once somebody has signed in, and
nowhere else.** The browser tab, the sign-in screen and the installed app's icons
show Go-Ticket-System's own mark whatever is uploaded — decided on 2026-11-18, because
those places say which program this is, before anybody has signed in to the
installation that runs it.

A logo is a **PNG or a JPEG**, up to 256 KB. Not an SVG: the bar draws a copy cut
to the part chosen for it, and the cut is made on the server, from pixels — a
drawing would be stored and never cut.

**It is converted when it is saved**, into the size the bar draws it: kept at
656×192 for the 328×96 the bar gives it, twice the drawn size because a screen at
twice the density draws twice as many pixels for the same box. Derived once and
stored, so a page load reads it rather than making it, and a restart has nothing
to redo. It also shrinks what every visitor downloads: the branding answer carries
the copy rather than the original. A 440×80 copy is made beside it as well,
because the API has always sent one as `logoHeader`.

**Which part of the logo the bar uses is yours to choose.** Press the preview on
the appearance screen and drag: any part, in any shape, from any corner. The
selection opens on the shape of the bar's room, because that is the one selection
nothing has to be done to, but nothing holds it there — and a part that is not
that shape is fitted into the room rather than stretched to fill it. A wordmark
with a symbol at one end can read better as the symbol alone. Nothing has to be
chosen: a logo starts as all of itself, which is what most installations will
leave it as.

The choice is remembered per logo. Uploading a different logo forgets it, because
a selection made against one picture means nothing against another.

**Until a logo is configured, the bar shows a chip with Go-Ticket-System's G.** That slot
belongs to whoever runs the installation, and filling it with our mark would make
an unbranded installation look branded by somebody else, so the chip only stands
in for a logo. The application's own mark — an hourglass on a blue tile, its
lower half dimmer, which the ticket system took with it from the time recording
and which stands in until it has a mark of its own — has its own places: the
browser tab, the sign-in card, the installer, the installed app's icons, the
`.exe`, the Android app, and the button beside the title that leads to the
welcome screen. It is drawn (`internal/interface/web/asset/favicon.svg`) rather
than a font character.
`go run ./build/icon > build/icon.ico` and
`go run ./build/icon -favicon > internal/interface/web/asset/favicon.ico` redraw
the two icon files from the same geometry whenever it changes, and tests fail
until they have.

**Every installation is called Go-Ticket-System, and the title is added to that
name.** It follows after a bar, as in *Go-Ticket-System | htp.net*. The full name
appears in the header, on the sign-in card, in an authenticator app and on a
passkey prompt. With nothing written, the name is *Go-Ticket-System* alone.
`APP_NAME` names none of these; it is the name the framework records traces
under.

**The tab can add something shorter of its own**, under *Browser tab* beside the
title. The two additions are one until the header's is too long for a tab: a
browser cuts a tab off after a couple of dozen characters, and somebody has six
of them open — "Go-Ticket-System | Beispiel GmbH & Co. KG" reads as
"Go-Ticket-System | Beispie…" in every one. Left empty, the tab adds what the
header adds. It translates like the other texts.

**A language you have written nothing for follows the base.** The form fills each language
with the base text so you can see what a reader of it currently gets; leaving
that untouched means exactly what it looks like — nothing written here. Rename
the installation later and every untranslated language follows along.

Saving redraws the bar where it stands, without reloading the page, so you stay
where you were on the screen.

## Business rules

The server enforces these; the interface merely also hides what is not allowed:

- Email addresses are unique.
- The built-in administrator cannot be deleted, and keeps a role that can still
  administer.
- No role the application ships with can be deleted, renamed or changed — not its
  rights, in either direction, and not its description. The `admin` role is the
  system role on top of that, the one that always administers.
- A role has to grant at least one permission. One that grants nothing can be assigned,
  and whoever holds it signs in to an interface with nothing on it, which reads as a
  broken installation rather than as a decision.
- A role still assigned to someone cannot be deleted.
- The role the directory hands to every account it brings in cannot be one that
  administers the installation.
- Deleting an account that owns unfinished tickets has to be confirmed, and the
  refusal says how many. The tickets stay, without an owner.
- An account the directory keeps cannot be renamed, readdressed or deleted here; the
  directory decides who it is, and only its role is this installation's to choose.
