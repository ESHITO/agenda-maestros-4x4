// Run: node --test internal/handler/assets/booking-logic.test.js
const test = require('node:test');
const assert = require('node:assert');
const B = require('./booking-logic.js');

test('dateKeyFromISO uses the SELECTED tz, not the host/browser tz', () => {
  // 02:00 UTC lands on different calendar days depending on the viewer's timezone.
  const iso = '2026-06-15T02:00:00Z';
  assert.equal(B.dateKeyFromISO(iso, 'Pacific/Auckland'), '2026-06-15'); // UTC+12 → 14:00 same day
  assert.equal(B.dateKeyFromISO(iso, 'America/New_York'), '2026-06-14'); // UTC-4 → 22:00 prev day
  assert.equal(B.dateKeyFromISO(iso, 'UTC'), '2026-06-15');
});

test('groupSlotsByDay buckets by tz-correct day and can exclude one slot', () => {
  const slots = [
    { start: '2026-06-15T02:00:00Z' }, // NY → 06-14
    { start: '2026-06-15T20:00:00Z' }, // NY → 06-15
    { start: '2026-06-15T21:00:00Z' }  // NY → 06-15
  ];
  const ny = B.groupSlotsByDay(slots, 'America/New_York');
  assert.deepEqual(Object.keys(ny).sort(), ['2026-06-14', '2026-06-15']);
  assert.equal(ny['2026-06-15'].length, 2);

  const excl = B.groupSlotsByDay(slots, 'America/New_York', '2026-06-15T20:00:00Z');
  assert.equal(excl['2026-06-15'].length, 1); // the excluded current-booking slot is dropped
});

test('dowIndex is Monday-first (0=Mon … 6=Sun)', () => {
  assert.equal(B.dowIndex(new Date(2026, 5, 15)), 0); // 2026-06-15 is a Monday
  assert.equal(B.dowIndex(new Date(2026, 5, 21)), 6); // Sunday
});

test('dowLabels is Monday-first and locale-aware', () => {
  assert.deepEqual(B.dowLabels('en'), ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su']);
  assert.deepEqual(B.dowLabels('es'), ['lu', 'ma', 'mi', 'ju', 'vi', 'sá', 'do']);
  // No locale passed → falls back to the runtime default rather than throwing.
  assert.equal(B.dowLabels().length, 7);
});

test('month helpers', () => {
  const d = new Date(2026, 5, 15); // June 2026
  assert.equal(B.startOfMonth(d).getDate(), 1);
  assert.equal(B.endOfMonth(d).getDate(), 30);
  assert.equal(B.addMonths(d, 1).getMonth(), 6);  // July
  assert.equal(B.addMonths(d, -6).getMonth(), 11); // prev Dec
  assert.equal(B.addMonths(d, -6).getFullYear(), 2025);
  assert.equal(B.daysInMonth(2024, 1), 29); // leap Feb
  assert.equal(B.daysInMonth(2026, 1), 28);
});


test('formatTime / formatDay respect tz', () => {
  const iso = '2026-06-15T02:00:00Z';
  assert.equal(B.formatTime(iso, 'UTC', 'en-US'), '2:00 AM');
  // NY (UTC-4) → prev day, June 14
  assert.match(B.formatDay(iso, 'America/New_York', 'short', 'en-US'), /Jun 14/);
  assert.match(B.formatDay(iso, 'America/New_York', 'long', 'en-US'), /June 14/);
});

test('mergeDaySlots interleaves taken slots in time order and tags them', () => {
  const free = [{ start: '2026-06-15T09:00:00Z' }, { start: '2026-06-15T11:00:00Z' }];
  const taken = [{ start: '2026-06-15T10:00:00Z' }];

  const merged = B.mergeDaySlots(free, taken);
  assert.deepEqual(merged.map((s) => s.start.slice(11, 16)), ['09:00', '10:00', '11:00']);
  assert.deepEqual(merged.map((s) => s.taken), [false, true, false]);
});

test('mergeDaySlots does not mutate the arrays it was given', () => {
  const free = [{ start: '2026-06-15T09:00:00Z' }];
  const taken = [{ start: '2026-06-15T10:00:00Z' }];
  B.mergeDaySlots(free, taken);
  assert.equal('taken' in free[0], false, 'the caller still holds the API response');
  assert.equal('taken' in taken[0], false);
});

test('mergeDaySlots handles a missing taken array (the opt-in is off)', () => {
  const free = [{ start: '2026-06-15T09:00:00Z' }];
  assert.deepEqual(B.mergeDaySlots(free, undefined).map((s) => s.taken), [false]);
  assert.deepEqual(B.mergeDaySlots(undefined, undefined), []);
});

test('fmt substitutes %s in order', () => {
  assert.equal(B.fmt('No available times on %s.', ['Monday, 15 June']), 'No available times on Monday, 15 June.');
  assert.equal(B.fmt('%s has no available times on %s.', ['Alex', 'Monday']), 'Alex has no available times on Monday.');
  assert.equal(B.fmt('Bookings must be made at least %s in advance.', ['4 hours']),
    'Bookings must be made at least 4 hours in advance.');
});

test('fmt honours indexed %[n]s, so a translation can reorder its arguments', () => {
  // German and Swedish put the date before the verb; the locale files are allowed to
  // reorder as long as the verbs match English (internal/i18n's parity test).
  assert.equal(B.fmt('%[2]s: %[1]s hat keine Termine.', ['Alex', 'Montag']), 'Montag: Alex hat keine Termine.');
  // An index may repeat an argument, and mixing forms keeps the sequential counter
  // independent of the indexed reads.
  assert.equal(B.fmt('%[1]s / %[1]s / %s', ['a', 'b']), 'a / a / a');
});

test('fmt leaves no format verb on screen when an argument is missing', () => {
  assert.equal(B.fmt('No available times on %s.', []), 'No available times on .');
  assert.equal(B.fmt('No available times on %s.'), 'No available times on .');
  assert.equal(B.fmt('%[3]s missing', ['a']), ' missing');
});

test('fmt leaves a string with no verbs untouched', () => {
  assert.equal(B.fmt('No available times.', ['unused']), 'No available times.');
  assert.equal(B.fmt('Inga lediga tider.'), 'Inga lediga tider.');
});

// ---- Phone questions ---------------------------------------------------------------------
// Against the real data the server embeds (phone-data.json), so a change to that table that
// breaks detection or the code split shows up here, not in a visitor's webhook.
const PHONE = require('./phone-data.json');
const KNOWN = new Set(PHONE.countries.map((c) => c[0]));
const TZ2CC = PHONE.tz2cc;
const regionNames = (locale) => {
  const dn = new Intl.DisplayNames([locale, 'en'], { type: 'region' });
  return (iso) => dn.of(iso);
};
const esName = regionNames('es');
const detect = (o) => B.phoneDetectCountry(Object.assign({ tz2cc: TZ2CC, known: KNOWN }, o));

test('phoneDetectCountry: the server hint wins when it is a known country', () => {
  assert.equal(detect({ hint: 'PE', timeZone: 'Europe/Madrid', languages: ['pt-BR'] }), 'PE');
  assert.equal(detect({ hint: 'pe' }), 'PE', 'case does not matter');
});

test('phoneDetectCountry: an unknown or placeholder hint is ignored, not trusted', () => {
  // XX (unknown) and T1 (Tor) are Cloudflare placeholders; ZZ and DD are not in the table.
  for (const hint of ['XX', 'T1', 'ZZ', 'DD', 'PER', '<b>']) {
    assert.equal(detect({ hint, timeZone: 'America/Lima' }), 'PE', hint);
  }
});

test('phoneDetectCountry: by browser time zone', () => {
  assert.equal(detect({ timeZone: 'America/Lima' }), 'PE');
  assert.equal(detect({ timeZone: 'Asia/Kolkata' }), 'IN');  // modern alias
  assert.equal(detect({ timeZone: 'Asia/Calcutta' }), 'IN'); // legacy name
  assert.equal(detect({ timeZone: 'Europe/Madrid', languages: ['pt-BR'] }), 'ES', 'zone beats language');
});

test('phoneDetectCountry: by the region of the browser languages', () => {
  // UTC is not in tz2cc, so the languages decide.
  assert.equal(detect({ timeZone: 'UTC', languages: ['pt-BR'] }), 'BR');
  assert.equal(detect({ languages: ['es-PE', 'en-US'] }), 'PE', 'the first one with a region');
  assert.equal(detect({ languages: ['es', 'es-MX'] }), 'MX', 'a bare language names no country');
  assert.equal(detect({ languages: ['es-419', 'es-AR'] }), 'AR', '419 is an area, not a country');
  assert.equal(detect({ languages: ['zh-Hant-TW'] }), 'TW', 'the script subtag is skipped');
  assert.equal(detect({ languages: ['en_GB'] }), 'GB', 'underscore form');
  assert.equal(detect({ languages: ['en-XA', 'fr-CA'] }), 'CA', 'a region outside the list is skipped');
  assert.equal(detect({ languages: 'pt-BR' }), 'BR', 'a single string works too');
  assert.equal(detect({ languages: ['de'] }), '');
});

test('phoneDetectCountry: a zone mapped to a code outside the list falls through', () => {
  const tz2cc = { 'Europe/Berlin': 'DD' }; // an obsolete code, as some tables still carry
  const known = new Set(['DE']);
  assert.equal(B.phoneDetectCountry({ timeZone: 'Europe/Berlin', languages: ['de-DE'], tz2cc, known }), 'DE');
  assert.equal(B.phoneDetectCountry({ timeZone: 'Europe/Berlin', languages: ['de'], tz2cc, known }), '');
});

test('phoneDetectCountry: nothing to go on gives no country, never a fixed default', () => {
  assert.equal(B.phoneDetectCountry(), '');
  assert.equal(B.phoneDetectCountry({}), '');
  assert.equal(detect({ hint: '', timeZone: '', languages: [] }), '');
  assert.equal(detect({ timeZone: 'constructor' }), '', 'no prototype keys from tz2cc');
  // Without `known` nothing can be offered, so nothing is guessed.
  assert.equal(B.phoneDetectCountry({ hint: 'PE', timeZone: 'America/Lima', tz2cc: TZ2CC }), '');
  // An array of ISO codes works as `known` as well as a Set.
  assert.equal(B.phoneDetectCountry({ hint: 'PE', known: ['PE', 'US'] }), 'PE');
});

test('phoneCombine: "+<dial> <digits>", separators dropped', () => {
  assert.equal(B.phoneCombine('51', '987 654-321'), '+51 987654321');
  assert.equal(B.phoneCombine('51', '(987) 654.321'), '+51 987654321');
  assert.equal(B.phoneCombine('1', '809-555-1234'), '+1 8095551234');
  assert.equal(B.phoneCombine('+51', '987654321'), '+51 987654321', 'a dial given with its + too');
});

test('phoneCombine: no digits is no answer', () => {
  for (const raw of ['', '   ', '-', '()', '+', 'abc', null, undefined]) {
    assert.equal(B.phoneCombine('51', raw), '', JSON.stringify(raw));
  }
  assert.equal(B.phoneCombine('51', '+51'), '', 'just the code is not a number');
});

test('phoneCombine: a number already starting with +<dial> does not get the code twice', () => {
  assert.equal(B.phoneCombine('51', '+51 987 654 321'), '+51 987654321');
  assert.equal(B.phoneCombine('51', '+51987654321'), '+51 987654321');
  assert.equal(B.phoneCombine('51', '  +51 (987) 654-321'), '+51 987654321');
  assert.equal(B.phoneCombine('1', '+1 (809) 555-1234'), '+1 8095551234');
  // Without the '+' the digits are national: "51…" can be a Puno landline, so it stays.
  assert.equal(B.phoneCombine('51', '51 234567'), '+51 51234567');
});

test('phoneCombine: a pasted international number keeps its own code over the picker', () => {
  assert.equal(B.phoneCombine('51', '+34 612 34 56 78'), '+34 612345678');
  assert.equal(B.phoneCombine('51', '+1 809 555 1234'), '+1 8095551234');
  assert.equal(B.phoneCombine('34', '+598 94 123 456'), '+598 94123456');
});

test('phoneCombine: no country picked is never "+ 987…" nor bare digits', () => {
  assert.equal(B.phoneCombine('', '987 654 321'), '');
  assert.equal(B.phoneCombine(undefined, '987 654 321'), '');
  assert.equal(B.phoneCombine('', '+44 7700 900123'), '+44 7700900123', 'unless the number says it');
});

test('phoneCombine: digits from Arabic, Persian and full-width keyboards are digits', () => {
  assert.equal(B.phoneCombine('966', '٥٥١ ٢٣٤ ٥٦٧'), '+966 551234567');   // Arabic-Indic
  assert.equal(B.phoneCombine('98', '۹۱۲ ۳۴۵ ۶۷۸۹'), '+98 9123456789');   // Persian
  assert.equal(B.phoneCombine('51', '９８７ ６５４ ３２１'), '+51 987654321'); // full-width
  assert.equal(B.phoneCombine('51', '＋５１ ９８７６５４３２１'), '+51 987654321');
});

test('phoneCombine splits every calling code in phone-data.json correctly', () => {
  for (const [iso, dial] of PHONE.countries) {
    const want = '+' + dial + ' 612345678';
    assert.equal(B.phoneCombine('', '+' + dial + '612345678'), want, iso + ' pasted, no country');
    assert.equal(B.phoneCombine(dial, '+' + dial + ' 612345678'), want, iso + ' pasted, same country');
    assert.equal(B.phoneCombine(dial, '612 345 678'), want, iso + ' national');
    // What reaches the webhook always has the one shape the server's validPhone accepts.
    assert.match(want, /^\+\d{1,3} \d+$/);
  }
});

test('phoneFilter: by localized name, ignoring case and accents', () => {
  const iso = (q) => B.phoneFilter(PHONE.countries, q, esName).map((c) => c[0]);
  assert.deepEqual(iso('perú'), ['PE']);
  assert.deepEqual(iso('peru'), ['PE']);
  assert.deepEqual(iso('PERÚ'), ['PE']);
  assert.deepEqual(iso('republica dom'), ['DO']);
});

test('phoneFilter: by ISO code, exact matches first', () => {
  const iso = (q) => B.phoneFilter(PHONE.countries, q, esName).map((c) => c[0]);
  assert.equal(iso('PE')[0], 'PE');
  assert.equal(iso('pe')[0], 'PE');
  assert.equal(iso('us')[0], 'US');
});

test('phoneFilter: by calling code, with or without +', () => {
  const pe = [['PE', '51']];
  assert.deepEqual(B.phoneFilter(PHONE.countries, '+51', esName), pe);
  assert.deepEqual(B.phoneFilter(PHONE.countries, '51', esName), pe);
  assert.deepEqual(B.phoneFilter(PHONE.countries, ' + 51 ', esName), pe);
  assert.deepEqual(B.phoneFilter(PHONE.countries, '+34', esName), [['ES', '34']]);
  // "+1" is shared: every one of them, and nothing else.
  const one = B.phoneFilter(PHONE.countries, '+1', esName);
  assert.equal(one.length, 25);
  assert.ok(one.every((c) => c[1] === '1'));
  assert.ok(['US', 'CA', 'DO', 'PR'].every((i) => one.some((c) => c[0] === i)));
  // While typing, a partial code narrows; a whole pasted number finds the code it starts with.
  const five = B.phoneFilter(PHONE.countries, '+5', esName);
  assert.ok(five.length > 1 && five.every((c) => c[1][0] === '5'));
  assert.ok(five.some((c) => c[0] === 'PE'));
  const pasted = B.phoneFilter(PHONE.countries, '+1 809 555 1234', esName);
  assert.ok(pasted.length === 25 && pasted.some((c) => c[0] === 'DO'));
});

test('phoneFilter: exact, then word-start, then anywhere; list order kept inside each', () => {
  const list = [['GP', '590'], ['ST', '239'], ['PM', '508'], ['PE', '51']];
  // Guadalupe (…pe), Santo Tomé y Príncipe (…pe), San Pedro y Miquelón (word "pe…"), Perú (ISO).
  assert.deepEqual(B.phoneFilter(list, 'pe', esName).map((c) => c[0]), ['PE', 'PM', 'GP', 'ST']);
});

test('phoneFilter: empty query is the whole list, as a copy; the input is untouched', () => {
  const list = PHONE.countries.slice(0, 5);
  const before = JSON.stringify(list);
  for (const q of ['', '   ', '+', null, undefined]) {
    const out = B.phoneFilter(list, q, esName);
    assert.deepEqual(out, list);
    assert.notStrictEqual(out, list);
  }
  B.phoneFilter(list, 'a', esName);
  assert.equal(JSON.stringify(list), before);
  assert.deepEqual(B.phoneFilter(undefined, 'pe', esName), []);
});

test('phoneFilter: without usable names it still filters by ISO and code', () => {
  const broken = () => { throw new RangeError('no Intl.DisplayNames'); };
  assert.deepEqual(B.phoneFilter(PHONE.countries, 'pe', broken).map((c) => c[0]), ['PE']);
  assert.deepEqual(B.phoneFilter(PHONE.countries, 'pe', undefined).map((c) => c[0]), ['PE']);
  assert.deepEqual(B.phoneFilter(PHONE.countries, '+51', broken), [['PE', '51']]);
});

test('phoneSort: pinned first, the rest by localized name', () => {
  const sorted = B.phoneSort(PHONE.countries, esName, 'es', ['PE']);
  assert.equal(sorted.length, PHONE.countries.length);
  assert.equal(sorted[0][0], 'PE');
  assert.deepEqual(sorted.slice(1, 5).map((c) => c[0]), ['AF', 'AL', 'DE', 'AD']); // Afganistán, Albania, Alemania, Andorra
  assert.equal(sorted.filter((c) => c[0] === 'PE').length, 1, 'the pinned one is not repeated');
});

test('phoneSort: several pins keep their order; unknown and repeated pins are skipped', () => {
  const list = [['AR', '54'], ['PE', '51'], ['US', '1'], ['CL', '56']];
  const out = B.phoneSort(list, esName, 'es', ['US', 'ZZ', 'pe', 'US']).map((c) => c[0]);
  assert.deepEqual(out, ['US', 'PE', 'AR', 'CL']);
  assert.deepEqual(B.phoneSort(list, esName, 'es', 'CL').map((c) => c[0]), ['CL', 'AR', 'US', 'PE'],
    'one pin as a plain string');
  assert.deepEqual(B.phoneSort(list, esName, 'es').map((c) => c[0]), ['AR', 'CL', 'US', 'PE'],
    'no pins: Argentina, Chile, Estados Unidos, Perú');
});

test('phoneSort: the order follows the locale', () => {
  // Swedish sorts Ö after Z: Norge, Sydafrika, Österrike. English collation puts Ö with O.
  const list = [['AT', '43'], ['ZA', '27'], ['NO', '47']];
  const svName = regionNames('sv');
  assert.deepEqual(B.phoneSort(list, svName, 'sv', []).map((c) => c[0]), ['NO', 'ZA', 'AT']);
  assert.deepEqual(B.phoneSort(list, svName, 'en', []).map((c) => c[0]), ['NO', 'AT', 'ZA']);
});

test('phoneSort: an empty or bad locale and a failing nameOf do not throw', () => {
  const list = [['PE', '51'], ['AR', '54']];
  // new Intl.Collator('') and 'a'.localeCompare('b', '') throw RangeError; phoneSort must not.
  for (const locale of ['', undefined, null, ['', 'es'], 'not a locale!']) {
    assert.equal(B.phoneSort(list, esName, locale).length, 2, JSON.stringify(locale));
  }
  const broken = () => { throw new RangeError('no Intl.DisplayNames'); };
  assert.deepEqual(B.phoneSort(list, broken, 'es').map((c) => c[0]), ['AR', 'PE'], 'falls back to the ISO');
});

test('phoneSort does not modify the list it was given', () => {
  const list = PHONE.countries.slice(0, 10);
  const before = JSON.stringify(list);
  B.phoneSort(list, esName, 'es', ['PE']);
  assert.equal(JSON.stringify(list), before);
});
