'use strict';

/**
 * Single-page UI for the QubitDesk API.
 *
 * Plain DOM APIs on purpose: the assets are embedded in the Go binary, so
 * keeping the UI build-step-free means `go build` is the only build there is.
 *
 * Permission checks here only decide what to *show*. Every rule is enforced
 * again by the server, so hiding a control is convenience, never security.
 */

const API = '/api/v1';

/** Cached lookups so tables can show names instead of raw ids. */
const cache = {
  users: [], roles: [], permissions: [], queues: [],
  ticketQueues: [], states: [], priorities: [], types: [],
  found: {}, sysconfig: [], calendars: [], levels: [],
  macros: [], macroSteps: [], stepChoices: { queues: [], states: [], priorities: [], types: [], accounts: [] },
  plans: [], planEvents: [], jobs: [],
  notifying: notifyingNothing(),
  templates: [], templateQueues: [], ticketTemplates: [],
  faqCategories: [], faqVisibilities: [],
};

/**
 * The branding as it was last read.
 *
 * Kept because the configured texts can refer to it - a footer saying "{instance}
 * {version}" is drawn from here - and because a language change redraws those
 * texts without asking the server again.
 */
let lastBranding = {};

/**
 * The version this document came from, read once and never moved.
 *
 * Not lastBranding.version, which follows the process the page is talking to -
 * so after an update it becomes the new version, and a comparison against it
 * would say nothing changed at the one moment everything did.
 *
 * What it answers is a different question: which build wrote the script, the
 * stylesheet and the markup this tab is running. Nothing can change that but a
 * load, which is exactly why it is worth knowing.
 */
let loadedVersion = null;

/** The signed-in user, their permissions, and whether auth is on at all. */
let me = { user: null, permissions: [], authEnabled: false };

// ---------------------------------------------------------------- transport

/**
 * Reads a cookie the server left for this page.
 *
 * Only non-HttpOnly cookies are visible here, which is exactly the CSRF token
 * and never the session token.
 */
function readCookie(name) {
  const prefix = `${name}=`;

  for (const part of document.cookie.split(';')) {
    const entry = part.trim();
    if (entry.startsWith(prefix)) return decodeURIComponent(entry.slice(prefix.length));
  }

  return '';
}

/** Requests that cannot change anything need no token. */
const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS']);

/**
 * How long a read may hang before this side gives up on it.
 *
 * A request that never comes back holds the in-flight counter above zero for as
 * long as the connection lives, and nothing recovers from that on its own: the
 * loading strip stays up, and every screen that was waiting on the answer waits
 * for ever. The browser does eventually drop a dead connection, but "eventually"
 * is minutes of a page that looks broken.
 *
 * Generous on purpose. This is meant to catch a server that has stopped
 * answering, not a query that is merely slow - the slowest real read here is a
 * long period of entries, which is seconds.
 */
const READ_TIMEOUT_MS = 60000;

/**
 * The loading strip, driven by how many requests are in flight.
 *
 * Counted rather than toggled: three requests starting together and finishing
 * apart would otherwise switch it off while two were still running.
 *
 * Nothing appears for a request that finishes quickly. A strip that flashes for
 * 40ms is noise, and the screen it flashes over is already answering - so it
 * waits, and only says something when there is something to wait for.
 */
const progress = { inFlight: 0, showTimer: null, hideTimer: null };

/** How long a request may take before it is worth mentioning. */
const PROGRESS_DELAY_MS = 140;

/**
 * How long the strip takes to run to the end and fade once nothing is waiting.
 *
 * Named rather than written into the one setTimeout that uses it, because a test
 * has to wait longer than this to see the strip actually go - and a test carrying
 * its own copy of the number starts blaming the wrong thing the day this changes.
 * It matches the transition under .progress.done span in app.css.
 */
const PROGRESS_FADE_MS = 460;

function progressStart() {
  progress.inFlight += 1;

  if (progress.inFlight > 1 || progress.showTimer) return;

  clearTimeout(progress.hideTimer);

  progress.showTimer = setTimeout(() => {
    progress.showTimer = null;

    const bar = $('#progress');
    if (!bar) return;

    // Removed and re-added so the creep animation restarts rather than
    // continuing from where the last request left it.
    bar.classList.remove('done');
    bar.hidden = false;
    void bar.offsetWidth;
  }, PROGRESS_DELAY_MS);
}

function progressDone() {
  progress.inFlight = Math.max(0, progress.inFlight - 1);

  if (progress.inFlight > 0) return;

  // Nothing is waiting any more, so a pending "show it" is dropped: this is the
  // common case, a request that finished before it was worth mentioning.
  if (progress.showTimer) {
    clearTimeout(progress.showTimer);
    progress.showTimer = null;
  }

  // Deliberately not a `return` above. The strip could still be on screen from an
  // earlier request, and then dropping the show timer is only half the job:
  //
  //   a slow request is shown, finishes, and starts fading out;
  //   inside that fade another request starts, which cancels it;
  //   that one finishes quickly, before it was itself worth showing.
  //
  // The fade had been cancelled and nothing re-armed it, so the strip sat there
  // with hidden still false - invisible, because the inner span had already faded,
  // but present, and the counter out of step with the screen. It stayed that way
  // until some later request outlived the delay and put the strip up again, which
  // on a quiet screen is a long time. A browser test caught it as "showing with no
  // request in flight", which is exactly what it was.
  const bar = $('#progress');
  if (!bar || bar.hidden) return;

  bar.classList.add('done');

  // Belt and braces. Under the only traffic there is - api(), which pairs these two
  // - progressStart has already cleared this on the way up from zero, so there is
  // nothing live to cancel here. Nothing enforces that pairing though, and a stray
  // timer would hide a strip that a later request has just put up.
  clearTimeout(progress.hideTimer);

  progress.hideTimer = setTimeout(() => {
    // Only if nothing started again while it was fading out.
    if (progress.inFlight === 0) bar.hidden = true;
  }, PROGRESS_FADE_MS);
}

/**
 * What a refusal means beyond its message.
 *
 * Lifted out of api() so the requests that cannot go through it share it. Three
 * cannot: the download needs the response as a blob, and the two imports need the
 * browser to set a multipart boundary only it knows. Each had written out the
 * part it needed and stopped at the message, so all three missed the two answers
 * that are about the installation rather than about the request - a session that
 * has ended, and maintenance mode - and an upload, the longest-running thing here
 * and so the likeliest to be the request that finds the session gone, showed a
 * red toast on a screen that still looked signed in.
 *
 * The caller throws it. This builds it, and does what the status obliges on the
 * way, which is why it is not simply a constructor.
 */
function refusalFrom(res, body) {
  const err = new Error(errorMessage(body) || `${t('msg.error', 'Error')} ${res.status}`);

  // A 503 from maintenance mode is not a failure of this request, it is the
  // state of the installation. Showing the banner here means every screen
  // reports it the same way rather than each one growing its own handling.
  if (res.status === 503 && body?.error?.maintenance) {
    const banner = $('#maintenance-banner');
    if (banner) {
      banner.textContent = err.message;
      banner.hidden = false;
    }

    // And whoever may not be here while it lasts goes back to the sign-in
    // screen, where the same sentence is waiting.
    //
    // Everything an ordinary account can reach is refused for as long as
    // maintenance is on, so what they had was a signed-in screen where every
    // card was an error and every click another one - the interface insisting
    // they were working while nothing worked. Two clicks after an
    // administrator flipped the switch, and no way to tell it was deliberate.
    //
    // Told to the server rather than only forgotten here: an account signed
    // out by this should have to sign in again afterwards, and a session left
    // standing would let the screen come back by itself the moment maintenance
    // ended.
    //
    // Not for whoever may administer the installation - they are the ones who
    // end it, and the request that brought this back was some other 503.
    if (me.user && !can('settings:manage') && !endingTheSession) {
      endingTheSession = true;

      try {
        void endTheSessionQuietly();
        handBackTheScreen(err.message);
      } finally {
        endingTheSession = false;
      }
    }
  }
  // The status alongside the message, so a caller can tell "you are not
  // signed in" from "that did not work" without parsing prose. The log
  // viewer's poller needs exactly that distinction: on a 401 it has to stop
  // rather than keep asking every few seconds forever.
  err.status = res.status;

  // The refusal itself, kept whole beside the sentence made from it. The
  // sentence is what a person reads; the object holds what could not be turned
  // into one - the words of whatever actually failed, and the reference that
  // finds the matching log line. A caller that has somewhere to fold that away
  // wants it, and every caller that does not can go on using err.message.
  err.refusal = body?.error ?? null;

  // A session that is no longer accepted takes the screen with it.
  //
  // Only where this page believed it had one: the first load asks before
  // anybody has signed in and is answered exactly like this, and treating that
  // as an ending would clear the drafts of a session that never started.
  //
  // Guarded against re-entry, because the ending itself makes requests fail -
  // the pollers it stops are stopped by it, and each of their failures would
  // otherwise arrive here and start the same ending again.
  if (res.status === 401 && me.user && !endingTheSession) {
    endingTheSession = true;

    try {
      handBackTheScreen(t('err.sessionExpired',
        'The session has ended. Please sign in again.'));
    } finally {
      endingTheSession = false;
    }

    // And the Android app is told, which signs in with its token rather than a
    // password and cannot see this for itself: it opens a new session where the
    // token is still good, and asks for a new token where it was taken back. Not
    // on signing out, which the app sees in the requests and answers by
    // disconnecting - told then too, it would sign straight back in.
    window.QubitDeskSession?.ended();
  }

  return err;
}

/**
 * fetch, with a request that never arrived put into words.
 *
 * fetch failing before there is an answer - the server stopped, the connection
 * dropped - throws the browser's own exception, worded in its language and its
 * own way: "Failed to fetch" in one, "NetworkError when attempting to fetch
 * resource." in another. The sentence is ours; the browser's words go where a
 * refusal's original words go, under it. Every request goes through here, api()
 * and the three that cannot use it, so none of them can forget.
 *
 * An aborted request goes back as it came: whoever aborted it asked for it, and
 * api() tells its own timeout from a caller's.
 */
async function reach(url, options) {
  // A write that is already on its way is not sent a second time.
  //
  // A double click, or Enter held down, submits a form twice before the first
  // answer has come back - and a record sent twice is written twice, an import
  // sent twice every row written twice. Measured: two entries, created in the
  // same second, from one form pressed twice. Asked here because
  // every request passes through here, so no form has to remember it.
  const key = writeKey(url, options);

  if (key && writesInFlight.has(key)) {
    const repeated = new Error(t('msg.alreadySending', 'This is already being sent.'));
    repeated.repeated = true;

    throw repeated;
  }

  if (key) writesInFlight.add(key);

  try {
    return await fetch(url, options);
  } catch (err) {
    if (options?.signal?.aborted) throw err;

    const unreachable = new Error(t('msg.unreachable',
      'The server could not be reached. Check the connection and try again.'));
    unreachable.refusal = { detail: err.message };
    unreachable.cause = err;

    // Which the sign-in screen tells apart from a refusal: see showSignInOffline.
    unreachable.unreachable = true;

    throw unreachable;
  } finally {
    if (key) writesInFlight.delete(key);
  }
}

/** The writes on their way now, by what they would change; see reach. */
const writesInFlight = new Set();

/**
 * What a write would change - its method, its address and what it carries - or
 * null for a read, which does no harm twice. A file stands for itself by name,
 * size and time of change, which is what a second press of the same import sends.
 */
function writeKey(url, options) {
  const method = (options?.method ?? 'GET').toUpperCase();
  if (SAFE_METHODS.has(method)) return null;

  const body = options?.body;
  const carried = body instanceof FormData
    ? [...body.entries()].map(([name, value]) => (value instanceof File
      ? `${name}=${value.name}:${value.size}:${value.lastModified}`
      : `${name}=${value}`)).join('&')
    : String(body ?? '');

  return `${method} ${url} ${carried}`;
}

/**
 * Calls the API and unwraps GoFr's {data, error} envelope.
 *
 * State-changing calls echo the CSRF cookie back in a header. Another site can
 * make the browser send the session cookie, but it can neither read this cookie
 * nor set a custom header, so the echo is what proves the call came from here.
 *
 * @throws {Error} with the server-provided message on a non-2xx response.
 */
async function api(path, options = {}) {
  const method = (options.method ?? 'GET').toUpperCase();
  const headers = { 'Content-Type': 'application/json', ...(options.headers ?? {}) };

  if (!SAFE_METHODS.has(method)) {
    headers['X-CSRF-Token'] = readCookie('qubitdesk_csrf');
  }

  // A request this page makes by itself - a check on a timer - says so, and the
  // server then does not count it as anybody being there. Counted, the minute's
  // check kept every session with a page in view open, so the idle timeout never
  // ended the one it is for: a screen left open at lunch.
  if (options.background) {
    headers['X-Background-Request'] = '1';
  }

  // Reads only, and this is the whole reason the distinction is drawn here.
  //
  // Aborting in the browser does not stop the server: it finishes the work either
  // way. Giving up on a write would therefore report a failure for something that
  // succeeded - and somebody would do it again, which for an import means writing
  // every row twice. A read costs nothing but a second attempt.
  //
  // A caller that brought its own signal keeps it: it has a reason of its own for
  // wanting to cancel, and two things aborting one request cannot both be right.
  const giveUp = SAFE_METHODS.has(method) && !options.signal ? new AbortController() : null;
  const countdown = giveUp ? setTimeout(() => giveUp.abort(), READ_TIMEOUT_MS) : null;

  progressStart();

  let res;

  try {
    res = await reach(API + path, {
      ...options,
      headers,
      signal: giveUp ? giveUp.signal : options.signal,
      // Without this a cross-origin deployment would drop the cookies entirely.
      credentials: 'same-origin',
    });
  } catch (err) {
    // Only our own abort is reported as a timeout. A caller's signal firing is
    // their business, and "the server did not answer" would be a lie about it.
    if (giveUp?.signal.aborted) {
      throw new Error(t('msg.tooSlow',
        'The server did not answer in time. Please try again.'));
    }

    throw err;
  } finally {
    // Cleared whatever happened, so a fast request does not leave a timer holding
    // a controller it has no further use for.
    clearTimeout(countdown);

    // In a finally, so a refused connection does not leave the strip running for
    // as long as the page is open.
    progressDone();
  }

  noticePermissionChange(res.headers.get('X-Permissions-Revision'));

  let body = null;
  try {
    body = await res.json();
  } catch {
    // A 204 or an error page has no JSON body; fall through to the status.
  }

  if (!res.ok) {
    throw refusalFrom(res, body);
  }

  return body ? body.data : null;
}

/**
 * The message to show for a failed request, in the reader's language where the
 * server said which rule was broken.
 *
 * The server writes its refusals in English at the point the rule is enforced,
 * which is right for the log and wrong for the person who tripped over it. Errors
 * that name themselves carry a code and the values the sentence interpolated, so
 * the sentence is looked up here and the values put back in German word order.
 * Anything without a code falls back to the server's own wording, which is what
 * every error did before.
 */
function errorMessage(body) {
  const err = body && body.error;
  if (!err) return '';
  if (typeof err === 'string') return err;

  return describeRefusal(err);
}

/**
 * FIELD_NAMES is the English a refused field is named by, in the words the forms
 * and tables use.
 *
 * Refusals name fields by their column names. German has field.<name> for each,
 * and English has no dictionary, so English is whatever the lookup falls back to -
 * which was the name itself: an English refusal read "Invalid field(s):
 * sessionIdleMinutes" about a box labelled Sign out after inactivity. The same
 * arrangement as PERMISSION_TITLES, for the same reason.
 */
const FIELD_NAMES = {
  access: 'Access',
  address: 'Address',
  banner: 'Banner text',
  body: 'Text',
  builderId: 'Change builder',
  baseDn: 'Base DN',
  channel: 'Channel',
  class: 'Class',
  classId: 'Class',
  contactId: 'Contact',
  code: 'Code',
  comment: 'Comment',
  companyName: 'Company',
  companyUrl: 'Company website',
  confirmed: 'Confirmed accounts',
  country: 'Country',
  clientId: 'Client ID',
  issuer: 'Issuer address',
  justification: 'Justification',
  label: 'Name on the sign-in form',
  defaultRole: 'Default role',
  deploymentStateId: 'Deployment state',
  description: 'Description',
  email: 'Email',
  expiresInDays: 'Valid for (days)',
  followUp: 'Answer to a closed ticket',
  functionality: 'Functionality',
  footerText: 'Footer',
  host: 'Host',
  id: 'ID',
  impactId: 'Impact',
  incidentStateId: 'Incident state',
  keep: 'Backups kept',
  language: 'Language',
  ldapSyncMaxDeleteRatio: 'Deletion limit',
  legalNotice: 'Legal notice',
  level: 'Level',
  limit: 'Per page',
  linkType: 'Kind of link',
  logLevel: 'Log level',
  minutes: 'Minutes',
  name: 'Name',
  note: 'Note',
  number: 'Number',
  object: 'Kind of record',
  organisationId: 'Organisation',
  ownerId: 'Owner',
  page: 'Page',
  parentId: 'Under',
  password: 'Password',
  pendingUntil: 'Pending until',
  phone: 'Phone',
  port: 'Port',
  priorityId: 'Priority',
  queue: 'Queue',
  queueId: 'Queue',
  region: 'State or region',
  requestedAt: 'Requested for',
  responsibleId: 'Responsible',
  rateLimit: 'Rate limit',
  rateLimitWindowSeconds: 'Rate limit window (seconds)',
  role: 'Role',
  schedule: 'Schedule',
  sessionIdleMinutes: 'Sign out after inactivity (minutes)',
  sessionLifetimeHours: 'Session lifetime (hours)',
  stateId: 'State',
  subject: 'Subject',
  syncSchedule: 'Schedule',
  tabTitle: 'Browser tab',
  targetObject: 'To',
  theme: 'Appearance',
  timezone: 'Timezone',
  title: 'Title',
  traceExporter: 'Trace exporter',
  tracerRatio: 'Share of traces recorded',
  tracerUrl: 'Collector',
  type: 'Type',
  typeId: 'Type',
  unlockMinutes: 'Unlock after (minutes)',
  url: 'Website',
  userFilter: 'User filter',
  value: 'Value',
  zone: 'Timezone',
  holidayPlace: 'Public holidays',
  hours: 'Working hours',
  freeDays: 'Free days',
  calendarId: 'Calendar',
  firstResponseMinutes: 'First response (minutes)',
  updateMinutes: 'Between answers (minutes)',
  solutionMinutes: 'Solution (minutes)',
  sourceObject: 'From',
  serviceLevelId: 'Service level',
  actions: 'Steps',
  weekdays: 'Weekdays',
  times: 'Times',
  events: 'Events',
  types: 'State types',
  nextStateId: 'Moves on to',
  criteria: 'Which tickets',
  macroIds: 'Macros',
  managerId: 'Change manager',
  intoId: 'Merge into',
  agentId: 'Agent',
  instruction: 'Instruction',
  report: 'Report',
  workOrderTypeId: 'Work order type',
  plannedStartAt: 'Planned start',
  plannedEndAt: 'Planned end',
  actualStartAt: 'Actual start',
  actualEndAt: 'Actual end',
  plannedMinutes: 'Planned effort (minutes)',
  planIds: 'Execution plans',
  pendingMinutes: 'Waits for (minutes)',
  locked: 'Locked',
  customerVisible: 'The customer sees it',
  recipients: 'Recipients',
  messages: 'Texts',
  switchedOff: 'Notifications you take',
  followed: 'Queues you follow',
  categoryId: 'Category',
  keywords: 'Keywords',
  visibility: 'Who may read it',
  watched: 'Only tickets I watch',
  overdue: 'Only overdue tickets',
  createdTo: 'Opened until',
  changedTo: 'Changed until',
  text: 'Words in the ticket',
  sort: 'Order',
  order: 'Direction',
};

/** fieldName names a refused field the way the screen names it, in the reader's language. */
function fieldName(field) {
  return t(`field.${field}`, FIELD_NAMES[field] ?? field);
}

/**
 * One refusal, in the reader's language where the server said which rule it was.
 *
 * Split out of errorMessage because a refusal does not always arrive as an error
 * status. The connection test answers 200 with the reason inside it - a database
 * that cannot be reached is information about what somebody typed, not a fault -
 * and that put it outside this path, so it was shown as the English prose the
 * server wrote. Two renderings of one thing is one too many. A synchronisation
 * that a guard refuses answers 200 the same way, with its reason in the report.
 */
function describeRefusal(err) {
  // Maintenance is the one refusal whose sentence may not be ours: an
  // administrator who wrote the notice wrote it for the people who will read it.
  // The server sends this code only when nobody did, so the default is the one
  // thing translated here - and it is the sentence the banner already carries, so
  // both say the same words.
  if (err.code === 'maintenance') {
    return t('err.maintenance',
      'This installation is temporarily unavailable for maintenance.');
  }

  // The named fields first, and before the code, which is the ordering this got
  // wrong the moment a code was added to them: a rejection that says which fields
  // is strictly more use than the sentence it would otherwise fall back to, and
  // the sentence was winning simply because it was tested for first.
  //
  // The fields are labelled the way the form labels them rather than by their
  // column names, which is the whole reason they travel as data.
  if (Array.isArray(err.param) && err.param.length) {
    const named = err.param.map(fieldName);

    return `${t('err.invalidFields', 'Invalid field(s)')}: ${named.join(', ')}`;
  }

  if (err.code) {
    const translated = t(`err.${err.code}`, err.message ?? '');
    if (translated) return fillIn(translated, refusalValues(err.code, err.values));
  }

  if (err.message) return err.message;

  return JSON.stringify(err);
}

/**
 * The part of a refusal that was never going to be translatable.
 *
 * A driver's "dial tcp 10.0.0.4:5432: connect: connection refused", a directory
 * library's own wording, a file system's "permission denied". None of it is this
 * application's prose - it is written by somebody else, in English, and the list
 * of sentences it can produce has no end, so no dictionary here can cover it.
 *
 * Which leaves two bad options and one good one. Showing it is showing a German
 * reader a line they cannot use, on the screen where the problem is. Hiding it
 * is throwing away the only text that says what actually happened. Folding it
 * away serves both: the sentence on screen is in the reader's language, and the
 * words an administrator needs are one click below it.
 *
 * The reference goes with it. The same string is in the log line the server
 * wrote, so somebody reading a screenshot and somebody reading the log are
 * looking at the same occurrence - which is exactly what a generic message
 * otherwise costs.
 */
function refusalDetail(err) {
  if (!err || typeof err !== 'object') return '';

  const parts = [];

  if (err.detail) parts.push(err.detail);

  if (err.ref) {
    parts.push(fillIn(t('detail.reference', 'Reference: {0}'), [err.ref]));
  }

  return parts.join('\n\n');
}

/** A fold-away block of text that is not in the reader's language. */
function detailDisclosure(detail) {
  const box = el('details', { class: 'refusal-detail' },
    el('summary', { text: t('detail.show', 'Technical details') }),
    el('pre', { class: 'refusal-text', text: detail }));

  return box;
}

/**
 * Writes a refusal into an element: the sentence, and the detail under it.
 *
 * One function because there are two places a refusal lands - a notice in the
 * corner and a line under a form - and they had drifted into saying the same
 * thing two different ways.
 */
function showRefusal(target, err) {
  if (!target) return;

  target.replaceChildren(el('span', { text: describeRefusal(err) }));

  const detail = refusalDetail(err);
  if (detail) target.append(detailDisclosure(detail));
}

/**
 * Which values of which refusal the screen writes in words of its own.
 *
 * fillIn writes a number the way the tables beside it do, and a string as it
 * arrives. Some strings arrive in a form nobody on this screen reads: a day in
 * the order the wire uses, a state as the word it is stored under, the kind of
 * record a "not found" names, a field by its name on the wire. So a German
 * refusal would say "ist closed_successful" beside a badge saying "erfolgreich
 * geschlossen" - a word the reader could look for and find nowhere.
 *
 * Named by code and position, because on the wire the values are plain strings
 * and stay so: an API client reads them too. Which of them is a day or a state is
 * known here, beside the sentences, and in the call that sends it.
 * TestARefusalWritesItsValuesTheWayTheScreenDoes reads every such call and fails
 * on one this does not name.
 */
const REFUSAL_VALUES = {
  notFound: { 0: entityName },
  stateShipped: { 0: statusName },
  stateInUse: { 0: statusName },
  stateRetired: { 0: statusName },
  stateEndsAWait: { 0: statusName },
  priorityShipped: { 0: priorityName },
  priorityInUse: { 0: priorityName },
  priorityRetired: { 0: priorityName },
  typeShipped: { 0: ticketTypeName },
  typeInUse: { 0: ticketTypeName },
  typeRetired: { 0: ticketTypeName },
  systemRoleUnrenamable: { 0: roleTitle },
  systemRoleRightsFixed: { 0: roleTitle },
  systemRoleDescriptionFixed: { 0: roleTitle },
  systemRoleUndeletable: { 0: roleTitle },
  defaultRoleUndeletable: { 0: roleTitle },
  roleStillAssigned: { 0: roleTitle },
  directoryRoleAdministers: { 0: roleTitle },
  adminRoleMustAdminister: { 0: roleTitle },
  portalAccountAdministers: { 1: roleTitle },
  roleHeldByPortalAccounts: { 0: roleTitle },
  macroStepInvalid: { 1: fieldName },
  catalogValueRetired: { 0: catalogValueTitle },
  configItemClassRetired: { 0: catalogValueTitle },
  configItemNoDefinition: { 0: catalogValueTitle },
  changeStateNotAllowed: { 0: catalogValueTitle, 1: catalogValueTitle },
  workOrderStateNotAllowed: { 0: catalogValueTitle, 1: catalogValueTitle },
};

/** The values of one refusal, each written the way the rest of the screen writes it. */
function refusalValues(code, values) {
  const written = REFUSAL_VALUES[code];
  if (!written || !Array.isArray(values)) return values;

  return values.map((value, i) => (written[i] && typeof value === 'string'
    ? written[i](value)
    : value));
}

/**
 * Puts {0}, {1} ... back into a translated sentence.
 *
 * A fractional number is shown to two places, which is how hours are written
 * everywhere else on screen - a refusal saying 6.5 next to a table saying 6.50
 * would read as two different figures.
 */
function fillIn(text, values) {
  if (!Array.isArray(values) || !values.length) return text;

  return text.replace(/\{(\d+)\}/g, (whole, index) => {
    const value = values[Number(index)];
    if (value === undefined || value === null) return whole;
    if (typeof value !== 'number') return String(value);

    return Number.isInteger(value) ? String(value) : fmtNumber(value);
  });
}

// -------------------------------------------------------------------- utils

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

/**
 * The languages this installation speaks, each by what it calls itself, English
 * first: written into the document by the server, from the dictionaries the
 * binary carries, so a language is offered here as soon as its file is there and
 * nothing in this script has to be told.
 *
 * Read from the document rather than asked of the API, because the document is
 * what the service worker keeps - offline, this is still known. Here rather than
 * beside the dictionaries because the appearance texts' languages are read from
 * it while this script is still loading.
 */
const LANGUAGES = (() => {
  const offered = {};

  for (const tag of $$('meta[name="language"]')) {
    if (tag.content && tag.dataset.name) offered[tag.content] = tag.dataset.name;
  }

  // The served document always carries them; only a page opened some other way
  // does not, and that page speaks English.
  return typeof offered.en === 'string' ? offered : { en: 'English' };
})();

/** Whether the signed-in user holds at least one of the given permissions. */
function can(...permissions) {
  if (!me.authEnabled) return true;

  return permissions.some((p) => me.permissions.includes(p));
}

/**
 * Tells somebody their rights changed while they were signed in.
 *
 * The server has always applied the change on the very next request - it resolves
 * who is calling from the database every time - but the interface read /me once at
 * start-up and kept whatever it was given. So a right taken away showed up as a
 * refusal on a button that was still on screen, and a right granted showed up as
 * nothing at all until the next sign-in.
 *
 * Three things arrive here, and all of them arrive as a revision to compare with
 * the one /me gave: the header on every response, the once-a-minute poll below,
 * and the announcement stream, which is the only one of the three that reaches a
 * page nobody is touching. Once per change, whichever of them got here first -
 * the recorded value moves forward, so the next one through is already in
 * agreement and says nothing.
 *
 * A banner rather than a toast. The case this is written for is a screen somebody
 * is reading rather than working in, and a notice that takes itself down after a
 * few seconds is one that a person who stepped away never sees - they come back
 * to a screen that looks right and is not. It is a state, like the two notices
 * above it: true until the page is loaded again.
 *
 * A reload rather than re-reading /me in place: what a role opens is more than a
 * set of tabs - it is which screens were loaded at all - and reloading is the one
 * way to be sure the whole interface agrees about it.
 */
function noticePermissionChange(revision) {
  if (!revision || !me.user || !me.permissionsRevision) return;
  if (revision === me.permissionsRevision) return;

  me.permissionsRevision = revision;

  showRightsChanged();
}

/** Puts the notice up, where it stays until the page is loaded again. */
function showRightsChanged() {
  const banner = $('#rights-banner');
  if (banner) banner.hidden = false;
}

/**
 * Takes it down.
 *
 * For a screen that is about to belong to somebody else: a session that ends
 * leaves every card behind it, and the previous account's news is the one thing
 * that must not be waiting for whoever signs in next.
 */
function hideRightsChanged() {
  const banner = $('#rights-banner');
  if (banner) banner.hidden = true;
}

/**
 * How often to ask whether this account may still do what it could a minute ago.
 *
 * The revision travels on every response, so anybody clicking around finds out
 * without this. Somebody reading a screen is not clicking around, and the case
 * that matters is exactly that one: a right is withdrawn while the person it was
 * withdrawn from is looking at the screen it opened.
 *
 * The stream is what covers that case now - the server checks the account behind
 * an open connection and writes down it when the answer moves, so the notice
 * arrives within seconds of the change and without anybody doing anything. This
 * is the fallback underneath it: a browser with no EventSource, a connection a
 * proxy has quietly dropped, a stream that has not reconnected yet.
 *
 * A minute is right for a fallback. This is a notice rather than an enforcement -
 * the API refuses a withdrawn right on the very next call whatever the interface
 * believes - so the only thing at stake is how long somebody looks at a screen
 * that has stopped being true.
 */
const PERMISSION_POLL_MS = 60000;

let permissionPoll = null;

/**
 * Asks periodically, and at once whenever the tab is looked at again.
 *
 * /me rather than a dedicated endpoint: every authenticated response carries the
 * revision header, api() compares it, and this one also happens to be the answer
 * to "what may I do", so nothing new had to be built or kept in step.
 *
 * Not while the tab is hidden. A window left open overnight would otherwise ask
 * five hundred times to tell nobody anything, and the check on becoming visible
 * again covers the whole gap in one request - which is the moment somebody would
 * find out anyway.
 */
function startPermissionPolling() {
  stopPermissionPolling();

  permissionPoll = setInterval(askWhetherTheScreenIsStillTrue, PERMISSION_POLL_MS);
}

/**
 * The two background questions, asked together.
 *
 * They answer one worry from opposite sides: what this account may do, and
 * whether the installation is open to it at all. Both are for the screen somebody
 * is reading rather than working in, which is the case neither the revision
 * header nor a refused click covers.
 *
 * One function because they are asked from two places - the minute beat, and the
 * moment a hidden tab is looked at again - and written out twice is how the
 * second place came to ask only the first of them. The interval is allowed to
 * skip while the tab is hidden precisely because coming back covers the gap in
 * one go, so a return that asked half the question left the interface standing
 * through a maintenance switch for up to a minute.
 *
 * The guard belongs here rather than at each caller: visibilitychange fires on
 * the way out as well as the way back.
 */
function askWhetherTheScreenIsStillTrue() {
  if (document.hidden || !me.user) return;

  // Failures are ignored on purpose. This is a background check nobody asked
  // for, and a toast about it - on a screen somebody is reading, once a minute
  // for as long as the network is unhappy - would be worse than the thing it
  // is watching for.
  api('/me', { background: true }).catch(() => {});

  // The announcement stream notices maintenance within seconds; this is what
  // covers a browser that has no EventSource at all.
  void checkWhetherStillWelcome();

  // And whether there is a newer version, for the same reason the two above are
  // asked here. The hourly watch says "A day left open should notice a release",
  // and a day left open is usually a day spent hidden - where its interval skips
  // by its own guard, and where browsers throttle timers heavily in any case.
  //
  // Asked of the watch rather than of the permission: startReleaseWatch already
  // decides who is offered this news and returns without starting for everybody
  // else, so a poll that is not running is the same answer without a second copy
  // of the rule.
  if (releasePoll) checkForRelease();
}

/**
 * Asks whether this installation is still open to whoever is looking at it, and
 * hands the screen back if it is not.
 *
 * Maintenance mode refuses everything an ordinary account does, which is the
 * point - but what it left behind was the whole interface standing, every card
 * an error and every click another one, with nothing saying that was deliberate.
 *
 * /maintenance is one of the endpoints maintenance mode deliberately keeps
 * answering, so this works from inside it - which is the whole reason the
 * question can be asked at all.
 *
 * Not for whoever may administer the installation. They are the ones who end it,
 * and signing them out of the screen with the switch on it would be the trap
 * this mode is written to avoid.
 */
async function checkWhetherStillWelcome() {
  if (!me.user || can('settings:manage') || endingTheSession) return;

  let state;

  try {
    state = await api('/maintenance', { background: true });
  } catch {
    // Unreachable, or refused. Either way this is a background question and the
    // answer to a failed one is to ask again next time.
    return;
  }

  if (!state?.enabled || endingTheSession) return;

  endingTheSession = true;

  try {
    void endTheSessionQuietly();
    handBackTheScreen(state.message || t('err.maintenance',
      'This installation is temporarily unavailable for maintenance.'));
  } finally {
    endingTheSession = false;
  }
}

function stopPermissionPolling() {
  clearInterval(permissionPoll);
  permissionPoll = null;
}

/** How often an open tab asks again. A day left open should notice a release. */
const RELEASE_POLL_MS = 60 * 60 * 1000;

let releasePoll = null;

/**
 * Tells whoever may install one that a newer version exists.
 *
 * The version card says the same thing and says it on one screen, which nobody
 * visits to find out. An installation can sit three releases behind because the
 * only place that mentions it is the place you go once a quarter.
 *
 * A banner rather than a toast, because a toast fades and this is worth not
 * losing - and not dismissable, which it once was. Dismissing read as a
 * courtesy and worked out as the same three releases behind, arrived at by
 * clicking a cross once on a busy morning. It is a state rather than news: a
 * newer version exists whether or not anybody wants to hear it, and it goes
 * when it stops being true.
 *
 * Only for the people who can act on it. Everybody else would be told about
 * work they cannot do, by an application that will not stop mentioning it.
 */
function startReleaseWatch() {
  stopReleaseWatch();

  // Only for the people who can act on it. Everybody else would be told about
  // work they cannot do, by an application that will not stop mentioning it.
  if (!can('settings:manage')) return;

  checkForRelease();

  releasePoll = setInterval(() => {
    if (document.hidden) return;

    checkForRelease();
  }, RELEASE_POLL_MS);
}

function stopReleaseWatch() {
  clearInterval(releasePoll);
  releasePoll = null;

  const banner = $('#release-banner');
  if (banner) banner.hidden = true;
}

async function checkForRelease() {
  let state;

  try {
    state = await api('/settings/update', { background: true });
  } catch {
    // Taken down, not left standing. A feed that cannot be reached is not this
    // installation being broken, and the version card says so where somebody has
    // gone looking - but the refusal that matters here is the other one: this
    // endpoint needs settings:manage, so an ordinary account is answered 403.
    //
    // Returning without hiding left whatever was on screen. Sign out of an
    // administrator and in as somebody who only works here, in the same page,
    // and the banner stayed - the previous account's news, with a working link
    // to a screen the new one may not have.
    hideReleaseBanner();

    return;
  }

  showReleaseState(state);
}

/**
 * Puts an answer about the newest version on screen, or takes the notice down.
 *
 * Its own function because two things ask that question and both have to end at
 * the same banner: the hourly watch above, and the button on the version card.
 * The button redrew only the card, so the one moment somebody is certainly
 * looking - they pressed it to find out - was the one moment the stripe across
 * the top of every screen went on saying nothing. An hour later it agreed with
 * the card, which is a long time to be told two things at once.
 */
function showReleaseState(state) {
  // Newer rather than installable: a container cannot install it from here, and
  // the person reading this is still the one who should know.
  if (!state?.newer || !state.latest) {
    hideReleaseBanner();

    return;
  }

  // Through the redraw registry, so the sentence follows a language change like
  // everything else the script writes.
  redrawable('release', () => drawReleaseBanner(state));
}

// hideReleaseBanner takes the notice down and stops it being redrawn.
//
// One function because there are three reasons to do it - refused, nothing
// newer, nobody signed in - and a banner that is only hidden on two of them is
// a banner that survives the third.
function hideReleaseBanner() {
  const banner = $('#release-banner');
  if (banner) banner.hidden = true;

  stopRedrawing('release');
}

function drawReleaseBanner(state) {
  const banner = $('#release-banner');
  if (!banner) return;

  const text = $('#release-text');

  text.textContent = '';

  // The version is the link, and the sentence is built around it rather than
  // followed by a button.
  //
  // There was a button beside this reading "Open Settings". Two things to read
  // where one would do, and the one it left out is the one somebody actually
  // wants to press: a release has a number, and the number is what they are
  // deciding about. So the sentence is split at the place the number goes and
  // the number is put there as the control.
  //
  // Split on the placeholder itself rather than on a marker put through the
  // filler: where the number falls in the sentence is the translator's
  // business, and the two halves are whatever they wrote around it.
  const [before, after] = t('update.available',
    'Version {0} is available. This installation runs {1}.').split('{0}');

  // Both halves through fillIn, not only the one after the split. The freedom the
  // comment above buys is where the *new* version falls; the running one has to
  // be as free, and a sentence naming it first - ordinary word order in plenty of
  // languages - put a literal {1} across the top of every screen. German happens
  // to put them the other way round, so nothing showed it, and nothing would:
  // the placeholder check compares which values a translation carries, not where.
  text.append(fillIn(before ?? '', [state.latest, state.running]));

  const link = el('button', {
    type: 'button',
    class: 'link-button',
    id: 'release-open',
    text: state.latest,
  });

  link.addEventListener('click', openTheVersionCard);
  text.append(link);

  text.append(fillIn(after ?? '', [state.latest, state.running]));

  banner.hidden = false;
}

/** Takes whoever pressed it to the card that installs the new version. */
function openTheVersionCard() {
  switchView('admin');

  const card = $('#update-card');
  openTheTabOf(card);
  if (card) card.scrollIntoView({ block: 'center' });
}

// --------------------------------------------------------------- announcements

/**
 * The open connection the server writes down when something is about to happen
 * to the application itself.
 *
 * Everything else here is a question this page asked. This is the one thing that
 * cannot be: an update replaces the binary underneath and, where the platform
 * allows it, restarts into it seconds later. Somebody in the middle of typing an
 * entry finds out by the application vanishing.
 *
 * EventSource rather than a shorter poll. A poll fast enough to be a warning is a
 * request per second per open tab, for ever, to say "nothing" - and it would
 * still be late. This costs one idle connection and arrives at once. It also
 * reconnects by itself, which is what turns the restart into something the page
 * can notice and recover from rather than a wall of failed requests.
 */
let announcements = null;

/** What was last announced, so a reconnection knows what it is coming back from. */
let announced = null;

function startAnnouncements() {
  stopAnnouncements();

  if (!me.user || typeof EventSource !== 'function') return;

  announcements = new EventSource('/api/v1/events');

  announcements.addEventListener('announcement', (event) => {
    let announcement;

    try {
      announcement = JSON.parse(event.data);
    } catch {
      return;
    }

    applyAnnouncement(announcement);
  });

  // The other thing this connection carries, and the only one that is about the
  // account holding it rather than about the installation.
  //
  // Here rather than in a poll of its own because the connection is already open
  // and already belongs to this account, so the answer costs nothing to receive
  // and arrives at the moment it becomes true. That is the whole point: the
  // person a right was taken from is, by definition, not the person clicking
  // around to find out.
  announcements.addEventListener('permissions', (event) => {
    let change;

    try {
      change = JSON.parse(event.data);
    } catch {
      return;
    }

    noticePermissionChange(change.revision);
  });

  // The connection dropping is not an error to report. It is the ordinary way a
  // restart looks from here, and the browser is already reconnecting - which is
  // handled where the reconnection succeeds rather than here, because a failure
  // to reconnect means the application is not back yet and there is nothing to
  // say to anybody about that.
  //
  // It is, however, the first thing a screen notices when the installation goes
  // out of service: this stream is one of the requests maintenance mode refuses,
  // and the browser retries it every few seconds. Everything else an idle screen
  // asks for is exempt - who you are, what the branding is - so without this a
  // screen nobody is touching would sit there working-looking until its next
  // click, or until the once-a-minute permission poll came round.
  announcements.addEventListener('error', () => { void checkWhetherStillWelcome(); });

  // The first open is the connection this page asked for; every later one is the
  // browser coming back after it dropped.
  let opened = false;

  announcements.addEventListener('open', () => {
    // Back. If the last thing said was that a restart was coming, this is the
    // other side of it: the application is answering again, and it is a different
    // version from the one this page was loaded from.
    //
    // Reloading is the only honest thing to do. The script, the stylesheet and
    // the markup in this tab all came from the previous version, and carrying on
    // with them against a new API is how a page ends up half working in ways
    // nobody can reproduce.
    if (announced === 'update.restarting') {
      announced = null;
      window.location.reload();

      return;
    }

    if (!opened) {
      opened = true;

      return;
    }

    // What still stands is replayed to every connection, right after this, so
    // what was said before is taken down and the replay puts back what is still
    // true. A retraction is not replayed - the hub forgets the update it takes
    // back - so a page away when an installation was abandoned kept "a new
    // version is being installed" up for good.
    forgetTheUpdateNotice();

    // And the restart is asked of the process rather than of what this page
    // heard: an announcement lives in the memory of the process that made it, so
    // a page asleep through an update, or offline the moment the restart was
    // said, came back to the new version with nothing to tell it.
    void theVersionChanged().then((changed) => {
      if (changed) window.location.reload();
    });
  });
}

function stopAnnouncements() {
  if (announcements) announcements.close();
  announcements = null;

  forgetTheUpdateNotice();
}

/** Takes down what was said about an update, and forgets that it was said. */
function forgetTheUpdateNotice() {
  announced = null;

  stopRedrawing('announcement');

  const banner = $('#update-banner');
  if (banner) banner.hidden = true;
}

/**
 * Puts an announcement on screen.
 *
 * A banner rather than a toast: a toast fades, and this has to be true for as
 * long as it is true. Nothing is dismissable either - what it describes does not
 * stop being the case because somebody closed it.
 */
function applyAnnouncement(announcement) {
  // Maintenance is not one of the update banner's messages and must not be
  // written into it - nor recorded as the last thing announced, which is what
  // the reconnection handler reads to decide whether a restart just finished.
  //
  // It carries nothing of its own on purpose: what the state is, and what the
  // administrator wrote about it, is read from /maintenance, which keeps
  // answering while everything else is refused.
  if (announcement.kind === 'instance.maintenance') {
    void checkWhetherStillWelcome();
    void loadMaintenance();

    return;
  }

  announced = announcement.kind;

  // Through the redraw registry, so a language change reaches it like everything
  // else the script writes. Without this the banner kept whatever words it was
  // built with while the page around it changed language - and this is the one
  // message on the screen that somebody may be reading precisely because they do
  // not understand what is happening.
  redrawable('announcement', () => drawAnnouncement(announcement));
}

/** Writes one announcement into the banner, in the language now in force. */
function drawAnnouncement(announcement) {
  const banner = $('#update-banner');
  if (!banner) return;

  const version = announcement.version || '';

  const words = {
    'update.installing': () => t('announce.installing',
      'A new version ({0}) is being installed. You can carry on working; ' +
      'the application will restart shortly.'),
    'update.restarting': () => t('announce.restarting',
      'The application is restarting into version {0}. This page will reload ' +
      'by itself in a moment.'),
    'update.pending': () => t('announce.pending',
      'Version {0} has been installed and takes effect the next time the ' +
      'application is started. Nothing changes until then.'),
    'update.cancelled': () => t('announce.cancelled',
      'The update was not installed and nothing has changed. The application ' +
      'keeps running as before.'),
  }[announcement.kind];

  if (!words) {
    banner.hidden = true;

    return;
  }

  banner.textContent = fillIn(words(), [version]);
  banner.hidden = false;

  // The two that are over: an update that was abandoned, and one that will not
  // happen until somebody restarts the application by hand. Both are worth
  // saying once and neither is worth a permanent stripe across the screen.
  if (announcement.kind === 'update.cancelled' || announcement.kind === 'update.pending') {
    setTimeout(() => {
      if (announced !== announcement.kind) return;

      banner.hidden = true;
      stopRedrawing('announcement');
      announced = null;
    }, 30000);
  }
}

/**
 * Whether a failed request is the restart everybody was just warned about.
 *
 * During those few seconds every request fails, and each one would otherwise
 * raise its own red toast on top of a banner that already explains it. The
 * banner is the message; the toasts would be noise on top of it.
 */
function duringARestart() {
  return announced === 'update.restarting';
}

/**
 * Reports news in a notice that floats over the page for as long as it takes to
 * read: what was saved, what was sent, what failed. A state that goes on being
 * true is a banner instead, in #standing-notices.
 *
 * At the foot of the window, and transparent to the pointer but for its cross
 * and its detail, so a press on a notice reaches what it lies over; app.css says
 * why. It still holds still while it is being read - while the pointer is over
 * it, which followPointerOverNotices works out, or while the keyboard is in it -
 * and goes at once when it is closed. The line along its foot runs down for the
 * time it stays, so how long that is can be seen.
 *
 * The same sentence again while it is still up is counted on it rather than
 * stacked under it: a loop of failures used to fill the corner with one
 * sentence four times, which reads as four different things having gone wrong.
 */
function toast(message, kind = 'ok', detail = '') {
  const stack = $('#toast');
  if (!stack) return;

  const twin = [...stack.children].find((other) => other.dataset.message === message
    && other.dataset.kind === kind && !other.classList.contains('leaving'));

  if (twin) {
    twin.bumpToast?.();

    return;
  }

  const note = el('div', { class: `toast-note ${kind}`, 'data-message': message, 'data-kind': kind });

  // Errors are announced assertively and successes politely: an error is worth
  // interrupting whatever a screen reader was saying, and "Saved" is not.
  note.setAttribute('role', kind === 'error' ? 'alert' : 'status');

  const body = el('div', { class: 'toast-body' }, el('span', { class: 'toast-text', text: message }));

  // The words of whatever actually failed, folded away. Shown open it would be a
  // paragraph of somebody else's English across the corner of the screen for
  // every reader who cannot use it; left out altogether it would be gone by the
  // time the person who can use it is asked.
  if (detail) body.append(detailDisclosure(detail));

  const count = el('span', { class: 'toast-count', hidden: true });

  const dismiss = el('button', {
    class: 'toast-close',
    type: 'button',
    'aria-label': t('action.dismiss', 'Dismiss'),
    onclick: () => leave(),
  }, spriteIcon('icon-close'));

  const timer = el('span', { class: 'toast-timer', 'aria-hidden': 'true' });

  note.append(spriteIcon(kind === 'error' ? 'icon-toast-error' : 'icon-toast-ok', 'toast-icon'),
    body, count, dismiss, timer);
  stack.append(note);

  // Stacked rather than replaced. A single slot meant two failures in a row
  // showed only the second, which is the case where the first one mattered.
  // Bounded, because a loop of failures should not fill the screen.
  while (stack.children.length > TOAST_LIMIT) stack.firstElementChild.remove();

  // Long enough to read what is there. A fixed five seconds is fine for "Saved"
  // and not for a sentence explaining why a directory refused a bind.
  const linger = Math.min(TOAST_MAX_MS, TOAST_MIN_MS + message.length * TOAST_MS_PER_CHAR);

  let remaining = linger;
  let since = 0;
  let pending = null;
  let holds = 0;

  function leave() {
    clearTimeout(pending);
    note.classList.add('leaving');

    // Away at once where nothing moves, and after its way out where it does.
    const motion = !window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    setTimeout(() => note.remove(), motion ? TOAST_LEAVE_MS : 0);
  }

  function run() {
    since = Date.now();
    pending = setTimeout(() => {
      // Not while somebody is reading the detail they just opened. The timer is
      // there so a notice nobody is looking at goes away by itself, and one that
      // has been unfolded is the opposite of that.
      if (note.querySelector('details[open]')) return;

      leave();
    }, remaining);
  }

  function hold() {
    if (note.classList.contains('leaving')) return;

    holds += 1;
    if (holds > 1) return;

    clearTimeout(pending);
    remaining = Math.max(0, remaining - (Date.now() - since));
    note.classList.add('held');
  }

  function release() {
    holds = Math.max(0, holds - 1);
    if (holds > 0 || note.classList.contains('leaving') || !note.isConnected) return;

    note.classList.remove('held');
    run();
  }

  // The same news again: counted here, and given its full time again.
  note.bumpToast = () => {
    const seen = Number(note.dataset.count ?? '1') + 1;

    note.dataset.count = String(seen);
    count.hidden = false;
    count.textContent = `×${seen}`;

    clearTimeout(pending);
    remaining = linger;

    timer.style.animation = 'none';
    void timer.offsetWidth;
    timer.style.animation = '';
    timer.style.animationDuration = `${linger}ms`;

    if (holds === 0) run();
  };

  note.holdToast = hold;
  note.releaseToast = release;
  note.addEventListener('focusin', hold);
  note.addEventListener('focusout', release);

  timer.style.animationDuration = `${linger}ms`;
  run();
}

/**
 * Holds a notice still while the pointer is over it, worked out from where the
 * pointer is: a notice is transparent to the pointer, so it never hears the
 * pointer arrive or leave - the page under it does. A finger has no hover, and
 * one that only scrolled past would hold a notice nothing then lets go of.
 */
function followPointerOverNotices() {
  let over = new Set();

  const follow = (x, y) => {
    const notes = document.querySelectorAll('#toast .toast-note');
    if (!notes.length && !over.size) return;

    const now = new Set([...notes].filter((note) => {
      const box = note.getBoundingClientRect();

      return x >= box.left && x <= box.right && y >= box.top && y <= box.bottom;
    }));

    for (const note of now) if (!over.has(note)) note.holdToast?.();
    for (const note of over) if (!now.has(note)) note.releaseToast?.();

    over = now;
  };

  document.addEventListener('pointermove', (event) => {
    if (event.pointerType !== 'touch') follow(event.clientX, event.clientY);
  }, { passive: true });

  // Out of the window altogether, which no pointermove reports.
  document.documentElement.addEventListener('pointerleave', () => follow(-1, -1));
}

/** A drawing from the sprite in the document, for script that builds a control. */
function spriteIcon(id, className = '') {
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');

  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');

  if (className) svg.setAttribute('class', className);

  const use = document.createElementNS(ns, 'use');
  use.setAttribute('href', `#${id}`);
  svg.append(use);

  return svg;
}

/**
 * Reports a failed request in the corner: the sentence, and folded away under it
 * what could not be turned into one.
 *
 * toast takes that as its third argument, and most callers left it out - so an
 * internal failure, whose sentence says the technical details are underneath,
 * arrived with nothing underneath and without the reference that finds its log
 * line. One function builds both halves, so a caller cannot pass the first and
 * forget the second. lead names what failed, in front of the refusal's own
 * sentence.
 */
function toastFailure(err, lead = '') {
  const message = lead ? `${lead}: ${err.message}` : err.message;

  toast(message, 'error', refusalDetail(err.refusal));
}

/** How many notices may be on screen, and how long each one stays. */
const TOAST_LIMIT = 4;
const TOAST_MIN_MS = 4000;
const TOAST_MAX_MS = 20000;
const TOAST_MS_PER_CHAR = 60;

/** How long a notice takes to leave. */
const TOAST_LEAVE_MS = 200;

/**
 * The placeholders an administrator may put in the texts they configure.
 *
 * The banner, the footer and the legal notice are written once and then read for
 * years, and the things that date them are exactly the things nobody remembers to
 * come back and change: a copyright year, a version number, the instance's own
 * name after somebody renames it. So those are written as a placeholder and
 * worked out when the page is drawn.
 *
 * Deliberately few. Every one of these is a thing somebody would otherwise type
 * as a literal and be wrong about later; anything else belongs in the text
 * itself.
 */
function placeholderValues() {
  const now = new Date();

  return {
    year: String(now.getFullYear()),
    date: fmtDate(todayISO()),
    time: new Intl.DateTimeFormat(activeLocale(), {
      hour: '2-digit', minute: '2-digit',
    }).format(now),
    version: lastBranding.version ?? '',
    instance: named(lastBranding.title),
  };
}

/**
 * Writes a configured text into an element: placeholders filled in, links made
 * into links, and everything else left as the words somebody typed.
 *
 * Not a rich text editor, and not HTML. The three texts this renders are shown on
 * the sign-in screen, before anybody has authenticated - so whatever an
 * administrator writes here is rendered for every visitor, including the next
 * administrator. Accepting HTML would mean accepting a script tag from anyone
 * holding settings:manage, and sanitising HTML correctly is a problem that is
 * never finished.
 *
 * What is actually wanted is a date that stays current and the occasional link.
 * Both fit in a grammar small enough to read in one line: [words](address). It is
 * built as DOM nodes rather than markup, so there is no parser to be wrong and
 * nothing is ever assigned as innerHTML.
 */
function renderConfiguredText(target, text) {
  if (!target) return;

  target.replaceChildren();

  const filled = fillPlaceholders(text ?? '');

  if (!filled) {
    target.hidden = true;

    return;
  }

  target.hidden = false;

  // [label](url), and everything between the matches as plain text.
  const pattern = /\[([^\]]+)\]\(([^)\s]+)\)/g;
  let at = 0;

  for (const match of filled.matchAll(pattern)) {
    if (match.index > at) {
      target.append(document.createTextNode(filled.slice(at, match.index)));
    }

    target.append(configuredLink(match[1], match[2]));
    at = match.index + match[0].length;
  }

  if (at < filled.length) target.append(document.createTextNode(filled.slice(at)));
}

/** Replaces {year} and its companions with what they mean right now. */
function fillPlaceholders(text) {
  const values = placeholderValues();

  return String(text).replace(/\{(\w+)\}/g, (whole, name) => (
    Object.hasOwn(values, name) ? values[name] : whole
  ));
}

/**
 * One link, or the words on their own where the address is not one this should
 * follow.
 *
 * Three schemes and no others. javascript: is the obvious one to refuse and data:
 * is the one that gets forgotten - both would turn a line of configured text into
 * something that runs.
 */
function configuredLink(label, url) {
  let parsed;

  try {
    parsed = new URL(url, window.location.origin);
  } catch {
    return document.createTextNode(label);
  }

  if (!['http:', 'https:', 'mailto:'].includes(parsed.protocol)) {
    return document.createTextNode(label);
  }

  return el('a', {
    href: parsed.href,
    text: label,
    // Somebody else's page opened from ours has no business reaching back into
    // it, and an administrator's link is still somebody else's page.
    rel: 'noreferrer noopener',
    target: '_blank',
  });
}

/**
 * The attributes whose presence is the whole of their meaning.
 *
 * setAttribute('disabled', false) disables the control: the value is never read,
 * only whether the attribute is there at all. Every right on the roles screen was
 * rendered with disabled="false" and could not be ticked - so no role could be
 * given a permission through the interface, which is a feature that had simply
 * stopped working.
 */
const BOOLEAN_ATTRIBUTES = new Set([
  'disabled', 'readonly', 'required', 'hidden', 'multiple', 'selected',
  'autofocus', 'open', 'novalidate',
]);

/** Builds an element; text is assigned via textContent, never innerHTML. */
function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (key === 'class') node.className = value;
    else if (key === 'text') node.textContent = value;
    else if (key === 'checked') node.checked = value;
    else if (key.startsWith('on')) node.addEventListener(key.slice(2), value);
    // Present or absent, never "false". aria-* is deliberately not in that set:
    // there the string is read, and aria-current="false" means something.
    else if (BOOLEAN_ATTRIBUTES.has(key)) { if (value) node.setAttribute(key, ''); }
    else if (value !== null && value !== undefined) node.setAttribute(key, value);
  }
  for (const child of children) {
    if (child !== null && child !== undefined) node.append(child);
  }
  return node;
}

/**
 * Gives the focus back to a button that is turned off while its request runs.
 *
 * A focused button that becomes disabled loses the focus to the page, so
 * somebody who pressed it from the keyboard was afterwards nowhere, and the next
 * Tab began again at the top. Asked before the button is turned off; what it
 * returns is called once the button is back, and leaves the focus alone if it
 * has gone somewhere on purpose in the meantime.
 */
function holdFocus(button) {
  const had = document.activeElement === button;

  return () => {
    if (had && (!document.activeElement || document.activeElement === document.body)) {
      button.focus();
    }
  };
}

/**
 * What a ticket's state is called, in the reader's language.
 *
 * The states that ship are named by identifiers - pending-reminder - which the
 * dictionaries translate and SHIPPED_STATE_TITLES gives the English of; a state
 * an installation adds is shown as it was typed, which is the only sensible
 * answer for a word this application has never seen.
 */
function statusName(status) {
  return t(`status.${status}`, SHIPPED_STATE_TITLES[status] ?? status);
}

/**
 * What a kind of record is called, in the reader's language - the word a
 * refusal names when what it looked for is not there. The server's own word is
 * the English, so a language without the entry still says something true.
 */
function entityName(entity) {
  return t(`entity.${entity}`, entity);
}

/**
 * A ticket's state as a badge: named in the reader's language, coloured by its
 * type - an installation names its own states, and a colour rule written for a
 * name would know none of them.
 */
function statusBadge(name, type) {
  return el('span', { class: `status status-${type}`, text: statusName(name) });
}

/**
 * A date in the reader's own language.
 *
 * Was hard-coded to d.m.Y, which is right in German and wrong everywhere else -
 * an English reader saw 03.08.2026 and had to work out which number was the
 * month. Intl knows every locale's order, and the date is built in UTC so a zone
 * behind the line cannot shift it to the previous day: the string is a calendar
 * day, not a moment.
 */

/**
 * Keeps a stylesheet property equal to the height of something that other parts
 * of the page make room for.
 *
 * Two things are: the sticky bar, which the stylesheet reserves scroll-padding-top
 * for so that anything scrolled to the top of the window lands below it rather
 * than behind it - a click, a keyboard focus, an in-page anchor, scrollIntoView -
 * and the footer, which the sign-in screen stops short of while the footer is
 * raised above it. The first reservation was a number somebody measured once.
 *
 * Both are wrapping rows. Their height depends on the width of the window and on
 * everything in them, so a narrow window turns one row into three, and the
 * reservation silently stops covering it. What that looks like is a tab that does
 * not open: the click lands on the bar sitting over it.
 *
 * So it is measured, and re-measured whenever the element changes shape.
 */
function trackHeight(selector, property) {
  const element = document.querySelector(selector);
  if (!element) return;

  const apply = () => {
    const height = Math.ceil(element.getBoundingClientRect().height);
    if (height > 0) document.documentElement.style.setProperty(property, `${height}px`);
  };

  apply();

  // Everything that changes it: the window's width, a logo arriving, a tab
  // appearing when rights change, a language whose words are longer, a footer
  // text configured in the settings.
  if (typeof ResizeObserver === 'function') {
    new ResizeObserver(apply).observe(element);

    return;
  }

  window.addEventListener('resize', apply);
}

/**
 * One of the configured texts, in the language the reader is being served.
 *
 * The rule is the one that governs everything else about language here: what the
 * switcher says, and the browser's own setting when nobody has switched. A
 * language with nothing written for it falls back to the base text rather than to
 * nothing - an installation that works in one language wrote that one, and a
 * German reader is better served by a German company's German banner than by a
 * blank strip.
 */
function brandingIn(branding, field) {
  const written = branding.translations?.[activeLanguage()]?.[field];

  return written || branding[field] || '';
}

/** What a language is called, in its own words. */
function languageName(language) {
  return LANGUAGES[language] ?? language;
}

/**
 * Choosing which part of the logo the bar draws.
 *
 * A logo is uploaded once and drawn in one place, the left of the bar, and the
 * part worth showing there is not always the whole: a wordmark with a symbol at
 * one end can read better as the symbol alone. Nobody can guess which part, so
 * it is chosen here. It is filed under the banner's name, because the bar draws
 * the copy the sign-in card used to draw.
 *
 * The selection is free: any part, in any shape, from any corner. It opens on
 * the shape the bar gives a logo, because that is the one selection nothing has
 * to be done to, but nothing holds it there.
 *
 * A chosen part that is not that shape is fitted into the place rather than
 * stretched to fill it - scaled until it fits and left at its own proportions,
 * which is what the bar does with it. So the preview shows it fitted too, and
 * what is on this screen is what will be on the bar.
 */
const CROP_SHAPES = {
  banner: { width: 656, height: 192, label: () => t('admin.logoInHeader', 'In the header') },
};

/** What has been chosen, per place, while the form is open. */
let logoCrops = {};

/** Which place the chooser is currently open for. */
let croppingFor = '';

/** The selection, in fractions of the image, while it is being dragged. */
let cropBox = { x: 0, y: 0, w: 1, h: 1 };

/**
 * The smallest selection either side may be dragged to.
 *
 * Not a matter of taste: a selection dragged to nothing is one that cannot be
 * seen, cannot be grabbed again, and would be scaled up into a place from almost
 * no pixels at all.
 */
const MIN_CROP = 0.02;

function openCropChooser(use) {
  const overlay = $('#crop-overlay');
  const image = $('#crop-image');
  if (!overlay || !image || !pendingLogo) return;

  croppingFor = use;
  image.src = pendingLogo;

  $('#crop-text').textContent = fillIn(
    t('crop.text', 'Drag to choose the part of the logo used here: {0}.'),
    [CROP_SHAPES[use]?.label() ?? use]);

  // Shown first, and measured after.
  //
  // A hidden element has no size, so a selection drawn before this line came out
  // as the two pixels of its own border - present, correct in every way a test
  // could ask about, and impossible to aim at.
  overlay.hidden = false;

  // Whatever was chosen before, or - with nothing chosen yet - the largest box
  // of this place's shape that fits, which for a wide header is the whole logo
  // and for a tab is a square of it. A starting point rather than a rule: it is
  // the one selection that needs no fitting, and every corner drags away from it.
  const begin = () => {
    cropBox = logoCrops[use] ?? largestBoxOfShape(use, image);
    drawCropBox();
  };

  if (image.complete && image.naturalWidth) begin();
  else image.onload = begin;

  // Again on the next frame: the image may be decoded already while the overlay
  // it was just put into has not been laid out yet, and the first measurement
  // would then be of a box with no width.
  requestAnimationFrame(() => {
    if (croppingFor === use && image.naturalWidth) drawCropBox();
  });
}

/**
 * The largest box of the wanted shape that fits inside the image, centred.
 *
 * The starting point when nothing has been chosen, and what "the whole logo"
 * means for a place whose shape the logo does not have: a square tab cannot show
 * a two-to-one wordmark whole, so the honest answer is as much of it as a square
 * can hold.
 */
function largestBoxOfShape(use, image) {
  const shape = CROP_SHAPES[use];

  if (!shape || !image.naturalWidth || !image.naturalHeight) {
    return { x: 0, y: 0, w: 1, h: 1 };
  }

  const wanted = shape.width / shape.height;
  const actual = image.naturalWidth / image.naturalHeight;

  if (actual > wanted) {
    const w = wanted / actual;

    return { x: (1 - w) / 2, y: 0, w, h: 1 };
  }

  const h = actual / wanted;

  return { x: 0, y: (1 - h) / 2, w: 1, h };
}

/** Puts the selection on screen, where the image actually is. */
function drawCropBox() {
  const image = $('#crop-image');
  const box = $('#crop-box');
  if (!image || !box) return;

  // Measured against the drawn image rather than its element: object-fit leaves
  // bands at the sides or the top, and a selection placed against the element
  // would be off by exactly those bands.
  const area = drawnImageArea(image);

  box.style.left = `${area.left + cropBox.x * area.width}px`;
  box.style.top = `${area.top + cropBox.y * area.height}px`;
  box.style.width = `${cropBox.w * area.width}px`;
  box.style.height = `${cropBox.h * area.height}px`;
}

/** Where inside its element the image is actually drawn. */
function drawnImageArea(image) {
  const width = image.clientWidth;
  const height = image.clientHeight;

  if (!image.naturalWidth || !image.naturalHeight) {
    return { left: 0, top: 0, width, height };
  }

  const scale = Math.min(width / image.naturalWidth, height / image.naturalHeight);
  const drawnWidth = image.naturalWidth * scale;
  const drawnHeight = image.naturalHeight * scale;

  return {
    left: image.offsetLeft + (width - drawnWidth) / 2,
    top: image.offsetTop + (height - drawnHeight) / 2,
    width: drawnWidth,
    height: drawnHeight,
  };
}

function wireCropChooser() {
  const overlay = $('#crop-overlay');
  const stage = $('#crop-stage');
  const box = $('#crop-box');
  const handles = $$('.crop-handle');
  if (!overlay || !stage || !box || !handles.length) return;

  for (const button of $$('.logo-use-button')) {
    button.addEventListener('click', () => openCropChooser(button.dataset.crop));
  }

  const close = () => {
    overlay.hidden = true;
    croppingFor = '';
  };

  $('#crop-cancel').addEventListener('click', close);

  $('#crop-whole').addEventListener('click', () => {
    // The whole logo for this place, which for a square tab means as much of it
    // as a square can hold rather than a squashed wordmark.
    delete logoCrops[croppingFor];
    setLogoPreview(pendingLogo);
    close();
  });

  $('#crop-apply').addEventListener('click', () => {
    logoCrops[croppingFor] = { ...cropBox };
    setLogoPreview(pendingLogo);
    close();
  });

  // Dragging the box moves it; dragging a corner resizes it from that corner.
  // Both stay inside the image, and neither constrains the shape.
  let mode = '';
  let corner = '';
  let from = { x: 0, y: 0 };
  let start = cropBox;

  const grab = (kind) => (e) => {
    mode = kind;
    corner = e.currentTarget.dataset.corner ?? '';
    from = { x: e.clientX, y: e.clientY };
    start = { ...cropBox };

    e.preventDefault();
    e.stopPropagation();
    stage.setPointerCapture(e.pointerId);
  };

  box.addEventListener('pointerdown', grab('move'));

  for (const grip of handles) {
    grip.addEventListener('pointerdown', grab('resize'));
  }

  stage.addEventListener('pointermove', (e) => {
    if (!mode) return;

    const area = drawnImageArea($('#crop-image'));
    if (!area.width || !area.height) return;

    const dx = (e.clientX - from.x) / area.width;
    const dy = (e.clientY - from.y) / area.height;

    cropBox = mode === 'move'
      ? moveCropBox(start, dx, dy)
      : resizeCropBox(start, dx, dy, corner);

    drawCropBox();
  });

  for (const ending of ['pointerup', 'pointercancel']) {
    stage.addEventListener(ending, () => { mode = ''; });
  }

  // The stage is a share of the window, so the selection has to be redrawn when
  // that changes - otherwise it keeps the pixels it had and points at the wrong
  // part of the logo.
  window.addEventListener('resize', () => {
    if (croppingFor) drawCropBox();
  });
}

/** Moves the selection, keeping it inside the image. */
function moveCropBox(start, dx, dy) {
  return {
    ...start,
    x: Math.min(Math.max(start.x + dx, 0), 1 - start.w),
    y: Math.min(Math.max(start.y + dy, 0), 1 - start.h),
  };
}

/**
 * Resizes the selection from one of its corners, freely.
 *
 * The corner being dragged follows the pointer on both axes and the opposite one
 * stays where it is, so any part of the logo can be enclosed in any shape. The
 * two axes are worked out separately - a corner is two edges, and holding one of
 * them back because the other ran into the image's edge would make the drag stick
 * for reasons nothing on the screen explains.
 */
function resizeCropBox(start, dx, dy, corner) {
  const right = start.x + start.w;
  const bottom = start.y + start.h;

  // Held between the image's edge and the smallest selection worth having, which
  // is what keeps a corner dragged past its opposite from turning the selection
  // inside out.
  const [x, w] = corner.includes('w')
    ? (() => {
      const moved = Math.min(Math.max(start.x + dx, 0), right - MIN_CROP);

      return [moved, right - moved];
    })()
    : [start.x, Math.min(Math.max(start.w + dx, MIN_CROP), 1 - start.x)];

  const [y, h] = corner.includes('n')
    ? (() => {
      const moved = Math.min(Math.max(start.y + dy, 0), bottom - MIN_CROP);

      return [moved, bottom - moved];
    })()
    : [start.y, Math.min(Math.max(start.h + dy, MIN_CROP), 1 - start.y)];

  return { x, y, w, h };
}

/** The languages the appearance texts can be written in: every one there is. */
const BRANDING_LANGUAGES = Object.keys(LANGUAGES);

/**
 * Offers the appearance texts in each of those languages, each by what it calls
 * itself - so a language whose dictionary arrives is a language its texts can be
 * written in, with nothing in the markup to add.
 */
function offerBrandingLanguages() {
  $('#branding-language')?.replaceChildren(...BRANDING_LANGUAGES.map((language) =>
    el('option', { value: language, text: languageName(language) })));
}

/** The texts that translate, per language, while the form is open. */
let brandingDraft = {};

/**
 * The texts as they were before any translation existed.
 *
 * They stay the base: what a reader gets when their language has nothing written
 * for it, and what the server puts in the document before anybody has chosen a
 * language. An installation working in one language fills these in and never
 * opens the switcher.
 */
let brandingBase = {};

/** The fields the language switcher swaps. */
const BRANDING_TEXT_FIELDS = ['title', 'tabTitle', 'banner', 'footerText', 'legalNotice'];

/**
 * Puts one language's texts into the form, keeping whatever was typed in the one
 * being left.
 */
function showBrandingLanguage(language) {
  // What is in the boxes belongs to the language it was typed in, and has to be
  // taken back into the draft before another language's words replace it.
  rememberBrandingDraft();
  fillBrandingBoxes(language);
}

/**
 * Puts one language's words into the boxes, without taking anything back first.
 *
 * Split out of showBrandingLanguage for the one caller that must not take
 * anything back: restoring a draft after a reload has just put the whole set of
 * languages into place, and the boxes at that moment hold the server's copy.
 * Capturing them would file the server's words under whichever language was
 * about to be shown and lose what had actually been written - which is what it
 * did, silently, in the half of this that was not being looked at.
 */
function fillBrandingBoxes(language) {
  const form = $('#form-branding');
  const picker = $('#branding-language');
  if (!form || !picker) return;

  const chosen = BRANDING_LANGUAGES.includes(language) ? language : BRANDING_LANGUAGES[0];

  picker.value = chosen;
  brandingLanguage = chosen;

  for (const field of BRANDING_TEXT_FIELDS) {
    // The base where this language has nothing of its own, so somebody adding a
    // translation starts from what is already on the screen rather than from an
    // empty box.
    form.elements[field].value = brandingDraft[chosen]?.[field] || brandingBase[field] || '';
  }
}

/** Which language the form is currently showing. */
let brandingLanguage = 'en';

/**
 * What a reader gets in a language nothing has been written for.
 *
 * The first language's box is the base itself, so it is read from there while
 * the form is open and from what was loaded before anybody typed.
 */
function brandingFallback(field) {
  return brandingDraft[BRANDING_LANGUAGES[0]]?.[field] || brandingBase[field] || '';
}

/** Takes what is in the form back into the draft for the language it belongs to. */
function rememberBrandingDraft() {
  const form = $('#form-branding');
  if (!form || !brandingDraft[brandingLanguage]) return;

  for (const field of BRANDING_TEXT_FIELDS) {
    const typed = form.elements[field].value;

    // The first language is the base, so whatever is in the form is it.
    if (brandingLanguage === BRANDING_LANGUAGES[0]) {
      brandingDraft[brandingLanguage][field] = typed;

      continue;
    }

    // Any other language holds a translation, and one that says exactly what
    // the base says is not a translation - it is the base, copied.
    //
    // Storing it froze the language. The form fills each language with the base
    // so that somebody switching to German sees what a German reader currently
    // gets; saving then wrote that back as German's own answer, and renaming the
    // installation afterwards reached every language except the ones that had
    // been looked at. Leaving a field as it was found has to mean what it looks
    // like it means: nothing written here, so the default applies - the same
    // rule the logo follows.
    brandingDraft[brandingLanguage][field] = typed === brandingFallback(field) ? '' : typed;
  }
}

function fmtDate(iso) {
  if (!iso) return '–';

  const [y, m, d] = iso.split('-').map(Number);
  const at = new Date(Date.UTC(y, m - 1, d));

  if (Number.isNaN(at.getTime())) return iso;

  return new Intl.DateTimeFormat(activeLocale(), {
    day: '2-digit', month: '2-digit', year: 'numeric', timeZone: 'UTC',
  }).format(at);
}

/**
 * A moment - a day and a time of day - in the reader's own convention.
 *
 * Deliberately not fmtDate, and the difference is not cosmetic. That one renders a
 * calendar day and pins the zone to UTC, because a stored day carries no zone and
 * must not be shifted by one. These are instants, and the question they answer
 * - when was this credential last used - only means anything in the zone the
 * reader lives in.
 *
 * fmtDate cannot be reused for them in any case: it splits an ISO string on its
 * hyphens, so a timestamp makes its third field NaN and the raw wire format
 * reaches the screen.
 *
 * Minutes and no finer. A token's last use is read to work out whether somebody
 * else has it, and seconds add nothing to that while making the column wider than
 * the answer.
 */
function fmtMoment(iso) {
  if (!iso) return '–';

  const at = new Date(iso);

  if (Number.isNaN(at.getTime())) return iso;

  return new Intl.DateTimeFormat(activeLocale(), {
    day: '2-digit', month: '2-digit', year: 'numeric',
    hour: '2-digit', minute: '2-digit',
    // The account's own zone, falling back to the browser's before sign-in and on
    // an installation that never chose one.
    timeZone: me.user?.effectiveTimezone || undefined,
  }).format(at);
}

/**
 * A figure with two decimal places, written the way the reader writes one.
 *
 * toFixed always produces a dot, so every hour figure on screen read 5.00 h to a
 * German reader looking at a screen that was otherwise entirely German - beside
 * date fields that did use the right convention, which is what made it look like
 * a rendering fault rather than a decision.
 *
 * Display only. Nothing here goes back to the server or into a form field: a
 * comma would be a different number to JSON, and the one place a comma is read
 * *in* is the spreadsheet importer, which handles both conventions on the way
 * through.
 */
function fmtNumber(n) {
  if (typeof n !== 'number' || !Number.isFinite(n)) return String(n ?? '');

  return new Intl.NumberFormat(activeLocale(), {
    minimumFractionDigits: 2, maximumFractionDigits: 2,
  }).format(n);
}

/**
 * A share between none and all, written the way the reader writes a number and
 * in full.
 *
 * Not fmtNumber: two fixed places turn a limit of 0.125 into 0,13, which is a
 * setting nobody made. Fifteen significant digits are what a decimal typed into
 * a configuration file survives as a double, so 0.1 comes back as 0,1 rather
 * than as the binary fraction under it. Display only, like fmtNumber.
 */
function fmtShare(n) {
  if (typeof n !== 'number' || !Number.isFinite(n)) return String(n ?? '');

  return new Intl.NumberFormat(activeLocale(), { maximumSignificantDigits: 15 }).format(n);
}

/**
 * A number of hours, with the unit the reader uses for one.
 *
 * The unit was a literal "h" appended here, which is a word - a short one that
 * happens to be the same in English and German, which is exactly why it survived
 * every pass over the translations. It is looked up like anything else now, so a
 * language that abbreviates hours differently has somewhere to say so.
 */
const fmtHours = (n) => `${fmtNumber(n)} ${t('unit.hours', 'h')}`;

/** Replaces a tbody with rows, or a single "empty" row when there are none. */
function fillTable(tbody, rows, columnCount, emptyText) {
  // Before the rows go in, while they can still be read for delete buttons.
  const picking = prepareBulkDelete(tbody, rows);

  tbody.replaceChildren();

  if (rows.length === 0) {
    tbody.append(el('tr', {},
      el('td', { class: 'empty', colspan: columnCount + (picking ? 1 : 0), text: emptyText })));
  } else {
    tbody.append(...rows);
  }

  // Only now, with the new rows in the document. The bar reads how many checkboxes
  // are ticked out of the table, so refreshing it before this line counted the rows
  // of the previous render: delete two of three, and the bar stayed up saying
  // "3 selected" over a table where nothing was ticked at all.
  if (picking) refreshBulkBar(tbody.closest('table'));
}

/**
 * The bar belonging to one table.
 *
 * Found by the table's own id rather than by position, so a card that grows a
 * second table later cannot end up driving the wrong bar.
 */
function bulkBarOf(table) {
  const card = table.parentElement && table.parentElement.parentElement;

  return card ? card.querySelector(`.bulk-bar[data-for="${table.id}"]`) : null;
}

/**
 * Gives a table a leading column of checkboxes, when any of its rows offers
 * deletion, plus the bar that acts on them.
 *
 * Driven entirely by what the rows contain, so there is no list anywhere of which
 * tables have this. A table of rows nobody may delete - the preview of a file about
 * to be imported, the runs of the directory synchronisation - is left exactly as it
 * was, and a table that grows a delete button later gets this without being told.
 *
 * Returns whether the column is there, because the empty row's colspan has to
 * know.
 */
function prepareBulkDelete(tbody, rows) {
  const table = tbody.closest('table');
  if (!table) return false;

  // Which rows carry a deletion, and what it is. Every .danger button is
  // considered, not just the first: a row may offer more than one dangerous
  // thing, and only one of them is the deletion.
  const deletions = new Map();
  for (const row of rows) {
    if (!row.querySelectorAll) continue;

    for (const button of row.querySelectorAll('button.danger')) {
      if (button.deletes) {
        deletions.set(row, button.deletes);
        break;
      }
    }
  }

  const head = table.tHead && table.tHead.rows[0];
  const column = head && head.querySelector('th.pick');

  if (deletions.size === 0) {
    // Nothing deletable this time round - a filter that matched only other
    // people's entries, say. The column goes rather than standing there with
    // nothing in it.
    if (column) column.remove();

    const bar = bulkBarOf(table);
    if (bar) bar.remove();

    return false;
  }

  for (const row of rows) {
    const deletes = deletions.get(row);

    // A row nobody may delete still gets the cell, so the columns line up. It
    // simply has nothing in it to tick.
    const cell = el('td', { class: 'pick' });

    if (deletes) {
      const box = el('input', {
        type: 'checkbox',
        class: 'row-pick',
        'aria-label': `${t('bulk.pick', 'Select')}: ${deletes.label}`,
        onchange: () => refreshBulkBar(table),
      });

      box.deletes = deletes;

      // In a label, which passes a tap on to its box: the room around the box is
      // what makes it something a finger can hit, and the box keeps its size.
      cell.append(el('label', { class: 'pick-hit' }, box));
    }

    row.prepend(cell);
  }

  if (!column && head) {
    head.prepend(el('th', { class: 'pick' }, el('label', { class: 'pick-hit' }, el('input', {
      type: 'checkbox',
      class: 'pick-all',
      'aria-label': t('bulk.pickAll', 'Select all'),
      onchange: (event) => {
        for (const box of table.querySelectorAll('tbody .row-pick')) {
          box.checked = event.target.checked;
        }

        refreshBulkBar(table);
      },
    }))));
  }

  ensureBulkBar(table);

  // The bar is left to fillTable to refresh, once the rows are actually in the
  // document. The rows are new, so nothing is ticked and the bar hides itself:
  // carrying a selection across a reload would be worse than losing it, because
  // the rows underneath it need not be the same rows.
  return true;
}

/** The bar that says how many rows are ticked and offers to delete them. */
function ensureBulkBar(table) {
  const existing = bulkBarOf(table);
  if (existing) return existing;

  const wrap = table.parentElement;
  if (!wrap || !wrap.parentElement) return null;

  const bar = el('div', { class: 'bulk-bar', 'data-for': table.id },
    el('span', { class: 'bulk-count' }),
    el('button', {
      // No text here: refreshBulkBar writes it, and writes it again every time
      // the bar is refreshed. Set once at creation it survived a language change
      // as the old language's word, over a count beside it in the new one.
      class: 'danger',
      onclick: () => runBulkDelete(table),
    }));

  // Above the table rather than below it: in a long table the bar would be off
  // the screen, and the rows being ticked start at the top.
  wrap.parentElement.insertBefore(bar, wrap);

  return bar;
}

function refreshBulkBar(table) {
  const bar = bulkBarOf(table);
  if (!bar) return;

  const boxes = [...table.querySelectorAll('tbody .row-pick')];
  const picked = boxes.filter((box) => box.checked);

  bar.classList.toggle('shown', picked.length > 0);
  bar.querySelector('.bulk-count').textContent = t('bulk.count', '{n} selected')
    .replace('{n}', String(picked.length));
  bar.querySelector('button.danger').textContent = t('bulk.delete', 'Delete selected');

  const all = table.querySelector('thead .pick-all');
  if (all) {
    all.checked = boxes.length > 0 && picked.length === boxes.length;

    // Some but not all: neither ticked nor empty is the truth, and the browser
    // draws that third state itself.
    all.indeterminate = picked.length > 0 && picked.length < boxes.length;
  }
}

/**
 * Deletes every ticked row, one after another.
 *
 * Sequentially, and through the ordinary single-row endpoint. There is no bulk
 * endpoint on purpose: each row goes through the same rules, ownership check and
 * audit entry as a single deletion, so a batch can never delete something the
 * reader could not have deleted on its own. Firing them all at once would race
 * for the same rows and, on SQLite, mostly queue behind each other anyway.
 *
 * A refusal partway does not stop the rest. Eight of ten deleted, with the two
 * refusals named, is more use than eight silently undone.
 */
async function runBulkDelete(table) {
  const picked = [...table.querySelectorAll('tbody .row-pick')]
    .filter((box) => box.checked)
    .map((box) => box.deletes);

  if (picked.length === 0) return;

  const shown = picked.slice(0, 5).map((item) => item.label);
  if (picked.length > shown.length) shown.push('…');

  const ok = await confirmDialog({
    title: t('bulk.title', 'Delete the selected rows?'),
    text: t('bulk.text', 'This cannot be undone.'),
    detail: shown.join(', '),
    confirmLabel: t('bulk.delete', 'Delete selected'),
  });

  if (!ok) return;

  const refused = [];
  let done = 0;

  for (const item of picked) {
    try {
      await api(item.path, { method: 'DELETE' });
      done += 1;
    } catch (err) {
      refused.push(`${item.label}: ${err.message}`);
    }
  }

  if (refused.length > 0) {
    toast(`${t('bulk.refused', '{n} could not be deleted').replace('{n}', String(refused.length))}`
      + ` – ${refused[0]}`, 'error');
  } else {
    toast(t('bulk.done', '{n} deleted').replace('{n}', String(done)), 'ok');
  }

  // One reload for the batch, along the row's own reload path: whatever a single
  // deletion refreshes is what a batch of them refreshes.
  if (picked[0].after) await picked[0].after();
}

/**
 * The "delete" button of a single row - and with it the row's ticket to being
 * deleted in a batch.
 *
 * Every table built this button at its own call site, which was fine while
 * deletion was one row at a time. Selecting several needs to know which rows may
 * be deleted at all, and that question is already answered precisely here: this
 * button exists only where the row builder decided the reader is allowed to
 * delete that row. Deriving the checkbox from the button means the two cannot
 * disagree - no second copy of "is this allowed?", no checkbox offering a
 * deletion the server would refuse.
 *
 * `ask` replaces the confirmation for a row whose single deletion asks something
 * extra; the batch still takes the plain path. `text` is for a table that calls
 * it something else, like revoking a token.
 *
 * The details ride on the node as a property rather than an attribute, so
 * `after` can stay a function.
 */
function deleteButton({ label, path, message, after, ask, text }) {
  const button = el('button', {
    class: 'link danger',
    text: text || t('action.delete', 'delete'),
    onclick: ask || (() => removeAfterConfirm(label, path, message, after)),
  });

  button.deletes = { label, path, message, after };

  return button;
}

/** Fills a <select> with options, preserving the current selection if possible. */
function fillSelect(select, items, { placeholder, labelKey = 'name', valueKey = 'id' } = {}) {
  if (!select) return;

  // Marked by the thing that fills it, so signing out knows which lists hold an
  // account's own data and which came with the markup. See forgetTheLastAccount:
  // emptying every select there would take the log levels, the languages and the
  // appearance with them, and nothing refills those.
  select.dataset.filled = '';

  const previous = select.value;
  select.replaceChildren();
  if (placeholder) select.append(el('option', { value: '', text: placeholder }));
  for (const item of items) {
    select.append(el('option', { value: String(item[valueKey]), text: item[labelKey] }));
  }
  if (previous && [...select.options].some((o) => o.value === previous)) {
    select.value = previous;
  }
}

/**
 * What each right is called, in words.
 *
 * The identifiers are what the API takes and what a role's rights are stored as -
 * "tickets:write" - and they were what the ticking boxes said. That asks
 * somebody deciding what a colleague may do to read a namespace: the difference
 * between "tickets:create" and "tickets:write" is plain once you know the
 * system and a guess before that.
 *
 * The identifier stays beside the words rather than being replaced by them. It is
 * what an API caller and a directory configuration deal in, so the screen that
 * hands rights out is the one place it has to stay readable.
 *
 * English here, translated through perm.<right>; the fallback is what an
 * installation without a translation sees.
 */
const PERMISSION_TITLES = {
  'users:read': 'See accounts',
  'users:write': 'Create and change accounts',
  'users:delete': 'Delete accounts',
  'roles:read': 'See roles',
  'roles:write': 'Create and change roles',
  'settings:manage': 'Administer the installation',
  'queues:write': 'Set up queues',
  'automation:write': 'Set up the automation',
  'communication:write': 'Set up communication',
  'tickets:read': 'See tickets',
  'tickets:create': 'Open tickets',
  'tickets:write': 'Work on tickets',
  'tickets:delete': 'Delete tickets',
  'customers:read': 'See customers',
  'customers:write': 'Create and change customers',
  'faq:read': 'Read the knowledge base',
  'faq:write': 'Write the knowledge base',
  'reports:read': 'Evaluate tickets',
  'catalog:write': 'Set up the general catalog',
  'assets:read': 'See the configuration items',
  'assets:write': 'Create and change configuration items',
  'changes:read': 'See the changes',
  'changes:write': 'Create, move and change changes',
  'portal:use': 'Use the customer portal',
};

/**
 * What each right actually allows, for the legend.
 *
 * One sentence, and specific: "See tickets" leaves open whether that is every
 * ticket there is, which is exactly the question somebody handing out rights
 * is trying to answer.
 */
const PERMISSION_DETAILS = {
  'users:read': 'The list of accounts, their addresses and which role each holds.',
  'users:write': 'Add an account, change a name or an address, assign a role. Not the initial password of an account somebody else set up.',
  'users:delete': 'Remove an account. The tickets it owned stay and lose their owner, and what it wrote in them stays without its name.',
  'roles:read': 'The roles on this screen and what each grants.',
  'roles:write': 'Make a role, change what it grants, remove one nobody holds. The roles shipped with the application stay as they are.',
  'settings:manage': 'The installation itself: the database connection, the directory, the mailboxes tickets arrive through, appearance, maintenance mode, telemetry, the log and the restart. This is the right that makes somebody an administrator.',
  'queues:write': 'Make and change queues and decide which role reaches each - and with them the states, priorities and types tickets are sorted by and the service levels. Whoever holds it reaches every queue.',
  'automation:write': 'Make and change the macros and what runs them. A macro acts as the application on every ticket it is given, in any queue and deleting included - so this reaches further than any right on tickets.',
  'communication:write': 'Make and change the notifications the agents around a ticket are told and the answer templates an article starts from - and the mailboxes still to come.',
  'tickets:read': 'The tickets in the queues this role reaches, with everything written in them. A queue the role does not reach is not there for it.',
  'tickets:create': 'Open a ticket for a customer - one who called, or one to write to - in a queue this role may work in.',
  'tickets:write': 'Answer, note, change state and priority, take a ticket and hand it over, move it to another queue - in the queues this role may work in.',
  'tickets:delete': 'Remove a ticket for good, with everything written in it. For what should never have been one; a ticket that is done is closed instead.',
  'customers:read': 'The organisations and the contacts tickets come from.',
  'customers:write': 'Add and change organisations and contacts, and give a contact access to the customer portal.',
  'faq:read': 'The knowledge base, its internal articles included.',
  'faq:write': 'Write, change and remove knowledge base articles, and decide who may read each.',
  'reports:read': 'Figures over the tickets of the queues this role reaches: how many arrived, how many were closed, how long they took.',
  'catalog:write': 'Add to and change the general catalog - the classes a configuration item can be of, its deployment and incident states and what each means, and the lists its details pick from - and retire a value nobody should pick any more.',
  'assets:read': 'Every configuration item and every version of it, whatever its class.',
  'assets:write': 'Create a configuration item, write its next version and delete it.',
  'changes:read': 'Every change and its work orders, whatever it is about and whoever manages it - and report on a work order given to the account: what was done, its state, when it ran and how long it took.',
  'changes:write': 'Create a change, correct it, move it on as its states allow, and delete it - and plan, hand out and correct its work orders.',
  'portal:use': 'As a customer: open tickets, read and answer one’s own, and read the public knowledge base. Nothing of anybody else’s.',
};

/** The areas rights are grouped under, in the order they are shown. */
const PERMISSION_GROUPS = {
  users: 'Accounts',
  roles: 'Roles',
  settings: 'Settings',
  queues: 'Queues',
  automation: 'Automation',
  communication: 'Communication',
  tickets: 'Tickets',
  customers: 'Customers',
  faq: 'Knowledge base',
  reports: 'Evaluations',
  catalog: 'General catalog',
  assets: 'Configuration items',
  changes: 'Changes',
  portal: 'Customer portal',
};

/** What a right is called, in the reader's language. */
function permissionTitle(right) {
  return t(`perm.${right}`, PERMISSION_TITLES[right] ?? right);
}

/** What a right allows, in the reader's language. */
function permissionDetail(right) {
  return t(`perm.desc.${right}`, PERMISSION_DETAILS[right] ?? '');
}

/** Which area a right belongs to, from the identifier's first part. */
function permissionGroup(right) {
  const area = String(right).split(':')[0];

  return { key: area, label: t(`perm.group.${area}`, PERMISSION_GROUPS[area] ?? area) };
}

// The role somebody gets when nobody has said otherwise: the one that works on
// tickets and administers nothing.
const ORDINARY_ROLE = 'agent';

/**
 * The English titles of the roles that ship.
 *
 * These are what an English reader sees, because English is what a t() fallback is for.
 * Without them the fallback would be the identifier itself, which is not a thing to
 * put in front of somebody deciding what a colleague may do.
 *
 * A role an installation named itself is not in here and falls back to the name it was
 * given, which is the only sensible answer for a word this application has never seen.
 */
const SHIPPED_ROLE_TITLES = {
  admin: 'Administrator',
  agent: 'Agent',
  customer: 'Customer',
};

/**
 * What a role is called, in the reader's language.
 *
 * The name in the database is an identifier: lowercase, hyphenated, English - admin,
 * agent, customer. It is what the API takes, what the directory configuration stores
 * and what the role editor edits. It is not what somebody choosing a colleague's role
 * should have to read, and it was: the dropdown, the account list, the greeting and the
 * confirmation before deleting a role all showed it raw, so a German reader picked
 * between two lowercase English words.
 *
 * Takes the name rather than the role, because most of the places that show one have
 * only the name - an account carries the name of its role, not the role.
 */
function roleTitle(name) {
  if (!name) return '';

  return t(`role.name.${name}`, SHIPPED_ROLE_TITLES[name] ?? name);
}

/**
 * What a role is for, in the reader's language.
 *
 * The description lives in the database, in English, because an administrator writes
 * their own there. The ones that ship with the application are translated instead,
 * keyed on the name - a custom role falls through to whatever was typed, which is the
 * only sensible answer for words this application has never seen.
 */
function roleDescription(role) {
  return t(`role.desc.${role.name}`, role.description || '');
}

/**
 * The roles as something to choose from, each with what it is for.
 *
 * A dropdown of bare identifiers asks somebody to guess: "agent" against "customer" is
 * a difference you can only infer, and the difference is whether that person works on
 * the staff side or only reaches the portal. The list on the Roles screen has always shown the
 * descriptions; the place where the choice is actually made did not.
 *
 * The value stays the identifier, because that is what the API takes.
 */
function roleChoices() {
  // The roles that ship, when nothing has been loaded.
  //
  // The list normally comes from /roles, which is only fetched by somebody
  // holding roles:read - so a picker built from it is empty for anybody who may
  // configure the installation without administering its roles, and empty again
  // whenever that fetch has not happened or did not finish. An empty picker is
  // the one answer that is always wrong: these are seeded, cannot be deleted,
  // and are therefore always a truthful set of choices.
  const roles = cache.roles.length > 0
    ? cache.roles
    : Object.keys(SHIPPED_ROLE_TITLES).map((name) => ({ name }));

  return roles.map((role) => {
    const title = roleTitle(role.name);
    const purpose = roleDescription(role);

    return { name: role.name, label: purpose ? `${title} — ${purpose}` : title };
  });
}

/**
 * The roles an account from the directory may start with.
 *
 * None that administers the installation: the server refuses to store one as
 * the directory's default, because every entry anybody writes to the directory
 * would arrive holding the installation, so offering it would be offering a
 * refusal. The rights are the server's model.AdministeringPermissions, copied
 * because the page cannot ask for them and held to them by a test. Without the
 * role list - which somebody holding settings:manage but
 * not roles:read never loads - the ordinary role is the only one known not to
 * administer, so it is the one choice.
 */
function directoryRoleChoices() {
  const administering = ['settings:manage', 'roles:write', 'users:write'];

  const allowed = cache.roles.length > 0
    ? new Set(cache.roles
      .filter((role) => !(role.permissions ?? []).some((right) => administering.includes(right)))
      .map((role) => role.name))
    : new Set([ORDINARY_ROLE]);

  return roleChoices().filter((choice) => allowed.has(choice.name));
}

/**
 * Where a form's unfinished contents wait out a page load.
 *
 * Reloading is not always a decision. It is a stray F5, a browser deciding the
 * tab has been idle long enough, a certificate prompt, a laptop coming back from
 * sleep - and whatever the reason, everything typed into a form and not yet
 * saved was simply gone, with the form coming back filled from the server as
 * though nobody had touched it. Losing twenty minutes of a legal notice that way
 * is not a small thing, and there is nothing on screen afterwards to say it
 * happened.
 *
 * sessionStorage rather than localStorage, and that is the whole of the rule
 * about how long this lasts: it survives a reload of this tab and dies with the
 * tab. Closing the page is the one way of saying "I am finished with this" that
 * cannot be done by accident, so it is the one that throws the draft away.
 *
 * Cleared on the way out as well, because a draft is the property of whoever
 * typed it and this application does not leave one person's half-written work on
 * screen for the next.
 */
const DRAFT_PREFIX = 'qubitdesk.draft.';

/**
 * Where one account's drafts are kept, apart from anybody else's.
 *
 * A draft is what somebody was part way through writing, and it belongs to
 * them. The store is a browser's, though, and a browser is shared: signing out
 * and letting a colleague sign in on the same machine is an ordinary thing to
 * do at a shared desk, and half a form somebody else had been filling in is
 * theirs to see rather than the next person's.
 *
 * Signing out clears them, which is the first answer and not a complete one: a
 * tab that is closed by a crash, a machine put to sleep and woken by somebody
 * else, a session that ended without anybody pressing anything. So the key
 * carries who wrote it, and a draft belonging to another account is simply not
 * found - whatever did or did not get cleared.
 *
 * Before anybody has signed in there is no account and therefore no draft to
 * restore, which is right: the only form on screen then is the sign-in one, and
 * that one keeps nothing on purpose.
 */
function draftsOf() {
  const who = me.user?.id;

  return who ? `${DRAFT_PREFIX}${who}.` : '';
}

/**
 * The controls a draft is made of.
 *
 * Named ones only: an unnamed control decides what the form shows rather than
 * what it would save, and there is nothing about it worth carrying across a
 * load. Passwords never - a draft is written to storage, and nothing is worth
 * putting a password there for. Files cannot be restored at all: a file input's
 * value is not something a page is allowed to set, for good reasons.
 */
function draftableFields(form) {
  return [...form.elements].filter((field) => field.name
    && !['password', 'file', 'submit', 'button', 'reset'].includes(field.type));
}

/**
 * A form's own identifier, which is not always what form.id answers with.
 *
 * A form exposes its controls as named properties of itself, and those come
 * first: a form holding <input name="id"> answers form.id with that input. Two
 * forms did - the role's, and a time entry's in the time recording this was cloned
 * from - so both wrote their draft under
 * the key "qubitdesk.draft.[object HTMLInputElement]", which is one key for two forms.
 * They overwrote each other, and on the way back in the first of them restored
 * whatever the other had left, then wrote its own emptiness over the top.
 *
 * The attribute is not shadowed by anything.
 */
function formKey(form) {
  return form?.getAttribute('id') ?? '';
}

/**
 * Whether a name belongs to several controls rather than to one.
 *
 * A form's elements answer to a name with the control itself where there is one
 * and with a list where there are several - which is how a set of switches is
 * told from a single switch, without either of them having to declare it.
 */
function sharedName(form, name) {
  const found = form.elements[name];

  return Boolean(found) && !('tagName' in found);
}

/** Reads a form into something that can be written down and put back. */
function draftOf(form) {
  const values = {};

  for (const field of draftableFields(form)) {
    if (field.type === 'radio') {
      if (field.checked) values[field.name] = field.value;

      continue;
    }

    // Several boxes under one name are a set, and what is worth keeping is which
    // of them are ticked. A role's rights are fifteen boxes all called
    // "permissions", told apart by what they carry, so writing down one true or
    // false under that name kept whichever box happened to be last - and put
    // every box in the set back agreeing with it. Ticking the rights for a new
    // role and reloading came back with none of them, which is what this was
    // reported as.
    if (field.type === 'checkbox') {
      if (sharedName(form, field.name)) {
        const ticked = values[field.name] ?? [];

        if (field.checked) ticked.push(field.value);

        // Assigned even when nothing is ticked, so an empty set is written down
        // as one rather than as an absence that restores nothing.
        values[field.name] = ticked;
      } else {
        values[field.name] = field.checked;
      }

      continue;
    }

    values[field.name] = field.value;
  }

  // Stamped with the version that wrote it.
  //
  // A draft is a snapshot of a form, and a form is not a fixed thing: fields are
  // added, a default changes, a card starts filling something in that it used to
  // leave blank. A draft written before such a change is not a record of what
  // somebody typed any more - it is a record of how the screen used to behave,
  // and putting it back re-creates that behaviour on top of the new screen.
  //
  // Which is exactly what happened. The connection card learnt to name the
  // database this process is connected to; a draft from the version before that
  // still held the empty type it used to leave, and restoring it put the card
  // back to naming nothing - on an installation that had just been updated to
  // fix precisely that.
  //
  // Dropped rather than migrated. What is lost is one form somebody had half
  // filled in at the moment they updated, and the alternative is carrying every
  // past shape of every form around for ever.
  // The appearance card keeps more than its boxes.
  //
  // Its texts exist once per language, and only the chosen language's are on
  // screen; the rest are held in memory, so a draft made of the boxes alone
  // would come back as the right words filed under the wrong language. What is
  // carried is therefore the whole set and which one is being looked at, and
  // restoring puts the chooser back before the boxes.
  const version = lastBranding.version ?? '';

  // Which controls were chosen rather than merely filled in.
  //
  // One control cares, and the reason is worth stating here rather than only
  // where it is read: the connection card's type decides which other fields
  // exist, so it follows what this installation is actually connected to until
  // somebody picks something else. That marker lives on the element, and an
  // element does not survive a reload - so without carrying it, a card somebody
  // had set to PostgreSQL came back describing the SQLite the environment
  // happens to run, with their host and user still in the boxes beneath it.
  const chosen = draftableFields(form)
    .filter((field) => field.dataset.chosen !== undefined)
    .map((field) => field.name);

  if (formKey(form) === 'form-branding') {
    return { version, values, chosen, brandingDraft, brandingLanguage };
  }

  return { version, values, chosen };
}

/** Writes down what is in a form, for the next load of this tab. */
function rememberDraft(form) {
  const key = formKey(form);

  if (!key || form.dataset.noDraft !== undefined) return;

  try {
    const mine = draftsOf();
    if (!mine) return;

    window.sessionStorage.setItem(mine + key, JSON.stringify(draftOf(form)));
  } catch {
    // Storage refused, or full. Nothing to do and nothing to say: the form on
    // screen is unaffected, and this was only ever insurance against a reload.
  }
}

/** Throws away a form's draft, once it is no longer unfinished. */
function forgetDraft(form) {
  const key = formKey(form);

  if (!key) return;

  try {
    window.sessionStorage.removeItem(draftsOf() + key);
  } catch {
    // Same reasoning as above.
  }
}

/** Throws away every draft in this tab. */
function forgetEveryDraft() {
  try {
    for (const key of Object.keys(window.sessionStorage)) {
      if (key.startsWith(DRAFT_PREFIX)) window.sessionStorage.removeItem(key);
    }
  } catch {
    // Same reasoning as above.
  }
}

/**
 * Puts back what was in the forms when this tab was last loaded.
 *
 * After the loaders and not before: they fill every form from the server, so a
 * draft restored first would be overwritten by the very thing it exists to
 * survive. Marking the form as being edited is what keeps the loaders off it
 * from here on - see beingEdited.
 *
 * The values are announced the way typing announces them, so everything a form
 * derives from a field follows: which database fields are on screen, which
 * collector box the exporter allows, whether an SSL mode applies at all.
 */
function restoreDrafts() {
  // Whose drafts these are. Nobody's before anybody has signed in, and then
  // there is nothing to put back - see draftsOf.
  const mine = draftsOf();
  if (!mine) return;

  for (const form of $$('form')) {
    const key = formKey(form);

    if (!key || form.dataset.noDraft !== undefined) continue;

    let draft = null;

    try {
      draft = JSON.parse(window.sessionStorage.getItem(mine + key) ?? 'null');
    } catch {
      // Unreadable or not there. Either way there is nothing to put back.
    }

    if (!draft?.values) continue;

    // Written by a version that is no longer running - see draftOf. Cleared as
    // well as skipped, so it does not sit there being skipped for ever.
    if ((draft.version ?? '') !== (lastBranding.version ?? '')) {
      forgetDraft(form);

      continue;
    }

    // The appearance card's language first, so the boxes below land in the
    // language they were typed in rather than over the one the loader chose.
    if (key === 'form-branding' && draft.brandingDraft) {
      brandingDraft = draft.brandingDraft;

      // Filled rather than shown: showBrandingLanguage takes the boxes back into
      // the draft on its way past, and the boxes here hold what the loader just
      // put in them - so it would file the server's words under the language
      // being restored and throw away the ones that were written.
      fillBrandingBoxes(draft.brandingLanguage ?? brandingLanguage);
    }

    const restored = [];

    for (const field of draftableFields(form)) {
      // A disabled control is not somebody's unsaved work; it is something the
      // screen has put beyond reach - a shipped role's rights - and putting a
      // draft into one would be writing over what the server said.
      if (field.disabled || !(field.name in draft.values)) continue;

      const was = draft.values[field.name];

      if (field.type === 'checkbox') {
        field.checked = Array.isArray(was) ? was.includes(field.value) : Boolean(was);
      } else if (field.type === 'radio') {
        field.checked = field.value === was;
      } else {
        field.value = was;
      }

      restored.push(field);
    }

    // Announced once every value is in rather than as each one lands. Handlers
    // derive one field from another here - the database port from the dialect,
    // which fields are on screen at all from the same - and one that runs part
    // way through reads a form half restored.
    for (const field of restored) {
      field.dispatchEvent(new Event('input', { bubbles: true }));
      field.dispatchEvent(new Event('change', { bubbles: true }));
    }

    // And which of them were chosen rather than filled in - see draftOf. Put
    // back after the events above rather than before: those are dispatched from
    // here, so the listeners that would otherwise set this mark deliberately
    // ignore them.
    for (const name of draft.chosen ?? []) {
      const field = form.elements[name];
      if (field) field.dataset.chosen = 'yes';
    }

    form.dataset.editing = 'yes';
  }
}

/**
 * Decides what stands in each of the two mark slots.
 *
 * The bar has three columns and the outer two mean different things: the left
 * one is the installation, the middle one is the application. They used to
 * share a single drawing and show one of it - so a fresh installation wore the
 * application's mark on the left and had a bare title in the middle, and an
 * installation with a logo had it the other way round. The title lost its mark
 * to a slot that was never about the application.
 *
 * Both are always filled now:
 *
 *   middle  the application's own mark, beside the title, always
 *   left    the uploaded logo, or a chip with QubitDesk's Q where there is none
 *
 * The chip carried the first letter of the configured title while that title
 * was the installation's whole name. Every installation is called QubitDesk now
 * and the title only follows it, so the first letter of the title was a letter
 * of the addition - an H beside QubitDesk | htp.net - and the chip carries the
 * name's own initial instead. It still never reads as a picture that failed to
 * load, which is what a generic glyph would.
 */
function showBrandMark(ownLogo) {
  const mark = $('#brand-mark');

  // The attribute rather than the property, because this is an SVG element and
  // `hidden` is defined on HTMLElement: assigning it here creates a property
  // nobody reads and leaves the chip on screen beside the logo that replaced it.
  if (mark) {
    if (ownLogo) mark.setAttribute('hidden', '');
    else mark.removeAttribute('hidden');
  }

  const initial = $('#brand-initial');
  if (initial) initial.textContent = BRAND.charAt(0);
}

/**
 * Whether somebody is part way through filling this form in.
 *
 * Every screen in this application is refilled from the server by loaders that
 * run for reasons having nothing to do with the person in front of them: a
 * language chosen, a neighbouring card saved, a directory synchronised. Each of
 * those refills every form on the way past, and anything typed and not yet saved
 * was replaced by the server's copy without a word.
 *
 * It is not a rare window either. Choosing a language reloads every screen, so
 * an administrator half way through a database connection who switched to German
 * to read a label lost the lot - and the form that came back looked like one
 * nobody had touched, so there was nothing to notice.
 *
 * Somebody who has started typing is answering a question, and the server's copy
 * is what they are answering it against. Replacing their answer with it
 * mid-sentence is the one thing a background refresh must not do. So a form that
 * has been changed and not saved is left alone until it is.
 */
function beingEdited(form) {
  return form?.dataset.editing === 'yes';
}

/**
 * Notices that somebody has started filling a form in.
 *
 * Both events, because they cover different fields: input is typing, and change
 * is what a picker, a checkbox and a file field report. Only what a person did -
 * setting a value from script fires neither, which is exactly the distinction
 * this rests on, and is why the loaders can go on filling forms nobody has
 * touched.
 */
function watchForEditing(form) {
  for (const event of ['input', 'change']) {
    form.addEventListener(event, (e) => {
      // Unless the control only decides what the form shows. The appearance
      // card's language chooser is one: it swaps which language's words are in
      // the boxes, and looking at a translation is not writing one - a card
      // somebody glanced at would otherwise stop following the server for as
      // long as they left the screen open.
      if (e.target?.dataset?.notAnEdit !== undefined) return;

      form.dataset.editing = 'yes';

      // And written down, so a reload does not take it. Here rather than on the
      // way out of the page: a tab that is closed by the browser, by a crash or
      // by a machine going to sleep never runs anything on the way out.
      rememberDraft(form);
    });
  }

  // Discarding is finishing too: a form put back to what it was holds nothing
  // worth protecting, and nothing worth carrying across a load.
  form.addEventListener('reset', () => {
    delete form.dataset.editing;
    forgetDraft(form);
  });
}

/**
 * Puts the browser's own refusals of a field into the screen's language.
 *
 * The forms state their rules in the markup - required, a minimum, a maximum, a
 * length, an address - and the browser holds a form to them before anything is
 * sent, in its own words: on a German screen in an English browser, "Value must
 * be less than or equal to 24." Each refusal is written here instead, from the
 * same rule, in the language the screen is in.
 *
 * A refusal set this way makes a field invalid until it is taken back, which is
 * the part to be careful with. It is taken back when the value changes, when a
 * form is reset, and before every attempt to send one - because a value the
 * script puts in raises no event, and the field would go on refusing a figure its
 * own rule accepts.
 */
function wireValidityMessages() {
  // Captured, because invalid does not bubble.
  document.addEventListener('invalid', (event) => {
    const field = event.target;
    if (typeof field.setCustomValidity !== 'function') return;

    field.setCustomValidity('');

    const message = validityMessage(field);
    if (message) field.setCustomValidity(message);
  }, true);

  const takeBack = (field) => {
    if (typeof field?.setCustomValidity === 'function') field.setCustomValidity('');
  };

  for (const type of ['input', 'change']) {
    document.addEventListener(type, (event) => takeBack(event.target), true);
  }

  document.addEventListener('reset', (event) => {
    for (const field of event.target.elements ?? []) takeBack(field);
  }, true);

  // A button's click comes before its form is checked - Enter in a field presses
  // the form's button too - so the fields are checked afresh, by their own rules.
  document.addEventListener('click', (event) => {
    const form = event.target.closest?.('button, input[type="submit"]')?.form;

    for (const field of form?.elements ?? []) takeBack(field);
  }, true);
}

/**
 * HOURS_PATTERN is what a calendar's box for a weekday takes: stretches such as
 * 08:00–12:00, either kind of dash, apart by commas, or nothing for a day off.
 * The dash is escaped because the browser reads a pattern in its v mode, where
 * an unescaped one in a class is no pattern at all - and an unreadable pattern
 * is ignored, which would let anything through.
 */
const HOURS_PATTERN = '\\d{2}:\\d{2}\\s*[\\-–]\\s*\\d{2}:\\d{2}(\\s*,\\s*\\d{2}:\\d{2}\\s*[\\-–]\\s*\\d{2}:\\d{2})*';

/**
 * What each pattern a field is held to asks for, in words. The browser can only
 * call a pattern "the requested format", which says nothing about what to type,
 * and says it in its own language rather than the screen's - so every pattern
 * the markup uses is named here, and TestEveryPatternSaysWhatItAsksFor fails on
 * one that is not.
 */
const PATTERN_MESSAGES = {
  [HOURS_PATTERN]: () => t('calendar.hoursFormat', 'Write the hours as 08:00–12:00, 13:00–17:00.'),
  '[0-9]{6}': () => t('valid.code', 'Please enter the six digits your authenticator app shows.'),
  '[A-Za-z]{3}': () => t('valid.currency', 'Please enter the three letters of a currency, such as EUR.'),
  '[0-9]{2}:[0-9]{2}( *, *[0-9]{2}:[0-9]{2})*': () => t('plan.timesFormat', 'Write the times as 08:00, 12:30.'),
};

/** What a field's rule says about its value, or nothing where it has no objection. */
function validityMessage(field) {
  const { validity } = field;
  const limit = (value) => (field.type === 'date' ? fmtDate(value) : Number(value));

  if (validity.valueMissing) return t('valid.required', 'Please fill in this field.');
  if (validity.badInput) return t('valid.number', 'Please enter a number.');
  if (validity.typeMismatch && field.type === 'email') return t('valid.email', 'Please enter an email address.');
  if (validity.typeMismatch && field.type === 'url') return t('valid.url', 'Please enter a web address.');

  // Before the lengths: a pattern says the whole of what the field takes, and
  // "at least 6 characters" is true of a code and less use than what the six are.
  if (validity.patternMismatch && PATTERN_MESSAGES[field.getAttribute('pattern')]) {
    return PATTERN_MESSAGES[field.getAttribute('pattern')]();
  }

  if (validity.rangeUnderflow) return fillIn(t('valid.min', 'At least {0}.'), [limit(field.min)]);
  if (validity.rangeOverflow) return fillIn(t('valid.max', 'At most {0}.'), [limit(field.max)]);
  if (validity.tooLong) return fillIn(t('valid.maxLength', 'At most {0} characters.'), [field.maxLength]);
  if (validity.tooShort) return fillIn(t('valid.minLength', 'At least {0} characters.'), [field.minLength]);

  // The steps that are left are the server's: whole numbers, and a rate in the
  // hundredths it is kept in.
  if (validity.stepMismatch) {
    return Number(field.step) === 1
      ? t('valid.wholeNumber', 'Please enter a whole number.')
      : fillIn(t('valid.step', 'In steps of {0}.'), [Number(field.step)]);
  }

  // Whatever a later browser checks: its own words are better than a wrong
  // sentence of ours.
  return '';
}

/**
 * Saves a form, and stops protecting what is in it once the save has landed.
 *
 * The same as mutate, with the one thing every form that is refilled from the
 * server has to do afterwards. Cleared here rather than when the form is
 * submitted, because a refused save is not finished: the values stay on screen
 * beside the complaint about them, and they are still the reader's to correct.
 *
 * Before `after` rather than after it, because `after` is where the reload is -
 * and the whole point of clearing is that this reload may fill the form again.
 */
function saveForm(form, fn, successMessage, after) {
  return mutate(fn, successMessage, async (result) => {
    if (form) delete form.dataset.editing;

    // The draft with it: what it held is on the server now, so keeping it would
    // only mean putting yesterday's copy back over today's on the next load.
    forgetDraft(form);

    if (after) await after(result);
  });
}

/** Reads a form into a plain object, dropping empty optional fields. */
function formData(form) {
  const out = {};
  for (const [key, raw] of new FormData(form).entries()) {
    const value = typeof raw === 'string' ? raw.trim() : raw;
    if (value !== '') out[key] = value;
  }
  return out;
}

/**
 * Makes every date field read and accept the reader's own convention.
 *
 * <input type="date"> renders in the *browser's* UI language and ignores the
 * page entirely - measured in Chrome 151 with the same value in three inputs,
 * one of them carrying lang="de-DE" itself: started with --lang=en-US all three
 * read 08/12/2026, with --lang=de-DE all three read 12.08.2026. So an English
 * screen on a German machine asked for a date as TT.MM.JJJJ, and a German screen
 * on an English one asked American-first.
 *
 * The native field is kept rather than replaced. It is what a phone turns into a
 * date wheel and what a screen reader already knows how to announce, and writing
 * a calendar that does both of those as well as the browser does is not a trade
 * worth making for a format string. It becomes the picker: off-screen, opened by
 * the button beside the field, and its value is still the value.
 *
 * What is added is the part that was wrong - a text field, written and read in
 * the reader's own convention. Typing is honoured, because a date field somebody
 * cannot type into is worse than one in the wrong order.
 *
 * The named element stays the native one, so every form read, every
 * form.elements lookup and every getElementById keeps working unchanged. Setting
 * one from code goes through setDateField.
 */
function enhanceDateFields(root = document) {
  for (const native of $$('input[type="date"]', root)) {
    if (native.dataset.enhanced) continue;

    native.dataset.enhanced = '1';
    native.classList.add('date-native');
    native.tabIndex = -1;
    native.setAttribute('aria-hidden', 'true');

    const shown = el('input', {
      type: 'text',
      class: 'date-shown',
      // A date is digits and separators; on a phone this is the difference
      // between the number pad and the whole keyboard.
      inputmode: 'numeric',
      autocomplete: 'off',
      spellcheck: 'false',
    });

    const open = el('button', {
      type: 'button',
      class: 'date-open',
      tabindex: '-1',
      'aria-hidden': 'true',
    });

    open.append(calendarIcon());

    const wrap = el('span', { class: 'date-wrap' });
    native.replaceWith(wrap);
    wrap.append(shown, native, open);

    if (native.required) shown.required = true;

    const draw = () => {
      shown.value = native.value ? fmtDate(native.value) : '';
      shown.placeholder = datePattern();
      shown.setAttribute('aria-label', dateFieldLabel(wrap));
    };

    native.addEventListener('change', draw);
    native.addEventListener('input', draw);

    // Typed by hand. Parsed on every keystroke so a complete date takes effect
    // at once, and left alone while it is still half-written.
    shown.addEventListener('input', () => {
      const iso = parseTypedDate(shown.value);
      if (iso !== null) native.value = iso;
      else if (shown.value.trim() === '') native.value = '';
    });

    // On the way out, the box is made to agree with what is stored - so half a
    // date, or one nobody could parse, does not sit there looking accepted.
    shown.addEventListener('blur', draw);

    const showPicker = () => {
      if (typeof native.showPicker !== 'function') return;

      try {
        native.showPicker();
      } catch {
        // Chrome throws without a user gesture. Not worth a message: typing
        // still works, which is why this is a text field.
      }
    };

    // mousedown rather than click: focus would move first and close the picker
    // in the same breath.
    open.addEventListener('mousedown', (event) => {
      event.preventDefault();
      shown.focus();
      showPicker();
    });

    // The keyboard way in, on the field itself - the button is out of the tab
    // order because it would double every date field's stops for no gain.
    shown.addEventListener('keydown', (event) => {
      if (event.key === 'ArrowDown' && (event.altKey || event.metaKey)) {
        event.preventDefault();
        showPicker();
      }
    });

    // Drawn now and again whenever the language changes, like everything else
    // written into the page rather than translated in it - and kept across a
    // sign-out with the installation's own draws, because a date field holds
    // nothing of an account's and outlives all of them. Forgotten with the
    // account's draws, every field nobody had touched went on reading in the
    // convention of the language before the sign-out.
    //
    // Numbered rather than keyed on the registry's size, which shrinks whenever
    // a draw is forgotten and would hand the next field a key already in use.
    dateFieldsDrawn += 1;
    redrawable(`dateField:${dateFieldsDrawn}`, draw, { installation: true });
  }
}

/** How many date fields have been given a drawing; it names the next one's. */
let dateFieldsDrawn = 0;

/** The pattern a date is written in here, as a placeholder. */
function datePattern() {
  // Read off a date whose parts cannot be mistaken for one another, so the order
  // and the separators come from the locale rather than from a guess.
  return new Intl.DateTimeFormat(activeLocale(), {
    day: '2-digit', month: '2-digit', year: 'numeric', timeZone: 'UTC',
  }).formatToParts(new Date(Date.UTC(2026, 10, 22))).map((part) => {
    if (part.type === 'day') return t('date.day', 'DD');
    if (part.type === 'month') return t('date.month', 'MM');
    if (part.type === 'year') return t('date.year', 'YYYY');

    return part.value;
  }).join('');
}

/** Which of day, month and year comes first, second and third here. */
function dateOrder() {
  return new Intl.DateTimeFormat(activeLocale(), {
    day: '2-digit', month: '2-digit', year: 'numeric', timeZone: 'UTC',
  }).formatToParts(new Date(Date.UTC(2026, 10, 22)))
    .filter((part) => ['day', 'month', 'year'].includes(part.type))
    .map((part) => part.type);
}

/**
 * Reads a date somebody typed, in the order this locale writes them.
 *
 * Null for anything incomplete, which is what leaves a half-typed date alone
 * instead of storing a guess at it.
 */
function parseTypedDate(text) {
  const groups = String(text).match(/\d+/g);
  if (!groups || groups.length < 3) return null;

  const order = dateOrder();
  const read = {};
  order.forEach((type, index) => { read[type] = groups[index]; });

  // A two-digit year is ambiguous and this is not the place to decide it.
  if (!read.year || read.year.length !== 4) return null;

  const year = Number(read.year);
  const month = Number(read.month);
  const day = Number(read.day);

  if (!year || !month || !day) return null;

  // Round-tripped through a real date, so the thirtieth of February is refused
  // rather than silently becoming the second of March.
  const at = new Date(Date.UTC(year, month - 1, day));
  if (at.getUTCFullYear() !== year || at.getUTCMonth() !== month - 1
    || at.getUTCDate() !== day) {
    return null;
  }

  return `${String(year).padStart(4, '0')}-${String(month).padStart(2, '0')}`
    + `-${String(day).padStart(2, '0')}`;
}

/** What to call a date field to a screen reader, taken from its own label. */
function dateFieldLabel(wrap) {
  const label = wrap.closest('label');
  const text = label ? leadingText(label).trim() : '';

  return text || t('field.date', 'Date');
}

/** Sets a date field from code, so both halves of it agree. */
function setDateField(native, iso) {
  if (!native) return;

  native.value = iso ?? '';
  native.dispatchEvent(new Event('change'));
}

function calendarIcon() {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('width', '18');
  svg.setAttribute('height', '18');
  svg.setAttribute('aria-hidden', 'true');

  const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  path.setAttribute('fill', 'currentColor');
  path.setAttribute('d', 'M7 2v2H5a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V6a2 2 0 0 0-2-2h-2V2h-2v2H9V2H7zm12 7v10H5V9h14z');

  svg.append(path);

  return svg;
}

/** Shows or hides every element that declares a data-perm requirement. */
function applyPermissionVisibility(root = document) {
  for (const node of $$('[data-perm]', root)) {
    node.hidden = !can(...node.dataset.perm.split(','));
  }
}

// ---------------------------------------------------------------------- i18n

/**
 * What the screen says in each language but English, by language: the
 * dictionaries loaded so far.
 *
 * Each lives in a file of its own, /i18n/<language>.json, which the server hands
 * out from the dictionaries the binary carries - the ones the installer and the
 * Android app read as well - and is fetched the first time its language is
 * spoken. English has none and needs none: it is the source, in the markup and in
 * the fallbacks passed to t(), so a key a dictionary lacks degrades to English
 * rather than to a language nobody asked for.
 */
const dictionaries = { en: {} };

/** The dictionaries on their way, so a language asked for twice is fetched once. */
const fetchingDictionaries = {};

/**
 * Loads a language's dictionary the first time it is spoken.
 *
 * English, and a language this installation does not speak, answer at once with
 * none. One that cannot be fetched - offline, and never fetched before, so the
 * service worker holds no copy of it - is not remembered as empty: the next time
 * the language is spoken it is asked for again.
 */
function loadDictionary(language) {
  if (dictionaries[language]) return Promise.resolve(dictionaries[language]);
  if (!(language in LANGUAGES)) return Promise.resolve(dictionaries.en);

  fetchingDictionaries[language] ??= (async () => {
    try {
      const response = await reach(`/i18n/${encodeURIComponent(language)}.json`);
      if (!response.ok) return dictionaries.en;

      dictionaries[language] = await response.json();

      return dictionaries[language];
    } catch {
      return dictionaries.en;
    } finally {
      delete fetchingDictionaries[language];
    }
  })();

  return fetchingDictionaries[language];
}

/** How many languages have been asked for, so the last one asked draws the screen. */
let languagesAsked = 0;

/**
 * Applies a language to the page.
 *
 * Elements carry their English text in the markup, so a key with no
 * translation - including every key when English itself is selected - leaves
 * the English in place. That is what makes English the fallback rather than
 * merely one more translation.
 *
 * The language's dictionary is loaded first, the first time it is spoken. A
 * second change of language can overtake the first while its dictionary is on
 * the way; the one asked for last draws the screen, and the other stands down
 * when its dictionary arrives rather than drawing the screen in the language
 * just left. Last asked, not the active language: a guard comparing with
 * activeLanguage() would make the argument mean nothing, and a page told to
 * speak German would stay English.
 */
async function applyLanguage(language) {
  const asked = ++languagesAsked;
  const dict = await loadDictionary(language);
  if (asked !== languagesAsked) return;

  document.documentElement.lang = language;

  for (const node of $$('[data-i18n]')) {
    const translated = dict[node.dataset.i18n];
    if (translated === undefined) {
      // Restore the English source from the copy taken on first run.
      if (node.dataset.i18nSource !== undefined) {
        setLeadingText(node, node.dataset.i18nSource);
      }

      continue;
    }

    if (node.dataset.i18nSource === undefined) {
      node.dataset.i18nSource = leadingText(node);
    }

    setLeadingText(node, translated);
  }

  for (const node of $$('[data-i18n-placeholder]')) {
    const translated = dict[node.dataset.i18nPlaceholder];
    if (node.dataset.i18nPlaceholderSource === undefined) {
      node.dataset.i18nPlaceholderSource = node.placeholder;
    }

    node.placeholder = translated ?? node.dataset.i18nPlaceholderSource;
  }

  // The English source kept and put back, as the three above do it. This one
  // only ever assigned, and English has no dictionary - so English is
  // exactly the case where there is nothing to assign, and going to German and
  // back left every one of these in German on an otherwise English page. Only
  // the readers who cannot see the label beside the control were told, and one of
  // the seven is the language picker itself.
  for (const node of $$('[data-i18n-aria]')) {
    if (node.dataset.i18nAriaSource === undefined) {
      node.dataset.i18nAriaSource = node.getAttribute('aria-label') ?? '';
    }

    const words = dict[node.dataset.i18nAria] ?? node.dataset.i18nAriaSource;

    // Still only when there is something to say: an empty aria-label is worse
    // than none, because it names the control as having no name.
    if (words) node.setAttribute('aria-label', words);
  }

  // The tooltip, for an element whose meaning is a picture. A mark on its own
  // says what it is to somebody who already knows it and nothing to anybody
  // else, and pointing at it is how the second group finds out.
  for (const node of $$('[data-i18n-title]')) {
    if (node.dataset.i18nTitleSource === undefined) {
      node.dataset.i18nTitleSource = node.title;
    }

    node.title = dict[node.dataset.i18nTitle] ?? node.dataset.i18nTitleSource;
  }

  redrawAll();
}

/**
 * How to draw each screen that only renders when somebody asks it to.
 *
 * Everything above translates the markup, which is most of the interface and not
 * the part that goes wrong. What goes wrong is what the script has already
 * written into the page: an import preview, a synchronisation's preview, the
 * version check. Those are drawn once, from an answer that arrived once, and
 * nothing translated them again - so switching the language left a screen whose
 * table heading was German beside a total still reading "5.01 h in total". Every
 * key was translated; none of them was looked up a second time.
 *
 * The screens that refreshAll reloads do not need this - they are drawn again
 * from a new request. These are the ones nobody asked to reload, and re-fetching
 * them on a language change would fire requests somebody did not ask for and
 * could fail on a form they have since edited. So the answer is kept and drawn
 * again from what is already in hand.
 *
 * Keyed, so a second answer for the same screen replaces the first rather than
 * accumulating closures over responses nobody can see any more.
 */
const redraws = new Map();

/** The keys of the draws that belong to the installation; see forgetEveryRedraw. */
const installationDraws = new Set();

/**
 * Draws a screen now, and again whenever the language changes.
 *
 * Forgotten when its account signs out, unless it is declared as the
 * installation's - the name and mark the sign-in screen shows, and the page's own
 * date fields.
 */
function redrawable(key, draw, { installation = false } = {}) {
  redraws.set(key, draw);

  if (installation) installationDraws.add(key);
  else installationDraws.delete(key);

  draw();
}

/** Forgets a screen's last answer, for one that has been emptied. */
function stopRedrawing(key) {
  redraws.delete(key);
}

/**
 * Forgets every screen drawn for whoever just signed out.
 *
 * Each draw keeps the answer it was made from, and the sign-out applies the
 * language to the screen it hands back - which redrew them all: the import
 * verdicts went back into the tables just emptied, and stood there for whoever
 * signed in next.
 */
function forgetEveryRedraw() {
  for (const key of [...redraws.keys()]) {
    if (!installationDraws.has(key)) redraws.delete(key);
  }
}

function redrawAll() {
  for (const draw of redraws.values()) draw();

  // The greeting is written entirely by the script and is drawn on arrival
  // rather than by any loader, so nothing else would put it back into the
  // language now in force. Only while it is the screen somebody is looking at:
  // drawing a hidden one costs a request for today's figures that nobody asked
  // for.
  if (me.user && !$('#view-welcome')?.hidden) renderWelcome();
}

/**
 * Reads the label text of a node, ignoring any nested elements.
 *
 * Labels wrap their input, so the text lives in the first text node; using
 * textContent would pull the field's own content in as well.
 */
function leadingText(node) {
  const first = node.firstChild;

  return first && first.nodeType === Node.TEXT_NODE ? first.nodeValue : node.textContent;
}

function setLeadingText(node, value) {
  const first = node.firstChild;

  if (first && first.nodeType === Node.TEXT_NODE) first.nodeValue = value;
  else node.textContent = value;
}

/**
 * The language to render in.
 *
 * A signed-in user's stored choice wins. Before anyone has signed in - which
 * is exactly the sign-in screen - the browser's own preference decides, so a
 * first-time visitor is not greeted in a language they may not read.
 *
 * This is the question "which dictionary", so the answer has to be a language
 * this application actually ships words for. For "which conventions to write a
 * date and a figure in", which is a different question with a better answer,
 * see activeLocale.
 */
function activeLanguage() {
  if (me.user?.language) return me.user.language;

  return detectBrowserLanguage();
}

/**
 * The locale to format dates, times and figures with.
 *
 * The same rule in the same order - a chosen language wins, the browser decides
 * otherwise - but the browser's answer is taken whole rather than reduced to a
 * language this application has a dictionary for.
 *
 * That reduction is right for choosing words and wrong for choosing conventions.
 * There is one English dictionary and there is not one English date: an en-GB
 * browser was being formatted as plain "en", which Intl reads as the American
 * order - so the twelfth of August was shown as 08/12/2026 to somebody whose own
 * browser would have written 12/08/2026. Nothing was mistranslated; the date was
 * simply wrong by five months for half the year.
 *
 * Where the two disagree about the language, the chosen one wins outright:
 * picking EN on a German browser is a decision about how this application should
 * read, and a date is part of how it reads.
 *
 * Where they agree about the language, the browser's tag is used, because it is
 * the same answer with a region on it. That case is not an edge: the first
 * sign-in stores the detected language on the account, so an account whose
 * language nobody ever chose looks exactly like one where somebody did. Treating
 * the stored value as a decision there would take the region away permanently,
 * on the strength of a choice that was never made.
 */
// One thing this cannot reach: the date fields.
//
// <input type="date"> renders in the browser's own UI language and nothing on
// the page changes it. Measured rather than assumed, in Chrome 151, with the
// same value in three inputs - a bare one, one inside a lang="de-DE" wrapper,
// and one carrying lang="de-DE" itself. Started with --lang=en-US all three read
// 08/12/2026; started with --lang=de-DE all three read 12.08.2026, including the
// one on a page whose lang is "en". The attribute is ignored outright.
//
// So a reader who switches this application to a language their browser is not
// set to gets their own convention in the pickers and the chosen one everywhere
// else. The only way to close that is to stop using the native field, and what
// it costs is not obvious in a desktop browser: on a phone this is what becomes
// a proper date wheel, and it is the accessible default a screen reader knows.
function activeLocale() {
  const chosen = me.user?.language;

  if (!chosen) return BROWSER_LOCALE || detectBrowserLanguage();

  if (BROWSER_LOCALE && primarySubtag(BROWSER_LOCALE) === primarySubtag(chosen)) {
    return BROWSER_LOCALE;
  }

  return chosen;
}

/** The language half of a tag: "de" for "de-AT", "de" for "de". */
function primarySubtag(tag) {
  return String(tag).toLowerCase().split('-')[0];
}

/**
 * The browser's own first preference, if Intl can use it.
 *
 * Validated once, here, rather than at each call: an unusable tag makes every
 * Intl constructor throw a RangeError, and a date formatter that throws takes
 * the screen it was rendering with it. Falling back to the language is the same
 * answer as before this existed, which is a worse date and not a broken page.
 */
const BROWSER_LOCALE = (() => {
  const tag = navigator.languages?.[0] ?? navigator.language ?? '';

  try {
    return tag && Intl.getCanonicalLocales(tag).length ? tag : '';
  } catch {
    return '';
  }
})();

/** The language shown when nothing better is known. */
const FALLBACK_LANGUAGE = 'en';

/**
 * Picks the best supported language from the browser's preference list.
 *
 * Only the primary subtag is compared, so "en-GB" and "en-US" both match "en".
 * A browser asking for something this application does not speak gets English,
 * which is the language the markup itself is written in.
 */
function detectBrowserLanguage() {
  const supported = Object.keys(LANGUAGES);
  const preferences = navigator.languages?.length
    ? navigator.languages
    : [navigator.language ?? FALLBACK_LANGUAGE];

  for (const preference of preferences) {
    if (supported.includes(primarySubtag(preference))) return primarySubtag(preference);
  }

  return FALLBACK_LANGUAGE;
}

/**
 * Translates one key for use in code-generated text: in English, the fallback,
 * until the language's dictionary has arrived - which applyLanguage waits for
 * before it draws anything again.
 */
function t(key, fallback) {
  return dictionaries[activeLanguage()]?.[key] ?? fallback;
}

/**
 * Remembers, per account and per browser, that the zone has been offered.
 *
 * An empty stored zone means "follow the instance", which is a real choice - so
 * adopting the browser's zone every time the page loaded would make that choice
 * impossible to keep. The marker is what makes it a one-time suggestion rather
 * than a standing override.
 *
 * The zone only. A language has no "follow the browser" to protect, so it is not
 * kept behind this - see adoptBrowserDefaults.
 */
function adoptionMarker(userID) {
  return `qubitdesk.adopted.${userID}`;
}

/**
 * Writes the browser's zone and language into the account.
 *
 * The browser knows two things the server cannot: which zone the person is
 * actually in, and which language they read. Until this, the language was
 * detected for the current page and thrown away on every load, and the zone was
 * not detected at all - so somebody in Vancouver saw their evening's work land on
 * the instance's tomorrow until they found the setting.
 *
 * The two are not decided the same way, and the difference is the point.
 *
 * A zone is a one-time suggestion. An empty stored zone means "follow the
 * instance", which is a real choice somebody can make and see offered - so
 * adopting the browser's on every load would make it impossible to keep. It is
 * offered once per account per browser and never again, and only when it differs
 * from the instance's, because writing the same value would take the choice away
 * to no effect at all.
 *
 * A language is not. The picker offers no "follow the browser" line, so an
 * account with none stored is not an account that chose anything - it is one
 * nobody has decided for yet, and nothing on screen could put it back. So it is
 * decided on sight, every time it is found undecided: the browser's language
 * where this interface speaks it, English where it does not.
 *
 * Returns whether anything was written, so the caller can read the account back.
 */
async function adoptBrowserDefaults() {
  if (!me.user) return false;

  let adopted = false;

  // The language first, and outside the marker below on purpose. Under it, an
  // adoption that was missed once - storage switched off, or a first sign-in on
  // a browser that had already recorded one for this account - left the account
  // with no language for ever, and its picker blank.
  if (!me.user.language) {
    try {
      await api('/me/language', {
        method: 'PUT',
        body: JSON.stringify({ language: detectBrowserLanguage() }),
      });

      adopted = true;
    } catch {
      // It still reads in the detected language for this page; it is simply not
      // remembered, and the next sign-in asks again.
    }
  }

  const marker = adoptionMarker(me.user.id);

  try {
    if (window.localStorage.getItem(marker)) return adopted;
  } catch {
    // Private browsing, or storage switched off. Suggesting the zone once per
    // load is worse than never suggesting it, so that half stops here.
    return adopted;
  }

  // The zone, when the browser's differs from what this account currently
  // resolves to. effectiveTimezone is the instance's while nothing is stored.
  const zone = guessTimezone();
  if (!me.user.timezone && zone && zone !== me.user.effectiveTimezone) {
    try {
      await api('/me/timezone', { method: 'PUT', body: JSON.stringify({ timezone: zone }) });
      adopted = true;
    } catch {
      // A zone this server does not know is not worth a message: the account
      // keeps following the instance, which is what it did before.
    }
  }

  try {
    window.localStorage.setItem(marker, '1');
  } catch {
    // Nothing to do. Worst case the zone is suggested again on the next load,
    // and the conditions above make that a no-op.
  }

  return adopted;
}

// -------------------------------------------------------------------- views

async function loadMe() {
  me = await api('/me');
  me.permissions = me.permissions ?? [];

  const who = $('#who');
  if (me.authEnabled) {
    who.replaceChildren(
      el('strong', { text: me.user.name }),
      el('span', {
        text: ` · ${roleTitle(me.user.role) || t('role.none', 'no role')}`,
      }),
    );
  } else {
    who.replaceChildren(el('span', { text: t('auth.disabled', 'Authentication disabled') }));
  }

  // The bar cuts the line short where it has no room; the whole of it is here.
  who.title = who.textContent;

  $("#password-banner").hidden = !me.user.mustChangePassword;
  $("#logout").hidden = !me.authEnabled;

  await applyLanguage(activeLanguage());
  applyAccountTheme();
  applyPermissionVisibility();

  // Not over an enrolment under way. The account is not enrolled until its first
  // code is confirmed, so drawn from it the card went back to "not enabled" -
  // taking the QR code somebody was scanning and the field for its first code -
  // on every reload, which a saved card or a change of language is. Leaving the
  // screen and signing out close it, on purpose.
  if ($('#totp-setup').hidden) renderTOTPState();
}

/**
 * An e-mail address that may fold after its @ and nowhere else.
 *
 * An address has no space, so a table cell holding one could not fold at all and
 * was as wide as the longest address in the column - room the row's actions did
 * not then have. After the @ is the one place a reader expects it to break.
 */
function foldableAddress(address) {
  const at = String(address ?? '').indexOf('@');
  if (at < 0) return [address ?? ''];

  return [address.slice(0, at + 1), el('wbr'), address.slice(at + 1)];
}

// ------------------------------------------------------------------- offline

/**
 * Lets the installed application open without a network: sw.js keeps the shell
 * of the page and hands it out when the installation cannot be reached, and
 * nothing the API answers. Browsers allow it on HTTPS and on this machine only,
 * so an installation reached over plain HTTP simply goes without.
 */
function keepTheShell() {
  if (!('serviceWorker' in navigator)) return;

  navigator.serviceWorker.register('/sw.js').catch(() => {
    // Not kept: the page works exactly as it did, it only cannot open offline.
  });
}

/**
 * The page's side of being offline: the sign-in screen says so while there is no
 * connection, and a page that could not even load for want of one starts again
 * by itself once it is back.
 */
function wireOffline() {
  window.addEventListener('offline', () => showSignInOffline(true));

  window.addEventListener('online', () => {
    if (!$('#login-offline')?.hidden) startAgain();
  });
}

/**
 * Loads the page again, once: the connection coming back and the next round of
 * asking can both notice it, and a second reload replaced the page that was in the
 * middle of sending what had waited.
 */
function startAgain() {
  clearInterval(reachingAgain);

  if (startingAgain) return;

  startingAgain = true;
  window.location.reload();
}

/** Whether the page is already being loaded again. */
let startingAgain = false;

/** How often a page that could not reach the installation asks it again. */
const REACH_AGAIN_MS = 10000;

/** The asking, while it goes on. */
let reachingAgain = null;

/**
 * Says on the sign-in screen that the installation cannot be reached, where that
 * is why nobody is signed in - the form on its own reads as a session that ended,
 * and invites a password that cannot be checked - and asks it again until it
 * answers, then starts the page again.
 *
 * Told by the load that failed rather than by navigator.onLine alone, which says
 * whether there is a network and not whether the installation answers on it: an
 * installation that is down behind a working network is the same wait.
 */
function showSignInOffline(unreachable) {
  const note = $('#login-offline');
  if (!note) return;

  note.hidden = !unreachable || Boolean(me.user);

  if (note.hidden || reachingAgain) return;

  reachingAgain = setInterval(async () => {
    try {
      const answer = await reach(`${API}/branding`, { credentials: 'same-origin' });

      if (answer.ok) startAgain();
    } catch {
      // Still not there: the next round asks again.
    }
  }, REACH_AGAIN_MS);
}

// ------------------------------------------------------- push notifications

/** Whether the installation pushes notifications, as the server last said it - unless it is being changed. */
async function loadPushSwitch() {
  const form = $('#form-push');
  if (!form || beingEdited(form)) return;

  const state = await api('/settings/push');

  form.elements.enabled.checked = Boolean(state.enabled);
  form.elements.enabled.defaultChecked = Boolean(state.enabled);
}

/**
 * Whether this browser can be pushed to at all: a service worker to show what
 * arrives, and a push manager to subscribe through. The Android app's WebView
 * has neither.
 */
function pushable() {
  return 'serviceWorker' in navigator && 'PushManager' in window;
}

/**
 * The notification card's part about pushing to this browser: offered where the
 * installation pushes and the browser can be pushed to, and saying so where this
 * browser is already among the reader's subscribed ones.
 */
async function drawPushState() {
  const box = $('#push-mine');
  const pushed = $('#push-state');
  if (!box || !pushed) return;

  box.hidden = true;
  pushed.hidden = true;

  // Where it cannot be asked, nothing is offered that could only fail; the
  // card's other parts stand either way.
  try {
    const state = pushable() && can('tickets:read') ? await api('/me/push') : null;
    if (!state?.enabled) return;

    const registration = await navigator.serviceWorker.getRegistration();
    const subscription = await registration?.pushManager.getSubscription();
    const mine = Boolean(subscription && (state.endpoints ?? []).includes(subscription.endpoint));

    $('#push-subscribe').hidden = mine;
    $('#push-unsubscribe').hidden = !mine;
    pushed.hidden = !mine;
    box.hidden = false;
  } catch {
    box.hidden = true;
  }
}

/** The key a browser subscribes with, from the base64url the server writes to the bytes the browser takes. */
function pushKeyBytes(key) {
  const padded = key.replaceAll('-', '+').replaceAll('_', '/').padEnd(Math.ceil(key.length / 4) * 4, '=');

  return Uint8Array.from(atob(padded), (c) => c.charCodeAt(0));
}

/**
 * Subscribes this browser and hands its subscription to the installation. The
 * browser's own leave to notify is asked for first, because a push it could not
 * show is one nobody sees; a refusal is said by the card's line about it.
 */
async function subscribeThisBrowser() {
  const state = await api('/me/push');
  if (!state.enabled) return;

  if (Notification.permission === 'default') await Notification.requestPermission();

  drawNotifyState();

  if (Notification.permission !== 'granted') return;

  const registration = await navigator.serviceWorker.ready;
  const subscription = await registration.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: pushKeyBytes(state.publicKey),
  });

  await api('/me/push', { method: 'POST', body: JSON.stringify(subscription.toJSON()) });
}

/**
 * Takes this browser out of the pushes: the installation forgets it, and the
 * browser drops the subscription. Where the installation cannot be told, the
 * push service is, by the browser, and answers the next push as gone.
 */
async function unsubscribeThisBrowser() {
  const registration = await navigator.serviceWorker?.getRegistration();
  const subscription = await registration?.pushManager?.getSubscription();
  if (!subscription) return;

  try {
    await api('/me/push', { method: 'DELETE', body: JSON.stringify({ endpoint: subscription.endpoint }) });
  } finally {
    await subscription.unsubscribe();
  }
}

/**
 * Takes the browser out of the pushes when somebody signs out of it, so whoever
 * uses it next is not pushed the notifications of whoever left. A sign-out goes
 * ahead whatever this answers.
 */
async function forgetThisBrowsersPush() {
  try {
    await unsubscribeThisBrowser();
  } catch {
    // Signing out is not held up by a push channel; the browser dropped it either way.
  }
}

function wirePush() {
  const form = $('#form-push');

  form?.addEventListener('submit', (event) => {
    event.preventDefault();

    const enabled = form.elements.enabled.checked;

    saveForm(form, () => api('/settings/push', {
      method: 'PUT', body: JSON.stringify({ enabled }),
    }), t('push.switchSaved', 'Push notifications saved'), refreshAll);
  });

  $('#push-subscribe')?.addEventListener('click', () => mutate(
    () => subscribeThisBrowser(), null, drawPushState));

  $('#push-unsubscribe')?.addEventListener('click', () => mutate(
    () => unsubscribeThisBrowser(), null, drawPushState));
}

// ------------------------------------------------------- accounts and roles

async function loadUsers() {
  // Listing accounts is a permission, and administering them is what this list
  // is for.
  //
  // Emptied rather than left alone on the way out: a session that loses the right -
  // its role changed while it was signed in - must not go on showing the accounts it
  // read a moment ago.
  if (!can('users:read')) {
    cache.users = [];

    return;
  }

  cache.users = (await api('/users'))?.items ?? [];

  const rows = cache.users.map((u) => {
    const actions = el('td', { class: 'actions' });

    // Whose account this is somebody else's to correct.
    //
    // Not the built-in administrator: it is the way back into an installation,
    // and its name and address are what an operator looks for when everything
    // else has gone wrong. Not the row somebody is reading their own name in
    // either - an administration screen is for other people's accounts, and a
    // person changing their own address here would be doing it in the one place
    // that cannot ask them for their password first.
    //
    // Not a directory account, and that one is the server's rule rather than
    // this screen's: the name and the address are copied from the entry on every
    // synchronisation, so a change made here holds until the next run and then
    // reverts. It is refused for exactly that reason, and a button that asks for
    // a refusal teaches somebody the screen is broken.
    if (can('users:write') && !u.isSystem && !u.isExternal && u.id !== me.user?.id) {
      actions.append(el('button', {
        class: 'link',
        'data-action': 'edit',
        text: t('action.edit', 'edit'),
        onclick: () => editUser(u),
      }));
    }

    // Letting somebody back in who has forgotten their password.
    //
    // The same three rows are left out as for editing, and for the same reasons
    // the server gives: their own account is changed under My account, which
    // asks for the password they have; the built-in administrator is the way
    // back into an installation; and a directory account's password lives in the
    // directory, so one set here could never be used.
    if (can('users:write') && !u.isSystem && !u.isExternal && u.id !== me.user?.id) {
      actions.append(el('button', {
        class: 'link',
        'data-action': 'reset-password',
        text: t('user.resetPassword', 'reset password'),
        onclick: () => resetPassword(u),
      }));
    }

    // For somebody who lost the phone their second factor was on, and only where
    // there is one to remove. Their own account turns it off under My account,
    // asking for a code; the built-in administrator is recovered from the
    // server's command line instead, so the way back is not one more button.
    if (can('users:write') && !u.isSystem && u.id !== me.user?.id && (u.totpEnabled || u.hasPasskeys)) {
      // A plain link like the reset beside it, not a red one: the column that
      // selects rows for deletion is derived from a row's red button.
      actions.append(el('button', {
        class: 'link',
        'data-action': 'remove-second-factor',
        text: t('user.removeFactor', 'remove second factor'),
        onclick: () => removeSecondFactor(u),
      }));
    }

    // Not your own, which the server refuses too: deleting it would end the
    // session the request arrived on and leave whoever pressed it looking at a
    // sign-in screen for an account that no longer exists.
    // Not a directory account either: removing it here removes a person, and the
    // next synchronisation creates them again from the entry that is still
    // there - so the hours are gone and nothing else has changed.
    if (can('users:delete') && !u.isSystem && !u.isExternal && u.id !== me.user?.id) {
      actions.append(deleteButton({
        label: `${u.name} <${u.email}>`,
        path: `/users/${u.id}`,
        message: t('msg.userDeleted', 'User deleted'),
        after: refreshAll,
        // Deleting one account asks something extra - whether to keep its
        // entries or purge them - so the row keeps its own dialog. A batch takes
        // the plain deletion, the answer that keeps the history.
        ask: () => deleteUser(u),
      }));
    }

    // What this row is, where it is not simply another account. The two are
    // different facts and were being told apart by whichever came first: the
    // built-in administrator is the installation's own account, and the row
    // somebody is reading their own name in is theirs. Both can be true, and the
    // one that matters to the person looking is which one is theirs.
    if (u.id === me.user?.id) {
      actions.append(el('span', {
        class: 'muted',
        text: t('user.ownAccount', 'Your account'),
      }));
    } else if (u.isSystem) {
      actions.append(el('span', {
        class: 'muted',
        text: t('user.systemAccount', 'System account'),
      }));
    }


    const roleCell = el('td', {});

    // The built-in account's role is shown as text rather than as a control that
    // cannot be used. A disabled dropdown still looks like something to click,
    // and the answer to clicking it is "no" - which is worse than not offering it.
    // Its role is fixed anyway: it must keep one that can still administer.
    if (u.isSystem) {
      roleCell.append(el('span', { text: roleTitle(u.role) }));
    } else if (can('users:write') && can('roles:read')) {
      // Changing a role is a select rather than a form: it is the one field
      // that is changed on its own often enough to deserve it.
      //
      // Named by its title alone, where the account form adds what each role is
      // for. A dropdown is as wide as its longest choice, and in a row that is
      // room taken from the row's actions; the closed box showed the title and
      // the first letter of the purpose anyway.
      const select = el('select', {
        onchange: (e) => patch(`/users/${u.id}/role`, { role: e.target.value },
          t('msg.roleChanged', 'Role changed'), refreshAll),
      });
      fillSelect(select, roleChoices().map((role) => ({ ...role, label: roleTitle(role.name) })),
        { labelKey: 'label', valueKey: 'name' });
      select.value = u.role;
      roleCell.append(select);
    } else {
      roleCell.textContent = roleTitle(u.role) || '–';
    }

    return el('tr', { class: me.user && u.id === me.user.id ? 'self' : '' },
      el('td', { class: 'text', text: u.name }),
      el('td', {}, ...foldableAddress(u.email)),
      roleCell,
      // Where the account is kept. It decides what may be done to it here, and
      // was only visible as a note beside the buttons - which is the wrong place
      // for the thing that explains why two of them are missing.
      el('td', {
        text: u.isExternal
          ? t('user.fromDirectory', 'Directory')
          : t('user.local', 'Local'),
        class: u.isExternal ? 'muted' : '',
        title: u.isExternal
          ? t('user.directoryHint',
            'Managed in LDAP. The password lives there, and removing the entry there removes this account.')
          : '',
      }),
      actions,
    );
  });

  fillTable($('#table-users tbody'), rows, 5, t('user.empty', 'No users yet.'));
}

/**
 * Gives an account a password it can get back in with.
 *
 * Asked before it is done, and the question names the person: an administrator
 * with a table of accounts open is one mis-click away from resetting the wrong
 * one, and "are you sure" would not have said which.
 *
 * What the dialog says is what actually happens, both halves of it. The account
 * has to replace this password before it can use anything, and every session it
 * has open ends now - a cookie is not re-checked against the password that
 * opened it, so a reset that left them running would leave the door it was
 * closing wide open.
 */
async function resetPassword(user) {
  const chosen = await confirmDialog({
    title: t('user.resetTitle', 'Reset password'),
    text: fillIn(t('user.resetText',
      'Give {0} a password to sign in with once. They have to choose their own '
      + 'before they can use anything, and any session this account has open ends '
      + 'now.'), [user.name]),
    confirmLabel: t('user.resetConfirm', 'Reset password'),
    danger: false,
    field: {
      id: 'reset-password-field',
      generateID: 'reset-password-generate',
      label: t('field.newPassword', 'New password'),
      hint: fillIn(t('user.resetHint', 'At least {0} characters.'), [MIN_PASSWORD_LENGTH]),
      minLength: MIN_PASSWORD_LENGTH,
      generate: t('action.generate', 'Generate'),
    },
  });

  if (!chosen) return;

  await patch(`/users/${user.id}/password`, { password: chosen },
    t('msg.passwordReset', 'Password reset'), refreshAll);
}

/**
 * Takes away the second factor somebody lost with their phone.
 *
 * The code from the authenticator app goes, every passkey goes with it - most
 * likely on the same phone, and a way in for whoever holds it now - and every
 * session the account has open ends, because that phone may still be signed
 * in. The dialog says all three, since all three happen.
 */
async function removeSecondFactor(user) {
  const sure = await confirmDialog({
    title: t('user.removeFactorTitle', 'Remove the second factor'),
    text: fillIn(t('user.removeFactorText',
      'For {0}, who has lost the phone it was on: the code from the authenticator app and '
      + 'every passkey are removed, and every session this account has open ends. They sign '
      + 'in with their password and can set the second factor up again.'), [user.name]),
    confirmLabel: t('user.removeFactorConfirm', 'Remove'),
  });

  if (!sure) return;

  await mutate(() => api(`/users/${user.id}/second-factor`, { method: 'DELETE' }),
    t('msg.secondFactorRemoved', 'Second factor removed'), refreshAll);
}

/** Opens an existing account in the form above the table. */
function editUser(user) {
  const form = $('#form-user');
  if (!form) return;

  form.elements.id.value = String(user.id);
  form.elements.name.value = user.name;
  form.elements.email.value = user.email;

  showUserCreationFields(false);

  swapTheLabel($('#user-form-title'), 'user.edit', 'Edit user');
  swapTheLabel($('#user-submit'), 'action.save', 'Save');
  $('#user-cancel').hidden = false;

  switchView('users');
  form.scrollIntoView({ block: 'nearest' });
  form.elements.name.focus();
}

/** Puts the form back to adding somebody. */
function resetUserForm() {
  const form = $('#form-user');
  if (!form) return;

  form.reset();
  form.elements.id.value = '';

  showUserCreationFields(true);

  swapTheLabel($('#user-form-title'), 'user.create', 'Add user');
  swapTheLabel($('#user-submit'), 'action.create', 'Create');
  $('#user-cancel').hidden = true;
}

/**
 * Shows or hides the two fields that only a new account has.
 *
 * The required flag goes with the role rather than staying on it. A control that
 * is hidden and still required is one the browser refuses to submit past and
 * cannot scroll to, so the form does nothing at all and says nothing about why -
 * which is a worse outcome than either of the two this is choosing between.
 */
function showUserCreationFields(shown) {
  const role = $('#user-role-field');
  const password = $('#user-password-field');

  if (role) role.hidden = !shown;
  if (password) password.hidden = !shown;

  const chooser = $('#form-user select[name=role]');
  if (chooser) chooser.required = shown;
}

async function loadRoles() {
  if (!can('roles:read')) return;

  cache.roles = (await api('/roles'))?.items ?? [];

  // Its own failure, rather than everybody's. This runs early in the sequence
  // that loads every screen, so a refusal here used to abort the rest of it -
  // and the screens further down, the directory card among them, were left
  // holding whatever the markup shipped with. The rights editor is the only
  // thing that needs this list; nothing else on the page should go dark because
  // it could not be had.
  try {
    cache.permissions = (await api('/permissions'))?.permissions ?? [];
  } catch {
    cache.permissions = [];
  }

  fillSelect($('#form-user select[name=role]'), roleChoices(),
    { labelKey: 'label', valueKey: 'name' });

  // Not over a role somebody is part way through. This runs on every reload of
  // the screens - a language chosen, a neighbouring card saved - and it rebuilds
  // the switches from nothing, so the rights ticked for a role that has not been
  // saved were swept away without a reload being involved at all.
  //
  // Only this call. Opening a role and starting a new one both redraw these on
  // purpose, and are somebody asking for it.
  if (!beingEdited($('#form-role'))) renderPermissionCheckboxes();

  const rows = cache.roles.map((role) => {
    const actions = el('td', { class: 'actions' });

    // A shipped role cannot be changed in any part - not its name, not its
    // description, not its rights - so the way in says "view". "Edit" was an
    // offer the server refuses, which is a button that teaches somebody the
    // screen is broken.
    const shipped = role.isSystem || role.isDefault;

    if (can('roles:write')) {
      actions.append(el('button', {
        class: 'link',
        text: shipped ? t('action.view', 'view') : t('action.edit', 'edit'),
        onclick: () => editRole(role),
      }));

      // Shipped roles are not offered a delete either, because the server
      // refuses one: they are what every new account and every synchronised one
      // is assigned.
      if (!shipped) {
        actions.append(deleteButton({
          label: `${t('field.role', 'Role')} "${roleTitle(role.name)}"`,
          path: `/roles/${role.id}`,
          message: t('msg.roleDeleted', 'Role deleted'),
          after: refreshAll,
        }));
      }
    }

    if (role.isSystem) {
      actions.append(el('span', { class: 'muted', text: t('role.systemRole', 'System role') }));
    } else if (role.isDefault) {
      actions.append(el('span', {
        class: 'muted',
        text: t('role.shippedRole', 'Shipped role'),
      }));
    }

    return el('tr', {},
      // The title, and the identifier under it for the three that have both: the
      // identifier is what the API takes and what the directory configuration stores,
      // so the screen that administers roles is the one place it has to stay visible.
      el('td', { class: 'text' }, el('span', { text: roleTitle(role.name) }),
        ...(roleTitle(role.name) === role.name
          ? []
          : [el('span', { class: 'muted', text: ` ${role.name}` })])),
      el('td', { class: 'text', text: roleDescription(role) || '–' }),
      el('td', { class: 'num', text: String(role.permissions.length) }),
      actions,
    );
  });

  fillTable($('#table-roles tbody'), rows, 4, t('role.empty', 'No roles available.'));
}

/**
 * The rights to tick, and whether they can be ticked at all.
 *
 * fixed is for a system role. Its permissions cannot be changed - neither given nor
 * taken - because that role belongs to the built-in account, which holds everything on
 * the staff side already; a right added here could only be the customer portal, handed
 * to the one account nobody chose from the screen that administers roles.
 *
 * Shown as unavailable rather than left clickable and refused on save. The name of a
 * system role has always been read-only in this form for the same reason: a control
 * that looks usable and answers "no" is worse than one that says so first.
 */
function renderPermissionCheckboxes(selected = [], { fixed = false } = {}) {
  const list = $('#permission-list');
  list.replaceChildren();

  // Grouped by area, in the order the rights arrive. Fifteen boxes in one run
  // is a wall to read; six short groups is a list to scan, and the grouping is
  // already in the identifiers rather than invented here.
  let group = '';

  for (const permission of cache.permissions) {
    const area = permissionGroup(permission);

    if (area.key !== group) {
      group = area.key;
      list.append(el('h3', { class: 'perm-group', text: area.label }));
    }

    // A switch, like every other on-or-off control in this interface. A right is
    // held or it is not, which is the question a switch asks - and fifteen of
    // them read as settings rather than as a form to fill in.
    list.append(el('label', { class: `switch perm-switch${fixed ? ' muted' : ''}` },
      el('input', {
        type: 'checkbox',
        name: 'permissions',
        value: permission,
        checked: selected.includes(permission),
        disabled: fixed,
      }),
      el('span', { class: 'switch-track' }),
      el('span', { class: 'perm-name', text: permissionTitle(permission) }),
      // The identifier stays: it is what the API takes and what a directory
      // configuration stores, so this screen is where it has to remain readable.
      el('code', { class: 'perm-id', text: permission }),
    ));
  }

  renderPermissionLegend();

  // Why they cannot be set, next to them rather than in a notice after the fact,
  // and beside it the thing that can be done instead.
  const note = $('#role-fixed-note');

  if (note) note.hidden = !fixed;

  const startNew = $('#role-start-new');

  if (startNew) startNew.hidden = !fixed;
}

/** The legend: every right, and one sentence on what it actually allows. */
function renderPermissionLegend() {
  const list = $('#permission-legend-list');
  if (!list) return;

  list.replaceChildren();

  for (const permission of cache.permissions) {
    const detail = permissionDetail(permission);
    if (!detail) continue;

    list.append(el('dt', {},
      el('span', { text: permissionTitle(permission) }),
      el('code', { class: 'perm-id', text: permission })));
    list.append(el('dd', { text: detail }));
  }
}

function editRole(role) {
  const form = $('#form-role');

  // Every shipped role, not only the administrator's: the server refuses a
  // rename, a changed right and a changed description on all three, and a form
  // that offers what the server refuses is worse than one that does not.
  const shipped = role.isSystem || role.isDefault;

  form.elements.id.value = String(role.id);

  // Shown in the reader's language, because this is a reading screen. The
  // identifier and the English description in the database are what a role is
  // stored as; what a person is looking at here is the role, and it has a name
  // and a purpose in their own language everywhere else on this screen.
  form.elements.name.value = shipped ? roleTitle(role.name) : role.name;
  form.elements.description.value = shipped
    ? roleDescription(role)
    : (role.description ?? '');

  form.elements.name.readOnly = shipped;
  form.elements.description.readOnly = shipped;

  renderPermissionCheckboxes(role.permissions, { fixed: shipped });

  // Nothing to save, so nothing offering to. The way out is "New", which is
  // still there beside it.
  const save = $('#form-role button[type="submit"]');
  if (save) save.hidden = shipped;

  // Drawn again on a language change rather than declared with a key, which is
  // how the other form titles do it: this message carries the role's name, and
  // applyLanguage would put the pattern back with its {0} showing. The key the
  // markup gave the heading has to come off for the same reason - left on, it
  // translates "Create role" over a form that has a role open.
  //
  // The English used to read "Edit role" while the German named the role, so the
  // name reached one set of readers and not the other, on the one screen where a
  // single form serves every role and the heading is what says which one is
  // open. role.view beside it already named it in both.
  const heading = $('#role-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('roleFormTitle', () => {
    heading.textContent = shipped
      ? t('role.view', 'Role {0}').replace('{0}', roleTitle(role.name))
      : t('role.edit', 'Edit role {0}').replace('{0}', roleTitle(role.name));
  });

  switchView('roles');

  // Straight to the rights, which is what somebody pressed the button for.
  //
  // The form is a long way down a screen that starts with the table of roles,
  // and the legend under the rights made it longer still - so opening a role
  // used to leave the reader at the top of the table they were already looking
  // at, with the thing they asked for somewhere below the fold.
  //
  // On the next frame, because the view is only just unhidden and an element in
  // a hidden one has nowhere to be scrolled to. The offset for the sticky bar is
  // the page's own: html carries scroll-padding-top, which every scroll into
  // view respects.
  requestAnimationFrame(() => {
    $('#form-role .perms')?.scrollIntoView({ block: 'start', behavior: 'smooth' });
  });
}

function resetRoleForm() {
  const form = $('#form-role');
  form.reset();
  form.elements.id.value = '';
  form.elements.name.readOnly = false;
  form.elements.description.readOnly = false;

  const save = $('#form-role button[type="submit"]');
  if (save) save.hidden = false;

  renderPermissionCheckboxes();

  // The heading goes back to being the markup's, key and English source with it,
  // and stops being drawn from a role that is no longer open.
  stopRedrawing('roleFormTitle');
  swapTheLabel($('#role-form-title'), 'role.create', 'Create role');
}

/**
 * Puts words on an element that says two different things, and says which.
 *
 * The key travels with the words. An element whose message depends on what the
 * screen is doing - a form title that creates or corrects, its submit button, the
 * label that names a database or a file - carries the data-i18n it was written
 * with in the markup, and applyLanguage translates every one of those from its
 * key. So changing the language put the declared message back over the shown one:
 * a correction went back to reading "Add user" above a form still holding the
 * account it had opened, with a button offering to add it. The heading disagreed
 * with the form, and the button disagreed with what pressing it would do.
 *
 * The English source travels too, and that half is easy to miss. applyLanguage
 * keeps a copy of the source the first time it translates an element and puts
 * that copy back when the dictionary has nothing for the key - and
 * English has no dictionary, so English is exactly the case that
 * restores the copy. A copy taken while the old key was declared would come back
 * as the old message the next time somebody read the page in English.
 *
 * Not for a message with a value in it: applyLanguage would put the pattern back
 * with its {0} showing. Those are drawn again through redrawable instead.
 */
function swapTheLabel(node, key, english) {
  if (!node) return;

  node.dataset.i18n = key;
  node.dataset.i18nSource = english;

  setLeadingText(node, t(key, english));
}

// --------------------------------------------------------------- calendars

/** The weekdays in the order a working week is written, Monday first; 0 is Sunday. */
const CALENDAR_WEEK = [1, 2, 3, 4, 5, 6, 0];

/** A weekday's name in the reader's language, in full or as short as it is written. */
function weekdayName(weekday, width = 'long') {
  // The 7th of January 2024 was a Sunday, so the 7th plus the weekday is that day.
  return new Intl.DateTimeFormat(activeLocale(), { weekday: width, timeZone: 'UTC' })
    .format(new Date(Date.UTC(2024, 0, 7 + weekday)));
}

/** Whose public holidays a calendar keeps, in words. */
function calendarHolidays(place) {
  if (!place) return t('calendar.installation', "the installation's");
  if (place === 'none') return t('holiday.noRegion', 'No public holidays');

  return place.includes('-') ? regionName(place) : countryName(place);
}

/**
 * Reads the calendars and draws their card, for whoever sets up queues; the
 * countries their public holidays are chosen from come with them, for somebody
 * whose screens never loaded the installation's holiday card.
 */
async function loadCalendars() {
  const page = await api('/calendars');
  cache.calendars = page?.items ?? [];
  if (page?.holidayCountries?.length) holidayCountries = page.holidayCountries;

  fillTable($('#table-calendar tbody'), cache.calendars.map(calendarRow), 5,
    t('calendar.empty', 'No calendars yet.'));

  const form = $('#form-calendar');
  if (form && !beingEdited(form) && !form.elements.id.value) fillCalendarForm(form, null);
}

/** One calendar as a row of its card's table. */
function calendarRow(calendar) {
  return el('tr', {},
    el('td', { class: 'text', text: calendar.name }),
    el('td', { class: 'text', text: calendar.zone || t('calendar.installation', "the installation's") }),
    el('td', { class: 'text', text: calendarHolidays(calendar.holidayPlace) }),
    el('td', { text: calendar.valid ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no') }),
    el('td', { class: 'actions' },
      el('button', { class: 'link', text: t('action.edit', 'edit'), onclick: () => editCalendar(calendar) }),
      deleteButton({
        label: `${t('entity.calendar', 'Calendar')} "${calendar.name}"`,
        path: `/calendars/${calendar.id}`,
        message: t('msg.calendarDeleted', 'Calendar deleted'),
        after: refreshAll,
      })),
  );
}

/**
 * Fills the calendar form from a calendar, or for a new one: the zone and the
 * public holidays offering the installation's own first, a box for each
 * weekday's hours and a row for each free day.
 */
function fillCalendarForm(form, calendar) {
  fillTimezoneSelect(form.elements.zone, calendar?.zone ?? '', t('calendar.installation', "the installation's"));
  fillHolidayPickers(form, calendar?.holidayPlace ?? '', '',
    el('option', { value: '', text: t('calendar.installation', "the installation's") }),
    el('option', { value: 'none', text: t('holiday.noRegion', 'No public holidays') }));

  $('#calendar-hours').replaceChildren(...CALENDAR_WEEK.map((weekday) => el('label', {},
    weekdayName(weekday),
    el('input', {
      type: 'text',
      name: `hours${weekday}`,
      maxlength: '120',
      pattern: HOURS_PATTERN,
      placeholder: '08:00–12:00, 13:00–17:00',
      value: (calendar?.hours ?? []).filter((stretch) => stretch.weekday === weekday)
        .map((stretch) => `${stretch.from}–${stretch.to}`).join(', '),
    }))));

  $('#calendar-free-days').replaceChildren(...(calendar?.freeDays ?? []).map(freeDayRow));
}

/**
 * One free day as a row of the calendar form: its date, whether it comes every
 * year, its name. Filled before it is put into the form, so that filling it is
 * not taken for somebody editing.
 */
function freeDayRow(free = { day: '', recurring: false, name: '' }) {
  const date = el('input', { type: 'date', name: 'freeDay', required: true });
  const row = el('div', { class: 'row calendar-day' },
    el('label', {}, t('calendar.day', 'Date'), date),
    el('label', { class: 'switch' },
      el('input', { type: 'checkbox', name: 'freeDayRecurring', checked: free.recurring }),
      el('span', { class: 'switch-track' }),
      el('span', { class: 'switch-label', text: t('calendar.everyYear', 'every year') })),
    el('label', {}, t('field.name', 'Name'),
      el('input', { type: 'text', name: 'freeDayName', maxlength: '255', value: free.name })),
    el('button', { type: 'button', class: 'link', text: t('calendar.removeDay', 'remove'), onclick: () => row.remove() }));

  enhanceDateFields(row);
  setDateField(date, free.day);

  return row;
}

/**
 * The hours the weekday boxes say. Each box is held to HOURS_PATTERN, which the
 * browser checks before the form is sent, so every stretch here reads; whether
 * stretches overlap or run backwards is the server's to refuse, which it does by
 * naming the hours.
 */
function calendarHoursOf(form) {
  const hours = [];

  for (const weekday of CALENDAR_WEEK) {
    for (const part of form.elements[`hours${weekday}`].value.split(',')) {
      const found = part.trim().match(/^(\d{2}:\d{2})\s*[-–]\s*(\d{2}:\d{2})$/);
      if (found) hours.push({ weekday, from: found[1], to: found[2] });
    }
  }

  return hours;
}

/** The free days the form's rows say. */
function calendarFreeDaysOf(form) {
  return $$('.calendar-day', form).map((row) => ({
    day: $('input[name="freeDay"]', row).value,
    recurring: $('input[name="freeDayRecurring"]', row).checked,
    name: $('input[name="freeDayName"]', row).value.trim(),
  }));
}

/** Sends the calendar form, making a calendar or correcting one. */
function saveCalendar(form) {
  const hours = calendarHoursOf(form);
  const id = form.elements.id.value;
  const body = JSON.stringify({
    name: form.elements.name.value.trim(),
    description: form.elements.description.value.trim(),
    zone: form.elements.zone.value,
    holidayPlace: chosenPlace(form),
    hours,
    freeDays: calendarFreeDaysOf(form),
    valid: form.elements.valid.checked,
  });

  mutate(
    () => (id ? api(`/calendars/${id}`, { method: 'PUT', body }) : api('/calendars', { method: 'POST', body })),
    id ? t('msg.calendarSaved', 'Calendar saved') : t('msg.calendarCreated', 'Calendar created'),
    async () => { resetCalendarForm(); await refreshAll(); },
  );
}

/** Opens a calendar in the form, to correct it. */
function editCalendar(calendar) {
  const form = $('#form-calendar');

  form.elements.id.value = String(calendar.id);
  form.elements.name.value = calendar.name;
  form.elements.description.value = calendar.description;
  form.elements.valid.checked = calendar.valid;
  fillCalendarForm(form, calendar);

  // Drawn rather than declared with a key, because it carries the calendar's
  // name; see editRole for why the key has to come off.
  const heading = $('#calendar-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('calendarFormTitle', () => {
    heading.textContent = t('calendar.edit', 'Edit the calendar {0}').replace('{0}', calendar.name);
  });

  swapTheLabel($('#calendar-submit'), 'action.save', 'Save');
  $('#calendar-cancel').hidden = false;
  form.elements.name.focus();
}

/** Puts the calendar form back to making a new one. */
function resetCalendarForm() {
  const form = $('#form-calendar');
  if (!form) return;

  form.reset();
  form.elements.id.value = '';
  fillCalendarForm(form, null);

  stopRedrawing('calendarFormTitle');
  swapTheLabel($('#calendar-form-title'), 'calendar.create', 'Add a calendar');
  swapTheLabel($('#calendar-submit'), 'action.create', 'Create');
  $('#calendar-cancel').hidden = true;
}

/** Wires the calendar form. */
function wireCalendars() {
  const form = $('#form-calendar');

  form.addEventListener('submit', (e) => { e.preventDefault(); saveCalendar(form); });
  form.elements.country.addEventListener('change', () => fillRegions(form));
  $('#calendar-add-day').addEventListener('click', () => $('#calendar-free-days').append(freeDayRow()));
  $('#calendar-cancel').addEventListener('click', resetCalendarForm);
}

// ---------------------------------------------------------- service levels

/** A service level's name, or "none" for a ticket or queue owed nothing. */
function levelName(id) {
  if (!id) return t('sla.none', 'none');

  return cache.levels.find((level) => level.id === id)?.name ?? `#${id}`;
}

/**
 * Fills a choice of service level: those still offered, and the one already
 * chosen, retired or not, because it may stay. What choosing nothing means is
 * the caller's to say - no level for a queue, the queue's for a new ticket.
 */
function fillLevelChoices(select, current, nothing) {
  fillSelect(select,
    cache.levels.filter((level) => level.valid || level.id === current)
      .map((level) => ({ value: String(level.id), label: level.name })),
    { placeholder: nothing, labelKey: 'label', valueKey: 'value' });
  select.value = current ? String(current) : '';
}

/** Draws the service levels' card, for whoever sets up queues. */
function loadSlaCard() {
  const calendarOf = (id) => cache.calendars.find((calendar) => calendar.id === id)?.name ?? `#${id}`;
  const minutes = (value) => (value ? String(value) : '–');

  fillTable($('#table-sla tbody'), cache.levels.map((level) => el('tr', {},
    el('td', { class: 'text', text: level.name }),
    el('td', { class: 'text', text: level.calendarId ? calendarOf(level.calendarId) : t('sla.aroundTheClock', 'around the clock') }),
    el('td', { text: minutes(level.firstResponseMinutes) }),
    el('td', { text: minutes(level.solutionMinutes) }),
    el('td', { text: level.valid ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no') }),
    el('td', { class: 'actions' },
      el('button', { class: 'link', text: t('action.edit', 'edit'), onclick: () => editSla(level) }),
      deleteButton({
        label: `${t('entity.sla', 'Service level')} "${level.name}"`,
        path: `/service-levels/${level.id}`,
        message: t('msg.slaDeleted', 'Service level deleted'),
        after: refreshAll,
      })),
  )), 6, t('sla.empty', 'No service levels yet.'));

  const form = $('#form-sla');
  if (form && !beingEdited(form) && !form.elements.id.value) fillSlaCalendars(form, null);
}

/** Fills the service level form's choice of calendar, around the clock first. */
function fillSlaCalendars(form, current) {
  fillSelect(form.elements.calendarId,
    cache.calendars.filter((calendar) => calendar.valid || calendar.id === current)
      .map((calendar) => ({ value: String(calendar.id), label: calendar.name })),
    { placeholder: t('sla.aroundTheClock', 'around the clock'), labelKey: 'label', valueKey: 'value' });
  form.elements.calendarId.value = current ? String(current) : '';
}

/** Opens a service level in the form, to correct it. */
function editSla(level) {
  const form = $('#form-sla');

  form.elements.id.value = String(level.id);
  form.elements.name.value = level.name;
  form.elements.description.value = level.description ?? '';
  form.elements.firstResponseMinutes.value = String(level.firstResponseMinutes);
  form.elements.updateMinutes.value = String(level.updateMinutes);
  form.elements.solutionMinutes.value = String(level.solutionMinutes);
  form.elements.valid.checked = level.valid;
  fillSlaCalendars(form, level.calendarId);

  // Drawn rather than declared with a key, because it carries the level's name;
  // see editRole for why the key has to come off.
  const heading = $('#sla-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('slaFormTitle', () => {
    heading.textContent = t('sla.edit', 'Edit the service level {0}').replace('{0}', level.name);
  });

  swapTheLabel($('#sla-submit'), 'action.save', 'Save');
  $('#sla-cancel').hidden = false;
  form.elements.name.focus();
}

/** Puts the service level form back to making a new one. */
function resetSlaForm() {
  const form = $('#form-sla');
  if (!form) return;

  form.reset();
  form.elements.id.value = '';
  fillSlaCalendars(form, null);

  stopRedrawing('slaFormTitle');
  swapTheLabel($('#sla-form-title'), 'sla.create', 'Add a service level');
  swapTheLabel($('#sla-submit'), 'action.create', 'Create');
  $('#sla-cancel').hidden = true;
}

/** Sends the service level form, making a level or correcting one. */
function saveSla(form) {
  const id = form.elements.id.value;
  const calendar = form.elements.calendarId.value;
  const body = JSON.stringify({
    name: form.elements.name.value.trim(),
    description: form.elements.description.value.trim(),
    firstResponseMinutes: Number(form.elements.firstResponseMinutes.value),
    updateMinutes: Number(form.elements.updateMinutes.value),
    solutionMinutes: Number(form.elements.solutionMinutes.value),
    valid: form.elements.valid.checked,

    // Said out loud when there is none: a calendar sent as nothing cannot be
    // told apart from one that was not sent, and a correction would keep it.
    ...(calendar ? { calendarId: Number(calendar) } : { clearCalendar: true }),
  });

  mutate(
    () => (id ? api(`/service-levels/${id}`, { method: 'PUT', body }) : api('/service-levels', { method: 'POST', body })),
    id ? t('msg.slaSaved', 'Service level saved') : t('msg.slaCreated', 'Service level created'),
    async () => { resetSlaForm(); await refreshAll(); },
  );
}

/** Wires the service level form. */
function wireSla() {
  const form = $('#form-sla');

  form.addEventListener('submit', (e) => { e.preventDefault(); saveSla(form); });
  $('#sla-cancel').addEventListener('click', resetSlaForm);
}

// -------------------------------------------------------------- automation

/**
 * What each step a macro can take is called. The server names them as KIX18
 * names its ticket macro actions, which is right for the wire and nobody's word
 * on a screen.
 */
const MACRO_STEP_TITLES = {
  StateSet: 'Set the state',
  PrioritySet: 'Set the priority',
  TypeSet: 'Set the type',
  TeamSet: 'Move to a queue',
  OwnerSet: 'Set the owner',
  ResponsibleSet: 'Set the responsible',
  LockSet: 'Lock or unlock',
  TitleSet: 'Set the title',
  ContactSet: 'Set the contact',
  OrganisationSet: 'Set the organisation',
  ArticleCreate: 'Add an article',
  TicketDelete: 'Delete the ticket',
};

/** A step a macro can take, in the reader's language. */
function macroStepName(type) {
  return t(`macrostep.${type}`, MACRO_STEP_TITLES[type] ?? type);
}

/**
 * How many steps one macro holds, as model.MaxMacroActions says. Written here
 * because the form needs it before it has asked anything;
 * TestTheRowsAFormAddsStopWhereTheServerDoes holds the two together.
 */
const MAX_MACRO_STEPS = 50;

/**
 * Fills the automation screen, for whoever may set it up. What a step and a
 * job's criteria can name is read through the automation's own choices, which
 * the right to set it up reads alone: a role allowed the automation and not the
 * tickets, the accounts or the customers was offered nothing to choose, while
 * the server took a step naming any record that is there.
 */
async function loadAutomation() {
  if (!can('automation:write')) {
    cache.macros = [];
    cache.macroSteps = [];
    cache.plans = [];
    cache.planEvents = [];
    cache.jobs = [];

    return;
  }

  const [macros, plans, jobs, choices] = await Promise.all(
    ['/macros', '/exec-plans', '/jobs', '/automation/choices'].map((path) => api(path)),
  );

  cache.macros = macros?.items ?? [];
  cache.macroSteps = macros?.steps ?? [];
  cache.plans = plans?.items ?? [];
  cache.planEvents = plans?.events ?? [];
  cache.jobs = jobs?.items ?? [];
  cache.stepChoices = {
    queues: choices?.queues ?? [],
    states: choices?.states ?? [],
    priorities: choices?.priorities ?? [],
    types: choices?.types ?? [],
    accounts: choices?.accounts ?? [],
  };

  fillTable($('#table-macro tbody'), cache.macros.map(macroRow), 4, t('macro.empty', 'No macros yet.'));
  fillSelect($('#macro-step-kind'),
    cache.macroSteps.map((step) => ({ value: step.type, label: macroStepName(step.type) })),
    { labelKey: 'label', valueKey: 'value' });

  const form = $('#form-macro');
  if (form && !beingEdited(form) && !form.elements.id.value) fillMacroSteps([]);

  fillTable($('#table-plan tbody'), cache.plans.map(planRow), 4, t('plan.empty', 'No execution plans yet.'));

  const planForm = $('#form-plan');
  if (planForm && !beingEdited(planForm) && !planForm.elements.id.value) fillPlanForm(planForm, null);

  fillTable($('#table-job tbody'), cache.jobs.map(jobRow), 6, t('job.empty', 'No jobs yet.'));
  fillSelect($('#job-macro-kind'),
    cache.macros.filter((macro) => macro.valid).map((macro) => ({ value: String(macro.id), label: macro.name })),
    { labelKey: 'label', valueKey: 'value' });

  const jobForm = $('#form-job');
  if (jobForm && !beingEdited(jobForm) && !jobForm.elements.id.value) fillJobForm(jobForm, null);
  showRowBounds();

  fillSelect($('#form-run')?.elements.job,
    cache.jobs.map((job) => ({ value: String(job.id), label: job.name })),
    { labelKey: 'label', valueKey: 'value' });
  await loadRuns();
}

/** One macro as a row of its card's table, its steps named in their order. */
function macroRow(macro) {
  const steps = macro.actions.map((action) => macroStepName(action.type)).join(', ');

  return el('tr', {},
    el('td', { class: 'text', text: macro.name }),
    el('td', { class: 'text', text: steps || t('macro.noSteps', 'none') }),
    el('td', { text: macro.valid ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no') }),
    el('td', { class: 'actions' },
      el('button', { class: 'link', text: t('action.edit', 'edit'), onclick: () => editMacro(macro) }),
      deleteButton({
        label: `${t('entity.macro', 'Macro')} "${macro.name}"`,
        path: `/macros/${macro.id}`,
        message: t('msg.macroDeleted', 'Macro deleted'),
        after: refreshAll,
      })),
  );
}

/** Puts a macro's steps into the form in their order, or none for a new one. */
function fillMacroSteps(actions) {
  $('#macro-steps').replaceChildren(...actions.map((action) => macroStepRow(action.type, action.parameters)));
  showRowBounds();
}

/**
 * Turns a button that adds a row off once its list holds as many as the server
 * keeps - a macro's steps, a job's macros - rather than letting somebody add one
 * more and be refused the whole record on Save, and while there is nothing to
 * add.
 */
function showRowBounds() {
  for (const [button, choice, rows, most] of [
    ['#macro-add-step', '#macro-step-kind', '#macro-steps > li', MAX_MACRO_STEPS],
    ['#job-add-macro', '#job-macro-kind', '#job-macros > li', MAX_JOB_MACROS],
  ]) {
    const add = $(button);
    if (add) add.disabled = !$(choice)?.options.length || $$(rows).length >= most;
  }
}

/**
 * Marks a form as being filled in once the rows it keeps in order change.
 * Adding, moving and taking out a row raise no input event, and a reload would
 * otherwise put the form back to what the server holds and take the rows with
 * it.
 */
function rowsChanged(form) {
  form.dataset.editing = 'yes';
  showRowBounds();
}

/**
 * One step of the macro form: what it does, a field for each value it reads in
 * the order its definition gives them, and the buttons that move it and take it
 * out. A step stays the kind it was added as; another kind is another step.
 */
function macroStepRow(type, parameters = {}) {
  const definition = cache.macroSteps.find((step) => step.type === type);
  const row = el('li', { 'data-step': type });
  const name = el('strong', {});

  // Declared with its key, so that a change of language reaches a step drawn
  // before it.
  swapTheLabel(name, `macrostep.${type}`, MACRO_STEP_TITLES[type] ?? type);

  row.append(
    orderedHead(row, name, '#macro-step-kind'),
    el('div', { class: 'grid' },
      ...(definition?.parameters ?? []).map((parameter) => stepField(parameter, parameters[parameter.name] ?? ''))),
  );

  if (type === 'StateSet') showWaiting(row);

  return row;
}

/**
 * The line heading a row of a list a form keeps in order: what the row is, and
 * the buttons that move it and take it out. The buttons are declared with their
 * keys, so that a change of language reaches a row drawn before it; a row taken
 * out hands the focus to the choice of the next one to add.
 */
function orderedHead(row, name, next) {
  const button = (mark, press) => {
    const made = el('button', { type: 'button', class: `link ${mark}` });
    made.addEventListener('click', () => press(made));

    return made;
  };
  const up = button('ordered-up', (pressed) => moveRow(row, pressed, -1));
  const down = button('ordered-down', (pressed) => moveRow(row, pressed, 1));
  const out = button('ordered-remove', () => {
    const form = row.closest('form');

    row.remove();
    rowsChanged(form);
    $(next).focus();
  });

  swapTheLabel(up, 'macro.up', 'move up');
  swapTheLabel(down, 'macro.down', 'move down');
  swapTheLabel(out, 'macro.removeStep', 'remove');

  return el('div', { class: 'row ordered-head' }, name, up, down, out);
}

/**
 * Moves a row one place up or down. Moving a node takes the focus off it, so the
 * button that was pressed is given it back: somebody moving a row from the
 * keyboard presses again to move it further.
 */
function moveRow(row, button, by) {
  const neighbour = by < 0 ? row.previousElementSibling : row.nextElementSibling;
  if (!neighbour) return;

  if (by < 0) neighbour.before(row);
  else neighbour.after(row);

  button.focus();
  rowsChanged(row.closest('form'));
}

/**
 * A step's field for one value it reads, as the value's kind asks: a record
 * chosen by its id, a channel, a switch, a number of minutes or a text, bounded
 * as the server bounds it. A person, a contact or an organisation can be left
 * empty, which is how a step takes the ticket's away; a record a step needs is
 * chosen rather than defaulted to whichever comes first.
 *
 * The fields carry no name, so that a draft of the form leaves them alone: a
 * step is a row the script makes, and a draft can only put values back into
 * fields that are there.
 */
function stepField(parameter, value) {
  if (parameter.kind === 'switch') return stepSwitch(parameter, value);
  if (parameter.kind === 'contact' || parameter.kind === 'organisation') return searchedStep(parameter, value);

  let control;

  if (parameter.kind === 'text') {
    control = el('input', { type: 'text', maxlength: String(parameter.max), required: parameter.required, value });
  } else if (parameter.kind === 'longText') {
    control = el('textarea', { rows: '4', maxlength: String(parameter.max), required: parameter.required });
    control.value = value;
  } else if (parameter.kind === 'minutes') {
    control = el('input', {
      type: 'number', min: '0', max: String(parameter.max), step: '1', required: parameter.required, value,
    });
  } else {
    let placeholder = t('ticket.nobody', 'nobody');
    if (parameter.required) placeholder = parameter.kind === 'channel' ? undefined : t('macro.choose', 'Choose one');

    control = el('select', { required: parameter.required });
    fillSelect(control, stepChoices(parameter.kind, value), { placeholder, labelKey: 'label', valueKey: 'value' });
    if (value || placeholder) control.value = value;
  }

  control.dataset.parameter = parameter.name;

  return fieldLabelled(parameter.name, control, parameter.kind === 'longText');
}

/** A label naming a field as FIELD_NAMES does, around its control. */
function fieldLabelled(name, control, wide = false) {
  const label = el('label', wide ? { class: 'wide' } : {});

  swapTheLabel(label, `field.${name}`, FIELD_NAMES[name] ?? name);
  label.append(control);

  return label;
}

/**
 * What a step can name of one kind: the records still offered, and the one it
 * names already, retired or not, so that opening a macro does not quietly change
 * it - the server refuses a retired one on Save and says which step names it.
 */
function stepChoices(kind, current) {
  const { queues, states, priorities, types } = cache.stepChoices;
  const kept = (items) => items.filter((item) => item.valid || String(item.id) === current);
  const byId = new Map(queues.map((queue) => [queue.id, queue]));

  const choices = {
    state: () => kept(states).filter((state) => state.type !== 'merged')
      .map((state) => ({ value: String(state.id), label: statusName(state.name) })),
    priority: () => kept(priorities)
      .map((priority) => ({ value: String(priority.id), label: priorityName(priority.name) })),
    type: () => kept(types).map((type) => ({ value: String(type.id), label: ticketTypeName(type.name) })),
    queue: () => kept(queues).map((queue) => ({ value: String(queue.id), label: queuePath(queue, byId) }))
      .sort((a, b) => a.label.localeCompare(b.label)),
    user: () => cache.stepChoices.accounts.map((user) => ({ value: String(user.id), label: user.name })),
    channel: () => AGENT_CHANNELS.map((channel) => ({ value: channel, label: channelName(channel) })),
  }[kind]?.() ?? [];

  if (current && !choices.some((choice) => choice.value === current)) {
    choices.unshift({ value: current, label: `#${current}` });
  }

  return choices;
}

/** A step's switch, which the server reads as "true" or "false". */
function stepSwitch(parameter, value) {
  const box = el('input', { type: 'checkbox', checked: value === 'true' });
  const words = el('span', { class: 'switch-label' });

  box.dataset.parameter = parameter.name;
  swapTheLabel(words, `field.${parameter.name}`, FIELD_NAMES[parameter.name] ?? parameter.name);

  return el('label', { class: 'switch' }, box, el('span', { class: 'switch-track' }), words);
}

/** What the search beside a step's contact or organisation is called. */
const STEP_SEARCHES = {
  contact: { label: ['ticket.contactSearch', 'Find the contact'], hint: ['ticket.contactSearchHint', 'name or address'] },
  organisation: {
    label: ['customer.organisationSearch', 'Find the organisation'],
    hint: ['customer.organisationSearchHint', 'name or number'],
  },
};

/**
 * Where a step searches for a contact or an organisation: the automation's own
 * searches, which the right to set it up reads. The paths alone: how an entry
 * is written is SEARCHED_KINDS', which is declared further down and read when a
 * step is drawn, because reading it here, while the script is still being run,
 * stopped the whole script before anything was wired.
 */
const STEP_SEARCH_PATHS = {
  contact: '/automation/contacts',
  organisation: '/automation/organisations',
};

/**
 * A step's choice of contact or organisation, each of which runs to thousands:
 * a box to search in and a select of what it finds, as every such choice is -
 * see SEARCHED_KINDS. The one the step names is kept among the options whatever
 * a search finds, and named once its record has been read.
 */
function searchedStep(parameter, value) {
  const kind = { path: STEP_SEARCH_PATHS[parameter.kind], describe: SEARCHED_KINDS[parameter.kind].describe };
  const words = STEP_SEARCHES[parameter.kind];
  const select = el('select', {});
  const reads = can('automation:write');
  let asked = 0;
  let typing;

  select.dataset.parameter = parameter.name;

  const offer = (entries, keep) => {
    const choices = entries.map((entry) => ({ value: String(entry.id), label: kind.describe(entry) }));
    if (keep && !choices.some((choice) => choice.value === keep.value)) choices.unshift(keep);

    fillSelect(select, choices, { placeholder: t('ticket.nobody', 'nobody'), labelKey: 'label', valueKey: 'value' });
    select.value = keep?.value ?? '';
  };

  offer([], keptEntry(value, ''));

  if (value && reads) {
    api(`${kind.path}?id=${encodeURIComponent(value)}`)
      .then((found) => {
        const record = found?.items?.[0];
        if (record && select.value === value) offer([], keptEntry(value, kind.describe(record)));
      })
      .catch((err) => toastFailure(err));
  }

  if (!reads) return fieldLabelled(parameter.name, select);

  const search = el('input', {
    type: 'search',
    maxlength: '255',
    placeholder: t(...words.hint),
    'data-i18n-placeholder': words.hint[0],
    'data-i18n-placeholder-source': words.hint[1],
  });
  const searchLabel = el('label', {});

  swapTheLabel(searchLabel, ...words.label);
  searchLabel.append(search);

  search.addEventListener('input', () => {
    clearTimeout(typing);
    typing = setTimeout(async () => {
      const mine = ++asked;
      const query = new URLSearchParams({ limit: String(SEARCH_OFFERS), valid: 'true' });
      if (search.value.trim()) query.set('q', search.value.trim());

      try {
        const entries = (await api(`${kind.path}?${query}`))?.items ?? [];
        const kept = select.value
          ? { value: select.value, label: select.selectedOptions[0]?.textContent ?? `#${select.value}` }
          : null;

        // A later search, or a form put back in the meantime, has the say.
        if (mine === asked && select.isConnected) offer(entries, kept);
      } catch (err) {
        toastFailure(err);
      }
    }, 300);
  });

  return el('div', { class: 'grid-passthrough' }, searchLabel, fieldLabelled(parameter.name, select));
}

/**
 * Shows the minutes a waiting state waits for beside a state that waits, and
 * asks for them there: the server wants them for such a state and refuses them
 * for any other.
 */
function showWaiting(row) {
  const state = $('[data-parameter="stateId"]', row);
  const minutes = $('[data-parameter="pendingMinutes"]', row);
  if (!state || !minutes) return;

  const show = () => {
    const chosen = cache.stepChoices.states.find((candidate) => String(candidate.id) === state.value);
    const waits = chosen?.type === 'pending-reminder' || chosen?.type === 'pending-auto';

    minutes.closest('label').hidden = !waits;
    minutes.required = waits;
    minutes.min = waits ? '1' : '0';
    if (!waits) minutes.value = '';
  };

  state.addEventListener('change', show);
  show();
}

/** The steps the form holds, in order, with what each of their fields says. */
function macroStepsOf() {
  return $$('#macro-steps > li').map((row) => {
    const parameters = {};

    for (const field of $$('[data-parameter]', row)) {
      const value = field.type === 'checkbox' ? String(field.checked) : field.value.trim();
      if (value) parameters[field.dataset.parameter] = value;
    }

    return { type: row.dataset.step, parameters };
  });
}

/** Sends the macro form, making a macro or correcting one. */
function saveMacro(form) {
  const id = form.elements.id.value;
  const body = JSON.stringify({
    name: form.elements.name.value.trim(),
    description: form.elements.description.value.trim(),
    actions: macroStepsOf(),
    valid: form.elements.valid.checked,
  });

  mutate(
    () => (id ? api(`/macros/${id}`, { method: 'PUT', body }) : api('/macros', { method: 'POST', body })),
    id ? t('msg.macroSaved', 'Macro saved') : t('msg.macroCreated', 'Macro created'),
    async () => { resetMacroForm(); await refreshAll(); },
  );
}

/** Opens a macro in the form, to correct it. */
function editMacro(macro) {
  const form = $('#form-macro');

  form.elements.id.value = String(macro.id);
  form.elements.name.value = macro.name;
  form.elements.description.value = macro.description;
  form.elements.valid.checked = macro.valid;
  fillMacroSteps(macro.actions);

  // Drawn rather than declared with a key, because it carries the macro's name;
  // see editRole for why the key has to come off.
  const heading = $('#macro-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('macroFormTitle', () => {
    heading.textContent = t('macro.edit', 'Edit the macro {0}').replace('{0}', macro.name);
  });

  swapTheLabel($('#macro-submit'), 'action.save', 'Save');
  $('#macro-cancel').hidden = false;
  form.elements.name.focus();
}

/** Puts the macro form back to making a new one. */
function resetMacroForm() {
  const form = $('#form-macro');
  if (!form) return;

  form.reset();
  form.elements.id.value = '';
  fillMacroSteps([]);

  stopRedrawing('macroFormTitle');
  swapTheLabel($('#macro-form-title'), 'macro.create', 'Add a macro');
  swapTheLabel($('#macro-submit'), 'action.create', 'Create');
  $('#macro-cancel').hidden = true;
}

/**
 * What each event a plan can run on is called. The server names them as KIX18
 * names its ticket events.
 */
const PLAN_EVENT_TITLES = {
  TicketCreate: 'Ticket opened',
  TicketTitleUpdate: 'Title changed',
  TicketQueueUpdate: 'Moved to another queue',
  TicketStateUpdate: 'State changed',
  TicketPriorityUpdate: 'Priority changed',
  TicketTypeUpdate: 'Type changed',
  TicketOwnerUpdate: 'Owner changed',
  TicketResponsibleUpdate: 'Responsible changed',
  TicketCustomerUpdate: 'Contact or organisation changed',
  TicketLockUpdate: 'Locked or unlocked',
  TicketPendingTimeUpdate: 'Waiting time changed',
  ArticleCreate: 'Article added',
};

/** An event a plan can run on, in the reader's language. */
function planEventName(event) {
  return t(`planevent.${event}`, PLAN_EVENT_TITLES[event] ?? event);
}

/** When a plan runs, in words: its weekdays and times, or the events it waits for. */
function planWhen(plan) {
  if (plan.type === 'EventBased') return plan.events.map(planEventName).join(', ');

  const days = CALENDAR_WEEK.filter((day) => plan.weekdays.includes(day)).map((day) => weekdayName(day, 'short'));

  return fillIn(t('plan.atTimes', '{0} at {1}'), [days.join(', '), plan.times.join(', ')]);
}

/** One plan as a row of its card's table. */
function planRow(plan) {
  return el('tr', {},
    el('td', { class: 'text', text: plan.name }),
    el('td', { class: 'text', text: planWhen(plan) }),
    el('td', { text: plan.valid ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no') }),
    el('td', { class: 'actions' },
      el('button', { class: 'link', text: t('action.edit', 'edit'), onclick: () => editPlan(plan) }),
      deleteButton({
        label: `${t('entity.plan', 'Execution plan')} "${plan.name}"`,
        path: `/exec-plans/${plan.id}`,
        message: t('msg.planDeleted', 'Execution plan deleted'),
        after: refreshAll,
      })),
  );
}

/**
 * Fills the plan form from a plan, or for a new one: a switch for each weekday,
 * Monday first, and one for each event the server lists, on as the plan has
 * them. The switches carry no name, for the reason the steps of a macro carry
 * none.
 */
function fillPlanForm(form, plan) {
  form.elements.type.value = plan?.type ?? 'TimeBased';
  form.elements.times.value = (plan?.times ?? []).join(', ');

  $('#plan-weekdays').replaceChildren(...CALENDAR_WEEK.map((day) =>
    choiceSwitch({ 'data-weekday': String(day) }, weekdayName(day), plan?.weekdays.includes(day) ?? false)));
  $('#plan-events').replaceChildren(...cache.planEvents.map((event) =>
    choiceSwitch({ 'data-event': event }, planEventName(event), plan?.events.includes(event) ?? false)));

  showPlanKind(form);
}

/** One switch of a form's choice of several - a weekday, an event - on or off, told apart by its mark. */
function choiceSwitch(mark, words, on) {
  return el('label', { class: 'switch perm-switch' },
    el('input', { type: 'checkbox', checked: on, ...mark }),
    el('span', { class: 'switch-track' }),
    el('span', { class: 'perm-name', text: words }));
}

/**
 * Shows what the plan's kind reads and puts the other kind's away. A hidden
 * field that is still required cannot be submitted past, so the times are
 * required only while they are on screen.
 */
function showPlanKind(form) {
  const timed = form.elements.type.value === 'TimeBased';

  $('#plan-time').hidden = !timed;
  $('#plan-event').hidden = timed;
  form.elements.times.required = timed;
}

/**
 * Sends the plan form, making a plan or correcting one. The other kind's values
 * go as nothing, because the server refuses them for a plan they do not apply
 * to rather than keeping them to look as if they did.
 */
function savePlan(form) {
  const id = form.elements.id.value;
  const timed = form.elements.type.value === 'TimeBased';
  const on = (selector) => $$(selector, form).filter((box) => box.checked);
  const body = JSON.stringify({
    name: form.elements.name.value.trim(),
    description: form.elements.description.value.trim(),
    type: form.elements.type.value,
    weekdays: timed ? on('[data-weekday]').map((box) => Number(box.dataset.weekday)) : [],
    times: timed ? form.elements.times.value.split(',').map((time) => time.trim()).filter(Boolean) : [],
    events: timed ? [] : on('[data-event]').map((box) => box.dataset.event),
    valid: form.elements.valid.checked,
  });

  mutate(
    () => (id ? api(`/exec-plans/${id}`, { method: 'PUT', body }) : api('/exec-plans', { method: 'POST', body })),
    id ? t('msg.planSaved', 'Execution plan saved') : t('msg.planCreated', 'Execution plan created'),
    async () => { resetPlanForm(); await refreshAll(); },
  );
}

/** Opens a plan in the form, to correct it. */
function editPlan(plan) {
  const form = $('#form-plan');

  form.elements.id.value = String(plan.id);
  form.elements.name.value = plan.name;
  form.elements.description.value = plan.description;
  form.elements.valid.checked = plan.valid;
  fillPlanForm(form, plan);

  // Drawn rather than declared with a key, because it carries the plan's name;
  // see editRole for why the key has to come off.
  const heading = $('#plan-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('planFormTitle', () => {
    heading.textContent = t('plan.edit', 'Edit the execution plan {0}').replace('{0}', plan.name);
  });

  swapTheLabel($('#plan-submit'), 'action.save', 'Save');
  $('#plan-cancel').hidden = false;
  form.elements.name.focus();
}

/** Puts the plan form back to making a new one. */
function resetPlanForm() {
  const form = $('#form-plan');
  if (!form) return;

  form.reset();
  form.elements.id.value = '';
  fillPlanForm(form, null);

  stopRedrawing('planFormTitle');
  swapTheLabel($('#plan-form-title'), 'plan.create', 'Add an execution plan');
  swapTheLabel($('#plan-submit'), 'action.create', 'Create');
  $('#plan-cancel').hidden = true;
}

/**
 * How many macros one job runs, as model.MaxJobMacros says; written here for
 * the reason MAX_MACRO_STEPS is.
 */
const MAX_JOB_MACROS = 20;

/** What started a run, in words. */
const RUN_TRIGGER_TITLES = {
  manual: 'by hand',
  time: "at its plan's time",
  event: 'on an event',
};

/** What started a run, in the reader's language. */
function runTriggerName(trigger) {
  return t(`runtrigger.${trigger}`, RUN_TRIGGER_TITLES[trigger] ?? trigger);
}

/** A macro's name, or its number where it is gone. */
function macroName(id) {
  return cache.macros.find((macro) => macro.id === id)?.name ?? `#${id}`;
}

/** One job as a row of its card's table. */
function jobRow(job) {
  const planName = (id) => cache.plans.find((plan) => plan.id === id)?.name ?? `#${id}`;

  return el('tr', {},
    el('td', { class: 'text', text: job.name }),
    el('td', { class: 'text', text: job.macroIds.map(macroName).join(', ') }),
    el('td', { class: 'text', text: job.planIds.map(planName).join(', ') || t('job.byHandOnly', 'by hand only') }),
    el('td', { text: fmtMoment(job.lastRunAt) }),
    el('td', { text: job.valid ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no') }),
    el('td', { class: 'actions' },
      el('button', { class: 'link', text: t('action.edit', 'edit'), onclick: () => editJob(job) }),
      el('button', { class: 'link', text: t('job.run', 'run now'), onclick: (e) => runJob(job, e.currentTarget) }),
      deleteButton({
        label: `${t('entity.job', 'Job')} "${job.name}"`,
        path: `/jobs/${job.id}`,
        message: t('msg.jobDeleted', 'Job deleted'),
        after: refreshAll,
      })),
  );
}

/**
 * Runs a job now, says what it did, and shows its runs below. The button is off
 * while the run takes its time, which on a few hundred tickets is a while; see
 * holdFocus for why it is asked first.
 */
function runJob(job, button) {
  const back = holdFocus(button);
  button.disabled = true;

  mutate(
    () => api(`/jobs/${job.id}/run`, { method: 'POST' }),
    null,
    async (run) => {
      toast(fillIn(t('job.ran', 'The job ran on {0} ticket(s): {1} changed, {2} refused.'),
        [run.tickets, run.changed, run.failed]), 'ok');
      $('#form-run').elements.job.value = String(job.id);
      await refreshAll();
    },
  ).finally(() => {
    button.disabled = false;
    back();
  });
}

/**
 * Fills the job form from a job, or for a new one: a switch for every queue,
 * state, priority and type a ticket can be taken by - retired ones among them,
 * because a job may well be for the tickets still carrying one - the macros in
 * the order the job runs them, and a switch for every plan still offered and
 * every plan the job already names.
 */
function fillJobForm(form, job) {
  fillCriteria(form, 'job', cache.stepChoices, job?.criteria);

  $('#job-macros').replaceChildren(...(job?.macroIds ?? []).map(jobMacroRow));
  $('#job-plans').replaceChildren(...cache.plans
    .filter((plan) => plan.valid || job?.planIds.includes(plan.id))
    .map((plan) => choiceSwitch({ 'data-plan': String(plan.id) }, plan.name, job?.planIds.includes(plan.id) ?? false)));

  showRowBounds();
}

/**
 * Fills a form's switches for the tickets something is about - a switch for
 * every queue, state, priority and type in lists, retired ones among them,
 * because a job or a notification may well be for the tickets still carrying
 * one - on as the criteria have them, and its choice of lock. The switches go
 * into the elements named prefix-queues, prefix-states and so on.
 */
function fillCriteria(form, prefix, lists, criteria) {
  const byId = new Map(lists.queues.map((queue) => [queue.id, queue]));
  const on = (list, id) => (criteria?.[list] ?? []).includes(id);
  const locked = criteria?.locked;

  $(`#${prefix}-queues`).replaceChildren(...lists.queues
    .map((queue) => ({ id: queue.id, words: queuePath(queue, byId) }))
    .sort((a, b) => a.words.localeCompare(b.words))
    .map((queue) => choiceSwitch({ 'data-queue': String(queue.id) }, queue.words, on('queueIds', queue.id))));
  $(`#${prefix}-states`).replaceChildren(...lists.states.map((state) =>
    choiceSwitch({ 'data-state': String(state.id) }, statusName(state.name), on('stateIds', state.id))));
  $(`#${prefix}-priorities`).replaceChildren(...lists.priorities.map((priority) =>
    choiceSwitch({ 'data-priority': String(priority.id) }, priorityName(priority.name),
      on('priorityIds', priority.id))));
  $(`#${prefix}-types`).replaceChildren(...lists.types.map((type) =>
    choiceSwitch({ 'data-type': String(type.id) }, ticketTypeName(type.name), on('typeIds', type.id))));

  form.elements.locked.value = locked === true || locked === false ? String(locked) : '';
}

/**
 * The tickets a form's switches say something is about. A list with nothing
 * switched on goes as nothing, which takes any.
 */
function criteriaOf(form) {
  const on = (mark) => $$(`[data-${mark}]`, form).filter((box) => box.checked)
    .map((box) => Number(box.getAttribute(`data-${mark}`)));
  const locked = form.elements.locked.value;

  return {
    queueIds: on('queue'),
    stateIds: on('state'),
    priorityIds: on('priority'),
    typeIds: on('type'),
    locked: locked ? locked === 'true' : null,
  };
}

/** One macro of the job form, in the place the job runs it. */
function jobMacroRow(id) {
  const row = el('li', { 'data-macro': String(id) });

  row.append(orderedHead(row, el('strong', { text: macroName(id) }), '#job-macro-kind'));

  return row;
}

/**
 * Sends the job form, making a job or correcting one. A list of criteria with
 * nothing switched on goes as nothing, which takes any.
 */
function saveJob(form) {
  const id = form.elements.id.value;
  const plans = $$('[data-plan]', form).filter((box) => box.checked).map((box) => Number(box.dataset.plan));
  const body = JSON.stringify({
    name: form.elements.name.value.trim(),
    description: form.elements.description.value.trim(),
    criteria: criteriaOf(form),
    macroIds: $$('#job-macros > li').map((row) => Number(row.dataset.macro)),
    planIds: plans,
    valid: form.elements.valid.checked,
  });

  mutate(
    () => (id ? api(`/jobs/${id}`, { method: 'PUT', body }) : api('/jobs', { method: 'POST', body })),
    id ? t('msg.jobSaved', 'Job saved') : t('msg.jobCreated', 'Job created'),
    async () => { resetJobForm(); await refreshAll(); },
  );
}

/** Opens a job in the form, to correct it. */
function editJob(job) {
  const form = $('#form-job');

  form.elements.id.value = String(job.id);
  form.elements.name.value = job.name;
  form.elements.description.value = job.description;
  form.elements.valid.checked = job.valid;
  fillJobForm(form, job);

  // Drawn rather than declared with a key, because it carries the job's name;
  // see editRole for why the key has to come off.
  const heading = $('#job-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('jobFormTitle', () => {
    heading.textContent = t('job.edit', 'Edit the job {0}').replace('{0}', job.name);
  });

  swapTheLabel($('#job-submit'), 'action.save', 'Save');
  $('#job-cancel').hidden = false;
  form.elements.name.focus();
}

/** Puts the job form back to making a new one. */
function resetJobForm() {
  const form = $('#form-job');
  if (!form) return;

  form.reset();
  form.elements.id.value = '';
  fillJobForm(form, null);

  stopRedrawing('jobFormTitle');
  swapTheLabel($('#job-form-title'), 'job.create', 'Add a job');
  swapTheLabel($('#job-submit'), 'action.create', 'Create');
  $('#job-cancel').hidden = true;
}

/**
 * Draws what the chosen job's latest runs did: when each ran, what started it,
 * how many tickets it took and changed, and every step it could not take, in
 * the reader's words. Drawn again from the answer in hand when the language
 * changes, and dropped when a later choice of job has the say.
 */
async function loadRuns() {
  const tbody = $('#table-run tbody');
  const id = $('#form-run')?.elements.job.value;
  if (!tbody) return;

  if (!id) {
    stopRedrawing('jobRuns');
    fillTable(tbody, [], 6, t('run.noJob', 'There is no job yet.'));

    return;
  }

  const runs = (await api(`/jobs/${id}/runs`))?.items ?? [];
  if ($('#form-run').elements.job.value !== id) return;

  redrawable('jobRuns', () => fillTable(tbody, runs.map(runRow), 6, t('run.empty', 'This job has not run yet.')));
}

/** One run as a row of its card's table, with every line it kept. */
function runRow(run) {
  const lines = run.lines.map((line) => {
    const where = [
      line.macroId ? macroName(line.macroId) : '',
      line.step ? fillIn(t('run.step', 'step {0}'), [line.step]) : '',
    ].filter(Boolean).join(' · ');
    const said = el('span', {});

    showRefusal(said, runLineRefusal(line));

    return el('li', {},
      line.ticketId
        ? el('button', {
          type: 'button',
          class: 'link',
          text: fillIn(t('run.ticket', 'Ticket {0}'), [`#${line.ticketId}`]),
          onclick: () => { switchView('tickets'); openTicket(line.ticketId); },
        })
        : null,
      el('span', { text: line.ticketId ? ` · ${where}: ` : `${where}: ` }),
      said);
  });

  return el('tr', {},
    el('td', { text: fmtMoment(run.startedAt) }),
    el('td', { class: 'text', text: runTriggerName(run.trigger) }),
    el('td', { text: String(run.tickets) }),
    el('td', { text: String(run.changed) }),
    el('td', { text: String(run.failed) }),
    el('td', { class: 'text' }, lines.length ? el('ul', { class: 'run-lines' }, ...lines) : '–'),
  );
}

/**
 * A line of a run as the refusal it keeps, shaped as the API would have
 * answered it, so that it is said the way any other refusal is: the fields of
 * an invalid request named as their form names them, a code in the reader's
 * words with its values written as the screen writes them, and the English of a
 * failure nobody translated folded away under it.
 */
function runLineRefusal(line) {
  if (line.code === 'invalidFields') return { param: line.values };

  return {
    code: line.code, values: line.values, message: line.message, detail: line.code === 'internal' ? line.message : '',
  };
}

/** Wires the automation screen's forms. */
function wireAutomation() {
  const macroForm = $('#form-macro');
  const planForm = $('#form-plan');
  const jobForm = $('#form-job');
  const runForm = $('#form-run');

  jobForm.addEventListener('submit', (e) => { e.preventDefault(); saveJob(jobForm); });
  $('#job-cancel').addEventListener('click', resetJobForm);
  $('#job-add-macro').addEventListener('click', () => {
    const id = Number($('#job-macro-kind').value);
    if (!id) return;

    const row = jobMacroRow(id);
    $('#job-macros').append(row);
    rowsChanged(jobForm);
    $('button', row)?.focus();
  });

  // A choice of what to look at rather than something to send.
  runForm.addEventListener('submit', (e) => e.preventDefault());
  runForm.elements.job.addEventListener('change', () => loadRuns().catch((err) => toastFailure(err)));

  planForm.addEventListener('submit', (e) => { e.preventDefault(); savePlan(planForm); });
  planForm.elements.type.addEventListener('change', () => showPlanKind(planForm));
  $('#plan-cancel').addEventListener('click', resetPlanForm);

  macroForm.addEventListener('submit', (e) => { e.preventDefault(); saveMacro(macroForm); });
  $('#macro-cancel').addEventListener('click', resetMacroForm);

  $('#macro-add-step').addEventListener('click', () => {
    const type = $('#macro-step-kind').value;
    if (!type) return;

    const row = macroStepRow(type);
    $('#macro-steps').append(row);
    rowsChanged(macroForm);
    $('select, input, textarea', row)?.focus();
  });
}

// ------------------------------------------------------------ notifications

/** What a notification has to work with before anything has been read: nothing. */
function notifyingNothing() {
  return {
    items: [], events: [], recipients: [], languages: [], subjectLimit: 0, bodyLimit: 0,
    lists: { queues: [], states: [], priorities: [], types: [] },
  };
}

/**
 * Who a notification can be sent to, in words. The server names them as KIX18
 * names its recipients.
 */
const RECIPIENT_TITLES = {
  AgentOwner: 'The owner',
  AgentResponsible: 'The responsible agent',
  AgentWatcher: 'Those watching the ticket',
  AgentMyQueues: 'Those who chose its queue',
  AgentWritePermissions: 'Those who work in its queue',
  AgentReadPermissions: 'Those who can read its queue',
};

/** Who a notification can be sent to, in the reader's language. */
function recipientName(recipient) {
  return t(`recipient.${recipient}`, RECIPIENT_TITLES[recipient] ?? recipient);
}

/**
 * Draws the notifications' card from the list the server sent: the table, and
 * the form where nobody is filling it in. lists are the queues, states,
 * priorities and types a notification's tickets can be named by.
 */
function drawNotifications(page, lists) {
  cache.notifying = {
    items: page?.items ?? [],
    events: page?.events ?? [],
    recipients: page?.recipients ?? [],
    languages: page?.languages ?? [],
    subjectLimit: page?.subjectLimit ?? 0,
    bodyLimit: page?.bodyLimit ?? 0,
    lists,
  };

  fillTable($('#table-notification tbody'), cache.notifying.items.map(notificationRow), 5,
    t('notification.empty', 'No notifications yet.'));

  const form = $('#form-notification');
  if (form && !beingEdited(form) && !form.elements.id.value) fillNotificationForm(form, null);
}

/** One notification as a row of its card's table. */
function notificationRow(definition) {
  return el('tr', {},
    el('td', { class: 'text', text: definition.name }),
    el('td', { class: 'text', text: definition.events.map(planEventName).join(', ') }),
    el('td', { class: 'text', text: definition.recipients.map(recipientName).join(', ') }),
    el('td', { text: definition.valid ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no') }),
    el('td', { class: 'actions' },
      el('button', { class: 'link', text: t('action.edit', 'edit'), onclick: () => editNotification(definition) }),
      deleteButton({
        label: `${t('entity.notification', 'Notification')} "${definition.name}"`,
        path: `/notification-definitions/${definition.id}`,
        message: t('msg.notificationDeleted', 'Notification deleted'),
        after: refreshAll,
      })),
  );
}

/**
 * Fills the notification form from a notification, or for a new one: a switch
 * for every event and every recipient the server lists, the tickets it is about,
 * and a subject and a text for every language the installation speaks.
 */
function fillNotificationForm(form, definition) {
  const { events, recipients, languages, lists } = cache.notifying;

  $('#notification-events').replaceChildren(...events.map((event) =>
    choiceSwitch({ 'data-event': event }, planEventName(event), definition?.events.includes(event) ?? false)));
  $('#notification-recipients').replaceChildren(...recipients.map((recipient) =>
    choiceSwitch({ 'data-recipient': recipient }, recipientName(recipient),
      definition?.recipients.includes(recipient) ?? false)));

  fillCriteria(form, 'notification', lists, definition?.criteria);

  $('#notification-messages').replaceChildren(...languages.map((language) =>
    notificationMessage(language, definition?.messages.find((message) => message.language === language))));
}

/**
 * What a notification says in one language: a subject and a text, bounded as
 * the server bounds them. Either asks for the other once it holds something, so
 * that a language half written is not sent to be refused; a language left empty
 * is not spoken.
 */
function notificationMessage(language, message) {
  const subject = el('input', {
    type: 'text', maxlength: String(cache.notifying.subjectLimit), value: message?.subject ?? '',
    'data-language': language, 'data-part': 'subject',
  });
  const body = el('textarea', {
    rows: '4', maxlength: String(cache.notifying.bodyLimit), 'data-language': language, 'data-part': 'body',
  });
  const pair = () => {
    const spoken = Boolean(subject.value.trim() || body.value.trim());
    subject.required = spoken;
    body.required = spoken;
  };

  body.value = message?.body ?? '';
  subject.addEventListener('input', pair);
  body.addEventListener('input', pair);
  pair();

  return el('fieldset', { class: 'notification-message' },
    el('legend', { text: LANGUAGES[language] ?? language }),
    el('div', { class: 'grid' }, fieldLabelled('subject', subject), fieldLabelled('body', body, true)));
}

/** What the notification form says, in each language it speaks. */
function notificationMessagesOf(form) {
  return cache.notifying.languages.map((language) => ({
    language,
    subject: $(`[data-language="${language}"][data-part="subject"]`, form)?.value.trim() ?? '',
    body: $(`[data-language="${language}"][data-part="body"]`, form)?.value.trim() ?? '',
  })).filter((message) => message.subject || message.body);
}

/** Sends the notification form, making a notification or correcting one. */
function saveNotification(form) {
  const id = form.elements.id.value;
  const on = (mark) => $$(`[data-${mark}]`, form).filter((box) => box.checked)
    .map((box) => box.getAttribute(`data-${mark}`));
  const body = JSON.stringify({
    name: form.elements.name.value.trim(),
    description: form.elements.description.value.trim(),
    events: on('event'),
    recipients: on('recipient'),
    criteria: criteriaOf(form),
    oncePerDay: form.elements.oncePerDay.checked,
    optional: form.elements.optional.checked,
    messages: notificationMessagesOf(form),
    valid: form.elements.valid.checked,
  });

  mutate(
    () => (id
      ? api(`/notification-definitions/${id}`, { method: 'PUT', body })
      : api('/notification-definitions', { method: 'POST', body })),
    id ? t('msg.notificationSaved', 'Notification saved') : t('msg.notificationCreated', 'Notification created'),
    async () => { resetNotificationForm(); await refreshAll(); },
  );
}

/** Opens a notification in the form, to correct it. */
function editNotification(definition) {
  const form = $('#form-notification');

  form.elements.id.value = String(definition.id);
  form.elements.name.value = definition.name;
  form.elements.description.value = definition.description;
  form.elements.oncePerDay.checked = definition.oncePerDay;
  form.elements.optional.checked = definition.optional;
  form.elements.valid.checked = definition.valid;
  fillNotificationForm(form, definition);

  // Drawn rather than declared with a key, because it carries the notification's
  // name; see editRole for why the key has to come off.
  const heading = $('#notification-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('notificationFormTitle', () => {
    heading.textContent = t('notification.edit', 'Edit the notification {0}').replace('{0}', definition.name);
  });

  swapTheLabel($('#notification-submit'), 'action.save', 'Save');
  $('#notification-cancel').hidden = false;
  form.elements.name.focus();
}

/** Puts the notification form back to making a new one. */
function resetNotificationForm() {
  const form = $('#form-notification');
  if (!form) return;

  form.reset();
  form.elements.id.value = '';
  fillNotificationForm(form, null);

  stopRedrawing('notificationFormTitle');
  swapTheLabel($('#notification-form-title'), 'notification.create', 'Add a notification');
  swapTheLabel($('#notification-submit'), 'action.create', 'Create');
  $('#notification-cancel').hidden = true;
}

/** Wires the notification form. */
function wireNotifications() {
  const form = $('#form-notification');

  form.addEventListener('submit', (e) => { e.preventDefault(); saveNotification(form); });
  $('#notification-cancel').addEventListener('click', resetNotificationForm);
}

// --------------------------------------------------------- answer templates

/**
 * Draws the answer templates' card, and the form where nobody is filling it in.
 * queues are the ones a template can be offered in.
 */
function drawTemplates(page, queues) {
  cache.templates = page?.items ?? [];
  cache.templateQueues = queues;

  const byId = new Map(queues.map((queue) => [queue.id, queue]));
  const queueName = (id) => {
    if (!id) return t('template.everyQueue', 'every queue');

    return byId.has(id) ? queuePath(byId.get(id), byId) : `#${id}`;
  };

  fillTable($('#table-template tbody'), cache.templates.map((template) => el('tr', {},
    el('td', { class: 'text', text: template.name }),
    el('td', { class: 'text', text: queueName(template.queueId) }),
    el('td', { text: template.valid ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no') }),
    el('td', { class: 'actions' },
      el('button', { class: 'link', text: t('action.edit', 'edit'), onclick: () => editTemplate(template) }),
      deleteButton({
        label: `${t('entity.template', 'Answer template')} "${template.name}"`,
        path: `/response-templates/${template.id}`,
        message: t('msg.templateDeleted', 'Answer template deleted'),
        after: refreshAll,
      })),
  )), 4, t('template.empty', 'No answer templates yet.'));

  const form = $('#form-template');
  if (form && !beingEdited(form) && !form.elements.id.value) fillTemplateQueues(form, null);
}

/**
 * Fills the template form's choice of queue: every queue, or one of those still
 * offered, and the one the template names already, retired or not.
 */
function fillTemplateQueues(form, current) {
  const queues = cache.templateQueues.filter((queue) => queue.valid || queue.id === current);
  const byId = new Map(cache.templateQueues.map((queue) => [queue.id, queue]));

  fillSelect(form.elements.queueId,
    queues.map((queue) => ({ value: String(queue.id), label: queuePath(queue, byId) }))
      .sort((a, b) => a.label.localeCompare(b.label)),
    { placeholder: t('template.everyQueue', 'every queue'), labelKey: 'label', valueKey: 'value' });
  form.elements.queueId.value = current ? String(current) : '';
}

/** Sends the template form, making a template or correcting one. */
function saveTemplate(form) {
  const id = form.elements.id.value;
  const queue = form.elements.queueId.value;
  const body = JSON.stringify({
    name: form.elements.name.value.trim(),
    subject: form.elements.subject.value.trim(),
    body: form.elements.body.value.trim(),
    valid: form.elements.valid.checked,
    ...(queue ? { queueId: Number(queue) } : { clearQueue: true }),
  });

  mutate(
    () => (id ? api(`/response-templates/${id}`, { method: 'PUT', body }) : api('/response-templates', { method: 'POST', body })),
    id ? t('msg.templateSaved', 'Answer template saved') : t('msg.templateCreated', 'Answer template created'),
    async () => { resetTemplateForm(); await refreshAll(); },
  );
}

/** Opens a template in the form, to correct it. */
function editTemplate(template) {
  const form = $('#form-template');

  form.elements.id.value = String(template.id);
  form.elements.name.value = template.name;
  form.elements.subject.value = template.subject;
  form.elements.body.value = template.body;
  form.elements.valid.checked = template.valid;
  fillTemplateQueues(form, template.queueId);

  // Drawn rather than declared with a key, because it carries the template's
  // name; see editRole for why the key has to come off.
  const heading = $('#template-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('templateFormTitle', () => {
    heading.textContent = t('template.edit', 'Edit the answer template {0}').replace('{0}', template.name);
  });

  swapTheLabel($('#template-submit'), 'action.save', 'Save');
  $('#template-cancel').hidden = false;
  form.elements.name.focus();
}

/** Puts the template form back to making a new one. */
function resetTemplateForm() {
  const form = $('#form-template');
  if (!form) return;

  form.reset();
  form.elements.id.value = '';
  fillTemplateQueues(form, null);

  stopRedrawing('templateFormTitle');
  swapTheLabel($('#template-form-title'), 'template.create', 'Add an answer template');
  swapTheLabel($('#template-submit'), 'action.create', 'Create');
  $('#template-cancel').hidden = true;
}

/**
 * Offers the templates of the ticket that is open in the article form. Not over
 * an article somebody is part way through writing, whose choice of template is
 * theirs.
 */
function offerTemplates(templates) {
  const article = $('#form-article');
  cache.ticketTemplates = templates;

  if (!article || beingEdited(article)) return;

  fillSelect(article.elements.template,
    templates.map((template) => ({ value: String(template.id), label: template.name })),
    { placeholder: t('ticket.noTemplate', 'none'), labelKey: 'label', valueKey: 'value' });
  article.elements.template.value = '';
  article.elements.template.closest('label').hidden = templates.length === 0;
}

/**
 * Starts the article from the template chosen: its subject where the article has
 * none yet, and its text after whatever is written already, so that choosing a
 * template never takes away what somebody wrote. The choice goes back to none,
 * so that the same template can be chosen again.
 */
function useTemplate(article) {
  const chosen = cache.ticketTemplates.find((template) => String(template.id) === article.elements.template.value);
  if (!chosen) return;

  if (!article.elements.subject.value.trim()) article.elements.subject.value = chosen.subject;

  const body = article.elements.body;
  body.value = body.value.trim() ? `${body.value.trimEnd()}\n\n${chosen.body}` : chosen.body;

  article.dataset.editing = 'yes';
  article.elements.template.value = '';
  body.focus();
}

/** Wires the template form, and the choice of template in the article form. */
function wireTemplates() {
  const form = $('#form-template');
  const article = $('#form-article');

  form.addEventListener('submit', (e) => { e.preventDefault(); saveTemplate(form); });
  $('#template-cancel').addEventListener('click', resetTemplateForm);
  article.elements.template.addEventListener('change', () => useTemplate(article));
}

// ----------------------------------------------------------- knowledge base

/** Who may read an article, in words; the server names them as KIX18 does. */
const FAQ_VISIBILITY_TITLES = {
  internal: 'the staff alone',
  external: 'customers signed in, and the staff',
  public: 'anybody',
};

/** Who may read an article, in the reader's language. */
function faqVisibilityName(visibility) {
  return t(`faqvisibility.${visibility}`, FAQ_VISIBILITY_TITLES[visibility] ?? visibility);
}

/** A category of the knowledge base by the path of the categories above it. */
function faqCategoryPath(id) {
  const byId = new Map(cache.faqCategories.map((category) => [category.id, category]));
  const category = byId.get(id);

  return category ? queuePath(category, byId) : `#${id}`;
}

/**
 * Fills the knowledge base's screen, for whoever reads it: the categories, the
 * search's choices and what it finds, and - for whoever writes it - the forms
 * where nobody is filling them in.
 */
async function loadFAQ() {
  if (!can('faq:read')) return;

  const categories = await api('/faq/categories');
  cache.faqCategories = categories?.items ?? [];

  const choices = cache.faqCategories
    .map((category) => ({ value: String(category.id), label: faqCategoryPath(category.id) }))
    .sort((a, b) => a.label.localeCompare(b.label));

  const search = $('#form-faq-search');
  if (search && !beingEdited(search)) {
    fillSelect(search.elements.category, choices,
      { placeholder: t('faq.everyCategory', 'every category'), labelKey: 'label', valueKey: 'value' });
  }

  drawFAQCategories();
  await loadFAQArticles();

  const form = $('#form-faq-article');
  if (form && !beingEdited(form) && !form.elements.id.value) fillFAQArticleForm(form, null);
}

/** Reads what the search finds, and draws it. */
async function loadFAQArticles() {
  const search = $('#form-faq-search');
  const query = new URLSearchParams();

  if (search?.elements.q.value.trim()) query.set('q', search.elements.q.value.trim());
  if (search?.elements.category.value) query.set('category', search.elements.category.value);

  const page = await api(`/faq/articles?${query}`);
  cache.faqVisibilities = page?.visibilities ?? [];

  fillTable($('#table-faq tbody'), (page?.items ?? []).map((article) => el('tr', {},
    el('td', { class: 'text' },
      el('button', { class: 'link', text: article.title, onclick: () => openFAQArticle(article.id) })),
    el('td', { class: 'text', text: faqCategoryPath(article.categoryId) }),
    el('td', { class: 'text', text: faqVisibilityName(article.visibility) }),
    el('td', { text: fmtMoment(article.updatedAt) }),
    el('td', { class: 'actions' }, ...(can('faq:write')
      ? [
        el('button', { class: 'link', text: t('action.edit', 'edit'), onclick: () => editFAQArticle(article) }),
        deleteButton({
          label: `${t('entity.faqarticle', 'Article')} "${article.title}"`,
          path: `/faq/articles/${article.id}`,
          message: t('msg.faqArticleDeleted', 'Article deleted'),
          after: async () => { closeFAQArticle(); await loadFAQ(); },
        }),
      ]
      : [])),
  )), 5, t('faq.empty', 'Nothing found.'));
}

/** The article that is open, as the server last answered it, or none. */
let faqArticleShown = null;

/** Opens an article to read it: what it says, and what it is filed under. */
async function openFAQArticle(id) {
  const article = await api(`/faq/articles/${id}`);
  if (!article) return;

  faqArticleShown = article;

  const fact = (term, value) => [el('dt', { text: term }), el('dd', { text: value })];

  $('#faq-article-title').textContent = article.title;
  $('#faq-article-facts').replaceChildren(
    ...fact(t('field.categoryId', 'Category'), faqCategoryPath(article.categoryId)),
    ...fact(t('field.visibility', 'Who may read it'), faqVisibilityName(article.visibility)),
    ...(article.language ? fact(t('field.language', 'Language'), LANGUAGES[article.language] ?? article.language) : []),
    ...(article.keywords ? fact(t('field.keywords', 'Keywords'), article.keywords) : []),
    ...fact(t('faq.updated', 'Written'),
      `${fmtMoment(article.updatedAt)}${article.updatedByName ? ` – ${article.updatedByName}` : ''}`),
  );
  $('#faq-article-body').textContent = article.body;
  $('#faq-article').hidden = false;
  $('#faq-article').scrollIntoView({ block: 'start', behavior: 'smooth' });
}

/** Puts the open article away. */
function closeFAQArticle() {
  faqArticleShown = null;
  $('#faq-article').hidden = true;
  $('#faq-article-title').textContent = '';
  $('#faq-article-facts').replaceChildren();
  $('#faq-article-body').textContent = '';
}

/**
 * Fills the article form's choices: the categories still offered and the one
 * the article is filed under, retired or not; who may read it, the staff alone
 * for a new one; and its language, any for a new one.
 */
function fillFAQArticleForm(form, article) {
  const offered = cache.faqCategories.filter((category) => category.valid || category.id === article?.categoryId);

  fillSelect(form.elements.categoryId,
    offered.map((category) => ({ value: String(category.id), label: faqCategoryPath(category.id) }))
      .sort((a, b) => a.label.localeCompare(b.label)),
    { labelKey: 'label', valueKey: 'value' });
  fillSelect(form.elements.visibility,
    (cache.faqVisibilities.length ? cache.faqVisibilities : Object.keys(FAQ_VISIBILITY_TITLES))
      .map((visibility) => ({ value: visibility, label: faqVisibilityName(visibility) })),
    { labelKey: 'label', valueKey: 'value' });
  fillSelect(form.elements.language,
    Object.entries(LANGUAGES).map(([code, name]) => ({ value: code, label: name })),
    { placeholder: t('faq.everyLanguage', 'every language'), labelKey: 'label', valueKey: 'value' });

  form.elements.categoryId.value = article ? String(article.categoryId) : (offered[0] ? String(offered[0].id) : '');
  form.elements.visibility.value = article?.visibility ?? 'internal';
  form.elements.language.value = article?.language ?? '';
}

/** Sends the article form, writing an article or correcting one. */
function saveFAQArticle(form) {
  const id = form.elements.id.value;
  const body = JSON.stringify({
    title: form.elements.title.value.trim(),
    categoryId: Number(form.elements.categoryId.value),
    visibility: form.elements.visibility.value,
    language: form.elements.language.value,
    keywords: form.elements.keywords.value.trim(),
    body: form.elements.body.value.trim(),
    valid: form.elements.valid.checked,
  });

  mutate(
    () => (id ? api(`/faq/articles/${id}`, { method: 'PUT', body }) : api('/faq/articles', { method: 'POST', body })),
    id ? t('msg.faqArticleSaved', 'Article saved') : t('msg.faqArticleCreated', 'Article written'),
    async (written) => {
      resetFAQArticleForm();
      await loadFAQ();
      if (written?.id) await openFAQArticle(written.id);
    },
  );
}

/** Opens the article that is shown in the form, to correct it. */
function editFAQArticle(article) {
  const form = $('#form-faq-article');

  form.elements.id.value = String(article.id);
  form.elements.title.value = article.title;
  form.elements.keywords.value = article.keywords;
  form.elements.body.value = article.body;
  form.elements.valid.checked = article.valid;
  fillFAQArticleForm(form, article);

  // Drawn rather than declared with a key, because it carries the article's
  // title; see editRole for why the key has to come off.
  const heading = $('#faq-article-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('faqArticleFormTitle', () => {
    heading.textContent = t('faq.edit', 'Edit the article {0}').replace('{0}', article.title);
  });

  swapTheLabel($('#faq-article-submit'), 'action.save', 'Save');
  $('#faq-article-cancel').hidden = false;
  form.scrollIntoView({ block: 'start', behavior: 'smooth' });
  form.elements.title.focus();
}

/** Puts the article form back to writing a new one. */
function resetFAQArticleForm() {
  const form = $('#form-faq-article');
  if (!form) return;

  form.reset();
  form.elements.id.value = '';
  fillFAQArticleForm(form, null);

  stopRedrawing('faqArticleFormTitle');
  swapTheLabel($('#faq-article-form-title'), 'faq.create', 'Write an article');
  swapTheLabel($('#faq-article-submit'), 'action.create', 'Create');
  $('#faq-article-cancel').hidden = true;
}

/** Draws the categories' card, and its form where nobody is filling it in. */
function drawFAQCategories() {
  fillTable($('#table-faq-category tbody'), cache.faqCategories
    .map((category) => ({ category, path: faqCategoryPath(category.id) }))
    .sort((a, b) => a.path.localeCompare(b.path))
    .map(({ category, path }) => el('tr', {},
      el('td', { class: 'text', text: path }),
      el('td', { class: 'text', text: category.description }),
      el('td', { text: category.valid ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no') }),
      el('td', { class: 'actions' },
        el('button', { class: 'link', text: t('action.edit', 'edit'), onclick: () => editFAQCategory(category) }),
        deleteButton({
          label: `${t('entity.faqcategory', 'Category')} "${category.name}"`,
          path: `/faq/categories/${category.id}`,
          message: t('msg.faqCategoryDeleted', 'Category deleted'),
          after: loadFAQ,
        })),
    )), 4, t('faq.categoriesEmpty', 'No categories yet.'));

  const form = $('#form-faq-category');
  if (form && !beingEdited(form) && !form.elements.id.value) fillFAQParents(form, null);
}

/** Fills the category form's choice of parent: the top, or any other category. */
function fillFAQParents(form, category) {
  fillSelect(form.elements.parentId,
    cache.faqCategories.filter((other) => other.id !== category?.id)
      .map((other) => ({ value: String(other.id), label: faqCategoryPath(other.id) }))
      .sort((a, b) => a.label.localeCompare(b.label)),
    { placeholder: t('faq.top', 'nothing - at the top'), labelKey: 'label', valueKey: 'value' });
  form.elements.parentId.value = category?.parentId ? String(category.parentId) : '';
}

/** Sends the category form, making a category or correcting one. */
function saveFAQCategory(form) {
  const id = form.elements.id.value;
  const parent = form.elements.parentId.value;
  const body = JSON.stringify({
    name: form.elements.name.value.trim(),
    description: form.elements.description.value.trim(),
    valid: form.elements.valid.checked,
    ...(parent ? { parentId: Number(parent) } : { clearParent: true }),
  });

  mutate(
    () => (id ? api(`/faq/categories/${id}`, { method: 'PUT', body }) : api('/faq/categories', { method: 'POST', body })),
    id ? t('msg.faqCategorySaved', 'Category saved') : t('msg.faqCategoryCreated', 'Category created'),
    async () => { resetFAQCategoryForm(); await loadFAQ(); },
  );
}

/** Opens a category in the form, to correct it. */
function editFAQCategory(category) {
  const form = $('#form-faq-category');

  form.elements.id.value = String(category.id);
  form.elements.name.value = category.name;
  form.elements.description.value = category.description;
  form.elements.valid.checked = category.valid;
  fillFAQParents(form, category);

  const heading = $('#faq-category-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('faqCategoryFormTitle', () => {
    heading.textContent = t('faq.categoryEdit', 'Edit the category {0}').replace('{0}', category.name);
  });

  swapTheLabel($('#faq-category-submit'), 'action.save', 'Save');
  $('#faq-category-cancel').hidden = false;
  form.elements.name.focus();
}

/** Puts the category form back to making a new one. */
function resetFAQCategoryForm() {
  const form = $('#form-faq-category');
  if (!form) return;

  form.reset();
  form.elements.id.value = '';
  fillFAQParents(form, null);

  stopRedrawing('faqCategoryFormTitle');
  swapTheLabel($('#faq-category-form-title'), 'faq.categoryCreate', 'Add a category');
  swapTheLabel($('#faq-category-submit'), 'action.create', 'Create');
  $('#faq-category-cancel').hidden = true;
}

/** Wires the knowledge base's screen. */
function wireFAQ() {
  const search = $('#form-faq-search');
  const article = $('#form-faq-article');
  const category = $('#form-faq-category');

  search.addEventListener('submit', (e) => {
    e.preventDefault();
    loadFAQArticles().catch((err) => toastFailure(err));
  });
  article.addEventListener('submit', (e) => { e.preventDefault(); saveFAQArticle(article); });
  category.addEventListener('submit', (e) => { e.preventDefault(); saveFAQCategory(category); });
  $('#faq-article-cancel').addEventListener('click', resetFAQArticleForm);
  $('#faq-category-cancel').addEventListener('click', resetFAQCategoryForm);
  $('#faq-article-close').addEventListener('click', closeFAQArticle);
  $('#faq-article-edit').addEventListener('click', () => { if (faqArticleShown) editFAQArticle(faqArticleShown); });
}

// ---------------------------------------------------- system configuration

/**
 * The value an option of the system configuration has, as the server last said.
 * Every account signed in is handed the external options, which are the ones a
 * screen needs; whoever administers is handed all of them.
 */
function configValue(name) {
  return cache.sysconfig.find((option) => option.name === name)?.value;
}

/**
 * A ticket's number as the installation writes it, KIX18's way: after the word
 * it calls a ticket and the divider, Ticket::Hook and Ticket::HookDivider.
 */
function ticketName(number) {
  return `${configValue('Ticket::Hook') ?? ''}${configValue('Ticket::HookDivider') ?? ''}${number}`;
}

/** What an option does, in the reader's language; the English is the server's. */
function configDescription(option) {
  return t(`sysconfig.${option.name}`, option.description);
}

/**
 * A value of an option as the card shows it: a switch as on or off, a calendar
 * by its name - none counts around the clock - and nothing as a word.
 */
function configShown(option, value) {
  if (option.type === 'bool') return value ? t('change.on', 'on') : t('change.off', 'off');

  if (option.type === 'calendar') {
    if (value === null || value === undefined) return t('sla.aroundTheClock', 'around the clock');

    return (cache.calendars ?? []).find((calendar) => calendar.id === value)?.name ?? `#${value}`;
  }

  return value === '' ? t('sysconfig.nothing', 'nothing') : value;
}

/** Reads the system configuration, and draws its card for whoever administers. */
async function loadSysConfig() {
  cache.sysconfig = (await api('/settings/sysconfig'))?.items ?? [];

  const tbody = $('#table-sysconfig tbody');
  if (!tbody || !can('settings:manage')) return;

  // The names of the calendars an option may name, read here as well because
  // nothing on this screen reads them otherwise; whoever cannot set up queues
  // sees an option's calendar by its number.
  if (cache.sysconfig.some((option) => option.type === 'calendar') && can('queues:write')) {
    cache.calendars = (await api('/calendars'))?.items ?? [];
  }

  fillTable(tbody, cache.sysconfig.map(sysconfigRow), 4, t('sysconfig.none', 'No options.'));
}

/** One option as a row of the card's table. */
function sysconfigRow(option) {
  const value = configShown(option, option.value);

  return el('tr', {},
    el('td', { class: 'text' },
      el('strong', { text: configDescription(option) }),
      el('br', {}),
      el('code', { class: 'muted', text: option.name })),
    el('td', { class: 'text', text: option.modified ? `${value} · ${t('sysconfig.changed', 'changed')}` : value }),
    el('td', { class: 'text', text: configShown(option, option.default) }),
    el('td', { class: 'actions' },
      el('button', { class: 'link', text: t('action.edit', 'edit'), onclick: () => editSysConfig(option) })),
  );
}

/**
 * Opens an option in the card's form, to change it: a line of text bounded as
 * the server bounds it, or a switch.
 */
function editSysConfig(option) {
  const form = $('#form-sysconfig');
  const text = option.type === 'string';
  const calendar = option.type === 'calendar';

  form.hidden = false;
  form.elements.id.value = option.name;
  $('.sysconfig-text', form).hidden = !text;
  $('.sysconfig-switch', form).hidden = option.type !== 'bool';
  $('.sysconfig-calendar', form).hidden = !calendar;
  form.elements.text.maxLength = option.maxLength || 255;
  form.elements.text.value = text ? option.value : '';
  form.elements.enabled.checked = option.value === true;

  // Around the clock first, then the calendars still offered - and the one the
  // option names, offered or not, so that opening it does not quietly change it.
  if (calendar) {
    fillSelect(form.elements.calendar,
      (cache.calendars ?? []).filter((entry) => entry.valid || entry.id === option.value)
        .map((entry) => ({ value: String(entry.id), label: entry.name })),
      { placeholder: t('sla.aroundTheClock', 'around the clock'), labelKey: 'label', valueKey: 'value' });
    form.elements.calendar.value = option.value ? String(option.value) : '';
  }
  $('#sysconfig-default').hidden = !option.modified;
  $('#sysconfig-cancel').hidden = false;

  // Drawn rather than declared with a key, because it carries the option; see
  // editRole for why the key has to come off.
  const heading = $('#sysconfig-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('sysconfigForm', () => {
    heading.textContent = option.name;
    $('#sysconfig-description').textContent = configDescription(option);
  });

  form.elements[{ string: 'text', calendar: 'calendar' }[option.type] ?? 'enabled'].focus();
}

/** Puts the card's form away, holding no option. */
function resetSysConfigForm() {
  const form = $('#form-sysconfig');
  if (!form) return;

  form.reset();
  form.elements.id.value = '';
  form.hidden = true;

  stopRedrawing('sysconfigForm');
  swapTheLabel($('#sysconfig-form-title'), 'sysconfig.formTitle', 'Change an option');
  $('#sysconfig-description').textContent = '';
  $('#sysconfig-default').hidden = true;
  $('#sysconfig-cancel').hidden = true;
}

/**
 * Sends the option in the card's form. Text goes as it was typed, not trimmed:
 * a colon and a space can be the point.
 */
function saveSysConfig(form) {
  const option = cache.sysconfig.find((candidate) => candidate.name === form.elements.id.value);
  if (!option) return;

  const chosen = form.elements.calendar.value;
  const value = {
    bool: () => form.elements.enabled.checked,
    calendar: () => (chosen ? Number(chosen) : null),
  }[option.type]?.() ?? form.elements.text.value;

  mutate(
    () => api(`/settings/sysconfig/${encodeURIComponent(option.name)}`, { method: 'PUT', body: JSON.stringify({ value }) }),
    t('msg.sysconfigSaved', 'Option saved'),
    async () => { resetSysConfigForm(); await refreshAll(); },
  );
}

/** Puts the option in the card's form back to its default. */
function putSysConfigBack(form) {
  const name = form.elements.id.value;
  if (!name) return;

  mutate(
    () => api(`/settings/sysconfig/${encodeURIComponent(name)}`, { method: 'DELETE' }),
    t('msg.sysconfigReset', 'Option put back to its default'),
    async () => { resetSysConfigForm(); await refreshAll(); },
  );
}

/** Wires the system configuration's form. */
function wireSysConfig() {
  const form = $('#form-sysconfig');

  form.addEventListener('submit', (e) => { e.preventDefault(); saveSysConfig(form); });
  $('#sysconfig-default').addEventListener('click', () => putSysConfigBack(form));
  $('#sysconfig-cancel').addEventListener('click', resetSysConfigForm);
}

// -------------------------------------------------------- searched choices

/**
 * What a searched choice can name, and how a choice reads.
 *
 * Contacts and organisations run to thousands where accounts and queues run to
 * dozens, and a listing answers two hundred at most - so a select filled from
 * one cannot hold them all, and a select missing the entry a record names
 * saves the record as naming nobody: filled that way, the organisation came off
 * every contact and every ticket of an organisation past the two hundredth the
 * moment either was saved. Each such choice is therefore a select with a search
 * box beside it.
 */
const SEARCHED_KINDS = {
  contact: {
    path: '/contacts',
    describe: (contact) => (contact.email ? `${contact.name} <${contact.email}>` : contact.name),
  },
  organisation: {
    path: '/organisations',
    describe: (organisation) => (organisation.number
      ? `${organisation.name} (${organisation.number})` : organisation.name),
  },
};

/**
 * Every searched choice: its form, its search box, its select, what it names,
 * and what it says while it names nothing. A filter finds retired entries as
 * well, because their contacts are still there to be found; a form offers only
 * what may still be chosen.
 */
const SEARCHED = {
  contactFilterOrganisation: {
    form: '#form-contact-filter', search: 'organisationSearch', select: 'organisation', kind: 'organisation',
    retired: true, none: () => t('customer.everyOrganisation', 'every organisation'),
  },
  contactOrganisation: {
    form: '#form-contact', search: 'organisationSearch', select: 'organisationId', kind: 'organisation',
    none: () => t('ticket.nobody', 'nobody'),
  },
  ticketContact: {
    form: '#form-ticket', search: 'contactSearch', select: 'contactId', kind: 'contact',
    none: () => t('ticket.nobody', 'nobody'),
  },
  ticketOrganisation: {
    form: '#form-ticket', search: 'organisationSearch', select: 'organisationId', kind: 'organisation',
    none: () => t('ticket.nobody', 'nobody'),
  },
};

/** How many entries a search offers: more wants better words, not scrolling. */
const SEARCH_OFFERS = 20;

/** A record's own entry, kept by its choice whatever a search finds, or none. */
function keptEntry(id, name) {
  return id ? { value: String(id), label: name || `#${id}` } : null;
}

/**
 * Fills a searched choice with what its search last found, and keeps the entry
 * given - a record's own, or null for a new record - or, given nothing, the one
 * it names now. The kept entry stays among the options, retired or not, so no
 * search can take away what the form was going to keep.
 */
function offerFound(name, keep) {
  const choice = SEARCHED[name];
  const select = $(choice.form)?.elements[choice.select];
  if (!select) return;

  const named = select.value
    ? { value: select.value, label: select.selectedOptions[0]?.textContent ?? `#${select.value}` }
    : null;
  const kept = keep === undefined ? named : keep;
  const choices = (cache.found[name]?.entries ?? []).map((entry) => ({
    value: String(entry.id), label: SEARCHED_KINDS[choice.kind].describe(entry),
  }));

  if (kept && !choices.some((offered) => offered.value === kept.value)) choices.unshift(kept);

  fillSelect(select, choices, { placeholder: choice.none(), labelKey: 'label', valueKey: 'value' });
  select.value = kept?.value ?? '';
}

/**
 * Searches what a choice may name for the words in its search box, and offers
 * the first of them. An answer that arrives after a later search was sent, or
 * after a sign-out, is dropped: it would offer what the box no longer says, or
 * somebody else's customers.
 */
async function searchChoices(name) {
  const choice = SEARCHED[name];
  const form = $(choice.form);
  if (!form) return;

  const search = { entries: cache.found[name]?.entries ?? [] };
  cache.found[name] = search;

  const query = new URLSearchParams({ limit: String(SEARCH_OFFERS) });
  if (!choice.retired) query.set('valid', 'true');

  const words = form.elements[choice.search].value.trim();
  if (words) query.set('q', words);

  const entries = (await api(`${SEARCHED_KINDS[choice.kind].path}?${query}`))?.items ?? [];
  if (cache.found[name] !== search) return;

  search.entries = entries;
  offerFound(name);
}

/** Has every search box fill its choice as somebody types. */
function wireSearches() {
  for (const [name, choice] of Object.entries(SEARCHED)) {
    let typing;

    $(choice.form).elements[choice.search].addEventListener('input', () => {
      clearTimeout(typing);
      typing = setTimeout(() => searchChoices(name).catch((err) => toastFailure(err)), 300);
    });
  }
}

// --------------------------------------------------------------- customers

/**
 * The two cards of the customer screen - organisations and contacts - each a
 * table and one form that makes an entry or corrects one, told apart by the
 * hidden id as every form here is.
 */
const CUSTOMER_FORMS = {
  organisation: {
    path: '/organisations',
    fields: ['name', 'number', 'url', 'address', 'note'],
    create: ['customer.organisationCreate', 'Add an organisation'],
    editing: (entry) => t('customer.organisationEdit', 'Edit the organisation {0}').replace('{0}', entry.name),
    created: () => t('msg.organisationCreated', 'Organisation created'),
    saved: () => t('msg.organisationSaved', 'Organisation saved'),
  },
  contact: {
    path: '/contacts',
    fields: ['name', 'email', 'phone', 'note'],
    create: ['customer.contactCreate', 'Add a contact'],
    editing: (entry) => t('customer.contactEdit', 'Edit the contact {0}').replace('{0}', entry.name),
    created: () => t('msg.contactCreated', 'Contact created'),
    saved: () => t('msg.contactSaved', 'Contact saved'),
  },
};

/** How many entries a customer card lists before it asks for a narrower search. */
const CUSTOMER_PAGE = 50;

/**
 * Fills the customer screen, for whoever may read customers, and its choices of
 * organisation where nobody is filling them in.
 */
async function loadCustomers() {
  if (!can('customers:read')) return;

  await Promise.all([
    loadOrganisationPage(),
    loadContactPage(),
    beingEdited($('#form-contact-filter')) ? null : searchChoices('contactFilterOrganisation'),
    beingEdited($('#form-contact')) ? null : searchChoices('contactOrganisation'),
  ]);
}

/** Says, under a card's table, when the search found more than it lists. */
function customerOverflow(line, shown, total) {
  if (!line) return;

  line.textContent = total > shown
    ? fillIn(t('customer.more', 'Showing {0} of {1} - narrow the search to find the rest.'), [shown, total])
    : '';
}

/** Reads the organisations the search asks for, and draws them. */
async function loadOrganisationPage() {
  const tbody = $('#table-organisation tbody');
  const filter = $('#form-organisation-filter');
  if (!tbody || !filter) return;

  const query = new URLSearchParams({ limit: String(CUSTOMER_PAGE) });
  if (filter.elements.q.value.trim()) query.set('q', filter.elements.q.value.trim());

  const page = await api(`/organisations?${query}`);
  const items = page?.items ?? [];

  fillTable(tbody, items.map(organisationRow), 5, t('customer.organisationsEmpty', 'No organisations.'));
  customerOverflow($('#organisation-overflow'), items.length, page?.totalCount ?? 0);
}

/** One organisation as a row of its table. */
function organisationRow(organisation) {
  return el('tr', {},
    el('td', { class: 'text', text: organisation.name }),
    el('td', { class: 'text', text: organisation.number || '–' }),
    el('td', { class: 'text' }, organisation.url
      ? el('a', { href: organisation.url, text: organisation.url, target: '_blank', rel: 'noopener noreferrer' })
      : '–'),
    el('td', { text: organisation.valid ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no') }),
    el('td', { class: 'actions' },
      el('button', { class: 'link', text: t('action.edit', 'edit'), onclick: () => editCustomer('organisation', organisation) }),
      deleteButton({
        label: `${t('field.organisationId', 'Organisation')} "${organisation.name}"`,
        path: `/organisations/${organisation.id}`,
        message: t('msg.organisationDeleted', 'Organisation deleted'),
        after: refreshAll,
      })),
  );
}

/** Reads the contacts the search asks for, and draws them. */
async function loadContactPage() {
  const tbody = $('#table-contact tbody');
  const filter = $('#form-contact-filter');
  if (!tbody || !filter) return;

  const query = new URLSearchParams({ limit: String(CUSTOMER_PAGE) });
  if (filter.elements.q.value.trim()) query.set('q', filter.elements.q.value.trim());
  if (filter.elements.organisation.value) query.set('organisation', filter.elements.organisation.value);

  const page = await api(`/contacts?${query}`);
  const items = page?.items ?? [];

  fillTable(tbody, items.map(contactRow), 7, t('customer.contactsEmpty', 'No contacts.'));
  customerOverflow($('#contact-overflow'), items.length, page?.totalCount ?? 0);
}

/** One contact as a row of its table. */
function contactRow(contact) {
  return el('tr', {},
    el('td', { class: 'text', text: contact.name }),
    el('td', { class: 'text', text: contact.email || '–' }),
    el('td', { class: 'text', text: contact.phone || '–' }),
    el('td', { class: 'text', text: contact.organisationName || '–' }),
    el('td', { text: contact.hasPortalAccount ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no') }),
    el('td', { text: contact.valid ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no') }),
    el('td', { class: 'actions' },
      el('button', { class: 'link', text: t('action.edit', 'edit'), onclick: () => editCustomer('contact', contact) }),
      ...(can('customers:write') ? portalActions(contact) : []),
      deleteButton({
        label: `${t('field.contactId', 'Contact')} "${contact.name}"`,
        path: `/contacts/${contact.id}`,
        message: t('msg.contactDeleted', 'Contact deleted'),
        after: refreshAll,
        ask: () => deleteContact(contact),
      })),
  );
}

/**
 * What a contact's row offers about the portal: access for one without it, and
 * for one with it a new password or taking it away. Only for whoever keeps the
 * customers, who is the one the server lets do it.
 *
 * Taking it away is a plain link, red only in the question it asks: a row's
 * checkbox deletes what the row's first button carrying a deletion deletes, and
 * a second such button in front of the contact's own would have "delete selected"
 * take the portal away instead of deleting the contacts.
 */
function portalActions(contact) {
  if (!contact.hasPortalAccount) {
    return [el('button', {
      class: 'link',
      'data-action': 'portal-give',
      text: t('customer.portalGive', 'give portal access'),
      onclick: () => givePortal(contact, false),
    })];
  }

  return [
    el('button', {
      class: 'link',
      'data-action': 'portal-password',
      text: t('customer.portalPassword', 'new portal password'),
      onclick: () => givePortal(contact, true),
    }),
    el('button', {
      class: 'link',
      'data-action': 'portal-take',
      text: t('customer.portalTake', 'take portal access away'),
      onclick: () => takePortal(contact),
    }),
  ];
}

/**
 * Gives a contact the portal, or the account they have a new password.
 *
 * Either way it is a password for one sign-in: the account has to choose its own
 * before it can do anything, so what was typed here does not stay standing as a
 * password two people know - and a new one ends whatever session the account
 * had open, as a reset of any account's does. The question says so, and names
 * the address the contact signs in with, because that is what they will be told.
 */
async function givePortal(contact, again) {
  const chosen = await confirmDialog({
    title: again
      ? t('customer.portalPasswordTitle', 'New portal password')
      : t('customer.portalGiveTitle', 'Give portal access'),
    text: again
      ? fillIn(t('customer.portalPasswordText',
        'Give {0} a new password to sign in to the customer portal with once. A password of their own is '
        + 'chosen at that sign-in, and any session of theirs ends now.'), [contact.name])
      : fillIn(t('customer.portalGiveText',
        '{0} signs in to the customer portal with the address {1} and this password, once: a password of '
        + 'their own is chosen at that sign-in.'), [contact.name, contact.email || '–']),
    confirmLabel: again
      ? t('customer.portalPasswordConfirm', 'Set the password')
      : t('customer.portalGiveConfirm', 'Give access'),
    danger: false,
    field: {
      id: 'portal-password-field',
      generateID: 'portal-password-generate',
      label: t('field.password', 'Password'),
      hint: fillIn(t('user.resetHint', 'At least {0} characters.'), [MIN_PASSWORD_LENGTH]),
      minLength: MIN_PASSWORD_LENGTH,
      generate: t('action.generate', 'Generate'),
    },
  });

  if (!chosen) return;

  await patch(`/contacts/${contact.id}/portal-access`, { password: chosen },
    again ? t('msg.portalPasswordSet', 'New portal password set') : t('msg.portalGiven', 'Portal access given'),
    refreshAll);
}

/** Takes the portal away from a contact, which erases the account they signed in with. */
async function takePortal(contact) {
  const proceed = await confirmDialog({
    title: t('customer.portalTakeTitle', 'Take portal access away?'),
    text: fillIn(t('customer.portalTakeText',
      '{0} can no longer sign in to the customer portal: the account is erased. The tickets stay theirs.'),
    [contact.name]),
    confirmLabel: t('customer.portalTakeConfirm', 'Take it away'),
  });

  if (!proceed) return;

  mutate(() => api(`/contacts/${contact.id}/portal-access`, { method: 'DELETE' }),
    t('msg.portalTaken', 'Portal access taken away'), refreshAll);
}

/**
 * Deletes a contact, asking first when tickets are for it.
 *
 * The server refuses that deletion rather than performing it, and its refusal
 * carries how many tickets would keep no contact, so the question has the real
 * number in it - as deleting an account that owns tickets does. A contact no
 * ticket is for is asked about once, as any deletion is.
 */
async function deleteContact(contact) {
  if (!await confirmDelete(`${t('field.contactId', 'Contact')} "${contact.name}"`)) return;

  const done = t('msg.contactDeleted', 'Contact deleted');

  try {
    await api(`/contacts/${contact.id}`, { method: 'DELETE' });
    toast(done, 'ok');
    await refreshAll();

    return;
  } catch (err) {
    if (err.status !== 409) {
      toastFailure(err);

      return;
    }

    const proceed = await confirmDialog({
      title: t('confirm.deleteTitle', 'Delete for good?'),
      text: t('customer.contactDeleteConfirm', 'Delete anyway? The tickets for this contact stay, for nobody.'),
      detail: err.message,
      confirmLabel: t('action.delete', 'delete'),
    });

    if (!proceed) return;
  }

  mutate(() => api(`/contacts/${contact.id}?confirm=true`, { method: 'DELETE' }), done, refreshAll);
}

// --------------------------------------------------------- published faq

/**
 * The categories each place reading the published knowledge base has been
 * told, for naming an article's by its place in the tree.
 */
const publishedCategories = { portal: [], public: [] };

/**
 * Fills one of the two places the knowledge base is read from outside the
 * staff - the portal's card, for a customer, and the sign-in screen's panel,
 * for anybody - with its categories and what its search finds. Both ask the
 * same routes, and the server decides what the asker may read: the places
 * differ only in where they are.
 */
async function loadPublishedFAQ(place) {
  publishedCategories[place] = (await api('/portal/faq/categories'))?.items ?? [];

  const form = $(`#form-${place}-faq`);
  if (!beingEdited(form)) {
    const choices = publishedCategories[place]
      .map((category) => ({ value: String(category.id), label: publishedCategoryPath(place, category.id) }))
      .sort((a, b) => a.label.localeCompare(b.label));

    fillSelect(form.elements.category, choices,
      { placeholder: t('faq.everyCategory', 'every category'), labelKey: 'label', valueKey: 'value' });
  }

  await findPublishedFAQ(place);
}

/** A published category named by its place in the tree, as a queue is. */
function publishedCategoryPath(place, id) {
  const byId = new Map(publishedCategories[place].map((category) => [category.id, category]));
  const category = byId.get(id);

  return category ? queuePath(category, byId) : '';
}

/** Draws what the place's search finds, a line each. */
async function findPublishedFAQ(place) {
  const form = $(`#form-${place}-faq`);
  const query = new URLSearchParams();

  if (form.elements.q.value.trim()) query.set('q', form.elements.q.value.trim());
  if (form.elements.category.value) query.set('category', form.elements.category.value);

  const found = (await api(`/portal/faq/articles?${query}`))?.items ?? [];

  $(`#${place}-faq-list`).replaceChildren(...(found.length
    ? found.map((article) => el('li', {},
      el('button', {
        type: 'button', class: 'link', text: article.title, onclick: () => openPublishedFAQ(place, article.id),
      }),
      el('span', { class: 'muted', text: ` · ${publishedCategoryPath(place, article.categoryId)}` })))
    : [el('li', { class: 'muted', text: t('faq.empty', 'Nothing found.') })]));
}

/** Opens an article of the place's list below it. */
async function openPublishedFAQ(place, id) {
  const article = await api(`/portal/faq/articles/${id}`);

  $(`#${place}-faq-article-title`).textContent = article.title;
  $(`#${place}-faq-article-facts`).textContent = [
    publishedCategoryPath(place, article.categoryId),
    article.keywords,
    fillIn(t('faq.writtenOn', 'written {0}'), [fmtMoment(article.updatedAt)]),
  ].filter(Boolean).join(' · ');
  $(`#${place}-faq-article-body`).textContent = article.body;
  $(`#${place}-faq-article`).hidden = false;
}

/** Puts the place's article away, and what it said with it. */
function closePublishedFAQ(place) {
  $(`#${place}-faq-article`).hidden = true;
  $(`#${place}-faq-article-title`).textContent = '';
  $(`#${place}-faq-article-facts`).textContent = '';
  $(`#${place}-faq-article-body`).textContent = '';
}

/**
 * Offers the knowledge base on the sign-in screen where something is public,
 * and says nothing where nothing is - or where the installation does not answer,
 * which is not a reason to hold up signing in.
 */
async function offerPublicFAQ() {
  let found = [];

  try {
    found = (await api('/portal/faq/articles'))?.items ?? [];
  } catch {
    found = [];
  }

  $('#login-faq-offer').hidden = found.length === 0;
}

/** Shows the sign-in screen's knowledge base in place of its form. */
async function openPublicFAQ() {
  $('#form-login').hidden = true;
  $('#login-faq-offer').hidden = true;
  $('#public-faq').hidden = false;

  try {
    await loadPublishedFAQ('public');
  } catch (err) {
    toastFailure(err);
  }
}

/** Puts the sign-in screen's knowledge base away, and the form back. */
function closePublicFAQ() {
  $('#public-faq').hidden = true;
  closePublishedFAQ('public');
  $('#form-public-faq').reset();
  $('#public-faq-list').replaceChildren();
  $('#form-login').hidden = false;
  offerPublicFAQ();
}

/** Wires the two places the knowledge base is read from outside the staff. */
function wirePublishedFAQ() {
  for (const place of ['portal', 'public']) {
    const form = $(`#form-${place}-faq`);
    let typing;

    form.addEventListener('submit', (e) => { e.preventDefault(); findPublishedFAQ(place).catch((err) => toastFailure(err)); });
    form.elements.category.addEventListener('change', () => findPublishedFAQ(place).catch((err) => toastFailure(err)));
    form.elements.q.addEventListener('input', () => {
      clearTimeout(typing);
      typing = setTimeout(() => findPublishedFAQ(place).catch((err) => toastFailure(err)), 300);
    });
    $(`#${place}-faq-article-close`).addEventListener('click', () => closePublishedFAQ(place));
  }

  $('#login-faq-open').addEventListener('click', openPublicFAQ);
  $('#public-faq-close').addEventListener('click', closePublicFAQ);
}

/** The classes of the general catalog that ship, in the reader's words; one an installation made is shown as it was typed. */
const CATALOG_CLASS_TITLES = {
  'ITSM::ConfigItem::Class': 'Configuration item classes',
  'ITSM::ConfigItem::DeploymentState': 'Deployment states',
  'ITSM::Core::IncidentState': 'Incident states',
  'ITSM::ConfigItem::YesNo': 'Yes or no',
  'ITSM::ConfigItem::Computer::Type': 'Computer types',
  'ITSM::ConfigItem::Hardware::Type': 'Hardware types',
  'ITSM::ConfigItem::Location::Type': 'Location types',
  'ITSM::ConfigItem::Network::Type': 'Network types',
  'ITSM::ConfigItem::Software::Type': 'Software types',
  'ITSM::ConfigItem::Software::LicenceType': 'Licence types',
  'ITSM::ChangeManagement::Category': 'Change categories',
  'ITSM::ChangeManagement::Impact': 'Change impacts',
  'ITSM::ChangeManagement::Priority': 'Change priorities',
  'ITSM::ChangeManagement::Change::State': 'Change states',
  'ITSM::ChangeManagement::WorkOrder::State': 'Work order states',
  'ITSM::ChangeManagement::WorkOrder::Type': 'Work order types',
};

/** The values of the general catalog that ship, by the identifier they are kept under. */
const SHIPPED_CATALOG_TITLES = {
  operational: 'operational',
  warning: 'warning',
  incident: 'incident',
  computer: 'computer',
  hardware: 'hardware',
  location: 'location',
  building: 'building',
  room: 'room',
  network: 'network',
  software: 'software',
  expired: 'expired',
  inactive: 'inactive',
  maintenance: 'maintenance',
  pilot: 'pilot',
  planned: 'planned',
  production: 'production',
  repair: 'repair',
  retired: 'retired',
  review: 'review',
  'test-qa': 'test/QA',
  yes: 'yes',
  no: 'no',
  desktop: 'desktop',
  phone: 'phone',
  server: 'server',
  other: 'other',
  laptop: 'laptop',
  monitor: 'monitor',
  printer: 'printer',
  switch: 'switch',
  router: 'router',
  'wlan-access-point': 'WLAN access point',
  'security-device': 'security device',
  'backup-device': 'backup device',
  mouse: 'mouse',
  keyboard: 'keyboard',
  camera: 'camera',
  beamer: 'projector',
  modem: 'modem',
  'usb-device': 'USB device',
  'docking-station': 'docking station',
  scanner: 'scanner',
  office: 'office',
  floor: 'floor',
  rack: 'rack',
  workplace: 'workplace',
  outlet: 'outlet',
  'it-facility': 'IT facility',
  lan: 'LAN',
  wlan: 'WLAN',
  telco: 'telephony',
  gsm: 'GSM',
  'client-application': 'client application',
  middleware: 'middleware',
  'server-application': 'server application',
  'client-os': 'client operating system',
  'server-os': 'server operating system',
  'admin-tool': 'administration tool',
  'user-tool': 'user tool',
  embedded: 'embedded',
  'single-licence': 'single licence',
  'per-user': 'per user',
  'concurrent-users': 'concurrent users',
  'per-processor': 'per processor',
  'per-server': 'per server',
  'per-node': 'per node',
  'volume-licence': 'volume licence',
  'enterprise-licence': 'enterprise licence',
  'developer-licence': 'developer licence',
  demo: 'demo',
  'time-restricted': 'time restricted',
  freeware: 'freeware',
  unlimited: 'unlimited',
  'open-source': 'open source',
  '1-very-low': '1 very low',
  '2-low': '2 low',
  '3-normal': '3 normal',
  '4-high': '4 high',
  '5-very-high': '5 very high',
  'requested': 'requested',
  'pending-approval': 'pending approval',
  'rejected': 'rejected',
  'approved': 'approved',
  'in-progress': 'in progress',
  'pending-pir': 'pending PIR',
  'successful': 'successful',
  'failed': 'failed',
  'canceled': 'canceled',
  'retracted': 'retracted',
  'created': 'created',
  'accepted': 'accepted',
  'ready': 'ready',
  'closed': 'closed',
  'approval': 'approval',
  'workorder': 'work order',
  'backout': 'backout',
  'decision': 'decision',
  'pir': 'PIR',
};

/** What a deployment or an incident state means to the code, in the reader's words. */
const FUNCTIONALITY_TITLES = {
  preproductive: 'before use',
  productive: 'in use',
  postproductive: 'after use',
  operational: 'working',
  warning: 'warned of',
  incident: 'broken',
};

/** A class of the general catalog in the reader's words, or as it was typed. */
function catalogClassTitle(name) {
  return CATALOG_CLASS_TITLES[name] ? t(`catalogclass.${name}`, CATALOG_CLASS_TITLES[name]) : name;
}

/** A value of the general catalog in the reader's words where it shipped, or as it was typed. */
function catalogValueTitle(name) {
  return SHIPPED_CATALOG_TITLES[name] ? t(`catalogvalue.${name}`, SHIPPED_CATALOG_TITLES[name]) : name;
}

/** What a value means to the code, in the reader's words. */
function functionalityTitle(name) {
  return name ? t(`functionality.${name}`, FUNCTIONALITY_TITLES[name] ?? name) : '–';
}

/** The classes of the general catalog, with what a value of each may mean. */
let generalCatalogClasses = [];

/**
 * Fills the general catalog's card for whoever may change it: the classes to
 * choose a list from, and the values of the one chosen.
 */
async function loadGeneralCatalog() {
  if (!can('catalog:write')) return;

  generalCatalogClasses = (await api('/general-catalog/classes'))?.items ?? [];

  const chooser = $('#form-catalog-class');
  if (!beingEdited(chooser)) {
    fillSelect(chooser.elements.class, generalCatalogClasses
      .map((entry) => ({ value: entry.name, label: catalogClassTitle(entry.name) }))
      .sort((a, b) => a.label.localeCompare(b.label)), { labelKey: 'label', valueKey: 'value' });
  }

  $('#catalog-classes').replaceChildren(...generalCatalogClasses.map((entry) => el('option', { value: entry.name })));

  const form = $('#form-catalog');
  if (!beingEdited(form) && !form.elements.id.value) {
    form.elements.class.value = chooser.elements.class.value;
    offerFunctionalities(form);
  }

  await loadGeneralCatalogItems();
}

/** Draws the values of the chosen class. */
async function loadGeneralCatalogItems() {
  const chosen = $('#form-catalog-class').elements.class.value;
  const items = chosen ? (await api(`/general-catalog?class=${encodeURIComponent(chosen)}`))?.items ?? [] : [];

  fillTable($('#table-catalog tbody'), items.map((item) => el('tr', {},
    el('td', { class: 'text', text: catalogValueTitle(item.name) }),
    el('td', { text: functionalityTitle(item.functionality) }),
    el('td', { class: 'text', text: item.comment || '–' }),
    el('td', { text: item.valid ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no') }),
    el('td', { class: 'actions' },
      el('button', { class: 'link', text: t('action.edit', 'edit'), onclick: () => editGeneralCatalogItem(item) })),
  )), 5, t('gencatalog.empty', 'No values.'));
}

/**
 * Offers the functionalities of the class the form names, and puts the field
 * away for a class whose values mean nothing to the code - where the server
 * refuses one, so a field left showing would be a question with no right answer.
 */
function offerFunctionalities(form, chosen = '') {
  const offered = generalCatalogClasses.find((entry) => entry.name === form.elements.class.value.trim())?.functionalities ?? [];

  fillSelect(form.elements.functionality, offered.map((name) => ({ value: name, label: functionalityTitle(name) })),
    { labelKey: 'label', valueKey: 'value' });
  form.elements.functionality.value = chosen || offered[0] || '';

  $('#catalog-functionality').hidden = offered.length === 0;
  form.elements.functionality.required = offered.length > 0;
}

/** Opens a value in the form, to correct it; its class stays the one it is in. */
function editGeneralCatalogItem(item) {
  const form = $('#form-catalog');

  form.elements.id.value = String(item.id);
  form.elements.class.value = item.class;
  form.elements.class.readOnly = true;
  form.elements.name.value = item.name;
  form.elements.comment.value = item.comment;
  form.elements.valid.checked = item.valid;
  offerFunctionalities(form, item.functionality);

  // Drawn rather than declared with a key, because it carries the value's name;
  // see editRole for why the key has to come off.
  const heading = $('#catalog-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('catalogFormTitle', () => {
    heading.textContent = fillIn(t('gencatalog.edit', 'Edit the value {0}'), [catalogValueTitle(item.name)]);
  });

  swapTheLabel($('#catalog-submit'), 'action.save', 'Save');
  $('#catalog-cancel').hidden = false;
  form.elements.name.focus();
}

/** Puts the form back to adding a value to the class chosen above it. */
function resetGeneralCatalogForm() {
  const form = $('#form-catalog');

  form.reset();
  form.elements.id.value = '';
  form.elements.class.readOnly = false;
  form.elements.class.value = $('#form-catalog-class').elements.class.value;
  offerFunctionalities(form);

  stopRedrawing('catalogFormTitle');
  swapTheLabel($('#catalog-form-title'), 'gencatalog.create', 'Add a value');
  swapTheLabel($('#catalog-submit'), 'action.create', 'Create');
  $('#catalog-cancel').hidden = true;
}

/** Sends the form: a new value, or the correction of the one it holds. */
function saveGeneralCatalogItem(form) {
  const id = form.elements.id.value;
  const offered = !$('#catalog-functionality').hidden;
  const sent = {
    name: form.elements.name.value.trim(),
    comment: form.elements.comment.value.trim(),
    valid: form.elements.valid.checked,
    functionality: offered ? form.elements.functionality.value : '',
    ...(id ? {} : { class: form.elements.class.value.trim() }),
  };
  const body = JSON.stringify(sent);

  mutate(
    () => (id ? api(`/general-catalog/${id}`, { method: 'PUT', body }) : api('/general-catalog', { method: 'POST', body })),
    id ? t('msg.catalogSaved', 'Value saved') : t('msg.catalogCreated', 'Value added'),
    async (item) => {
      const chooser = $('#form-catalog-class');
      resetGeneralCatalogForm();
      await loadGeneralCatalog();

      if (item?.class && chooser.elements.class.value !== item.class) {
        chooser.elements.class.value = item.class;
        await loadGeneralCatalogItems();
      }
    },
  );
}

/** Wires the general catalog's card. */
function wireGeneralCatalog() {
  $('#form-catalog-class').addEventListener('change', () => {
    resetGeneralCatalogForm();
    loadGeneralCatalogItems().catch((err) => toastFailure(err));
  });
  $('#form-catalog-class').addEventListener('submit', (e) => e.preventDefault());
  $('#form-catalog').addEventListener('submit', (e) => { e.preventDefault(); saveGeneralCatalogItem(e.target); });
  $('#form-catalog').elements.class.addEventListener('input', (e) => offerFunctionalities(e.target.form));
  $('#catalog-cancel').addEventListener('click', resetGeneralCatalogForm);
}

// ------------------------------------------------------- class definitions

/** The classes of configuration items, with the version each definition stands at. */
let ciClasses = [];

/**
 * Fills the class definitions' card for whoever may write one: the classes to
 * choose from, keeping the one chosen, and the versions of its definition.
 */
async function loadClassDefinitions() {
  if (!can('catalog:write')) return;

  ciClasses = (await api('/cmdb/classes'))?.items ?? [];

  const form = $('#form-classdef');
  const chosen = form.elements.classId.value;

  fillSelect(form.elements.classId, ciClasses
    .map((entry) => ({ value: String(entry.id), label: catalogValueTitle(entry.name) }))
    .sort((a, b) => a.label.localeCompare(b.label)), { labelKey: 'label', valueKey: 'value' });

  if (ciClasses.some((entry) => String(entry.id) === chosen)) form.elements.classId.value = chosen;

  await loadClassDefinitionVersions();
}

/**
 * Draws the versions of the chosen class's definition, and puts the standing
 * one in the box - unless somebody is writing in it, whose words a reload must
 * not take.
 */
async function loadClassDefinitionVersions() {
  const form = $('#form-classdef');
  const id = form.elements.classId.value;
  const versions = id ? (await api(`/cmdb/classes/${id}/definitions`))?.items ?? [] : [];

  if (!beingEdited(form)) {
    form.elements.attributes.value = versions.length ? JSON.stringify(versions[0].attributes, null, 2) : '';
  }

  redrawable('classDefinitionVersion', () => {
    $('#classdef-version').textContent = versions.length
      ? fillIn(t('classdef.standing', 'Version {0} stands now.'), [versions[0].version])
      : t('classdef.none', 'This class has no definition yet.');
  });

  fillTable($('#table-classdef-versions tbody'), versions.map((version) => el('tr', {},
    el('td', { class: 'num', text: String(version.version) }),
    el('td', { text: fmtMoment(version.createdAt) }),
    el('td', { class: 'num', text: String(countClassAttributes(version.attributes)) }),
  )), 3, t('classdef.noVersions', 'No version yet.'));
}

/** How many attributes a definition holds, those below others included. */
function countClassAttributes(attributes) {
  return (attributes ?? []).reduce((count, attribute) => count + 1 + countClassAttributes(attribute.sub), 0);
}

/**
 * Sends the box as the class's next version. What is not JSON, or not a list,
 * is said here; whether it is a definition an item can be written under is the
 * server's to answer.
 */
function saveClassDefinition(form) {
  let attributes;

  try {
    attributes = JSON.parse(form.elements.attributes.value);
  } catch {
    toast(t('classdef.notJSON', 'The definition is not JSON.'), 'error');

    return;
  }

  if (!Array.isArray(attributes)) {
    toast(t('classdef.notAList', 'The definition is a list of attributes.'), 'error');

    return;
  }

  const id = form.elements.classId.value;

  saveForm(form,
    () => api(`/cmdb/classes/${id}/definitions`, { method: 'POST', body: JSON.stringify({ attributes }) }),
    t('msg.classDefinitionSaved', 'Kept as a new version'),
    loadClassDefinitions);
}

/** Wires the class definitions' card. */
function wireClassDefinitions() {
  const form = $('#form-classdef');

  form.elements.classId.addEventListener('change', () => {
    // Another class is another document: what was being written was the last
    // one's, and stays only as long as nobody chooses away from it.
    delete form.dataset.editing;
    loadClassDefinitionVersions().catch((err) => toastFailure(err));
  });
  form.addEventListener('submit', (e) => { e.preventDefault(); saveClassDefinition(e.target); });
}

// ------------------------------------------------------- configuration items

/**
 * What the items' screen reads besides the items: the classes, the states an
 * item can be in, the lists a definition picks from, and each class's
 * definitions by version. Kept between loads, so that a form drawn from a
 * definition does not ask for it again with every keystroke.
 */
const assetLists = {
  classes: [],
  deployment: [],
  incident: [],
  catalogs: new Map(),
  definitions: new Map(),
};

/** Where the page of items stands. */
const itemPaging = { page: 1, limit: 50, total: 0 };

/**
 * The item open below the list and its versions, or null. version is the one on
 * screen, which is the newest unless an older one was chosen to be read.
 */
let itemShown = null;

/** The kinds of record an attribute names by id, searched rather than listed. */
const REFERENCE_KINDS = {
  Contact: SEARCHED_KINDS.contact,
  Organisation: SEARCHED_KINDS.organisation,
  CIClassReference: {
    path: '/cmdb/items',
    describe: (item) => `${item.number} ${item.name}`,
  },
};

/** The classes as choices, by their names in the reader's words. */
function classChoices(classes) {
  return classes
    .map((entry) => ({ value: String(entry.id), label: catalogValueTitle(entry.name) }))
    .sort((a, b) => a.label.localeCompare(b.label));
}

/**
 * Fills the items' screen for whoever may read them: the classes to narrow the
 * list by, the page of items, and the open one, read again - unless somebody is
 * writing in it, whose words a reload must not take.
 */
async function loadConfigItems() {
  if (!can('assets:read')) return;

  const [classes, deployment, incident] = await Promise.all([
    api('/cmdb/classes'),
    api('/general-catalog?class=ITSM::ConfigItem::DeploymentState'),
    api('/general-catalog?class=ITSM::Core::IncidentState'),
  ]);

  assetLists.classes = classes?.items ?? [];
  assetLists.deployment = deployment?.items ?? [];
  assetLists.incident = incident?.items ?? [];
  assetLists.catalogs.clear();
  assetLists.definitions.clear();

  fillSelect($('#form-configitem-filter').elements.class, classChoices(assetLists.classes),
    { placeholder: t('configitem.everyClass', 'every class'), labelKey: 'label', valueKey: 'value' });

  await loadConfigItemPage();

  if (itemShown && !beingEdited($('#form-configitem'))) await openConfigItem(itemShown.item.id);
}

/** Reads the page of items the filter asks for, and draws it. */
async function loadConfigItemPage() {
  const filter = $('#form-configitem-filter');
  const query = new URLSearchParams({ limit: String(itemPaging.limit), page: String(itemPaging.page) });

  if (filter.elements.class.value) query.set('class', filter.elements.class.value);
  if (filter.elements.q.value.trim()) query.set('q', filter.elements.q.value.trim());

  const page = await api(`/cmdb/items?${query}`);
  itemPaging.total = page?.totalCount ?? 0;

  fillTable($('#table-configitem tbody'), (page?.items ?? []).map(configItemRow), 7,
    t('configitem.empty', 'No items.'));

  const { total, limit } = itemPaging;
  const from = total === 0 ? 0 : (itemPaging.page - 1) * limit + 1;
  const to = Math.min(itemPaging.page * limit, total);

  $('#configitems-range').textContent = fillIn(t('ticket.range', '{0}–{1} of {2}'), [from, to, total]);
  $('#configitems-previous').disabled = itemPaging.page <= 1;
  $('#configitems-next').disabled = to >= total;
}

/** A catalog value by its id among those read, in the reader's words. */
function catalogEntryTitle(entries, id) {
  const entry = entries.find((candidate) => candidate.id === id);

  return entry ? catalogValueTitle(entry.name) : `#${id}`;
}

/** One item as a row of the list. */
function configItemRow(item) {
  const changed = item.changedByName
    ? fillIn(t('configitem.changedBy', '{0}, by {1}'), [fmtMoment(item.changedAt), item.changedByName])
    : fmtMoment(item.changedAt);

  return el('tr', {},
    el('td', { class: 'text' },
      el('button', { class: 'link', text: item.number, onclick: () => openConfigItem(item.id) })),
    el('td', { class: 'text', text: item.name }),
    el('td', { class: 'text', text: catalogEntryTitle(assetLists.classes, item.classId) }),
    el('td', { class: 'text', text: catalogEntryTitle(assetLists.deployment, item.deploymentStateId) }),
    el('td', { class: 'text', text: catalogEntryTitle(assetLists.incident, item.incidentStateId) }),
    el('td', { class: 'num', text: String(item.versionCount) }),
    el('td', { text: changed }),
  );
}

/** A class's definitions, the newest first, read once a load. */
async function classDefinitions(classId) {
  if (!assetLists.definitions.has(classId)) {
    assetLists.definitions.set(classId, (await api(`/cmdb/classes/${classId}/definitions`))?.items ?? []);
  }

  return assetLists.definitions.get(classId);
}

/** The values of a list of the general catalog, read once a load. */
async function catalogList(catalogClass) {
  if (!assetLists.catalogs.has(catalogClass)) {
    const values = (await api(`/general-catalog?class=${encodeURIComponent(catalogClass)}`))?.items ?? [];
    assetLists.catalogs.set(catalogClass, values);
  }

  return assetLists.catalogs.get(catalogClass);
}

/**
 * The lists a definition picks from, read before its form is drawn, so that
 * drawing it waits on nothing.
 */
async function readListsOf(attributes) {
  const wanted = new Set();
  const walk = (list) => {
    for (const attribute of list) {
      if (attribute.type === 'GeneralCatalog' && attribute.catalog) wanted.add(attribute.catalog);
      walk(attribute.sub ?? []);
    }
  };

  walk(attributes);
  await Promise.all([...wanted].map(catalogList));
}

/**
 * What the records a version names by id are called, for the choices that keep
 * them: a contact, an organisation or another item is searched for, never
 * listed whole, so the one already chosen has to be read on its own.
 */
async function referenceLabels(attributes, data) {
  const asked = [];
  const walk = (list, values) => {
    for (const attribute of list) {
      for (const occurrence of values?.[attribute.key] ?? []) {
        const kind = REFERENCE_KINDS[attribute.type];
        if (kind && occurrence.value) asked.push({ kind, id: occurrence.value });
        walk(attribute.sub ?? [], occurrence.sub);
      }
    }
  };

  walk(attributes, data);

  const labels = new Map();

  await Promise.all(asked.slice(0, 50).map(async ({ kind, id }) => {
    try {
      labels.set(`${kind.path}/${id}`, kind.describe(await api(`${kind.path}/${id}`)));
    } catch {
      labels.set(`${kind.path}/${id}`, fillIn(t('configitem.gone', '#{0}, no longer there'), [id]));
    }
  }));

  return labels;
}

/** Opens the form for a new item, of the first class still offered. */
async function startConfigItem() {
  const form = $('#form-configitem');

  resetConfigItemForm();
  $('#configitem-detail').hidden = false;

  const offered = assetLists.classes.filter((entry) => entry.valid && entry.version > 0);
  fillSelect(form.elements.classId, classChoices(offered), { labelKey: 'label', valueKey: 'value' });
  form.elements.classId.disabled = false;

  await drawNewItemFields();
  $('#configitem-detail').scrollIntoView({ block: 'start' });
  form.elements.name.focus();
}

/** Draws the fields of a new item of the class chosen, from its definition as it stands. */
async function drawNewItemFields() {
  const form = $('#form-configitem');
  const classId = Number(form.elements.classId.value);
  const definition = classId ? (await classDefinitions(classId))[0] : null;

  fillStateChoices(form, null);
  await drawItemAttributes(definition?.attributes ?? [], {}, false);
}

/** Fills the two states, offering what is still offered and keeping what a version names. */
function fillStateChoices(form, version) {
  const choices = (entries, kept) => entries
    .filter((entry) => entry.valid || entry.id === kept)
    .map((entry) => ({ value: String(entry.id), label: catalogValueTitle(entry.name) }));

  fillSelect(form.elements.deploymentStateId, choices(assetLists.deployment, version?.deploymentStateId),
    { placeholder: t('configitem.choose', 'choose…'), labelKey: 'label', valueKey: 'value' });
  fillSelect(form.elements.incidentStateId, choices(assetLists.incident, version?.incidentStateId),
    { placeholder: t('configitem.choose', 'choose…'), labelKey: 'label', valueKey: 'value' });

  form.elements.deploymentStateId.value = version ? String(version.deploymentStateId) : '';
  form.elements.incidentStateId.value = version ? String(version.incidentStateId) : '';
}

/**
 * Opens an item: the form holding what its newest version says, under its
 * class's definition as it stands now - which is what the next version is
 * written under - and its versions below. Read only for whoever may not write
 * items.
 */
async function openConfigItem(id) {
  let item;
  let versions;

  try {
    [item, versions] = await Promise.all([api(`/cmdb/items/${id}`), api(`/cmdb/items/${id}/versions`)]);
  } catch (err) {
    toastFailure(err);
    closeConfigItem();
    await loadConfigItemPage();

    return;
  }

  itemShown = { item, versions: versions?.items ?? [], version: null };

  const form = $('#form-configitem');
  const definition = (await classDefinitions(item.classId))[0];

  resetConfigItemForm({ keepShown: true });
  $('#configitem-detail').hidden = false;

  form.elements.id.value = String(item.id);
  fillSelect(form.elements.classId, classChoices(assetLists.classes), { labelKey: 'label', valueKey: 'value' });
  form.elements.classId.value = String(item.classId);
  form.elements.classId.disabled = true;
  form.elements.name.value = item.version.name;
  fillStateChoices(form, item.version);

  // Drawn rather than declared with a key, because it carries the item's
  // number; see editRole for why the key has to come off.
  const heading = $('#configitem-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('configItemFormTitle', () => {
    heading.textContent = fillIn(t('configitem.edit', 'Item {0}'), [item.number]);
  });

  swapTheLabel($('#configitem-submit'), 'configitem.saveVersion', 'Keep as a new version');
  $('#configitem-delete').hidden = !can('assets:write');

  await drawItemAttributes(definition?.attributes ?? [], item.version.data, !can('assets:write'));
  drawItemVersions();

  const linked = $('#configitem-links');
  offerLinkKinds($('#form-configitem-link'));
  linked.hidden = false;
  await drawLinks(linked, 'configitem', item.id, can('assets:write'));
}

/**
 * Shows an older version as it was written - under the definition it was
 * written with, and read only, because a version is never changed. The newest
 * is the form again.
 */
async function showItemVersion(version) {
  if (!itemShown) return;

  if (version.id === itemShown.versions[0]?.id) {
    await openConfigItem(itemShown.item.id);

    return;
  }

  const definitions = await classDefinitions(itemShown.item.classId);
  const definition = definitions.find((candidate) => candidate.id === version.definitionId);
  const form = $('#form-configitem');

  itemShown.version = version;
  form.elements.name.value = version.name;
  fillStateChoices(form, version);
  $('#configitem-fields').disabled = true;
  $('#configitem-submit').hidden = true;

  const place = itemShown.versions.length - itemShown.versions.indexOf(version);
  const note = $('#configitem-version-shown');

  note.replaceChildren(
    el('span', { text: fillIn(t('configitem.olderShown', 'Version {0} as it was written; a version is never changed.'),
      [place]) }),
    ' ',
    el('button', {
      type: 'button',
      class: 'link',
      text: t('configitem.backToNewest', 'back to the newest'),
      onclick: () => openConfigItem(itemShown.item.id),
    }),
  );
  note.hidden = false;

  await drawItemAttributes(definition?.attributes ?? [], version.data, true);
  drawItemVersions();
}

/** Draws the open item's versions, the newest first, the one on screen marked. */
function drawItemVersions() {
  const shown = itemShown.version ?? itemShown.versions[0];
  const count = itemShown.versions.length;

  fillTable($('#table-configitem-versions tbody'), itemShown.versions.map((version, i) => el('tr',
    { 'aria-current': version === shown ? 'true' : null },
    el('td', { class: 'num', text: String(count - i) }),
    el('td', { text: fmtMoment(version.createdAt) }),
    el('td', { class: 'text', text: version.createdByName || (version.createdBy ? `#${version.createdBy}` : '–') }),
    el('td', { class: 'text', text: version.name }),
    el('td', { class: 'actions' }, version === shown ? null : el('button', {
      type: 'button',
      class: 'link',
      text: t('configitem.read', 'read'),
      onclick: () => showItemVersion(version),
    })),
  )), 5);
}

/**
 * Draws a definition's attributes into the form, holding what data says: each
 * as often as it occurs there, or as often as the definition starts it with,
 * and never fewer times than it has to occur.
 */
async function drawItemAttributes(attributes, data, readOnly) {
  await readListsOf(attributes);
  const labels = await referenceLabels(attributes, data);

  const box = $('#configitem-attributes');
  box.replaceChildren(...attributes.map((attribute) => attributeBlock(attribute, data?.[attribute.key], readOnly,
    labels)));
  enhanceDateFields(box);

  for (const native of $$('input[type="date"][data-value]', box)) setDateField(native, native.dataset.value);

  $('#configitem-fields').disabled = readOnly;
  $('#configitem-submit').hidden = readOnly;
}

/** One attribute: its occurrences, and the way to add one while it may occur again. */
function attributeBlock(attribute, occurrences, readOnly, labels) {
  const section = attribute.type === 'Section';
  const block = el(section ? 'fieldset' : 'div', {
    class: section ? 'ci-section' : 'ci-attribute',
    'data-key': attribute.key,
  });

  block.append(el(section ? 'legend' : 'span', { class: section ? '' : 'ci-name', text: attribute.name }));

  const list = el('div', { class: 'ci-occurrences' });
  const given = occurrences ?? Array.from({ length: Math.max(attribute.countDefault, attribute.countMin) },
    () => ({}));

  for (const occurrence of given) list.append(occurrenceRow(attribute, occurrence, readOnly, labels));
  block.append(list);

  if (!readOnly && attribute.countMax > 1) {
    const add = el('button', {
      type: 'button',
      class: 'link',
      'data-ci': 'add',
      text: fillIn(t('configitem.addAnother', 'add another: {0}'), [attribute.name]),
      onclick: () => {
        list.append(occurrenceRow(attribute, {}, false, labels));
        enhanceDateFields(list);
        countOccurrences(block, attribute);
      },
    });

    block.append(add);
  }

  countOccurrences(block, attribute);

  return block;
}

/** Offers adding and removing an occurrence only while the definition allows it. */
function countOccurrences(block, attribute) {
  const rows = $$(':scope > .ci-occurrences > .ci-occurrence', block);
  const add = block.querySelector(':scope > [data-ci="add"]');

  if (add) add.hidden = rows.length >= attribute.countMax;

  for (const row of rows) {
    const remove = row.querySelector(':scope > [data-ci="remove"]');
    if (remove) remove.hidden = rows.length <= attribute.countMin;
  }
}

/** One occurrence of an attribute: its value, and the attributes beneath it. */
function occurrenceRow(attribute, occurrence, readOnly, labels) {
  const row = el('div', { class: 'ci-occurrence' });

  if (attribute.type !== 'Section') row.append(valueField(attribute, occurrence.value ?? '', labels));

  if (!readOnly && attribute.countMax > attribute.countMin) {
    row.append(el('button', {
      type: 'button',
      class: 'link',
      'data-ci': 'remove',
      text: t('configitem.remove', 'remove'),
      onclick: () => {
        const block = row.closest('[data-key]');
        row.remove();
        countOccurrences(block, attribute);
      },
    }));
  }

  if (attribute.sub?.length) {
    const sub = el('div', { class: 'ci-sub' });
    sub.append(...attribute.sub.map((child) => attributeBlock(child, occurrence.sub?.[child.key], readOnly, labels)));
    row.append(sub);
  }

  return row;
}

/** The control a value of an attribute's type is written in, named by the attribute. */
function valueField(attribute, value, labels) {
  const named = { 'aria-label': attribute.name, class: 'ci-value' };
  const required = attribute.required ? { required: true } : {};

  switch (attribute.type) {
    case 'TextArea':
      return el('textarea', { ...named, ...required, rows: '3', maxlength: '50000', text: value });
    case 'Date':
      return el('input', { ...named, ...required, type: 'date', 'data-value': value });
    case 'DateTime': {
      const at = value ? dayAndTimeIn(value, readerZone()) : null;

      return el('input', { ...named, ...required, type: 'datetime-local', value: at ? `${at.day}T${at.time}` : '' });
    }
    case 'GeneralCatalog': {
      const select = el('select', { ...named, ...required });
      const entries = (assetLists.catalogs.get(attribute.catalog) ?? [])
        .filter((entry) => entry.valid || String(entry.id) === value);

      select.append(el('option', { value: '', text: '–' }),
        ...entries.map((entry) => el('option', { value: String(entry.id), text: catalogValueTitle(entry.name) })));
      select.value = value;

      return select;
    }
    case 'Contact':
    case 'Organisation':
    case 'CIClassReference':
      return referenceField(attribute, value, labels);
    default:
      return el('input', {
        ...named, ...required, type: 'text', maxlength: String(attribute.maxLength || 50000), value,
      });
  }
}

/**
 * A value naming a contact, an organisation or another item: a search and the
 * choice it fills, which always keeps the record already named. A reference to
 * an item is searched among the classes the attribute names.
 */
function referenceField(attribute, value, labels) {
  const kind = REFERENCE_KINDS[attribute.type];
  const select = el('select', { 'aria-label': attribute.name, class: 'ci-value' });
  const search = el('input', {
    type: 'search',
    'aria-label': fillIn(t('configitem.searchFor', 'search for {0}'), [attribute.name]),
    placeholder: t('configitem.findHint', 'name or number'),
    maxlength: '255',
  });

  const offer = (found) => {
    const kept = select.value;
    const options = [el('option', { value: '', text: '–' })];

    if (kept && !found.some((record) => String(record.id) === kept)) {
      options.push(el('option', { value: kept, text: select.selectedOptions[0]?.textContent ?? `#${kept}` }));
    }

    options.push(...found.map((record) => el('option', { value: String(record.id), text: kind.describe(record) })));
    select.replaceChildren(...options);
    select.value = kept;
  };

  select.append(el('option', { value: '', text: '–' }));

  if (value) {
    select.append(el('option', { value, text: labels.get(`${kind.path}/${value}`) ?? `#${value}` }));
    select.value = value;
  }

  let pending;

  search.addEventListener('input', () => {
    clearTimeout(pending);
    pending = setTimeout(async () => {
      const query = new URLSearchParams({ q: search.value.trim(), limit: String(SEARCH_OFFERS) });

      if (attribute.type === 'CIClassReference') {
        const classes = assetLists.classes.filter((entry) => (attribute.classes ?? []).includes(entry.name));
        query.set('class', classes.map((entry) => entry.id).join(',') || '0');
      }

      try {
        offer((await api(`${kind.path}?${query}`))?.items ?? []);
      } catch (err) {
        toastFailure(err);
      }
    }, 250);
  });

  return el('span', { class: 'ci-choice' }, search, select);
}

/** What the form's attributes hold, as the API takes a version's values. */
function attributeData(box) {
  const data = {};

  for (const block of $$(':scope > [data-key]', box)) {
    const occurrences = $$(':scope > .ci-occurrences > .ci-occurrence', block).map((row) => {
      const occurrence = {};
      // A date's named box sits inside the wrapper enhanceDateFields puts around it.
      const field = row.querySelector(':scope > .ci-value, :scope > .ci-choice > .ci-value, '
        + ':scope > .date-wrap > .ci-value');
      const value = field ? fieldValue(field) : '';

      if (value) occurrence.value = value;

      const sub = row.querySelector(':scope > .ci-sub');
      if (sub) {
        const below = attributeData(sub);
        if (Object.keys(below).length) occurrence.sub = below;
      }

      return occurrence;
    });

    if (occurrences.length) data[block.dataset.key] = occurrences;
  }

  return data;
}

/** A value as the API takes it: a moment in the reader's zone as the moment it names. */
function fieldValue(field) {
  if (field.type === 'datetime-local') {
    const [day, time] = field.value.split('T');

    return day ? momentIn(day, time, readerZone()) : '';
  }

  return field.value;
}

/** Writes the item: a new one with its first version, or the open one's next version. */
async function saveConfigItem(form) {
  const id = form.elements.id.value;
  const body = {
    name: form.elements.name.value,
    deploymentStateId: Number(form.elements.deploymentStateId.value) || 0,
    incidentStateId: Number(form.elements.incidentStateId.value) || 0,
    data: attributeData($('#configitem-attributes')),
  };

  if (!id) body.classId = Number(form.elements.classId.value) || 0;

  await mutate(() => api(id ? `/cmdb/items/${id}/versions` : '/cmdb/items',
    { method: 'POST', body: JSON.stringify(body) }),
    id ? t('msg.versionKept', 'Kept as a new version') : t('msg.itemAdded', 'Item added'),
    async (item) => {
      delete form.dataset.editing;
      await loadConfigItemPage();
      await openConfigItem(item.id);
    });
}

/** Deletes the open item with every version of it, once asked. */
async function deleteConfigItem() {
  if (!itemShown) return;

  const { item } = itemShown;
  const sure = await confirmDialog({
    title: t('configitem.deleteTitle', 'Delete the item?'),
    text: fillIn(t('configitem.deleteText', '{0} {1} goes with every version of it.'), [item.number, item.name]),
    confirmLabel: t('configitem.deleteConfirm', 'Delete it'),
  });

  if (!sure) return;

  await mutate(() => api(`/cmdb/items/${item.id}`, { method: 'DELETE' }), t('msg.itemDeleted', 'Item deleted'),
    async () => {
      closeConfigItem();
      await loadConfigItemPage();
    });
}

/**
 * Puts the item form back to adding one, empty. keepShown leaves which item is
 * open alone, for opening another in its place.
 */
function resetConfigItemForm({ keepShown = false } = {}) {
  const form = $('#form-configitem');

  form.reset();
  form.elements.id.value = '';
  delete form.dataset.editing;
  form.elements.classId.disabled = false;
  $('#configitem-fields').disabled = false;
  $('#configitem-attributes').replaceChildren();
  $('#configitem-version-shown').hidden = true;
  stopRedrawing('configItemFormTitle');
  swapTheLabel($('#configitem-form-title'), 'configitem.create', 'Add an item');
  swapTheLabel($('#configitem-submit'), 'configitem.add', 'Add the item');
  $('#configitem-submit').hidden = false;
  $('#configitem-delete').hidden = true;
  $('#table-configitem-versions tbody').replaceChildren();
  $('#configitem-links').hidden = true;
  forgetLinks($('#configitem-links'));

  if (!keepShown) itemShown = null;
}

/** Closes the item below the list. */
function closeConfigItem() {
  resetConfigItemForm();
  $('#configitem-detail').hidden = true;
}

function wireConfigItems() {
  const filter = $('#form-configitem-filter');
  let pending;

  filter.addEventListener('submit', (e) => e.preventDefault());
  filter.elements.class.addEventListener('change', () => {
    itemPaging.page = 1;
    loadConfigItemPage().catch((err) => toastFailure(err));
  });
  filter.elements.q.addEventListener('input', () => {
    clearTimeout(pending);
    pending = setTimeout(() => {
      itemPaging.page = 1;
      loadConfigItemPage().catch((err) => toastFailure(err));
    }, 250);
  });

  $('#configitems-previous').addEventListener('click', () => {
    itemPaging.page = Math.max(1, itemPaging.page - 1);
    loadConfigItemPage().catch((err) => toastFailure(err));
  });
  $('#configitems-next').addEventListener('click', () => {
    itemPaging.page += 1;
    loadConfigItemPage().catch((err) => toastFailure(err));
  });

  $('#configitem-new').addEventListener('click', () => startConfigItem().catch((err) => toastFailure(err)));

  const form = $('#form-configitem');

  form.elements.classId.addEventListener('change', () => {
    if (!form.elements.id.value) drawNewItemFields().catch((err) => toastFailure(err));
  });
  form.addEventListener('submit', (e) => { e.preventDefault(); saveConfigItem(e.target); });
  $('#configitem-cancel').addEventListener('click', closeConfigItem);
  $('#configitem-delete').addEventListener('click', () => deleteConfigItem());

  wireLinkForm($('#configitem-links'));
  wireLinkForm($('#ticket-links'));
  wireTicketMerge();
}

// --------------------------------------------------------------------- links

/**
 * How a link reads, by its type: from the record that is its source and from
 * the one that is its target. Normal reads the same both ways.
 */
const LINK_WORDS = {
  Normal: { out: () => t('link.normal', 'relevant to'), in: () => t('link.normal', 'relevant to') },
  ParentChild: { out: () => t('link.parentOf', 'parent of'), in: () => t('link.childOf', 'child of') },
  DependsOn: { out: () => t('link.dependsOn', 'depends on'), in: () => t('link.requiredFor', 'required for') },
};

/**
 * The kinds of record a link reaches: how one is searched for and with which
 * right, how it is written, and how it is opened.
 */
const LINK_KINDS = {
  ticket: {
    path: '/tickets',
    right: 'tickets:read',
    describe: (ticket) => `${ticket.number} ${ticket.title}`,
    name: () => t('link.aTicket', 'a ticket'),
    open: (id) => { switchView('tickets'); return openTicket(id); },
  },
  configitem: {
    path: '/cmdb/items',
    right: 'assets:read',
    describe: (item) => `${item.number} ${item.name}`,
    name: () => t('link.anItem', 'a configuration item'),
    open: (id) => { switchView('assets'); return openConfigItem(id); },
  },
  change: {
    path: '/changes',
    right: 'changes:read',
    describe: (change) => `${change.number} ${change.title}`,
    name: () => t('link.aChange', 'a change'),
    open: (id) => { switchView('changes'); return openItsmChange(id); },
  },
};

/**
 * Offers a link form only the kinds of record its reader may search for - the
 * right a link to one is made with - and chooses the first of them where the
 * one chosen is not offered.
 */
function offerLinkKinds(form) {
  const select = form.elements.object;

  for (const option of select.options) option.hidden = !can(LINK_KINDS[option.value].right);

  if (select.selectedOptions[0]?.hidden) {
    select.value = [...select.options].find((option) => !option.hidden)?.value ?? '';
  }
}

/**
 * Draws a record's links into its section: how each reads from here, the record
 * at its other end, which opens it, and - where the reader may change this
 * record - the way to undo it and to link another.
 */
async function drawLinks(section, object, id, canChange) {
  const links = (await api(`/links?object=${object}&record=${id}`))?.items ?? [];
  const list = section.querySelector('.link-list');

  section.dataset.object = object;
  section.dataset.record = String(id);

  list.replaceChildren(...links.map((link) => el('li', {},
    el('span', { class: 'muted', text: LINK_WORDS[link.type]?.[link.outward ? 'out' : 'in']() ?? link.type }),
    ' ',
    el('button', {
      type: 'button',
      class: 'link',
      text: link.label,
      onclick: () => LINK_KINDS[link.object]?.open(link.objectId)?.catch((err) => toastFailure(err)),
    }),
    canChange ? ' ' : null,
    canChange ? el('button', {
      type: 'button',
      class: 'link',
      text: t('link.undo', 'undo'),
      onclick: () => mutate(() => api(`/links/${link.id}?object=${object}&record=${id}`, { method: 'DELETE' }),
        t('msg.unlinked', 'Link undone'), () => drawLinks(section, object, id, canChange)),
    }) : null)));

  if (!links.length) list.append(el('li', { class: 'muted', text: t('link.none', 'Nothing linked yet.') }));

  const form = section.querySelector('form');
  if (form) {
    form.hidden = !canChange;
    form.dataset.canChange = canChange ? 'yes' : '';
  }
}

/** Empties a record's links, for a record closed or a reader gone. */
function forgetLinks(section) {
  section.querySelector('.link-list').replaceChildren();
  delete section.dataset.object;
  delete section.dataset.record;

  const form = section.querySelector('form');
  if (form) {
    form.reset();
    form.elements.record.replaceChildren(el('option', { value: '', text: '–' }));
  }
}

/**
 * Wires a section's form: the types in the words they read with from here, a
 * search for the record to link, which fills the choice, and the link made from
 * the record the section shows.
 */
function wireLinkForm(section) {
  const form = section.querySelector('form');
  if (!form) return;

  const kind = () => form.elements.object.value;
  let pending;

  const offer = async () => {
    const query = new URLSearchParams({ q: form.elements.find.value.trim(), limit: String(SEARCH_OFFERS) });
    const found = (await api(`${LINK_KINDS[kind()].path}?${query}`))?.items ?? [];

    form.elements.record.replaceChildren(el('option', { value: '', text: '–' }),
      ...found
        .filter((record) => !(kind() === section.dataset.object && String(record.id) === section.dataset.record))
        .map((record) => el('option', { value: String(record.id), text: LINK_KINDS[kind()].describe(record) })));
  };

  const types = () => fillSelect(form.elements.type, Object.keys(LINK_WORDS)
    .map((type) => ({ value: type, label: LINK_WORDS[type].out() })), { labelKey: 'label', valueKey: 'value' });

  types();
  redrawable(`linkTypes:${section.getAttribute('id')}`, types);

  form.elements.record.replaceChildren(el('option', { value: '', text: '–' }));
  form.elements.find.addEventListener('input', () => {
    clearTimeout(pending);
    pending = setTimeout(() => offer().catch((err) => toastFailure(err)), 250);
  });
  form.elements.object.addEventListener('change', () => offer().catch((err) => toastFailure(err)));

  form.addEventListener('submit', (e) => {
    e.preventDefault();

    const object = section.dataset.object;
    const id = Number(section.dataset.record);
    const body = {
      sourceObject: object, sourceId: id, targetObject: kind(),
      targetId: Number(form.elements.record.value) || 0, type: form.elements.type.value,
    };

    mutate(() => api('/links', { method: 'POST', body: JSON.stringify(body) }), t('msg.linked', 'Linked'),
      async () => {
        form.reset();
        types();
        form.elements.record.replaceChildren(el('option', { value: '', text: '–' }));
        await drawLinks(section, object, id, form.dataset.canChange === 'yes');
      });
  });
}

// ------------------------------------------------------------------ changes

/**
 * The lists the Changes screen picks from and the people it can name, read on
 * each load: a change's category, impact, priority and state, and a work
 * order's state and type.
 */
const itsmChangeLists = {
  categories: [], impacts: [], priorities: [], states: [], orderStates: [], orderTypes: [], people: [],
};

/** Which page of changes the list shows. */
const itsmChangePaging = { page: 1, limit: 50, total: 0 };

/** The change open below the list, or null. */
let itsmChangeShown = null;

/** The work order open in its form, or null while the form adds one or is closed. */
let workOrderShown = null;

/** A list's values as choices: those still offered, and the one given, which may since have been retired. */
function catalogChoices(entries, kept) {
  return entries
    .filter((entry) => entry.valid || entry.id === kept)
    .map((entry) => ({ value: String(entry.id), label: catalogValueTitle(entry.name) }));
}

/**
 * The people a field can name, as choices: those who may work on changes for a
 * manager or a builder, anybody who may see them for a work order's agent -
 * and the one named already, who may since have lost the right.
 */
function peopleChoices(working, kept, keptName) {
  const people = itsmChangeLists.people
    .filter((person) => !working || person.works)
    .map((person) => ({ value: String(person.id), label: person.name }));

  if (kept && !people.some((person) => person.value === String(kept))) {
    people.push({ value: String(kept), label: keptName || `#${kept}` });
  }

  return people;
}

/** From when to when something is planned or ran, since when where it goes on, or a dash. */
function momentSpan(start, end) {
  if (start && end) return fillIn(t('itsmchange.span', '{0} to {1}'), [fmtMoment(start), fmtMoment(end)]);
  if (start) return fillIn(t('workorder.since', 'since {0}'), [fmtMoment(start)]);

  return '–';
}

/**
 * Fills the Changes screen for whoever may read changes: the lists a change and
 * its work orders pick from, the people they can name, the page of changes,
 * and the open change, read again - unless somebody is writing in it or in one
 * of its work orders.
 */
async function loadItsmChanges() {
  if (!can('changes:read')) return;

  const list = (name) => api(`/general-catalog?class=${encodeURIComponent(`ITSM::ChangeManagement::${name}`)}`);
  const [categories, impacts, priorities, states, orderStates, orderTypes, people] = await Promise.all([
    list('Category'), list('Impact'), list('Priority'), list('Change::State'), list('WorkOrder::State'),
    list('WorkOrder::Type'), api('/changes/people'),
  ]);

  Object.assign(itsmChangeLists, {
    categories: categories?.items ?? [],
    impacts: impacts?.items ?? [],
    priorities: priorities?.items ?? [],
    states: states?.items ?? [],
    orderStates: orderStates?.items ?? [],
    orderTypes: orderTypes?.items ?? [],
    people: people?.items ?? [],
  });

  fillSelect($('#form-itsmchange-filter').elements.state,
    itsmChangeLists.states.map((entry) => ({ value: String(entry.id), label: catalogValueTitle(entry.name) })),
    { placeholder: t('itsmchange.everyState', 'every state'), labelKey: 'label', valueKey: 'value' });

  await loadItsmChangePage();

  if (itsmChangeShown && !beingEdited($('#form-itsmchange')) && !beingEdited($('#form-workorder'))) {
    await openItsmChange(itsmChangeShown.id);
  }
}

/** Reads the page of changes the filter asks for, and draws it. */
async function loadItsmChangePage() {
  const filter = $('#form-itsmchange-filter');
  const query = new URLSearchParams({
    limit: String(itsmChangePaging.limit), page: String(itsmChangePaging.page),
  });

  if (filter.elements.state.value) query.set('state', filter.elements.state.value);
  if (filter.elements.q.value.trim()) query.set('q', filter.elements.q.value.trim());

  const page = await api(`/changes?${query}`);
  itsmChangePaging.total = page?.totalCount ?? 0;

  fillTable($('#table-itsmchange tbody'), (page?.items ?? []).map(itsmChangeRow), 7,
    t('itsmchange.empty', 'No changes.'));

  const { total, limit } = itsmChangePaging;
  const from = total === 0 ? 0 : (itsmChangePaging.page - 1) * limit + 1;
  const to = Math.min(itsmChangePaging.page * limit, total);

  $('#itsmchanges-range').textContent = fillIn(t('ticket.range', '{0}–{1} of {2}'), [from, to, total]);
  $('#itsmchanges-previous').disabled = itsmChangePaging.page <= 1;
  $('#itsmchanges-next').disabled = to >= total;
}

/** One change as a row of the list. */
function itsmChangeRow(change) {
  return el('tr', {},
    el('td', { class: 'text' },
      el('button', { class: 'link', text: change.number, onclick: () => openItsmChange(change.id) })),
    el('td', { class: 'text', text: change.title }),
    el('td', { class: 'text', text: catalogEntryTitle(itsmChangeLists.states, change.stateId) }),
    el('td', { class: 'text', text: catalogEntryTitle(itsmChangeLists.priorities, change.priorityId) }),
    el('td', { class: 'text', text: change.managerName || '–' }),
    el('td', { text: momentSpan(change.plannedStartAt, change.plannedEndAt) }),
    el('td', { class: 'num', text: String(change.workOrders) }),
  );
}

/** Fills the change form's lists, keeping what a change already names. */
function fillItsmChangeForm(form, change) {
  const choose = { labelKey: 'label', valueKey: 'value' };
  const nobody = { ...choose, placeholder: t('ticket.nobody', 'nobody') };

  fillSelect(form.elements.categoryId, catalogChoices(itsmChangeLists.categories, change?.categoryId), choose);
  fillSelect(form.elements.impactId, catalogChoices(itsmChangeLists.impacts, change?.impactId), choose);
  fillSelect(form.elements.priorityId, catalogChoices(itsmChangeLists.priorities, change?.priorityId),
    { ...choose, placeholder: t('itsmchange.byMatrix', 'from category and impact') });
  fillSelect(form.elements.managerId, peopleChoices(true, change?.managerId, change?.managerName), nobody);
  fillSelect(form.elements.builderId, peopleChoices(true, change?.builderId, change?.builderName), nobody);
}

/** Opens the form for a new change, ranked in the middle of the scale, as OTRS ranks one nobody ranks. */
function startItsmChange() {
  const form = $('#form-itsmchange');
  const normal = (entries) => String(entries.find((entry) => entry.name === '3-normal')?.id ?? '');

  resetItsmChangeForm();
  $('#itsmchange-detail').hidden = false;
  fillItsmChangeForm(form, null);
  form.elements.categoryId.value = normal(itsmChangeLists.categories);
  form.elements.impactId.value = normal(itsmChangeLists.impacts);
  form.elements.priorityId.value = '';
  form.elements.managerId.value = '';
  form.elements.builderId.value = '';

  $('#itsmchange-detail').scrollIntoView({ block: 'start' });
  form.elements.title.focus();
}

/**
 * Opens a change below the list: its form, read only for whoever may not work
 * on changes; where it stands and where it may go; its work orders; and what it
 * is linked to.
 */
async function openItsmChange(id) {
  let change;
  let orders;

  try {
    [change, orders] = await Promise.all([api(`/changes/${id}`), api(`/changes/${id}/work-orders`)]);
  } catch (err) {
    toastFailure(err);
    closeItsmChange();
    await loadItsmChangePage();

    return;
  }

  const form = $('#form-itsmchange');

  resetItsmChangeForm({ keepShown: true });
  itsmChangeShown = change;
  $('#itsmchange-detail').hidden = false;

  form.elements.id.value = String(change.id);
  fillItsmChangeForm(form, change);
  form.elements.title.value = change.title;
  form.elements.description.value = change.description;
  form.elements.justification.value = change.justification;
  form.elements.categoryId.value = String(change.categoryId);
  form.elements.impactId.value = String(change.impactId);
  form.elements.priorityId.value = String(change.priorityId);
  form.elements.managerId.value = change.managerId ? String(change.managerId) : '';
  form.elements.builderId.value = change.builderId ? String(change.builderId) : '';

  const requested = change.requestedAt ? dayAndTimeIn(change.requestedAt, readerZone()) : { day: '', time: '' };
  setDateField(form.elements.requestedDay, requested.day);
  form.elements.requestedTime.value = requested.time;

  $('#itsmchange-fields').disabled = !can('changes:write');

  // Drawn rather than declared with a key, because it carries the change's
  // number; see editRole for why the key has to come off.
  const heading = $('#itsmchange-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('itsmChangeFormTitle', () => {
    heading.textContent = fillIn(t('itsmchange.edit', 'Change {0}'), [change.number]);
  });

  swapTheLabel($('#itsmchange-submit'), 'itsmchange.save', 'Save the change');
  $('#itsmchange-delete').hidden = !can('changes:write');

  drawItsmChangeStanding(change);
  drawWorkOrders(orders?.items ?? []);

  const linked = $('#itsmchange-links');
  offerLinkKinds($('#form-itsmchange-link'));
  linked.hidden = false;
  await drawLinks(linked, 'change', change.id, can('changes:write'));
}

/**
 * Says where the open change stands and what its work orders say of when it
 * runs, and offers the states the machine lets it move on to - nothing where
 * its state ends it, or the reader may not work on changes.
 */
function drawItsmChangeStanding(change) {
  const minutes = (count) => fillIn(t('ticket.minutes', '{0} minutes'), [count]);
  const move = $('#form-itsmchange-move');
  const next = change.nextStateIds ?? [];

  $('#itsmchange-standing').textContent = fillIn(t('itsmchange.standing',
    'State: {0}. Planned: {1}. Ran: {2}. {3} planned and {4} accounted over {5} work orders.'), [
    catalogEntryTitle(itsmChangeLists.states, change.stateId),
    momentSpan(change.plannedStartAt, change.plannedEndAt),
    momentSpan(change.actualStartAt, change.actualEndAt),
    minutes(change.plannedMinutes), minutes(change.accountedMinutes), change.workOrders,
  ]);

  fillSelect(move.elements.stateId,
    next.map((id) => ({ value: String(id), label: catalogEntryTitle(itsmChangeLists.states, id) })),
    { labelKey: 'label', valueKey: 'value' });
  move.hidden = !next.length || !can('changes:write');
  $('#itsmchange-state').hidden = false;
}

/**
 * Writes the change: a new one, or what the open one says now. Its priority is
 * sent only where it was chosen, so that a category or an impact changed here
 * is ranked by the matrix rather than kept at the priority it had.
 */
async function saveItsmChange(form) {
  const id = form.elements.id.value;
  const number = (name) => Number(form.elements[name].value) || 0;
  const priority = number('priorityId');
  const day = form.elements.requestedDay.value;
  const body = {
    title: form.elements.title.value,
    description: form.elements.description.value,
    justification: form.elements.justification.value,
    categoryId: number('categoryId'),
    impactId: number('impactId'),
    ...(priority && priority !== itsmChangeShown?.priorityId ? { priorityId: priority } : {}),
    ...(number('managerId') ? { managerId: number('managerId') } : { clearManager: true }),
    ...(number('builderId') ? { builderId: number('builderId') } : { clearBuilder: true }),
    ...(day ? { requestedAt: momentIn(day, form.elements.requestedTime.value, readerZone()) }
      : { clearRequestedAt: true }),
  };

  await mutate(() => api(id ? `/changes/${id}` : '/changes',
    { method: id ? 'PUT' : 'POST', body: JSON.stringify(body) }),
  id ? t('msg.itsmChangeSaved', 'Change saved') : t('msg.itsmChangeAdded', 'Change added'),
  async (change) => {
    delete form.dataset.editing;
    await loadItsmChangePage();
    await openItsmChange(change.id);
  });
}

/** Moves the open change on to the state chosen. */
async function moveItsmChange(form) {
  if (!itsmChangeShown) return;

  const { id } = itsmChangeShown;

  await mutate(() => api(`/changes/${id}`,
    { method: 'PUT', body: JSON.stringify({ stateId: Number(form.elements.stateId.value) || 0 }) }),
  t('msg.itsmChangeMoved', 'Change moved on'),
  async () => {
    await loadItsmChangePage();
    await openItsmChange(id);
  });
}

/** Deletes the open change with its work orders and its links, once asked. */
async function deleteItsmChange() {
  if (!itsmChangeShown) return;

  const change = itsmChangeShown;
  const sure = await confirmDialog({
    title: t('itsmchange.deleteTitle', 'Delete the change?'),
    text: fillIn(t('itsmchange.deleteText', '{0} {1} goes with its work orders and its links.'),
      [change.number, change.title]),
    confirmLabel: t('itsmchange.deleteConfirm', 'Delete it'),
  });

  if (!sure) return;

  await mutate(() => api(`/changes/${change.id}`, { method: 'DELETE' }),
    t('msg.itsmChangeDeleted', 'Change deleted'),
    async () => {
      closeItsmChange();
      await loadItsmChangePage();
    });
}

/**
 * Puts the change form back to adding one, empty, with nothing below it.
 * keepShown leaves which change is open alone, for opening another in its
 * place.
 */
function resetItsmChangeForm({ keepShown = false } = {}) {
  const form = $('#form-itsmchange');

  form.reset();
  form.elements.id.value = '';
  delete form.dataset.editing;
  $('#itsmchange-fields').disabled = false;
  stopRedrawing('itsmChangeFormTitle');
  swapTheLabel($('#itsmchange-form-title'), 'itsmchange.create', 'Add a change');
  swapTheLabel($('#itsmchange-submit'), 'itsmchange.add', 'Add the change');
  $('#itsmchange-submit').hidden = !can('changes:write');
  $('#itsmchange-delete').hidden = true;
  $('#itsmchange-state').hidden = true;
  $('#itsmchange-standing').textContent = '';
  $('#itsmchange-workorders').hidden = true;
  $('#table-workorder tbody').replaceChildren();
  resetWorkOrderForm();
  $('#itsmchange-links').hidden = true;
  forgetLinks($('#itsmchange-links'));

  if (!keepShown) itsmChangeShown = null;
}

/** Closes the change below the list. */
function closeItsmChange() {
  resetItsmChangeForm();
  $('#itsmchange-detail').hidden = true;
}

/** Draws the open change's work orders, in the order of its plan. */
function drawWorkOrders(orders) {
  fillTable($('#table-workorder tbody'), orders.map(workOrderRow), 8, t('workorder.empty', 'No work orders yet.'));
  $('#itsmchange-workorders').hidden = false;
}

/** One work order as a row of its change's table. */
function workOrderRow(order) {
  return el('tr', {},
    el('td', { class: 'num', text: String(order.number) }),
    el('td', { class: 'text' },
      el('button', { type: 'button', class: 'link', text: order.title, onclick: () => openWorkOrder(order) })),
    el('td', { class: 'text', text: catalogEntryTitle(itsmChangeLists.orderStates, order.stateId) }),
    el('td', { class: 'text', text: catalogEntryTitle(itsmChangeLists.orderTypes, order.workOrderTypeId) }),
    el('td', { class: 'text', text: order.agentName || '–' }),
    el('td', { text: momentSpan(order.plannedStartAt, order.plannedEndAt) }),
    el('td', { text: momentSpan(order.actualStartAt, order.actualEndAt) }),
    el('td', { class: 'num', text: fillIn(t('ticket.minutes', '{0} minutes'), [order.accountedMinutes]) }),
  );
}

/**
 * Fills the work order form from a work order, or empty and of OTRS's default
 * type for a new one. Its state offers where it stands and where the machine
 * lets it go from there.
 */
function fillWorkOrderForm(form, order) {
  const choose = { labelKey: 'label', valueKey: 'value' };
  const states = order ? [order.stateId, ...(order.nextStateIds ?? [])] : [];

  fillSelect(form.elements.workOrderTypeId, catalogChoices(itsmChangeLists.orderTypes, order?.workOrderTypeId),
    choose);
  fillSelect(form.elements.agentId, peopleChoices(false, order?.agentId, order?.agentName),
    { ...choose, placeholder: t('ticket.nobody', 'nobody') });
  fillSelect(form.elements.stateId,
    states.map((id) => ({ value: String(id), label: catalogEntryTitle(itsmChangeLists.orderStates, id) })), choose);

  const plain = itsmChangeLists.orderTypes.find((entry) => entry.name === 'workorder')?.id;

  form.elements.workOrderTypeId.value = String(order?.workOrderTypeId ?? plain ?? '');
  form.elements.agentId.value = order?.agentId ? String(order.agentId) : '';
  form.elements.stateId.value = order ? String(order.stateId) : '';
  form.elements.title.value = order?.title ?? '';
  form.elements.instruction.value = order?.instruction ?? '';
  form.elements.report.value = order?.report ?? '';
  form.elements.plannedMinutes.value = order ? String(order.plannedMinutes) : '';
  form.elements.minutes.value = '';

  for (const [field, at] of [
    ['plannedStart', order?.plannedStartAt], ['plannedEnd', order?.plannedEndAt],
    ['actualStart', order?.actualStartAt], ['actualEnd', order?.actualEndAt],
  ]) {
    const moment = at ? dayAndTimeIn(at, readerZone()) : { day: '', time: '' };

    setDateField(form.elements[`${field}Day`], moment.day);
    form.elements[`${field}Time`].value = moment.time;
  }
}

/** Opens the form for a new work order of the open change, without the report a new one has nothing for. */
function startWorkOrder() {
  const form = $('#form-workorder');

  resetWorkOrderForm();
  form.hidden = false;
  fillWorkOrderForm(form, null);
  form.scrollIntoView({ block: 'nearest' });
  form.elements.title.focus();
}

/**
 * Opens a work order in its form: all of it for whoever may work on changes,
 * the report alone for its agent - whom the server lets write nothing else -
 * and read only for anybody else.
 */
function openWorkOrder(order) {
  const form = $('#form-workorder');
  const plans = can('changes:write');
  const reports = plans || (Boolean(order.agentId) && order.agentId === me.user?.id);

  resetWorkOrderForm();
  workOrderShown = order;
  form.hidden = false;
  form.elements.id.value = String(order.id);
  fillWorkOrderForm(form, order);

  $('#workorder-plan').disabled = !plans;
  $('#workorder-report').hidden = false;
  $('#workorder-report').disabled = !reports;
  $('#workorder-submit').hidden = !reports;
  $('#workorder-delete').hidden = !plans;

  // Drawn rather than declared with a key, because it carries the work
  // order's number; see editRole for why the key has to come off.
  const heading = $('#workorder-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('workOrderFormTitle', () => {
    heading.textContent = fillIn(t('workorder.edit', 'Work order {0}'), [order.number]);
  });

  swapTheLabel($('#workorder-submit'), 'workorder.save', 'Save the work order');
  form.scrollIntoView({ block: 'nearest' });
}

/**
 * Writes the work order: a new one of the open change, or what the open one
 * says now - only what the reader may write of it, so that its agent's report
 * is not refused for the plan beside it.
 */
async function saveWorkOrder(form) {
  if (!itsmChangeShown) return;

  const changeId = itsmChangeShown.id;
  const order = workOrderShown;
  const moment = (field) => {
    const day = form.elements[`${field}Day`].value;

    return day ? momentIn(day, form.elements[`${field}Time`].value, readerZone()) : null;
  };
  const moments = (startField, endField, startKey, endKey, clearKey) => {
    const [start, end] = [moment(startField), moment(endField)];

    if (!start && !end) return { [clearKey]: true };

    return { ...(start ? { [startKey]: start } : {}), ...(end ? { [endKey]: end } : {}) };
  };
  const body = {};

  if (!$('#workorder-plan').disabled) {
    const agent = Number(form.elements.agentId.value) || 0;

    Object.assign(body, {
      title: form.elements.title.value,
      instruction: form.elements.instruction.value,
      workOrderTypeId: Number(form.elements.workOrderTypeId.value) || 0,
      plannedMinutes: Number(form.elements.plannedMinutes.value) || 0,
      ...(agent ? { agentId: agent } : { clearAgent: true }),
      ...moments('plannedStart', 'plannedEnd', 'plannedStartAt', 'plannedEndAt', 'clearPlanned'),
    });
  }

  if (order && !$('#workorder-report').disabled) {
    const state = Number(form.elements.stateId.value) || 0;

    Object.assign(body, {
      report: form.elements.report.value,
      minutes: Number(form.elements.minutes.value) || 0,
      ...(state && state !== order.stateId ? { stateId: state } : {}),
      ...moments('actualStart', 'actualEnd', 'actualStartAt', 'actualEndAt', 'clearActual'),
    });
  }

  await mutate(() => api(order ? `/work-orders/${order.id}` : `/changes/${changeId}/work-orders`,
    { method: order ? 'PUT' : 'POST', body: JSON.stringify(body) }),
  order ? t('msg.workOrderSaved', 'Work order saved') : t('msg.workOrderAdded', 'Work order added'),
  async () => {
    resetWorkOrderForm();
    await loadItsmChangePage();
    await openItsmChange(changeId);
  });
}

/** Deletes the open work order, once asked. */
async function deleteWorkOrder() {
  const order = workOrderShown;
  if (!order || !itsmChangeShown) return;

  const changeId = itsmChangeShown.id;
  const sure = await confirmDialog({
    title: t('workorder.deleteTitle', 'Delete the work order?'),
    text: fillIn(t('workorder.deleteText', '{0} goes, with its report and the time accounted on it.'),
      [order.title]),
    confirmLabel: t('workorder.deleteConfirm', 'Delete it'),
  });

  if (!sure) return;

  await mutate(() => api(`/work-orders/${order.id}`, { method: 'DELETE' }),
    t('msg.workOrderDeleted', 'Work order deleted'),
    async () => {
      resetWorkOrderForm();
      await loadItsmChangePage();
      await openItsmChange(changeId);
    });
}

/** Puts the work order form back to adding one, empty and closed. */
function resetWorkOrderForm() {
  const form = $('#form-workorder');

  form.reset();
  form.elements.id.value = '';
  delete form.dataset.editing;
  workOrderShown = null;
  $('#workorder-plan').disabled = false;
  $('#workorder-report').disabled = false;
  $('#workorder-report').hidden = true;
  stopRedrawing('workOrderFormTitle');
  swapTheLabel($('#workorder-form-title'), 'workorder.create', 'Add a work order');
  swapTheLabel($('#workorder-submit'), 'workorder.add', 'Add the work order');
  $('#workorder-submit').hidden = false;
  $('#workorder-delete').hidden = true;
  form.hidden = true;
}

function wireItsmChanges() {
  const filter = $('#form-itsmchange-filter');
  const reload = () => loadItsmChangePage().catch((err) => toastFailure(err));
  let pending;

  filter.addEventListener('submit', (e) => e.preventDefault());
  filter.elements.state.addEventListener('change', () => {
    itsmChangePaging.page = 1;
    reload();
  });
  filter.elements.q.addEventListener('input', () => {
    clearTimeout(pending);
    pending = setTimeout(() => {
      itsmChangePaging.page = 1;
      reload();
    }, 250);
  });

  $('#itsmchanges-previous').addEventListener('click', () => {
    itsmChangePaging.page = Math.max(1, itsmChangePaging.page - 1);
    reload();
  });
  $('#itsmchanges-next').addEventListener('click', () => {
    itsmChangePaging.page += 1;
    reload();
  });

  $('#itsmchange-new').addEventListener('click', startItsmChange);
  $('#form-itsmchange').addEventListener('submit', (e) => { e.preventDefault(); saveItsmChange(e.target); });
  $('#itsmchange-cancel').addEventListener('click', closeItsmChange);
  $('#itsmchange-delete').addEventListener('click', () => deleteItsmChange());
  $('#form-itsmchange-move').addEventListener('submit', (e) => { e.preventDefault(); moveItsmChange(e.target); });

  $('#workorder-new').addEventListener('click', startWorkOrder);
  $('#form-workorder').addEventListener('submit', (e) => { e.preventDefault(); saveWorkOrder(e.target); });
  $('#workorder-close').addEventListener('click', resetWorkOrderForm);
  $('#workorder-delete').addEventListener('click', () => deleteWorkOrder());

  wireLinkForm($('#itsmchange-links'));
}

// ------------------------------------------------------------------ portal

/** The ticket open on the portal's screen, or null. */
let portalTicketShown = null;

/**
 * Fills the portal's screen for an account that reaches it: a page of the
 * tickets it follows and the queues a ticket is opened in. The question of
 * which queue is put only where there is more than one, and the form goes where
 * there is none, with a sentence saying why rather than nothing.
 */
async function loadPortal() {
  if (!can('portal:use')) return;

  await loadPortalTickets();
  await loadPublishedFAQ('portal');

  const form = $('#form-portal-ticket');
  let queues = [];

  try {
    queues = (await api('/portal/queues'))?.items ?? [];
  } catch (err) {
    // An account that signs in as no contact is refused this, and has nothing
    // to open a ticket as - which the empty screen says on its own.
    if (err.status !== 409) throw err;
  }

  // A choice nobody has made yet where there are several, so that the first
  // queue in the list is not where every ticket lands because nobody looked.
  if (!beingEdited(form)) {
    fillSelect(form.elements.queueId, queues,
      queues.length > 1 ? { placeholder: t('portal.chooseQueue', 'choose…') } : {});
  }

  $('#portal-queue-choice').hidden = queues.length < 2;
  form.elements.queueId.required = queues.length > 1;
  form.hidden = queues.length === 0;
  $('#portal-no-queue').hidden = queues.length > 0;
}

/** Reads the page of tickets the filter asks for, and draws it. */
async function loadPortalTickets() {
  const finished = $('#form-portal-filter').elements.finished.value === 'true';
  const page = await api(`/portal/tickets?limit=${CUSTOMER_PAGE}${finished ? '&finished=true' : ''}`);
  const items = page?.items ?? [];

  fillTable($('#table-portal tbody'), items.map((ticket) => el('tr', {},
    el('td', { class: 'text' }, el('button', {
      type: 'button', class: 'link', text: ticket.title, onclick: () => openPortalTicket(ticket.id),
    })),
    el('td', { text: ticketName(ticket.number) }),
    el('td', { text: statusName(ticket.state) }),
    el('td', { class: 'text', text: ticket.contactName || '–' }),
    el('td', { text: fmtMoment(ticket.updatedAt) }),
  )), 5, finished
    ? t('portal.noneFinished', 'No finished tickets.')
    : t('portal.none', 'No tickets being worked on.'));
  customerOverflow($('#portal-overflow'), items.length, page?.totalCount ?? 0);
}

/**
 * Opens one of the tickets on the portal's screen: where it stands, and what
 * the customer is shown of it, the oldest first, each article with who wrote it
 * and its files.
 */
async function openPortalTicket(id) {
  const [ticket, articles] = await Promise.all([
    api(`/portal/tickets/${id}`), api(`/portal/tickets/${id}/articles`),
  ]);

  portalTicketShown = ticket;

  $('#portal-ticket-title').textContent = ticket.title;
  $('#portal-ticket-facts').textContent = [
    ticketName(ticket.number),
    statusName(ticket.state),
    fillIn(t('portal.opened', 'opened {0}'), [fmtMoment(ticket.createdAt)]),
  ].join(' · ');

  $('#portal-thread').replaceChildren(...(articles?.items ?? []).map((article) => el('li', {},
    el('p', { class: 'muted', text: [
      article.sender === 'customer'
        ? (article.authorName || t('portal.you', 'you'))
        : (article.authorName || t('portal.serviceDesk', 'the service desk')),
      fmtMoment(article.createdAt),
    ].join(' · ') }),
    article.subject ? el('strong', { text: article.subject }) : null,
    el('div', { class: 'ticket-article-body', text: article.body }),
    article.attachments.length ? el('ul', { class: 'ticket-attachments' }, ...article.attachments.map((file) => el('li', {},
      el('button', {
        type: 'button',
        class: 'link',
        text: `${file.filename} (${fileSize(file.size)})`,
        onclick: () => downloadAttachment(ticket.id, file, 'portal/tickets').catch((err) => toastFailure(err)),
      })))) : null,
  )));

  $('#portal-ticket').hidden = false;
  $('#portal-ticket').scrollIntoView({ block: 'start' });
}

/** Puts the open ticket away, and what was written about it with it. */
function closePortalTicket() {
  portalTicketShown = null;
  $('#portal-ticket').hidden = true;
  $('#portal-thread').replaceChildren();
  $('#portal-ticket-title').textContent = '';
  $('#portal-ticket-facts').textContent = '';
  $('#form-portal-answer').reset();
}

/**
 * Opens a ticket from the portal's form, and then sends its files with the
 * article it was opened with - after the form is emptied, as an article's files
 * go on the staff's side, so that a file refused cannot leave the text behind
 * for a second press to send again.
 */
function savePortalTicket(form) {
  const files = [...(form.elements.files.files ?? [])];
  const queue = form.elements.queueId.value;
  const body = JSON.stringify({
    title: form.elements.title.value.trim(),
    body: form.elements.body.value,
    ...(queue && !$('#portal-queue-choice').hidden ? { queueId: Number(queue) } : {}),
  });

  mutate(() => api('/portal/tickets', { method: 'POST', body }), t('msg.portalOpened', 'Ticket opened'),
    async (ticket) => {
      form.reset();

      if (ticket?.id && files.length) {
        const articles = await api(`/portal/tickets/${ticket.id}/articles`);
        await attachFiles(ticket.id, articles?.items?.[0]?.id, files, 'portal/tickets');
      }

      await loadPortalTickets();
      if (ticket?.id) await openPortalTicket(ticket.id);
    });
}

/**
 * Sends the customer's answer to the open ticket, with its files. The answer
 * says which ticket it landed in, which is a new one where the ticket was
 * finished and its queue opens one for an answer - and that is the one opened
 * afterwards.
 */
function savePortalAnswer(form) {
  if (!portalTicketShown) return;

  const id = portalTicketShown.id;
  const files = [...(form.elements.files.files ?? [])];
  const body = JSON.stringify({ body: form.elements.body.value });

  mutate(() => api(`/portal/tickets/${id}/articles`, { method: 'POST', body }), t('msg.portalAnswered', 'Answer sent'),
    async (article) => {
      form.reset();

      const landed = article?.ticketId || id;
      await attachFiles(landed, article?.id, files, 'portal/tickets');
      await loadPortalTickets();
      await openPortalTicket(landed);
    });
}

/** Wires the portal's screen. */
function wirePortal() {
  $('#form-portal-filter').addEventListener('change', () => loadPortalTickets().catch((err) => toastFailure(err)));
  $('#form-portal-filter').addEventListener('submit', (e) => e.preventDefault());
  $('#form-portal-ticket').addEventListener('submit', (e) => { e.preventDefault(); savePortalTicket(e.target); });
  $('#form-portal-answer').addEventListener('submit', (e) => { e.preventDefault(); savePortalAnswer(e.target); });
  $('#portal-ticket-close').addEventListener('click', closePortalTicket);
}

/** Opens an organisation or a contact in its card's form, to correct it. */
function editCustomer(kind, entry) {
  const form = $(`#form-${kind}`);
  const card = CUSTOMER_FORMS[kind];

  form.elements.id.value = String(entry.id);
  for (const field of card.fields) form.elements[field].value = entry[field] ?? '';
  form.elements.valid.checked = entry.valid;

  if (kind === 'contact') {
    form.elements.seesOrganisation.checked = entry.seesOrganisation;
    offerFound('contactOrganisation', keptEntry(entry.organisationId, entry.organisationName));
  }

  // Drawn rather than declared with a key, because it carries the entry's name;
  // see editRole for why the key has to come off.
  const heading = $(`#${kind}-form-title`);
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable(`${kind}FormTitle`, () => { heading.textContent = card.editing(entry); });

  swapTheLabel($(`#${kind}-submit`), 'action.save', 'Save');
  $(`#${kind}-cancel`).hidden = false;
  form.elements.name.focus();
}

/** Puts a customer card's form back to making a new entry. */
function resetCustomerForm(kind) {
  const form = $(`#form-${kind}`);
  const card = CUSTOMER_FORMS[kind];

  form.reset();
  form.elements.id.value = '';

  if (kind === 'contact') offerFound('contactOrganisation', null);

  stopRedrawing(`${kind}FormTitle`);
  swapTheLabel($(`#${kind}-form-title`), ...card.create);
  swapTheLabel($(`#${kind}-submit`), 'action.create', 'Create');
  $(`#${kind}-cancel`).hidden = true;
}

/**
 * Sends a customer card's form. A contact's organisation is taken off by saying
 * so, because an id sent as nothing cannot be told apart from one not sent.
 */
function saveCustomer(kind, form) {
  const card = CUSTOMER_FORMS[kind];
  const id = form.elements.id.value;
  const sent = { valid: form.elements.valid.checked };

  for (const field of card.fields) sent[field] = form.elements[field].value.trim();

  if (kind === 'contact') {
    const organisation = form.elements.organisationId.value;

    if (organisation) sent.organisationId = Number(organisation);
    else if (id) sent.clearOrganisation = true;

    sent.seesOrganisation = form.elements.seesOrganisation.checked;
  }

  const body = JSON.stringify(sent);

  mutate(
    () => (id ? api(`${card.path}/${id}`, { method: 'PUT', body }) : api(card.path, { method: 'POST', body })),
    id ? card.saved() : card.created(),
    async () => { resetCustomerForm(kind); await refreshAll(); },
  );
}

/** Wires the customer screen's forms. */
function wireCustomers() {
  for (const kind of Object.keys(CUSTOMER_FORMS)) {
    $(`#form-${kind}`).addEventListener('submit', (e) => { e.preventDefault(); saveCustomer(kind, e.target); });
    $(`#${kind}-cancel`).addEventListener('click', () => resetCustomerForm(kind));
  }

  for (const [filter, load] of [['#form-organisation-filter', loadOrganisationPage], ['#form-contact-filter', loadContactPage]]) {
    const form = $(filter);
    let typing;

    form.addEventListener('submit', (e) => { e.preventDefault(); load(); });
    form.addEventListener('change', () => load());
    form.elements.q.addEventListener('input', () => {
      clearTimeout(typing);
      typing = setTimeout(load, 300);
    });
  }

  wireSearches();
}

// ----------------------------------------------------------------- tickets

/** The English for how a ticket or an article arrived, which are the server's model.AgentChannels. */
const CHANNEL_TITLES = {
  note: 'note',
  phone: 'phone call',
  web: 'customer portal',
};

/**
 * The channels an agent writes through by hand, which are the ones the forms
 * offer: the portal is the customer's, and the server refuses it from anybody
 * else.
 */
const AGENT_CHANNELS = ['note', 'phone'];

/** How an article or a ticket arrived, in the reader's language. */
function channelName(channel) {
  return t(`channel.${channel}`, CHANNEL_TITLES[channel] ?? channel);
}

/**
 * What each kind of history line says, by the server's model.HistoryEvents. The
 * values are filled in by describeHistory, in the reader's words for each.
 */
const HISTORY_SENTENCES = {
  created: () => t('history.created', 'Opened the ticket'),
  title: () => t('history.title', 'Changed the title from "{0}" to "{1}"'),
  queue: () => t('history.queue', 'Moved it from {0} to {1}'),
  state: () => t('history.state', 'Changed the state from {0} to {1}'),
  priority: () => t('history.priority', 'Changed the priority from {0} to {1}'),
  type: () => t('history.type', 'Changed the type from {0} to {1}'),
  owner: () => t('history.owner', 'Changed the owner from {0} to {1}'),
  responsible: () => t('history.responsible', 'Changed who is responsible from {0} to {1}'),
  contact: () => t('history.contact', 'Changed who it is for from {0} to {1}'),
  organisation: () => t('history.organisation', 'Changed the organisation from {0} to {1}'),
  lock: () => t('history.lock', 'Locked it: {1}'),
  pending: () => t('history.pending', 'Changed how long it waits from {0} to {1}'),
  article: () => t('history.article', 'Wrote a {1}'),
  time: () => t('history.time', 'Noted {1} minutes'),
  attachment: () => t('history.attachment', 'Attached {1}'),
  sla: () => t('history.sla', 'Changed the service level from {0} to {1}'),
  merged: () => t('history.merged', 'Merged ticket {0} into ticket {1}'),
};

/** How the ticket list stands: which page, how many to a page, how many in all. */
const ticketPaging = { page: 1, limit: 25, total: 0 };

/** The ticket that is open, as the server last answered it, or nobody. */
let openTicketShown = null;

/** The zone a moment is read and written in: the account's, or the browser's before it has one. */
function readerZone() {
  return me.user?.effectiveTimezone || Intl.DateTimeFormat().resolvedOptions().timeZone;
}

/**
 * A day and a time of day in a zone, as the moment they name.
 *
 * Intl reads a moment in a zone but cannot write one, so the offset is found by
 * reading back: the wall clock taken as UTC, read in the zone, says how far off it
 * is, and reading once more at the corrected moment settles a day the zone moves
 * its clocks on.
 */
function momentIn(day, time, zone) {
  const [y, m, d] = day.split('-').map(Number);
  const [h, mi] = (time || '00:00').split(':').map(Number);
  const wall = Date.UTC(y, m - 1, d, h, mi);

  const offset = (at) => {
    const parts = Object.fromEntries(new Intl.DateTimeFormat('en-US', {
      timeZone: zone, hourCycle: 'h23', year: 'numeric', month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit',
    }).formatToParts(new Date(at)).map((part) => [part.type, part.value]));

    return Date.UTC(Number(parts.year), Number(parts.month) - 1, Number(parts.day),
      Number(parts.hour), Number(parts.minute)) - at;
  };

  const first = wall - offset(wall);

  return new Date(wall - offset(first)).toISOString();
}

/** A moment as the day and the time of day it is in a zone, as date and time fields hold them. */
function dayAndTimeIn(iso, zone) {
  const parts = Object.fromEntries(new Intl.DateTimeFormat('en-US', {
    timeZone: zone, hourCycle: 'h23', year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit',
  }).formatToParts(new Date(iso)).map((part) => [part.type, part.value]));

  return { day: `${parts.year}-${parts.month}-${parts.day}`, time: `${parts.hour}:${parts.minute}` };
}

/**
 * Fills the ticket screens, for whoever may read tickets: the lists a ticket is
 * described by, the page of tickets, and the one that is open, read again.
 */
async function loadTickets() {
  if (!can('tickets:read')) return;

  const [queues, states, priorities, types, levels] = await Promise.all(
    ['/queues', CATALOGS.state.path, CATALOGS.priority.path, CATALOGS.type.path, '/service-levels']
      .map((path) => api(path)),
  );

  cache.levels = levels?.items ?? [];
  cache.ticketQueues = queues?.items ?? [];
  cache.states = states?.items ?? [];
  cache.priorities = priorities?.items ?? [];
  cache.types = types?.items ?? [];

  const filter = $('#form-ticket-filter');
  if (filter && !beingEdited(filter)) {
    fillSelect(filter.elements.queue, queueChoices(cache.ticketQueues),
      { placeholder: t('ticket.allQueues', 'every queue'), labelKey: 'label', valueKey: 'value' });
  }

  const form = $('#form-ticket');
  if (form && !beingEdited(form) && !form.elements.id.value) {
    fillTicketForm(form, null, []);
    showTicketJob(form, false);
  }

  if (form && !beingEdited(form) && can('customers:read')) {
    await Promise.all([searchChoices('ticketContact'), searchChoices('ticketOrganisation')]);
  }

  await loadTicketPage();
  await loadTold();
  await loadNotifyChoices();

  if (openTicketShown) await openTicket(openTicketShown.id, { keepPlace: true });
}

/** Queues as choices, by their place in the tree. */
function queueChoices(queues) {
  const byId = new Map(cache.ticketQueues.map((queue) => [queue.id, queue]));

  return queues
    .map((queue) => ({ value: String(queue.id), label: queuePath(queue, byId) }))
    .sort((a, b) => a.label.localeCompare(b.label));
}

// --------------------------------------------------------- what I was told

/**
 * Draws what the notifications told the reader, the latest first, the ones not
 * read yet marked so, and offers to mark them all read while any are unread.
 */
async function loadTold() {
  if (!can('tickets:read')) return;

  const page = await api('/me/notifications');
  const told = page?.items ?? [];

  fillTable($('#table-told tbody'), told.map(toldRow), 3, t('told.empty', 'Nothing yet.'));
  $('#told-all-read').hidden = !(page?.unread > 0);
}

/** One thing the reader was told, as a row: when, what, and the ticket it is about. */
function toldRow(notification) {
  const unread = !notification.readAt;
  const actions = el('td', { class: 'actions' },
    el('button', {
      class: 'link',
      text: t('told.open', 'open the ticket'),
      onclick: () => openToldTicket(notification),
    }));

  if (unread) {
    actions.append(el('button', {
      class: 'link',
      text: t('told.read', 'mark as read'),
      onclick: () => mutate(() => api(`/me/notifications/${notification.id}/read`, { method: 'PUT' }), null, loadTold),
    }));
  }

  return el('tr', unread ? { class: 'unread' } : {},
    el('td', { text: fmtMoment(notification.createdAt) }),
    el('td', { class: 'text' },
      el('strong', { class: 'told-subject', text: notification.subject }),
      el('div', { class: 'muted told-body', text: notification.body })),
    actions);
}

/** Opens the ticket a notification is about, which reads the notification. */
function openToldTicket(notification) {
  const read = notification.readAt
    ? Promise.resolve()
    : api(`/me/notifications/${notification.id}/read`, { method: 'PUT' });

  mutate(() => read, null, async () => {
    await loadTold();
    await openTicket(notification.ticketId);
  });
}

/**
 * Draws what the reader may choose about what they are told: a switch for every
 * queue they reach, on where they follow it, and one for every notification that
 * lets them switch it off, on where they take it. Not over choices somebody is
 * part way through.
 */
async function loadNotifyChoices() {
  const form = $('#form-notify-choices');
  if (!form || !can('tickets:read') || beingEdited(form)) return;

  const choices = await api('/me/notification-choices');
  const queues = choices?.queues ?? [];
  const byId = new Map(queues.map((queue) => [queue.id, queue]));

  $('#notify-queues').replaceChildren(...queues
    .map((queue) => ({ id: queue.id, words: queuePath(queue, byId) }))
    .sort((a, b) => a.words.localeCompare(b.words))
    .map((queue) => choiceSwitch({ 'data-followed': String(queue.id) }, queue.words,
      (choices?.followed ?? []).includes(queue.id))));

  $('#notify-optional').replaceChildren(...(choices?.optional ?? []).map((definition) =>
    choiceSwitch({ 'data-taken': String(definition.id) }, definition.name,
      !(choices?.switchedOff ?? []).includes(definition.id))));
}

/** Sends what the reader chose about what they are told. */
function saveNotifyChoices(form) {
  const ids = (mark, on) => $$(`[data-${mark}]`, form).filter((box) => box.checked === on)
    .map((box) => Number(box.getAttribute(`data-${mark}`)));

  mutate(
    () => api('/me/notification-choices', {
      method: 'PUT',
      body: JSON.stringify({ followed: ids('followed', true), switchedOff: ids('taken', false) }),
    }),
    t('msg.notifyChosen', 'Saved what you are told'),
    async () => { form.reset(); await loadNotifyChoices(); },
  );
}

/** Wires the card of what the reader was told, and their choices about it. */
function wireTold() {
  const choices = $('#form-notify-choices');

  $('#told-all-read').addEventListener('click', () => mutate(
    () => api('/me/notifications/read', { method: 'PUT' }), null, loadTold));
  choices.addEventListener('submit', (e) => { e.preventDefault(); saveNotifyChoices(choices); });
}

/** Reads the page of tickets the filter asks for, and draws it. */
async function loadTicketPage() {
  const tbody = $('#table-tickets tbody');
  const filter = $('#form-ticket-filter');
  if (!tbody || !filter) return;

  const query = new URLSearchParams({ limit: String(ticketPaging.limit), page: String(ticketPaging.page) });

  if (filter.elements.queue.value) query.set('queue', filter.elements.queue.value);
  if (filter.elements.q.value.trim()) query.set('q', filter.elements.q.value.trim());
  if (filter.elements.unfinished.checked) query.set('unfinished', 'true');
  if (filter.elements.mine.checked) query.set('owner', 'me');

  const page = await api(`/tickets?${query}`);
  ticketPaging.total = page?.totalCount ?? 0;

  const byId = new Map(cache.ticketQueues.map((queue) => [queue.id, queue]));

  fillTable(tbody, (page?.items ?? []).map((ticket) => ticketRow(ticket, byId)), 8,
    t('ticket.empty', 'No tickets.'));

  drawTicketPager();
}

/** One ticket as a row of the list. */
function ticketRow(ticket, byId) {
  const state = cache.states.find((candidate) => candidate.id === ticket.stateId);
  const priority = cache.priorities.find((candidate) => candidate.id === ticket.priorityId);
  const queue = byId.get(ticket.queueId);

  return el('tr', {},
    el('td', { class: 'text' },
      el('button', { class: 'link', text: ticket.number, onclick: () => openTicket(ticket.id) })),
    el('td', { class: 'text' },
      ticket.escalated ? el('span', { class: 'escalated', text: t('sla.overdue', 'overdue') }) : null,
      ticket.title),
    el('td', { class: 'text', text: ticket.contactName || ticket.organisationName || '–' }),
    el('td', { class: 'text', text: queue ? queuePath(queue, byId) : `#${ticket.queueId}` }),
    el('td', {}, state ? statusBadge(state.name, state.type) : el('span', { text: `#${ticket.stateId}` })),
    el('td', { class: 'text', text: priority ? priorityName(priority.name) : '' }),
    el('td', { class: 'text', text: ticket.ownerName || '–' }),
    el('td', { text: fmtMoment(ticket.updatedAt) }),
  );
}

/** Where the list stands, and the way to the pages either side. */
function drawTicketPager() {
  const { page, limit, total } = ticketPaging;
  const from = total === 0 ? 0 : (page - 1) * limit + 1;
  const to = Math.min(page * limit, total);

  $('#tickets-range').textContent = fillIn(t('ticket.range', '{0}–{1} of {2}'), [from, to, total]);
  $('#tickets-previous').disabled = page <= 1;
  $('#tickets-next').disabled = to >= total;
}

/**
 * Opens a ticket: what it is, what was written in it and what happened to it,
 * and the form holding it for correction.
 *
 * A ticket this reader cannot reach any more - moved, or the role changed - is
 * said to be gone, which is what the server answers, and the list is read again.
 */
async function openTicket(id, { keepPlace = false } = {}) {
  let ticket;
  let articles;
  let history;
  let assignees;

  let templates;

  try {
    [ticket, articles, history, templates] = await Promise.all([
      api(`/tickets/${id}`), api(`/tickets/${id}/articles`), api(`/tickets/${id}/history`),
      // Asked only of whoever may write in tickets, the right the server hands
      // them out to; anybody else would turn the whole ticket into a refusal.
      can('tickets:write') ? api(`/tickets/${id}/templates`) : { items: [] },
    ]);

    assignees = (await api(`/tickets/assignees?queue=${ticket.queueId}`))?.items ?? [];
  } catch (err) {
    toastFailure(err);
    closeTicket();
    await loadTicketPage();

    return;
  }

  openTicketShown = ticket;
  drawTicketDetail(ticket, articles?.items ?? [], history?.items ?? []);
  offerTemplates(templates?.items ?? []);

  // The items and the changes a ticket concerns, for whoever may read either; a
  // link is made where the reader works the ticket, as any change to it is.
  const linked = $('#ticket-links');
  linked.hidden = !can('assets:read', 'changes:read');
  if (!linked.hidden) {
    offerLinkKinds($('#form-ticket-link'));
    await drawLinks(linked, 'ticket', ticket.id, worksIn(ticket.queueId) && can('tickets:write'));
  }

  // Merged only where it is worked, and only once.
  const merging = $('#form-ticket-merge');
  merging.hidden = !worksIn(ticket.queueId) || !can('tickets:write') || Boolean(ticket.mergedInto);
  forgetMergeChoice();

  // Corrected only where the reader's role works in its queue: the server
  // answers any change to a ticket of a queue only read as not found, and a form
  // that offers what the server refuses teaches somebody the screen is broken.
  const form = $('#form-ticket');
  if (form && worksIn(ticket.queueId)) {
    if (!beingEdited(form) || Number(form.elements.id.value) !== ticket.id) editTicket(form, ticket, assignees);
  } else if (form?.elements.id.value) {
    resetTicketForm();
  }

  const detail = $('#ticket-detail');
  detail.hidden = false;

  if (!keepPlace) detail.scrollIntoView({ block: 'start', behavior: 'smooth' });
}

/** Whether the reader's role works in the queue - creates and changes its tickets. */
function worksIn(queueID) {
  return cache.ticketQueues.find((queue) => queue.id === queueID)?.reach === 'write';
}

/**
 * Shows a control of the open ticket where the reader works in its queue and
 * holds the right the control names, and hides it otherwise.
 */
function showWhereWorked(node, works) {
  node.hidden = !works || (node.dataset.perm ? !can(...node.dataset.perm.split(',')) : false);
}

/** Closes the ticket that is open and puts the form back to opening one. */
function closeTicket() {
  forgetTheOpenTicket();
  resetTicketForm();
}

/**
 * Forgets which ticket is open and empties its card - what was written in it,
 * what happened to it and the button that deletes it - rather than only hiding
 * the card. The card is not a table and the memory is not a form, so nothing
 * else on the way out of a session reaches either: a sign-out left the ticket in
 * the document under the sign-in screen, and opened it again for whoever signed
 * in next.
 */
function forgetTheOpenTicket() {
  openTicketShown = null;

  const card = $('#ticket-detail');
  if (!card) return;

  card.hidden = true;
  for (const part of ['#ticket-heading', '#ticket-facts', '#ticket-delete-slot', '#ticket-articles', '#ticket-history-list']) {
    $(part)?.replaceChildren();
  }

  forgetLinks($('#ticket-links'));
  forgetMergeChoice();
}

/** Empties the merge form's search and its choices, for a ticket opened or closed. */
function forgetMergeChoice() {
  const form = $('#form-ticket-merge');

  form.reset();
  form.elements.intoId.replaceChildren(el('option', { value: '', text: '–' }));
}

/**
 * Wires the merge form: a search for the ticket to merge into, which fills the
 * choice with tickets not merged themselves, and the merge once it is
 * confirmed - after which the ticket it went into is opened, holding both.
 */
function wireTicketMerge() {
  const form = $('#form-ticket-merge');
  let pending;

  const offer = async () => {
    const query = new URLSearchParams({ q: form.elements.find.value.trim(), limit: String(SEARCH_OFFERS) });
    const found = (await api(`/tickets?${query}`))?.items ?? [];

    form.elements.intoId.replaceChildren(el('option', { value: '', text: '–' }),
      ...found
        .filter((ticket) => ticket.id !== openTicketShown?.id && !ticket.mergedInto)
        .map((ticket) => el('option', { value: String(ticket.id), text: `${ticket.number} ${ticket.title}` })));
  };

  form.elements.find.addEventListener('input', () => {
    clearTimeout(pending);
    pending = setTimeout(() => offer().catch((err) => toastFailure(err)), 250);
  });

  form.addEventListener('submit', async (e) => {
    e.preventDefault();

    const ticket = openTicketShown;
    const into = Number(form.elements.intoId.value) || 0;
    if (!ticket || !into) return;

    const sure = await confirmDialog({
      title: t('ticket.mergeConfirmTitle', 'Merge the ticket?'),
      text: fillIn(t('ticket.mergeConfirmText',
        '{0} goes into {1}: its articles, the time noted on it and its watchers go there, and it stays merged.'),
      [ticket.number, form.elements.intoId.selectedOptions[0]?.textContent ?? '']),
      confirmLabel: t('ticket.mergeConfirm', 'Merge it'),
    });

    if (!sure) return;

    await mutate(() => api(`/tickets/${ticket.id}/merge`, { method: 'POST', body: JSON.stringify({ intoId: into }) }),
      t('msg.ticketMerged', 'Ticket merged'),
      async () => {
        await openTicket(into);
        await loadTicketPage();
      });
  });
}

/** What a ticket is, what was written in it and what happened to it. */
function drawTicketDetail(ticket, articles, history) {
  const byId = new Map(cache.ticketQueues.map((queue) => [queue.id, queue]));
  const state = cache.states.find((candidate) => candidate.id === ticket.stateId);
  const priority = cache.priorities.find((candidate) => candidate.id === ticket.priorityId);
  const type = cache.types.find((candidate) => candidate.id === ticket.typeId);
  const queue = byId.get(ticket.queueId);

  $('#ticket-heading').textContent = `${ticketName(ticket.number)} – ${ticket.title}`;

  const fact = (term, ...description) => [el('dt', { text: term }), el('dd', {}, ...description)];
  const facts = [
    ...fact(t('field.queue', 'Queue'), queue ? queuePath(queue, byId) : `#${ticket.queueId}`),
    ...fact(t('field.state', 'State'), state ? statusBadge(state.name, state.type) : `#${ticket.stateId}`),
    ...fact(t('field.priority', 'Priority'), priority ? priorityName(priority.name) : ''),
    ...fact(t('field.type', 'Type'), type ? ticketTypeName(type.name) : ''),
    ...fact(t('field.serviceLevelId', 'Service level'), levelName(ticket.serviceLevelId),
      ticket.escalated ? el('span', { class: 'escalated', text: t('sla.overdue', 'overdue') }) : null),
    ...(ticket.firstResponseDue ? fact(t('sla.firstResponseDue', 'First answer owed by'),
      ticket.firstResponseAt
        ? fillIn(t('sla.answeredAt', '{0} - answered {1}'), [fmtMoment(ticket.firstResponseDue), fmtMoment(ticket.firstResponseAt)])
        : fmtMoment(ticket.firstResponseDue)) : []),
    ...(ticket.updateDue ? fact(t('sla.updateDue', 'Next answer owed by'), fmtMoment(ticket.updateDue)) : []),
    ...(ticket.solutionDue ? fact(t('sla.solutionDue', 'Solution owed by'), fmtMoment(ticket.solutionDue)) : []),
    ...fact(t('ticket.owner', 'Owner'), ticket.ownerName || t('ticket.nobody', 'nobody')),
    ...fact(t('field.responsibleId', 'Responsible'), ticket.responsibleName || t('ticket.nobody', 'nobody')),
    ...fact(t('field.contactId', 'Contact'), ticket.contactName || t('ticket.nobody', 'nobody')),
    ...fact(t('field.organisationId', 'Organisation'), ticket.organisationName || t('ticket.nobody', 'nobody')),
    ...fact(t('ticket.locked', 'Locked'), ticket.locked ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no')),
    ...(ticket.pendingUntil ? fact(t('field.pendingUntil', 'Pending until'), fmtMoment(ticket.pendingUntil)) : []),
    ...(ticket.mergedInto ? fact(t('ticket.mergedInto', 'Merged into'), el('button', {
      type: 'button',
      class: 'link',
      text: t('ticket.openMergedInto', 'the ticket it went into'),
      onclick: () => openTicket(ticket.mergedInto).catch((err) => toastFailure(err)),
    })) : []),
    ...fact(t('ticket.opened', 'Opened'),
      `${fmtMoment(ticket.createdAt)}${ticket.createdByName ? ` – ${ticket.createdByName}` : ''}`),
    ...fact(t('ticket.accounted', 'Time noted'),
      fillIn(t('ticket.minutes', '{0} minutes'), [ticket.accountedMinutes ?? 0])),
  ];

  $('#ticket-facts').replaceChildren(...facts);

  const works = worksIn(ticket.queueId);

  for (const node of [$('#ticket-lock'), $('#form-article'), $('#ticket-delete-slot')]) {
    showWhereWorked(node, works);
  }

  const lock = $('#ticket-lock');
  lock.textContent = ticket.locked ? t('ticket.unlock', 'Unlock') : t('ticket.lockIt', 'Lock');
  lock.onclick = () => changeOpenTicket({ locked: !ticket.locked });

  const watch = $('#ticket-watch');
  watch.textContent = ticket.watching ? t('ticket.unwatch', 'Stop watching') : t('ticket.watchIt', 'Watch');
  watch.onclick = () => mutate(
    () => api(`/tickets/${ticket.id}/watch`, { method: ticket.watching ? 'DELETE' : 'PUT' }),
    ticket.watching ? t('msg.ticketUnwatched', 'No longer watching') : t('msg.ticketWatched', 'Watching'),
    () => openTicket(ticket.id, { keepPlace: true }),
  );

  $('#ticket-delete-slot').replaceChildren(deleteButton({
    label: ticketName(ticket.number),
    path: `/tickets/${ticket.id}`,
    message: t('msg.ticketDeleted', 'Ticket deleted'),
    after: async () => { closeTicket(); await refreshAll(); },
  }));

  $('#ticket-articles').replaceChildren(...articles.map((article) => el('li', {},
    el('p', { class: 'muted', text: [
      article.authorName || '–',
      channelName(article.channel),
      fmtMoment(article.createdAt),
      article.customerVisible ? t('ticket.seenByCustomer', 'the customer sees it') : t('ticket.internal', 'internal'),
    ].join(' · ') }),
    article.subject ? el('strong', { text: article.subject }) : null,
    el('div', { class: 'ticket-article-body', text: article.body }),
    article.attachments?.length ? el('ul', { class: 'ticket-attachments' }, ...article.attachments.map((file) => el('li', {},
      el('button', {
        type: 'button',
        class: 'link',
        text: `${file.filename} (${fileSize(file.size)})`,
        onclick: () => downloadAttachment(ticket.id, file).catch((err) => toastFailure(err)),
      })))) : null,
  )));

  $('#ticket-history-list').replaceChildren(...history.map((entry) => el('li', {
    text: `${fmtMoment(entry.at)} ${entry.actor === 'system' ? t('history.bySystem', 'the system') : (entry.actorName || '–')}: ${describeHistory(entry)}`,
  })));
}

/** One line of a ticket's history, in the reader's words. */
function describeHistory(entry) {
  const word = {
    state: (name) => (name ? statusName(name) : ''),
    priority: (name) => (name ? priorityName(name) : ''),
    type: (name) => (name ? ticketTypeName(name) : ''),
    owner: (name) => name || t('ticket.nobody', 'nobody'),
    responsible: (name) => name || t('ticket.nobody', 'nobody'),
    contact: (name) => name || t('ticket.nobody', 'nobody'),
    organisation: (name) => name || t('ticket.nobody', 'nobody'),
    lock: (value) => (value === 'true' ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no')),
    pending: (value) => (value ? fmtMoment(value) : t('ticket.notWaiting', 'not waiting')),
    article: (value) => channelName(value),
  }[entry.event] ?? ((value) => value);

  const sentence = HISTORY_SENTENCES[entry.event];

  return sentence ? fillIn(sentence(), [word(entry.oldValue), word(entry.newValue)]) : entry.event;
}

/** Changes what the request says about the ticket that is open, and reads it again. */
function changeOpenTicket(change) {
  if (!openTicketShown) return;

  const id = openTicketShown.id;

  mutate(
    () => api(`/tickets/${id}`, { method: 'PUT', body: JSON.stringify(change) }),
    t('msg.ticketSaved', 'Ticket saved'),
    async () => { await openTicket(id, { keepPlace: true }); await loadTicketPage(); },
  );
}

/**
 * Fills the ticket form's choices. Opening a ticket is offered the queues the
 * reader works in and the values still offered; correcting one keeps what the
 * ticket already carries among them, retired or not, because it may stay.
 */
function fillTicketForm(form, ticket, assignees) {
  const offered = (items, current) => items.filter((item) => item.valid || item.id === current);

  fillLevelChoices(form.elements.serviceLevelId, ticket?.serviceLevelId ?? null,
    ticket ? t('sla.none', 'none') : t('sla.asQueue', 'as the queue says'));
  const working = cache.ticketQueues.filter((queue) =>
    (queue.reach === 'write' && queue.valid) || queue.id === ticket?.queueId);

  fillSelect(form.elements.queueId, queueChoices(working), { labelKey: 'label', valueKey: 'value' });

  fillSelect(form.elements.typeId,
    offered(cache.types, ticket?.typeId).map((type) => ({ value: String(type.id), label: ticketTypeName(type.name) })),
    { labelKey: 'label', valueKey: 'value' });

  fillSelect(form.elements.priorityId,
    offered(cache.priorities, ticket?.priorityId)
      .map((priority) => ({ value: String(priority.id), label: priorityName(priority.name) })),
    { labelKey: 'label', valueKey: 'value' });

  fillSelect(form.elements.stateId,
    offered(cache.states, ticket?.stateId)
      .filter((state) => state.type !== 'merged' || state.id === ticket?.stateId)
      .map((state) => ({ value: String(state.id), label: statusName(state.name) })),
    { labelKey: 'label', valueKey: 'value' });

  fillSelect(form.elements.channel,
    AGENT_CHANNELS.map((channel) => ({ value: channel, label: channelName(channel) })),
    { labelKey: 'label', valueKey: 'value' });

  for (const [field, current, name] of [
    ['ownerId', ticket?.ownerId, ticket?.ownerName],
    ['responsibleId', ticket?.responsibleId, ticket?.responsibleName],
  ]) {
    const people = assignees.map((person) => ({ value: String(person.id), label: person.name }));
    if (current && !people.some((person) => person.value === String(current))) {
      people.push({ value: String(current), label: name || `#${current}` });
    }

    fillSelect(form.elements[field], people,
      { placeholder: t('ticket.nobody', 'nobody'), labelKey: 'label', valueKey: 'value' });
  }

  offerFound('ticketContact', keptEntry(ticket?.contactId, ticket?.contactName));
  offerFound('ticketOrganisation', keptEntry(ticket?.organisationId, ticket?.organisationName));

  if (!ticket) {
    const shipped = (items, name) => items.find((item) => item.name === name)?.id;

    form.elements.typeId.value = String(shipped(cache.types, 'unclassified') ?? form.elements.typeId.value);
    form.elements.priorityId.value = String(shipped(cache.priorities, 'normal') ?? form.elements.priorityId.value);
  }
}

/**
 * Shows the fields of one of the form's two jobs. The text a ticket is opened
 * with goes with the opening: a hidden control that is still required cannot be
 * submitted past and cannot be scrolled to, so it stops being required with it.
 */
function showTicketJob(form, correcting) {
  const customers = can('customers:read');

  for (const label of $$('.ticket-new-only', form)) label.hidden = correcting;
  for (const label of $$('.ticket-edit-only', form)) label.hidden = !correcting;

  // Who a ticket is for, where the reader may read customers - decided here
  // with the job rather than by data-perm, which would show the organisation
  // again on every reload whichever job the form is doing.
  for (const label of $$('.ticket-customer', form)) {
    label.hidden = !customers || (label.classList.contains('ticket-edit-only') && !correcting);
  }

  form.elements.body.required = !correcting;
}

/** Puts a ticket into the form, to correct it. */
function editTicket(form, ticket, assignees) {
  fillTicketForm(form, ticket, assignees);
  showTicketJob(form, true);

  form.elements.id.value = String(ticket.id);
  form.elements.title.value = ticket.title;
  form.elements.queueId.value = String(ticket.queueId);
  form.elements.typeId.value = String(ticket.typeId);
  form.elements.priorityId.value = String(ticket.priorityId);
  form.elements.stateId.value = String(ticket.stateId);
  form.elements.ownerId.value = ticket.ownerId ? String(ticket.ownerId) : '';
  form.elements.responsibleId.value = ticket.responsibleId ? String(ticket.responsibleId) : '';

  const waiting = ticket.pendingUntil ? dayAndTimeIn(ticket.pendingUntil, readerZone()) : { day: '', time: '' };
  setDateField(form.elements.pendingDay, waiting.day);
  form.elements.pendingTime.value = waiting.time;

  // Drawn rather than declared with a key, because it carries the ticket's
  // number; see editRole for why the key has to come off.
  const heading = $('#ticket-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('ticketFormTitle', () => {
    heading.textContent = t('ticket.edit', 'Correct {0}').replace('{0}', ticketName(ticket.number));
  });

  swapTheLabel($('#ticket-submit'), 'action.save', 'Save');
  $('#ticket-cancel').hidden = false;
}

/** Puts the form back to opening a ticket. */
function resetTicketForm() {
  const form = $('#form-ticket');
  if (!form) return;

  form.reset();
  form.elements.id.value = '';
  setDateField(form.elements.pendingDay, '');

  fillTicketForm(form, null, []);
  showTicketJob(form, false);

  stopRedrawing('ticketFormTitle');
  swapTheLabel($('#ticket-form-title'), 'ticket.create', 'Open a ticket');
  swapTheLabel($('#ticket-submit'), 'ticket.open', 'Open the ticket');
  $('#ticket-cancel').hidden = true;
}

/**
 * Sends the ticket form: a new ticket with the text it starts from, or the
 * correction of the one that is open.
 *
 * A correction says nobody out loud, with clearOwner and clearResponsible, since
 * an id sent as nothing cannot be told apart from one not sent. A moment to wait
 * for goes only with a state that waits: the server refuses one for any other,
 * rather than quietly dropping it.
 */
function saveTicket(form) {
  const id = form.elements.id.value;
  const value = (name) => (form.elements[name].value ? Number(form.elements[name].value) : undefined);

  if (!id) {
    const body = JSON.stringify({
      title: form.elements.title.value.trim(),
      queueId: value('queueId'),
      typeId: value('typeId'),
      priorityId: value('priorityId'),
      channel: form.elements.channel.value,
      customerVisible: form.elements.customerVisible.checked,
      body: form.elements.body.value,
      minutes: Number(form.elements.minutes.value) || 0,
      ...(can('customers:read') && value('contactId') ? { contactId: value('contactId') } : {}),
      ...(value('serviceLevelId') ? { serviceLevelId: value('serviceLevelId') } : {}),
    });

    mutate(() => api('/tickets', { method: 'POST', body }), t('msg.ticketOpened', 'Ticket opened'),
      async (opened) => {
        resetTicketForm();
        await loadTicketPage();
        if (opened?.id) await openTicket(opened.id);
      });

    return;
  }

  const state = cache.states.find((candidate) => candidate.id === value('stateId'));
  const waits = state && (state.type === 'pending-reminder' || state.type === 'pending-auto');
  const day = form.elements.pendingDay.value;

  const change = {
    title: form.elements.title.value.trim(),
    queueId: value('queueId'),
    typeId: value('typeId'),
    priorityId: value('priorityId'),
    stateId: value('stateId'),
    ...(value('ownerId') ? { ownerId: value('ownerId') } : { clearOwner: true }),
    ...(value('responsibleId') ? { responsibleId: value('responsibleId') } : { clearResponsible: true }),
    ...(waits && day ? { pendingUntil: momentIn(day, form.elements.pendingTime.value, readerZone()) } : {}),
    ...(can('customers:read') ? customerChange(form, openTicketShown) : {}),
    ...(value('serviceLevelId') ? { serviceLevelId: value('serviceLevelId') } : { clearServiceLevel: true }),
  };

  // saveForm rather than mutate: a corrected ticket stays in the form, unlike a
  // record any other form saves, so nothing resets the form after the save - and
  // a form still protected went on showing what it was saved with while locking
  // the ticket made somebody its owner.
  saveForm(form, () => api(`/tickets/${id}`, { method: 'PUT', body: JSON.stringify(change) }),
    t('msg.ticketSaved', 'Ticket saved'),
    async () => { await openTicket(Number(id), { keepPlace: true }); await loadTicketPage(); });
}

/**
 * What a correction says about who the ticket is for: only what changed, so a
 * contact newly chosen brings its organisation along - the organisation is sent
 * only when it was changed itself - and nobody said out loud.
 */
function customerChange(form, ticket) {
  const change = {};
  const contact = form.elements.contactId.value ? Number(form.elements.contactId.value) : null;
  const organisation = form.elements.organisationId.value ? Number(form.elements.organisationId.value) : null;

  if (contact !== (ticket?.contactId ?? null)) {
    if (contact) change.contactId = contact;
    else change.clearContact = true;
  }

  if (organisation !== (ticket?.organisationId ?? null)) {
    if (organisation) change.organisationId = organisation;
    else change.clearOrganisation = true;
  }

  return change;
}

/** Writes the article form's text in the ticket that is open. */
function saveArticle(form) {
  if (!openTicketShown) return;

  const id = openTicketShown.id;
  const files = [...(form.elements.files.files ?? [])];
  const body = JSON.stringify({
    channel: form.elements.channel.value,
    customerVisible: form.elements.customerVisible.checked,
    subject: form.elements.subject.value.trim(),
    body: form.elements.body.value,
    minutes: Number(form.elements.minutes.value) || 0,
  });

  // The files go once the article is written, after the form is emptied: a file
  // refused then cannot leave the text behind for a second press to write again.
  mutate(() => api(`/tickets/${id}/articles`, { method: 'POST', body }), t('msg.articleWritten', 'Written'),
    async (article) => {
      form.reset();
      await attachFiles(id, article?.id, files);
      await openTicket(id, { keepPlace: true });
      await loadTicketPage();
    });
}

/**
 * Attaches the files chosen with an article that has been written, one after
 * another: each is a request bounded on its own, and a file refused is named,
 * while the rest still go.
 */
async function attachFiles(ticketID, articleID, files, area = 'tickets') {
  if (!articleID) return;

  for (const file of files) {
    try {
      await uploadAttachment(ticketID, articleID, file, area);
    } catch (err) {
      toastFailure(err, fillIn(t('ticket.fileRefused', '{0} was not attached.'), [file.name]));
    }
  }
}

/**
 * Sends one file for an article. Not api(), which sends JSON: only the browser
 * knows the multipart boundary it generated, so the request carries no
 * Content-Type of its own.
 */
async function uploadAttachment(ticketID, articleID, file, area = 'tickets') {
  const form = new FormData();
  form.append('file', file);

  const res = await reach(`${API}/${area}/${ticketID}/articles/${articleID}/attachments`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'X-CSRF-Token': readCookie('qubitdesk_csrf') },
    body: form,
  });

  noticePermissionChange(res.headers.get('X-Permissions-Revision'));

  if (res.ok) return;

  let refusal = null;
  try {
    refusal = await res.json();
  } catch {
    // An error page rather than JSON; the status carries the meaning.
  }

  throw refusalFrom(res, refusal);
}

/** Fetches a file of a ticket and saves it under the name it came with. */
function downloadAttachment(ticketID, file, area = 'tickets') {
  return saveDownload(`${API}/${area}/${ticketID}/attachments/${file.id}`, file.filename);
}

/**
 * A file's size the way the reader writes a number: in kilobytes, and in
 * megabytes from one upwards.
 */
function fileSize(bytes) {
  const mega = bytes >= 1024 * 1024;

  return new Intl.NumberFormat(activeLocale(), {
    style: 'unit', unit: mega ? 'megabyte' : 'kilobyte', maximumFractionDigits: 1,
  }).format(bytes / (mega ? 1024 * 1024 : 1024));
}

/** Wires the ticket screens' forms and buttons. */
function wireTickets() {
  const filter = $('#form-ticket-filter');

  const refilter = () => {
    ticketPaging.page = 1;
    loadTicketPage();
  };

  filter.addEventListener('submit', (e) => { e.preventDefault(); refilter(); });
  filter.addEventListener('change', refilter);

  let typing;
  filter.elements.q.addEventListener('input', () => {
    clearTimeout(typing);
    typing = setTimeout(refilter, 300);
  });

  $('#tickets-previous').addEventListener('click', () => {
    ticketPaging.page = Math.max(1, ticketPaging.page - 1);
    loadTicketPage();
  });

  $('#tickets-next').addEventListener('click', () => {
    ticketPaging.page += 1;
    loadTicketPage();
  });

  $('#ticket-close').addEventListener('click', closeTicket);
  // Back to what the open ticket says: the form is reset first, which ends its
  // being edited, so that reading the ticket again is allowed to fill it.
  $('#ticket-cancel').addEventListener('click', () => {
    if (!openTicketShown) {
      resetTicketForm();

      return;
    }

    $('#form-ticket').reset();
    openTicket(openTicketShown.id, { keepPlace: true });
  });

  $('#form-ticket').addEventListener('submit', (e) => { e.preventDefault(); saveTicket(e.target); });

  const article = $('#form-article');
  fillSelect(article.elements.channel,
    AGENT_CHANNELS.map((channel) => ({ value: channel, label: channelName(channel) })),
    { labelKey: 'label', valueKey: 'value' });
  article.addEventListener('submit', (e) => { e.preventDefault(); saveArticle(e.target); });

  // What was chosen, beside the chooser that hides the browser's own words.
  const chosen = () => {
    $('#article-files').textContent = [...article.elements.files.files].map((file) => file.name).join(', ');
  };

  article.elements.files.addEventListener('change', chosen);
  article.addEventListener('reset', () => setTimeout(chosen));
}

// ------------------------------------------------- what tickets are sorted by

/** The English for the states a ticket ships with, by the identifier each is stored under. */
const SHIPPED_STATE_TITLES = {
  new: 'new',
  open: 'open',
  'pending-reminder': 'pending reminder',
  'pending-auto-close': 'pending auto close',
  closed: 'closed',
  removed: 'removed',
  merged: 'merged',
};

/** The English for the priorities that ship, as for the states. */
const SHIPPED_PRIORITY_TITLES = {
  'very-high': 'very high',
  high: 'high',
  normal: 'normal',
  low: 'low',
  'very-low': 'very low',
};

/** The English for the ticket types that ship, as for the states. */
const SHIPPED_TYPE_TITLES = {
  unclassified: 'unclassified',
  incident: 'incident',
  'service-request': 'service request',
};

/**
 * The English for the types a state can have, which are the server's
 * model.StateTypes: what the code does with a ticket in such a state. The type
 * field offers exactly these, so a state the server would refuse cannot be
 * chosen.
 */
const STATE_TYPE_TITLES = {
  new: 'new',
  open: 'open',
  'pending-reminder': 'pending, with a reminder',
  'pending-auto': 'pending, then moving by itself',
  closed: 'closed',
  removed: 'removed',
  merged: 'merged',
};

/** What a priority is called, in the reader's language, as statusName says for a state. */
function priorityName(name) {
  return t(`priority.${name}`, SHIPPED_PRIORITY_TITLES[name] ?? name);
}

/** What a ticket type is called, in the reader's language, as statusName says for a state. */
function ticketTypeName(name) {
  return t(`tickettype.${name}`, SHIPPED_TYPE_TITLES[name] ?? name);
}

/** What a state's type is called, in the reader's language. */
function stateTypeName(type) {
  return t(`statetype.${type}`, STATE_TYPE_TITLES[type] ?? type);
}

/**
 * The three cards of the ticket setup screen - states, priorities, types - each a
 * table and one form that makes a value or corrects one.
 *
 * What differs between them is one column and one field - a state's type, a
 * priority's level - and their words; the rest is the same and lives here once.
 * A shipped value the code relies on (isSystem) is shown and not offered for
 * editing or deleting, because the server refuses both: a button that is always
 * refused teaches somebody the screen is broken.
 */
const CATALOGS = {
  state: {
    path: '/ticket-states',
    columns: 5,
    create: ['catalog.stateCreate', 'Add a state'],
    title: (name) => statusName(name),
    editing: (name) => t('catalog.stateEdit', 'Edit the state {0}').replace('{0}', name),
    kind: () => t('field.state', 'State'),
    empty: () => t('catalog.statesEmpty', 'No states.'),
    created: () => t('msg.stateCreated', 'State created'),
    saved: () => t('msg.stateSaved', 'State saved'),
    deleted: () => t('msg.stateDeleted', 'State deleted'),
    cells: (entry) => [el('td', { class: 'text', text: stateTypeAndNext(entry) })],
    fill: (form, entry) => {
      form.elements.type.value = entry.type;
      form.elements.nextStateId.value = entry.nextStateId ? String(entry.nextStateId) : '';
      showNextState(form);
    },
    sent: (form) => (form.elements.type.value === 'pending-auto'
      ? { type: 'pending-auto', nextStateId: Number(form.elements.nextStateId.value) }
      : { type: form.elements.type.value }),
    reset: (form) => showNextState(form),
  },
  priority: {
    path: '/ticket-priorities',
    columns: 5,
    create: ['catalog.priorityCreate', 'Add a priority'],
    title: (name) => priorityName(name),
    editing: (name) => t('catalog.priorityEdit', 'Edit the priority {0}').replace('{0}', name),
    kind: () => t('field.priority', 'Priority'),
    empty: () => t('catalog.prioritiesEmpty', 'No priorities.'),
    created: () => t('msg.priorityCreated', 'Priority created'),
    saved: () => t('msg.prioritySaved', 'Priority saved'),
    deleted: () => t('msg.priorityDeleted', 'Priority deleted'),
    cells: (entry) => [el('td', { class: 'num', text: String(entry.level) })],
    fill: (form, entry) => { form.elements.level.value = String(entry.level); },
    sent: (form) => ({ level: Number(form.elements.level.value) }),
    reset: () => {},
  },
  type: {
    path: '/ticket-types',
    columns: 4,
    create: ['catalog.typeCreate', 'Add a type'],
    title: (name) => ticketTypeName(name),
    editing: (name) => t('catalog.typeEdit', 'Edit the type {0}').replace('{0}', name),
    kind: () => t('field.type', 'Type'),
    empty: () => t('catalog.typesEmpty', 'No types.'),
    created: () => t('msg.typeCreated', 'Type created'),
    saved: () => t('msg.typeSaved', 'Type saved'),
    deleted: () => t('msg.typeDeleted', 'Type deleted'),
    cells: () => [],
    fill: () => {},
    sent: () => ({}),
    reset: () => {},
  },
};

/**
 * Fills the ticket setup screen, for whoever may set it up. The three lists are
 * read by anybody who works tickets as well, but this screen is where they are
 * changed, and it is behind the same right.
 */
async function loadTicketSetup() {
  if (!can('queues:write')) return;

  const [queues, states, priorities, types, levels, unlocking] = await Promise.all(
    ['/queues', CATALOGS.state.path, CATALOGS.priority.path, CATALOGS.type.path, '/service-levels',
      '/unlock-state-types']
      .map((path) => api(path)),
  );

  cache.levels = levels?.items ?? [];
  loadQueueCard(queues?.items ?? []);

  const lists = { state: states?.items ?? [], priority: priorities?.items ?? [], type: types?.items ?? [] };

  // Not over a state somebody is part way through choosing a type for: this runs
  // on every reload of the screens, and refilling the select puts it back on its
  // first entry.
  const stateForm = $('#form-state');
  cache.ticketStates = lists.state;
  if (stateForm && !beingEdited(stateForm)) {
    fillSelect(stateForm.elements.type,
      Object.keys(STATE_TYPE_TITLES).map((type) => ({ value: type, label: stateTypeName(type) })),
      { labelKey: 'label', valueKey: 'value' });
    fillSelect(stateForm.elements.nextStateId,
      lists.state.filter(endsAWait).map((state) => ({ value: String(state.id), label: statusName(state.name) })),
      { labelKey: 'label', valueKey: 'value' });
    showNextState(stateForm);
  }

  for (const [kind, catalog] of Object.entries(CATALOGS)) {
    const tbody = $(`#table-${kind} tbody`);
    if (!tbody) continue;

    fillTable(tbody, lists[kind].map((entry) => catalogRow(kind, entry)), catalog.columns, catalog.empty());
  }

  drawUnlockTypes(unlocking?.items ?? []);
  await loadCalendars();
  loadSlaCard();
}

/**
 * Fills the communication screen, for whoever may set communication up: the
 * notifications and the answer templates, KIX18's communication area. What a
 * notification's criteria and a template's queue name is read through the
 * rules' own choices, which this right reads as the automation's does, so that
 * setting communication up takes no right to set the queues up.
 */
async function loadCommunication() {
  if (!can('communication:write')) return;

  const [notifying, templates, choices] = await Promise.all(
    ['/notification-definitions', '/response-templates', '/automation/choices'].map((path) => api(path)),
  );

  drawNotifications(notifying, {
    queues: choices?.queues ?? [], states: choices?.states ?? [], priorities: choices?.priorities ?? [],
    types: choices?.types ?? [],
  });
  drawTemplates(templates, choices?.queues ?? []);
}

/**
 * The state types a lock runs out in, one switch each. The shipped ones are on
 * and cannot be switched off, because the server refuses a list leaving them
 * out. Not redrawn over a choice somebody is part way through.
 */
function drawUnlockTypes(kept) {
  const form = $('#form-unlock-types');
  if (!form || beingEdited(form)) return;

  $('#unlock-types').replaceChildren(...Object.keys(STATE_TYPE_TITLES).map((type) => {
    const entry = kept.find((candidate) => candidate.type === type);

    return el('label', { class: `switch perm-switch${entry?.isSystem ? ' muted' : ''}` },
      el('input', {
        type: 'checkbox', name: 'types', value: type, checked: Boolean(entry), disabled: Boolean(entry?.isSystem),
      }),
      el('span', { class: 'switch-track' }),
      el('span', { class: 'perm-name', text: stateTypeName(type) }));
  }));
}

/** Wires the form that names the state types a lock runs out in. */
function wireUnlockTypes() {
  const form = $('#form-unlock-types');

  form.addEventListener('submit', (e) => {
    e.preventDefault();

    const types = [...form.querySelectorAll('input[name="types"]')]
      .filter((box) => box.checked).map((box) => box.value);

    mutate(
      () => api('/unlock-state-types', { method: 'PUT', body: JSON.stringify({ types }) }),
      t('msg.unlockTypesSaved', 'Saved where a lock runs out'),
      refreshAll,
    );
  });
}

/**
 * Whether a ticket whose wait is over may move on to a state - offered, not
 * waiting itself, not merged - as the server holds a waiting state's next state
 * to.
 */
function endsAWait(state) {
  return state.valid && !['pending-auto', 'pending-reminder', 'merged'].includes(state.type);
}

/**
 * Shows the choice of the state a waiting state moves a ticket on to only for
 * a state of that type, and makes it needed there: hidden, it must not be
 * required, or the form could not be sent and would not say why.
 */
function showNextState(form) {
  const waits = form.elements.type.value === 'pending-auto';

  $('#state-next').hidden = !waits;
  form.elements.nextStateId.required = waits;
}

/** A state's type, and for a waiting state the state it moves a ticket on to. */
function stateTypeAndNext(entry) {
  const next = (cache.ticketStates ?? []).find((state) => state.id === entry.nextStateId);

  return next ? `${stateTypeName(entry.type)} → ${statusName(next.name)}` : stateTypeName(entry.type);
}

/** One value as a row of its card's table. */
function catalogRow(kind, entry) {
  const catalog = CATALOGS[kind];
  const actions = el('td', { class: 'actions' });

  if (entry.isSystem) {
    actions.append(el('span', { class: 'muted', text: t('catalog.shipped', 'ships with the application') }));
  } else {
    actions.append(el('button', {
      class: 'link',
      text: t('action.edit', 'edit'),
      onclick: () => editCatalogEntry(kind, entry),
    }));

    actions.append(deleteButton({
      label: `${catalog.kind()} "${catalog.title(entry.name)}"`,
      path: `${catalog.path}/${entry.id}`,
      message: catalog.deleted(),
      after: refreshAll,
    }));
  }

  return el('tr', {},
    el('td', { class: 'text', text: catalog.title(entry.name) }),
    ...catalog.cells(entry),
    el('td', { class: 'text', text: entry.description || '–' }),
    el('td', { text: entry.valid ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no') }),
    actions,
  );
}

/** Opens a value in its card's form, to correct it. */
function editCatalogEntry(kind, entry) {
  const catalog = CATALOGS[kind];
  const form = $(`#form-${kind}`);

  form.elements.id.value = String(entry.id);
  form.elements.name.value = entry.name;
  form.elements.description.value = entry.description ?? '';
  form.elements.valid.checked = entry.valid;
  catalog.fill(form, entry);

  // Drawn rather than declared with a key, because it carries the value's name;
  // see editRole for why the key has to come off.
  const heading = $(`#${kind}-form-title`);
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable(`${kind}FormTitle`, () => {
    heading.textContent = catalog.editing(catalog.title(entry.name));
  });

  swapTheLabel($(`#${kind}-submit`), 'action.save', 'Save');
  $(`#${kind}-cancel`).hidden = false;

  form.elements.name.focus();
}

/** Puts a card's form back to making a new value. */
function resetCatalogForm(kind) {
  const catalog = CATALOGS[kind];
  const form = $(`#form-${kind}`);

  form.reset();
  form.elements.id.value = '';
  catalog.reset(form);

  stopRedrawing(`${kind}FormTitle`);
  swapTheLabel($(`#${kind}-form-title`), ...catalog.create);
  swapTheLabel($(`#${kind}-submit`), 'action.create', 'Create');
  $(`#${kind}-cancel`).hidden = true;
}

/** Sends a card's form: a new value, or the correction of the one it holds. */
function saveCatalogEntry(kind, form) {
  const catalog = CATALOGS[kind];
  const id = form.elements.id.value;
  const body = JSON.stringify({
    name: form.elements.name.value.trim(),
    description: form.elements.description.value.trim(),
    valid: form.elements.valid.checked,
    ...catalog.sent(form),
  });

  mutate(
    () => (id
      ? api(`${catalog.path}/${id}`, { method: 'PUT', body })
      : api(catalog.path, { method: 'POST', body })),
    id ? catalog.saved() : catalog.created(),
    async () => { resetCatalogForm(kind); await refreshAll(); },
  );
}

// ------------------------------------------------------------------ queues

/** The English for what an answer to a closed ticket does, which are the server's model.FollowUps. */
const FOLLOW_UP_TITLES = {
  reopen: 'opens it again',
  reject: 'is refused',
  'new-ticket': 'opens a new ticket',
};

/** The English for how far a role reaches into a queue, which are the server's model.AccessLevels. */
const ACCESS_TITLES = {
  read: 'reads its tickets',
  write: 'works in it',
};

/** What an answer to a closed ticket does, in the reader's language. */
function followUpName(followUp) {
  return t(`followup.${followUp}`, FOLLOW_UP_TITLES[followUp] ?? followUp);
}

/** How far a role reaches into a queue, in the reader's language. */
function accessName(level) {
  return t(`queueaccess.${level}`, ACCESS_TITLES[level] ?? level);
}

/**
 * A queue's place in the tree: its parents' names before its own.
 *
 * Stops at a queue it has passed, as the server's walk up the tree does, so a
 * tree somebody broke by hand in the database draws a name rather than hanging
 * the page.
 */
function queuePath(queue, byId) {
  const names = [queue.name];
  const passed = new Set([queue.id]);

  for (let at = byId.get(queue.parentId); at && !passed.has(at.id); at = byId.get(at.parentId)) {
    names.unshift(at.name);
    passed.add(at.id);
  }

  return names.join(' › ');
}

/** Whether a queue sits under another - or is it - however many levels down. */
function sitsUnder(queue, ancestor, byId) {
  const passed = new Set();

  for (let at = queue; at && !passed.has(at.id); at = byId.get(at.parentId)) {
    if (at.id === ancestor.id) return true;
    passed.add(at.id);
  }

  return false;
}

/** The name of the role with this id, as the role list has it. */
function roleNameOf(roleId) {
  return (cache.roles ?? []).find((role) => role.id === roleId)?.name ?? `#${roleId}`;
}

/**
 * Fills the queue card from the queues the screen just read.
 *
 * Not over a queue somebody is part way through: refilling the parent list and
 * the access would put a half-chosen form back to what it was.
 */
function loadQueueCard(queues) {
  cache.queues = queues;

  const byId = new Map(queues.map((queue) => [queue.id, queue]));
  const tbody = $('#table-queue tbody');

  if (tbody) {
    const rows = [...queues]
      .sort((a, b) => queuePath(a, byId).localeCompare(queuePath(b, byId)))
      .map((queue) => queueRow(queue, byId));

    fillTable(tbody, rows, 6, t('catalog.queuesEmpty', 'No queues.'));
  }

  const form = $('#form-queue');
  if (form && !beingEdited(form)) {
    fillFollowUps(form);
    fillParents(form, null);
    fillLevelChoices(form.elements.serviceLevelId, null, t('sla.none', 'none'));
    drawQueueAccess([]);
  }
}

/** One queue as a row of the queue card's table. */
function queueRow(queue, byId) {
  const access = (queue.access ?? [])
    .map((entry) => `${roleTitle(roleNameOf(entry.roleId))}: ${accessName(entry.level)}`)
    .join(', ');

  return el('tr', {},
    el('td', { class: 'text', text: queuePath(queue, byId) }),
    el('td', { class: 'text', text: followUpName(queue.followUp) }),
    el('td', {
      class: 'num',
      text: queue.unlockMinutes ? String(queue.unlockMinutes) : t('catalog.never', 'never'),
    }),
    el('td', { class: 'text', text: access || t('catalog.onlySetup', 'only whoever sets up queues') }),
    el('td', { text: queue.valid ? t('catalog.validYes', 'yes') : t('catalog.validNo', 'no') }),
    el('td', { class: 'actions' },
      el('button', { class: 'link', text: t('action.edit', 'edit'), onclick: () => editQueue(queue) }),
      deleteButton({
        label: `${t('field.queue', 'Queue')} "${queue.name}"`,
        path: `/queues/${queue.id}`,
        message: t('msg.queueDeleted', 'Queue deleted'),
        after: refreshAll,
      })),
  );
}

/** The three things an answer to a closed ticket can do, as the form's choices. */
function fillFollowUps(form) {
  fillSelect(form.elements.followUp,
    Object.keys(FOLLOW_UP_TITLES).map((followUp) => ({ value: followUp, label: followUpName(followUp) })),
    { labelKey: 'label', valueKey: 'value' });
}

/**
 * The queues a queue can sit under: every one but itself and those below it,
 * which the server refuses because the tree would loop.
 */
function fillParents(form, editing) {
  const byId = new Map(cache.queues.map((queue) => [queue.id, queue]));
  const choices = cache.queues
    .filter((queue) => !editing || !sitsUnder(queue, editing, byId))
    .map((queue) => ({ value: String(queue.id), label: queuePath(queue, byId) }))
    .sort((a, b) => a.label.localeCompare(b.label));

  fillSelect(form.elements.parentId, choices,
    { placeholder: t('catalog.topLevel', 'nothing - at the top'), labelKey: 'label', valueKey: 'value' });
}

/**
 * One choice per role of how far it reaches into the queue.
 *
 * Hidden without the roles, which only somebody who may read them has: the form
 * then sends no access at all, and the server leaves it as it is rather than
 * taking it away.
 */
function drawQueueAccess(access) {
  const fieldset = $('#queue-access');
  const list = $('#queue-access-list');
  if (!fieldset || !list) return;

  const roles = cache.roles ?? [];
  fieldset.hidden = roles.length === 0;

  list.replaceChildren(...roles.map((role) => {
    const select = el('select', { name: 'access', 'data-role': String(role.id) },
      el('option', { value: '', text: t('catalog.noAccess', 'no access') }),
      ...Object.keys(ACCESS_TITLES).map((level) => el('option', { value: level, text: accessName(level) })));

    select.value = access.find((entry) => entry.roleId === role.id)?.level ?? '';

    return el('label', {}, el('span', { text: roleTitle(role.name) }), select);
  }));
}

/** Opens a queue in the form, to correct it. */
function editQueue(queue) {
  const form = $('#form-queue');

  form.elements.id.value = String(queue.id);
  form.elements.name.value = queue.name;
  form.elements.description.value = queue.description ?? '';
  form.elements.unlockMinutes.value = String(queue.unlockMinutes);
  form.elements.valid.checked = queue.valid;
  form.elements.portal.checked = queue.portal;

  fillFollowUps(form);
  form.elements.followUp.value = queue.followUp;

  fillParents(form, queue);
  form.elements.parentId.value = queue.parentId ? String(queue.parentId) : '';
  fillLevelChoices(form.elements.serviceLevelId, queue.serviceLevelId, t('sla.none', 'none'));

  drawQueueAccess(queue.access ?? []);

  // Drawn rather than declared with a key, because it carries the queue's name;
  // see editRole for why the key has to come off.
  const heading = $('#queue-form-title');
  delete heading.dataset.i18n;
  delete heading.dataset.i18nSource;

  redrawable('queueFormTitle', () => {
    heading.textContent = t('catalog.queueEdit', 'Edit the queue {0}').replace('{0}', queue.name);
  });

  swapTheLabel($('#queue-submit'), 'action.save', 'Save');
  $('#queue-cancel').hidden = false;

  form.elements.name.focus();
}

/** Puts the queue form back to making a new queue. */
function resetQueueForm() {
  const form = $('#form-queue');

  form.reset();
  form.elements.id.value = '';

  fillFollowUps(form);
  fillParents(form, null);
  fillLevelChoices(form.elements.serviceLevelId, null, t('sla.none', 'none'));
  drawQueueAccess([]);

  stopRedrawing('queueFormTitle');
  swapTheLabel($('#queue-form-title'), 'catalog.queueCreate', 'Add a queue');
  swapTheLabel($('#queue-submit'), 'action.create', 'Create');
  $('#queue-cancel').hidden = true;
}

/** Sends the queue form: a new queue, or the correction of the one it holds. */
function saveQueue(form) {
  const id = form.elements.id.value;
  const parent = form.elements.parentId.value;
  const level = form.elements.serviceLevelId.value;

  const sent = {
    name: form.elements.name.value.trim(),
    description: form.elements.description.value.trim(),
    followUp: form.elements.followUp.value,
    unlockMinutes: Number(form.elements.unlockMinutes.value),
    valid: form.elements.valid.checked,
    portal: form.elements.portal.checked,

    // Said out loud when there is none: a parent sent as nothing cannot be told
    // apart from one that was not sent, and a correction would keep the old one.
    ...(parent ? { parentId: Number(parent) } : { clearParent: true }),
    ...(level ? { serviceLevelId: Number(level) } : { clearServiceLevel: true }),
  };

  if (!$('#queue-access').hidden) {
    sent.access = $$('#queue-access-list select', form)
      .filter((select) => select.value)
      .map((select) => ({ roleId: Number(select.dataset.role), level: select.value }));
  }

  const body = JSON.stringify(sent);

  mutate(
    () => (id ? api(`/queues/${id}`, { method: 'PUT', body }) : api('/queues', { method: 'POST', body })),
    id ? t('msg.queueSaved', 'Queue saved') : t('msg.queueCreated', 'Queue created'),
    async () => { resetQueueForm(); await refreshAll(); },
  );
}

// ---------------------------------------------------------- public holidays

/** A date as its calendar day in the browser's own zone, YYYY-MM-DD. */
const ISO_DAY = (d) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;

/**
 * The calendar day it is right now, in the zone that applies to this user.
 *
 * Not `new Date().toISOString().slice(0, 10)`, which is the day in UTC: for
 * anyone east of Greenwich that is yesterday for part of every evening, and for
 * anyone west it is tomorrow for part of every morning - the kind of error nobody
 * notices until a month-end figure is short.
 *
 * Intl is used rather than date arithmetic because it is the only thing in the
 * platform that knows about daylight saving.
 */
function todayISO() {
  const timeZone = me.user?.effectiveTimezone;

  if (!timeZone) {
    // Before sign-in there is no zone to apply, so the browser's own is the
    // best guess available - and still better than UTC.
    return ISO_DAY(new Date());
  }

  try {
    // en-CA renders as YYYY-MM-DD, which is the format the API expects.
    return new Intl.DateTimeFormat('en-CA', {
      timeZone, year: 'numeric', month: '2-digit', day: '2-digit',
    }).format(new Date());
  } catch {
    // A zone the browser does not know must not break the screen entirely.
    return ISO_DAY(new Date());
  }
}

/**
 * What each country public holidays can be kept in is called, in words.
 *
 * Each written out rather than looked up as country.${code}: the translation
 * checks read literal keys, and a key built at run time is one they cannot see
 * go missing.
 */
function countryName(code) {
  const names = {
    AT: () => t('country.AT', 'Austria'),
    BE: () => t('country.BE', 'Belgium'),
    CZ: () => t('country.CZ', 'Czechia'),
    DE: () => t('country.DE', 'Germany'),
    DK: () => t('country.DK', 'Denmark'),
    FI: () => t('country.FI', 'Finland'),
    FR: () => t('country.FR', 'France'),
    GB: () => t('country.GB', 'United Kingdom'),
    IE: () => t('country.IE', 'Ireland'),
    IT: () => t('country.IT', 'Italy'),
    LU: () => t('country.LU', 'Luxembourg'),
    NL: () => t('country.NL', 'Netherlands'),
    NO: () => t('country.NO', 'Norway'),
    PL: () => t('country.PL', 'Poland'),
    PT: () => t('country.PT', 'Portugal'),
    SE: () => t('country.SE', 'Sweden'),
    US: () => t('country.US', 'United States'),
  };

  return names[code]?.() ?? code;
}

/**
 * What each region that keeps holidays of its own is called, by its place -
 * the country's code and its own, DE-BY - written out for the same reason.
 */
function regionName(place) {
  const names = {
    'DE-BW': () => t('region.DE-BW', 'Baden-Wuerttemberg'),
    'DE-BY': () => t('region.DE-BY', 'Bavaria'),
    'DE-BE': () => t('region.DE-BE', 'Berlin'),
    'DE-BB': () => t('region.DE-BB', 'Brandenburg'),
    'DE-HB': () => t('region.DE-HB', 'Bremen'),
    'DE-HH': () => t('region.DE-HH', 'Hamburg'),
    'DE-HE': () => t('region.DE-HE', 'Hesse'),
    'DE-MV': () => t('region.DE-MV', 'Mecklenburg-Western Pomerania'),
    'DE-NI': () => t('region.DE-NI', 'Lower Saxony'),
    'DE-NW': () => t('region.DE-NW', 'North Rhine-Westphalia'),
    'DE-RP': () => t('region.DE-RP', 'Rhineland-Palatinate'),
    'DE-SL': () => t('region.DE-SL', 'Saarland'),
    'DE-SN': () => t('region.DE-SN', 'Saxony'),
    'DE-ST': () => t('region.DE-ST', 'Saxony-Anhalt'),
    'DE-SH': () => t('region.DE-SH', 'Schleswig-Holstein'),
    'DE-TH': () => t('region.DE-TH', 'Thuringia'),
    'FR-6AE': () => t('region.FR-6AE', 'Alsace'),
    'FR-57': () => t('region.FR-57', 'Moselle'),
    'GB-ENG': () => t('region.GB-ENG', 'England'),
    'GB-WLS': () => t('region.GB-WLS', 'Wales'),
    'GB-SCT': () => t('region.GB-SCT', 'Scotland'),
    'GB-NIR': () => t('region.GB-NIR', 'Northern Ireland'),
    'IT-BZ': () => t('region.IT-BZ', 'South Tyrol'),
    'PT-20': () => t('region.PT-20', 'Azores'),
    'PT-30': () => t('region.PT-30', 'Madeira'),
  };

  return names[place]?.() ?? place;
}

/** The countries public holidays can be kept in and their regions, as last loaded. */
let holidayCountries = [];

/**
 * Fills the holiday card's two pickers from a choice: the countries after
 * whatever comes before them, and the regions of the one chosen.
 *
 * emptyCountry is what an empty choice shows: Germany, where nobody has chosen
 * yet, with no state - which keeps none until one is saved.
 */
function fillHolidayPickers(form, choice, emptyCountry, ...before) {
  const locale = activeLocale();
  const countries = holidayCountries.map((country) => country.code)
    .sort((a, b) => countryName(a).localeCompare(countryName(b), locale));

  form.elements.country.replaceChildren(...before,
    ...countries.map((code) => el('option', { value: code, text: countryName(code) })));

  const [country, region] = choice ? choice.split('-') : [emptyCountry];

  form.elements.country.value = country;
  fillRegions(form, region);
}

/**
 * Fills the region picker for the country chosen, under the word that country
 * uses for its parts, and puts it away for a country with none.
 *
 * Required where the regions divide the country - a German state, a British
 * nation - and starting on nothing there, so the form cannot be saved without
 * one. Where a few regions keep a day or two more than the rest, the rest of the
 * country comes first.
 */
function fillRegions(form, chosen) {
  const code = form.elements.country.value;
  const country = holidayCountries.find((one) => one.code === code);
  const regions = country?.regions ?? [];
  const select = form.elements.region;
  const field = select.closest('label');

  // Not required while it is away: a hidden control that is still required
  // cannot be submitted past, and the form silently does nothing.
  field.hidden = !regions.length;
  select.required = Boolean(regions.length && country.regionRequired);

  if (code === 'DE') swapTheLabel(field, 'holiday.region', 'State');
  else if (code === 'GB') swapTheLabel(field, 'holiday.nation', 'Nation');
  else swapTheLabel(field, 'holiday.area', 'Region');

  select.replaceChildren(
    country?.regionRequired
      ? el('option', { value: '', text: t('holiday.chooseRegion', 'Choose one') })
      : el('option', { value: '', text: t('holiday.elsewhere', 'Everywhere else') }),
    ...regions.map((region) => el('option', { value: region, text: regionName(`${code}-${region}`) })));

  select.value = regions.includes(chosen) ? chosen : '';
}

/** The choice the holiday card's pickers make: a place, or what stands in for one. */
function chosenPlace(form) {
  const country = form.elements.country.value;
  const region = form.elements.region.value;

  if (!holidayCountries.some((one) => one.code === country)) return country;

  return region ? `${country}-${region}` : country;
}

function wireHolidays() {
  const form = $('#form-holidays');
  if (!form) return;

  form.elements.country.addEventListener('change', (event) => fillRegions(event.target.form, ''));

  form.addEventListener('submit', (event) => {
    event.preventDefault();

    saveForm(form,
      () => api('/settings/holidays', { method: 'PUT', body: JSON.stringify({ place: chosenPlace(form) }) }),
      t('holiday.saved', 'Public holidays saved'),
      refreshAll);
  });
}

/** Draws what the account screen says about the account, from the account as it was last read. */
function fillSettingsForm() {
  if (!me.user) return;

  // Each of these guards itself against somebody part way through its form.
  fillMyTimezone();
  drawNotifyState();
  drawPushState();
}

// --------------------------------------------------------------- API tokens

async function loadTokens() {
  const tokens = (await api('/me/tokens'))?.items ?? [];

  // A value on the card whose token is no longer listed - revoked here or in
  // another tab - opens nothing, and the card went on saying "copy it now".
  const shown = $('#token-secret')?.dataset.token;
  if (shown && !tokens.some((token) => String(token.id) === shown)) forgetTheTokenValue();

  const rows = tokens.map((token) => el('tr', { class: token.expired ? 'empty' : '' },
    el('td', { class: 'text', text: token.name }),
    el('td', {}, el('code', { text: `${token.prefix}…` })),
    el('td', { text: fmtMoment(token.createdAt) }),
    el('td', { text: token.expiresAt ? fmtMoment(token.expiresAt) : t('token.never', 'unlimited') }),
    el('td', { text: token.lastUsedAt ? fmtMoment(token.lastUsedAt) : t('token.unused', 'never') }),
    el('td', { class: 'actions' }, deleteButton({
      // Called revoking rather than deleting, because that is what it means for
      // a token - but it is the same request, so it joins the batch too.
      text: t('token.revoke', 'revoke'),
      label: `${t('token.name', 'Label')} "${token.name}"`,
      path: `/me/tokens/${token.id}`,
      message: t('token.revoked', 'Token revoked'),
      after: loadTokens,
    })),
  ));

  fillTable($('#table-tokens tbody'), rows, 6, t('token.empty', 'No tokens yet.'));
}

/**
 * Takes a token's value off the screen.
 *
 * It exists once, in the answer that created it, and the card says so where the
 * reader can see it: "Copy it now - this value is never shown again." What it did
 * not say is how long "now" lasted, and the answer was until the page was loaded
 * again: the value was written in one place and cleared in none, so it stayed
 * through every screen and through a sign-out.
 *
 * That is a bearer credential carrying the whole of this account's role, working
 * until somebody revokes it, left on the machine for whoever sits down next. The
 * enrolment panel two cards above is cleared on both of those, and by comparison
 * a second factor nobody finished is the smaller thing to leave behind.
 */
function forgetTheTokenValue() {
  const panel = $('#token-secret');
  if (!panel) return;

  panel.hidden = true;
  delete panel.dataset.token;
  $('#token-secret-value').textContent = '';
  $('#token-address-value').textContent = '';
  $('#token-address').hidden = true;

  // The code beside it carries the same value, in its picture rather than as
  // text, so it goes with it - from the screen and from the markup.
  const qr = $('#token-qr');
  qr.hidden = true;
  qr.removeAttribute('src');
  $('#token-qr-hint').hidden = true;
}

function wireTokens() {
  $('#form-token').addEventListener('submit', (e) => {
    e.preventDefault();

    const raw = formData(e.target);
    const body = { name: raw.name, expiresInDays: Number(raw.expiresInDays ?? 0) };

    mutate(async () => {
      const created = await api('/me/tokens', { method: 'POST', body: JSON.stringify(body) });

      // The secret exists only in this response, so it is shown on the card
      // rather than in a toast that disappears - and taken off by
      // forgetTheTokenValue when the screen is left or the session ends, which
      // is what makes "until you navigate away" true rather than a description
      // of what was meant.
      $('#token-secret-value').textContent = created.secret;
      $('#token-secret').dataset.token = String(created.id);

      // The address the value is used at - read out of the code's own URI rather
      // than from this page, so that the text and the code cannot name two
      // different installations.
      const address = new URLSearchParams(created.connectUri?.split('?')[1]).get('url');
      $('#token-address-value').textContent = address ?? '';
      $('#token-address').hidden = !address;

      // And the same value with the installation's address, as a code an app
      // scans instead of having the two typed into it. Not every answer brings
      // one - a code that failed to render does not cost the token - and then
      // there is only the value.
      const qr = $('#token-qr');
      qr.hidden = !created.qr;
      qr.alt = created.qr ? t('token.qrAlt', 'QR code with the address of this installation and the new token') : '';
      if (created.qr) qr.src = created.qr;
      $('#token-qr-hint').hidden = !created.qr;

      $('#token-secret').hidden = false;
      e.target.reset();
      await loadTokens();
    }, null, null);
  });
}

// ------------------------------------------------------- sign-in provider

/**
 * Where a provider sends the browser back to: a page of this interface rather
 * than a route of the API, because it is the page that hands the code to the
 * server - with the session cookie and the round's own cookie that a request
 * from this page carries and an address followed from another site would not.
 */
const OPENID_CALLBACK = '/sign-in/openid';

/** What the sign-in form offers of a provider, as the server last said. */
let openIDOffer = { offered: false };

/** Asks whether a provider is offered, and labels the sign-in form's button. */
async function loadOpenIDOffer() {
  try {
    openIDOffer = (await api('/auth/openid')) ?? { offered: false };
  } catch {
    openIDOffer = { offered: false };
  }

  // The installation's, like its name on the same form: kept across a sign-out,
  // and written again in a language chosen there.
  redrawable('openid-offer', drawOpenIDOffer, { installation: true });
}

function drawOpenIDOffer() {
  const button = $('#login-openid');
  if (!button) return;

  button.hidden = !openIDOffer.offered;
  button.textContent = openIDOffer.label
    ? fillIn(t('openid.signIn', 'Sign in with {0}'), [openIDOffer.label])
    : t('openid.signInUnnamed', 'Sign in through your organisation');
}

/** Sends the browser to the provider, for a sign-in or for linking the account. */
async function goToTheProvider(path) {
  const { address } = await api(path, { method: 'POST' });

  window.location.assign(address);
}

/**
 * Finishes the round a provider sent the browser back from, before anything else
 * loads, and answers what the page should say once it has.
 *
 * The code and the state are taken off the address at once, so that a reload,
 * the history or a bookmark never holds them. A provider that sent the browser
 * back without a code - the person turned back there, or it refused them - is
 * reported in the same words as one whose code was refused here, because to the
 * person it is the same thing.
 */
async function finishTheProviderRound() {
  if (window.location.pathname !== OPENID_CALLBACK) return null;

  const back = new URLSearchParams(window.location.search);
  window.history.replaceState(null, '', '/');

  const code = back.get('code');
  const state = back.get('state');
  const notConfirmed = t('openid.notConfirmed', 'The sign-in provider did not confirm the sign-in.');

  if (!code || !state) return { problem: notConfirmed };

  try {
    const answer = await api('/auth/openid/finish', {
      method: 'POST', body: JSON.stringify({ state, code }),
    });

    return { linked: Boolean(answer?.linked) };
  } catch (err) {
    return { problem: err.message || notConfirmed };
  }
}

/**
 * Says what the round came to, once the page has loaded: a link on the screen it
 * was made from, and a problem in a notice over whatever screen is showing. A
 * problem on the sign-in form is said there instead, by init.
 */
function sayWhatTheProviderRoundCameTo(returned) {
  if (returned?.linked) {
    switchView('settings');
    openTheTabOf($('#openid-card'));
    toast(t('openid.linkedNow', 'Your account is linked. From now on you can sign in through your organisation.'), 'ok');
  } else if (returned?.problem) {
    toast(returned.problem, 'error');
  }
}

/**
 * Whether this account is linked to its identity at the provider.
 *
 * Not offered at all where no provider is configured. For the two accounts a
 * provider never signs in - the built-in administrator and an account the
 * directory holds - the card says why in place of its button, because a card that
 * is simply absent cannot be told from a feature that has been taken away.
 */
async function loadOpenIDLink() {
  const card = $('#openid-card');
  if (!card) return;

  // Asked again with everything else on a reload, for the reason the sign-in form
  // asks again: the provider can have been configured since the page opened.
  await loadOpenIDOffer();

  card.hidden = !openIDOffer.offered || !me.user;
  if (card.hidden) return;

  let barred = '';

  if (me.user.isSystem) {
    barred = t('openid.adminBarred',
      'The built-in administrator signs in with its password, so that this installation never depends on a provider to be let in.');
  } else if (me.user.isExternal) {
    barred = t('openid.directoryBarred',
      'Your account comes from the directory and signs in with its password there, so that the directory can end it.');
  }

  const link = barred ? null : await api('/me/openid');

  $('#openid-state').textContent = barred || (link?.linked
    ? fillIn(t('openid.linked', 'Linked since {0}.'), [fmtMoment(link.linkedAt)])
    : t('openid.notLinked', 'Not linked yet.'));

  const button = $('#openid-link');
  button.hidden = Boolean(barred) || Boolean(link?.linked);
  button.textContent = openIDOffer.label
    ? fillIn(t('openid.link', 'Link with {0}'), [openIDOffer.label])
    : t('openid.linkUnnamed', 'Link your organisation’s account');

  $('#openid-unlink').hidden = Boolean(barred) || !link?.linked;
}

/** Takes the provider card back to saying nothing about anybody's link. */
function forgetTheOpenIDLink() {
  const card = $('#openid-card');
  if (!card) return;

  card.hidden = true;
  $('#openid-state').textContent = '';
  $('#openid-link').hidden = true;
  $('#openid-unlink').hidden = true;
}

/** Fills the settings card from the provider as stored. */
async function loadOpenIDSettings() {
  const form = $('#form-openid');
  if (!form) return;

  const config = await api('/settings/openid');

  // What the provider has to be told, which is this installation as this browser
  // reached it - written whatever is being typed below, since nobody types it.
  $('#openid-redirect').value = config.redirectUri ?? '';

  if (beingEdited(form)) return;

  form.elements.enabled.checked = Boolean(config.enabled);
  form.elements.issuer.value = config.issuer ?? '';
  form.elements.clientId.value = config.clientId ?? '';
  form.elements.label.value = config.label ?? '';
  form.elements.clientSecret.value = '';
}

function openIDPayload() {
  const form = $('#form-openid');

  return {
    enabled: form.elements.enabled.checked,
    issuer: form.elements.issuer.value.trim(),
    clientId: form.elements.clientId.value.trim(),
    clientSecret: form.elements.clientSecret.value,
    label: form.elements.label.value.trim(),
  };
}

function wireOpenID() {
  $('#login-openid').addEventListener('click', () => {
    goToTheProvider('/auth/openid').catch((err) => showLogin(err.message));
  });

  $('#openid-link').addEventListener('click', () => {
    mutate(() => goToTheProvider('/me/openid'), null, null);
  });

  $('#openid-unlink').addEventListener('click', async () => {
    const unlink = await confirmDialog({
      title: t('openid.unlinkTitle', 'Unlink your account?'),
      text: t('openid.unlinkText', 'The provider will no longer sign you in. Your password keeps working.'),
      confirmLabel: t('openid.unlink', 'Unlink'),
    });
    if (!unlink) return;

    mutate(() => api('/me/openid', { method: 'DELETE' }),
      t('openid.unlinked', 'Unlinked'),
      loadOpenIDLink);
  });

  $('#form-openid').addEventListener('submit', (e) => {
    e.preventDefault();
    saveForm(e.target,
      () => api('/settings/openid', { method: 'PUT', body: JSON.stringify(openIDPayload()) }),
      t('admin.saved', 'Settings saved'),
      async () => { await loadAdmin(); await loadOpenIDOffer(); });
  });

  $('#openid-test').addEventListener('click', () => {
    runConnectionTest($('#openid-test-result'), () => api('/settings/openid/test',
      { method: 'POST', body: JSON.stringify({ issuer: openIDPayload().issuer }) }));
  });
}

// ------------------------------------------------------------ administration

/**
 * Whether this account administers the installation: the built-in administrator,
 * or anybody whose role holds settings:manage.
 *
 * Mirrors Authorizer.RequireInstallationAdmin, and has to: this decides what is
 * offered and that decides what is answered. A screen offering what the server
 * refuses is worse than one that offers nothing.
 */
const administers = () => !me.authEnabled
  || (me.user?.isSystem ?? false)
  || can('settings:manage');

/**
 * Applies the instance branding.
 *
 * Fetched separately from /me because the sign-in screen must show the title
 * and logo before anyone has authenticated.
 */
async function loadBranding() {
  const branding = await api('/branding');

  lastBranding = branding;

  // The first answer of the page's life, which is the build this document was
  // written by. Every later one is about the process rather than about the tab.
  if (loadedVersion === null) loadedVersion = branding.version ?? '';

  // Drawn through the redraw registry, so switching language picks the texts
  // again. Everything on this screen that a language change reaches goes the same
  // way; a title and a banner written in two languages would otherwise stay in
  // whichever one the page happened to load in.
  redrawable('branding', () => drawBranding(branding), { installation: true });

  return branding;
}

/**
 * Writes the sign-in card's heading, which greets by the installation's name.
 *
 * A sentence with the name in it rather than a fixed word in the markup with the
 * name after it, so a language can put the name where its grammar wants it.
 */
function drawWelcome(title) {
  $('#login-welcome').textContent = fillIn(t('login.welcome', 'Welcome to {0}'), [title]);
}

/**
 * The name of every installation. What an administrator writes under Settings
 * is added to it after a bar and never takes its place; the server writes the
 * same rule in model.Named, and the two have to agree.
 */
const BRAND = 'QubitDesk';

/** What an installation is called with the addition written beside the brand. */
function named(addition) {
  const added = (addition ?? '').trim();

  // The brand written again adds nothing: it was the obvious answer while the
  // name could still be replaced, so it is stored where nobody meant to add.
  if (!added || added.toLowerCase() === BRAND.toLowerCase()) return BRAND;

  return `${BRAND} | ${added}`;
}

function drawBranding(branding) {
  const title = named(brandingIn(branding, 'title'));

  // The tab may add something of its own, because the room runs out there
  // first: a name that reads across the top of the screen is cut off after a
  // couple of dozen characters in a tab, and somebody has six of them open.
  // Where nothing separate was written, it adds what the header adds.
  const tabTitle = named(brandingIn(branding, 'tabTitle') || brandingIn(branding, 'title'));

  // Remembered on the device, so the next load has the instance's own name
  // before it is painted rather than a second later. theme.js reads this; see
  // the note there for why a reload otherwise flickers back to a name nobody
  // chose. The tab's name resolved rather than both fields, since the tab is the
  // only thing that reads it.
  try {
    localStorage.setItem('qubitdesk.branding', JSON.stringify({
      title: tabTitle,
    }));
  } catch {
    // Private browsing refuses storage outright, which costs only the flicker.
  }

  document.title = tabTitle;
  // Into the span rather than the button: the button also holds the mark, and
  // writing text onto the button would take the mark out with it.
  $('#app-title-text').textContent = title;
  drawWelcome(title);

  // The one place an installation's logo is drawn: the left of the bar, once
  // somebody has signed in. The sign-in screen and the browser tab carry
  // QubitDesk's own mark whatever is uploaded - decided by the maintainer on
  // 2026-11-18 - and the button beside the title carries it as well.
  //
  // It is the installation's slot, so with no logo it does not fall back to our
  // mark - that would make an unbranded installation look branded by somebody
  // else - but to the chip with the G, which showBrandMark draws.
  //
  // At the size it draws, made when the logo was saved: the banner-sized copy,
  // which is the one the old sign-in card drew at the same 96 pixels. The
  // 440x80 one is the right file for a 40px slot and a blurred one here. The
  // original is the fallback for an installation whose logo predates the derived
  // sizes; it looks the same, it is only bigger.
  const logo = branding.logoBanner || branding.logo;
  const header = $('#brand-logo');

  if (header) {
    // Emptied as well as hidden: a hidden element holding a few hundred
    // kilobytes of data URI is still holding them.
    header.src = logo || '';
    header.hidden = !logo;
    header.alt = logo ? (branding.title || '') : '';
  }

  showBrandMark(Boolean(logo));

  // The announcement banner is separate from the "change your password" one.
  // Through the renderer, so a {year} in a copyright line stays right and a link
  // in a footer is a link. Drawn rather than assigned, because none of this is
  // HTML - see renderConfiguredText for why it must not be.
  renderConfiguredText($('#instance-banner'), brandingIn(branding, 'banner'));
  renderConfiguredText($('#footer-text'), brandingIn(branding, 'footerText'));
  renderConfiguredText($('#footer-legal'), brandingIn(branding, 'legalNotice'));

  const company = $('#footer-company');
  company.textContent = branding.companyName ?? '';
  company.hidden = !branding.companyName;
  if (branding.companyUrl) company.href = branding.companyUrl;
  else company.removeAttribute('href');

  // The build serving this page. Shown even when nothing else is configured,
  // which is why the footer is no longer hidden when the branding is empty:
  // "which version is actually running" is the first question of every support
  // conversation, and guessing it from a container tag is not an answer.
  const versionEl = $('#footer-version');
  if (versionEl) {
    // The platform beside the version, as "v1.0 (windows)". The same version is
    // published for four of them and they do not all behave alike - restarting
    // from the interface works on Linux and cannot on Windows - so the version
    // alone does not say what somebody is looking at.
    const version = branding.version || '';
    const platform = branding.os || '';

    versionEl.textContent = version && platform ? `${version} (${platform})` : version;
    versionEl.title = version
      ? t('footer.versionTitle', 'Running version of this installation')
      : '';
  }
}

/**
 * Says where the connection on a screen with nothing stored comes from.
 *
 * The connection can come from three places, and the screen only ever knew one
 * of them: the file the installer or this form writes. An installation
 * configured through the environment therefore saw an empty form under a line
 * saying "connected via postgres" - which reads as "not configured" and is not.
 * The loader fills it with the running connection now, as values; this is the
 * note that says they are the environment's.
 *
 * The reason that matters beyond looking wrong: the file wins over the
 * environment. Saving this form on such an installation, changed or not, stores
 * the connection as a setting that overrides the deployment's own at the next
 * start, and nothing said so.
 */
function showRunningConnection(form, ds) {
  const note = $('#datasource-source');
  const running = ds.running ?? {};

  if (ds.stored) {
    if (note) note.hidden = true;

    return;
  }

  // The type follows what is running even while the form counts as being
  // edited, which the other fields do not.
  //
  // It was once the one field left blank, on the reasoning that a select cannot
  // hold a placeholder; that produced a dropdown showing nothing above fields
  // describing a connection of a type the card would not name, and a reload
  // landed it on the first option, claiming SQLite on an installation running
  // PostgreSQL. So it names what is running, and the note says where that came
  // from - unless somebody has chosen one themselves.
  //
  // Asked of the control rather than of the form. "Somebody is filling this in"
  // is the right question for the text fields - they hold what was typed and
  // must not be taken away - and the wrong one for this select, because the
  // select decides which text fields exist at all. A form counted as being
  // edited for any reason, including a draft restored from an earlier visit,
  // left the type on whatever it happened to hold, and the card then described
  // a SQLite file directly under a line reading "currently connected via
  // postgres". Two answers to one question, on the one card whose job is to
  // give it.
  //
  // A person choosing a type is a different event from a script setting one:
  // isTrusted is false for anything dispatched from code, which is what restoring
  // a draft does. So this follows what is running until somebody actually picks
  // something, and then it is theirs.
  if (running.dialect && form.elements.dialect.dataset.chosen === undefined) {
    form.elements.dialect.value = running.dialect;
  }

  if (note) {
    note.textContent = t('admin.connectionFromEnvironment',
      'This connection comes from the environment, not from a saved setting. '
      + 'Saving this form, changed or not, stores it as a setting of its own, '
      + 'which takes precedence over the environment from the next start.');
    note.hidden = false;
  }
}

/**
 * What the password box holds while it stands for a password the server keeps.
 *
 * The card shows the connection in force, and a password box left empty beside
 * it read as "no password". The page never receives the password itself: it
 * would be one click on the eye away from whoever sits at the screen, and a way
 * for an administrator, who by design reads nobody's hours, to open all of them
 * in the database. So the box is filled with this instead, masked like any
 * password, and it is never sent - datasourcePayload turns it back into the
 * empty field the server reads as "keep the one in force", which it does for the
 * same server and user only. Naming another one empties the box, and naming the
 * same one again fills it back, so the box says what saving will do.
 */
const KEPT_PASSWORD = '\u2022'.repeat(8);

/** The port a database type is reached on when none is given. */
function defaultPortOf(dialect) {
  if (dialect === 'postgres') return '5432';
  if (dialect === 'mysql') return '3306';

  return '';
}

/**
 * Who the connection on the card signs in as, read the way the server compares
 * it: an empty port is the type's default, a host is not case-sensitive, and the
 * database name is no part of it - a password belongs to a user on a server.
 */
function datasourceAccount(values) {
  const dialect = (values.dialect ?? '').toLowerCase();

  return JSON.stringify([dialect, (values.host ?? '').trim().toLowerCase(),
    (values.port ?? '').trim() || defaultPortOf(dialect), values.user ?? '']);
}

/**
 * Fills the password box for the connection the card was filled from, or
 * empties it when that connection has no password.
 */
function showKeptPassword(form, source, hasPassword) {
  const box = form.elements.password;

  if (hasPassword) box.dataset.keptFor = datasourceAccount(source);
  else delete box.dataset.keptFor;

  box.value = '';
  followKeptPassword(form);
}

/**
 * Keeps the password box in step with the account the card now names.
 *
 * While nobody has typed in it, it shows the kept password for the account it
 * belongs to and nothing for any other; the eye beside it is shut, because there
 * is nothing behind the dots to reveal. Once somebody types, the box is theirs.
 */
function followKeptPassword(form) {
  const box = form.elements.password;
  const typed = box.value !== '' && box.value !== KEPT_PASSWORD;
  const eye = box.closest('.password-field')?.querySelector('button');

  if (typed || box.dataset.keptFor === undefined) {
    delete box.dataset.kept;
    if (eye) eye.disabled = false;
  } else {
    const kept = box.dataset.keptFor === datasourceAccount(formData(form));

    if (kept) box.dataset.kept = 'yes';
    else delete box.dataset.kept;

    // Not over a box somebody has just clicked into, which is empty on purpose.
    if (document.activeElement !== box) box.value = kept ? KEPT_PASSWORD : '';
    if (eye) eye.disabled = kept;
  }

  box.placeholder = box.dataset.keptFor !== undefined && !box.dataset.kept && !typed
    ? t('admin.passwordForThisServer', 'the password for this server')
    : t('admin.keepStored', 'leave unchanged');
}

/**
 * The database card as a request: what saving and testing both send.
 *
 * One reader for both, because they had one each: the save put away the fields
 * the chosen type does not have, and the test sent them as they stood. And the
 * stand-in in the password box must never leave the page - an untouched box is
 * an empty password, which the server reads as the one in force.
 */
function datasourcePayload(form) {
  const body = formData(form);

  if (form.elements.password.dataset.kept === 'yes' || body.password === KEPT_PASSWORD) {
    delete body.password;
  }

  // A field that is not on screen for this dialect has nothing to say about the
  // connection, and sending what is left in it would store an SSL mode against
  // MySQL, or a host against a file on disk.
  if (!datasourceIsServer()) {
    for (const field of ['host', 'port', 'user', 'password']) body[field] = '';
  }

  if (body.dialect !== 'postgres') body.sslMode = '';

  return body;
}

/**
 * Whether the chosen database is one that lives on a server rather than a file.
 *
 * Nothing chosen is not a server. It read as one - anything that was not sqlite
 * counted - so a card with no type selected offered the host, the user and the
 * password of a server nobody had asked for, and filled the port in with 3306
 * because that is what a server that is not PostgreSQL uses. A real value in one
 * box, placeholders in the others and an empty dropdown above all of it, on a
 * screen whose whole job is to say what this installation is connected to.
 */
function datasourceIsServer() {
  const chosen = $('#form-datasource').elements.dialect.value;

  return chosen !== '' && chosen !== 'sqlite';
}

/**
 * Shows the database fields that apply to the chosen type, and hides the rest.
 *
 * The same four behaviours the installation assistant has had all along, which
 * this screen never got: SQLite offered a host, a port, a user, a password and an
 * SSL mode for a file on disk; MySQL offered PostgreSQL's SSL mode; the caption
 * above the name said "database" for a thing that is a file path; and the port was
 * never filled in, so it had to be looked up.
 *
 * Kept separate from the installer's copy rather than shared: that page ships as
 * one self-contained file precisely because it runs before these assets are
 * loaded, so there is nothing to share it with.
 */
function syncDatasourceFields() {
  const form = $('#form-datasource');
  const server = datasourceIsServer();

  $('#ds-server-fields').hidden = !server;
  $('#ds-ssl-label').hidden = form.elements.dialect.value !== 'postgres';

  // The key travels with the words. This label says two different things
  // depending on the type, and only the text was being changed - so the element
  // went on declaring itself as one of them while showing the other, and a
  // language chosen afterwards put the declared one back.
  const nameLabel = $('#ds-name-label');

  if (server) {
    swapTheLabel(nameLabel, 'admin.dbName', 'Database / file name');
  } else {
    swapTheLabel(nameLabel, 'admin.dbFile',
      'Database file - created if it does not exist');
  }

  if (server && !form.elements.port.value) {
    form.elements.port.value = defaultPortOf(form.elements.dialect.value);
  }
}

async function loadAdmin() {
  // The tab itself is a data-perm, applied as soon as who is signed in is known -
  // which is not here. This only decides whether there is anything to load, and it
  // has to agree with the tab, or an account would see a screen full of failures.
  if (!can('settings:manage')) return;

  await loadMaintenance();
  await loadPushSwitch();

  const branding = await api('/branding');
  const form = $('#form-branding');

  // Not over an appearance somebody is part way through writing. This runs after
  // every save on this screen and after a language is chosen, and the draft
  // below is rebuilt from the server - so a reload used to take back not only
  // the boxes on screen but the translations typed into the other languages.
  //
  // Skipped as one block. Filling half of it would leave the boxes showing one
  // thing and the draft holding another, which is worse than either.
  if (!beingEdited(form)) {
    // The two that are the same in every language. A logo, an address and a link
    // do not translate - translating a link would be translating where it goes.
    for (const field of ['companyName', 'companyUrl']) {
      form.elements[field].value = branding[field] ?? '';
    }

  // And the ones that do, kept per language while this screen is open so
  // switching between them does not lose what has been typed.
    brandingDraft = {};

    for (const language of BRANDING_LANGUAGES) {
      const written = branding.translations?.[language] ?? {};

      brandingDraft[language] = {
        title: written.title ?? '',
        tabTitle: written.tabTitle ?? '',
        banner: written.banner ?? '',
        footerText: written.footerText ?? '',
        legalNotice: written.legalNotice ?? '',
      };
    }

  // What the installation answered before any of this existed stays the base,
  // and is what the form shows for the language it was presumably written in.
    brandingBase = {
      title: branding.title ?? '',
      tabTitle: branding.tabTitle ?? '',
      banner: branding.banner ?? '',
      footerText: branding.footerText ?? '',
      legalNotice: branding.legalNotice ?? '',
    };

    showBrandingLanguage($('#branding-language')?.value || activeLanguage());

    // What was chosen last time, so the previews show it and the chooser opens
    // on it rather than starting again.
    logoCrops = branding.crops ?? {};

    setLogoPreview(branding.logo ?? '');
  }

  await loadOperational();
  await loadUpdate();
  await loadBackups();

  const timezone = await api('/settings/timezone');
  const instanceSelect = $('#instance-timezone');

  // Not over a zone somebody has picked and not yet saved.
  if (!beingEdited($('#form-timezone'))) {
    fillTimezoneSelect(instanceSelect, timezone.timezone ?? 'UTC');
  }

  // The clock beside it either way: it follows whatever the picker shows, and
  // what time it is somewhere does not depend on anybody having saved.
  showTimeIn(instanceSelect, $('#instance-timezone-now'), 'UTC');

  const holidays = await api('/settings/holidays');
  holidayCountries = holidays?.countries ?? holidayCountries;

  if (!beingEdited($('#form-holidays'))) {
    fillHolidayPickers($('#form-holidays'), holidays.place, 'DE',
      el('option', { value: 'none', text: t('holiday.noRegion', 'No public holidays') }));
  }

  const ds = await api('/settings/datasource');
  const dsForm = $('#form-datasource');

  // This card has two halves, and only one of them belongs to whoever is at the
  // keyboard.
  //
  // The values are theirs. This is the card the loss was reported from:
  // choosing a language reloads every screen, so switching to German to read a
  // label put the stored connection back over the one being typed - and an
  // empty stored connection put back nothing at all, which looked like a form
  // nobody had filled in.
  //
  // What this installation is connected to is not theirs, and it was being
  // skipped along with them. One touch of this form and the card went on saying
  // "connected via postgres" above five empty boxes for as long as the tab
  // stayed open - no note saying where the connection came from, and no
  // placeholders saying what it was. A restored draft did it without anybody
  // touching anything, because restoring one marks the form as being filled in.
  //
  // Filled from the connection in force: the stored one, or - where nothing is
  // stored, which is every installation configured through the environment, a
  // compose deployment or a container run with DB_* set - the one this process
  // opened. Those were shown as placeholders once, on the reasoning that they
  // were not this form's to save; they read as examples rather than as what is
  // running, and the note beside them says what saving them does instead.
  const source = ds.stored ? ds : (ds.running ?? {});

  if (!beingEdited(dsForm)) {
    for (const field of ['dialect', 'name', 'host', 'port', 'user', 'sslMode']) {
      dsForm.elements[field].value = source[field] ?? '';
    }
  }

  showRunningConnection(dsForm, ds);

  // After the values are in, or the port would be prefilled over a stored one.
  syncDatasourceFields();

  // And the password after the port, since the port is part of whose it is.
  if (!beingEdited(dsForm)) {
    showKeptPassword(dsForm, source, ds.stored ? ds.hasPassword : ds.runningHasPassword);
  } else {
    followKeptPassword(dsForm);
  }

  // What this process is connected to, whoever is typing what: it describes the
  // running application rather than the form.
  $('#datasource-active').textContent =
    `${t('admin.activeConnection', 'Currently connected via')}: ${ds.active}`;

  await loadTelemetry();
  await loadRestart();

  const ldap = await api('/settings/ldap');
  const ldapForm = $('#form-ldap');
  const ldapFields = [
    'host', 'baseDn', 'bindDn', 'userFilter',
    'nameAttribute', 'emailAttribute', 'idAttribute',
  ];
  // The options first, and outside the guard below.
  //
  // What goes into a picker is the installation's roles, which is server data
  // rather than anything somebody typed - so protecting a half-filled card from
  // being overwritten must not take the choices with it. It did, and a card that
  // had been touched came back offering nothing to choose. fillSelect keeps a
  // selection that is still among the options, so a role chosen and not yet
  // saved survives this.
  fillSelect(ldapForm.elements.defaultRole, directoryRoleChoices(),
    { labelKey: 'label', valueKey: 'name' });

  // Not over a directory somebody is part way through configuring. A bind DN
  // and a filter are long enough to be worth not losing to a reload nobody
  // asked for.
  if (!beingEdited(ldapForm)) {
    for (const field of ldapFields) {
      ldapForm.elements[field].value = ldap[field] ?? '';
    }

    ldapForm.elements.port.value = ldap.port || 389;

    for (const flag of ['enabled', 'startTls', 'useTls', 'skipVerify']) {
      ldapForm.elements[flag].checked = Boolean(ldap[flag]);
    }

    // || rather than ??, because the value that actually arrives from an older
    // installation is an empty string rather than a missing field - and ?? lets
    // it through, which sets a <select> to a value none of its options carry and
    // leaves it showing nothing at all.
    ldapForm.elements.defaultRole.value = ldap.defaultRole || ORDINARY_ROLE;
  }

  // Whatever happened above, something is selected. A <select> set to a value
  // none of its options carry selects nothing and draws an empty box, which is
  // what this card kept showing; there is always at least one role to fall back
  // to, and none of the options is an empty one.
  //
  // The ordinary user role before the first option in the list, because the two
  // are not the same answer: the list is in whatever order the roles came back
  // in, and the first of them can as easily be the administrator. A directory
  // nobody has configured yet should provision people who record time, not
  // people who can reconfigure the installation - so if the stored role is
  // missing or unknown, this lands on the safe one rather than the nearest one.
  const picker = ldapForm.elements.defaultRole;

  if (!picker.value) picker.value = ORDINARY_ROLE;

  if (!picker.value) picker.selectedIndex = 0;

  await loadOpenIDSettings();

  await loadSyncRuns();

  const schedule = $('#form-sync-schedule');
  if (schedule) {
    // Filled whether the card is on screen or not: the schedule travels with the
    // rest of the directory settings, so an empty field here would clear it the
    // next time somebody saved the connection.
    //
    // Not over one being typed, for the same reason as every other card here.
    if (!beingEdited(schedule)) {
      schedule.elements.syncSchedule.value = ldap.syncSchedule ?? '';
    }

    // What this process is actually scheduled to do, which is not the stored
    // value until the next start - and the restart card is what says so.
    $('#sync-schedule-active').textContent = ldap.syncSchedule
      ? `${t('sync.scheduleStored', 'Saved')}: ${ldap.syncSchedule}`
      : t('sync.scheduleManual', 'Runs only when the button below is pressed.');
  }

  // Last, so a log that cannot be read leaves every card above it filled.
  await loadChanges();
}

// ------------------------------------------------------- administration log

/** The changes shown so far, oldest last, and whether older ones remain. */
let adminChanges = { entries: [], more: false };

/**
 * Reads the administration log: the newest page, or the page before the oldest
 * change shown when older ones are asked for.
 */
async function loadChanges(older = false) {
  if (!can('settings:manage') || !$('#changes-card')) return;

  const before = older ? adminChanges.entries.at(-1)?.id : null;
  const page = await api(`/admin/changes${before ? `?before=${before}` : ''}`);

  adminChanges = {
    entries: older ? [...adminChanges.entries, ...page.entries] : page.entries,
    more: page.more,
  };

  redrawable('changes', drawChanges);
}

function drawChanges() {
  // Nobody's account made a change numbered zero: that is the recover command,
  // run on the server where no request and so no account is involved.
  const changedBy = (actor) => (actor.id === 0
    ? t('changes.byCommand', 'The command line on the server')
    : actor.name || `#${actor.id}`);

  const rows = adminChanges.entries.map((entry) => el('tr', {},
    el('td', { text: fmtMoment(entry.at) }),
    el('td', { text: changedBy(entry.actor) }),
    el('td', { text: describeChange(entry) }),
  ));

  fillTable($('#table-changes tbody'), rows, 3, t('changes.empty', 'Nothing has been changed yet.'));
  $('#changes-more').hidden = !adminChanges.more;
}

/**
 * The sentence for each change the administration log records, one for every
 * code in its table - test/adminlog_test.go holds the two together, so a change
 * recorded is never shown as its code.
 */
const CHANGE_SENTENCES = {
  'user.created': () => t('change.userCreated', 'Created the account {0}'),
  'user.updated': () => t('change.userUpdated', 'Corrected the account {0}'),
  'user.deleted': () => t('change.userDeleted', 'Deleted the account {0}'),
  'user.role': () => t('change.userRole', 'Gave {0} the role {1}'),
  'user.password': () => t('change.userPassword', 'Set a new password for {0}'),
  'user.secondFactor': () => t('change.userSecondFactor', 'Removed the second factor of {0}'),
  'user.recovered': () => t('change.userRecovered', 'Recovered {0}: a new password, no second factor'),
  'users.imported': () => t('change.usersImported', 'Imported accounts from a spreadsheet'),
  'contact.portalGiven': () => t('change.contactPortalGiven', 'Gave {0} access to the customer portal'),
  'contact.portalTaken': () => t('change.contactPortalTaken', 'Took the customer portal away from {0}'),
  'role.created': () => t('change.roleCreated', 'Created the role {0}'),
  'role.updated': () => t('change.roleUpdated', 'Changed the role {0}'),
  'role.deleted': () => t('change.roleDeleted', 'Deleted the role {0}'),
  'roles.imported': () => t('change.rolesImported', 'Imported roles from a spreadsheet'),
  'catalog.created': () => t('change.catalogCreated', 'Added {0} to the general catalog'),
  'catalog.updated': () => t('change.catalogUpdated', 'Changed {0} in the general catalog'),
  'classDefinition.created': () => t('change.classDefinitionCreated', 'Wrote a new definition of the class {0}'),
  'queue.created': () => t('change.queueCreated', 'Created the queue {0}'),
  'queue.updated': () => t('change.queueUpdated', 'Changed the queue {0}'),
  'queue.deleted': () => t('change.queueDeleted', 'Deleted the queue {0}'),
  'state.created': () => t('change.stateCreated', 'Created the ticket state {0}'),
  'state.updated': () => t('change.stateUpdated', 'Changed the ticket state {0}'),
  'state.deleted': () => t('change.stateDeleted', 'Deleted the ticket state {0}'),
  'priority.created': () => t('change.priorityCreated', 'Created the priority {0}'),
  'priority.updated': () => t('change.priorityUpdated', 'Changed the priority {0}'),
  'priority.deleted': () => t('change.priorityDeleted', 'Deleted the priority {0}'),
  'type.created': () => t('change.typeCreated', 'Created the ticket type {0}'),
  'type.updated': () => t('change.typeUpdated', 'Changed the ticket type {0}'),
  'type.deleted': () => t('change.typeDeleted', 'Deleted the ticket type {0}'),
  'settings.maintenance': () => t('change.maintenance', 'Switched maintenance mode {1}'),
  'settings.branding': () => t('change.branding', 'Changed the appearance'),
  'settings.ldap': () => t('change.ldap', 'Changed the LDAP connection'),
  'settings.openid': () => t('change.openid', 'Changed the sign-in provider'),
  'directory.synchronised': () => t('change.synchronised', 'Ran the directory synchronisation'),
  'settings.operational': () => t('change.operational', 'Changed operation and limits'),
  'settings.timezone': () => t('change.timezone', 'Changed the timezone'),
  'settings.holidays': () => t('change.holidays', 'Changed the public holidays'),
  'settings.datasource': () => t('change.datasource', 'Changed the database connection'),
  'settings.telemetry': () => t('change.telemetry', 'Changed logging, metrics and tracing'),
  'settings.push': () => t('change.push', 'Switched push notifications {1}'),
  'installation.restart': () => t('change.restart', 'Restarted the application'),
  'installation.update': () => t('change.update', 'Started an update'),
  'installation.setup': () => t('change.setup', 'Completed the setup'),
  'installation.backup': () => t('change.backup', 'Made a backup'),
  'installation.backupDownloaded': () => t('change.backupDownloaded', 'Downloaded a backup'),
  'installation.backupDeleted': () => t('change.backupDeleted', 'Deleted a backup'),
  'settings.backup': () => t('change.backupPlan', 'Changed when backups are made'),
  'calendar.created': () => t('change.calendarCreated', 'Created the calendar {0}'),
  'calendar.updated': () => t('change.calendarUpdated', 'Changed the calendar {0}'),
  'calendar.deleted': () => t('change.calendarDeleted', 'Deleted the calendar {0}'),
  'serviceLevel.created': () => t('change.serviceLevelCreated', 'Created the service level {0}'),
  'serviceLevel.updated': () => t('change.serviceLevelUpdated', 'Changed the service level {0}'),
  'serviceLevel.deleted': () => t('change.serviceLevelDeleted', 'Deleted the service level {0}'),
  'unlockStateTypes.changed': () => t('change.unlockStateTypesChanged', 'Changed where a lock runs out'),
  'macro.created': () => t('change.macroCreated', 'Created the macro {0}'),
  'macro.updated': () => t('change.macroUpdated', 'Changed the macro {0}'),
  'macro.deleted': () => t('change.macroDeleted', 'Deleted the macro {0}'),
  'execPlan.created': () => t('change.execPlanCreated', 'Created the execution plan {0}'),
  'execPlan.updated': () => t('change.execPlanUpdated', 'Changed the execution plan {0}'),
  'execPlan.deleted': () => t('change.execPlanDeleted', 'Deleted the execution plan {0}'),
  'job.created': () => t('change.jobCreated', 'Created the job {0}'),
  'job.updated': () => t('change.jobUpdated', 'Changed the job {0}'),
  'job.deleted': () => t('change.jobDeleted', 'Deleted the job {0}'),
  'job.run': () => t('change.jobRun', 'Ran the job {0}'),
  'template.created': () => t('change.templateCreated', 'Created the answer template {0}'),
  'template.updated': () => t('change.templateUpdated', 'Changed the answer template {0}'),
  'template.deleted': () => t('change.templateDeleted', 'Deleted the answer template {0}'),
  'notification.created': () => t('change.notificationCreated', 'Created the notification {0}'),
  'notification.updated': () => t('change.notificationUpdated', 'Changed the notification {0}'),
  'notification.deleted': () => t('change.notificationDeleted', 'Deleted the notification {0}'),
  'sysconfig.set': () => t('change.sysconfigSet', 'Changed the option {1}'),
  'sysconfig.reset': () => t('change.sysconfigReset', 'Put the option {1} back to its default'),
};

/** One change as a sentence: what was done, to whom, and the value it kept. */
function describeChange(entry) {
  // A class is named by its identifier, which the screen writes in the
  // reader's words wherever else it shows one.
  const named = entry.action.startsWith('classDefinition.') ? catalogValueTitle : (name) => name;
  const target = entry.target
    ? ((entry.target.name && named(entry.target.name)) || entry.detail || `#${entry.target.id}`)
    : '';
  const detail = {
    'user.role': () => roleTitle(entry.detail),
    'settings.maintenance': () => (entry.detail === 'true'
      ? t('change.on', 'on') : t('change.off', 'off')),
    'settings.push': () => (entry.detail === 'true'
      ? t('change.on', 'on') : t('change.off', 'off')),
  }[entry.action]?.() ?? entry.detail;

  const sentence = CHANGE_SENTENCES[entry.action];

  return sentence ? fillIn(sentence(), [target, detail]) : entry.action;
}

/**
 * The latest runs that removed or added an account, and nothing about whom.
 *
 * What lasts of a run that deleted people: the log names them for as long as the
 * log lasts, and this says that it happened - when, started how, and how much
 * went with them. Only for an account that may run one, as the server asks.
 */
async function loadSyncRuns() {
  const tbody = $('#table-sync-runs tbody');
  if (!tbody) return;

  const runs = await api('/settings/ldap/sync/runs');

  const rows = (runs.items ?? []).map((run) => el('tr', {},
    el('td', { text: fmtMoment(run.ranAt) }),
    el('td', {
      text: run.confirmed
        ? t('sync.byPerson', 'Confirmed against a preview')
        : t('sync.unattended', 'Schedule or script'),
    }),
    el('td', { class: 'num', text: String(run.deleted) }),
    el('td', { class: 'num', text: String(run.ticketsReleased) }),
    el('td', { class: 'num', text: String(run.created) }),
  ));

  fillTable(tbody, rows, 5, t('sync.noRuns', 'No run has removed or added an account yet.'));
}

/** The logo travels as a data URI, so it needs no upload endpoint. */
let pendingLogo = '';

/**
 * Shows the chosen image at the size the bar will give it, before saving.
 *
 * The left of the bar is the one place the logo is drawn - the sign-in screen
 * and the browser tab carry QubitDesk's own mark - and it draws the banner-sized
 * copy, so the preview shows the part chosen for the banner.
 */
function setLogoPreview(dataURI) {
  pendingLogo = dataURI;

  // Drawn here rather than by the server, because nothing has been saved yet -
  // the logo may not even have left the machine.
  const preview = $('#logo-preview');
  if (preview) preview.src = dataURI ? cropPreview(dataURI, logoCrops.banner, preview) : '';

  const uses = $('#logo-preview-uses');
  if (uses) uses.hidden = !dataURI;

  const hint = $('#logo-crop-hint');
  if (hint) hint.hidden = !dataURI;
}

/**
 * The chosen part of a logo, for a preview.
 *
 * The whole image where nothing has been chosen, which is the usual case and
 * costs nothing. Where something has, the part is cut out with a canvas - the one
 * piece of image work that happens in the browser, because the alternative is a
 * round trip per preview for a picture that has not been saved yet.
 */
function cropPreview(dataURI, crop, into) {
  // Both sides, not just the width: a selection may keep the full width of a
  // logo and half its height, and asking only about the width would answer that
  // with the whole image.
  if (!crop || (crop.w >= 1 && crop.h >= 1)) return dataURI;

  const source = new Image();

  source.onload = () => {
    const canvas = document.createElement('canvas');

    canvas.width = Math.max(1, Math.round(source.naturalWidth * crop.w));
    canvas.height = Math.max(1, Math.round(source.naturalHeight * crop.h));

    canvas.getContext('2d').drawImage(
      source,
      source.naturalWidth * crop.x, source.naturalHeight * crop.y,
      canvas.width, canvas.height,
      0, 0, canvas.width, canvas.height,
    );

    into.src = canvas.toDataURL('image/png');
  };

  source.src = dataURI;

  // Until it has loaded, the whole image: a preview that is briefly too generous
  // is better than one that is briefly empty.
  return dataURI;
}

function wireAdmin() {
  $('#changes-more').addEventListener('click', () => mutate(() => loadChanges(true), null, null));

  $('#logo-file').addEventListener('change', (e) => {
    const file = e.target.files?.[0];
    if (!file) return;

    // The accept list on the input is a filter in the file dialog and nothing
    // more - a file can still be dragged in, or chosen after switching the
    // dialog to "all files". So the type is checked here as well, before
    // anything is read.
    if (!['image/png', 'image/jpeg'].includes(file.type)) {
      toast(t('err.logoNotRaster', 'The logo must be a PNG or a JPEG.'), 'error');
      e.target.value = '';

      return;
    }

    // 256 KB matches the server-side limit; checking here gives a better
    // message than a rejected request would.
    if (file.size > 256 * 1024) {
      toast(t('admin.logoTooBig', 'The logo must be smaller than 256 KB.'), 'error');
      e.target.value = '';

      return;
    }

    const reader = new FileReader();

    reader.onload = () => {
      // A different logo means the parts chosen from the old one are about a
      // picture that is gone. Keeping them would crop the new logo by
      // coordinates somebody chose against another image.
      logoCrops = {};
      setLogoPreview(String(reader.result));
    };

    reader.readAsDataURL(file);
  });

  $('#logo-clear').addEventListener('click', () => {
    setLogoPreview('');
    $('#logo-file').value = '';
  });

  $('#form-branding').addEventListener('submit', (e) => {
    e.preventDefault();
    rememberBrandingDraft();

    // The base stays what is in the form for the language it was written in, so
    // an installation that never opens the switcher goes on working exactly as
    // it did - and a reader whose language has nothing written still gets
    // something rather than a blank header.
    const base = brandingDraft[BRANDING_LANGUAGES[0]] ?? brandingDraft[brandingLanguage] ?? {};

    const body = {
      ...formData(e.target),
      ...base,
      logo: pendingLogo,
      translations: brandingDraft,
      crops: logoCrops,
    };

    // No reload after it, though there was one for as long as the logo was the
    // tab's icon: every engine takes that from the document it was given, so a
    // changed logo meant a new document. The tab keeps QubitDesk's mark now, and
    // the one place the logo is drawn is redrawn by loadBranding like everything
    // else on the page.
    saveForm(e.target, async () => {
      const saved = await api('/settings/branding',
        { method: 'PUT', body: JSON.stringify(body) });

      // The copy the language chooser fills its boxes from, brought up to date
      // from what was just sent rather than from the load that follows: between
      // the two there is a window somebody can click in, and clicking in it
      // filled the boxes with the name from before the save - a wrong value
      // written by a screen that looked like it was showing the right one. What
      // was sent is what the server now holds, so there is nothing to wait for.
      brandingBase = { ...brandingBase, ...base };

      return saved;
    },
      t('admin.saved', 'Settings saved'),
      async () => {
        await loadBranding();
        await loadAdmin();
      });
  });

  $('#form-datasource').elements.dialect.addEventListener('change', (e) => {
    // A person picking a type takes it over from here; see
    // showRunningConnection, which follows the running connection until then.
    //
    // isTrusted is what tells the two apart: restoring a draft sets the value
    // and dispatches a change so the fields that depend on it are redrawn, and
    // that is the script speaking rather than somebody choosing.
    if (e.isTrusted) e.target.dataset.chosen = 'yes';

    syncDatasourceFields();
  });

  // The password box follows the account the card names; see followKeptPassword.
  const dsForm = $('#form-datasource');

  for (const name of ['host', 'port', 'user', 'password']) {
    dsForm.elements[name].addEventListener('input', () => followKeptPassword(dsForm));
  }

  dsForm.elements.dialect.addEventListener('change', () => followKeptPassword(dsForm));

  // Clicked into, it is empty for typing; left untouched, it is the kept one again.
  dsForm.elements.password.addEventListener('focus', (e) => {
    if (e.target.dataset.kept === 'yes') e.target.value = '';
  });
  dsForm.elements.password.addEventListener('blur', () => followKeptPassword(dsForm));

  $('#form-datasource').addEventListener('submit', (e) => {
    e.preventDefault();
    const body = datasourcePayload(e.target);

    saveForm(e.target,
      () => api('/settings/datasource', { method: 'PUT', body: JSON.stringify(body) }),
      null,
      // loadAdmin ends with loadRestart, so the card follows - and announceSave
      // reads what that just worked out, rather than promising a restart on
      // every press of a form somebody only opened to look at.
      async () => { await loadAdmin(); announceSave(); });
  });

  $('#form-ldap').addEventListener('submit', (e) => {
    e.preventDefault();
    saveForm(e.target,
      () => api('/settings/ldap', { method: 'PUT', body: JSON.stringify(ldapPayload()) }),
      t('admin.saved', 'Settings saved'),
      loadAdmin);
  });

  $('#form-sync-schedule').addEventListener('submit', (e) => {
    e.preventDefault();
    saveForm(e.target,
      () => api('/settings/ldap', { method: 'PUT', body: JSON.stringify(ldapPayload()) }),
      null,
      // The restart card too: the schedule is the one directory setting that
      // waits, because a scheduler is built while the application starts - and
      // only when it actually changed, which is what announceSave asks.
      async () => { await loadAdmin(); announceSave(); });
  });

  $('#datasource-test').addEventListener('click', () => {
    runConnectionTest($('#datasource-test-result'), () => api('/settings/datasource/test',
      { method: 'POST', body: JSON.stringify(datasourcePayload($('#form-datasource'))) }));
  });

  $('#ldap-test').addEventListener('click', () => {
    runConnectionTest($('#ldap-test-result'), () => api('/settings/ldap/test',
      { method: 'POST', body: JSON.stringify(ldapPayload()) }));
  });
}

/**
 * Tries a connection and reports the outcome under the card that asked for it.
 *
 * The waiting line has to be taken back by whichever way the attempt ends. It used
 * to be written before the request and overwritten only where the request came
 * back, and mutate() turns a refusal into a toast - so a database or directory that
 * would not answer left "Testing the connection …" standing underneath for as long
 * as the screen stayed open, beside an error that had already been read and
 * dismissed.
 *
 * A failure now says why in the same place a success does, because that is where
 * somebody who just pressed the button is looking.
 */
async function runConnectionTest(result, attempt) {
  result.textContent = t('admin.testing', 'Testing the connection …');
  result.className = 'muted';

  try {
    const outcome = await attempt();

    // A failure goes through the same renderer as any other refusal. Half of
    // them are a fixed complaint - a field left empty, a port that is not a
    // number - and those are said in the reader's language and name the fields
    // the way the labels above them do. The rest is what the driver said back,
    // which nobody can anticipate and nobody can translate: that gets a generic
    // sentence in the reader's language, with the driver's own words folded away
    // underneath for whoever is going to act on them.
    if (outcome.ok) {
      // A success is named here rather than by the server, which wrote it in
      // English and had it shown in preference to this sentence.
      result.replaceChildren(el('span', {
        text: t('admin.testOk', 'The connection works.'),
      }));
    } else {
      showRefusal(result, outcome.error ?? { message: outcome.message });
    }

    result.className = outcome.ok ? 'muted plus' : 'muted minus';
    sayAtLeastSomething(result);
  } catch (err) {
    showRefusal(result, err.refusal ?? { message: err.message });
    result.className = 'muted minus';
    sayAtLeastSomething(result);

    toastFailure(err);
  }
}

/**
 * Never nothing.
 *
 * A refusal with no words in it - a request that was aborted, an answer whose
 * message is empty - renders as an empty box, which is the one outcome that says
 * less than not having pressed the button at all. Whoever is looking at it is
 * waiting for an answer, and "no answer" is not one of the answers.
 *
 * Called on both ways out rather than only on the throw. That was the first
 * attempt at this and it covered half the paths: an answer that came back
 * perfectly well and said the attempt had failed, without saying anything about
 * why, went straight past it.
 */
function sayAtLeastSomething(result) {
  if (result.textContent.trim() !== '') return;

  result.textContent = t('err.internal', 'Something went wrong on this installation.');
}

/**
 * Runs a directory reconciliation, or previews one.
 *
 * The real run asks for confirmation naming the exact number of accounts it
 * deletes and of the unfinished tickets they leave without an owner, because it
 * cannot be undone.
 */
function wireDirectorySync() {
  const status = $('#sync-status');
  const result = $('#sync-result');

  // Drawn through redrawable, because everything it says is translated - the
  // counts, the abort reason, the empty row - and a language change is not a
  // reason to run a directory synchronisation again.
  const show = (report) => redrawable('directorySync', () => draw(report));

  const draw = (report) => {
    const rows = report.candidates.map((c) => el('tr', {},
      el('td', { class: 'text', text: c.name }),
      el('td', { text: c.email }),
      el('td', { class: `num ${c.tickets > 0 ? 'minus' : ''}`, text: String(c.tickets) }),
    ));

    fillTable($('#table-sync tbody'), rows, 3, t('sync.none', 'No accounts are missing from the directory.'));
    result.hidden = false;

    if (report.aborted) {
      const reason = describeRefusal({
        code: report.abortCode, message: report.aborted, values: report.abortValues,
      });

      status.textContent = `${t('sync.aborted', 'Aborted')}: ${reason}`;
      status.className = 'muted minus';

      return;
    }

    const parts = [
      `${t('sync.directoryUsers', 'In the directory')}: ${report.directoryUsers}`,
      `${t('sync.localExternal', 'Local, from the directory')}: ${report.localExternal}`,
    ];

    if (report.dryRun) {
      parts.push(`${t('sync.wouldDelete', 'Would be deleted')}: ${report.candidates.length}`);
      parts.push(`${t('sync.wouldCreate', 'Would be created')}: ${report.created.length}`);
    } else {
      parts.push(`${t('sync.deleted', 'Deleted')}: ${report.deleted.length}`);
      parts.push(`${t('sync.created', 'Created')}: ${report.created.length}`);
    }

    status.textContent = parts.join(' · ');
    status.className = 'muted';
  };

  $('#sync-preview').addEventListener('click', () => {
    status.textContent = t('sync.running', 'Checking …');
    status.className = 'muted';

    // Cleared on the way out, not only on the way through: a failure raises its
    // own notice, and "Checking …" left standing underneath it says the check is
    // still running when it has already finished badly.
    mutate(
      async () => show(await api('/settings/ldap/sync/preview', { method: 'POST' })),
      null,
      null,
    ).finally(() => {
      if (status.textContent === t('sync.running', 'Checking …')) status.textContent = '';
    });
  });

  $('#sync-run').addEventListener('click', () => {
    mutate(async () => {
      // Preview first, so the confirmation can state the real damage instead
      // of a vague warning.
      const preview = await api('/settings/ldap/sync/preview', { method: 'POST' });
      show(preview);

      if (preview.aborted) return;

      const tickets = preview.candidates.reduce((sum, c) => sum + c.tickets, 0);

      if (preview.candidates.length > 0) {
        const question = t('sync.confirm', 'WARNING: {n} account(s) will be deleted irreversibly, '
          + 'and {t} unfinished ticket(s) left without an owner. Continue?')
          .replace('{n}', String(preview.candidates.length))
          .replace('{t}', String(tickets));

        const proceed = await confirmDialog({
          title: t('sync.confirmTitle', 'Run the synchronisation?'),
          text: question,
          confirmLabel: t('sync.run', 'Run synchronisation'),
        });

        if (!proceed) return;
      }

      // Bound to what was just put in front of somebody, including nobody. The
      // run asks the directory again, and without this it deleted whatever the
      // second answer left out - accounts nobody had been shown, or, after a
      // preview that asked nothing because it proposed nobody, up to the deletion
      // limit. The server refuses a run whose candidates are not these.
      const confirmed = preview.candidates.map((c) => c.userId).join(',');

      try {
        show(await api(`/settings/ldap/sync?confirmed=${confirmed}`, { method: 'POST' }));
      } catch (err) {
        // A run that stops part-way has still deleted what it reached, and the
        // card went on showing the preview it started from - accounts that
        // "would be deleted", some of them already gone. What is left is asked
        // for again; the refusal itself says how far the run got. A preview that
        // cannot be had either leaves the card as it was rather than hiding why.
        await api('/settings/ldap/sync/preview', { method: 'POST' }).then(show, () => {});
        throw err;
      }

      await refreshAll();
    }, null, null);
  });
}

/** Reads the LDAP form, including its checkboxes and numeric port. */
function ldapPayload() {
  const form = $('#form-ldap');
  const body = {
    host: form.elements.host.value.trim(),
    port: Number(form.elements.port.value || 389),
    baseDn: form.elements.baseDn.value.trim(),
    bindDn: form.elements.bindDn.value.trim(),
    bindPassword: form.elements.bindPassword.value,
    userFilter: form.elements.userFilter.value.trim(),
    nameAttribute: form.elements.nameAttribute.value.trim(),
    emailAttribute: form.elements.emailAttribute.value.trim(),
    idAttribute: form.elements.idAttribute.value.trim(),
    defaultRole: form.elements.defaultRole.value,
  };

  for (const flag of ['enabled', 'startTls', 'useTls', 'skipVerify']) {
    body[flag] = form.elements[flag].checked;
  }

  // The schedule lives in its own card, next to the buttons that run the thing
  // it schedules, but it is stored with the rest of the directory settings and
  // travels on the same request - so it has to be in every payload or saving the
  // connection would clear it.
  const schedule = $('#form-sync-schedule');
  body.syncSchedule = schedule ? schedule.elements.syncSchedule.value.trim() : '';

  return body;
}

// ------------------------------------------------------------- mutations

/**
 * Wraps a mutating call so every failure surfaces as a toast, not a crash.
 *
 * Two failures, and they are not the same thing - which is the reason for the
 * two blocks below rather than the one this used to have. The call can fail, and
 * then nothing happened. Or the call can succeed and the reload behind it fail,
 * and then everything happened and the screen simply has not caught up.
 *
 * Both used to come out as the same red toast, carrying whatever the *read* said,
 * immediately after the green one saying the save had worked. refreshAll is the
 * `after` most callers pass and it awaits a dozen loads with no handling of its
 * own, so any one of them rejects it.
 *
 * What that costs is a retry. This application already reasons about that hazard
 * where it decides not to abort a write mid-flight - "somebody would do it again,
 * which for an import means writing every row twice" - and telling somebody that
 * a save which worked did not invites exactly that. The two other callers of
 * refreshAll had the distinction already, and the sentence for it.
 */
async function mutate(fn, successMessage, after) {
  let result;

  try {
    // Handed to `after`, so a caller that needs what the call answered does not
    // have to make the call again or smuggle it out through a closure. Every
    // existing caller ignores it, which is what makes this safe to add.
    result = await fn();
  } catch (err) {
    // A second press of a write still on its way: the first one is the answer,
    // and it is still coming.
    if (err.repeated) return;

    // Silent while the application is restarting into a new version. Every
    // request fails for those few seconds, and each one would raise its own red
    // toast on top of a banner that already says exactly what is happening. The
    // banner is the message; these would be noise piled on it.
    if (duringARestart()) return;

    toastFailure(err);

    return;
  }

  // Before the reload, because it is the answer to what was asked and the reload
  // is not: a slow refresh would otherwise hold back the one word confirming the
  // save landed.
  if (successMessage) toast(successMessage, 'ok');

  if (!after) return;

  try {
    await after(result);
  } catch (err) {
    if (duringARestart()) return;

    // Named as what it is. The save is done and is not coming undone; what
    // failed is the screen catching up, and the way out of that is to load the
    // page again rather than to save a second time.
    toastFailure(err, t('msg.loadFailed', 'Could not load everything'));
  }
}

const patch = (path, body, msg, after) => mutate(
  () => api(path, { method: 'PUT', body: JSON.stringify(body) }), msg, after);

const remove = (path, msg, after) => mutate(
  () => api(path, { method: 'DELETE' }), msg, after);

/**
 * Asks a yes-or-no question, and resolves to the answer.
 *
 * Replaces window.confirm, which the browser draws itself: an unstyled box
 * naming the origin, unreadable in a dark theme, and impossible to translate
 * beyond the message inside it. It also blocks the whole page, which is why the
 * old maintenance-mode call had to put the checkbox back by hand afterwards.
 *
 * The markup is built rather than written into index.html, because a question
 * differs only in its words - and reusing .overlay and .overlay-card means this
 * needs no new styling at all.
 */
function confirmDialog({
  title, text, detail, confirmLabel, danger = true, field = null, choices = null, empty = '',
}) {
  return new Promise((resolve) => {
    // What had focus before, so it can be given back: a dialog that leaves
    // focus on nothing strands anybody using a keyboard.
    const previous = document.activeElement;

    const card = el('div', {
      class: 'overlay-card confirm-card',
      role: 'dialog',
      'aria-modal': 'true',
    });

    card.append(el('h2', { text: title }));
    card.append(el('p', { class: 'muted', text }));

    // The server's own words, when there are any - the count of what would be
    // destroyed usually lives there rather than in the question.
    if (detail) card.append(el('p', { class: 'muted minus', text: detail }));

    // A question that needs an answer rather than a yes.
    //
    // Here rather than in a second dialog written beside this one: a question is
    // a card, a sentence and two buttons whatever it asks, and the one that came
    // after this had all three of those and a box. What it resolves to is the
    // answer - the string typed, or false for a cancel - so a caller reads
    // truthiness exactly as every existing one already does.
    const input = field
      ? el('input', {
        type: 'password',
        id: field.id,
        minlength: String(field.minLength ?? 0),
        autocomplete: 'off',
        spellcheck: 'false',
      })
      : null;

    // A choice of several rather than a yes: switches, and what it resolves to is
    // the values switched on - an empty list too, which is an answer ("nobody")
    // where a cancel is not. empty is said instead where there is nothing to choose.
    const ticks = (choices ?? []).map((choice) => el('input', {
      type: 'checkbox',
      value: String(choice.value),
      checked: Boolean(choice.checked),
    }));

    if (choices?.length) {
      card.append(el('div', { class: 'choice-list' }, ...choices.map((choice, i) => el('label',
        { class: 'switch' }, ticks[i], el('span', { class: 'switch-track' }),
        el('span', { class: 'switch-label', text: choice.label })))));
    } else if (choices && empty) {
      card.append(el('p', { class: 'muted', text: empty }));
    }

    if (field) {
      const controls = el('span', { class: 'row' }, input);

      if (field.generate) {
        controls.append(el('button', {
          type: 'button',
          class: 'secondary',
          id: field.generateID,
          text: field.generate,
          onclick: () => {
            input.value = invented();

            // And shown, through the reveal this field already has rather than a
            // second control beside it. A password that has to be read out or
            // written down and cannot be looked at is worse than none - and
            // clicking the eye afterwards is a step nobody should have to know
            // about.
            const eye = card.querySelector('.password-toggle');
            if (eye && input.type === 'password') eye.click();

            input.focus();
          },
        }));
      }

      card.append(el('label', { text: field.label }, controls));

      if (field.hint) card.append(el('p', { class: 'muted', text: field.hint }));
    }

    const overlay = el('div', { class: 'overlay confirm-overlay' }, card);

    const close = (answer) => {
      document.removeEventListener('keydown', onKey);
      overlay.remove();

      if (previous instanceof HTMLElement) previous.focus();

      resolve(answer);
    };

    // Escape cancels, which is what a dialog with a destructive option should
    // do with an ambiguous keypress.
    const onKey = (event) => {
      if (event.key === 'Escape') close(false);
    };

    const cancel = el('button', {
      class: 'secondary',
      type: 'button',
      text: t('action.cancel', 'Cancel'),
      onclick: () => close(false),
    });

    const proceed = el('button', {
      class: `confirm-proceed${danger ? ' danger' : ''}`,
      type: 'button',
      text: confirmLabel,
      onclick: () => {
        if (field) close(input.value);
        else if (choices) close(ticks.filter((tick) => tick.checked).map((tick) => tick.value));
        else close(true);
      },
    });

    card.append(el('div', { class: 'row confirm-actions' }, cancel, proceed));

    document.addEventListener('keydown', onKey);
    document.body.append(overlay);

    // The reveal button every password field on this screen gets, given to this
    // one too - by the same function, so there is one of them rather than two
    // that drift.
    if (input) wirePasswordReveal(card);

    // Cancel takes the focus, not the destructive button: a stray Enter should
    // not delete anything. Where something has to be typed, the box takes it
    // instead - there is nothing to protect anybody from until it holds a value,
    // and a dialog that opens with the cursor somewhere else is one more click.
    if (input) input.focus();
    else if (ticks.length) ticks[0].focus();
    else cancel.focus();
  });
}

/**
 * The shortest password the server will hash.
 *
 * The same number as security.MinPasswordLength, and written here because the
 * form has to refuse what the server refuses before somebody presses the button
 * rather than after.
 */
const MIN_PASSWORD_LENGTH = 8;

/**
 * A password nobody chose.
 *
 * For handing an account back to somebody who has lost theirs. A password a
 * person invents under mild pressure is a password much like the last one they
 * invented, and this one is going to be spoken down a telephone or typed into a
 * chat window before it is replaced - so it is worth it being neither guessable
 * nor a variation on a theme.
 *
 * crypto.getRandomValues rather than Math.random, which is not required to be
 * unpredictable and on some engines is trivially so.
 *
 * Rejection rather than a remainder. Taking a byte modulo the alphabet length
 * makes the first few characters likelier than the rest, which is a small bias
 * and a pointless one when discarding the overhang costs a loop.
 *
 * The alphabet leaves out the characters that are read wrongly rather than
 * typed wrongly: no O and no zero, no I, l or one. This value is transcribed by
 * a person at least once, and "did you say ell or one" is the failure it would
 * otherwise produce.
 */
function invented() {
  const alphabet = '23456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz';
  const limit = 256 - (256 % alphabet.length);
  const out = [];

  while (out.length < 16) {
    const bytes = new Uint8Array(16);

    window.crypto.getRandomValues(bytes);

    for (const byte of bytes) {
      if (byte >= limit || out.length >= 16) continue;

      out.push(alphabet[byte % alphabet.length]);
    }
  }

  // In groups, because it is going to be read aloud or copied by eye once.
  return out.join('').replace(/(.{4})(?=.)/g, '$1-');
}

/**
 * The question asked before something is deleted for good.
 *
 * One wording for all of them, with the thing being deleted named, so the
 * question reads the same wherever it comes from - and so that five delete
 * buttons which asked nothing at all now ask the same thing.
 */
function confirmDelete(what) {
  return confirmDialog({
    title: t('confirm.deleteTitle', 'Delete for good?'),
    text: `${what} ${t('confirm.deleteText', 'will be deleted. This cannot be undone.')}`,
    confirmLabel: t('action.delete', 'delete'),
  });
}

/** remove() with the question in front of it. */
const removeAfterConfirm = async (what, path, msg, after) => {
  if (await confirmDelete(what)) await remove(path, msg, after);
};

/**
 * Deletes a user, asking first when it would leave tickets without an owner.
 *
 * The server refuses that deletion rather than performing it, and its refusal
 * carries the number of unfinished tickets the account owns. So the question put
 * to the administrator has the real number in it rather than a general warning:
 * "42 tickets" is a different decision from "some tickets may be affected", and
 * only the server knows which it is.
 *
 * An account owning no unfinished ticket is deleted straight away. Asking about
 * something with no consequence trains people to click through the dialog that
 * does have one.
 */
async function deleteUser(user) {
  const done = t('msg.userDeleted', 'User deleted');

  try {
    await api(`/users/${user.id}`, { method: 'DELETE' });
    toast(done, 'ok');
    await refreshAll();

    return;
  } catch (err) {
    if (err.status !== 409) {
      toastFailure(err);

      return;
    }

    // The refusal explains what is attached, so it becomes the question - the
    // server's own sentence as the detail, because it carries the count, and
    // "42 tickets" is a different decision from "some tickets may be affected".
    const proceed = await confirmDialog({
      title: t('confirm.deleteTitle', 'Delete for good?'),
      text: t('user.deleteConfirm', 'Delete anyway? The tickets it owns stay, without an owner.'),
      detail: err.message,
      confirmLabel: t('action.delete', 'delete'),
    });

    if (!proceed) return;
  }

  mutate(
    () => api(`/users/${user.id}?confirm=true`, { method: 'DELETE' }),
    done,
    refreshAll);
}

// ------------------------------------------------------------------ sign-in

/**
 * Whether the sign-in screen has been given up for a session.
 *
 * Set the moment a password is accepted and cleared only by signing out, which
 * makes it true across the gap that whether an account is loaded yet cannot
 * cover. showLogin is the only reader; the two writers are below and in
 * doLogout.
 */
let handedToASession = false;

/** Shows the sign-in overlay and hides the application behind it. */
function showLogin(message) {
  // Never over a session this page has already been given to.
  //
  // The first load starts before there is one and fails because there is none.
  // The sign-in form is wired and usable from the first paint, though, so
  // anybody quick - or any machine slow enough for the two to overlap - signs in
  // underneath it, and that failure then arrives after theirs succeeded. The
  // result is a page that is signed in, fully loaded and on a screen, with a
  // sign-in form laid across all of it. Firefox in CI reported it four times.
  //
  // Asked of handedToASession rather than of me.user, which is what this asked
  // first and was not enough. A sign-in takes this screen down the moment the
  // password is accepted and only fills in the account afterwards, so between
  // those two there is a page that has been signed into and cannot prove it -
  // and that is exactly the window the stale failure lands in.
  if (handedToASession) return;

  const screen = $('#login-screen');
  screen.hidden = false;
  placeFooter();
  // The check is over, whatever it concluded: show the form rather than the
  // spinner that stood in for it.
  screen.classList.remove('checking');

  const error = $('#login-error');
  error.textContent = message ?? '';
  error.hidden = !message;

  if ($('#public-faq').hidden) offerPublicFAQ();
}

function hideLogin() {
  // From here this page belongs to a session, whether or not the account has
  // arrived yet. See showLogin, which is the only reader.
  handedToASession = true;

  // A new session has not been greeted yet, whatever the last one was told.
  // Cleared here rather than on the way out, because signing in is the moment
  // the question becomes open again - and the marker outliving a sign-out would
  // tell anything watching that the next account's tour had already been
  // decided. See greetAfterSignIn.
  delete document.documentElement.dataset.greeted;

  $('#login-screen').classList.remove('checking');
  $('#login-screen').hidden = true;
  placeFooter();
  $('#login-error').hidden = true;
  backToThePasswordForm();
  $('#form-login').reset();

  if (!$('#public-faq').hidden) closePublicFAQ();
  $('#login-faq-offer').hidden = true;
}

/**
 * Puts the footer inside the sign-in screen while that screen is up, after the
 * form, and back at the end of the page when it is not.
 *
 * It used to be raised over the bottom of the window instead, with the screen
 * stopping where it began - which on a short window gave the footer the window.
 * With a legal notice configured that was 135 of a sideways telephone's 320
 * pixels and 204 of an upright one's 568, the form scrolling in what was left
 * with its buttons below the fold of it. Inside the screen the footer is where it
 * was on a window with room, the bottom edge, and after the last button on one
 * without, reached by scrolling the form rather than taking room from it.
 *
 * Moved rather than copied: one footer, and one place the version, the source
 * link and the installation's legal notice are written to. Until the script
 * runs, the stylesheet raises it as before - see .site-footer.
 */
function placeFooter() {
  const footer = $('#site-footer');
  const screen = $('#login-screen');
  if (!footer || !screen) return;

  if (!screen.hidden) {
    if (footer.parentElement !== screen) screen.append(footer);
  } else if (footer.parentElement === screen) {
    $('main').after(footer);
  }
}

async function submitLogin(e) {
  e.preventDefault();

  const form = e.target;

  // The second step of a ticket sign-in, which asks for the code and nothing
  // else. See askForTheCodeAfterKerberos.
  if (form.dataset.via === 'ticket') {
    await signInWithKerberos({ totp: form.elements.totp.value.trim() });

    return;
  }

  const body = {
    email: form.elements.email.value.trim(),
    password: form.elements.password.value,
    totp: form.elements.totp.value.trim(),
  };

  // Only the sign-in call itself decides whether the credentials were wrong.
  //
  // Wrapping the loading that follows in the same catch made a correct password
  // look like a wrong one: with the initial password still in place the server
  // refuses the rest of the API, refreshAll threw, and the handler put the
  // sign-in screen back with "email address or password is not correct" - on
  // the one account that had just authenticated successfully. There was no way
  // through to the screen that would have fixed it.
  let result;

  try {
    result = await api('/auth/login', { method: 'POST', body: JSON.stringify(body) });
  } catch {
    // The server deliberately does not say which part was wrong, so neither
    // does the interface.
    showLogin(t('login.failed', 'Email address or password is not correct.'));
    form.elements.password.value = '';

    return;
  }

  // The password was right but the account has a second factor: ask for the
  // code instead of reporting a failed sign-in.
  if (result.totpRequired) {
    showCodeField($('#login-totp-field'), true);
    $('#login-error').textContent = t('login.totpNeeded', 'Please enter the code from your authenticator app.');
    $('#login-error').hidden = false;
    form.elements.totp.focus();

    return;
  }

  await arriveAfterSignIn();
}

/**
 * Takes a page that has just been signed into from the sign-in screen to the
 * application: the same arrival whichever way somebody signed in.
 *
 * One function for the password, the passkey and the ticket. The passkey kept a
 * copy of these lines that described itself as "the same arrival the password
 * sign-in makes", and a third copy is how three ways in start arriving
 * differently.
 */
async function arriveAfterSignIn() {
  hideLogin();

  try {
    await refreshAll();

    // The same choice a page load makes, rather than always the first tab: signing
    // out and back in used to discard the screen somebody was working on, so the
    // only way back to it was to reload the page afterwards. Through
    // openTheStartingView rather than switchView, so the page does not call itself
    // loaded while it is still about to change screens - see the comment there.
    openTheStartingView();

    // Here as well as on a page load, or the greeting would only ever appear to
    // somebody who reloaded after signing in - which is nobody's first sign-in.
    await greetAfterSignIn();
  } catch (err) {
    // Signed in, but something behind it would not load. Staying on the
    // application with an explanation beats being thrown back to a sign-in
    // screen that will accept the same password and do this again.
    toastFailure(err, t('msg.loadFailed', 'Could not load everything'));
  }
}

/** Whether this installation offers a sign-in with the browser's Kerberos ticket. */
let kerberosOffered = false;

/**
 * Where a tab remembers not to try a ticket by itself.
 *
 * Set by signing out, or reloading the sign-in screen would sign straight back
 * in and signing out would be something this page could not do. Set as well by
 * an attempt of its own that failed: on Windows a browser that does not trust
 * this address may answer the challenge with a password dialog of its own, and
 * one of those on every reload would be all the sign-in screen did. A ticket
 * sign-in that works clears it. sessionStorage, so it is this tab's and ends
 * with it.
 */
const KERBEROS_HELD_BACK_KEY = 'qubitdesk.kerberos.heldBack';

/** Whether the page may try the ticket before anybody asks it to. */
function mayTryKerberosUnasked() {
  try {
    return window.sessionStorage.getItem(KERBEROS_HELD_BACK_KEY) === null;
  } catch {
    // Without storage a sign-out could not be remembered past a reload, so the
    // page would sign straight back in. The button still works.
    return false;
  }
}

function holdKerberosBack() {
  try {
    window.sessionStorage.setItem(KERBEROS_HELD_BACK_KEY, '1');
  } catch {
    // Nothing to remember it in; mayTryKerberosUnasked already says no.
  }
}

function letKerberosTryAgain() {
  try {
    window.sessionStorage.removeItem(KERBEROS_HELD_BACK_KEY);
  } catch {
    // Nothing was stored, so nothing is held back by it.
  }
}

/**
 * Asks the server whether to offer a sign-in with the browser's ticket.
 *
 * Offered only where one can succeed - a keytab was read and a directory is
 * configured - so a browser is never sent the Negotiate challenge by an
 * installation that could do nothing with the answer.
 */
async function loadKerberosSupport() {
  try {
    kerberosOffered = Boolean((await api('/auth/kerberos'))?.available);
  } catch {
    kerberosOffered = false;
  }

  $('#login-kerberos').hidden = !kerberosOffered;
}

/**
 * Signs in with the browser's Kerberos ticket.
 *
 * The browser does the work. The POST is answered with the Negotiate
 * challenge, a browser that trusts this address and holds a ticket sends the
 * same request again with it, and one that does not keeps the 401 - which
 * cannot be told apart from here from a ticket the server refused, and does not
 * need to be.
 *
 * quietly is the attempt the page makes by itself when it opens: a failure says
 * nothing, because the form is the answer, and holds the ticket back for the
 * rest of the tab. Asked for with the button, a failure is said.
 */
async function signInWithKerberos({ quietly = false, totp = '' } = {}) {
  let result;

  try {
    result = await api('/auth/kerberos', {
      method: 'POST',
      body: JSON.stringify(totp ? { totp } : {}),
    });
  } catch (err) {
    if (quietly) {
      holdKerberosBack();

      return;
    }

    let message = err.message;

    if (err.status === 401) {
      message = totp
        ? t('kerberos.codeFailed', 'The sign-in was not accepted. Please check the code.')
        : t('kerberos.failed', 'Single sign-on did not work: the browser presented no ticket this installation accepts, or the directory does not know the account.');
    }

    showLogin(message);

    if (totp) {
      const field = $('#form-login').elements.totp;
      field.value = '';
      field.focus();
    }

    return;
  }

  // The ticket was good and the account has a second factor: the ticket stands in
  // for the password and for nothing else.
  if (result?.totpRequired) {
    askForTheCodeAfterKerberos();

    return;
  }

  letKerberosTryAgain();
  await arriveAfterSignIn();
}

/**
 * Turns the sign-in form into the second step of a ticket sign-in: the code, and
 * nowhere to type an address or a password.
 *
 * The same form rather than a second one, so the band, the language and the
 * button stay where they are; what changes is where it is sent, which
 * submitLogin reads off data-via. The address and the password are put away
 * and stop being required - a hidden field that is still required cannot be
 * submitted past, and the form would silently do nothing.
 */
function askForTheCodeAfterKerberos() {
  const form = $('#form-login');

  form.dataset.via = 'ticket';

  for (const name of ['email', 'password']) {
    form.elements[name].required = false;
    form.elements[name].closest('label').hidden = true;
  }

  showCodeField($('#login-totp-field'), true);
  $('#login-kerberos').hidden = true;
  $('#login-passkey').hidden = true;
  $('#login-kerberos-leave').hidden = false;

  const error = $('#login-error');
  error.textContent = t('login.totpNeeded', 'Please enter the code from your authenticator app.');
  error.hidden = false;

  form.elements.totp.focus();
}

/**
 * Puts the sign-in form back to asking for an address and a password.
 *
 * From the button, and from hideLogin, so a form left asking for a ticket's code
 * is not what the next person to sign in on this page finds.
 */
function backToThePasswordForm() {
  const form = $('#form-login');

  delete form.dataset.via;

  for (const name of ['email', 'password']) {
    form.elements[name].required = true;
    form.elements[name].closest('label').hidden = false;
  }

  form.elements.totp.value = '';
  showCodeField($('#login-totp-field'), false);
  $('#login-kerberos').hidden = !kerberosOffered;
  $('#login-passkey').hidden = !passkeysAvailable;
  $('#login-kerberos-leave').hidden = true;
  $('#login-error').hidden = true;
}

/** The two buttons a ticket sign-in adds to the form. */
function wireKerberosSignIn() {
  $('#login-kerberos').addEventListener('click', () => signInWithKerberos());
  $('#login-kerberos-leave').addEventListener('click', backToThePasswordForm);
}

/**
 * Puts the screen back to how it looks for somebody who has never been here.
 *
 * Appearance is chosen per device, which is right while somebody is using it and
 * wrong the moment they leave: the next person at that machine got the last
 * one's dark mode, on a screen that has nothing else of theirs on it. Back to
 * following the time of day, which is what a fresh browser does.
 *
 * What is not cleared, because none of it can reach anybody else: the screen
 * each account was last on, whether it has been greeted, and which release it
 * has dismissed - all three stored against that account's own id - and the
 * instance's own name and mark, which belong to the installation rather than to
 * a person.
 */
function forgetTheLastAppearance() {
  // Through the same function the picker uses, so there is one place that knows
  // what choosing an appearance means: it clears the stored choice and applies
  // the automatic one at once, rather than at the next load.
  setThemePreference('auto');

  const picker = $('#theme-picker');
  if (picker) picker.value = 'auto';

  // And the language switcher, which is a per-account setting and was left
  // showing the account that has gone. It sits behind the sign-in screen, so
  // nobody was looking at a control that disagreed with the page - but it is a
  // control that disagreed with the page, and the next thing it does is be
  // right for somebody else.
  const language = $('#language-picker');
  if (language) language.value = activeLanguage();

}

/**
 * Drops everything on screen and in hand that belonged to whoever just left.
 *
 * Two things survived a sign-out, and the second is the one that matters.
 *
 * The address bar keeps the screen somebody was on - switchView writes it there
 * so a reload comes back to the same place and a link can be sent to somebody.
 * Nothing cleared it, and the starting view prefers the address bar over the
 * screen this account was last on, so signing in as somebody else landed on the
 * previous person's screen. Signing in as the *same* account hid it, because
 * both answers agreed.
 *
 * And the tables kept their rows. Every loader begins by checking a right and
 * returning if it is absent, which is correct for loading and wrong for what was
 * already loaded: an ordinary account signing in after an administrator found
 * loadUsers returning at once and the administrator's list of accounts still in
 * the document underneath a tab that is merely hidden. The API never answered
 * them anything - the rows were already there.
 *
 * So both are cleared here rather than at the next sign-in. Sign-out is the
 * moment this stops being anybody's data, and leaving it to the next arrival
 * means it sits on the sign-in screen in the meantime.
 */
function forgetTheLastAccount() {
  // replaceState rather than assigning: assigning pushes a history entry, and
  // Back would then return to a screen belonging to the session that ended.
  if (currentHashView()) {
    history.replaceState(null, '', window.location.pathname + window.location.search);
  }

  cache.users = [];
  cache.roles = [];
  cache.permissions = [];
  cache.queues = [];
  cache.ticketQueues = [];
  cache.states = [];
  cache.priorities = [];
  cache.types = [];
  cache.found = {};
  cache.sysconfig = [];
  cache.calendars = [];
  cache.levels = [];
  cache.macros = [];
  cache.macroSteps = [];
  cache.stepChoices = { queues: [], states: [], priorities: [], types: [], accounts: [] };
  cache.plans = [];
  cache.planEvents = [];
  cache.jobs = [];
  cache.notifying = notifyingNothing();
  cache.templates = [];
  cache.templateQueues = [];
  cache.ticketTemplates = [];
  cache.faqCategories = [];
  cache.faqVisibilities = [];
  closeFAQArticle();
  closePortalTicket();
  generalCatalogClasses = [];
  resetGeneralCatalogForm();
  ciClasses = [];
  $('#form-classdef').reset();
  $('#table-classdef-versions tbody').replaceChildren();
  assetLists.classes = [];
  assetLists.deployment = [];
  assetLists.incident = [];
  assetLists.catalogs.clear();
  assetLists.definitions.clear();
  closeConfigItem();
  $('#form-configitem-filter').reset();
  itemPaging.page = 1;
  Object.keys(itsmChangeLists).forEach((list) => { itsmChangeLists[list] = []; });
  closeItsmChange();
  $('#form-itsmchange-filter').reset();
  itsmChangePaging.page = 1;
  closePublishedFAQ('portal');
  $('#form-portal-faq').reset();
  $('#portal-faq-list').replaceChildren();
  publishedCategories.portal = [];
  $('#told-all-read').hidden = true;

  // Emptied rather than left to the next render, which may never come: a table
  // whose loader returns early keeps whatever it last held.
  //
  // The ones a loader fills, which are the ones named by an id. Every table went
  // before, and one of them is not data at all: the legend under Appearance that
  // lists what may be written in a banner is part of the markup, filled by
  // nobody, and once emptied it stayed empty for the rest of the session - a
  // heading and a closing sentence with the six lines they explain gone from
  // between them.
  for (const body of $$('table[id] tbody')) body.replaceChildren();

  // And the lists, which are not tables and were left standing: a loader that
  // returns at its first line when the right is absent leaves the last person's
  // choices in every list it filled.
  //
  // Only the ones a loader filled, by the mark fillSelect leaves. The rule is the
  // same one the tables above are chosen by, and for the same reason - the legend
  // under Appearance was emptied once by a sweep that took everything, and stayed
  // empty for the rest of the session.
  for (const list of $$('select[data-filled]')) {
    list.replaceChildren();
    delete list.dataset.filled;
  }

  // The selection bars belong to those rows and would otherwise stand over an
  // empty table offering to delete three things.
  for (const bar of $$('.bulk-bar')) bar.remove();

  // And the ticket that was open, which is neither.
  forgetTheOpenTicket();

  // And an enrolment somebody was half way through. The panel holds a shared
  // secret, the otpauth URI that carries it together with the account's address,
  // and a QR code encoding both - and the code says twice that none of them may
  // hang about: renderTOTPState clears them because "the code encodes the secret,
  // so it must not survive the enrolment it belongs to", and switchView calls it
  // on the way out of Settings because an enrolment "has no business sitting on a
  // screen somebody has walked away from".
  //
  // Signing out is the strongest form of walking away and was the one way out
  // that did not call it. The next sign-in clears it through loadMe, so what was
  // left was the window in between: the sign-in form, with somebody's second
  // factor in the document behind it.
  renderTOTPState();

  // And a token's value, for the same reason and with more at stake: this one is
  // a working credential rather than an enrolment nobody finished.
  forgetTheTokenValue();

  // And the installation's address as a code, so that no code outlives a session.
  forgetTheAndroidCode();

  // And what the provider card said about the account's link.
  forgetTheOpenIDLink();

  // And any password left showing. The forms are reset on the way out, which
  // takes the value and leaves the box a text field - so the next person types
  // theirs into one that shows it.
  hideEveryRevealedPassword();

  // And the greeting, which names whoever it greeted and is drawn again only
  // when somebody arrives on it.
  $('#welcome-title').textContent = '';

  // And each form's own reset, which resetting the form does not reach: a hidden
  // field keeps its value through one, so a form left correcting a record kept
  // the record's id under its "edit" heading - and an administrator filling it in
  // to add somebody changed the account their predecessor had open. Every form
  // holding a hidden id, which is how the sign-out case finds them; after the
  // caches and the account are gone, because some of them draw from those.
  resetUserForm();
  resetRoleForm();
  resetQueueForm();
  for (const kind of Object.keys(CATALOGS)) resetCatalogForm(kind);
  for (const kind of Object.keys(CUSTOMER_FORMS)) resetCustomerForm(kind);
  resetSysConfigForm();
  resetCalendarForm();
  resetSlaForm();
  resetMacroForm();
  resetPlanForm();
  resetJobForm();
  resetNotificationForm();
  resetTemplateForm();
  resetFAQArticleForm();
  resetFAQCategoryForm();
  resetTicketForm();

  // And the imports this account had checked. The cards are not forms, so the
  // reset of every form never reached the file, the verdict or the button that
  // writes it.
  forgetEveryImport();

  // Last, after the tables are emptied: the sign-out applies the language next,
  // and every draw still registered would put its account's rows back.
  forgetEveryRedraw();
}

/**
 * Whether a session is already being ended, so one ending is not started by the
 * failures it causes.
 */
let endingTheSession = false;

/**
 * Tells the server the session is over, and says nothing if it cannot.
 *
 * Its own function because two things end a session from this side and only one
 * of them is a person pressing a button: the other is maintenance mode turning
 * an account away, which happens inside a failed request and must not wait for
 * this one to answer before the screen changes.
 */
async function endTheSessionQuietly() {
  try {
    await api('/auth/logout', { method: 'POST' });
  } catch {
    // Even a failed call should drop the client back to the sign-in screen.
  }
}

async function doLogout() {
  // While the session still is one: the installation is told with it.
  await forgetThisBrowsersPush();
  await endTheSessionQuietly();

  // Somebody who signs out means it: a reload of the sign-in screen must not
  // sign them straight back in with the ticket. Here rather than wherever a
  // session ends, because a session the server ended is not a choice anybody
  // made.
  holdKerberosBack();

  handBackTheScreen();
}

/**
 * Puts the page back the way somebody who is not signed in should find it.
 *
 * Everything doLogout did after telling the server, which is everything that has
 * to happen when a session ends for any reason. It ends for two: somebody presses
 * the button, or the server stops accepting the cookie - a lifetime that ran out,
 * an idle timeout, an administrator ending the session, a password changed
 * elsewhere.
 *
 * One function for both, because the second used to do none of it. An expired
 * session left the whole interface standing: every screen still drawn, every
 * poller still asking, the previous account's name in the corner - and a red
 * notice, once per click, saying to sign in again on a screen with nowhere to do
 * it.
 */
function handBackTheScreen(message) {
  // Before the state is cleared: both pollers ask with the session that is
  // about to end, and a timer left running would keep asking with none and
  // paint the screen with authentication failures. The announcement stream goes
  // the same way - the server turns an unauthenticated one away, and EventSource
  // would keep reopening it.
  stopLogPolling();
  stopPermissionPolling();
  stopAnnouncements();
  stopReleaseWatch();

  // And the walk through, which is not a timer but outlives the session in the
  // same way and costs more when it does. It seals the page on purpose - a
  // blocker over the viewport and inert on every one of the body's children -
  // and sealThePage(false) is reached from endTour alone. So a session ending
  // underneath it left the sign-in screen dimmed and inert, with a bubble over
  // it describing a step of the account that had gone.
  //
  // Reachable without anybody doing anything: a poll answered with 401 after a
  // right is withdrawn, or maintenance mode turning on. The way out is the
  // tour's own bubble, which is spared from the seal - and the tour is what an
  // account meets on its first sign-in, so the person most likely to be in it is
  // the one least likely to work that out.
  //
  // Put away rather than ended: this walk was interrupted, and recording it as
  // seen would spend the one time it is offered on a session that stopped.
  putTheTourAway();

  // And anything else painted over the whole page. There are three - the crop
  // editor, the restart wait and the setup wizard - each fixed at inset 0 over
  // everything, and each taken down by its own flow rather than on the way out.
  // The wizard is the one that comes up by itself, and the worst to be stuck
  // behind: its dismiss button asks the server to complete the setup, which a
  // session that has ended cannot do, so the answer is an error inside the
  // overlay and the sign-in screen stays out of reach until the page is loaded
  // again.
  //
  // The restart overlay is deliberately left standing by settleAfterRestart,
  // and this does not reach that case: a restarting server fails requests at the
  // socket rather than answering 401, so nothing hands the screen back while it
  // is up. If anything ever did, the session would be genuinely gone and putting
  // the sign-in screen within reach is the right answer anyway.
  for (const overlay of $$('.overlay')) overlay.hidden = true;
  for (const history of $$('.history-overlay')) history.remove();

  // And the notices in the corner, which belong to the session they were raised
  // in. One lingers for up to twenty seconds and the stack sits above the sign-in
  // screen on purpose, so whoever signed in next at the same desk read the last
  // account's "Password changed", or a failure with that account's detail folded
  // under it. The reason a session ends is said on the sign-in form instead.
  $('#toast')?.replaceChildren();

  // And the notice those two could have put up. It is about what the account
  // that is leaving may do, and it must not be waiting for whoever signs in at
  // the same desk next.
  hideRightsChanged();

  // The screen goes back to being nobody's, which is what lets showLogin put it
  // up again at the end of this.
  handedToASession = false;

  // And the unfinished forms, which belong to whoever typed them.
  //
  // Both halves, because they are two places. forgetEveryDraft empties the
  // store a reload would be restored from; the boxes on screen are the other
  // one, and they keep what was typed into them until something says otherwise.
  // Nothing did - so signing out and letting somebody else sign in at the same
  // desk left them looking at a card still holding the previous person's
  // half-written database connection.
  //
  // Reset rather than emptied: a form goes back to what the markup says, which
  // is what somebody arriving at it should find.
  forgetEveryDraft();

  for (const form of $$('form')) form.reset();

  // And what this installation is waiting to restart into, which is the
  // administration of the installation and none of the next person's business.
  // It is a banner now rather than a card behind a permission-checked tab, so
  // nothing else would take it off the screen.
  const restart = $('#restart-banner');
  if (restart) restart.hidden = true;

  me = { user: null, permissions: [], authEnabled: true };

  forgetTheLastAccount();
  forgetTheLastAppearance();

  // And the news that belonged to whoever just left. checkForRelease takes it
  // down on its next answer, but the next answer is a request away and the
  // screen is here now - so somebody signing in behind an administrator would
  // see it for as long as that takes.
  hideReleaseBanner();

  // The screen belongs to nobody now, so it stops speaking the language of
  // whoever just left. activeLanguage() falls back to the browser once me.user
  // is gone, but nothing was re-rendering with it - so signing out of a German
  // account left an English browser looking at a German sign-in form.
  applyLanguage(activeLanguage());

  // And it stops holding their address. The fields keep their values across a
  // sign-out otherwise, which on a shared machine hands the next person the
  // last one's name.
  $('#form-login').reset();
  showCodeField($('#login-totp-field'), false);

  // Asked again rather than kept from when the page opened: a provider an
  // administrator configured since is otherwise missing from the form until
  // somebody reloads it. Not awaited - the button appears when the answer does.
  loadOpenIDOffer();

  showLogin(message);
}

// ------------------------------------------------------------- two-factor

/**
 * Shows a field for a second factor's code or puts it away, and with it whether
 * its form asks for one: a field that is hidden and still required stops its
 * form without a word, and one that is shown and not required lets an empty
 * code through to be called wrong by the server.
 */
function showCodeField(label, shown) {
  label.hidden = !shown;

  const field = label.querySelector('input');
  if (field) field.required = shown;
}

function renderTOTPState() {
  const enabled = me.user?.totpEnabled ?? false;

  $('#totp-state').textContent = enabled
    ? t('totp.on', 'Two-factor authentication is enabled.')
    : t('totp.off', 'Two-factor authentication is not enabled.');

  $('#totp-begin').hidden = enabled;
  $('#totp-disable').hidden = !enabled;
  $('#totp-confirm').hidden = true;
  $('#totp-setup').hidden = true;

  // The code encodes the secret, so it must not survive the enrolment it belongs
  // to - neither on screen nor in the markup.
  const qr = $('#totp-qr');
  qr.hidden = true;
  qr.removeAttribute('src');
  $('#totp-secret').textContent = '';
  $('#totp-uri').textContent = '';
  // Disabling also needs a current code, so the field stays visible for it.
  showCodeField($('#totp-code-field'), enabled);
}

function wireTOTP() {
  $('#totp-begin').addEventListener('click', () => mutate(async () => {
    const setup = await api('/me/totp', { method: 'POST' });
    $('#totp-secret').textContent = setup.secret;
    $('#totp-uri').textContent = setup.uri;

    // The picture is the ordinary way in; the typed key stays folded away behind
    // it for a machine with no camera, and for when a code will not scan.
    const qr = $('#totp-qr');
    qr.hidden = !setup.qr;
    qr.alt = setup.qr ? t('totp.qrAlt', 'QR code for your authenticator app') : '';
    if (setup.qr) qr.src = setup.qr;

    // Open on the key when there is no picture, or the screen would offer nothing.
    $('#totp-manual').open = !setup.qr;

    $('#totp-setup').hidden = false;
    showCodeField($('#totp-code-field'), true);
    $('#totp-confirm').hidden = false;
    $('#totp-begin').hidden = true;
  }, null, null));

  // Reached only once the browser has held the code to its rule. Which of the two
  // is meant is the account's to say rather than the button's: Enter in the field
  // presses the form's first button, and that is Confirm, hidden or not.
  $('#form-totp').addEventListener('submit', (event) => {
    event.preventDefault();

    const code = $('#totp-code').value.trim();

    if (me.user?.totpEnabled) {
      mutate(() => api(`/me/totp?code=${encodeURIComponent(code)}`, { method: 'DELETE' }),
        t('totp.disabled', 'Two-factor authentication disabled'),
        async () => { $('#totp-code').value = ''; await refreshAll(); });

      return;
    }

    mutate(() => api('/me/totp', { method: 'PUT', body: JSON.stringify({ code }) }),
      t('totp.enabled', 'Two-factor authentication enabled'),
      async () => {
        $('#totp-code').value = '';
        await refreshAll();

        // Finished, so drawn from the account now - which the reload left alone.
        renderTOTPState();
      });
  });
}

async function loadLanguages() {
  const languages = (await api('/languages'))?.languages ?? [FALLBACK_LANGUAGE];
  const picker = $('#language-picker');

  picker.replaceChildren();
  for (const language of languages) {
    picker.append(el('option', { value: language, text: language.toUpperCase() }));
  }

  // What is actually on screen, whether that came from the account or from the
  // browser.
  //
  // ?? was the wrong test: it steps aside for null and undefined and not for the
  // empty string, and an account that has stored no language has exactly the
  // empty string. So the picker was set to a value no option carries, and a
  // select given one of those shows nothing - a blank control on the topbar of
  // every account that had never chosen, which reads as a language that could
  // not be worked out rather than one that simply was not stored.
  picker.value = me.user?.language || activeLanguage();
}

// ------------------------------------------------------------- guided tour

/**
 * The tour, in the order someone meets the application.
 *
 * Each step names a real selector rather than describing a control in prose,
 * so a step whose target has been removed can be detected and dropped instead
 * of pointing at nothing. `view` switches to the tab first, because explaining
 * a card on one screen while another is showing helps nobody.
 *
 * `permission` keeps the tour honest about what this person can actually do:
 * a user has no Roles tab, and a tour that mentions one is a tour that
 * lies.
 */
const TOUR_STEPS = [
  {
    target: '#app-title',
    view: null,
    title: () => t('tour.welcome.title', 'The way back'),
    text: () => t('tour.welcome.text',
      'The title takes you to the welcome screen from wherever you are, and the welcome '
      + 'screen can start this walk again.'),
  },
  {
    target: '#tabs',
    view: null,
    title: () => t('tour.nav.title', 'Everything lives up here'),
    text: () => t('tour.nav.text',
      'These tabs are the whole application. You only see the ones your role allows, '
      + 'so this bar is shorter for some people than for others.'),
  },
  {
    target: '#form-user',
    view: 'users',
    permission: 'users:write',
    title: () => t('tour.users.title', 'The people who work here'),
    text: () => t('tour.users.text',
      'Add an account and give it a role: an agent works on tickets, a customer reaches the '
      + 'portal and nothing else.'),
  },
  {
    target: '#table-users',
    view: 'users',
    permission: 'users:read',
    title: () => t('tour.userTable.title', 'Who is here, and what they may do'),
    text: () => t('tour.userTable.text',
      'The role beside each name can be changed from here, a forgotten password reset and a '
      + 'second factor lost with a phone removed. The built-in administrator is the exception: '
      + 'it cannot be deleted and its role cannot be moved, because it is the way back in.'),
  },
  {
    target: '#form-role',
    view: 'roles',
    permission: 'roles:write',
    title: () => t('tour.roles.title', 'Roles decide what is possible'),
    text: () => t('tour.roles.text',
      'A role is a set of rights, and each one is enforced by real code rather than by a '
      + 'hidden button. Tick what the role may do; every account holding it changes with it.'),
  },
  {
    target: '#ticket-list-card',
    view: 'tickets',
    permission: 'tickets:read',
    title: () => t('tour.tickets.title', 'The tickets of your queues'),
    text: () => t('tour.tickets.text',
      'Every ticket in a queue your role reaches, what is still to do first. Open one by its number '
      + 'to read what was written in it, write in it, and see what happened to it.'),
  },
  {
    target: '#form-ticket',
    view: 'tickets',
    permission: 'tickets:create',
    title: () => t('tour.openTicket.title', 'Opening a ticket'),
    text: () => t('tour.openTicket.text',
      'A title, the queue it goes to and what was said - the rest starts as the installation '
      + 'ships it, and can be corrected here once the ticket is open.'),
  },
  {
    target: '#organisation-card',
    view: 'customers',
    permission: 'customers:read',
    title: () => t('tour.organisations.title', 'Who tickets come from'),
    text: () => t('tour.organisations.text',
      'The companies your customers work for, each with its customer number where it has one. An '
      + 'organisation that contacts or tickets belong to is made invalid rather than deleted.'),
  },
  {
    target: '#contact-card',
    view: 'customers',
    permission: 'customers:read',
    title: () => t('tour.contacts.title', 'The people behind the tickets'),
    text: () => t('tour.contacts.text',
      'A ticket is for a contact, and for the organisation the contact belongs to unless it says otherwise. '
      + 'Deleting a contact keeps the tickets for them, for nobody. A contact given portal access signs in '
      + 'with their address and follows their own tickets there.'),
  },
  {
    target: '#told-card',
    view: 'tickets',
    permission: 'tickets:read',
    title: () => t('tour.told.title', 'What you were told'),
    text: () => t('tour.told.text',
      'What the notifications tell you about the tickets around you waits here until you have read it. Opening a '
      + 'ticket from here reads it, and where you let this browser, it is pushed to you as well.'),
  },
  {
    target: '#portal-list-card',
    view: 'portal',
    permission: 'portal:use',
    title: () => t('tour.portal.title', 'Your tickets'),
    text: () => t('tour.portal.text',
      'Every ticket opened for you, the ones still being worked on first. Open one to read what the service desk '
      + 'answered and to answer it.'),
  },
  {
    target: '#portal-faq-card',
    view: 'portal',
    permission: 'portal:use',
    title: () => t('tour.portalFAQ.title', 'Answers already written'),
    text: () => t('tour.portalFAQ.text',
      'What the service desk has written down for customers, searched by words and by category. It may answer '
      + 'your question before you open a ticket.'),
  },
  {
    target: '#portal-new-card',
    view: 'portal',
    permission: 'portal:use',
    title: () => t('tour.portalNew.title', 'Something new'),
    text: () => t('tour.portalNew.text',
      'Open a ticket here: what it is about, what happened, and any files that show it.'),
  },
  {
    target: '#faq-card',
    view: 'faq',
    permission: 'faq:read',
    title: () => t('tour.faq.title', 'What is known'),
    text: () => t('tour.faq.text',
      'The knowledge base, searched by the words in an article\'s title, keywords or text. Each article says who '
      + 'may read it: the staff alone, customers signed in as well, or anybody.'),
  },
  {
    target: '#form-faq-article',
    view: 'faq',
    permission: 'faq:write',
    title: () => t('tour.faqArticle.title', 'Writing it down'),
    text: () => t('tour.faqArticle.text',
      'An article is for the staff alone unless you say otherwise. One that has stopped being true is made '
      + 'invalid: then only those who write the knowledge base see it.'),
  },
  {
    target: '#faq-category-card',
    view: 'faq',
    permission: 'faq:write',
    title: () => t('tour.faqCategories.title', 'Where it is filed'),
    text: () => t('tour.faqCategories.text',
      'Categories are headings, each under one or none. One that holds articles is made invalid rather than '
      + 'deleted.'),
  },
  {
    target: '#queue-card',
    view: 'ticketsetup',
    permission: 'queues:write',
    title: () => t('tour.queues.title', 'Where tickets go'),
    text: () => t('tour.queues.text',
      'A queue is where tickets are sorted to, and each role reaches the queues it is given - to '
      + 'read their tickets, or to work in them. A role with no access does not see a queue at all.'),
  },
  {
    target: '#state-card',
    view: 'ticketsetup',
    permission: 'queues:write',
    title: () => t('tour.states.title', 'Where a ticket can stand'),
    text: () => t('tour.states.text',
      'Every state has a type, and the type is what the application does with a ticket in it. '
      + 'Add the states your desk works with; the ones the application moves tickets into by '
      + 'itself ship with it and stay as they are.'),
  },
  {
    target: '#unlock-card',
    view: 'ticketsetup',
    permission: 'queues:write',
    title: () => t('tour.unlockTypes.title', 'Where a lock runs out'),
    text: () => t('tour.unlockTypes.text',
      'The state types a queue\'s unlock time frees a lock in. New and open ship with the application; switch on '
      + 'another, and a lock nobody uses runs out there too.'),
  },
  {
    target: '#priority-card',
    view: 'ticketsetup',
    permission: 'queues:write',
    title: () => t('tour.priorities.title', 'How urgent a ticket is'),
    text: () => t('tour.priorities.text',
      'Priorities are ordered by their level, 1 the most urgent. Normal is what a ticket starts '
      + 'with when nobody says otherwise.'),
  },
  {
    target: '#type-card',
    view: 'ticketsetup',
    permission: 'queues:write',
    title: () => t('tour.types.title', 'What kind of ticket it is'),
    text: () => t('tour.types.text',
      'Types are words to sort, filter and count by - nothing behaves differently by type. A '
      + 'value tickets carry is made invalid rather than deleted, here as in the two cards above.'),
  },
  {
    target: '#settings-tabs',
    view: 'settings',
    title: () => t('tour.settingsTabs.title', 'My account, by subject'),
    text: () => t('tour.settingsTabs.text',
      'What you are told and your timezone, how you sign in, apps and tokens, and your data '
      + 'each have a tab of their own. You only see the tabs that have something for you.'),
  },
  {
    target: '#notify-card',
    view: 'settings',
    title: () => t('tour.notify.title', 'Being told'),
    text: () => t('tour.notify.text',
      'What happens to your tickets is said on the screen while a page is open. Here this '
      + 'browser can say it as a notification as well, and receive it while it is closed '
      + 'where the installation allows that.'),
  },
  {
    target: '#form-my-timezone',
    view: 'settings',
    title: () => t('tour.myZone.title', 'Your timezone'),
    text: () => t('tour.myZone.text',
      'Moments are written in it for you: when a ticket arrived, when a deadline falls. It is '
      + 'taken from your browser the first time you sign in, and this is where you disagree '
      + 'with that.'),
  },
  {
    target: '#form-password',
    view: 'settings',
    title: () => t('tour.password.title', 'Your password'),
    text: () => t('tour.password.text',
      'Changing it ends every other session but this one. An account that signs in through '
      + 'the directory has no password here to change.'),
  },
  {
    target: '#totp-card',
    view: 'settings',
    title: () => t('tour.totp.title', 'A second factor'),
    text: () => t('tour.totp.text',
      'A code from an authenticator app, set up by scanning a QR code - or by typing the '
      + 'secret, where scanning is not an option. Lost with a phone, it is removed by an '
      + 'administrator, and you set it up again.'),
  },
  {
    target: '#passkey-card',
    view: 'settings',
    title: () => t('tour.passkey.title', 'Signing in without a password'),

    // Where the card says why it offers none, the tour says the same rather than
    // describing a form that is not there.
    text: () => passkeysWithheldBecause() || t('tour.passkey.text',
      'A passkey stays on this device and unlocks with whatever already unlocks it. You '
      + 'can register several, one per device.'),
  },
  {
    target: '#openid-card',
    view: 'settings',
    title: () => t('tour.openid.title', 'Signing in through your organisation'),
    text: () => t('tour.openid.text',
      'Link your account to your organisation\'s sign-in provider and sign in there, with its '
      + 'own second factor. Your password keeps working alongside it.'),
  },
  {
    target: '#install-card',
    view: 'settings',
    title: () => t('tour.install.title', 'The Android app'),
    text: () => t('tour.install.text',
      'Scan this code with an Android phone to download the app of the release this '
      + 'installation runs. It shows this interface, and at every start offers its new '
      + 'version once this installation has been updated.'),
  },
  {
    target: '#token-card',
    view: 'settings',
    title: () => t('tour.tokens.title', 'Tokens for scripts and the app'),
    text: () => t('tour.tokens.text',
      'A personal token lets a script use the API as you, and connects the Android app that '
      + 'scans its code. It carries exactly the rights your role has when it is used, and is '
      + 'shown once, when it is created.'),
  },
  {
    target: '#export-card',
    view: 'settings',
    title: () => t('tour.export.title', 'Everything about you'),
    text: () => t('tour.export.text',
      'Everything this installation keeps about your account, as a ZIP to take with you. '
      + 'Passwords, second factors, tokens and keys stay out of it.'),
  },
  {
    target: '#admin-tabs',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.adminTabs.title', 'Settings, by subject'),
    text: () => t('tour.adminTabs.text',
      'General settings, how people sign in, how the installation runs, and its logs each '
      + 'have a tab of their own. The walk opens each one as it gets there.'),
  },
  {
    target: '#form-branding',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.branding.title', 'What this installation looks like'),
    text: () => t('tour.branding.text',
      'What follows QubitDesk, banner, logo and footer. The logo stands at the left of '
      + "the header once somebody has signed in; the browser tab and the sign-in screen always "
      + "show QubitDesk's own."),
  },
  {
    target: '#form-push',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.push.title', 'Push notifications'),
    text: () => t('tour.push.text',
      'Whether notifications reach a browser that is closed. Off until you switch it on, '
      + 'because every push leaves through the push service of the browser\'s maker.'),
  },
  {
    target: '#form-timezone',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.zone.title', 'The installation\'s timezone'),
    text: () => t('tour.zone.text',
      'The day a ticket\'s number is taken from and the zone the service hours are read in - '
      + 'and the zone moments are written in for everybody who has not chosen their own under '
      + 'My account.'),
  },
  {
    target: '#form-holidays',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.holidays.title', 'The installation\'s public holidays'),
    text: () => t('tour.holidays.text',
      'A country and, where its holidays differ, a state or a region. The service hours leave '
      + 'its public holidays out; until a choice is saved, none is left out.'),
  },
  {
    target: '#sysconfig-card',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.sysconfig.title', 'How the installation behaves'),
    text: () => t('tour.sysconfig.text',
      'The options of the system configuration with their defaults, under the names KIX18 gives them. '
      + 'Only what was changed is kept, and putting an option back to its default forgets it again.'),
  },
  {
    target: '#form-ldap',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.ldap.title', 'Signing in against a directory'),
    text: () => t('tour.ldap.text',
      'Accounts can come from LDAP instead of being created here. Below it, the '
      + 'reconciliation runs on a schedule, and can be previewed before it is run by hand.'),
  },
  {
    target: '#form-openid',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.provider.title', 'A sign-in provider'),
    text: () => t('tour.provider.text',
      'People can sign in through your organisation\'s provider instead of with a password, '
      + 'once each has linked their account to it. Nobody gets an account just because the '
      + 'provider knows them.'),
  },
  {
    target: '#restart-card',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.restart.title', 'Restarting'),
    text: () => t('tour.restart.text',
      'Some settings are read only while the application starts. Saved ones wait here until '
      + 'a restart applies them, and this is where to restart on purpose.'),
  },
  {
    target: '#update-card',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.update.title', 'Which version runs here'),
    text: () => t('tour.update.text',
      'The version this installation runs, and whether a newer one is out. Where it can '
      + 'replace itself, it downloads and installs the new one from here.'),
  },
  {
    target: '#backup-card',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.backup.title', 'Backups'),
    text: () => t('tour.backup.text',
      'The whole installation in one sealed file: made now and downloaded, or made on a '
      + 'schedule and kept on the server, the oldest going past the number you set. It '
      + 'comes back through the installer, into an empty database.'),
  },
  {
    target: '#form-maintenance',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.maintenance.title', 'Maintenance mode'),
    text: () => t('tour.maintenance.text',
      'Closes the installation with an explanation, shown on the sign-in screen as well - '
      + 'so somebody who cannot get in is told why rather than left guessing.'),
  },
  {
    target: '#form-operational',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.limits.title', 'Limits and lifetimes'),
    text: () => t('tour.limits.text',
      'How long a session lasts, how many requests a caller may make, and how much of the '
      + 'directory one synchronisation may remove.'),
  },
  {
    target: '#form-datasource',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.database.title', 'The database'),
    text: () => t('tour.database.text',
      'Which database this installation writes to. The connection is tested before it is '
      + 'saved, and the change applies at the next start.'),
  },
  {
    target: '#form-telemetry',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.telemetry.title', 'Metrics and tracing'),
    text: () => t('tour.telemetry.text',
      'The log level, the metrics endpoint and where traces are exported to. The level applies '
      + 'at once; the other two are read when the process starts, so they wait for the next one.'),
  },
  {
    target: '#changes-card',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.changes.title', 'Who changed what'),
    text: () => t('tour.changes.text',
      'Every account, role and setting created, changed or deleted, every switch of '
      + 'maintenance mode and every restart, with who did it, newest first.'),
  },
  {
    target: '#log-card',
    view: 'admin',
    permission: 'settings:manage',
    title: () => t('tour.log.title', 'The log, without a shell'),
    text: () => t('tour.log.text',
      'What this process is writing, filterable by level. It is the first place to look '
      + 'when something refused and the reason was not on screen.'),
  },
  {
    target: '#calendar-card',
    view: 'ticketsetup',
    permission: 'queues:write',
    title: () => t('tour.calendars.title', 'When the service is given'),
    text: () => t('tour.calendars.text',
      'The hours of each weekday, less public holidays and free days of its own, in which a service level\'s '
      + 'minutes are counted. A calendar that names no timezone or public holidays follows the installation\'s.'),
  },
  {
    target: '#sla-card',
    view: 'ticketsetup',
    permission: 'queues:write',
    title: () => t('tour.sla.title', 'What a ticket is owed'),
    text: () => t('tour.sla.text',
      'Minutes until the first answer and until the solution, counted in the level\'s calendar, in the installation\'s '
      + 'where it names none, or around the clock. A queue '
      + 'names the level its new tickets start with, and a ticket past its moment is marked overdue.'),
  },
  {
    target: '#notification-card',
    view: 'communication',
    permission: 'communication:write',
    title: () => t('tour.notifications.title', 'Who is told what'),
    text: () => t('tour.notifications.text',
      'A notification tells the agents around a ticket - its owner, those watching it, those who work in its queue - '
      + 'when something happens to it, each in their language. It is shown to them here and pushed to their browsers.'),
  },
  {
    target: '#template-card',
    view: 'communication',
    permission: 'communication:write',
    title: () => t('tour.templates.title', 'Answers written once'),
    text: () => t('tour.templates.text',
      'An answer template is offered to whoever writes in a ticket of its queue, or of every queue, with the '
      + 'ticket\'s placeholders filled in their words. It starts an article; it does not send one.'),
  },
  {
    target: '#configitem-card',
    view: 'assets',
    permission: 'assets:read',
    title: () => t('tour.configitem.title', 'The inventory'),
    text: () => t('tour.configitem.text',
      'The configuration items, a page of them by class and words, each opened below the list in a form drawn '
      + 'from its class\'s definition. A change is kept as the item\'s next version, and every version stays '
      + 'readable as it was written.'),
  },
  {
    target: '#catalog-card',
    view: 'assets',
    permission: 'catalog:write',
    title: () => t('tour.catalog.title', 'What there is to pick from'),
    text: () => t('tour.catalog.text',
      'The general catalog: the classes a configuration item can be of, where in its life one is and how it is '
      + 'doing, and the lists its details pick from. A value nobody should pick any more is retired, never deleted.'),
  },
  {
    target: '#classdef-card',
    view: 'assets',
    permission: 'catalog:write',
    title: () => t('tour.classdef.title', 'What an item holds'),
    text: () => t('tour.classdef.text',
      'Each class\'s definition: the attributes an item of it holds, written as one document. A change is kept as '
      + 'a new version, so an item written before it is still read the way it was written.'),
  },
  {
    target: '#itsmchange-card',
    view: 'changes',
    permission: 'changes:read',
    title: () => t('tour.itsmchange.title', 'Changes and their work orders'),
    text: () => t('tour.itsmchange.text',
      'The changes to what the organisation runs, a page of them by state and words, each opened below the list '
      + 'with the work orders it is carried out in. A change moves on only as its states allow, and whoever a '
      + 'work order is given to reports on it here.'),
  },
  {
    target: '#macro-card',
    view: 'automation',
    permission: 'automation:write',
    title: () => t('tour.macros.title', 'What the application does by itself'),
    text: () => t('tour.macros.text',
      'A macro is a run of steps taken on a ticket, in order. A job runs macros on the tickets it finds, as the '
      + 'application, so every step meets the rules a person\'s change meets and is written down with the ticket.'),
  },
  {
    target: '#plan-card',
    view: 'automation',
    permission: 'automation:write',
    title: () => t('tour.plans.title', 'When jobs run'),
    text: () => t('tour.plans.text',
      'An execution plan runs the jobs that name it at times of day on the weekdays it names, in the '
      + 'installation\'s timezone, or when something happens to a ticket - once a person\'s change is written.'),
  },
  {
    target: '#job-card',
    view: 'automation',
    permission: 'automation:write',
    title: () => t('tour.jobs.title', 'What runs the macros'),
    text: () => t('tour.jobs.text',
      'A job takes the tickets its criteria name - by queue, state, priority, type and lock - and runs its macros '
      + 'on each in order, when one of its plans says so or when you run it here.'),
  },
  {
    target: '#run-card',
    view: 'automation',
    permission: 'automation:write',
    title: () => t('tour.runs.title', 'What the jobs did'),
    text: () => t('tour.runs.text',
      'Every run is kept for thirty days with the steps it could not take, each with the reason a person would '
      + 'have been given.'),
  },
  {
    target: '#theme-picker',
    view: null,
    title: () => t('tour.theme.title', 'Appearance and language'),
    text: () => t('tour.theme.text',
      'Light or dark - automatic follows the time of day. The language follows your browser '
      + 'until you choose one. That is everything; enjoy.'),
  },
];

/** Where we are in the tour, and the steps that apply to this person. */
let tour = { steps: [], index: 0, active: false };

/** Drops steps this person cannot reach, so the tour never points at nothing. */
function applicableTourSteps() {
  return TOUR_STEPS.filter((step) => {
    if (step.permission && !can(...step.permission.split(','))) return false;

    return tourTargetIsReachable($(step.target));
  });
}

/**
 * Whether a control is one this person could be shown.
 *
 * This asked for offsetParent, and offsetParent is null for everything inside a
 * screen that is not the current one - every `.view` but one is display:none. So
 * every step outside the screen the tour happened to start on measured as absent
 * and was dropped: a tour begun on one screen came to four steps and looked
 * complete, and every other screen was simply never mentioned. A step switches to its own screen before it runs, so where
 * somebody is standing right now says nothing about whether the control exists.
 *
 * What does say so is a hidden that is not a screen, nor a tab of one - a card
 * kept back because there is nothing in it yet, or one a permission removed. A
 * card on a tab that is not chosen is reachable for the same reason one on
 * another screen is: the step opens its tab before it points at it.
 */
function tourTargetIsReachable(node) {
  for (let current = node; current && current !== document.body; current = current.parentElement) {
    const aPlaceToGo = current.classList.contains('view') || current.getAttribute('role') === 'tabpanel';

    if (current.hidden && !aPlaceToGo) return false;
  }

  return Boolean(node);
}

/**
 * Positions the spotlight and the bubble around one element.
 *
 * Does nothing if the walk has ended in the meantime. This runs two animation
 * frames after the step was rendered - the delay is what lets a view that has
 * just been switched to settle before anything is measured against it - and
 * ending the tour inside that gap used to leave a bubble on screen belonging to
 * a walk that had finished, because this unhides what endTour had just hidden.
 *
 * Rare by hand and not rare at all for anything driving the application: the
 * interface says the walk has been decided as soon as it starts, so a caller
 * that reacts to that and ends it immediately lands in exactly this gap.
 */
function placeTour(node) {
  if (!tour.active) return;

  const rect = node.getBoundingClientRect();
  const pad = 6;
  const spotlight = $('#tour-spotlight');
  const bubble = $('#tour-bubble');

  // Page coordinates, not viewport ones: both elements are absolutely
  // positioned in the document, so they stay put while it scrolls. The screen
  // is the document's client area rather than the window, which also counts a
  // scrollbar nothing can be read under.
  const screenWidth = document.documentElement.clientWidth;

  // Across, the ring stops at the screen's edges. A ring wider than the screen
  // makes the page wider than the screen, and a telephone answers that by
  // zooming the whole page out to fit: a table inside its own scroller measures
  // its full width, so the step about the accounts table shrank a 320-pixel
  // screen to four tenths of its size. The part of the target on the screen is
  // the part anybody can see.
  const ringLeft = Math.max(0, rect.left - pad);
  const ringRight = Math.min(screenWidth, rect.right + pad);

  spotlight.style.top = `${rect.top + window.scrollY - pad}px`;
  spotlight.style.left = `${ringLeft + window.scrollX}px`;
  spotlight.style.width = `${Math.max(0, ringRight - ringLeft)}px`;
  spotlight.style.height = `${rect.height + pad * 2}px`;
  spotlight.hidden = false;

  bubble.hidden = false;

  // Wholly on the screen, in both dimensions. Below the target where there is
  // room, above it where there is room there, and otherwise at the foot of the
  // screen over the target, which the ring still marks - a card on a telephone
  // on its side is taller than the screen, so there is room on neither side.
  //
  // This used to bound the right edge and nothing else, so "above" was never
  // asked whether there was room above: on a short screen it put the bubble off
  // the top, with the step's words and its buttons in it.
  const margin = 12;
  const gap = 14;
  const { width, height } = bubble.getBoundingClientRect();
  const lowest = window.innerHeight - margin - height;

  let y = rect.bottom + gap;
  if (y > lowest) {
    const above = rect.top - gap - height;
    y = above >= margin ? above : lowest;
  }

  const x = Math.min(rect.left - pad, screenWidth - margin - width);

  bubble.style.top = `${Math.max(margin, Math.min(y, lowest)) + window.scrollY}px`;
  bubble.style.left = `${Math.max(margin, x) + window.scrollX}px`;
}

/**
 * Opens the bar's fold when the step points inside it, and puts it back when
 * the step does not.
 *
 * On a screen too narrow to hold it the bar keeps its own things behind the
 * burger: the navigation, and beside it the appearance picker, the language and
 * the way out. An element in there has no box at all, and placeTour rings a
 * box - so the step about the navigation ringed twelve pixels in the top left
 * corner of a 390px screen while the bubble beside it explained what those
 * twelve pixels were for.
 *
 * Only when the target has no box of its own. The rest of the bar - the title,
 * the account - is on screen at every width, and unfolding the whole navigation
 * to point at the title would answer a question nobody asked.
 *
 * And folded back for a step about anything else, because the open fold is a
 * column of nine points: left standing it pushes the next fifteen steps down
 * the screen, which is the tour holding the page open rather than walking
 * through it.
 */
function showWhatTheStepPointsAt(node) {
  const bar = $('.topbar');
  if (!bar) return;

  if (!bar.contains(node)) {
    showNavigationMenu(false);

    return;
  }

  if (node.getBoundingClientRect().height === 0) showNavigationMenu(true);
}

async function renderTourStep() {
  const step = tour.steps[tour.index];

  if (step.view) switchView(step.view);

  const node = $(step.target);
  if (!node) {
    // The target went away between building the list and getting here.
    await advanceTour();

    return;
  }

  openTheTabOf(node);
  showWhatTheStepPointsAt(node);

  node.scrollIntoView({ block: 'center', behavior: 'smooth' });

  $('#tour-count').textContent = t('tour.count', 'Step {n} of {total}')
    .replace('{n}', String(tour.index + 1))
    .replace('{total}', String(tour.steps.length));
  $('#tour-title').textContent = step.title();
  $('#tour-text').textContent = step.text();

  $('#tour-back').disabled = tour.index === 0;
  if (tour.index === tour.steps.length - 1) {
    swapTheLabel($('#tour-next'), 'tour.finish', 'Finish');
  } else {
    swapTheLabel($('#tour-next'), 'tour.next', 'Next');
  }

  // After the layout has settled from the view switch and the scroll, or the
  // rectangle measured would be the one from before it moved.
  requestAnimationFrame(() => requestAnimationFrame(() => placeTour(node)));
}

async function advanceTour() {
  if (tour.index >= tour.steps.length - 1) {
    await endTour();

    return;
  }

  tour.index += 1;
  await renderTourStep();
}

/** Starts the tour, from the beginning. */
async function startTour() {
  tour.steps = applicableTourSteps();

  if (tour.steps.length === 0) return;

  tour.index = 0;
  tour.active = true;

  sealThePage(true);

  await renderTourStep();
}

/**
 * Puts the page out of reach while the tour is running, and back afterwards.
 *
 * Two mechanisms, because they stop different things.
 *
 * The blocker is a fixed element over the whole viewport that takes every
 * press. It has to exist because the spotlight cannot do this job: the
 * spotlight is one element with a 9999-pixel shadow, and it carries
 * pointer-events: none so that it does not swallow presses meant for the
 * control it is drawing a ring around. The result was that every press landed -
 * on the highlighted control, and on everything under the dimming too. A tour
 * explaining a card while somebody presses its buttons is a tour narrating
 * something that is no longer on the screen it describes.
 *
 * inert is the other half, and the blocker cannot do it: a blocker stops the
 * mouse and nothing else, so Tab still walked into the page behind it and Enter
 * still pressed what it found. inert takes a subtree out of the tab order, out
 * of reach of assistive technology, and out of the way of clicks in one go.
 *
 * Applied to the body's children rather than to one wrapper, because there is
 * no wrapper - the bar, the views, the banners and the overlays are siblings.
 * Everything except the tour's own two elements, which have to keep working:
 * they are how somebody gets out of this.
 */
function sealThePage(sealed) {
  const spared = new Set(['tour-bubble', 'tour-spotlight', 'tour-blocker']);

  for (const child of document.body.children) {
    if (spared.has(child.id)) continue;

    // Scripts and templates have nothing to make inert, and marking them would
    // be noise in the markup for anybody reading it.
    if (child.tagName === 'SCRIPT' || child.tagName === 'TEMPLATE') continue;

    child.inert = sealed;
  }

  const blocker = $('#tour-blocker');
  if (blocker) blocker.hidden = !sealed;
}

/**
 * Ends the tour and records it as seen.
 *
 * Skipping counts as seen: someone who dismissed it made a decision, and
 * greeting them with it again on the next sign-in would override that.
 */
async function endTour() {
  putTheTourAway();

  await recordTourSeen();
}

/**
 * Takes the walk through off the screen and gives the page back.
 *
 * Separate from endTour because a walk can stop without being finished, and the
 * two must not be written down the same way. endTour records it as seen, which
 * is right for somebody who reached the end or decided to skip it; an
 * interruption is neither, and the next sign-in should offer it again.
 */
function putTheTourAway() {
  tour.active = false;
  $('#tour-spotlight').hidden = true;
  $('#tour-bubble').hidden = true;

  // A fold the walk opened is a fold it puts back. Left open, the bar stays a
  // column of nine points over whatever screen the walk finished on.
  showNavigationMenu(false);

  sealThePage(false);
}

/** Notes that the introduction has been offered, so it is offered once. */
async function recordTourSeen() {
  if (!me.user || me.user.tourSeen) return;

  try {
    await api('/me/tour', { method: 'PUT', body: JSON.stringify({ seen: true }) });
    me.user.tourSeen = true;
  } catch {
    // Worst case it is offered again next time, which is a small annoyance
    // and not worth an error message over.
  }
}

function wireTour() {
  $('#tour-next').addEventListener('click', advanceTour);

  $('#tour-back').addEventListener('click', async () => {
    if (tour.index > 0) {
      tour.index -= 1;
      await renderTourStep();
    }
  });

  $('#tour-end').addEventListener('click', endTour);

  // Escape is what people press to get out of a modal, and a tour that traps
  // someone is worse than no tour.
  document.addEventListener('keydown', (e) => {
    if (tour.active && e.key === 'Escape') endTour();
  });

  // The highlight is drawn from a measured rectangle, so it has to be redrawn
  // when the layout changes underneath it.
  const redraw = () => {
    if (!tour.active) return;

    const node = $(tour.steps[tour.index].target);
    if (node) placeTour(node);
  };

  for (const event of ['resize', 'scroll']) {
    window.addEventListener(event, redraw, { passive: true });
  }

  // And when the page itself changes under it, which neither of those reports.
  // A step is drawn two frames after its screen is switched to, and the card it
  // points at may fill in later than that - the log's lines, the telemetry
  // settings - so the ring stood around the part of the card that had been
  // there: 804 pixels of a form that had grown to 1,012. The page is watched
  // rather than the target, because a card filling in above the target moves it
  // without resizing it. The ring and the bubble are positioned absolutely, so
  // moving them does not resize the page and this cannot set itself off.
  if (typeof ResizeObserver === 'function') {
    new ResizeObserver(redraw).observe(document.body);
  }

  $('#tour-restart').addEventListener('click', startTour);
}

/**
 * What happens once, on arrival, beyond putting a screen up.
 *
 * Only the walk through is left here. The greeting itself is a screen now, so it
 * needs no arranging - somebody arriving at it sees it, and somebody who wants it
 * again presses the title.
 */
async function greetAfterSignIn() {
  await maybeWelcome();

  // Says the question has been settled, whichever way it went.
  //
  // The same idea as dataset.loaded and for the same reader: something watching
  // from outside, which cannot otherwise tell "the tour is not up yet" from
  // "the tour is not coming". That distinction used to cost nothing, because
  // the tour let the page be used underneath it. It does not any more - the
  // page is sealed while it runs - so anything driving this application has to
  // know whether to wait.
  //
  // Set even when nothing was offered: "no tour for this account" is an answer.
  document.documentElement.dataset.greeted = 'yes';
}

/**
 * Whether this person has already been greeted during this visit, marking them as
 * greeted if not.
 *
 * Per tab and gone when the tab is: exactly the lifetime of "this visit". Shared by
 * both greetings so they cannot both land - somebody who declined the walk through
 * and then reloaded was being welcomed back to a screen they had just arrived at.
 *
 * Private browsing can refuse storage outright. Then a greeting shows again on the
 * next load, which is a small annoyance and not worth failing over.
 */
function greetedThisVisit() {
  if (!me.user) return true;

  const key = `qubitdesk_greeted_${me.user.id}`;

  try {
    if (sessionStorage.getItem(key)) return true;

    sessionStorage.setItem(key, '1');
  } catch {
    return false;
  }

  return false;
}

/**
 * Greets somebody on their first sign-in by walking them through the place.
 *
 * The walk starts by itself now. It used to be offered by a modal with a "Not
 * now" beside it, and "Not now" recorded the tour as seen - so the one button
 * that looked like "later" meant "never", and the introduction the application
 * had was the introduction almost nobody got.
 *
 * Never while the setup wizard is up: being walked through the application by
 * two things at once is worse than either alone, and the wizard is the one
 * that has to happen first.
 */
async function maybeWelcome() {
  if (!me.user || me.user.tourSeen) return;
  if (!$('#setup-wizard').hidden) return;
  if (me.user.mustChangePassword) return;
  if (greetedThisVisit()) return;

  await startTour();
}

/**
 * Fills in the greeting screen for whoever is looking at it.
 *
 * Rendered on every arrival rather than once, because what it offers follows
 * what this account may do, and a screen somebody can come back to has to be
 * right when they do.
 */
function renderWelcome() {
  if (!me.user) return;

  // Somebody who has been walked through the application is not new to it, so
  // the same screen greets them differently. tourSeen is the only durable record
  // of "has been here before" there is.
  const returning = me.user.tourSeen;

  const greeting = returning
    ? {
      named: t('welcome.backName', 'Welcome back, {0}'),
      plain: t('welcome.back', 'Welcome back'),
    }
    : {
      named: t('welcome.hello', 'Welcome, {0}'),
      plain: t('welcome.helloPlain', 'Welcome'),
    };

  $('#welcome-title').textContent = me.user.name
    ? greeting.named.replace('{0}', me.user.name)
    : greeting.plain;

  $('#welcome-text').textContent = t('welcome.text',
    'This is where requests are taken in, worked on and answered. A short walk through it takes '
    + 'about a minute, and you can start it again at any time under My account.');

  // What the application is for, in the order somebody meets it. Only the points
  // this person can actually act on: a list that promises something somebody cannot
  // do is worse than a shorter list.
  const points = [
    can('portal:use')
      && t('welcome.point.portal',
        'Open a ticket and follow your tickets under My tickets.'),
    can('users:write')
      && t('welcome.point.administer',
        'Add the people who work here, and decide what each of them may do.'),
    t('welcome.point.account',
      'Choose your timezone, your language and a second factor under My account.'),
  ].filter(Boolean);

  $('#welcome-points').replaceChildren(...points.map((text) => el('li', { text })));

  // Every step of the walk points at a control, and a step whose control this person
  // cannot reach is dropped - so for an account that only administers there may be
  // nothing left to walk through. Offering it anyway would open an empty tour, which
  // is a worse first impression than not offering one.
  $('#welcome-tour').hidden = applicableTourSteps().length === 0;
}

/**
 * The screen to carry on to from the greeting.
 *
 * Checked with viewIsOffered rather than trusted, for the reason written there.
 * Without it, "carry on where you were" led to a screen that no longer existed
 * and left the page blank - and, for a tab the account had merely lost, did
 * nothing at all, because viewTheReaderMayHave sent it straight back here.
 *
 * The first tab this reader does have is the honest answer to both: they were
 * somewhere once, that somewhere is gone, and this is where they can go now.
 */
function onwardView() {
  const last = rememberedView();

  return last !== 'welcome' && viewIsOffered(last) ? last : firstVisibleView();
}

function wireWelcome() {
  // The title, from anywhere. This is the way back to the greeting, and the
  // reason it is a screen rather than something that appears once and is gone.
  $('#app-title').addEventListener('click', () => switchView('welcome'));

  $('#welcome-continue').addEventListener('click', () => switchView(onwardView()));
  $('#welcome-tour').addEventListener('click', startTour);
}

// ------------------------------------------------------------ setup wizard

/**
 * What each step says and does.
 *
 * The server decides which steps exist and which are done; this only supplies
 * the words and the form. Keeping the two apart means a step cannot quietly
 * complete because the interface thinks it should have.
 *
 * `fields` renders inside the wizard for the things worth settling on the spot.
 * `tab` sends the administrator to the real screen for the rest - duplicating
 * the LDAP form here would mean two forms to keep in step, and the second one
 * always drifts.
 */
const SETUP_STEPS = {
  password: {
    title: () => t('setup.password.title', 'Change the administrator password'),
    text: () => t('setup.password.text',
      'This account still has the password from the documentation, which anyone can look up. '
      + 'Nothing else can be used until it is changed.'),
    fields: () => [
      passwordField('currentPassword', t('settings.currentPassword', 'Current password'), 'changeme123'),
      passwordField('newPassword', t('settings.newPassword', 'New password'), ''),
    ],
    submit: async (values) => {
      await api('/me/password', {
        method: 'PUT',
        body: JSON.stringify({
          currentPassword: values.currentPassword,
          newPassword: values.newPassword,
        }),
      });

      // The session survives now: the server ends the other devices and keeps
      // this one, so the wizard carries straight on to its next step instead of
      // dropping the person at a sign-in screen half way through setting up.
      return {};
    },
  },

  timezone: {
    title: () => t('setup.timezone.title', 'Choose the timezone'),
    text: () => t('setup.timezone.text',
      'This decides the day a ticket\'s number is taken from and the zone the service '
      + 'hours are read in. Left unset it is UTC, which is the wrong hour for most of '
      + 'the world.'),
    fields: () => {
      const select = el('select', { name: 'timezone' });
      fillTimezoneSelect(select, guessTimezone());

      return [el('label', { text: t('tz.timezone', 'Timezone') }, select)];
    },
    submit: async (values) => {
      await api('/settings/timezone', {
        method: 'PUT', body: JSON.stringify({ timezone: values.timezone }),
      });
    },
  },

  branding: {
    title: () => t('setup.branding.title', 'Add to the name'),
    text: () => t('setup.branding.text',
      'Every installation is called QubitDesk. What you write here follows it after a bar, '
      + 'as in QubitDesk | htp.net, in the header and the browser tab. Optional, but it is '
      + 'what tells a test instance apart from the real one at a glance.'),
    // One field per language, rather than one field.
    //
    // What follows the name is the first thing an installation says about itself
    // and the one thing this step exists for, so a company that works in two
    // languages should not have to come back to the appearance screen afterwards
    // to say it twice. Two boxes is small enough to put on the wizard; the four
    // texts on the appearance screen are not, which is why that one has a
    // switcher instead.
    fields: () => BRANDING_LANGUAGES.map((language) => el('label', {
      text: `${t('setup.instanceName', 'After “QubitDesk |”')} — ${languageName(language)}`,
    }, el('input', { type: 'text', name: `title.${language}`, maxlength: '80' }))),
    submit: async (values) => {
      const written = {};

      for (const language of BRANDING_LANGUAGES) {
        written[language] = (values[`title.${language}`] ?? '').trim();
      }

      // Nothing typed at all is a step somebody skipped, which it is allowed to
      // be.
      if (!Object.values(written).some(Boolean)) return;

      const branding = await api('/branding');

      // The base is the first language that has something, so an installation
      // that fills in one box is named in every language rather than in one.
      const base = written[BRANDING_LANGUAGES[0]]
        || Object.values(written).find(Boolean)
        || '';

      const translations = { ...branding.translations };

      for (const language of BRANDING_LANGUAGES) {
        translations[language] = {
          ...(translations[language] ?? {}),
          title: written[language],
        };
      }

      await api('/settings/branding', {
        method: 'PUT',
        body: JSON.stringify({ ...branding, title: base, translations }),
      });

      await loadBranding();
    },
  },

  directory: {
    title: () => t('setup.directory.title', 'Connect a directory (optional)'),
    text: () => t('setup.directory.text',
      'With LDAP or Active Directory connected, people sign in with the account they already have '
      + 'and no passwords are kept here. Skip this to manage accounts locally.'),
    tab: 'admin',
  },
};

/** A labelled password field for the wizard's own forms. */
function passwordField(name, label, placeholder) {
  return el('label', { text: label },
    el('input', { type: 'password', name, placeholder, autocomplete: 'off' }));
}

/** The browser's own zone, as the opening suggestion. */
function guessTimezone() {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

/** Server state, where in it we are, and what this process is running on. */
let setup = { state: null, index: 0 };

/** Loads the wizard state and shows it if anything is outstanding. */
async function loadSetup() {
  if (!administers()) {
    $('#setup-wizard').hidden = true;

    return;
  }

  try {
    setup.state = await api('/setup');
  } catch {
    // Not fatal: an installation is usable without the wizard, and failing
    // here would block the interface over a hint.
    $('#setup-wizard').hidden = true;

    return;
  }

  if (!shouldShowSetup(setup.state)) {
    $('#setup-wizard').hidden = true;

    return;
  }

  // Open on the first thing still to do rather than always at step one, so
  // coming back does not mean clicking through what is already settled.
  setup.index = Math.max(0, setup.state.steps.findIndex((s) => !s.done));

  try {
    renderSetup();
  } catch {
    // The policy stated above the fetch, applied to the line it was missing
    // from: an installation is usable without the wizard, and this runs before
    // every other loader, so a throw here empties the whole screen over a hint.
    //
    // What throws is a step id this page has no definition for. renderSetup
    // guards that lookup in the step list - SETUP_STEPS[s.id]?.title() ?? s.id -
    // and not in the detail pane three lines below it. A server one version
    // ahead is what produces one, which normally cannot last, because
    // settleAfterRestart reloads the tab when the build changed; it lasts when
    // the new build arrived without this tab being told, which is a compose
    // pull or a container rollout.
    $('#setup-wizard').hidden = true;

    return;
  }

  $('#setup-wizard').hidden = false;
}

/**
 * Mirrors the server's rule: dismissing the wizard settles the optional steps,
 * not the required ones. Anything required and undone brings it back.
 */
function shouldShowSetup(state) {
  const outstanding = state.steps.some((s) => s.required && !s.done);

  return !state.completed || outstanding;
}

function renderSetup() {
  const { state, index } = setup;
  const step = state.steps[index];
  const definition = SETUP_STEPS[step.id];

  const done = state.steps.filter((s) => s.done).length;
  $('#setup-progress').textContent = t('setup.progress', 'Step {n} of {total} — {done} done')
    .replace('{n}', String(index + 1))
    .replace('{total}', String(state.steps.length))
    .replace('{done}', String(done));

  $('#setup-steps').replaceChildren(...state.steps.map((s, i) => el('li', {
    class: [s.done ? 'done' : '', s.required ? 'required' : '', i === index ? 'current' : '']
      .filter(Boolean).join(' '),
    text: SETUP_STEPS[s.id]?.title() ?? s.id,
  })));

  $('#setup-step-title').textContent = definition.title();
  $('#setup-step-text').textContent = definition.text();
  $('#setup-error').hidden = true;

  const fields = el('div', {});
  if (!step.done && definition.fields) fields.append(...definition.fields());
  $('#setup-step-fields').replaceChildren(fields);

  // The wizard builds its fields as it goes, so the one-off pass at start-up has
  // already been and gone by the time a password field of its own exists.
  wirePasswordReveal($('#setup-step-fields'));

  $('#setup-back').disabled = index === 0;

  const last = index === state.steps.length - 1;
  if (last) swapTheLabel($('#setup-next'), 'setup.finish', 'Finish');
  else swapTheLabel($('#setup-next'), 'setup.next', 'Next');

  // A required step cannot be skipped; that is what "required" means here.
  $('#setup-skip').hidden = step.required && !step.done;
  if (definition.tab) {
    swapTheLabel($('#setup-skip'), 'setup.openSettings', 'Open Settings instead');
  } else {
    swapTheLabel($('#setup-skip'), 'setup.skip', 'Skip this step');
  }
}

function setupError(message) {
  const box = $('#setup-error');
  box.textContent = message;
  box.hidden = false;
}

/** Reads the wizard's own fields. */
function setupValues() {
  const values = {};
  for (const input of $$('#setup-step-fields input, #setup-step-fields select')) {
    values[input.name] = input.value;
  }

  return values;
}

async function advanceSetup() {
  const step = setup.state.steps[setup.index];
  const definition = SETUP_STEPS[step.id];

  // A required step with no form of its own - the database question - would
  // otherwise be walked straight past by the Next button, which is exactly the
  // skipping this ordering exists to prevent.
  if (step.required && !step.done && !definition.submit) {
    setupError(t('setup.decideFirst', 'Please settle this step before continuing.'));

    return;
  }

  if (!step.done && definition.submit) {
    try {
      // No step signs the person out any more. The password step used to: the
      // server ended every session on a change, so the wizard had nowhere to go
      // but the sign-in screen, half way through setting the installation up. It
      // keeps this session now, so the password step advances like every other.
      await definition.submit(setupValues());
    } catch (err) {
      setupError(err.message);

      return;
    }
  }

  await nextSetupStep();
}

/** Re-reads the state, so a step completed elsewhere is reflected too. */
async function nextSetupStep() {
  const wasLast = setup.index >= setup.state.steps.length - 1;
  const current = setup.state.steps[setup.index];

  try {
    setup.state = await api('/setup');
  } catch (err) {
    // Said rather than swallowed. advanceSetup hangs straight off the Next
    // button, so a rejection here has nowhere to go: the wizard would sit on the
    // same step with no message, and the obvious response is to press Next
    // again. On the password step that is the worst place for it - the password
    // has already been changed by then, so the second attempt fails on the old
    // one too, and the screen still explains nothing.
    setupError(err.message);

    return;
  }

  // The server is the judge of whether the step took. A submit that returned
  // without error but left the step outstanding - a blank title, a connection
  // that was not actually saved - must not be walked past.
  const after = setup.state.steps[setup.index];

  if (current.required && !after.done) {
    // Drawn first and said after: drawing a step begins by hiding the box, so
    // said first, the wizard stayed put in silence.
    renderSetup();
    setupError(t('setup.decideFirst', 'Please settle this step before continuing.'));

    return;
  }

  if (wasLast) {
    await finishSetup();

    return;
  }

  setup.index += 1;
  renderSetup();
}

async function finishSetup() {
  try {
    await api('/setup/complete', { method: 'POST' });
  } catch (err) {
    setupError(err.message);

    return;
  }

  $('#setup-wizard').hidden = true;
  toast(t('setup.done', 'Setup complete.'), 'ok');

  try {
    await refreshAll();
  } catch (err) {
    // The same distinction mutate draws, and the same wording: setting up is
    // done and is not coming undone, so this must not read as the wizard having
    // failed. What failed is the screen catching up, and the way out is to load
    // the page again rather than to run the wizard a second time.
    toastFailure(err, t('msg.loadFailed', 'Could not load everything'));
  }
}

function wireSetup() {
  $('#setup-next').addEventListener('click', advanceSetup);

  $('#setup-back').addEventListener('click', () => {
    if (setup.index > 0) {
      setup.index -= 1;
      renderSetup();
    }
  });

  $('#setup-skip').addEventListener('click', async () => {
    const step = setup.state.steps[setup.index];
    const definition = SETUP_STEPS[step.id];

    if (definition.tab) {
      // Sends them to the real screen rather than a copy of it, and gets out
      // of the way so they can use it.
      $('#setup-wizard').hidden = true;
      switchView(definition.tab);

      return;
    }

    await nextSetupStep();
  });

  $('#setup-dismiss').addEventListener('click', async () => {
    const outstanding = setup.state.steps.filter((s) => s.required && !s.done);

    if (outstanding.length > 0) {
      setupError(t('setup.stillRequired',
        'These steps have to be settled first; the wizard will keep coming back until they are.'));

      return;
    }

    await finishSetup();
  });
}

// ------------------------------------------------------- operation & limits

/** The fields of the operational form, in the order they appear. */
const OPERATIONAL_FIELDS = [
  'sessionLifetimeHours', 'sessionIdleMinutes', 'rateLimit',
  'rateLimitWindowSeconds', 'ldapSyncMaxDeleteRatio',
];

/**
 * Fills the operational form.
 *
 * An overridden value goes in the field; anything still following the
 * configuration file leaves the field empty and shows that file's value as the
 * placeholder. Prefilling every field instead would turn "not set here" into a
 * deliberate-looking choice the moment anyone pressed Save.
 */
function fillOperationalForm(data) {
  const form = $('#form-operational');

  // What is in force first, because it is not this form's and must not wait for
  // it. The same two halves the telemetry and datasource cards are split along:
  // the boxes belong to whoever is at the keyboard, the line saying what the
  // installation is actually running does not - and it is the set of figures
  // somebody is weighing their own against while they type them.
  const effective = data.effective ?? {};

  // A zero that switches a limit off is said to be off. Written as a figure it
  // read as the strictest limit there is, and for the deletion limit that is the
  // opposite of the truth: "delete limit 0" was a synchronisation allowed to
  // remove every account the directory no longer lists.
  const offOr = (value, shown) => (value === 0 ? t('ops.off', 'off') : shown);

  $('#operational-effective').textContent = `${t('ops.effective', 'Currently in force')}: `
    + `${t('ops.sessionShort', 'session')} ${fmtHours(effective.sessionLifetimeHours)}, `
    + `${t('ops.idleShort', 'idle')} ${offOr(effective.sessionIdleMinutes, `${effective.sessionIdleMinutes} min`)}, `
    + `${t('ops.rateShort', 'rate')} ${effective.rateLimit}/${effective.rateLimitWindowSeconds} s, `
    + `${t('ops.ratioShort', 'delete limit')} ${offOr(effective.ldapSyncMaxDeleteRatio, fmtShare(effective.ldapSyncMaxDeleteRatio))}`;

  // Not over somebody who is part way through filling it in. This runs after
  // every save on the screen and after a language is chosen, and it used to
  // replace whatever had been typed with the server's copy.
  if (beingEdited(form)) return;

  for (const field of OPERATIONAL_FIELDS) {
    const input = form.elements[field];

    // A field this list names and the markup does not is a mistake, but it must
    // not be a silent one that takes the rest of the screen with it: this loop
    // runs while the administration screen is being built, and a throw here left
    // the telemetry and directory cards empty with nothing said about why.
    if (!input) {
      console.warn(`operational field ${field} is not in the form`);

      continue;
    }

    const override = data.configured?.[field];

    input.value = override ?? '';
    input.placeholder = String(data.defaults?.[field] ?? '');
  }
}

/** Reads the form, omitting empty fields so they keep following the file. */
function operationalPayload() {
  const form = $('#form-operational');
  const body = {};

  for (const field of OPERATIONAL_FIELDS) {
    const input = form.elements[field];
    if (!input) continue;

    const raw = input.value.trim();
    if (raw !== '') body[field] = Number(raw);
  }

  return body;
}

function wireOperational() {
  $('#form-operational').addEventListener('submit', (e) => {
    e.preventDefault();
    saveForm(e.target,
      () => api('/settings/operational', {
        method: 'PUT', body: JSON.stringify(operationalPayload()),
      }),
      t('ops.saved', 'Limits saved'),
      loadOperational);
  });

  $('#operational-reset').addEventListener('click', () => {
    // Through saveForm like a save, because it is one: putting every value back
    // to the file is a decision, and what was typed before it is discarded on
    // purpose rather than kept from the reload.
    saveForm($('#form-operational'),
      () => api('/settings/operational', { method: 'PUT', body: JSON.stringify({}) }),
      t('ops.reset.done', 'All values follow the configuration file again'),
      loadOperational);
  });
}

async function loadOperational() {
  fillOperationalForm(await api('/settings/operational'));
}

// ----------------------------------------------------------------- updating

/**
 * Whether the image updater has answered that it replaced nothing.
 *
 * Asked of the version card's own state rather than of the announcement stream:
 * the stream can still be carrying a cancellation from an earlier press, while
 * an outcome on the card belongs to this one, because the request clears the
 * last.
 */
async function theImageUpdateChangedNothing() {
  try {
    return Boolean((await api('/settings/update'))?.imageOutcome);
  } catch {
    return false;
  }
}

/**
 * What version is running, whether a newer one exists, and what can be done
 * about it here.
 *
 * The card says something true on every deployment, which is the whole
 * difficulty: a single binary can fetch its successor and put it in its own
 * place, and a container cannot - a binary swapped inside one is undone by the
 * next recreate, which is the moment somebody is most certain the update took.
 * So where installing would not last, the card says what to run instead rather
 * than offering a button that reverts itself.
 */
async function loadUpdate() {
  const card = $('#update-card');
  if (!card) return;

  // Everybody who can reach this screen, and nobody narrower: a card that appears
  // for some of the people who got here and not others is a rule nobody can hold
  // in their head while using it.
  if (!can('settings:manage')) {
    card.hidden = true;

    return;
  }

  let state;

  try {
    state = await api('/settings/update');
  } catch {
    // A failed lookup is not a broken installation, and this is the one card on
    // the screen that depends on somebody else's service being up.
    card.hidden = true;

    return;
  }

  card.hidden = false;
  redrawable('update', () => renderUpdate(state));
}

function renderUpdate(state) {
  const running = t('update.running', 'This installation runs {0}')
    .replace('{0}', state.running);

  const line = $('#update-state');
  const hint = $('#update-hint');
  const button = $('#update-now');
  const notes = $('#update-notes');
  const problem = $('#update-problem');
  const byHand = $('#update-by-hand');

  button.hidden = !state.available;
  byHand.hidden = true;
  notes.hidden = !state.url;

  if (state.url) notes.href = state.url;

  problem.hidden = !state.problem;

  // The server's own words for a lookup that failed - what went wrong reaching
  // somebody else's service is not a fixed set of sentences.
  if (state.problem) {
    // The sentence in the reader's language, and whatever the feed actually said
    // folded away underneath - the same shape every other failure here takes.
    // It used to be pasted on the end, so a German screen ended with a line of
    // somebody else's English: "… konnte nicht abgefragt werden: the release feed
    // answered 403".
    showRefusal(problem, {
      message: t('update.unreachable', 'Could not ask for the newest version'),
      detail: state.problem,
    });
  }

  // What the last image update came to, where it left this container running.
  // On the card, which is where whoever pressed the button looks; the banner
  // every screen saw has already been taken down.
  const image = $('#update-image');
  image.hidden = !state.imageOutcome;

  if (state.imageOutcome === 'none') {
    image.textContent = t('update.imageNothingNewer',
      'The last image update found nothing newer than the image this runs, so '
      + 'nothing was changed. Where QUBITDESK_VERSION names a version, the image follows '
      + 'that setting rather than this card.');
  } else if (state.imageOutcome) {
    showRefusal(image, {
      message: t('update.imageFailed',
        'The last image update did not work, and nothing was changed.'),
      detail: state.imageProblem,
    });
  }

  hint.hidden = false;

  if (!state.enabled) {
    line.textContent = running;
    hint.textContent = t('update.off',
      'Checking for new versions is switched off on this installation (UPDATE_CHECK).');

    return;
  }

  // Already downloaded and waiting. Said before anything else, or somebody who
  // has taken the update is offered it again.
  if (state.pending) {
    line.textContent = t('update.pending', '{0} is downloaded and starts with the next restart.')
      .replace('{0}', state.pending);

    hint.textContent = state.restartable
      ? t('update.pendingRestart', 'Use the restart above, or restart the application yourself.')
      : t(`restart.unsupported.${state.restartCode || 'other'}`,
        'Restart the application the way it was started.');

    return;
  }

  // A build not made from a release calls itself "dev" and cannot be ranked
  // against anything. Saying "dev is the newest version" would be untrue, and
  // the sort of untrue that stops somebody looking further.
  if (!state.comparable) {
    line.textContent = running;
    hint.textContent = state.latest
      ? t('update.unversioned', 'This build was not made from a release, so there is '
        + 'nothing to compare. The newest published version is {0}.')
        .replace('{0}', state.latest)
      : t('update.unversionedAlone', 'This build was not made from a release.');

    return;
  }

  if (!state.newer) {
    line.textContent = state.latest
      ? t('update.current', '{0} is the installed version.').replace('{0}', state.running)
      : running;

    hint.hidden = true;

    return;
  }

  line.textContent = t('update.found', '{0} is available. This installation runs {1}.')
    .replace('{0}', state.latest).replace('{1}', state.running);

  const promise = state.restartable
    ? t('update.willRestart', 'The download is checked against the release’s own checksum. '
      + 'The application restarts itself afterwards.')
    : t('update.willAskRestart', 'The download is checked against the release’s own '
      + 'checksum. Afterwards the application has to be restarted by hand — this '
      + 'platform cannot restart itself.');

  // Before anything that describes the download: there is none on offer, and
  // the card said there was beside a button that had gone.
  if (state.why === 'noBinary') {
    hint.textContent = t('update.noBinary',
      'The release published no build for this platform, so it cannot be installed '
      + 'from here. The release page says what it offers.');

    return;
  }

  // A container with an updater beside it takes the whole image, and what the
  // button does then is different enough to say plainly: it is not this
  // application replacing its own binary, it is something else replacing this
  // container.
  if (state.byImage) {
    hint.textContent = t('update.byImage',
      'A new image is pulled and this container is recreated from it, then the '
      + 'image it replaced is removed. The application is away for up to a minute '
      + 'and comes back as the new version - this page waits for it.');

    return;
  }

  // A container without one is not offered a button, and is told what to run.
  //
  // Swapping the binary works there and does not last: it changes this
  // container and not the image it was made from, so the next recreate brings
  // the old version back - and a recreate is how a container deployment applies
  // anything. An update that reverts on a day nobody connects to the button
  // they pressed is worse than no button.
  if (!state.installable) {
    hint.textContent = t('update.inContainer',
      'This runs in a container, where a replaced binary is undone the next time '
      + 'the container is recreated. Update the image by hand: '
      + 'docker compose pull && docker compose up -d - or add '
      + 'deploy/compose.update.yaml to the deployment and this card will do it.');

    // That command is Compose's with its overlays listed in COMPOSE_FILE. The
    // guide recommends pinning the version and describes overlays given with -f
    // and a bare docker run, and it does not take any of those as it stands.
    byHand.hidden = false;

    return;
  }

  hint.textContent = promise;
}

/**
 * Waits out a process that is replacing itself, and says how it went.
 *
 * The restart button and the update button both end here, because both leave
 * the application starting again and both have to tell somebody whether it came
 * back. Written out twice, the two endings had already drifted: the same
 * translation key was given two different English fallbacks, one with a hyphen
 * and one with an em dash, so which sentence an untranslated screen showed
 * depended on which button had been pressed.
 *
 * The screen is refreshed before the good news rather than after it: refreshAll
 * redraws every card from the process that has just come back, and a message
 * put up first is a message competing with that.
 *
 * givenUp is asked on every look, and ends the wait when the answer is that no
 * restart is coming. An image update that found nothing newer, or failed,
 * leaves this process running - and without it the screen sat under the
 * overlay for the whole five minutes and then called the application slow. The
 * card says what the answer was, so the card is what is redrawn; the banner
 * every screen saw has already been taken down by the server.
 */
async function settleAfterRestart(previous, done, patience, givenUp = async () => false) {
  const overlay = $('#restart-overlay');
  const cameBack = await waitForRestart(previous, patience, givenUp);

  // The installer explains itself, and nothing on this page applies to it any
  // more: the overlay stays up until the page it asked for replaces this one.
  if (cameBack === 'installer') {
    window.location.reload();

    return;
  }

  if (cameBack) {
    // A different build came back, so everything in this tab is last version's.
    //
    // This was the one tab that did not reload. Every other open one did: the
    // announcement stream drops when the application goes away, reconnects to
    // the new one, and reloads because the last thing it heard was that a
    // restart was coming. The tab that pressed the button ran this instead,
    // which refreshed every card from the new server and left the script, the
    // stylesheet and the markup exactly as the old version had built them - so
    // an update that changed the interface showed nothing at all until somebody
    // reloaded by hand.
    //
    // And it was worse than doing nothing, because refreshAll below restarts
    // the announcement stream, and restarting it clears the record of what was
    // last announced. The reconnection that would have reloaded this tab a
    // moment later found nothing to act on. The one tab that knew an update had
    // happened was the one that forgot.
    //
    // Decided on the version rather than on which button was pressed. A restart
    // that comes back as the same build changed no assets, and throwing away
    // the scroll position, the open card and whatever is half-typed would be a
    // cost with nothing bought by it.
    //
    // The overlay is left standing on purpose: hiding it and then reloading is
    // a flash of a screen nobody gets to use.
    if (await theVersionChanged()) {
      sayAfterTheReload(done);
      window.location.reload();

      return;
    }

    overlay.hidden = true;
    await refreshAll();
    toast(done, 'ok');

    return;
  }

  if (await givenUp()) {
    overlay.hidden = true;
    await loadUpdate();

    return;
  }

  // Not an error as such: it may still be coming back. Saying that is more use
  // than a spinner that never stops.
  overlay.hidden = true;
  toast(t('restart.slow',
    'The application has not answered yet. It may still be starting - please '
    + 'reload the page in a moment.'), 'error');
}

/**
 * Whether the process that came back is a different build from the one that
 * built this page.
 *
 * /branding rather than the version card: it is one small public answer that
 * every deployment gives, it is the same one this page read at start-up, and it
 * needs no permission - so this holds for whoever happened to press the button.
 *
 * A failure is not a version change. The application has only just come back and
 * one refused request is not evidence of anything; saying "no" leaves the screen
 * exactly as it was, which is the outcome that loses nothing.
 */
async function theVersionChanged() {
  if (!loadedVersion) return false;

  try {
    const branding = await api('/branding');

    return Boolean(branding?.version) && branding.version !== loadedVersion;
  } catch {
    return false;
  }
}

/** Where a message waits out a reload. */
const AFTER_RELOAD_KEY = 'qubitdesk_after_reload';

/**
 * Keeps one sentence for the document that comes next.
 *
 * Somebody pressed a button and is owed the answer to it, and the answer arrives
 * after the page it was pressed on has been thrown away. Without this the update
 * would finish in silence: the overlay goes, a new document appears, and nothing
 * on it says that what was asked for actually happened.
 *
 * sessionStorage rather than a query parameter: it belongs to this tab, it does
 * not survive into a bookmark, and it does not put a message in the address bar
 * that a reload would show again.
 */
function sayAfterTheReload(message) {
  try {
    window.sessionStorage.setItem(AFTER_RELOAD_KEY, message);
  } catch {
    // A browser with no storage, or one refusing it. The reload is the point and
    // it still happens; only the sentence is lost.
  }
}

/**
 * Says it, once.
 *
 * Removed before it is shown rather than after, so a failure to draw it cannot
 * leave a message that reappears on every load for the rest of the session.
 */
function saySoAfterTheReload() {
  let message = null;

  try {
    message = window.sessionStorage.getItem(AFTER_RELOAD_KEY);
    window.sessionStorage.removeItem(AFTER_RELOAD_KEY);
  } catch {
    return;
  }

  if (message) toast(message, 'ok');
}

/**
 * The button that asks the release feed now.
 *
 * The card is drawn from an answer the server keeps for six hours, because the
 * feed allows sixty unauthenticated requests an hour per address and every open
 * card would otherwise spend them. That is right for a screen refreshing itself
 * and wrong for the minute after somebody publishes a release, when the card
 * states the version before it and is not going to change its mind until the
 * evening.
 *
 * The whole state comes back, so the card is redrawn from the answer rather than
 * loaded again - one request, and the same rendering path a fresh load takes.
 */
// ------------------------------------------------------------------- backups

/** The backup card's last answer, kept so a change of language redraws it. */
let backupState = null;

/**
 * Reads the backup card: the plan, the backups kept, and whether one can be made
 * at all. Only for an account that may administer the installation, as the
 * server asks: a backup is all of it.
 */
async function loadBackups() {
  const card = $('#backup-card');
  if (!card) return;

  if (!can('settings:manage')) {
    card.hidden = true;

    return;
  }

  backupState = await api('/settings/backups');
  card.hidden = false;
  redrawable('backups', drawBackups);
}

function drawBackups() {
  const state = backupState;
  if (!state) return;

  // Without the passphrase the server refuses every backup, so the button is not
  // offered and the card says what to set instead.
  const off = $('#backup-off');
  off.hidden = state.enabled;
  off.textContent = fillIn(t('backup.off',
    'No backup is made until BACKUP_PASSPHRASE is set on the server - at least {0} '
    + 'characters - and the application restarted. Every backup is sealed with it, and '
    + 'an unsealed one is not offered.'), [state.minPassphraseLength]);
  $('#backup-now').hidden = !state.enabled;

  $('#backup-folder').textContent = fillIn(t('backup.folder', 'Kept in {0} on the server.'), [state.folder]);

  const form = $('#form-backup-plan');
  if (!beingEdited(form)) {
    form.elements.schedule.value = state.schedule ?? '';
    form.elements.keep.value = String(state.keep);
  }

  form.elements.keep.max = String(state.maxKeep);

  const rows = (state.backups ?? []).map((kept) => el('tr', {},
    el('td', { text: fmtMoment(kept.made) }),
    el('td', { class: 'num', text: fmtFileSize(kept.size) }),
    el('td', { class: 'actions' },
      el('button', {
        type: 'button',
        class: 'link',
        'data-action': 'download',
        text: t('backup.download', 'download'),
        onclick: () => mutate(() => downloadBackup(kept.name), null, null),
      }),
      deleteButton({
        label: kept.name,
        path: `/settings/backups/${encodeURIComponent(kept.name)}`,
        message: t('backup.deleted', 'Backup deleted'),
        after: loadBackups,
      })),
  ));

  fillTable($('#table-backups tbody'), rows, 3, t('backup.none', 'No backup has been kept yet.'));
}

/** Saves a kept backup under its own name, which says when it was made. */
function downloadBackup(name) {
  return saveDownload(`${API}/settings/backups/${encodeURIComponent(name)}`, name);
}

/** A file's size in the unit it is read in, written the reader's way. */
function fmtFileSize(bytes) {
  const megabytes = bytes >= 1024 * 1024;

  return new Intl.NumberFormat(activeLocale(), {
    style: 'unit',
    unit: megabytes ? 'megabyte' : 'kilobyte',
    maximumFractionDigits: megabytes ? 1 : 0,
  }).format(megabytes ? bytes / (1024 * 1024) : Math.max(1, Math.round(bytes / 1024)));
}

/**
 * Making one downloads it as well - what somebody pressing "Back up now" wants
 * is the file - and saving the plan answers with the whole card, drawn from
 * what was kept rather than from what was typed.
 */
function wireBackups() {
  $('#backup-now')?.addEventListener('click', () => mutate(
    () => api('/settings/backups', { method: 'POST' }),
    t('backup.madeNow', 'Backup made'),
    async (made) => {
      await loadBackups();
      await downloadBackup(made.name);
    },
  ));

  $('#form-backup-plan')?.addEventListener('submit', (e) => {
    e.preventDefault();

    const form = e.target;

    saveForm(form, () => api('/settings/backups/plan', {
      method: 'PUT',
      body: JSON.stringify({
        schedule: form.elements.schedule.value.trim(),
        keep: Number(form.elements.keep.value),
      }),
    }), t('admin.saved', 'Settings saved'), (state) => {
      backupState = state;
      redrawable('backups', drawBackups);
    });
  });
}

function wireUpdateCheck() {
  const button = $('#update-check');
  if (!button) return;

  button.addEventListener('click', async () => {
    const wasSaying = button.textContent;

    // Hold the width the button already has, for as long as it says something
    // else.
    //
    // "Wird gesucht ..." is not the width of "Aktualisierung suchen", so swapping
    // the label resized the button, which moved the row, which shifted the card -
    // and swapping it back a moment later moved everything home again. Pressing
    // it made the card twitch.
    //
    // Measured rather than set in the stylesheet, because the two labels are
    // different widths in every language and a number that fits both in German
    // is a gap in English. Through the CSSOM rather than a style attribute: the
    // policy forbids the attribute, and this is the same distinction the chart
    // export had to learn.
    button.style.minWidth = `${button.offsetWidth}px`;

    const giveBack = holdFocus(button);

    button.disabled = true;
    button.textContent = t('update.checking', 'Looking …');

    try {
      const state = await api('/settings/update/check', { method: 'POST' });

      redrawable('update', () => renderUpdate(state));

      // And the banner, which is the same answer put where whoever is not on
      // this screen would see it. It stays up until it stops being true: a
      // version that exists goes on existing after the card has scrolled away.
      showReleaseState(state);
    } catch (err) {
      // Including "asked a moment ago", which is a sentence rather than a
      // failure: the answer on the card is current, and saying so is better than
      // a button that appears to do nothing.
      toastFailure(err);
    } finally {
      button.disabled = false;
      button.textContent = wasSaying;
      button.style.minWidth = '';
      giveBack();
    }
  });
}

function wireUpdate() {
  const button = $('#update-now');
  if (!button) return;

  button.addEventListener('click', async () => {
    const proceed = await confirmDialog({
      title: t('update.title', 'Version'),
      text: t('update.confirm',
        'Download the new version and install it? The application restarts afterwards.'),
      confirmLabel: t('update.install', 'Download and install'),
    });

    if (!proceed) return;

    // The same overlay a restart uses, and for the same reason: this takes tens
    // of seconds - a thirty-megabyte download and a hash over it - and the thin
    // strip at the top of the page is not enough to say "do not press that
    // again". It also keeps the screen from being used while the binary
    // underneath it is being replaced.
    const overlay = $('#restart-overlay');
    const status = $('#restart-status');
    const previous = restartStartedAt;

    overlay.hidden = false;
    status.textContent = t('update.downloading',
      'Downloading and checking the new version …');

    let state;

    try {
      state = await api('/settings/update', { method: 'POST' });
    } catch (err) {
      overlay.hidden = true;
      toastFailure(err);

      return;
    }

    // Something else is replacing this container, so there is nothing to ask
    // for: the wait is the whole of what is left to do.
    //
    // This used to fall through to the restart below, and the result was the
    // opposite of an update. The POST stopped the application; its restart
    // policy started the same container again from the same, old image; and the
    // updater's recreate arrived into the middle of that. The version that came
    // back was whichever won, which was usually the one that was already there -
    // reported as "it did not restart, and nothing changed".
    if (state?.byImage) {
      status.textContent = t('update.replacing',
        'A new image is being pulled and this container replaced …');

      await settleAfterRestart(previous,
        t('update.done', 'The new version is running.'), IMAGE_UPDATE_TIMEOUT_MS,
        theImageUpdateChangedNothing);

      return;
    }

    // In place. What happens next is the difference this card exists to say: a
    // platform that can replace its own process does it now, and one that
    // cannot has to be restarted by somebody.
    if (!state?.restartable) {
      overlay.hidden = true;
      await loadUpdate();
      await loadRestart();

      toast(t('update.installedRestart',
        'The new version is in place. Restart the application to use it.'), 'ok');

      return;
    }

    status.textContent = t('restart.waitingHint',
      'Waiting for the application to come back …');

    try {
      await api('/settings/restart', { method: 'POST' });
    } catch (err) {
      overlay.hidden = true;
      await loadUpdate();

      toastFailure(err, t('restart.failed', 'The restart could not be started'));

      return;
    }

    // This used to read "refreshAll rather than location.reload()", on the
    // grounds that refreshing every screen from the new process leaves the
    // reader where they were and a reload would not. Half of that is true and
    // the half it missed is the one that matters: refreshAll reloads the data,
    // and the script, the stylesheet and the markup in this tab are still the
    // ones the *previous* version wrote. So an update that changed anything
    // about the interface showed none of it until somebody reloaded by hand,
    // and the version in the footer - offered as the proof it had worked - was
    // the one thing that did update, which made it look like it had.
    //
    // settleAfterRestart now decides by what came back: a different build
    // reloads, the same build refreshes.
    await settleAfterRestart(previous, t('update.done', 'The new version is running.'));
  });
}

// ----------------------------------------------------------------- restart

/**
 * What each pending setting is called on screen.
 *
 * The server names the setting and leaves the wording here, so the list is
 * translated like everything else rather than arriving as a sentence in one
 * language.
 */
function pendingLabel(setting) {
  switch (setting) {
    case 'logLevel': return t('tel.logLevel', 'Log level');
    case 'metrics': return t('tel.metrics', 'Metrics endpoint');
    case 'traceExporter': return t('tel.exporter', 'Trace exporter');
    case 'tracerUrl': return t('tel.urlShort', 'Collector');
    case 'tracerRatio': return t('tel.ratioShort', 'Share of traces recorded');
    case 'database': return t('admin.database', 'Database connection');
    case 'databasePassword': return t('restart.dbPassword', 'Database password');
    case 'directorySchedule': return t('sync.scheduleShort', 'Directory schedule');
    default: return setting;
  }
}

/**
 * What a value reads as on the card.
 *
 * "" would look like a rendering fault. A switch arrives as "on" or "off" and a
 * share the way the form takes it, which is the API's vocabulary rather than
 * the reader's.
 */
function pendingValue(setting, value) {
  if (value === '') return t('restart.none', 'none');

  if (setting === 'metrics' && value === 'on') return t('restart.on', 'on');
  if (setting === 'metrics' && value === 'off') return t('restart.off', 'off');
  if (setting === 'tracerRatio' && Number.isFinite(Number(value))) return fmtShare(Number(value));

  return value;
}

/**
 * Shows what is waiting for a restart, and offers one.
 *
 * The comparison is the server's: it knows what this process started with and
 * what is stored, and working it out again here would be a second place for the
 * answer to be wrong in.
 */
async function loadRestart() {
  const banner = $('#restart-banner');
  if (!banner) return;

  // The same permission the screen that causes this needs. The banner is on
  // every screen rather than behind the tab the card sits on, so who may see it
  // is decided here rather than inherited from where it sits.
  if (!can('settings:manage')) {
    banner.hidden = true;

    const card = $('#restart-card');
    if (card) card.hidden = true;

    return;
  }

  const state = await api('/settings/restart');
  restartStartedAt = state.startedAt ?? '';

  const pending = state.pending ?? [];
  restartWaiting = pending.length > 0;

  // Only when something is actually waiting.
  //
  // It used to stay on screen wherever restarting is impossible - on Windows,
  // which has no execve - whether anything was pending or not, on the reasoning
  // that this is a standing property of the installation and worth knowing. It
  // is worth knowing at the moment it matters, which is when you have just saved
  // something that needs a restart. A notice that is always there is furniture:
  // it is read once, and after that it is the thing you look past.
  //
  // Nothing is lost by waiting. The card says what restarting does here and why
  // it cannot, whether or not anything is pending, and it is one press away
  // whether or not this notice is up.
  banner.hidden = pending.length === 0;

  const list = $('#restart-card-pending');
  list.replaceChildren(...pending.map((change) => {
    const label = el('strong', { text: pendingLabel(change.setting) });

    // One entry deliberately arrives without a before and an after: the database
    // password changed, and the server will not put the old one beside the new
    // one on a screen. "Passwort: none → none" reads as a fault rather than as
    // the discretion it is, so the name of the setting stands on its own.
    if (!change.running && !change.stored) return el('li', {}, label);

    return el('li', {}, label,
      el('span', { class: 'from', text: `: ${pendingValue(change.setting, change.running)} → ` }),
      el('strong', { text: pendingValue(change.setting, change.stored) }));
  }));

  const waiting = pending.length > 0;

  // The hint promises a list of saved changes and the list follows it, so both
  // go when there are none. On the card, which is where they live now: the
  // banner is the notice and points at this.
  $('#restart-card-hint').hidden = !waiting;
  $('#restart-card-pending').hidden = !waiting;

  // One sentence saying how many, with the way to the card inside it.
  //
  // The banner is only up while something is waiting, so this is always the
  // count - there is no "nothing is waiting" wording, because nobody could read
  // it.
  //
  // The link is a word in the sentence rather than a control beside it: what
  // somebody wants to press is the thing being talked about, and a button
  // reading "go to the restart" next to a sentence about a restart is the same
  // word twice. Split on the placeholder, so a translator decides where in
  // their sentence it falls.
  const summary = $('#restart-summary');

  if (summary) {
    summary.textContent = '';

    // Split on the placeholder rather than on a marker put through the filler:
    // where the link falls in the sentence is the translator's business, and
    // the two halves are whatever they wrote around it - the count included,
    // which is why both are filled. The verb belongs to the restart rather than
    // to the settings in both languages, because one setting is the usual case
    // and "1 saved setting(s) are waiting" agreed with none.
    const [before, after] = t('restart.summary',
      'A {1} is pending for {0} saved setting(s).').split('{1}');

    summary.append(fillIn(before ?? '', [pending.length]));

    const link = el('button', {
      type: 'button',
      class: 'link-button',
      id: 'restart-open',
      text: t('restart.open', 'restart'),
    });

    link.addEventListener('click', openTheRestartCard);
    summary.append(link);

    summary.append(fillIn(after ?? '', [pending.length]));
  }

  // What the button does, said before it is pressed rather than found out
  // afterwards.
  const description = state.mode === 'container'
    ? t('restart.modeContainer',
      'This installation runs in a container. The button stops it, and your '
      + 'container manager starts it again - which it only does under a restart '
      + 'policy that also restarts a container that ended without an error: '
      + 'always or unless-stopped, not on-failure. The deployment shipped with '
      + 'this application sets one.')
    : t('restart.modeProcess',
      'The application replaces itself, so it is never not running.');

  // Translated here rather than shown as it arrives: the server writes this in
  // English at the point the limitation is decided, which is right for a log and
  // wrong for the person reading the screen.
  //
  // Keyed on the code rather than on one sentence for every refusal. There is more
  // than one: Windows has no execve, and on unix the running binary can fail to be
  // located. A single translated sentence would tell a Linux reader they are on
  // Windows and throw away the diagnostic that named the real fault. The server's
  // own wording is the fallback, so a reason nobody has translated still says
  // something true.
  const refusal = state.supported
    ? ''
    : t(`restart.unsupported.${state.reasonCode || 'other'}`, state.reason ?? '');

  // Offered only where pressing it would actually work. Where it would not, the
  // reason is shown instead of a button that fails on click.
  showRestartControls('#restart-card-mode', '#restart-card-now',
    '#restart-card-unsupported', description, refusal, state.supported, true);

  // The card appears for the same reason the notice does, and goes with it.
  //
  // It was always on screen for a while, so that a restart could be asked for
  // on purpose and so the screen said what one would do here. What that
  // produced was a card offering a restart beside the version card offering an
  // update - and the update button restarts by itself, so the two read as two
  // ways to do one thing, one of which is the wrong one.
  //
  // A restart on its own is not a thing anybody wants; it is what some other
  // change needs. So the card is here when something needs it and says what,
  // and the update looks after its own.
  const card = $('#restart-card');
  if (card) card.hidden = !waiting;
}

/**
 * Writes one set of restart controls: what it would do, the button, the reason
 * there is none.
 *
 * Two sets exist and they show the same answer - the banner and the card - so
 * this is called twice rather than written twice. The only thing that differs
 * is when the description is worth showing: in the banner it belongs to the
 * moment something is waiting, and in the card it is the whole point.
 */
function showRestartControls(modeSelector, buttonSelector, refusedSelector,
  description, refusal, supported, describe) {
  const mode = $(modeSelector);

  if (mode) {
    mode.textContent = description;
    mode.hidden = !supported || !describe;
  }

  const button = $(buttonSelector);
  if (button) button.hidden = !supported;

  const refused = $(refusedSelector);

  if (refused) {
    refused.textContent = refusal;
    refused.hidden = supported;
  }
}

/** The identity of the running process, to tell a restart from a hiccup. */
let restartStartedAt = '';

/** Whether anything is stored that the running process has not picked up. */
let restartWaiting = false;

/**
 * Reports a save on one of the cards the framework only reads while starting.
 *
 * Both of them used to announce "Applied on the next start" unconditionally, so
 * pressing Save on a form you had only looked at, or twice in a row, promised a
 * restart that would change nothing. The server already knows which it was: the
 * restart card is a comparison of what is running against what is stored, so an
 * empty list after the save means nothing is waiting.
 *
 * Call it after loadRestart, which is what fills that in - the save paths reload
 * the card anyway, so this costs no request of its own.
 */
function announceSave() {
  toast(restartWaiting
    ? t('admin.restartNeeded', 'Saved. Applied on the next start.')
    : t('admin.saved', 'Settings saved'), 'ok');
}

/** How long to wait for the application to come back before giving up on it. */
const RESTART_TIMEOUT_MS = 60000;

/**
 * And how long where a new image has to be fetched first.
 *
 * A restart is this process starting again, which is seconds. Replacing the
 * image is a download of some tens of megabytes over whatever connection the
 * server has, then a container recreated from it - and on a small machine at
 * the end of a domestic line that is minutes, not seconds. Giving up at one
 * would tell somebody it had failed while it was still working.
 */
const IMAGE_UPDATE_TIMEOUT_MS = 5 * 60000;

/**
 * Waits for a different process to answer.
 *
 * Polling for "does it respond" is not enough: replacing the process image takes
 * milliseconds, and a poll that misses that gap would report success without
 * anything having happened. The start time changing is what proves it.
 *
 * Answers 'installer' when that is what came back - see theInstallerAnswers.
 *
 * givenUp ends the wait early, with the same answer as running out of patience;
 * see settleAfterRestart.
 */
async function waitForRestart(previousStartedAt, patience = RESTART_TIMEOUT_MS,
  givenUp = async () => false) {
  const deadline = Date.now() + patience;

  while (Date.now() < deadline) {
    await new Promise((resolve) => { setTimeout(resolve, 1000); });

    if (await givenUp()) return false;

    try {
      const state = await api('/settings/restart');
      if (state.startedAt && state.startedAt !== previousStartedAt) return true;
    } catch {
      // Expected while it is down: the connection is refused, or the session
      // has not been read back out of the database yet.
      if (await theInstallerAnswers()) return 'installer';
    }
  }

  return false;
}

/**
 * Whether the installer, rather than the application, answers on this address.
 *
 * What a restart comes back as when no connection is left: it reads the
 * configuration afresh, so a connection file that was removed - which is how the
 * manual brings the installer back - takes the installation to its installer.
 * That answers every path with its page, so the wait read it as an application
 * not up yet and said after a minute that it might still be starting, of
 * something that had been answering all along. Told apart the way the
 * installer's own page tells them apart: it answers /install/state with JSON,
 * where the application answers with a document.
 */
async function theInstallerAnswers() {
  try {
    const res = await reach('/install/state', { cache: 'no-store' });

    return res.ok && (res.headers.get('content-type') || '').includes('json');
  } catch {
    return false;
  }
}


/**
 * Takes whoever pressed it to the card that performs the restart.
 *
 * The banner restarts nothing; it says one is waiting and leads to where it
 * happens. One button for one action, in the one place the detail is - which is
 * what stops the notice and the control drifting apart.
 */
function openTheRestartCard() {
  switchView('admin');

  const card = $('#restart-card');
  openTheTabOf(card);
  if (card) card.scrollIntoView({ block: 'center' });
}

/**
 * Wires the one button that restarts the application.
 *
 * One button, and that is the design rather than the current state: the banner
 * says a restart is waiting and leads to the card, it does not perform one - see
 * openTheRestartCard. This took a button as an argument because there were two,
 * and kept taking one after the second was deliberately removed, which left a
 * function whose parameter existed to describe a choice nobody has.
 */
function wireRestart() {
  const button = $('#restart-card-now');
  if (!button) return;

  button.addEventListener('click', async () => {
    const proceed = await confirmDialog({
      title: t('restart.title', 'Restart'),
      text: t('restart.confirm',
        'Restart the application? Anyone working in it will have to reload the page.'),
      confirmLabel: t('restart.now', 'Restart now'),
    });

    if (!proceed) return;

    const overlay = $('#restart-overlay');
    const status = $('#restart-status');
    const previous = restartStartedAt;

    overlay.hidden = false;
    status.textContent = t('restart.waitingHint', 'Waiting for the application to come back …');

    try {
      await api('/settings/restart', { method: 'POST' });
    } catch (err) {
      overlay.hidden = true;
      toastFailure(err, t('restart.failed', 'The restart could not be started'));

      return;
    }

    await settleAfterRestart(previous,
      t('restart.done', 'The application has restarted and the settings are in force.'));
  });
}

// ------------------------------------------------------ revealing a password

/**
 * Draws the eye, with a slash that is shown only while the password is readable.
 *
 * Built here rather than written into the markup because the button itself is,
 * and inline rather than fetched because the Content-Security-Policy allows no
 * external origin at all. createElementNS, not createElement: an <svg> built in
 * the HTML namespace parses without complaint and renders nothing.
 */
function passwordToggleIcon() {
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');

  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('width', '18');
  svg.setAttribute('height', '18');
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '2');
  svg.setAttribute('stroke-linecap', 'round');
  // The button already carries the label; the drawing would only repeat it.
  svg.setAttribute('aria-hidden', 'true');

  const outline = document.createElementNS(ns, 'path');
  outline.setAttribute('d', 'M2 12s3.6-6.5 10-6.5S22 12 22 12s-3.6 6.5-10 6.5S2 12 2 12z');

  const pupil = document.createElementNS(ns, 'circle');
  pupil.setAttribute('cx', '12');
  pupil.setAttribute('cy', '12');
  pupil.setAttribute('r', '3');

  const slash = document.createElementNS(ns, 'path');
  slash.setAttribute('d', 'M3 3l18 18');
  slash.setAttribute('class', 'password-slash');

  svg.append(outline, pupil, slash);

  return svg;
}

/**
 * Labels the button for what pressing it will do.
 *
 * The key is kept on the element as well as applied, so switching language
 * re-labels it through applyLanguage instead of leaving the last language's
 * word on a button nobody looks at twice.
 */
function labelPasswordToggle(button, revealed) {
  const key = revealed ? 'password.hide' : 'password.reveal';
  const label = revealed
    ? t('password.hide', 'Hide the password')
    : t('password.reveal', 'Show the password');

  button.dataset.i18nAria = key;
  button.setAttribute('aria-label', label);
  button.title = label;
  button.setAttribute('aria-pressed', String(revealed));
  button.classList.toggle('revealed', revealed);
}

/**
 * Puts every revealed password back behind its dots.
 *
 * wirePasswordReveal says when that should happen - "when the form is submitted
 * or the page is left" - and only the submit was wired. Nothing re-renders these
 * inputs, so a field left showing stayed showing: through a screen change, and
 * through a sign-out, which resets the form's value and does nothing to its type.
 * The next person at that desk then typed their password into a box that showed
 * it.
 *
 * Found by the wrapper rather than by a list, for the same reason the button is
 * given by selector: the next password field added gets this without anybody
 * remembering to ask.
 */
function hideEveryRevealedPassword() {
  for (const field of $$('.password-field')) {
    const input = field.querySelector('input[type="text"]');
    if (!input) continue;

    input.type = 'password';

    const button = field.querySelector('button.password-toggle');
    if (button) labelPasswordToggle(button, false);
  }
}

/**
 * Gives every password field a button that reveals what was typed.
 *
 * Every field, found by selector rather than listed, so the next one added gets
 * this without anyone remembering to ask. It matters most where the value is not
 * a password being recalled but one being transcribed - a database or bind
 * password copied from somewhere else, where a silent typo is answered minutes
 * later by "connection refused" and nothing on screen says which character was
 * wrong.
 *
 * The field goes back to being hidden when the form is submitted or the page is
 * left, because the browser is where it stays visible otherwise: nothing here
 * re-renders these inputs.
 */
function wirePasswordReveal(root = document) {
  for (const input of root.querySelectorAll('input[type="password"]')) {
    // Running twice would nest a second wrapper and a second button.
    if (input.parentElement?.classList.contains('password-field')) continue;

    const field = document.createElement('span');
    field.className = 'password-field';

    input.replaceWith(field);
    field.append(input);

    const button = document.createElement('button');
    // Not a submit: inside a form, a button with no type submits it, so the
    // first attempt to look at a password would send the form instead.
    button.type = 'button';
    button.className = 'password-toggle';
    button.append(passwordToggleIcon());
    labelPasswordToggle(button, false);

    button.addEventListener('click', () => {
      const revealed = input.type === 'password';
      input.type = revealed ? 'text' : 'password';
      labelPasswordToggle(button, revealed);

      // The caret goes back where it was: the type change moves focus off the
      // field in some browsers and puts it at the end in others, and continuing
      // to type is the normal next act.
      const caret = input.value.length;

      input.focus();
      input.setSelectionRange(caret, caret);
    });

    // Submitting is the end of looking at it. Left revealed, the value would
    // still be on screen behind whatever the save put there.
    input.form?.addEventListener('submit', () => {
      input.type = 'password';
      labelPasswordToggle(button, false);
    });

    field.append(button);
  }
}

// ------------------------------------------- export and import, table by table

/**
 * Fetches a file and hands it to the browser to save under a readable name.
 *
 * Not api(): that one decodes JSON and would have to be taught about blobs, and
 * about not aborting a read that is a download. This is the plainer thing - ask,
 * check, save - and the two callers below differ only in how they ask.
 */
async function downloadFile(url, name, extension, request = {}) {
  await saveDownload(url, `${name}-${todayISO()}.${extension}`, request);
}

/**
 * Fetches a file and saves it under exactly this name - a ticket's attachment
 * keeps the name it came with. The server sends every attachment as plain
 * bytes, and the name is what tells the reader's computer what it is.
 */
async function saveDownload(url, filename, request = {}) {
  const res = await reach(url, { credentials: 'same-origin', ...request });

  // Everything api() reads off an answer that is not its body. Only the body is
  // this function's own business - it wants a blob, which is why it asks
  // directly - and the rest means what it always means.
  noticePermissionChange(res.headers.get('X-Permissions-Revision'));

  if (!res.ok) {
    let body = null;
    try {
      body = await res.json();
    } catch {
      // An error page rather than JSON; the status carries the meaning.
    }

    throw refusalFrom(res, body);
  }

  const blob = await res.blob();
  const objectURL = URL.createObjectURL(blob);

  const link = el('a', { href: objectURL, download: filename });
  document.body.append(link);
  link.click();
  link.remove();

  // Released once the browser has taken it; a blob left behind holds the whole
  // file in memory for as long as the page is open.
  URL.revokeObjectURL(objectURL);
}

/**
 * Fetches a spreadsheet export.
 *
 * The language travels with the request, so the headings in the file are the ones
 * on the screen of whoever asked for it. It is a query parameter rather than the
 * account's setting: the file is about to be opened by the person looking at this
 * screen, in the language they are reading it in.
 */
async function downloadSheet(path, name) {
  const separator = path.includes('?') ? '&' : '?';

  await downloadFile(
    `${API}${path}${separator}lang=${encodeURIComponent(activeLanguage())}`, name, 'xlsx');
}

/**
 * Why a row of an imported file cannot be written, in the reader's language.
 *
 * The preview around it is translated - the headings, and the cells, down to a
 * status column reading "archiviert" because that is the word the file used - and
 * this column was English prose. The server writes it where the rule is enforced,
 * which is right for the log and wrong for the person who typed the row, so it
 * sends a code and the values its sentence interpolated as well, exactly as it
 * does for a refused request.
 */
function rowProblem(row) {
  if (!row.problem) return '';
  if (!row.problemCode) return row.problem;

  // A row refused for the shape of its values carries the column names, which are
  // not what the labels above the form say - the same case errorMessage handles,
  // and named from the same list so both screens use one word per field.
  if (row.problemCode === 'invalidFields') {
    const named = (row.problemValues ?? []).map(fieldName);

    return `${t('err.invalidFields', 'Invalid field(s)')}: ${named.join(', ')}`;
  }

  // Two prefixes, because some rows are refused by a rule the form enforces too -
  // a day over its ceiling is the same refusal whether it arrives one entry at a
  // time or eighty at once, and it already has a sentence under err.
  const sentence = t(`row.${row.problemCode}`, '')
    || t(`err.${row.problemCode}`, row.problem);

  return fillIn(sentence, refusalValues(row.problemCode, row.problemValues));
}

/** The resets of the tables' import cards, which buildSheetCard builds. */
const sheetCardResets = [];

/** Puts every import card back to its resting state. */
function forgetEveryImport() {
  for (const reset of sheetCardResets) reset();
}

/**
 * The tables that go in and out as a spreadsheet.
 *
 * Each one exports what its own tab shows and imports into its own table, which is
 * the whole point: a single pair of buttons somewhere general would have meant
 * guessing which table somebody had in mind.
 *
 * Two tables are deliberately absent, for two different reasons. A token's
 * secret exists once, at the moment it is created, and is not in the database to
 * export. A passkey is bound to the device holding it and means nothing anywhere
 * else. Neither is a table a spreadsheet can carry honestly.
 *
 * A role's rights are not one cell holding "tickets:read,tickets:write,...",
 * which is a list that has to be exactly right and where a typo removes a right
 * silently: the sheet gives every permission a column of its own holding yes or
 * no, so a typo is a heading that does not match a right the application
 * enforces, and the import refuses the file by name. Reviewing what several
 * roles may do is a grid, and the role screen shows one role at a time.
 */
const SHEET_CARDS = [
  {
    key: 'users',
    path: '/users',
    view: '#view-users',
    read: 'users:read',
    write: 'users:write',
    reload: () => refreshAll(),
  },
  {
    key: 'roles',
    path: '/roles',
    view: '#view-roles',
    read: 'roles:read',
    write: 'roles:write',
    reload: () => refreshAll(),
  },
];

/**
 * Builds the export/import card for one table.
 *
 * Built here rather than written into index.html once per table. The markup is
 * identical for every table - the only differences are the words and the endpoint -
 * and a copy of it per table is a place per table to fix the next time one of these
 * buttons needs to change.
 *
 * The preview is driven by the answer: the server sends its column headings, already
 * translated, so this renders a file of roles and a file of people with the same code
 * and needs to know nothing about either.
 */
function buildSheetCard(spec) {
  const summary = el('p', { class: 'muted' });
  const headRow = el('tr');
  const body = el('tbody');

  const wrap = el('div', { class: 'table-wrap' },
    el('table', {}, el('thead', {}, headRow), body));
  wrap.hidden = true;

  const file = el('input', {
    type: 'file',
    accept: '.xlsx,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
  });

  const check = el('button', {
    type: 'button', class: 'secondary',
    'data-i18n': 'wb.preview', text: 'Check the file',
  });

  const write = el('button', { type: 'button', 'data-i18n': 'wb.import', text: 'Import' });

  const cancel = el('button', {
    type: 'button', class: 'link', 'data-i18n': 'wb.clear', text: 'cancel',
  });

  for (const button of [check, write, cancel]) button.hidden = true;

  const reset = () => {
    file.value = '';

    stopRedrawing(`sheetPreview:${spec.key}`);

    for (const button of [check, write, cancel]) button.hidden = true;

    wrap.hidden = true;
    summary.textContent = '';
    headRow.replaceChildren();
    body.replaceChildren();
  };

  const send = async (dryRun) => {
    const chosen = file.files?.[0];
    if (!chosen) throw new Error(t('wb.noFile', 'Choose a file first.'));

    const form = new FormData();
    form.append('file', chosen);
    form.append('dryRun', dryRun ? 'true' : 'false');

    // No Content-Type of our own: only the browser knows the multipart boundary
    // it generated.
    const res = await reach(`${API}${spec.path}/import`
      + `?lang=${encodeURIComponent(activeLanguage())}`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'X-CSRF-Token': readCookie('qubitdesk_csrf') },
      body: form,
    });

    noticePermissionChange(res.headers.get('X-Permissions-Revision'));

    let payload = null;
    try {
      payload = await res.json();
    } catch {
      // Falls through to the status below.
    }

    if (!res.ok) {
      throw refusalFrom(res, payload);
    }

    return payload?.data ?? null;
  };

  const preview = (result) => {
    const columns = result?.columns ?? [];

    headRow.replaceChildren(
      el('th', { text: t('wb.row', 'Row') }),
      ...columns.map((column) => el('th', { text: column })),
      el('th', { text: t('wb.problem', 'Problem') }),
    );

    const rows = (result?.rows ?? []).map((row) => el('tr',
      { class: row.problem ? 'rejected' : '' },
      el('td', { class: 'num', text: String(row.row) }),
      // Padded to the column count, because a row the file could not be read at
      // all has no cells and would otherwise leave the table ragged.
      ...columns.map((_, i) => el('td', { text: (row.cells ?? [])[i] || '–' })),
      el('td', { class: row.problem ? 'minus' : 'muted', text: rowProblem(row) || '✓' }),
    ));

    fillTable(body, rows, columns.length + 2, t('wb.empty', 'The file has no entries in it.'));
    wrap.hidden = false;

    const writable = result?.writable ?? 0;
    const rejected = result?.rejected ?? 0;

    summary.textContent = rejected > 0
      ? t('wb.someRejected',
        '{0} of {1} rows can be imported. Nothing is written while any row is refused.')
        .replace('{0}', String(writable)).replace('{1}', String(writable + rejected))
      : t('wb.allReady', 'All {0} rows can be imported.').replace('{0}', String(writable));

    // The import button only where it would do something: offering it for a file
    // that would be refused is offering a failure.
    write.hidden = rejected > 0 || writable === 0;
  };

  const exportButton = el('button', {
    type: 'button', class: 'secondary',
    'data-i18n': 'wb.export', text: 'Export as .xlsx',
    onclick: () => mutate(
      () => downloadSheet(`${spec.path}/export`, t(`sheet.${spec.key}.file`, spec.key)),
      t('wb.exported', 'Export saved'), null),
  });

  const picker = el('label', { class: 'file-pick', 'data-perm': spec.write },
    el('span', { 'data-i18n': 'wb.choose', text: 'Choose a file…' }), file);

  file.addEventListener('change', () => {
    const chosen = Boolean(file.files?.length);

    // The same line reset() carries, for the same reason: the preview described
    // the file that was chosen before this one, so there is nothing left to draw
    // again. Without it the draw stayed registered and the next language change
    // put that verdict back on screen above a file nobody had checked - and a
    // clean verdict brings the Import button with it.
    stopRedrawing(`sheetPreview:${spec.key}`);

    check.hidden = !chosen;
    cancel.hidden = !chosen;
    write.hidden = true;
    wrap.hidden = true;
    summary.textContent = chosen ? t('wb.chosen', 'Check the file to see what it would do.') : '';
  });

  // Keyed per card, so the cards this builds keep a preview each rather than
  // overwriting each other's.
  check.addEventListener('click', () => mutate(() => send(true), null,
    (result) => redrawable(`sheetPreview:${spec.key}`, () => preview(result))));

  write.addEventListener('click', () => mutate(() => send(false),
    t('wb.imported', 'The file was imported'),
    async (result) => {
      reset();
      await spec.reload();

      if (result?.imported) {
        toast(t(`sheet.${spec.key}.done`, '{0} rows written.')
          .replace('{0}', String(result.imported)), 'ok');
      }
    }));

  cancel.addEventListener('click', reset);
  sheetCardResets.push(reset);

  return el('div', { class: 'card', 'data-perm': spec.read },
    el('h2', { 'data-i18n': 'wb.title', text: 'Spreadsheet' }),
    el('p', {
      class: 'muted',
      'data-i18n': `sheet.${spec.key}.text`,
      text: SHEET_TEXTS[spec.key],
    }),
    el('div', { class: 'row' }, exportButton, picker, check, write, cancel),
    summary,
    wrap);
}

/**
 * What each card says it does, in English, which is the source language.
 *
 * Here rather than in the markup because the card is built rather than written, and
 * the paragraph is the one part of it that differs per table - each import behaves
 * differently enough that a shared sentence would be wrong for at least one of
 * them.
 */
const SHEET_TEXTS = {
  roles: 'Exports every role with a column per permission, holding yes or no - which '
    + 'is the grid you want when the question is what several roles may do. An import '
    + 'matches rows by name, creates a role that is not there yet, and refuses a '
    + 'heading naming a right this application does not enforce. A system role can be '
    + 'described differently and nothing else.',
  users: 'Exports every account: name, email, role, and whether its password lives in '
    + 'the directory. An import changes the name and the role, matched on the email '
    + 'address, and does not create accounts - a new one needs a password, which does '
    + 'not belong in a spreadsheet. Time zones are not here: they belong to whoever '
    + 'they are about, who sets theirs under My account.',
};

function wireSheetCards() {
  for (const spec of SHEET_CARDS) {
    const view = $(spec.view);
    if (!view) continue;

    view.append(buildSheetCard(spec));
  }
}

// ------------------------------------------------------------ notifications

/**
 * Whether this browser will show a notification, and the button that asks it
 * where it has not been asked yet. A refusal can only be taken back in the
 * browser's own settings, so it is said rather than asked again. The Android
 * app's WebView has no Notification, so inside the app this says it cannot show
 * one - which is true of the app as well, while it shows none of its own.
 */
function drawNotifyState() {
  const button = $('#notify-allow');
  const state = $('#notify-state');
  if (!button || !state) return;

  const permission = 'Notification' in window ? Notification.permission : 'unsupported';

  button.hidden = permission !== 'default';
  state.textContent = {
    granted: () => t('notify.granted', 'This browser shows notifications from here.'),
    denied: () => t('notify.denied',
      'This browser blocks notifications from here; that can only be changed in its own settings.'),
    unsupported: () => t('notify.unsupported', 'This browser cannot show notifications.'),
  }[permission]?.() ?? '';
}

// ------------------------------------------------------------- installing

function wireInstall() {
  // Inside the Android app there is nothing left to offer: it is the app the card
  // is about. The app is known by the bridge it registers for saving files, which
  // its WebView puts on the window before any script of the page runs.
  if ('QubitDeskFiles' in window) {
    const card = $('#install-card');
    if (card) card.hidden = true;
  }

  // The browser's own offer to install the page is left to its menu rather than
  // made over the page: the card offers the Android app, and two offers of an app
  // at once read as two apps.
  window.addEventListener('beforeinstallprompt', (event) => event.preventDefault());
}

/**
 * Shows the code that downloads the Android app on the card, and names the
 * release it comes from - once a session, because neither changes while the page
 * is open, and not at all where the card is hidden, which is inside the app.
 *
 * The server draws the code and names the release, the one this installation
 * runs; without its answer the card keeps its link to the newest release, and
 * nothing else waits on it.
 */
async function loadAndroidCode() {
  const qr = $('#install-qr');
  if (!qr || $('#install-card')?.hidden || qr.getAttribute('src')) return;

  try {
    const answer = await api('/me/android');

    if (answer?.release) $('#install-android-link').href = answer.release;
    if (!answer?.qr) return;

    qr.src = answer.qr;
    qr.alt = t('install.qrAlt', 'QR code that downloads the Android app');
    qr.hidden = false;
  } catch {
    // The card leads to the newest release either way.
  }
}

/**
 * Takes the code off the card when the session ends. Not a secret, but no code
 * on the page outlives the session it was drawn in, so none has to be asked
 * about separately.
 */
function forgetTheAndroidCode() {
  const qr = $('#install-qr');
  if (!qr) return;

  qr.hidden = true;
  qr.removeAttribute('src');
}

// ------------------------------------------------------- metrics and tracing

/**
 * The value that means "administered off", as opposed to the empty value, which
 * means "nothing is administered here and the configuration file decides".
 *
 * Two distinct things that a single empty option would conflate, and the
 * difference is what the server stores: an empty exporter it has been told about
 * overrides a configured one, while an absent field leaves it alone.
 */
const TELEMETRY_OFF = 'off';

/** Fills the metrics and tracing form from the server's answer. */
function fillTelemetryForm(data) {
  const form = $('#form-telemetry');
  const configured = data.configured ?? {};

  // This card has two halves, and only one of them belongs to whoever is at the
  // keyboard - the same split the datasource card above it draws, and for the
  // same reason.
  //
  // The boxes are theirs. This runs after every save on the screen and after a
  // language is chosen, and it used to replace whatever had been typed with the
  // server's copy.
  //
  // What this process is serving is not theirs, and it was being skipped along
  // with them: one touch of the form and the line went on describing the process
  // that was running when the screen was opened - including the metrics address,
  // which the comment over it calls the one thing here somebody wants to copy.
  // An administrator who types in this form and then restarts from the card below
  // came back to a description of the process that had gone.
  if (!beingEdited(form)) fillTelemetryBoxes(form, configured);

  const active = data.active ?? {};

  $('#telemetry-active').textContent = describeActiveTelemetry(active);

  // The log viewer's filters need this to know when they are asking for lines
  // the process never wrote. Both cards are on this screen, so the answer is
  // already here rather than worth a request of its own.
  logView.runningLevel = active.logLevel ?? null;
  warnAboutLevelsTheProcessDoesNotWrite();

  // The placeholders are the third thing, and they belong to both halves: an
  // empty field means "whatever applies", and what applies was something you had
  // to work out from the line above. Saying it in the box that is asking is where
  // somebody is looking when they wonder what leaving it empty will do - and the
  // collector keeps the address the shipped tracing overlay uses, because an
  // empty one has no current value to show.
  //
  // They describe the running process, so they follow it; they are written into
  // the boxes, so they wait for the boxes to be free.
  if (beingEdited(form)) return;

  form.elements.tracerRatio.placeholder = String(active.tracerRatio ?? '');

  if (active.tracerUrl) form.elements.tracerUrl.placeholder = active.tracerUrl;
}

/** The stored settings, which are whoever is at the keyboard's to change. */
function fillTelemetryBoxes(form, configured) {
  form.elements.logLevel.value = configured.logLevel ?? '';
  form.elements.metricsOff.value = configured.metricsOff ? TELEMETRY_OFF : '';

  // Three states in one control: absent, administered off, or an exporter.
  if (configured.traceExporter === undefined || configured.traceExporter === null) {
    form.elements.traceExporter.value = '';
  } else {
    form.elements.traceExporter.value = configured.traceExporter || TELEMETRY_OFF;
  }

  form.elements.tracerUrl.value = configured.tracerUrl ?? '';
  form.elements.tracerRatio.value = configured.tracerRatio ?? '';
}

/**
 * Says what this process is actually serving and exporting.
 *
 * Worth a sentence of its own rather than leaving the form to imply it: until the
 * next restart the stored settings and the running ones disagree, and the
 * metrics URL is the one thing on this screen somebody wants to copy.
 */
function describeActiveTelemetry(active) {
  const metrics = active.metricsServed
    // The host comes from the browser rather than the server, which cannot know
    // the name this installation is reached under. The port is a different one,
    // so it has to be spelled out in full to be usable.
    ? `${window.location.protocol}//${window.location.hostname}:${active.metricsPort}${active.metricsPath}`
    : t('tel.activeMetricsOff', 'not served');

  const traces = active.traceExporter
    ? `${active.traceExporter} → ${active.tracerUrl} (${fmtShare(active.tracerRatio)})`
    : t('tel.activeTracesOff', 'not exported');

  return `${t('tel.activeLog', 'Log level')}: ${active.logLevel} · `
    + `${t('tel.activeMetrics', 'Metrics')}: ${metrics} · `
    + `${t('tel.activeTraces', 'Traces')}: ${traces}`;
}

/**
 * Reads the form, omitting anything left following the configuration file.
 *
 * An omitted field is not the same as an empty one here, which is why this is
 * assembled by hand rather than by formData: sending tracerUrl: "" would store a
 * deliberate blank where the intent was to leave the file's value in place.
 */
function telemetryPayload() {
  const form = $('#form-telemetry');
  const body = {};

  const level = form.elements.logLevel.value;
  if (level !== '') body.logLevel = level;

  if (form.elements.metricsOff.value === TELEMETRY_OFF) body.metricsOff = true;

  const exporter = form.elements.traceExporter.value;
  if (exporter === TELEMETRY_OFF) body.traceExporter = '';
  else if (exporter !== '') body.traceExporter = exporter;

  const url = form.elements.tracerUrl.value.trim();
  if (url !== '') body.tracerUrl = url;

  const ratio = form.elements.tracerRatio.value.trim();
  if (ratio !== '') body.tracerRatio = Number(ratio);

  return body;
}

function wireTelemetry() {
  $('#form-telemetry').addEventListener('submit', (e) => {
    e.preventDefault();
    saveForm(e.target,
      () => api('/settings/telemetry', {
        method: 'PUT', body: JSON.stringify(telemetryPayload()),
      }),
      // Named afterwards rather than here: whether this needs a restart depends
      // on whether it changed anything the running process has not got.
      null,
      // The restart card too, so what was just saved appears in the list of
      // what is waiting rather than only in a toast that fades.
      async () => { await afterTelemetrySaved(); announceSave(); });
  });

  $('#telemetry-reset').addEventListener('click', () => {
    // Through saveForm for the same reason as the limits above.
    saveForm($('#form-telemetry'),
      () => api('/settings/telemetry', { method: 'PUT', body: JSON.stringify({}) }),
      t('tel.resetDone', 'Metrics and tracing follow the configuration file again'),
      afterTelemetrySaved);
  });
}

async function loadTelemetry() {
  fillTelemetryForm(await api('/settings/telemetry'));
  showTracingHint();
}

/**
 * Says where the traces are, on this installation.
 *
 * The sentence used to name http://127.0.0.1:16686, which is right on exactly
 * one machine: the server itself. Read from anywhere else - and this screen is
 * read from somewhere else, or nobody would be reading it in a browser - it
 * names the reader's own machine, where nothing is listening.
 *
 * So it names the host this page was reached on. The port is the trace
 * browser's own and does not follow the application's: they are two services,
 * and 8443 belongs to this one.
 *
 * The second line is there because the first may well not answer. The overlay
 * this repository ships binds the trace browser to the server's loopback on
 * purpose - it asks nobody to sign in, and traces carry request paths and the
 * identifiers in them - so on an unchanged deployment the way in is a tunnel,
 * and the command for it names the same host rather than leaving it as <server>.
 */
function showTracingHint() {
  const hint = $('#tel-tracing-hint');
  if (!hint) return;

  const host = window.location.hostname;

  hint.textContent = '';

  hint.append(t('tel.tracingHint',
    'Running deploy/compose.tracing.yaml beside the application? Then it is '
    + 'exporter OTLP and collector jaeger:4317, and the traces are at {0}.')
    .replace('{0}', `http://${host}:16686`));

  // Its own line: the first sentence is where to look, and this one is what to
  // do when looking there gives nothing. Run together they read as one
  // instruction that contradicts itself.
  hint.append(el('br'));

  hint.append(t('tel.tracingLoopback',
    'If that address does not answer, the shipped file publishes the trace '
    + 'browser on the server\'s loopback only - then the way in is {0}.')
    .replace('{0}', `ssh -L 16686:127.0.0.1:16686 ${host}`));
}

/** Both cards, because saving one of these changes what the other has to say. */
async function afterTelemetrySaved() {
  await loadTelemetry();
  await loadRestart();
}

// ---------------------------------------------------------------- passkeys

/**
 * WebAuthn speaks ArrayBuffers; JSON speaks strings. Everything crossing that
 * boundary is base64url, and getting one of these backwards produces a
 * signature that verifies against nothing, with no useful error - so they live
 * here, once, rather than inline at four call sites.
 */
function b64urlToBytes(value) {
  const padded = value.replace(/-/g, '+').replace(/_/g, '/')
    .padEnd(value.length + (4 - value.length % 4) % 4, '=');
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);

  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);

  return bytes.buffer;
}

function bytesToB64url(buffer) {
  const bytes = new Uint8Array(buffer);
  let binary = '';

  for (const byte of bytes) binary += String.fromCharCode(byte);

  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/** Whether this browser and this connection can do passkeys at all. */
let passkeysAvailable = false;

/**
 * Asks the server whether to offer passkeys.
 *
 * Two conditions have to hold: the browser has to implement WebAuthn, and the
 * connection has to be a secure context. Chrome does not define
 * PublicKeyCredential at all over plain HTTP, so the first check covers the
 * second - but the server knows why, and can be asked before showing a button
 * that could only fail.
 */
async function loadPasskeySupport() {
  if (!window.PublicKeyCredential) {
    passkeysAvailable = false;

    return;
  }

  try {
    passkeysAvailable = Boolean((await api('/auth/passkey'))?.available);
  } catch {
    passkeysAvailable = false;
  }

  $('#login-passkey').hidden = !passkeysAvailable;
}

/**
 * A refused passkey, in the reader's language.
 *
 * navigator.credentials rejects with a DOMException whose message the browser writes
 * itself, in its own wording and usually in English: "The request is not allowed by the
 * user agent or the platform in the current context, possibly because the user denied
 * permission." That went straight into a notice, so a German reader got an English
 * sentence - and a long one that names neither what failed nor what to do next.
 *
 * The name is the part worth reading. It is fixed by the specification, so it can be
 * translated where the message cannot, and it distinguishes the cases that need
 * different answers: a prompt somebody dismissed, a device that already holds a passkey
 * for this account, and a connection the browser refuses to do this over.
 *
 * That last one is wider than plain HTTP, which is what this used to say. Since Firefox
 * 140 - the fix for CVE-2025-6433 - an accepted certificate warning makes the browser
 * refuse as well, reported as SecurityError like the rest, and clicking past the warning
 * is exactly what somebody does before trying. So the sentence names the certificate
 * too: deploy/ terminates TLS with a local CA, so a device that has not been given that
 * CA is the case here that actually happens rather than the theoretical one.
 *
 * The one thing read before the name is that same refusal in Chromium, which answers it
 * with NotAllowedError - the name it also uses for a prompt somebody dismissed, so the
 * name cannot tell the two apart and only the message can. That is not a retreat from
 * the paragraph above: the browser's wording is still never shown, it is read purely as
 * a signal, and this signal is a constant in Chromium's own source rather than a
 * sentence somebody phrased. Matched loosely on the one word, because a rewording should
 * cost this improvement and not turn a dismissed prompt into a certificate that is fine.
 *
 * Anything unrecognised keeps the browser's own sentence. It is in the wrong language,
 * and it is still better than "something went wrong".
 */
function passkeyProblem(err) {
  if (/certificate/i.test(err?.message ?? '')) {
    return t('passkey.err.certificate',
      'This device does not trust the certificate of this site, so no passkey can be '
      + 'made here. Its certificate authority has to be installed on the device.');
  }

  switch (err?.name) {
    case 'NotAllowedError':
      return t('passkey.err.notAllowed',
        'The prompt was dismissed, or it timed out. Nothing was changed.');
    case 'InvalidStateError':
      return t('passkey.err.already',
        'This device already holds a passkey for this account.');
    case 'NotSupportedError':
      return t('passkey.err.unsupported',
        'This device cannot make the kind of passkey this installation asks for.');
    case 'SecurityError':
      return t('passkey.err.insecure',
        'A passkey needs HTTPS, the address it was made for, and a certificate this '
        + 'device trusts. Clicking past a certificate warning is not enough.');
    case 'AbortError':
      return t('passkey.err.aborted', 'The prompt closed before anything was done.');
    default: {
      // A name this does not know: still said in the reader's language, with the
      // browser's own words - in the browser's language - kept as the detail.
      const said = t('passkey.failed', 'The passkey was not accepted.');

      return err?.message ? `${said} (${err.message})` : said;
    }
  }
}

/** Registers a new credential for the signed-in user. */
async function registerPasskey(name) {
  const started = await api('/me/passkeys/register', { method: 'POST' });
  const options = started.options.publicKey;

  // The server sends base64url because JSON cannot hold bytes; the browser
  // wants the bytes back.
  options.challenge = b64urlToBytes(options.challenge);
  options.user.id = b64urlToBytes(options.user.id);

  for (const credential of options.excludeCredentials ?? []) {
    credential.id = b64urlToBytes(credential.id);
  }

  let created;

  try {
    created = await navigator.credentials.create({ publicKey: options });
  } catch (err) {
    throw new Error(passkeyProblem(err));
  }

  if (!created) throw new Error(t('passkey.notCreated', 'The passkey was not created.'));

  try {
    return await api('/me/passkeys/register', {
      method: 'PUT',
      body: JSON.stringify({
        token: started.token,
        name,
        credential: {
          id: created.id,
          rawId: bytesToB64url(created.rawId),
          type: created.type,
          response: {
            clientDataJSON: bytesToB64url(created.response.clientDataJSON),
            attestationObject: bytesToB64url(created.response.attestationObject),
          },
          // What the device is: a phone, a security key, the laptop itself. The
          // server keeps it so the next prompt can ask for the right thing.
          transports: created.response.getTransports?.() ?? [],
        },
      }),
    });
  } catch (err) {
    dropFromTheDevice(options, created, err);

    throw err;
  }
}

/**
 * Tells the device that a passkey it has just made is one this installation
 * does not hold, so that it can drop it.
 *
 * The device makes the passkey before the server has seen it, and a server that
 * then refuses it - the attempt had run out, the check failed - left it on the
 * device, offered at every sign-in and refused at every one. Only a refusal is
 * said: a request that never arrived, or one the server failed on, may have
 * stored the passkey after all, and one the server already holds is not unknown
 * to it. Best effort, and nothing at all on a browser without the Signal API.
 */
function dropFromTheDevice(options, created, err) {
  if (!err?.status || err.status >= 500 || err.refusal?.code === 'passkeyKnown') return;

  if (typeof window.PublicKeyCredential?.signalUnknownCredential !== 'function') return;

  PublicKeyCredential.signalUnknownCredential({
    rpId: options.rp?.id || location.hostname,
    credentialId: created.id,
  }).catch(() => {});
}

/**
 * Signs in with a passkey, without a username.
 *
 * The browser offers whichever credentials it holds for this site, and the one
 * that signs names its own owner - so there is nothing to type. That is the
 * point: a password never typed cannot be phished, reused, or read out of
 * somebody else's breach.
 */
async function signInWithPasskey() {
  const started = await api('/auth/passkey/login', { method: 'POST' });
  const options = started.options.publicKey;

  options.challenge = b64urlToBytes(options.challenge);

  for (const credential of options.allowCredentials ?? []) {
    credential.id = b64urlToBytes(credential.id);
  }

  let assertion;

  try {
    assertion = await navigator.credentials.get({ publicKey: options });
  } catch (err) {
    throw new Error(passkeyProblem(err));
  }

  if (!assertion) throw new Error(t('passkey.cancelled', 'The passkey was not used.'));

  return api('/auth/passkey/login', {
    method: 'PUT',
    body: JSON.stringify({
      token: started.token,
      credential: {
        id: assertion.id,
        rawId: bytesToB64url(assertion.rawId),
        type: assertion.type,
        response: {
          clientDataJSON: bytesToB64url(assertion.response.clientDataJSON),
          authenticatorData: bytesToB64url(assertion.response.authenticatorData),
          signature: bytesToB64url(assertion.response.signature),
          userHandle: assertion.response.userHandle
            ? bytesToB64url(assertion.response.userHandle)
            : null,
        },
      },
    }),
  });
}

/**
 * Why this account is offered no passkey here, as the sentence the card says in
 * place of the offer - or nothing, where it is offered one.
 *
 * The built-in administrator keeps its password on purpose, so it is never
 * offered a passkey it might then come to rely on.
 *
 * isSystem here rather than whether the account administers, which is the one
 * place the two deliberately differ. Everything else reserved for the built-in account was
 * reserved for what it *does*, and an account holding the admin role does the
 * same things - but this is reserved for what it *is*: the guaranteed way back
 * in, which a way back in tied to one particular phone would stop being.
 * Somebody who administers because they were given the role is not that account
 * and does not carry that guarantee, so withholding a phishing-resistant
 * sign-in from them would cost security rather than protect it.
 *
 * Nor to an account the directory holds, and for the opposite reason: its
 * sign-in has to go through the directory, which is the one place that can end
 * it, and the server refuses to register a passkey that would go round it.
 *
 * Nor where nothing could make one: the Android app shows this page in a
 * WebView, which has no passkeys, and signs in with its token instead; and a
 * browser may have none, or be reaching the installation over plain HTTP, where
 * browsers refuse them.
 */
function passkeysWithheldBecause() {
  if (me.user.isSystem) {
    return t('passkey.elsewhereSystem', 'Not offered to the built-in administrator, which keeps its '
      + 'password: the way back in that works from any device.');
  }

  if (me.user.isExternal) {
    return t('passkey.elsewhereDirectory', 'Not offered to an account from the directory, which signs '
      + 'in through the directory: the one place that can end its sign-in.');
  }

  if ('QubitDeskFiles' in window) {
    return t('passkey.elsewhereApp', 'The app signs in with the token it was connected with, and '
      + 'needs no passkey. Passkeys are set up and used in a browser.');
  }

  if (!passkeysAvailable) {
    return t('passkey.elsewhereBrowser', 'This browser cannot use passkeys here: they need a browser '
      + 'that supports them, over HTTPS.');
  }

  return '';
}

async function loadPasskeys() {
  const card = $('#passkey-card');

  card.hidden = !me.user;
  if (card.hidden) return;

  const withheld = passkeysWithheldBecause();

  $('#passkey-offer').hidden = Boolean(withheld);
  $('#passkey-elsewhere').hidden = !withheld;
  $('#passkey-elsewhere').textContent = withheld;

  if (withheld) return;

  const passkeys = (await api('/me/passkeys'))?.items ?? [];

  const rows = passkeys.map((passkey) => el('tr', {},
    el('td', { class: 'text', text: passkey.name }),
    el('td', { text: fmtMoment(passkey.createdAt) }),
    el('td', {
      text: passkey.lastUsedAt ? fmtMoment(passkey.lastUsedAt) : t('passkey.never', 'never'),
    }),
    el('td', { class: 'actions' }, deleteButton({
      label: `${t('field.name', 'Name')} "${passkey.name}"`,
      path: `/me/passkeys/${passkey.id}`,
      message: t('passkey.removed', 'Passkey removed'),
      after: loadPasskeys,
    })),
  ));

  fillTable($('#table-passkeys tbody'), rows, 4,
    t('passkey.empty', 'No passkeys yet. Add one to sign in without a password.'));
}

function wirePasskeys() {
  $('#form-passkey').addEventListener('submit', (e) => {
    e.preventDefault();

    const field = e.target.elements.name;

    mutate(() => registerPasskey(field.value.trim()),
      t('passkey.added.done', 'Passkey added'),
      async () => { field.value = ''; await loadPasskeys(); });
  });

  $('#login-passkey').addEventListener('click', async () => {
    try {
      await signInWithPasskey();
    } catch (err) {
      // A cancelled prompt and a rejected credential are indistinguishable
      // from here, so the message covers both without guessing.
      showLogin(err.message || t('passkey.failed', 'The passkey was not accepted.'));

      return;
    }

    await arriveAfterSignIn();
  });
}

// --------------------------------------------------------------- live log

/**
 * State of the log viewer.
 *
 * `since` is the sequence number the server last reported, not the last line
 * rendered. The filtering happens on the server, so a filter that matches
 * nothing still advances the cursor - asking from the last *rendered* line
 * would re-scan the whole buffer on every poll.
 */
const logView = {
  since: 0,

  // Which process counted `since`, as its last answer named it, and sent back
  // with it. The numbers start again with the process, so without this a viewer
  // left open across a restart asked the new process for "everything after" a
  // number only the old one had reached - and was shown none of what the new one
  // wrote while it started.
  epoch: '',
  timer: null,
  polling: false,
  paused: false,

  // Set when a poll was turned away, and read by the reschedule. See
  // stopLogPolling for why clearing the timer is not enough on its own.
  refused: false,
  levels: null,
  // A default that is useful rather than complete: DEBUG carries every SQL
  // statement the process runs, which buries everything else.
  quiet: new Set(['DEBUG']),

  // What the process is actually writing, read off the telemetry card. Here so
  // the filters can say when one of them is asking for lines that were never
  // captured - see warnAboutLevelsTheProcessDoesNotWrite.
  runningLevel: null,
};

const LOG_DEFAULT_DELAY = 3;
const LOG_MAX_LINES = 2000;

function wireLogViewer() {
  const search = $('#log-search');
  const delay = $('#log-delay');
  const pause = $('#log-pause');

  if (!search || !delay || !pause) return;

  // A filter change means the server has to re-scan from the start of its
  // buffer, so the cursor and the view both reset.
  const restart = () => {
    logView.since = 0;
    $('#log-output').replaceChildren();
    schedulePoll({ immediate: true });
  };

  search.addEventListener('input', debounce(restart, 300));
  $('#log-levels').addEventListener('change', () => {
    // Before the poll, so somebody who has just ticked DEBUG on a process
    // running at INFO is told why nothing arrives rather than watching for it.
    warnAboutLevelsTheProcessDoesNotWrite();
    restart();
  });

  delay.addEventListener('change', () => schedulePoll({ immediate: true }));

  pause.addEventListener('click', () => {
    logView.paused = !logView.paused;
    renderPauseButton();
    schedulePoll({ immediate: !logView.paused });
  });

  // Hidden is nobody looking - see logViewerActive.
  document.addEventListener('visibilitychange', () => {
    if (logViewerActive()) schedulePoll({ immediate: true });
    else stopLogPolling();
  });

  $('#log-clear').addEventListener('click', () => {
    // The view only. The server's buffer is not the viewer's to discard, and an
    // administrator clearing their screen must not destroy evidence for the
    // next person to look.
    $('#log-output').replaceChildren();
    $('#log-warning').hidden = true;
  });

  renderPauseButton();
}

/** Waits until the user stops typing, so each keystroke is not a request. */
function debounce(fn, ms) {
  let handle = null;

  return (...args) => {
    clearTimeout(handle);
    handle = setTimeout(() => fn(...args), ms);
  };
}

function renderPauseButton() {
  if (logView.paused) swapTheLabel($('#log-pause'), 'log.resume', 'Resume');
  else swapTheLabel($('#log-pause'), 'log.pause', 'Pause');
}

/** The levels currently ticked. */
function selectedLogLevels() {
  return $$('#log-levels input:checked').map((input) => input.value);
}

/** The refresh interval in seconds; 0 means do not poll. */
function logDelaySeconds() {
  const value = Number.parseInt($('#log-delay').value, 10);
  if (!Number.isFinite(value) || value < 0) return LOG_DEFAULT_DELAY;

  return Math.min(value, 300);
}

/**
 * Restarts the polling timer for the current settings.
 *
 * Called on every change rather than adjusting a running timer, because
 * "refresh every n seconds" has to mean the new n immediately - not after the
 * old interval has elapsed one more time.
 */
function schedulePoll({ immediate = false } = {}) {
  clearTimeout(logView.timer);
  logView.timer = null;

  // Asked for deliberately - the screen was opened, the delay changed, the pause
  // lifted - so whatever turned it away last is over and it may try again. A
  // refusal that is still true simply happens again, once, instead of every few
  // seconds.
  logView.refused = false;

  if (immediate) void pollLog();

  if (logView.paused) {
    setLogStatus(t('log.paused', 'Paused.'));
    return;
  }

  const seconds = logDelaySeconds();
  if (seconds === 0) {
    setLogStatus(t('log.manual', 'Refreshing is off. Set a number of seconds to follow along.'));
    return;
  }

  logView.timer = setTimeout(() => {
    // The page's own: following along is not somebody being there.
    void pollLog({ background: true }).finally(() => {
      // Not if that poll turned the viewer off itself. stopLogPolling clears
      // logView.timer, and this callback *is* that timer - there is nothing left
      // for it to clear, so without asking here the stop is undone by the line
      // below it, one interval later, for as long as the tab stays open.
      //
      // A 401 hid this: the sign-in screen goes up and me is emptied, so
      // logViewerActive answers false by itself. A 403 has no such side effect -
      // the session is fine and only the permission is gone, and me.permissions
      // is not refreshed while a session lasts.
      if (logView.refused || !logViewerActive()) return;

      // Chained rather than an interval: a slow or hanging request must not
      // pile up behind itself.
      schedulePoll();
    });
  }, seconds * 1000);
}

/**
 * Stops polling. Called when the screen is left or the session ends.
 *
 * `refused` says the poll stopped itself because the server turned it away, and
 * is the difference between stopping and appearing to. Clearing the timer is
 * enough when something outside the loop stops it - leaving the screen, the
 * session ending - because then no timer is running. It is not enough when the
 * poll that is running is the timer: there is nothing to clear, and the
 * reschedule after it would start the whole thing again.
 */
function stopLogPolling({ refused = false } = {}) {
  clearTimeout(logView.timer);
  logView.timer = null;

  if (refused) logView.refused = true;
}

/**
 * Whether the log should be polled at all.
 *
 * Only while its own screen is on top: an administrator who moved on to book
 * time has no use for a request every three seconds, and the endpoint is not
 * free - it reads a mutex-guarded buffer.
 *
 * And only while its tab is the one being looked at, as for the other two things
 * that ask in the background. At INFO every poll is a line in the buffer it
 * shows, so a log screen left in a hidden tab overnight wrote one every few
 * seconds into five thousand, and by morning had pushed out the lines somebody
 * would have come to read. Looking again asks for what was missed in one go.
 */
function logViewerActive() {
  const card = $('#log-card');
  if (!card) return false;

  // The sign-in screen being up means there is no session to poll with. Without
  // this the poller keeps asking through a password change - which ends every
  // session - and paints the screen with authentication failures. And only on
  // its own tab: on one that is not chosen it is as unseen as on a screen that
  // is not.
  return can('settings:manage') && !$('#view-admin').hidden && !onAHiddenTab(card)
    && $('#login-screen').hidden && !document.hidden;
}

function setLogStatus(text) {
  const status = $('#log-status');
  if (status) status.textContent = text;
}

/**
 * What a page of the log has to admit about itself: where the output stops
 * being one unbroken run of lines. Empty when it is one.
 *
 * The three are not alternatives, which is why this is not a chain of else. A
 * viewer that was paused, or sat in a tab the browser put to sleep, comes back
 * to a process started while it was away and follows on into a log that has
 * been written for hours: it has begun again *and* its first lines have left
 * the buffer *and* more are left than a page holds. Said one at a time, a
 * restart was announced over a page that began three hundred lines in.
 */
function logGaps(page) {
  const said = [];

  if (page.restarted) {
    // The lines above were another process's. The output is one column of
    // lines and would otherwise run the old log into the new one as though
    // nothing had happened between them.
    said.push(t('log.restarted',
      'The application has started again. What follows is the log of the new process.'));
  }

  if (page.dropped > 0) {
    said.push(t('log.dropped',
      'Older lines have been discarded from the buffer and cannot be recovered.'));
  }

  if (page.skipped > 0) {
    // More arrived than one page holds - after a pause, or on a busy
    // installation - and the newest were shown.
    said.push(t('log.skipped',
      'More lines arrived than one page holds, so {0} were passed over to show the newest.')
      .replace('{0}', String(page.skipped)));
  }

  return said.join(' ');
}

async function pollLog({ background = false } = {}) {
  if (!logViewerActive() || logView.polling) return;

  logView.polling = true;

  try {
    const query = new URLSearchParams({ since: String(logView.since), limit: '500' });
    if (logView.epoch) query.set('epoch', logView.epoch);

    const levels = selectedLogLevels();
    // Every level ticked is the same request as none, and sending none keeps
    // the server from filtering at all.
    if (levels.length > 0 && levels.length < (logView.levels?.length ?? 0)) {
      query.set('levels', levels.join(','));
    }

    const search = $('#log-search').value.trim();
    if (search) query.set('search', search);

    const page = await api(`/admin/logs?${query}`, { background });

    if (logView.levels === null) buildLogLevelFilters(page.levels ?? []);

    if (!page.available) {
      setLogStatus(t('log.unavailable',
        'Log capture is not installed in this process, so there is nothing to show.'));
      return;
    }

    logView.since = page.lastSeq ?? logView.since;
    logView.epoch = page.epoch ?? logView.epoch;

    appendLogLines(page.records ?? []);

    const gaps = logGaps(page);
    if (gaps) {
      const warning = $('#log-warning');

      warning.textContent = gaps;
      warning.hidden = false;
    }

    setLogStatus(logView.paused
      ? t('log.paused', 'Paused.')
      : `${t('log.upTo', 'Up to line')} ${logView.since}`);
  } catch (err) {
    if (err.status === 401 || err.status === 403) {
      // The session ended - a password change does that, and so does an
      // expiry. Retrying would produce one failure per interval for as long as
      // the tab stays open. Signing in again restarts it, because switchView
      // does.
      stopLogPolling({ refused: true });
      setLogStatus(t('log.signedOut', 'Not signed in any more; the log stopped updating.'));

      return;
    }

    // Any other failure leaves it running: a viewer that gives up on one bad
    // request is useless during exactly the incident it exists for.
    setLogStatus(`${t('log.failed', 'Could not read the log')}: ${err.message}`);
  } finally {
    logView.polling = false;
  }
}

/** Builds the level chips from what the server says it can emit. */
function buildLogLevelFilters(levels) {
  logView.levels = levels;

  const holder = $('#log-levels');
  holder.replaceChildren();

  for (const level of levels) {
    const label = document.createElement('label');
    const input = document.createElement('input');

    input.type = 'checkbox';
    input.value = level;
    input.checked = !logView.quiet.has(level);

    label.append(input, document.createTextNode(level));
    holder.append(label);
  }

  warnAboutLevelsTheProcessDoesNotWrite();
}

/** The levels, quietest first, so two of them can be compared. */
const LOG_LEVELS_BY_DETAIL = ['DEBUG', 'INFO', 'NOTICE', 'WARN', 'ERROR', 'FATAL'];

/**
 * Says so when a ticked level is one the running process never writes.
 *
 * These boxes filter what was captured; they do not ask for more. The sink reads
 * the process's own output, so a level the logger is not writing was never
 * captured and can never appear - which makes ticking DEBUG on a process running
 * at INFO look like a filter that is broken rather than one that is working
 * exactly as intended and has nothing to show.
 *
 * The level itself is set on the logging card, and that card is the one place
 * that says when a saved level applies - this one went on saying "at the next
 * start" after that had stopped being true, beside a card saying "at once".
 * So this is a sentence rather than a button, and it stops at naming the card:
 * being told where the level is beats waiting for lines that are not coming.
 */
function warnAboutLevelsTheProcessDoesNotWrite() {
  const warning = $('#log-level-warning');
  if (!warning) return;

  const running = LOG_LEVELS_BY_DETAIL.indexOf(logView.runningLevel ?? '');
  if (running < 0) {
    warning.hidden = true;

    return;
  }

  const quieter = selectedLogLevels()
    .filter((level) => LOG_LEVELS_BY_DETAIL.indexOf(level) >= 0
      && LOG_LEVELS_BY_DETAIL.indexOf(level) < running);

  warning.textContent = quieter.length
    ? t('log.levelTooQuiet',
      'This installation is writing {0} and above, so {1} will stay empty. '
      + 'The log level is set under "Logging, metrics and tracing" on the Operation tab.')
      .replace('{0}', logView.runningLevel)
      .replace('{1}', quieter.join(', '))
    : '';

  warning.hidden = !warning.textContent;
}

function appendLogLines(records) {
  if (records.length === 0) return;

  const output = $('#log-output');

  // Whether to scroll afterwards has to be decided before anything is added,
  // because appending changes scrollHeight.
  const follow = $('#log-follow').checked && atLogBottom(output);

  const batch = document.createDocumentFragment();

  for (const record of records) {
    const line = document.createElement('div');
    line.className = 'log-line';
    line.dataset.level = record.level;

    const time = document.createElement('span');
    time.className = 'log-time';
    time.textContent = formatLogTime(record.time);

    const level = document.createElement('span');
    level.className = 'log-level';
    level.textContent = record.level;

    const message = document.createElement('span');
    message.className = 'log-message';
    // textContent, not innerHTML: these are log lines, and a log line is
    // attacker-influenced text - a failed sign-in carries whatever address was
    // typed into it.
    message.textContent = record.message;

    if (record.traceId) line.title = `trace ${record.traceId}`;

    line.append(time, level, message);
    batch.append(line);
  }

  output.append(batch);

  // The view is not the buffer: keeping every line since the screen opened
  // would grow without limit in a long incident.
  while (output.childElementCount > LOG_MAX_LINES) output.firstElementChild.remove();

  if (follow) output.scrollTop = output.scrollHeight;
}

/**
 * Whether the view is scrolled to the end.
 *
 * Scrolling up is a deliberate act - reading something. Following would yank
 * the reader back down on the next poll, so it only applies while they are
 * already at the bottom.
 */
function atLogBottom(output) {
  return output.scrollHeight - output.scrollTop - output.clientHeight < 40;
}

/**
 * When a log line was written: the time to the second, and the day as well
 * whenever it was not today.
 *
 * In the account's own zone, like every other moment on this screen - see
 * fmtMoment. It was the browser's, so on a device set to another zone the log
 * and the token list beside it gave the same minute two different times.
 *
 * The day because the buffer outlives it. It holds the last five thousand
 * lines, and on a quiet installation - or one logging at WARN, as the manual
 * asks of one with a schedule - those reach back days, so "03:00:01 ERROR"
 * could not be told from this morning's.
 */
function formatLogTime(iso) {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return '';

  const timeZone = me.user?.effectiveTimezone || undefined;
  const dayOf = (moment) => new Intl.DateTimeFormat('en-CA', { timeZone }).format(moment);

  return new Intl.DateTimeFormat(activeLocale(), {
    ...(dayOf(at) === dayOf(new Date()) ? {} : { day: '2-digit', month: '2-digit', year: 'numeric' }),
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
    timeZone,
  }).format(at);
}

// -------------------------------------------------------- maintenance mode

/**
 * Renders the notice, and reports whether the installation is out of service.
 *
 * Read on its own rather than as part of the branding, because it has to work on
 * the sign-in screen - where there is no session and most of the API is refused.
 */
async function loadMaintenance() {
  const banner = $('#maintenance-banner');
  if (!banner) return false;

  let state = { enabled: false, message: '' };

  try {
    state = await api('/maintenance');
  } catch {
    // Not fatal. An installation whose settings cannot be read has a bigger
    // problem than a missing banner, and the request that needs them will say so.
    banner.hidden = true;

    return false;
  }

  const notice = state.enabled
    ? (state.message || t('err.maintenance',
      'This installation is temporarily unavailable for maintenance.'))
    : '';

  banner.textContent = notice;
  banner.hidden = !state.enabled;

  // And again inside the sign-in screen, which covers the banner above: it is
  // fixed over the whole viewport with an opaque background, so somebody who is
  // not signed in would never see the one at the top of the page.
  const onLogin = $('#login-maintenance');
  if (onLogin) {
    onLogin.textContent = notice;
    onLogin.hidden = !state.enabled;
  }

  // And who may still come in. The notice above is whatever the administrator
  // typed - "back at 14:00" - which tells somebody nothing about why their own
  // password was refused, so they try it again. This line is the application's
  // own and says the refusal is not about them.
  const who = $('#login-maintenance-who');
  if (who) who.hidden = !state.enabled;

  // The form, for the administrator who is looking at it - unless they are part
  // way through typing the notice, which this runs often enough to interrupt:
  // every load of the administration screen passes through here.
  //
  // The banners above are filled either way. They say what the installation is
  // doing, which is true whatever anybody is typing.
  const form = $('#form-maintenance');
  if (form && !beingEdited(form)) {
    form.elements.enabled.checked = Boolean(state.enabled);
    form.elements.message.value = state.message ?? '';

    // And as the defaults, which is how the form remembers what the server said
    // rather than what the markup shipped. Two things read it: the question below
    // asks only when this is being switched on, and a reset puts the card back to
    // the installation's own state instead of to "in service", which is a lie on
    // an installation that is out of it.
    form.elements.enabled.defaultChecked = Boolean(state.enabled);
    form.elements.message.defaultValue = state.message ?? '';
  }

  return Boolean(state.enabled);
}

function wireMaintenance() {
  const form = $('#form-maintenance');
  if (!form) return;

  // async, because the question is now a dialog that resolves rather than a
  // browser box that blocks the page.
  form.addEventListener('submit', async (event) => {
    event.preventDefault();

    const enabled = form.elements.enabled.checked;

    // What the server last said, kept by the form itself. Asked of that rather
    // than of the box, because "is out of service" and "is being put out of
    // service" are different questions and only the second is worth a dialog.
    const wasEnabled = form.elements.enabled.defaultChecked;

    // Asked about only when switching it on. Turning it off needs no
    // confirmation: that is the direction that ends an outage, and a dialog in
    // front of it is a dialog between somebody and fixing their installation.
    //
    // Nor when it is already on. An administrator changing the notice they are
    // showing - "back at 14:00" - was asked whether to do the thing that had
    // already been done, and cancelling then put the box somewhere it had never
    // been: the card read "in service" over an installation that was out of it.
    if (enabled && !wasEnabled) {
      const proceed = await confirmDialog({
        title: t('maint.title', 'Maintenance mode'),
        text: t('maint.confirm',
          'Turn this installation out of service? Everyone who may not administer it will be turned away.'),
        confirmLabel: t('maint.enabled', 'Out of service'),
      });

      if (!proceed) {
        // Put back to what the server said, because the checkbox has already
        // been ticked by the click that opened this question.
        form.elements.enabled.checked = wasEnabled;

        return;
      }
    }

    saveForm(form,
      () => api('/settings/maintenance', {
        method: 'PUT',
        body: JSON.stringify({ enabled, message: form.elements.message.value }),
      }),
      enabled
        ? t('maint.onSaved', 'The installation is now out of service.')
        : t('maint.offSaved', 'The installation is back in service.'),
      loadMaintenance);
  });
}

// ---------------------------------------------------------------- timezone

/**
 * A short list to fall back on where the browser cannot enumerate zones.
 *
 * Deliberately not a curated "important zones" list, which would be a
 * permanent maintenance burden and would still be wrong for someone: it exists
 * only so the picker is never empty, and the server accepts any valid name.
 */
const FALLBACK_TIMEZONES = [
  'UTC', 'Europe/London', 'Europe/Berlin', 'Europe/Zurich', 'Europe/Vienna',
  'Europe/Moscow', 'America/New_York', 'America/Chicago', 'America/Denver',
  'America/Los_Angeles', 'America/Sao_Paulo', 'Asia/Dubai', 'Asia/Kolkata',
  'Asia/Shanghai', 'Asia/Tokyo', 'Australia/Sydney', 'Pacific/Auckland',
];

/** Every zone this browser knows, so the picker matches the tz database. */
function availableTimezones() {
  try {
    const supported = Intl.supportedValuesOf?.('timeZone');
    if (supported?.length) return supported;
  } catch {
    // Older browsers have no supportedValuesOf; the fallback covers them.
  }

  return FALLBACK_TIMEZONES;
}

/** The current time in a zone, so a choice can be checked at a glance. */
function timeIn(timeZone) {
  try {
    return new Intl.DateTimeFormat(activeLocale(), {
      timeZone, dateStyle: 'medium', timeStyle: 'short',
    }).format(new Date());
  } catch {
    return '';
  }
}

/**
 * Fills a zone picker.
 *
 * inheritLabel, when given, adds an empty option meaning "follow the instance
 * setting". That option is the point of the personal picker: without it every
 * account would be pinned to a zone the moment someone opened the screen, and
 * changing the instance zone would then move nobody.
 */
function fillTimezoneSelect(select, selected, inheritLabel) {
  const zones = availableTimezones();

  select.replaceChildren();

  if (inheritLabel !== undefined) {
    select.append(el('option', { value: '', text: inheritLabel }));
  }

  for (const zone of zones) {
    select.append(el('option', { value: zone, text: zone }));
  }

  // A stored zone this browser does not list would otherwise be silently
  // dropped, and saving the form would change it without anyone asking.
  if (selected && !zones.includes(selected)) {
    select.append(el('option', { value: selected, text: selected }));
  }

  select.value = selected ?? '';
}

/** Shows what time it currently is in the picked zone. */
function showTimeIn(select, output, fallbackZone) {
  const zone = select.value || fallbackZone;
  const now = timeIn(zone);

  output.textContent = now ? `${zone} — ${now}` : '';
}

/** Loads the personal zone into "My account". */
function fillMyTimezone() {
  // Not over somebody who is part way through filling it in. This runs after
  // every save on the screen and after a language is chosen, and it used to
  // replace whatever had been typed with the server's copy.
  if (beingEdited($('#form-my-timezone'))) return;

  const select = $('#my-timezone');
  const inherit = `${t('tz.inherit', 'Follow the instance setting')}`;

  // Naming the effective zone in this option made it read "follow the instance
  // setting (Africa/Abidjan)" right after Africa/Abidjan had been chosen by
  // hand - an offer to change nothing, and no way to find out what stopping
  // would actually give you. See instanceZone.
  const instance = instanceZone();

  fillTimezoneSelect(select, me.user?.timezone ?? '', `${inherit} (${instance})`);

  // The clock beside the picker follows the picker: with nothing chosen it
  // shows the instance's zone, which is what would apply.
  showTimeIn(select, $('#my-timezone-now'), instance);
}

/**
 * The zone "follow the instance setting" would actually give you.
 *
 * Not the zone in effect for this account: they are the same until somebody
 * picks one of their own, and then they are not. Written here rather than at
 * each of the two places that need it, because it is one rule about what an
 * option means and a rule in two places is a rule that can be changed in one.
 */
function instanceZone() {
  return me.user?.instanceTimezone ?? me.user?.effectiveTimezone ?? 'UTC';
}

/**
 * Wires one zone picker: the clock beside it follows it, and the form saves it.
 *
 * Both pickers on this screen do exactly this and differ in three things - the
 * controls, where the choice is saved, and which zone the clock falls back to
 * when nothing is chosen. Those are the arguments.
 *
 * `fallback` is a function rather than a value because the personal picker's
 * answer depends on the account, which arrives after this runs.
 */
function wireTimezonePicker(select, clock, form, endpoint, fallback) {
  // Picking the "follow the instance setting" line has to show the time it
  // would actually be, not the time in the zone being left.
  select.addEventListener('change', () => showTimeIn(select, clock, fallback()));

  form.addEventListener('submit', (e) => {
    e.preventDefault();
    saveForm(e.target,
      () => api(endpoint, {
        method: 'PUT', body: JSON.stringify({ timezone: select.value }),
      }),
      t('tz.saved', 'Timezone saved'),
      refreshAll);
  });
}

function wireTimezones() {
  wireTimezonePicker($('#my-timezone'), $('#my-timezone-now'), $('#form-my-timezone'),
    '/me/timezone', instanceZone);

  // UTC, because there is nothing above the instance to inherit from.
  wireTimezonePicker($('#instance-timezone'), $('#instance-timezone-now'), $('#form-timezone'),
    '/settings/timezone', () => 'UTC');
}

// ------------------------------------------------------------------- theme

/**
 * How often "automatic" re-checks the clock.
 *
 * A dashboard left open across the evening should follow the day rather than
 * stay bright until someone reloads it. A minute is far more often than it can
 * matter and costs nothing.
 */
const THEME_RECHECK_MS = 60_000;

/**
 * Applies an appearance and remembers it for the next first paint.
 *
 * The remembering is a cache, not the setting. The setting lives on the account
 * - see setThemeForAccount - and the copy in this browser exists for one reason:
 * theme.js runs before anything has been asked of the server, and without it the
 * page paints in the wrong colours and corrects itself a moment later. It is
 * cleared on the way out, so the next person starts from the clock.
 */
function setThemePreference(preference) {
  const theme = window.qubitDeskTheme;

  document.documentElement.dataset.themePreference = preference;
  document.documentElement.dataset.theme = theme.resolve(preference);

  try {
    if (preference === 'auto') localStorage.removeItem(theme.STORAGE_KEY);
    else localStorage.setItem(theme.STORAGE_KEY, preference);
  } catch {
    // Storage can be refused; the choice still holds for this page.
  }
}

/**
 * Chooses an appearance: on the account where there is one, on the page where
 * there is not.
 *
 * Signed out, the sign-in screen still has to be readable by whoever is looking
 * at it, so the choice holds for the page and is not written anywhere. Signed
 * in, it goes to the account - which is the whole point of moving it off the
 * device, where a shared machine handed the next person the last one's dark
 * mode.
 *
 * "auto" is sent as an empty value, because on the account the absence of a
 * choice is what following the clock means.
 */
function setThemeForAccount(preference) {
  setThemePreference(preference);

  if (!me.user) return;

  // Quietly: this is a preference, and a notice saying it was saved is a notice
  // about something the person can already see happened.
  api('/me/theme', {
    method: 'PUT',
    body: JSON.stringify({ theme: preference === 'auto' ? '' : preference }),
  }).then(() => {
    me.user.theme = preference === 'auto' ? '' : preference;
  }).catch(() => {
    // It holds for this page either way, which is what somebody just asked for.
  });
}

/** Puts the signed-in account's appearance on the screen. */
function applyAccountTheme() {
  setThemePreference(me.user?.theme || 'auto');

  const picker = $('#theme-picker');
  if (picker) picker.value = me.user?.theme || 'auto';
}

/** Wires the appearance picker and keeps "automatic" honest as the day passes. */
function wireTheme() {
  const picker = $('#theme-picker');
  const theme = window.qubitDeskTheme;

  picker.value = theme.stored();
  picker.addEventListener('change', (e) => setThemeForAccount(e.target.value));

  setInterval(() => {
    if (document.documentElement.dataset.themePreference !== 'auto') return;

    const now = theme.resolve('auto');
    if (now !== document.documentElement.dataset.theme) {
      document.documentElement.dataset.theme = now;
    }
  }, THEME_RECHECK_MS);
}

/**
 * The navigation, folded away on a screen too narrow to hold it.
 *
 * Nine points do not fit beside anything on a telephone. They were a row that
 * scrolled sideways, which shows three and hides six with nothing to say the
 * others exist - so somebody looking for Roles had to discover it by dragging.
 * Folded into a list behind one control, the count is visible the moment it
 * opens.
 *
 * Closed by everything that means "I am done with this": choosing a point,
 * pressing Escape, and a press anywhere else. A menu that only closes by its own
 * button is a menu that ends up covering the screen somebody wanted to read.
 */
function wireNavigationMenu() {
  const bar = $('.topbar');
  const toggle = $('#nav-toggle');
  const tabs = $('#tabs');
  if (!bar || !toggle || !tabs) return;

  toggle.addEventListener('click', (e) => {
    e.stopPropagation();
    showNavigationMenu(!navigationMenuIsOpen());
  });

  // Choosing one is the point of opening it.
  tabs.addEventListener('click', () => showNavigationMenu(false));

  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || !navigationMenuIsOpen()) return;

    showNavigationMenu(false);

    // Back to the control that opened it, so a keyboard is not left with the
    // focus on something that is no longer there.
    toggle.focus();
  });

  document.addEventListener('click', (e) => {
    if (!navigationMenuIsOpen() || bar.contains(e.target)) return;

    // Not while the walk through is running. It seals the page and opens this
    // fold itself, to point at something inside it - so the only press that can
    // reach here is on the tour's own bubble, and closing the fold under the
    // bubble describing it is not what pressing Next means.
    if (tour.active) return;

    showNavigationMenu(false);
  });

  // A window widened past the breakpoint has the points on screen again, and a
  // menu left open would be a second copy of them.
  window.addEventListener('resize', () => {
    if (window.innerWidth > 900) showNavigationMenu(false);
  });
}

/**
 * Opens or closes the fold, and says which it is.
 *
 * Out here rather than a closure inside the wiring because the walk through
 * opens the same fold before pointing into it. Two places setting the class
 * would be two places to remember that the control's aria-expanded says the
 * same thing - and a burger that claims to be closed over an open menu is the
 * half a screen reader is given.
 */
function showNavigationMenu(open) {
  const bar = $('.topbar');
  const toggle = $('#nav-toggle');
  if (!bar || !toggle) return;

  bar.classList.toggle('nav-open', open);
  toggle.setAttribute('aria-expanded', String(open));
}

/** navigationMenuIsOpen answers whether the fold is open. */
function navigationMenuIsOpen() {
  return $('.topbar')?.classList.contains('nav-open') === true;
}

// ------------------------------------------------------------- screen tabs

/**
 * Sorts the two long screens, My account and Settings, into their tabs.
 *
 * Each had grown into one long column of cards - Settings to well over six
 * thousand pixels - which is a page somebody scrolls through rather than reads.
 * A card says which tab it is on with data-tab and keeps its place in the
 * markup; this moves it into its tab's panel once, in the order the markup has
 * them, so the order of the markup is still the order on screen within a tab.
 *
 * A tab is offered while something on it is shown. Whether a card is shown, and
 * to whom, is decided by the code that loads it and by the account's rights,
 * and neither has any business knowing which tab the card is on - so the cards'
 * own hidden is watched instead of every place that sets it being taught.
 */
function arrangeScreenTabs() {
  for (const strip of $$('.screen-tabs')) {
    const view = strip.closest('.view');
    if (!view) continue;

    const cards = $$(':scope > [data-tab]', view);

    for (const card of cards) {
      $(`[role="tabpanel"][data-card-panel="${card.dataset.tab}"]`, view)?.append(card);
    }

    for (const tab of $$('[role="tab"]', strip)) {
      tab.addEventListener('click', () => chooseScreenTab(view, tab.dataset.cardTab));
    }

    strip.addEventListener('keydown', (event) => walkTheScreenTabs(strip, event));

    const watcher = new MutationObserver(() => offerTheTabsThatHoldSomething(view));
    for (const card of cards) watcher.observe(card, { attributes: true, attributeFilter: ['hidden'] });

    showScreenTab(view, rememberedScreenTab(view) ?? $('[role="tab"]', strip)?.dataset.cardTab);
    offerTheTabsThatHoldSomething(view);
  }
}

/**
 * Shows one tab of a screen, and hides the panels of the others.
 *
 * Kept for the session, per screen, so a reload comes back to the same tab
 * rather than to the first one, which is seldom where somebody was working.
 */
function showScreenTab(view, key, { focus = false } = {}) {
  if (!key) return;

  for (const tab of $$('[role="tab"]', view)) {
    const chosen = tab.dataset.cardTab === key;

    tab.setAttribute('aria-selected', String(chosen));
    tab.tabIndex = chosen ? 0 : -1;

    if (chosen && focus) tab.focus();
  }

  for (const panel of $$('[role="tabpanel"]', view)) {
    const shown = panel.dataset.cardPanel === key;
    if (panel.hidden === shown) panel.hidden = !shown;
  }

  try {
    sessionStorage.setItem(`qubitdesk.tab.${view.id}`, key);
  } catch {
    // Private browsing refuses storage; the screen then opens on its first tab.
  }
}

/**
 * Changes to another tab of a screen.
 *
 * What the tabs left behind showed is let go as it is when the screen itself is
 * left: on a tab that is not chosen it is just as unseen. A password read in the
 * clear goes back behind its dots; a token's value and a second factor's key -
 * each a credential shown once - come off their cards; and the log stops being
 * read.
 */
function chooseScreenTab(view, key, options) {
  showScreenTab(view, key, options);

  hideEveryRevealedPassword();

  if (onAHiddenTab($('#token-card'))) forgetTheTokenValue();
  if (onAHiddenTab($('#totp-card')) && $('#totp-setup') && !$('#totp-setup').hidden) renderTOTPState();

  if (logViewerActive()) schedulePoll({ immediate: true });
  else stopLogPolling();
}

/** The tab a screen last showed in this session, if it still has it. */
function rememberedScreenTab(view) {
  let key = null;

  try {
    key = sessionStorage.getItem(`qubitdesk.tab.${view.id}`);
  } catch {
    return null;
  }

  if (!key || !/^[a-z]+$/.test(key)) return null;

  return $(`[role="tab"][data-card-tab="${key}"]`, view) ? key : null;
}

/**
 * Offers the tabs that have something on them, and only those.
 *
 * The chosen one moves to the first left when it empties, and the strip goes
 * when one tab is all there is: a choice of one says nothing the cards do not.
 */
function offerTheTabsThatHoldSomething(view) {
  const tabs = $$('[role="tab"]', view);
  let first = null;

  for (const tab of tabs) {
    const panel = document.getElementById(tab.getAttribute('aria-controls'));
    const holds = [...(panel?.children ?? [])].some((card) => !card.hidden);

    if (tab.hidden === holds) tab.hidden = !holds;
    if (holds && !first) first = tab;
  }

  const strip = $('.screen-tabs', view);
  const several = tabs.filter((tab) => !tab.hidden).length > 1;

  if (strip && strip.hidden === several) strip.hidden = !several;

  const chosen = tabs.find((tab) => tab.getAttribute('aria-selected') === 'true');
  if (first && (!chosen || chosen.hidden)) chooseScreenTab(view, first.dataset.cardTab);
}

/** Moves along a strip with the arrow keys, Home and End, as any tab strip does. */
function walkTheScreenTabs(strip, event) {
  const tabs = $$('[role="tab"]', strip).filter((tab) => !tab.hidden);
  const at = tabs.indexOf(document.activeElement);
  if (at < 0) return;

  const to = { ArrowRight: at + 1, ArrowLeft: at - 1, Home: 0, End: tabs.length - 1 }[event.key];
  if (to === undefined) return;

  event.preventDefault();
  chooseScreenTab(strip.closest('.view'), tabs[(to + tabs.length) % tabs.length].dataset.cardTab, { focus: true });
}

/**
 * Opens the tab a card is on, for whatever is about to point at it - a banner's
 * way to a card, or a step of the guided tour. A scroll to an element on a tab
 * that is not chosen lands nowhere, and a ring drawn round it rings nothing.
 */
function openTheTabOf(node) {
  const panel = node?.closest('[role="tabpanel"]');
  const view = panel?.closest('.view');

  if (panel?.hidden && view) chooseScreenTab(view, panel.dataset.cardPanel);
}

/** Whether an element is on a tab of its screen that is not the chosen one. */
function onAHiddenTab(node) {
  return Boolean(node?.closest('[role="tabpanel"]')?.hidden);
}

// --------------------------------------------------------------- bootstrap

// viewTheReaderMayHave answers with the screen asked for, or the welcome screen
// when that one takes a permission the reader has not got.
//
// Most ways in were already covered, and it is worth saying which rather than
// implying they were not. The address bar and the remembered screen both go
// through startingView, which only accepts a view whose tab is visible; the tour
// drops steps whose permission the reader lacks; the rest are reached from
// inside a screen somebody already has.
//
// The release banner's button was the one that was not. It calls switchView
// directly, and it was still on screen for an account that had just signed in
// behind an administrator - so an ordinary user clicked it and got the
// administration screen. Both halves are fixed; this is the half that stops the
// next direct caller needing to remember.
//
// The data was never at risk: every request behind that screen is refused by the
// server on its own. What was at risk is the sentence this application is meant
// to be describable in - somebody who may not administer this installation
// should not be looking at the screen that administers it.
//
// The permission comes off the tab, so there is one declaration of who may have
// a screen rather than two that can disagree.
function viewTheReaderMayHave(name) {
  const tab = $(`.tab[data-view="${name}"]`);
  const needs = tab?.dataset.perm;

  if (!needs || can(...needs.split(','))) return name;

  return 'welcome';
}

function switchView(name) {
  name = viewTheReaderMayHave(name);

  $$('.tab').forEach((tab) => tab.setAttribute('aria-current', String(tab.dataset.view === name)));
  $$('.view').forEach((view) => { view.hidden = view.id !== `view-${name}`; });

  // In the address bar, so a reload comes back to the same screen and a link to
  // one can be sent to somebody. replaceState rather than assigning to
  // location.hash: assigning pushes an entry, and Back would then walk through
  // every tab that was looked at instead of leaving the page.
  if (currentHashView() !== name) {
    history.replaceState(null, '', `#${name}`);
  }

  rememberView(name);

  // Filled on arrival rather than once, because who it greets and what it
  // offers them may have changed since it was last drawn.
  if (name === 'welcome') renderWelcome();

  // And what the reader was told, which other people's changes add to while
  // nothing on this page moves.
  if (name === 'tickets') loadTold().catch((err) => toastFailure(err));

  // The log viewer polls, so it follows the screen it lives on rather than
  // running for as long as the tab is open.
  if (logViewerActive()) schedulePoll({ immediate: true });
  else stopLogPolling();

  // A password somebody was reading goes back behind its dots. Not only on the
  // account screen: these boxes are on the sign-in form, the account card and
  // three of the administration cards, and leaving any of them is the end of
  // looking at one.
  hideEveryRevealedPassword();

  // Two things on the account screen do not survive leaving it, and both are
  // credentials. The enrolment panel holds a shared secret and the QR code that
  // encodes it; the token panel holds a value that carries the whole of this
  // account's role. Neither has any business sitting on a screen somebody has
  // walked away from, and starting again is what each card's own button does.
  if (name !== 'settings') {
    if ($('#totp-setup') && !$('#totp-setup').hidden) renderTOTPState();

    forgetTheTokenValue();
  }
}

/** The view named in the address bar, if it names one. */
function currentHashView() {
  return decodeURIComponent(window.location.hash.replace(/^#/, ''));
}

/**
 * Where this person's last visit left off, if it was recorded.
 *
 * Per account and in localStorage rather than in the session: the point is to
 * survive signing out, closing the browser and coming back tomorrow. Keyed by
 * user id so two people sharing a machine do not inherit each other's screen.
 *
 * Storage can be refused outright in private browsing, which costs nothing here -
 * the first permitted tab is a perfectly good answer.
 */
function rememberedView() {
  if (!me.user) return '';

  try {
    return localStorage.getItem(`qubitdesk_view_${me.user.id}`) ?? '';
  } catch {
    return '';
  }
}

/** Records the screen somebody is on, for the next time they sign in. */
function rememberView(name) {
  // Not while the initial password stands: the wizard forces My account open, and
  // recording that would make the forced screen look like a choice somebody made.
  if (!me.user || me.user.mustChangePassword) return;

  // The greeting is not somewhere somebody was working, it is the way back to the
  // start - recording it would make "carry on where you were" lead here.
  if (name === 'welcome') return;

  try {
    localStorage.setItem(`qubitdesk_view_${me.user.id}`, name);
  } catch {
    // Nothing to do about a storage that will not take it.
  }
}

/**
 * Opens the screen this session starts on, and only then calls the page loaded.
 *
 * refreshAll marks the page loaded once every screen has been filled from the
 * server, and for a refresh that is the truth. On the way in it is one step
 * early: the first view has still to be chosen, and choosing it switches the
 * view.
 *
 * That mark is what anything outside this page has to go on - a browser case, or
 * a person wondering whether a blank card is empty or unanswered - and it was
 * saying "ready" while the interface was still about to change screens. A tab
 * opened in that window was switched away from a moment later, with nothing to
 * show that anything had happened.
 *
 * So the mark is taken back and given again around the choice. Nothing is
 * awaited in between, so it is never observably given too early.
 */
function openTheStartingView() {
  document.documentElement.dataset.loaded = 'no';

  switchView(startingView());

  document.documentElement.dataset.loaded = 'yes';
}

/**
 * Whether this reader can actually be sent to that screen.
 *
 * A view name is never more than a string that was true once: the hash is
 * whatever was typed or bookmarked, and a remembered screen may belong to a tab
 * this account has since lost, or to one that stopped existing between releases -
 * this application has removed a view before now.
 *
 * Its own function because two callers ask it and only one used to. startingView
 * had this inline; onwardView, behind the greeting's Continue button, took the
 * remembered name unchecked. A screen that no longer exists then went through to
 * switchView, which hid every view and showed one that was not there: a blank
 * page, no tab marked current, and no way out but the title or a reload.
 *
 * The greeting is permitted by name because it has no tab of its own. It asks for
 * no right - it only ever says what this person already has.
 */
function viewIsOffered(name) {
  return name === 'welcome'
    || $$('.tab').some((tab) => !tab.hidden && tab.dataset.view === name);
}

/**
 * The view to open: the one in the address bar when it is real and permitted, then
 * the one this person was last on, and otherwise the greeting.
 *
 * Checked against the tabs rather than trusted, because neither the hash nor the
 * remembered name is anything more than a string that was true once - the hash is
 * whatever was typed or bookmarked, and a remembered screen may belong to a tab
 * this account has since lost, or one that stopped existing between releases.
 *
 * The greeting has no tab of its own, so it is permitted by name: it asks for no
 * right, because it only ever says what this person already has. It is where a
 * first sign-in lands, and where anybody lands who has not been anywhere yet -
 * somebody who was working somewhere goes back to it instead, which is the point
 * of remembering.
 */
function startingView() {
  const wanted = currentHashView();
  if (viewIsOffered(wanted)) return wanted;

  const last = rememberedView();
  if (viewIsOffered(last)) return last;

  return 'welcome';
}

/** Picks the first tab the user is actually allowed to see. */
function firstVisibleView() {
  const tab = $$('.tab').find((candidate) => !candidate.hidden);
  return tab ? tab.dataset.view : 'settings';
}

async function refreshAll() {
  // Says whether every screen has been filled from the server yet.
  //
  // Written for anything watching from outside - a browser case, a person
  // wondering why a card is blank - because there is no other way to tell a
  // screen that is empty from one that has not been answered yet. Every form
  // and every card in this application is in index.html, so all of them are on
  // screen from the first paint and hold nothing until their request lands.
  //
  // Cleared here rather than only set at the end, so a second pass says
  // "loading" again while it runs.
  document.documentElement.dataset.loaded = 'no';

  await loadMe();

  // From here on, whether this account may still do what it could a moment ago
  // is checked on a timer as well as on every response. Started after /me
  // because that is what establishes the revision to compare against.
  startPermissionPolling();

  // And the connection the server writes down when the application itself is
  // about to change underneath this page. Only for somebody signed in: it is the
  // sign-in screen's business what happens after they get there.
  startAnnouncements();

  // Whether a newer version exists, for whoever may do something about it.
  startReleaseWatch();

  // On a first sign-in the zone and the language are the browser's, adopted
  // here; what the account says afterwards is read again.
  if (await adoptBrowserDefaults()) await loadMe();

  // Before every other loader, because the server blocks the rest of the API
  // while the initial password stands - and that is exactly when the wizard is
  // most needed. /setup deliberately stays reachable through that block.
  await loadSetup();

  await loadLanguages();

  // With the initial password still in place the server refuses everything
  // below, so asking would only produce a screenful of errors. What is left -
  // the banner, My account, the wizard - is exactly what is needed to get past
  // it, and My account is where that happens.
  if (me.user?.mustChangePassword) {
    fillSettingsForm();
    switchView('settings');

    // And on the tab the password is on, which is not the first: a screen of
    // working hours the server will refuse to save, with the one form that
    // gets past the block a tab away, is the wizard pointing at nothing.
    openTheTabOf($('#form-password'));

    // Loaded, as far as this account is going to get: the server refuses the
    // rest until the password is replaced, so there is nothing else coming.
    // Saying "still loading" here would be waiting for requests that are never
    // going to be made.
    document.documentElement.dataset.loaded = 'yes';

    return;
  }

  // Roles first: the user table renders a role picker from them.
  await loadRoles();
  await loadSysConfig();
  await loadTicketSetup();
  await loadCommunication();
  await loadCustomers();
  await loadGeneralCatalog();
  await loadClassDefinitions();
  await loadConfigItems();
  await loadItsmChanges();
  await loadPortal();
  await loadFAQ();
  await loadTickets();
  await loadUsers();
  await loadAutomation();
  await loadAdmin();
  await loadTokens();
  await loadAndroidCode();
  await loadPasskeys();
  await loadOpenIDLink();
  fillSettingsForm();

  document.documentElement.dataset.loaded = 'yes';
}

function wireForms() {
  $('#form-user').addEventListener('submit', (e) => {
    e.preventDefault();

    const form = e.target;
    const id = form.elements.id.value;

    // Correcting an existing account, which takes the two fields that have no
    // other control. The role is changed from its own dropdown in the row, and a
    // password is not set for somebody else here - so neither is read from a form
    // that is not showing them, and neither is sent.
    if (id) {
      const correction = {
        name: form.elements.name.value.trim(),
        email: form.elements.email.value.trim(),
      };

      mutate(() => api(`/users/${id}`,
        { method: 'PUT', body: JSON.stringify(correction) }),
      t('msg.userSaved', 'User saved'),
      async () => { resetUserForm(); await refreshAll(); });

      return;
    }

    const body = formData(form);

    mutate(() => api('/users', { method: 'POST', body: JSON.stringify(body) }),
      t('msg.userCreated', 'User created'),
      async () => { resetUserForm(); await refreshAll(); });
  });

  $('#user-cancel').addEventListener('click', resetUserForm);

  $('#form-role').addEventListener('submit', (e) => {
    e.preventDefault();
    const form = e.target;
    const id = form.elements.id.value;
    const permissions = $$('input[name=permissions]:checked', form).map((i) => i.value);
    const body = {
      name: form.elements.name.value.trim(),
      description: form.elements.description.value.trim(),
      permissions,
    };

    const request = id
      ? api(`/roles/${id}`, { method: 'PUT', body: JSON.stringify(body) })
      : api('/roles', { method: 'POST', body: JSON.stringify(body) });

    mutate(() => request, id ? t('msg.roleSaved', 'Role saved') : t('msg.roleCreated', 'Role created'),
      async () => { resetRoleForm(); await refreshAll(); });
  });

  $('#role-reset').addEventListener('click', resetRoleForm);

  wireTickets();
  wireCustomers();
  wirePortal();
  wirePublishedFAQ();
  wireGeneralCatalog();
  wireClassDefinitions();
  wireConfigItems();
  wireItsmChanges();
  wireSysConfig();
  wireCalendars();
  wireSla();
  wireUnlockTypes();
  wireAutomation();
  wireNotifications();
  wireTold();
  wireTemplates();
  wireFAQ();

  $('#form-queue').addEventListener('submit', (e) => {
    e.preventDefault();
    saveQueue(e.target);
  });

  $('#queue-cancel').addEventListener('click', resetQueueForm);

  for (const kind of Object.keys(CATALOGS)) {
    $(`#form-${kind}`).addEventListener('submit', (e) => {
      e.preventDefault();
      saveCatalogEntry(kind, e.target);
    });

    $(`#${kind}-cancel`).addEventListener('click', () => resetCatalogForm(kind));
  }

  $('#form-state').elements.type.addEventListener('change', (e) => showNextState(e.target.form));

  // The same thing, offered where a shipped role is being read rather than at
  // the end of a form that is a screen further down.
  $('#role-start-new')?.addEventListener('click', () => {
    resetRoleForm();
    $('#form-role .perms')?.scrollIntoView({ block: 'start', behavior: 'smooth' });
    $('#form-role').elements.name.focus();
  });

  $('#notify-allow').addEventListener('click', async () => {
    if ('Notification' in window) await Notification.requestPermission();

    drawNotifyState();
  });

  $('#export-me').addEventListener('click', () => mutate(
    () => downloadFile(`${API}/me/export`, t('export.filename', 'everything-about-me'), 'zip'),
    t('wb.exported', 'Export saved'), null));

  $('#form-password').addEventListener('submit', (e) => {
    e.preventDefault();
    const body = formData(e.target);
    mutate(() => api('/me/password', { method: 'PUT', body: JSON.stringify(body) }),
      t('msg.passwordChanged',
        'Password changed. Your other devices have been signed out.'),
      async () => {
        e.target.reset();

        // Deliberately not signed out. The server ends this account's other
        // sessions and keeps this one, because this is the device that just
        // proved it knew the old password - so there is nothing to sign back
        // into, and dropping somebody at a sign-in screen achieves only a second
        // sign-in.
        //
        // Refreshed, though. Until the change the server refuses most of the API
        // and the interface knows it; the flag that says so lives on the account,
        // and this screen is still holding the copy it was given at sign-in. The
        // re-login used to do this refresh as a side effect.
        await refreshAll();
      });
  });

  $('#tabs').addEventListener('click', (e) => {
    const tab = e.target.closest('.tab');
    if (tab) switchView(tab.dataset.view);
  });

  // Following a link to another screen, or editing the address bar, moves the
  // screen rather than leaving the address disagreeing with what is shown.
  window.addEventListener('hashchange', () => {
    // Only once there is a session; before that the sign-in screen is the whole
    // interface and switching underneath it would change what appears after it.
    if (!$('#login-screen').hidden) return;

    switchView(startingView());
  });
}

async function init() {
  // The sign-in form is wired first and on its own: if anything below fails,
  // the user must still be able to sign in rather than face a form whose
  // submit handler was never attached, which would silently reload the page.
  $('#form-login').addEventListener('submit', submitLogin);
  wireKerberosSignIn();

  // Before anything can fail for want of a connection, so a page that cannot
  // reach the installation says so rather than only failing.
  keepTheShell();
  wireOffline();

  // With them, because a failed start says so in a notice.
  followPointerOverNotices();

  // Here rather than with the rest of the wiring below, so the sign-in form has
  // its reveal before anything that could fail has been tried. It also registers
  // a submit handler per field, which runs beside a form's own rather than
  // instead of it - two listeners on one event, in the order they were added.
  wirePasswordReveal();

  // Beside the password fields, and for the same reason: a field added later is
  // covered without anybody remembering to come back here.
  enhanceDateFields();

  // Before anything is shown or loaded: the cards of the two long screens into
  // their tabs, so every loader below fills a card where it will stay.
  arrangeScreenTabs();

  // The same again for what the browser says when it refuses a field: bound to
  // the document, so a form drawn later is covered too.
  wireValidityMessages();

  // Every form, and before anything is loaded, so a reader who starts typing
  // into a card the moment it appears is already protected from the loaders
  // filling the cards after it. Forms nothing refills are unaffected by it.
  $$('form').forEach(watchForEditing);

  // Appearance is a device setting and needs no session, so the picker works on
  // the sign-in screen too.
  wireTheme();

  // Coming back to a tab is the moment somebody would find out anyway, so it is
  // the moment to ask - and it covers however long the tab was hidden in one go,
  // which is why the interval is allowed to skip while it is.
  //
  // The same pair of questions the beat asks, through the same function, so this
  // one cannot go back to asking half of them.
  document.addEventListener('visibilitychange', askWhetherTheScreenIsStillTrue);

  // The browser's language's dictionary, on its way while the branding is.
  loadDictionary(activeLanguage());
  offerBrandingLanguages();

  // Branding is public, so the sign-in screen already carries the instance's
  // own title and logo. A failure here must not block signing in.
  try {
    await loadBranding();
  } catch {
    // Falls back to the brand alone, which the header carries in its markup and
    // the sign-in heading has to be told.
    drawWelcome(BRAND);
  }

  // Applied before the first render so the sign-in screen already speaks the
  // browser language; a signed-in user overrides it in loadMe.
  await applyLanguage(activeLanguage());

  // Before the sign-in screen is shown, so its passkey button appears with it
  // rather than popping in afterwards. The ticket's button likewise, and the
  // answer decides whether a ticket is tried at all below.
  await loadPasskeySupport();
  await loadKerberosSupport();
  await loadOpenIDOffer();
  await loadMaintenance();

  try {
    wireForms();
    wireTOTP();
    wireHolidays();
    wireAdmin();
    wireTokens();
    wireDirectorySync();
    wireTimezones();
    wireOperational();
    wireTelemetry();
    wireRestart();
    wireUpdate();
    wireUpdateCheck();
    wireBackups();
    wireCropChooser();

    // The one control on the rights notice, and the whole of what there is to do
    // about it.
    $('#rights-reload')?.addEventListener('click', () => window.location.reload());

    $('#branding-language')?.addEventListener('change', (e) => {
      showBrandingLanguage(e.target.value);
    });
    wireNavigationMenu();
    trackHeight('.topbar', '--topbar-height');
    trackHeight('#site-footer', '--footer-height');
    placeFooter();
    wireInstall();
    wireSheetCards();
    wireSetup();
    wireTour();
    wireWelcome();
    wirePasskeys();
    wireOpenID();
    wireLogViewer();
    wireMaintenance();
    wirePush();
    $('#logout').addEventListener('click', doLogout);
    $('#language-picker').addEventListener('change', (e) => mutate(
      () => api('/me/language', { method: 'PUT', body: JSON.stringify({ language: e.target.value }) }),
      null,
      refreshAll));
  } catch (err) {
    toastFailure(err, t('msg.initFailed', 'Initialisation failed'));
  }

  // Before the first load, which then finds the session the round opened.
  const returned = await finishTheProviderRound();

  try {
    await refreshAll();

    // After the loaders and before anything can look: everything from here to
    // the end of openTheStartingView happens without awaiting, so nothing
    // observes a page calling itself loaded while what somebody typed into it is
    // still missing.
    restoreDrafts();

    hideLogin();
    openTheStartingView();

    // After the first view is up, so the tour highlights something that is
    // actually on screen.
    await greetAfterSignIn();

    // And whatever the document before this one was told to say. An update
    // finishes by throwing the page away, so the answer to the button somebody
    // pressed arrives here rather than there.
    saySoAfterTheReload();

    // And what a round through the provider came to, if this page is its way back.
    sayWhatTheProviderRoundCameTo(returned);
  } catch (err) {
    afterAFailedFirstLoad(err);

    // A round that signed nobody in says why on the form it leaves the person at,
    // rather than having a ticket tried over it.
    if (returned?.problem && !$('#login-screen').hidden) {
      showLogin(returned.problem);

      return;
    }

    // Then the ticket, where the installation offers one and this tab has not
    // been told otherwise - after the form is up, so a browser without a ticket
    // is simply left looking at it. Only where the failure put the form up: a
    // load that failed behind a session that was accepted keeps its screen.
    if (!$('#login-screen').hidden && kerberosOffered && !handedToASession
      && mayTryKerberosUnasked()) {
      await signInWithKerberos({ quietly: true });
    }
  }
}

/**
 * What a first load that failed leaves on screen.
 *
 * A session /me accepted is not undone by a loader that failed after it - a
 * query the database refused, a connection that dropped part-way. Showing the
 * sign-in form here put it over a session that was fine, said nothing about
 * what had failed, and made signing in again open a second one. The reader
 * keeps the screen they are signed into and is told what did not load, as a
 * reload that fails after a save is.
 *
 * Not on a 401, which is the session itself refused: a first load that failed
 * for want of one can land after somebody signed in underneath it, and that
 * belongs to showLogin, which already answers exactly that.
 */
function afterAFailedFirstLoad(err) {
  showSignInOffline(Boolean(err?.unreachable) || !navigator.onLine);

  if (me.user && err?.status !== 401) {
    restoreDrafts();
    hideLogin();
    openTheStartingView();
    toastFailure(err, t('msg.loadFailed', 'Could not load everything'));

    return;
  }

  // No usable session: the sign-in screen is the whole interface until there is
  // one. Unless somebody signed in while this was running, which showLogin
  // decides - it is the same question wherever it is asked from.
  showLogin();

  // And said, where what failed was not the session refused. The server answers
  // a session it could not check - a database that did not answer - as a
  // failure, keeping its cookie for when the database is back, and the form on
  // its own reads as the session having ended.
  if (err?.status !== 401 && !$('#login-screen').hidden) {
    toastFailure(err, t('msg.loadFailed', 'Could not load everything'));
  }
}

document.addEventListener('DOMContentLoaded', init);
