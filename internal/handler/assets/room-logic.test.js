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

// Every message RoomLogic.mediaProblem can pick (checked against the function below).
const MEDIA_KEYS = ['room_media_denied_ios', 'room_media_denied', 'room_media_busy', 'room_media_not_found',
  'room_media_unsupported', 'room_media_inapp', 'room_media_failed'];

test('EN covers every room_ key livekit-room.js uses, and nothing it does not', () => {
  const src = fs.readFileSync(path.join(__dirname, 'livekit-room.js'), 'utf8');
  const used = new Set(Array.from(src.matchAll(/['"](room_[a-z_]+)['"]/g), (m) => m[1]));
  assert.ok(used.size > 20, 'scan found too few keys: ' + used.size);
  // The join error keys reach livekit-room.js through RoomLogic.tokenErrorKey, not as literals.
  assert.match(src, /t\(RoomLogic\.tokenErrorKey\(/);
  for (const s of [403, 404, 500]) used.add(RL.tokenErrorKey(s));
  assert.match(src, /RoomLogic\.mediaProblem\(/);
  for (const k of MEDIA_KEYS) used.add(k);
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
function runRoom({ lang = 'es', table, search = '?t=abc', LivekitClient, fetch, nav = {} }) {
  const els = {};
  const el = (id) => els[id] || (els[id] = {
    id, textContent: '', value: '', disabled: false, title: '', attrs: {},
    classList: {
      s: new Set(['hidden']),
      add(c) { this.s.add(c); }, remove(c) { this.s.delete(c); }, contains(c) { return this.s.has(c); },
      toggle(c, on) { if (on === undefined) on = !this.s.has(c); if (on) this.s.add(c); else this.s.delete(c); return on; }
    },
    setAttribute(k, v) { this.attrs[k] = v; }, removeAttribute(k) { delete this.attrs[k]; },
    appendChild() {}, focus() {}, remove() {}, addEventListener() {}, contains() { return false; }, select() {},
    style: {}, kids: {},
    querySelector(sel) { return this.kids[sel] || (this.kids[sel] = el(this.id + ' ' + sel)); }
  });
  let n = 0;
  const ctx = {
    document: { documentElement: { lang }, readyState: 'complete', getElementById: el,
      addEventListener() {}, createElement: (tag) => Object.assign(el('_new' + n++), { tag }),
      body: { appended: [], appendChild(x) { this.appended.push(x); } } },
    location: { search, href: 'https://agenda.example/room?t=abc' }, URLSearchParams, fetch, setTimeout,
    localStorage: { getItem: () => null, setItem() {} },
    navigator: Object.assign({ mediaDevices: { enumerateDevices: async () => [] } }, nav)
  };
  els.__ctx = ctx;
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
const fakeTrack = (kind) => ({ kind, attachedElements: [], attach(e) { if (!this.attachedElements.includes(e)) this.attachedElements.push(e); e.srcObject = {}; }, detach() { const a = this.attachedElements; this.attachedElements = []; return a; }, stop() { this.stopped = true; } });
const failingCamera = () => ({
  createLocalVideoTrack: async () => { throw new Error('NotAllowedError'); },
  createLocalAudioTrack: async () => fakeTrack('audio')
});

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
  const LivekitClient = {
    createLocalVideoTrack: async () => { if (calls++ === 0) throw new Error('busy'); return track; },
    createLocalAudioTrack: async () => fakeTrack('audio')
  };
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

// ---- iPhone camera / playback: pure helpers -------------------------------------------------
const UA_IPHONE_SAFARI = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1';
const UA_IPHONE_CHROME = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/126.0.6478.54 Mobile/15E148 Safari/604.1';
const UA_IPAD_AS_MAC = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15';
const UA_ANDROID = 'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36';
const UA_INSTAGRAM = UA_IPHONE_SAFARI.replace('Safari/604.1', 'Instagram 340.0.2.18.84 (iPhone15,3; iOS 17_5; es_US; es; scale=3.00; 1290x2796)');
const UA_FACEBOOK = UA_IPHONE_SAFARI + ' [FBAN/FBIOS;FBAV/470.0.0.40.98;FBBV/600000000;FBDV/iPhone15,3]';

test('isIOS — every iPhone browser (WebKit underneath), and an iPad posing as a Mac', () => {
  assert.equal(RL.isIOS(UA_IPHONE_SAFARI), true);
  assert.equal(RL.isIOS(UA_IPHONE_CHROME), true);   // CriOS: the SDK calls it "Chrome", it is WebKit
  assert.equal(RL.isIOS(UA_INSTAGRAM), true);
  assert.equal(RL.isIOS(UA_IPAD_AS_MAC, 'MacIntel', 5), true);
  assert.equal(RL.isIOS(UA_IPAD_AS_MAC, 'MacIntel', 0), false); // a real Mac
  assert.equal(RL.isIOS(UA_ANDROID, 'Linux armv8l', 5), false);
  assert.equal(RL.isIOS(undefined), false);
});

test('inAppBrowser — the in-app views that block the camera; real browsers are not flagged', () => {
  assert.equal(RL.inAppBrowser(UA_INSTAGRAM), 'instagram');
  assert.equal(RL.inAppBrowser(UA_FACEBOOK), 'facebook');
  assert.equal(RL.inAppBrowser(UA_IPHONE_SAFARI + ' WhatsApp/2.24.1'), 'whatsapp');
  assert.equal(RL.inAppBrowser(UA_IPHONE_SAFARI), '');
  assert.equal(RL.inAppBrowser(UA_IPHONE_CHROME), '');
  assert.equal(RL.inAppBrowser(UA_ANDROID), '');
  assert.equal(RL.inAppBrowser(''), '');
});

test('mediaProblem — a refusal: Safari settings + reload on iPhone, open-in-Safari in an app', () => {
  const denied = { name: 'NotAllowedError', message: 'The request is not allowed by the user agent' };
  assert.deepEqual(RL.mediaProblem(denied, { ios: true }), { key: 'room_media_denied_ios', action: 'reload' });
  assert.deepEqual(RL.mediaProblem(denied, { ios: false }), { key: 'room_media_denied', action: 'reload' });
  assert.deepEqual(RL.mediaProblem(denied, { ios: true, inApp: true }), { key: 'room_media_inapp', action: 'none' });
  assert.equal(RL.mediaProblem({ name: 'SecurityError' }, {}).key, 'room_media_denied');
  assert.equal(RL.mediaProblem({ name: 'PermissionDeniedError' }, {}).key, 'room_media_denied');
  // A plain Error carrying the refusal in its message (some wrappers do that).
  assert.equal(RL.mediaProblem(new Error('Permission denied'), {}).key, 'room_media_denied');
});

test('mediaProblem — busy, missing, unsupported and unknown failures', () => {
  assert.deepEqual(RL.mediaProblem({ name: 'NotReadableError' }, { ios: true }), { key: 'room_media_busy', action: 'again' });
  assert.deepEqual(RL.mediaProblem({ name: 'AbortError' }, {}), { key: 'room_media_busy', action: 'again' });
  assert.deepEqual(RL.mediaProblem({ name: 'NotFoundError' }, {}), { key: 'room_media_not_found', action: 'again' });
  assert.deepEqual(RL.mediaProblem({ name: 'OverconstrainedError' }, {}), { key: 'room_media_not_found', action: 'again' });
  // No navigator.mediaDevices: the SDK throws reading getUserMedia of undefined.
  const noApi = new TypeError("Cannot read properties of undefined (reading 'getUserMedia')");
  assert.deepEqual(RL.mediaProblem(noApi, {}), { key: 'room_media_unsupported', action: 'none' });
  assert.deepEqual(RL.mediaProblem(noApi, { inApp: true }), { key: 'room_media_inapp', action: 'none' });
  assert.deepEqual(RL.mediaProblem(new Error('busy'), {}), { key: 'room_media_failed', action: 'again' });
  assert.equal(RL.mediaProblem(null, {}), null);
  assert.equal(RL.mediaProblem(undefined), null);
});

test('mediaProblem — every key it can return has English text (and MEDIA_KEYS lists exactly them)', () => {
  const seen = new Set();
  const errs = [{ name: 'NotAllowedError' }, { name: 'NotReadableError' }, { name: 'NotFoundError' }, new TypeError('x'), new Error('?')];
  for (const err of errs) for (const ios of [true, false]) for (const inApp of [true, false]) {
    const p = RL.mediaProblem(err, { ios, inApp });
    assert.ok(['again', 'reload', 'none'].includes(p.action), p.action);
    assert.ok(Object.prototype.hasOwnProperty.call(RL.EN, p.key), p.key);
    seen.add(p.key);
  }
  assert.deepEqual([...seen].sort(), [...MEDIA_KEYS].sort());
});

test('captureOptions — front camera unless a device was picked; never an exact deviceId', () => {
  assert.deepEqual(RL.captureOptions('video', false, 'back-triple-camera'), { facingMode: 'user' }); // list default ignored
  assert.deepEqual(RL.captureOptions('video', true, 'cam-2'), { deviceId: 'cam-2' });
  assert.deepEqual(RL.captureOptions('video', true, ''), { facingMode: 'user' });
  assert.deepEqual(RL.captureOptions('audio', false, 'mic-1'), {});
  assert.deepEqual(RL.captureOptions('audio', true, 'mic-1'), { deviceId: 'mic-1' });
});

// ---- acquireTracks: camera + mic in one request, each on its own when that fails ----------
const tr = (kind) => ({ kind });
const named = (name) => Object.assign(new Error(name), { name });
function maker({ both, video, audio }) {
  const calls = [];
  const wrap = (k, f) => f && (() => { calls.push(k); return f(); });
  return { calls, make: { both: wrap('both', both), video: wrap('video', video), audio: wrap('audio', audio) } };
}

test('acquireTracks — both granted: one request, no per-device calls', async () => {
  const m = maker({ both: async () => [tr('video'), tr('audio')], video: async () => tr('video'), audio: async () => tr('audio') });
  const got = await RL.acquireTracks({ video: true, audio: true }, m.make);
  assert.deepEqual(m.calls, ['both']);
  assert.equal(got.video.kind, 'video'); assert.equal(got.audio.kind, 'audio');
  assert.deepEqual(got.errs, { video: null, audio: null });
});

test('acquireTracks — camera blocked, mic allowed: the mic still comes through (the combined request fails whole)', async () => {
  const m = maker({
    both: async () => { throw named('NotAllowedError'); },
    video: async () => { throw named('NotAllowedError'); },
    audio: async () => tr('audio')
  });
  const got = await RL.acquireTracks({ video: true, audio: true }, m.make);
  assert.deepEqual(m.calls, ['both', 'video', 'audio']);
  assert.equal(got.video, null); assert.equal(got.errs.video.name, 'NotAllowedError');
  assert.equal(got.audio.kind, 'audio'); assert.equal(got.errs.audio, null);
});

test('acquireTracks — mic blocked, camera allowed: the camera still comes through', async () => {
  const m = maker({
    both: async () => { throw named('NotAllowedError'); },
    video: async () => tr('video'),
    audio: async () => { throw named('NotAllowedError'); }
  });
  const got = await RL.acquireTracks({ video: true, audio: true }, m.make);
  assert.equal(got.video.kind, 'video'); assert.equal(got.errs.video, null);
  assert.equal(got.audio, null); assert.equal(got.errs.audio.name, 'NotAllowedError');
});

test('acquireTracks — both refused: both errors kept, never a rejection', async () => {
  const m = maker({ both: async () => { throw named('NotAllowedError'); }, video: async () => { throw named('NotAllowedError'); }, audio: async () => { throw named('NotAllowedError'); } });
  const got = await RL.acquireTracks({ video: true, audio: true }, m.make);
  assert.equal(got.errs.video.name, 'NotAllowedError'); assert.equal(got.errs.audio.name, 'NotAllowedError');
});

test('acquireTracks — only one wanted: no combined request; a missing combined API falls back per device', async () => {
  let m = maker({ both: async () => [tr('video'), tr('audio')], video: async () => tr('video'), audio: async () => tr('audio') });
  let got = await RL.acquireTracks({ video: true, audio: false }, m.make);
  assert.deepEqual(m.calls, ['video']); assert.equal(got.audio, null);
  m = maker({ video: async () => tr('video'), audio: async () => tr('audio') }); // no `both`
  got = await RL.acquireTracks({ video: true, audio: true }, m.make);
  assert.deepEqual(m.calls, ['video', 'audio']);
  assert.equal(got.video.kind, 'video'); assert.equal(got.audio.kind, 'audio');
});

test('acquireTracks — the combined request returns only one device: the other is asked for on its own', async () => {
  const m = maker({ both: async () => [tr('video')], video: async () => tr('video'), audio: async () => tr('audio') });
  const got = await RL.acquireTracks({ video: true, audio: true }, m.make);
  assert.deepEqual(m.calls, ['both', 'audio']);
  assert.equal(got.audio.kind, 'audio');
});

test('acquireTracks — a synchronous throw from the SDK is caught too', async () => {
  const m = maker({ both: () => { throw new TypeError('no mediaDevices'); }, video: () => { throw new TypeError('no mediaDevices'); }, audio: async () => tr('audio') });
  const got = await RL.acquireTracks({ video: true, audio: true }, m.make);
  assert.equal(got.errs.video.name, 'TypeError'); assert.equal(got.audio.kind, 'audio');
});

test('controlOn — the prejoin choice while publishing, the real state after', () => {
  assert.equal(RL.controlOn(true, true, false), true);   // publishing: shows ON, does not invite a 2nd camera
  assert.equal(RL.controlOn(true, false, false), false);
  assert.equal(RL.controlOn(false, true, false), false); // settled: what the SDK has (publish failed)
  assert.equal(RL.controlOn(false, false, true), true);
});

test('hostUi — no screen-share button where the browser cannot share (iPhone), even for the host', () => {
  assert.equal(RL.hostUi({ isHost: true, screenSupported: false }).screenVisible, false);
  assert.equal(RL.hostUi({ isHost: false, allowShare: true, screenSupported: false }).screenVisible, false);
  assert.equal(RL.hostUi({ isHost: true, screenSupported: true }).screenVisible, true);
  assert.equal(RL.hostUi({ isHost: true }).screenVisible, true); // unknown = supported (old callers)
});

test('playGate — shows while remote video/audio or one of our videos is blocked', () => {
  assert.equal(RL.playGate({ canVideo: true, canAudio: true, localBlocked: false }), false);
  assert.equal(RL.playGate({ canVideo: false, canAudio: true }), true);   // iPhone Low Power Mode
  assert.equal(RL.playGate({ canVideo: true, canAudio: false }), true);
  assert.equal(RL.playGate({ canVideo: true, canAudio: true, localBlocked: true }), true);
  assert.equal(RL.playGate({}), false); // unknown = not blocked
});

// ---- iPhone camera / playback: the real asset against a fake SDK ------------------------------
// A minimal LivekitClient: tracks that record what they were attached to, and a Room whose
// connect() brings one participant already in the meeting (the mentor), with a camera and a mic.
function fakeSDK({ publishHangs = false, publishWait = null, tracksError = null } = {}) {
  const calls = { createLocalTracks: [], createLocalVideoTrack: [], publishTrack: [], setCameraEnabled: [], setMicrophoneEnabled: [], startVideo: 0, startAudio: 0 };
  const remoteVideo = fakeTrack('video'), remoteAudio = fakeTrack('audio');
  const mentor = {
    identity: 'mentor', name: 'Mentor', isLocal: false, isMicrophoneEnabled: true, metadata: 'host',
    trackPublications: new Map([['TR_v', { track: remoteVideo }], ['TR_a', { track: remoteAudio }]])
  };
  const rooms = [];
  class Room {
    constructor(opts) {
      this.opts = opts; this.handlers = {}; this.metadata = ''; this.canPlaybackVideo = true; this.canPlaybackAudio = true;
      this.remoteParticipants = new Map();
      this.localParticipant = {
        identity: 'me', metadata: '', isMicrophoneEnabled: false, isCameraEnabled: false, isScreenShareEnabled: false,
        publishTrack: (tr) => { calls.publishTrack.push(tr); return publishHangs ? new Promise(() => {}) : publishWait || Promise.resolve(); },
        setCameraEnabled: async (on, o) => { calls.setCameraEnabled.push([on, o]); },
        setMicrophoneEnabled: async (on, o) => { calls.setMicrophoneEnabled.push([on, o]); }
      };
      rooms.push(this);
    }
    on(ev, fn) { this.handlers[ev] = fn; return this; }
    async connect() { this.remoteParticipants.set('mentor', mentor); }
    startVideo() { calls.startVideo++; return Promise.resolve(); }
    startAudio() { calls.startAudio++; return Promise.resolve(); }
    disconnect() {}
  }
  const LivekitClient = {
    Room,
    RoomEvent: new Proxy({}, { get: (_, k) => String(k) }),
    Track: { Source: { Camera: 'camera', ScreenShare: 'screen_share' } },
    createLocalTracks: async (opts) => {
      calls.createLocalTracks.push(opts);
      if (tracksError) throw tracksError;
      return [fakeTrack('video'), fakeTrack('audio')];
    },
    createLocalVideoTrack: async (opts) => { calls.createLocalVideoTrack.push(opts); if (tracksError) throw tracksError; return fakeTrack('video'); },
    createLocalAudioTrack: async () => { if (tracksError) throw tracksError; return fakeTrack('audio'); }
  };
  return { LivekitClient, calls, rooms, remoteVideo, remoteAudio, mentor };
}
const okToken = async () => ({ ok: true, status: 200, json: async () => ({ token: 'jwt', url: 'wss://lk.example', role: 'attendee' }) });

test('asset smoke — prejoin asks for camera AND mic in one request, front camera', async () => {
  const sdk = fakeSDK();
  const els = runRoom({ table: ES, LivekitClient: sdk.LivekitClient });
  await flush();
  assert.equal(sdk.calls.createLocalTracks.length, 1);
  assert.deepEqual(JSON.parse(JSON.stringify(sdk.calls.createLocalTracks[0])), { video: { facingMode: 'user' }, audio: {} });
  assert.equal(sdk.calls.createLocalVideoTrack.length, 0); // no second, separate camera request
  assert.equal(els['lk-pre-cam'].textContent, 'Cámara activada');
  assert.equal(els['lk-pre-mic'].textContent, 'Micrófono activado');
  assert.equal(els['lk-media-help'].classList.contains('hidden'), true);
});

test('asset smoke — iPhone refusal: plain instructions + a reload button (Safari will not ask twice)', async () => {
  const sdk = fakeSDK({ tracksError: Object.assign(new Error('denied'), { name: 'NotAllowedError' }) });
  const els = runRoom({ table: ES, LivekitClient: sdk.LivekitClient, nav: { userAgent: UA_IPHONE_SAFARI } });
  await flush();
  assert.equal(sdk.calls.createLocalTracks.length, 1);
  // Each device is then asked for on its own (a really denied one fails again at once, with no
  // second question), so a refusal of only one of them never costs the other.
  assert.equal(sdk.calls.createLocalVideoTrack.length, 1);
  assert.equal(els['lk-media-help'].classList.contains('hidden'), false);
  assert.equal(els['lk-media-help-msg'].textContent, RL.EN.room_media_denied_ios);
  assert.equal(els['lk-media-retry'].textContent, RL.EN.room_media_reload);
  assert.equal(els['lk-media-retry'].classList.contains('hidden'), false);
  assert.equal(els['lk-copy-link'].classList.contains('hidden'), true);
  assert.equal(els['lk-preview-off'].textContent, 'Cámara no disponible');
});

test('asset smoke — in-app browser: "open in Safari" + copy-link, no useless retry', async () => {
  const sdk = fakeSDK({ tracksError: Object.assign(new Error('denied'), { name: 'NotAllowedError' }) });
  const els = runRoom({ table: ES, LivekitClient: sdk.LivekitClient, nav: { userAgent: UA_INSTAGRAM } });
  await flush();
  assert.equal(els['lk-media-help-msg'].textContent, RL.EN.room_media_inapp);
  assert.equal(els['lk-media-retry'].classList.contains('hidden'), true);
  assert.equal(els['lk-copy-link'].classList.contains('hidden'), false);
});

test('asset smoke — join: people already here get tiles BEFORE our publish finishes; preview tracks are published, not re-acquired', async () => {
  const sdk = fakeSDK({ publishHangs: true }); // an iPhone whose publish never completes
  const els = runRoom({ table: ES, LivekitClient: sdk.LivekitClient, fetch: okToken });
  await flush();
  els['lk-name'].value = 'Ana';
  els['lk-join'].onclick(); // not awaited: it waits on the hung publish
  await flush();
  assert.equal(els['lk-room'].classList.contains('hidden'), false);
  // The mentor's video is attached to a tile even though our publish is still pending.
  assert.equal(sdk.remoteVideo.attachedElements.length, 1);
  const v = sdk.remoteVideo.attachedElements[0];
  assert.ok('playsinline' in v.attrs && 'muted' in v.attrs, 'tile video lacks playsinline/muted attributes');
  assert.ok(!('autoplay' in v.attrs), 'tile video must not carry the autoplay attribute');
  assert.equal(v.muted, true);
  // The buttons work already.
  assert.equal(typeof els['lk-mic-btn'].onclick, 'function');
  // The prejoin tracks went to publishTrack; the camera was not opened a second time.
  assert.deepEqual(sdk.calls.publishTrack.map((tr) => tr.kind).sort(), ['audio', 'video']);
  assert.equal(sdk.calls.setCameraEnabled.length, 0);
  assert.equal(sdk.calls.setMicrophoneEnabled.length, 0);
  assert.equal(sdk.calls.createLocalTracks.length, 1);
});

test('asset smoke — one <audio> per voice; a late video for an unknown participant still gets a tile', async () => {
  const sdk = fakeSDK();
  const els = runRoom({ table: ES, LivekitClient: sdk.LivekitClient, fetch: okToken });
  await flush();
  els['lk-name'].value = 'Ana';
  await els['lk-join'].onclick();
  const room = sdk.rooms[0];
  assert.equal(sdk.remoteAudio.attachedElements.length, 1);
  room.handlers.TrackSubscribed(sdk.remoteAudio, {}, sdk.mentor); // re-delivered: must not double the voice
  assert.equal(sdk.remoteAudio.attachedElements.length, 1);
  room.handlers.TrackUnsubscribed(sdk.remoteAudio, {}, sdk.mentor);
  assert.equal(sdk.remoteAudio.attachedElements.length, 0);
  const late = fakeTrack('video');
  room.handlers.TrackSubscribed(late, {}, { identity: 'nuevo', name: 'Nuevo', isLocal: false });
  assert.equal(late.attachedElements.length, 1);
});

test('asset smoke — blocked playback: the big tap button appears and starts video + audio inside the tap', async () => {
  const sdk = fakeSDK();
  const els = runRoom({ table: ES, LivekitClient: sdk.LivekitClient, fetch: okToken });
  await flush();
  els['lk-name'].value = 'Ana';
  await els['lk-join'].onclick();
  const room = sdk.rooms[0];
  assert.equal(els['lk-play-gate'].classList.contains('hidden'), true);
  room.canPlaybackVideo = false; room.handlers.VideoPlaybackStatusChanged(false); // Low Power Mode
  assert.equal(els['lk-play-gate'].classList.contains('hidden'), false);
  els['lk-play-gate'].onclick();
  assert.equal(sdk.calls.startVideo, 1);
  assert.equal(sdk.calls.startAudio, 1);
  room.canPlaybackVideo = true; room.handlers.VideoPlaybackStatusChanged(true);
  assert.equal(els['lk-play-gate'].classList.contains('hidden'), true);
  room.canPlaybackAudio = false; room.handlers.AudioPlaybackStatusChanged(false);
  assert.equal(els['lk-play-gate'].classList.contains('hidden'), false);
});

test('asset smoke — a camera that fails in the room says why (no silent catch)', async () => {
  const sdk = fakeSDK();
  const els = runRoom({ table: ES, LivekitClient: sdk.LivekitClient, fetch: okToken, nav: { userAgent: UA_IPHONE_SAFARI } });
  await flush();
  els['lk-name'].value = 'Ana';
  await els['lk-join'].onclick();
  const lp = sdk.rooms[0].localParticipant;
  lp.setCameraEnabled = async () => { throw Object.assign(new Error('in use'), { name: 'NotReadableError' }); };
  await els['lk-cam-btn'].onclick();
  assert.equal(els['lk-notice'].classList.contains('hidden'), false);
  assert.equal(els['lk-notice-msg'].textContent, RL.EN.room_media_busy);
  assert.equal(els['lk-notice-retry'].textContent, RL.EN.room_media_retry);
});

test('asset smoke — a camera tap while the join publish runs waits for it: no second camera, the tap still counts', async () => {
  let release; const publishWait = new Promise((r) => { release = r; });
  const sdk = fakeSDK({ publishWait });
  const els = runRoom({ table: ES, LivekitClient: sdk.LivekitClient, fetch: okToken, nav: { userAgent: UA_IPHONE_SAFARI } });
  await flush();
  els['lk-name'].value = 'Ana';
  const joining = els['lk-join'].onclick();
  await flush();
  const lp = sdk.rooms[0].localParticipant;
  // Still publishing: the buttons show the prejoin choice (ON), not an inviting "off".
  assert.equal(els['lk-cam-btn'].classList.contains('off'), false);
  assert.equal(els['lk-mic-btn'].classList.contains('off'), false);
  const tap = els['lk-cam-btn'].onclick();   // the person wants the camera OFF
  els['lk-cam-btn'].onclick();               // an impatient second tap is ignored
  await flush();
  assert.equal(sdk.calls.setCameraEnabled.length, 0, 'no camera request while the preview track is mid-publish');
  lp.isCameraEnabled = true; lp.isMicrophoneEnabled = true; // the preview tracks are now published
  release();
  await joining; await tap;
  assert.deepEqual(sdk.calls.setCameraEnabled.map((c) => c[0]), [false]); // switched off, once
  assert.equal(sdk.calls.setMicrophoneEnabled.length, 0);
});

test('asset smoke — camera off in the prejoin: a switch-on tap during the publish waits, then goes through the guarded SDK path once', async () => {
  let release; const publishWait = new Promise((r) => { release = r; });
  const sdk = fakeSDK({ publishWait });
  const els = runRoom({ table: ES, LivekitClient: sdk.LivekitClient, fetch: okToken });
  await flush();
  els['lk-pre-cam'].onclick(); await flush(); // camera off in the prejoin
  els['lk-name'].value = 'Ana';
  const joining = els['lk-join'].onclick();
  await flush();
  const lp = sdk.rooms[0].localParticipant;
  assert.equal(els['lk-cam-btn'].classList.contains('off'), true);
  const tap = els['lk-cam-btn'].onclick(); // wants it ON; it is switched on AFTER the publish, via the SDK's guarded path
  await flush();
  assert.equal(sdk.calls.setCameraEnabled.length, 0);
  lp.isMicrophoneEnabled = true;
  release();
  await joining; await tap;
  assert.deepEqual(sdk.calls.setCameraEnabled.map((c) => c[0]), [true]);
});

test('asset smoke — the Room gets no videoCaptureDefaults (they would override a picked camera)', async () => {
  const sdk = fakeSDK();
  const els = runRoom({ table: ES, LivekitClient: sdk.LivekitClient, fetch: okToken });
  await flush();
  els['lk-name'].value = 'Ana';
  await els['lk-join'].onclick();
  assert.equal(sdk.rooms[0].opts.videoCaptureDefaults, undefined);
});

test('asset smoke — no screen-share button on a browser without getDisplayMedia (every iPhone browser)', async () => {
  const sdk = fakeSDK();
  const host = async () => ({ ok: true, status: 200, json: async () => ({ token: 'jwt', url: 'wss://lk.example', role: 'host' }) });
  let els = runRoom({ table: ES, LivekitClient: sdk.LivekitClient, fetch: host, nav: { userAgent: UA_IPHONE_SAFARI } });
  await flush();
  els['lk-name'].value = 'Ana';
  await els['lk-join'].onclick();
  assert.equal(els['lk-screen'].classList.contains('hidden'), true);
  const sdk2 = fakeSDK();
  els = runRoom({ table: ES, LivekitClient: sdk2.LivekitClient, fetch: host,
    nav: { mediaDevices: { enumerateDevices: async () => [], getDisplayMedia: async () => ({}) } } });
  await flush();
  els['lk-name'].value = 'Ana';
  await els['lk-join'].onclick();
  assert.equal(els['lk-screen'].classList.contains('hidden'), false);
});
