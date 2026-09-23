// Run: node --test internal/handler/assets/room-logic.test.js
const test = require('node:test');
const assert = require('node:assert');
const RL = require('./room-logic.js');

test('amHost — live flag OR current host metadata', () => {
  assert.equal(RL.amHost({ isHost: true }), true);
  assert.equal(RL.amHost({ hostMeta: true }), true);
  assert.equal(RL.amHost({ isHost: false, hostMeta: false }), false);
  assert.equal(RL.amHost({}), false);
});

test('nextIsHost — upgrade on host, downgrade only on explicit attendee', () => {
  assert.equal(RL.nextIsHost(false, 'host'), true);      // promoted/reassigned to host
  assert.equal(RL.nextIsHost(true, 'attendee'), false);  // explicit single-host demote
  assert.equal(RL.nextIsHost(true, ''), true);           // transient/empty must NOT strip host
  assert.equal(RL.nextIsHost(true, undefined), true);
  assert.equal(RL.nextIsHost(false, 'attendee'), false);
});

test('hostUi — durable host sees everything but reclaim', () => {
  const ui = RL.hostUi({ isHost: true, recordingAvailable: true, allowShare: false, hostCapable: true });
  assert.equal(ui.host, true);
  assert.equal(ui.recordVisible, true);
  assert.equal(ui.screenVisible, true);   // host always
  assert.equal(ui.gearVisible, true);
  assert.equal(ui.hostActions, true);
  assert.equal(ui.reclaimVisible, false); // is host, hasn't stepped down
});

test('hostUi — attendee with sharing OFF', () => {
  const ui = RL.hostUi({ isHost: false, hostMeta: false, recordingAvailable: true, allowShare: false, recording: true, consentDecided: false });
  assert.equal(ui.host, false);
  assert.equal(ui.recordVisible, false);
  assert.equal(ui.screenVisible, false);  // attendee + sharing off → no screen button
  assert.equal(ui.gearVisible, false);
  assert.equal(ui.hostActions, false);
  assert.equal(ui.consentPrompt, true);   // recording + not decided + not host
});

test('hostUi — attendee with sharing ON sees the screen button', () => {
  assert.equal(RL.hostUi({ isHost: false, allowShare: true }).screenVisible, true);
});

test('hostUi — recordVisible needs recording AVAILABLE, not just host', () => {
  assert.equal(RL.hostUi({ isHost: true, recordingAvailable: false }).recordVisible, false);
});

test('hostUi — stepped-down owner: gear + reclaim, no active actions', () => {
  const ui = RL.hostUi({ isHost: false, hostMeta: false, hostCapable: true });
  assert.equal(ui.host, false);
  assert.equal(ui.gearVisible, true);
  assert.equal(ui.reclaimVisible, true);
  assert.equal(ui.hostActions, false);
});

test('hostUi — consent not re-prompted once decided', () => {
  assert.equal(RL.hostUi({ isHost: false, recording: true, consentDecided: true }).consentPrompt, false);
});

test('hostUi — host is never consent-prompted (their record click IS consent)', () => {
  assert.equal(RL.hostUi({ isHost: true, recording: true, consentDecided: false }).consentPrompt, false);
});

// ---- Translation: fmt / translate / EN / tokenErrorKey ----------------------------------------
const fs = require('node:fs');
const path = require('node:path');
const BookingLogic = require('./booking-logic.js');

test('fmt substitutes %s in order', () => {
  assert.equal(RL.fmt('Device %s', ['2']), 'Device 2');
  assert.equal(RL.fmt('%s (you)', ['Ana']), 'Ana (you)');
  assert.equal(RL.fmt('%s and %s', ['a', 'b']), 'a and b');
});

test('fmt honours indexed %[n]s, so a translation can reorder its arguments', () => {
  assert.equal(RL.fmt('%[2]s: %[1]s', ['Ana', 'Montag']), 'Montag: Ana');
  assert.equal(RL.fmt('%[1]s / %[1]s / %s', ['a', 'b']), 'a / a / a');
});

test('fmt leaves no format verb on screen when an argument is missing', () => {
  assert.equal(RL.fmt('Device %s', []), 'Device ');
  assert.equal(RL.fmt('Device %s'), 'Device ');
  assert.equal(RL.fmt('%[3]s missing', ['a']), ' missing');
});

test('fmt does not re-expand a verb that arrives inside an argument (names are user content)', () => {
  assert.equal(RL.fmt('%s (you)', ['50%s off']), '50%s off (you)');
});

test('fmt is a faithful mirror of BookingLogic.fmt (keep the copies in step)', () => {
  const cases = [
    ['Device %s', ['1']], ['%s (tú)', ['Ana']], ['%[2]s: %[1]s', ['x', 'y']],
    ['%[1]s / %[1]s / %s', ['a', 'b']], ['%s %s', ['only']], ['no verbs', ['unused']],
    ['%s', [null]], ['%s', [undefined]], ['%s', [0]], ['%[3]s', ['a']], ['Inga', undefined],
    ['100%% and %d stay', ['z']]
  ];
  for (const [tpl, args] of cases) assert.equal(RL.fmt(tpl, args), BookingLogic.fmt(tpl, args), tpl);
});

test('translate — the page table wins, then substitution', () => {
  const es = { room_joining: 'Uniéndote…', room_tile_you: '%s (tú)', room_device_fallback: 'Dispositivo %s' };
  assert.equal(RL.translate(es, 'room_joining'), 'Uniéndote…');
  assert.equal(RL.translate(es, 'room_tile_you', ['Ana']), 'Ana (tú)');
  assert.equal(RL.translate(es, 'room_device_fallback', ['2']), 'Dispositivo 2');
});

test('translate — a translation may reorder with %[n]s', () => {
  assert.equal(RL.translate({ room_tile_you: '(du) %[1]s' }, 'room_tile_you', ['Ana']), '(du) Ana');
});

test('translate — a missing key or table falls back to English, never the raw key', () => {
  assert.equal(RL.translate({}, 'room_joining'), 'Joining…');
  assert.equal(RL.translate(undefined, 'room_error_connect'), 'Could not connect to the meeting server.');
  assert.equal(RL.translate(null, 'room_tile_you', ['Ana']), 'Ana (you)');
  assert.equal(RL.translate({ room_joining: '' }, 'room_joining'), 'Joining…'); // empty = missing
  assert.equal(RL.translate({ room_joining: 42 }, 'room_joining'), 'Joining…'); // not a string
  for (const key of Object.keys(RL.EN)) {
    const shown = RL.translate({}, key, ['X']);
    assert.ok(!/room_/.test(shown), key + ' fell through to the raw key');
    assert.ok(!/%(\[\d+\])?s/.test(shown), key + ' left a format verb on screen');
  }
});

test('translate — an unknown key (a bug) comes back as itself rather than throwing', () => {
  assert.equal(RL.translate({}, 'room_not_a_real_key'), 'room_not_a_real_key');
});

test('translate — inherited object keys are not mistaken for strings', () => {
  assert.equal(RL.translate({}, 'toString'), 'toString');
  assert.equal(RL.translate({}, 'constructor'), 'constructor');
});

test('tokenErrorKey — a translated message from the status, never the server text', () => {
  assert.equal(RL.tokenErrorKey(403), 'room_error_link_invalid');   // bad / expired / malformed link
  assert.equal(RL.tokenErrorKey(404), 'room_error_not_configured'); // LiveKit off on this instance
  assert.equal(RL.tokenErrorKey(400), 'room_error_token');
  assert.equal(RL.tokenErrorKey(500), 'room_error_token');
  assert.equal(RL.tokenErrorKey(502), 'room_error_token');          // proxy HTML page
  assert.equal(RL.tokenErrorKey(200), 'room_error_token');          // 200 with unreadable JSON
  assert.equal(RL.tokenErrorKey(undefined), 'room_error_token');    // network failure
  for (const s of [400, 403, 404, 500, undefined]) assert.ok(RL.tokenErrorKey(s) in RL.EN);
});

test('EN — only the two name/number keys carry a verb, exactly one %s each; no literal %', () => {
  for (const [key, val] of Object.entries(RL.EN)) {
    const verbs = (val.match(/%/g) || []).length;
    const expected = key === 'room_device_fallback' || key === 'room_tile_you' ? 1 : 0;
    assert.equal(verbs, expected, key);
    if (expected) assert.match(val, /%s/, key);
  }
});

test('EN covers every room_ key livekit-room.js uses, and nothing it does not', () => {
  const src = fs.readFileSync(path.join(__dirname, 'livekit-room.js'), 'utf8');
  const used = new Set(Array.from(src.matchAll(/['"](room_[a-z_]+)['"]/g), (m) => m[1]));
  assert.ok(used.size > 20, 'scan found too few keys: ' + used.size);
  // The join error keys reach livekit-room.js through RoomLogic.tokenErrorKey, not as literals.
  assert.match(src, /t\(RoomLogic\.tokenErrorKey\(/);
  for (const s of [403, 404, 500]) used.add(RL.tokenErrorKey(s));
  for (const key of used) assert.ok(Object.prototype.hasOwnProperty.call(RL.EN, key), 'no English fallback for ' + key);
  for (const key of Object.keys(RL.EN)) assert.ok(used.has(key), 'EN has ' + key + ' but livekit-room.js never uses it');
});

test('no translated text is written as HTML (textContent / attributes only)', () => {
  // A translation is data from a JSON file, not markup: it must never be concatenated into an
  // innerHTML / outerHTML / insertAdjacentHTML string (the host badge used to be, in English).
  const src = fs.readFileSync(path.join(__dirname, 'livekit-room.js'), 'utf8');
  const html = src.split('\n').filter((l) => /\b(innerHTML|outerHTML)\s*\+?=|insertAdjacentHTML\s*\(/.test(l));
  assert.ok(html.length > 5, 'scan found too few HTML writes: ' + html.length);
  for (const line of html) assert.doesNotMatch(line, /\bt\(|RoomLogic\.(translate|fmt)\(|I18N\b/, line.trim());
});

test('EN matches internal/i18n/locales/en.json (the fallback must not drift from the source)', (ctx) => {
  const en = JSON.parse(fs.readFileSync(path.join(__dirname, '..', '..', 'i18n', 'locales', 'en.json'), 'utf8'));
  if (!Object.keys(en).some((k) => k.startsWith('room_'))) {
    ctx.skip('en.json has no room_ keys yet'); // strict as soon as the room keys land
    return;
  }
  for (const [key, val] of Object.entries(RL.EN)) {
    assert.ok(key in en, 'en.json is missing ' + key);
    assert.equal(val, en[key], key);
  }
});

// ---- Smoke: the REAL served asset (room-logic.js + "\n" + livekit-room.js, as livekit_room.go
// concatenates it) run against a minimal fake DOM, so t() is exercised in the actual code paths.
const vm = require('node:vm');
const ES = {
  room_error_library: 'No se pudo cargar el módulo de video.',
  room_error_missing_token: 'A este enlace de la reunión le falta el código de acceso.',
  room_error_link_invalid: 'Este enlace de la reunión no es válido o ha caducado.',
  room_error_token: 'No se pudo obtener el acceso a la reunión.',
  room_error_not_configured: 'Las videollamadas no están disponibles en este sitio.',
  room_toggle_camera_on: 'Cámara activada', room_toggle_camera_off: 'Cámara desactivada',
  room_toggle_mic_on: 'Micrófono activado', room_toggle_mic_off: 'Micrófono desactivado',
  room_camera_unavailable: 'Cámara no disponible', room_preview_camera_off: 'Cámara apagada',
  room_joining: 'Uniéndote…'
};
function runRoom({ lang = 'es', table, search = '?t=abc', LivekitClient, fetch }) {
  const els = {};
  const el = (id) => els[id] || (els[id] = {
    id, textContent: '', value: '', disabled: false, title: '', attrs: {},
    classList: {
      s: new Set(['hidden']),
      add(c) { this.s.add(c); }, remove(c) { this.s.delete(c); }, contains(c) { return this.s.has(c); },
      toggle(c, on) { if (on === undefined) on = !this.s.has(c); if (on) this.s.add(c); else this.s.delete(c); return on; }
    },
    setAttribute(k, v) { this.attrs[k] = v; }, appendChild() {}, focus() {}
  });
  let n = 0;
  const ctx = {
    document: { documentElement: { lang }, readyState: 'complete', getElementById: el,
      addEventListener() {}, createElement: () => el('_new' + n++) },
    location: { search }, URLSearchParams, fetch,
    localStorage: { getItem: () => null, setItem() {} },
    navigator: { mediaDevices: { enumerateDevices: async () => [] } }
  };
  ctx.self = ctx; ctx.window = ctx;
  if (table) ctx.__CALNODE_I18N = table;
  if (LivekitClient) ctx.LivekitClient = LivekitClient;
  vm.createContext(ctx);
  const code = fs.readFileSync(path.join(__dirname, 'room-logic.js'), 'utf8') + '\n' +
    fs.readFileSync(path.join(__dirname, 'livekit-room.js'), 'utf8');
  vm.runInContext(code, ctx);
  return els;
}
const flush = async () => { for (let i = 0; i < 5; i++) await new Promise((r) => setImmediate(r)); };
const failingCamera = () => ({ createLocalVideoTrack: async () => { throw new Error('NotAllowedError'); } });

test('asset smoke — SDK missing: the library error, translated (and English with no table)', () => {
  let els = runRoom({ table: ES });
  assert.equal(els['lk-error-msg'].textContent, ES.room_error_library);
  assert.equal(els['lk-error'].classList.contains('hidden'), false);
  els = runRoom({ lang: 'en' });
  assert.equal(els['lk-error-msg'].textContent, 'Video library failed to load.');
});

test('asset smoke — link without ?t: the missing-token error, translated', () => {
  const els = runRoom({ table: ES, search: '', LivekitClient: failingCamera() });
  assert.equal(els['lk-error-msg'].textContent, ES.room_error_missing_token);
});

test('asset smoke — prejoin toggles + camera overlay, and the overlay never keeps stale "unavailable"', async () => {
  let calls = 0;
  const track = { attach() {}, detach() {}, stop() {} };
  const LivekitClient = { createLocalVideoTrack: async () => { if (calls++ === 0) throw new Error('busy'); return track; } };
  const els = runRoom({ table: ES, LivekitClient });
  await flush();
  assert.equal(els['lk-pre-mic'].textContent, 'Micrófono activado');
  assert.equal(els['lk-pre-cam'].textContent, 'Cámara desactivada');      // first attempt failed
  assert.equal(els['lk-preview-off'].textContent, 'Cámara no disponible');
  els['lk-pre-cam'].onclick(); await flush();                              // retry succeeds
  assert.equal(els['lk-pre-cam'].textContent, 'Cámara activada');
  assert.equal(els['lk-preview-off'].classList.contains('hidden'), true);
  els['lk-pre-cam'].onclick(); await flush();                              // switched off by the user
  assert.equal(els['lk-pre-cam'].textContent, 'Cámara desactivada');
  assert.equal(els['lk-preview-off'].textContent, 'Cámara apagada');       // not "no disponible"
  els['lk-pre-mic'].onclick();
  assert.equal(els['lk-pre-mic'].textContent, 'Micrófono desactivado');
});

async function joinWith(fetch) {
  const els = runRoom({ table: ES, LivekitClient: failingCamera(), fetch });
  await flush();
  els['lk-name'].value = 'Ana';
  const joining = els['lk-join'].onclick();
  assert.equal(els['lk-join'].textContent, ES.room_joining);
  await joining;
  return els['lk-error-msg'].textContent;
}
const reply = (status, body) => async () => ({ ok: status >= 200 && status < 300, status, json: async () => body });

test('asset smoke — join errors: translated by status, never the server or browser text', async () => {
  assert.equal(await joinWith(reply(403, { error: 'livekit: bad room token signature' })), ES.room_error_link_invalid);
  assert.equal(await joinWith(reply(403, { error: 'livekit: this meeting link has expired' })), ES.room_error_link_invalid);
  assert.equal(await joinWith(reply(404, { error: 'video meetings are not configured' })), ES.room_error_not_configured);
  assert.equal(await joinWith(reply(500, { error: 'could not create a meeting token' })), ES.room_error_token);
  assert.equal(await joinWith(async () => ({ ok: false, status: 502, json: async () => { throw new SyntaxError('Unexpected token <'); } })), ES.room_error_token);
  assert.equal(await joinWith(async () => ({ ok: true, status: 200, json: async () => { throw new SyntaxError('Unexpected token <'); } })), ES.room_error_token);
  assert.equal(await joinWith(async () => { throw new TypeError('Failed to fetch'); }), ES.room_error_token);
});
