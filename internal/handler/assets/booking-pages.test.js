// Run: node --test internal/handler/assets/booking-pages.test.js
//
// Runs the REAL page scripts of book.html and manage.html (with booking-logic.js inlined, as
// the Go handlers serve them) against a minimal fake DOM, a scripted fetch and a fixed clock,
// to pin behaviour that lives in the page wiring rather than in booking-logic.js: out-of-order
// month answers, the one-day fallback, the 409 path, the zone selector and the zone change on
// manage's confirm view. Template actions are replaced with fixed values below; only the
// ones the scripts read are listed, anything else becomes "".
//
// Not a browser: markup is only as real as the strings the scripts write. The Go surface
// tests (booking_surfaces_contract_test.go) cover the rendered templates themselves.
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const TEMPLATES = path.join(__dirname, '..', 'templates');
const LOGIC = fs.readFileSync(path.join(__dirname, 'booking-logic.js'), 'utf8');
const NOW = Date.parse('2026-09-29T20:20:00Z'); // Tue 29 Sep 2026, 15:20 in Lima

// The page script: the <script> block that holds `marker`, with the template actions filled.
function pageScript(file, marker, values) {
  const src = fs.readFileSync(path.join(TEMPLATES, file), 'utf8');
  const at = src.indexOf(marker);
  const start = src.lastIndexOf('<script>', at) + '<script>'.length;
  const end = src.indexOf('</script>', at);
  return src.slice(start, end).replace(/("?)\{\{\s*\.(\w+)\s*\}\}("?)/g, (_m, q1, name, q2) => {
    const v = Object.prototype.hasOwnProperty.call(values, name) ? values[name] : '';
    if (q1 && q2) return q1 + String(v) + q2;
    return q1 + JSON.stringify(v) + q2;
  });
}

// ---- fake DOM ---------------------------------------------------------------------------
function classList() {
  const set = new Set();
  return {
    add: (...c) => c.forEach((x) => set.add(x)),
    remove: (...c) => c.forEach((x) => set.delete(x)),
    toggle: (c, on) => { const want = on === undefined ? !set.has(c) : !!on; if (want) set.add(c); else set.delete(c); return want; },
    contains: (c) => set.has(c),
  };
}

function makeEl(tag, id) {
  const el = {
    tagName: String(tag || 'div').toUpperCase(), id: id || '', children: [], parentNode: null,
    _html: '', _text: '', value: '', disabled: false, selected: false, dataset: {}, style: {},
    attrs: {}, handlers: {}, classList: classList(), className: '', label: '',
    offsetWidth: 0, scrollHeight: 0, clientHeight: 0,
    get innerHTML() { return this._html; },
    set innerHTML(v) { this._html = String(v); this.children = []; this._text = ''; },
    get textContent() { return this._text; },
    set textContent(v) { this._text = String(v); this._html = ''; this.children = []; },
    get text() { return this._text; },
    set text(v) { this._text = String(v); },
    get firstChild() { return this.children[0] || null; },
    addEventListener(type, fn) { (this.handlers[type] = this.handlers[type] || []).push(fn); },
    appendChild(c) { this.children.push(c); c.parentNode = this; return c; },
    insertBefore(c, ref) {
      const i = ref ? this.children.indexOf(ref) : -1;
      if (i === -1) this.children.push(c); else this.children.splice(i, 0, c);
      c.parentNode = this; return c;
    },
    insertAdjacentHTML(pos, html) { this._html = pos === 'afterbegin' ? html + this._html : this._html + html; },
    setAttribute(k, v) { this.attrs[k] = String(v); },
    getAttribute(k) { return this.attrs[k]; },
    removeAttribute(k) { delete this.attrs[k]; },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    closest() { return null; },
    focus() {},
    remove() {},
    getBoundingClientRect() { return { width: 1000, height: 600, top: 0, left: 0, right: 1000, bottom: 600 }; },
  };
  if (el.tagName === 'SELECT') {
    Object.defineProperty(el, 'options', {
      get() { return this.children.flatMap((g) => (g.tagName === 'OPTGROUP' ? g.children : [g])); },
    });
    Object.defineProperty(el, 'selectedIndex', {
      get() { const i = this.options.findIndex((o) => o.selected); return i === -1 ? 0 : i; },
    });
    Object.defineProperty(el, 'value', {
      get() { const o = this.options[this.selectedIndex]; return o ? o.value : ''; },
      set(v) { this.options.forEach((o) => { o.selected = o.value === v; }); },
    });
  }
  return el;
}

// Visible text of an element: its markup with the tags stripped, plus its element children.
function textOf(el) {
  const own = el._text || el._html.replace(/<[^>]*>/g, ' ');
  return (own + ' ' + el.children.map(textOf).join(' ')).replace(/[  ]/g, ' ').replace(/\s+/g, ' ').trim();
}

// The time buttons in #slots-list: [{i, label, taken}].
function slotButtons(list) {
  const out = [];
  const re = /<button type="button" class="slot-btn( taken)?"([^>]*)>([^<]*)<\/button>/g;
  let m;
  while ((m = re.exec(list.innerHTML))) {
    const i = /data-i="(\d+)"/.exec(m[2]);
    out.push({ i: i ? Number(i[1]) : -1, label: m[3].replace(/[  ]/g, ' '), taken: !!m[1] });
  }
  return out;
}

// ---- page harness -----------------------------------------------------------------------
function loadPage({ file, marker, values, browserTZ, zones }) {
  process.env.TZ = browserTZ; // the Date methods follow the process zone
  const byId = {};
  const $ = (id) => byId[id] || (byId[id] = makeEl(id === 'tz-select' ? 'select' : 'div', id));
  const card = makeEl('div');
  const requests = [];
  const document = {
    getElementById: $,
    querySelector: (sel) => (sel === '.card' ? card : null),
    querySelectorAll: () => [],
    createElement: (tag) => makeEl(tag),
  };
  // fetch answers are released by the test: each request waits for respond().
  function fetch(url, opts) {
    return new Promise((resolve) => {
      requests.push({
        url: String(url), method: (opts && opts.method) || 'GET', body: opts && opts.body,
        respond(status, data) {
          resolve({ ok: status >= 200 && status < 300, status, json: async () => data });
        },
      });
    });
  }
  const RealDTF = Intl.DateTimeFormat;
  function FakeDTF(locale, opts) {
    if (locale === undefined && opts === undefined) {
      return { resolvedOptions: () => ({ timeZone: browserTZ }) };
    }
    return new RealDTF(locale, opts);
  }
  const FakeIntl = Object.create(Intl);
  FakeIntl.DateTimeFormat = FakeDTF;
  FakeIntl.supportedValuesOf = () => zones || ['Africa/Abidjan', 'America/Lima', 'Europe/Madrid', 'Australia/Sydney'];
  class FakeDate extends Date {
    constructor(...a) { if (a.length === 0) super(NOW); else super(...a); }
    static now() { return NOW; }
  }
  const window = {
    __CALNODE_I18N: { slot_taken_error: 'Ese horario ya fue tomado', slot_no_longer_available_error: 'Ese horario ya no está disponible' },
    location: { search: '', href: '' },
    innerWidth: 1200,
    addEventListener() {},
  };
  const ctx = {
    window, document, fetch, Intl: FakeIntl, Date: FakeDate, URLSearchParams,
    navigator: { languages: ['es-PE'], language: 'es-PE' },
    setTimeout, clearTimeout, console,
  };
  window.document = document;
  vm.createContext(ctx);
  ctx.self = ctx;
  vm.runInContext(LOGIC, ctx);
  ctx.BookingLogic = ctx.self.BookingLogic;
  vm.runInContext(pageScript(file, marker, values), ctx);
  return { $, requests, ctx, card };
}

const settle = () => new Promise((r) => setTimeout(r, 0));
async function flush() { for (let i = 0; i < 5; i++) await settle(); }
function click(el, target) { (el.handlers.click || []).forEach((fn) => fn({ target, preventDefault() {} })); }
function clickDay(page, ds) {
  const [y, m, d] = ds.split('-').map(Number);
  assert.match(page.$('cal').innerHTML, new RegExp(`data-ds="${ds}"`), `day ${ds} is clickable`);
  click(page.$('cal'), { closest: () => ({ disabled: false, dataset: { ds, y: String(y), m: String(m - 1), d: String(d) } }) });
}
function clickSlot(page, i) {
  click(page.$('slots-list'), { closest: () => ({ disabled: false, dataset: { i: String(i) } }) });
}
function lastSlotsRequest(page) {
  return page.requests.filter((r) => r.url.includes('/slots?')).pop();
}

const BOOK = {
  file: 'book.html', marker: 'const SLUG',
  values: { Slug: 'mentoria', MaxFutureDays: 60, PriceCents: 0, Currency: 'usd', Name: 'Mentoría', SoleHostName: 'Ana', MinNoticeLabel: '', Locale: 'es', Hour12: true },
};
const MANAGE = {
  file: 'manage.html', marker: 'const TOKEN',
  values: { Token: 'tok', EventTypeSlug: 'mentoria', MaxFutureDays: 60, CurrentStartISO: '2026-10-15T14:00:00Z', OrganizerTZ: 'America/Lima', SoleHostName: 'Ana', MinNoticeLabel: '', Locale: 'es', Hour12: true, BookingID: 'b1', EventTypeName: 'Mentoría', HostName: 'Ana' },
};

const savedTZ = process.env.TZ;
test.after(() => { if (savedTZ === undefined) delete process.env.TZ; else process.env.TZ = savedTZ; });

test('book.html: a late answer for the month the visitor left does not overwrite the shown month', async () => {
  const page = loadPage({ ...BOOK, browserTZ: 'America/Lima' });
  const sep = lastSlotsRequest(page);
  assert.match(sep.url, /from=2026-09-01&to=2026-09-30/);
  click(page.$('next-btn'));
  const oct = lastSlotsRequest(page);
  assert.match(oct.url, /from=2026-10-01&to=2026-10-31/);
  oct.respond(200, { slots: [
    { start: '2026-10-05T09:00:00-05:00' }, { start: '2026-10-06T09:00:00-05:00' }, { start: '2026-10-07T09:00:00-05:00' },
  ] });
  await flush();
  sep.respond(200, { slots: [{ start: '2026-09-30T09:00:00-05:00' }, { start: '2026-10-01T09:00:00-05:00' }] });
  await flush();
  const cal = page.$('cal').innerHTML;
  for (const d of ['2026-10-05', '2026-10-06', '2026-10-07']) assert.match(cal, new RegExp(`data-ds="${d}"`));
  assert.doesNotMatch(cal, /data-ds="2026-10-01"/, 'the late September answer did not replace October');
});

test('book.html: the one-day fallback lists only the picked day, and the labels follow the slot instant', async () => {
  const page = loadPage({ ...BOOK, browserTZ: 'Europe/Madrid' });
  click(page.$('next-btn')); // October; its month answer stays pending
  click(page.$('next-btn')); // November … back to October so the fallback path runs
  click(page.$('prev-btn'));
  clickDay(page, '2026-10-31');
  const day = lastSlotsRequest(page);
  assert.match(day.url, /from=2026-10-31&to=2026-10-31&tz=Europe%2FMadrid/);
  day.respond(200, { slots: [
    { start: '2026-10-31T21:00:00+01:00', host_ids: [] },
    { start: '2026-11-01T02:30:00+01:00', host_ids: [] }, // Sunday in Madrid
  ] });
  await flush();
  const labels = slotButtons(page.$('slots-list')).map((b) => b.label);
  assert.deepEqual(labels, ['9:00 p. m.'], 'the next day\'s 2:30 a. m. is not listed under Saturday');
  clickSlot(page, 0);
  assert.equal(page.$('form-slot-label').textContent.replace(/[  ]/g, ' '), 'sáb, 31 oct · 9:00 p. m.');
});

test('book.html: a 409 shows the message on the times list and fetches that day again', async () => {
  const page = loadPage({ ...BOOK, browserTZ: 'America/Lima' });
  lastSlotsRequest(page).respond(200, { slots: [
    { start: '2026-09-30T15:30:00-05:00', host_ids: [] }, { start: '2026-09-30T16:00:00-05:00', host_ids: [] },
  ] });
  await flush();
  clickDay(page, '2026-09-30');
  assert.deepEqual(slotButtons(page.$('slots-list')).map((b) => b.label), ['3:30 p. m.', '4:00 p. m.']);
  clickSlot(page, 0);
  const form = { name: { value: 'Ana' }, email: { value: 'ana@example.com' }, elements: {}, hp_extra: { value: '' } };
  (page.$('booking-form').handlers.submit || []).forEach((fn) => fn({ preventDefault() {}, target: form }));
  await flush();
  const post = page.requests.find((r) => r.method === 'POST');
  assert.ok(post, 'the booking was posted');
  post.respond(409, { error: 'taken' });
  await flush();
  const refetch = lastSlotsRequest(page);
  assert.match(refetch.url, /from=2026-09-30&to=2026-09-30/, 'the day is fetched again');
  refetch.respond(200, { slots: [{ start: '2026-09-30T16:00:00-05:00', host_ids: [] }] });
  await flush();
  assert.equal(page.$('slots-view').classList.contains('hidden'), false);
  assert.equal(page.$('form-view').classList.contains('hidden'), true);
  const list = page.$('slots-list');
  assert.match(textOf(list), /Ese horario ya fue tomado/, 'the message is on the visible list');
  assert.deepEqual(slotButtons(list).map((b) => b.label), ['4:00 p. m.'], 'the taken time is gone');
});

test('book.html: the zone selector selects the detected zone even when the list lacks it', () => {
  const page = loadPage({ ...BOOK, browserTZ: 'America/Argentina/Buenos_Aires',
    zones: ['Africa/Abidjan', 'America/Buenos_Aires', 'America/Lima'] });
  assert.equal(page.$('tz-select').value, 'America/Argentina/Buenos_Aires');
  assert.match(lastSlotsRequest(page).url, /tz=America%2FArgentina%2FBuenos_Aires/);
});

test('book.html: the 60th day stays open across a summer-time change (Madrid)', async () => {
  const page = loadPage({ ...BOOK, browserTZ: 'Europe/Madrid' });
  click(page.$('next-btn'));
  click(page.$('next-btn')); // November
  lastSlotsRequest(page).respond(200, { slots: [
    { start: '2026-11-27T15:00:00+01:00' }, { start: '2026-11-28T15:00:00+01:00' },
  ] });
  await flush();
  assert.match(page.$('cal').innerHTML, /data-ds="2026-11-28"/, 'Sat 28 Nov is day 60 and bookable');
});

test('manage.html: a zone change on the confirm view moves the day with the instant and relabels the list', async () => {
  const page = loadPage({ ...MANAGE, browserTZ: 'America/Lima' });
  click(page.$('reschedule-btn'));
  lastSlotsRequest(page).respond(200, { slots: [] }); // September
  await flush();
  click(page.$('next-btn'));
  lastSlotsRequest(page).respond(200, { slots: [
    { start: '2026-10-31T15:30:00-05:00' }, { start: '2026-10-31T20:30:00-05:00' },
  ] });
  await flush();
  clickDay(page, '2026-10-31');
  assert.deepEqual(slotButtons(page.$('slots-list')).map((b) => b.label), ['3:30 p. m.', '8:30 p. m.']);
  clickSlot(page, 1);
  const sp = (s) => s.replace(/[  ]/g, ' ');
  assert.equal(sp(page.$('new-time').textContent), 'sábado, 31 de octubre · 8:30 p. m.');

  const sel = page.$('tz-select');
  sel.value = 'Europe/Madrid';
  (sel.handlers.change || []).forEach((fn) => fn({ target: sel }));
  assert.equal(sp(page.$('new-time').textContent), 'domingo, 1 de noviembre · 2:30 a. m.',
    'day and time both come from the instant in the new zone');
  await flush();
  const nov = lastSlotsRequest(page);
  assert.match(nov.url, /from=2026-11-01&to=2026-11-30&tz=Europe%2FMadrid/, 'the month follows the picked day');
  nov.respond(200, { slots: [{ start: '2026-11-01T02:30:00+01:00' }, { start: '2026-11-02T15:00:00+01:00' }] });
  await flush();
  click(page.$('back-to-picker-btn'));
  assert.equal(sp(page.$('slots-date').textContent), 'domingo, 1 de noviembre');
  const buttons = slotButtons(page.$('slots-list'));
  assert.deepEqual(buttons.map((b) => b.label), ['2:30 a. m.'], 'the list is in the new zone');
  clickSlot(page, buttons[0].i);
  assert.equal(sp(page.$('new-time').textContent), 'domingo, 1 de noviembre · 2:30 a. m.', 'the button and the confirm agree');
});

test('manage.html: the current booking is left out of the list although it comes back in another offset', async () => {
  const page = loadPage({ ...MANAGE, browserTZ: 'America/Lima' });
  click(page.$('reschedule-btn'));
  lastSlotsRequest(page).respond(200, { slots: [] });
  await flush();
  click(page.$('next-btn'));
  lastSlotsRequest(page).respond(200, {
    slots: [{ start: '2026-10-15T10:00:00-05:00' }],
    taken: [{ start: '2026-10-15T09:00:00-05:00' }], // = CURRENT_ISO 2026-10-15T14:00:00Z
  });
  await flush();
  clickDay(page, '2026-10-15');
  assert.deepEqual(slotButtons(page.$('slots-list')).map((b) => [b.label, b.taken]), [['10:00 a. m.', false]]);
});

test('manage.html: a late month answer is dropped', async () => {
  const page = loadPage({ ...MANAGE, browserTZ: 'America/Lima' });
  click(page.$('reschedule-btn'));
  const sep = lastSlotsRequest(page);
  click(page.$('next-btn'));
  lastSlotsRequest(page).respond(200, { slots: [{ start: '2026-10-20T09:00:00-05:00' }] });
  await flush();
  sep.respond(200, { slots: [{ start: '2026-09-30T09:00:00-05:00' }] });
  await flush();
  assert.match(page.$('cal').innerHTML, /data-ds="2026-10-20"/);
});

// ---- embed.js ---------------------------------------------------------------------------
// The widget does not load booking-logic.js; it is run here as the standalone file it is.
const EMBED = fs.readFileSync(path.join(__dirname, '..', 'embed.js'), 'utf8');

function loadEmbed({ browserTZ }) {
  process.env.TZ = browserTZ;
  const requests = [];
  function fetch(url, opts) {
    return new Promise((resolve) => {
      requests.push({
        url: String(url), method: (opts && opts.method) || 'GET',
        respond(status, data) { resolve({ ok: status >= 200 && status < 300, status, json: async () => data }); },
      });
    });
  }
  const RealDTF = Intl.DateTimeFormat;
  function FakeDTF(locale, opts) {
    if (locale === undefined && opts === undefined) return { resolvedOptions: () => ({ timeZone: browserTZ }) };
    return new RealDTF(locale, opts);
  }
  const FakeIntl = Object.create(Intl);
  FakeIntl.DateTimeFormat = FakeDTF;
  class FakeDate extends Date {
    constructor(...a) { if (a.length === 0) super(NOW); else super(...a); }
    static now() { return NOW; }
  }
  class HTMLElementStub {
    constructor() { this.attrs = {}; this.style = { setProperty() {} }; this.events = []; }
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : null; }
    setAttribute(k, v) { this.attrs[k] = String(v); }
    attachShadow() { return makeEl('shadow-root'); }
    dispatchEvent(e) { this.events.push(e); }
  }
  let Widget = null;
  const document = {
    currentScript: null, readyState: 'complete',
    createElement: (tag) => makeEl(tag),
    createTextNode: (text) => { const n = makeEl('#text'); n.textContent = text; return n; },
    querySelectorAll: () => [],
    addEventListener() {},
  };
  const window = { location: { origin: 'https://agenda.test' }, customElements: null, top: null };
  const ctx = {
    window, document, fetch, Intl: FakeIntl, Date: FakeDate, HTMLElement: HTMLElementStub,
    customElements: { get: () => undefined, define: (_n, c) => { Widget = c; } },
    CustomEvent: class { constructor(type, init) { this.type = type; this.detail = init && init.detail; } },
    URL, navigator: { languages: ['es-PE'], language: 'es-PE' }, setTimeout, clearTimeout, console,
    requestAnimationFrame: (fn) => setTimeout(fn, 0),
  };
  window.customElements = ctx.customElements;
  vm.createContext(ctx);
  vm.runInContext(EMBED, ctx);
  const w = new Widget();
  w.attrs.slug = 'mentoria';
  w.connectedCallback();
  return { w, requests };
}

// Depth-first search of the widget's element tree.
function findAll(node, pred, out = []) {
  if (pred(node)) out.push(node);
  (node.children || []).forEach((c) => findAll(c, pred, out));
  return out;
}
const hasClass = (n, c) => String(n.className || '').split(/\s+/).includes(c);

async function openEmbed(browserTZ, firstMonth) {
  const e = loadEmbed({ browserTZ });
  e.requests.find((r) => r.url.includes('/public')).respond(200, { name: 'Mentoría', hosts: [{ name: 'Ana' }], locale: 'es', i18n: { slot_taken_error: 'Ese horario ya fue tomado' }, hour12: true, duration_minutes: 40 });
  e.requests.find((r) => r.url.includes('/questions')).respond(200, { items: [] });
  await flush();
  e.requests.filter((r) => r.url.includes('/slots?')).pop().respond(200, firstMonth);
  await flush();
  return e;
}
function embedDays(e) {
  return findAll(e.w.wrap, (n) => n.tagName === 'BUTTON' && hasClass(n, 'cd') && !n.disabled).map((n) => Number(n.textContent));
}
function embedNav(e, which) {
  const btn = findAll(e.w.wrap, (n) => n.tagName === 'BUTTON' && n.attrs['aria-label'] === which)[0];
  click(btn, {});
}

test('embed.js: a late answer for the month the visitor left does not overwrite the shown month', async () => {
  const e = await openEmbed('America/Lima', { slots: [{ start: '2026-09-30T15:30:00-05:00' }] });
  embedNav(e, 'next_month_aria'); // October
  const oct = e.requests.filter((r) => r.url.includes('/slots?')).pop();
  embedNav(e, 'prev_month_aria'); // back to September, while October is still loading
  const sep = e.requests.filter((r) => r.url.includes('/slots?')).pop();
  assert.notEqual(oct, sep);
  sep.respond(200, { slots: [{ start: '2026-09-30T15:30:00-05:00' }] });
  await flush();
  oct.respond(200, { slots: [{ start: '2026-10-05T09:00:00-05:00' }] });
  await flush();
  assert.deepEqual(embedDays(e), [30], 'September stays; the late October answer is dropped');
});

test('embed.js: a 409 returns to the day\'s times with the message and a refreshed list', async () => {
  const e = await openEmbed('America/Lima', { slots: [
    { start: '2026-09-30T15:30:00-05:00', host_ids: [] }, { start: '2026-09-30T16:00:00-05:00', host_ids: [] },
  ] });
  click(findAll(e.w.wrap, (n) => n.tagName === 'BUTTON' && hasClass(n, 'cd') && n.textContent === '30')[0], {});
  const times = () => findAll(e.w.wrap, (n) => n.tagName === 'BUTTON' && hasClass(n, 'slot-btn')).map((n) => n.textContent.replace(/[  ]/g, ' '));
  assert.deepEqual(times(), ['3:30 p. m.', '4:00 p. m.']);
  click(findAll(e.w.wrap, (n) => n.tagName === 'BUTTON' && hasClass(n, 'slot-btn'))[0], {});
  const form = findAll(e.w.wrap, (n) => n.tagName === 'FORM')[0];
  const inputs = findAll(form, (n) => n.tagName === 'INPUT');
  inputs.find((n) => n.attrs.type === 'text' && n.attrs.autocomplete === 'name').value = 'Ana';
  inputs.find((n) => n.attrs.type === 'email').value = 'ana@example.com';
  form.handlers.submit.forEach((fn) => fn({ preventDefault() {} }));
  await flush();
  e.requests.find((r) => r.method === 'POST').respond(409, { error: 'taken' });
  await flush();
  assert.equal(findAll(e.w.wrap, (n) => n.tagName === 'FORM').length, 0, 'back on the times list');
  const err = findAll(e.w.wrap, (n) => hasClass(n, 'form-error'));
  assert.equal(err.length, 1);
  assert.equal(err[0].textContent, 'Ese horario ya fue tomado');
  assert.deepEqual(times(), ['4:00 p. m.'], 'the taken time is gone at once');
  const refetch = e.requests.filter((r) => r.url.includes('/slots?')).pop();
  assert.match(refetch.url, /from=2026-09-29&to=2026-09-30/, 'the month is fetched again');
  refetch.respond(200, { slots: [{ start: '2026-09-30T16:00:00-05:00', host_ids: [] }, { start: '2026-09-30T16:30:00-05:00', host_ids: [] }] });
  await flush();
  assert.deepEqual(times(), ['4:00 p. m.', '4:30 p. m.'], 'the refreshed list is shown');
  assert.equal(findAll(e.w.wrap, (n) => hasClass(n, 'form-error')).length, 1, 'with the message still there');
});
