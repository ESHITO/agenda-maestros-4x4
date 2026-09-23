// booking-logic.js — the PURE date/slot/format logic shared by book.html and manage.html, so a
// change is made once instead of twice. No DOM.
// Also the phone-question helpers (phoneDetectCountry, phoneCombine, phoneFilter, phoneSort)
// that book.html's country picker uses.
// Served inlined into the book/manage Go templates, and require()-able by the node tests
// (booking-logic.test.js). Same UMD pattern as room-logic.js — no build step, stays
// framework-free.
//
// NOT loaded by embed.js. The widget is served as its own standalone file
// (internal/handler/embed_handler.go serves the embedded bytes unmodified), so `BookingLogic`
// is undefined inside it and it carries its own copies of the few helpers it needs — see the
// comments on its dowLabels, fmt and phone* copies. Anything added here that all three
// surfaces need has to be mirrored there deliberately.
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.BookingLogic = factory();
})(typeof self !== 'undefined' ? self : this, function () {
  function pad2(n) { return (n < 10 ? '0' : '') + n; }

  // dateKeyFromISO — the "YYYY-MM-DD" a slot belongs to, in the SELECTED timezone. Correct: uses
  // Intl with an explicit tz, NOT new Date().toLocaleDateString() (which keys off the browser tz
  // and was the latent bug in book.html/manage.html). This is the grouping key for slots-by-day.
  function dateKeyFromISO(iso, tz) {
    var p = new Intl.DateTimeFormat('en-CA', {
      timeZone: tz, year: 'numeric', month: '2-digit', day: '2-digit'
    }).format(new Date(iso));
    return p; // en-CA already yields YYYY-MM-DD
  }

  // ymd — "YYYY-MM-DD" for a local Date (the calendar grid's own day cells).
  function ymd(d) { return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate()); }

  // groupSlotsByDay — { "YYYY-MM-DD": [slot,…] } in the selected tz. Slots are {start,…} (or pass
  // a `key` selector for shapes that differ). Optionally drops one slot (reschedule excludes the
  // current booking's own time).
  function groupSlotsByDay(slots, tz, excludeStart) {
    var by = {};
    (slots || []).forEach(function (s) {
      if (excludeStart && s.start === excludeStart) return;
      var k = dateKeyFromISO(s.start, tz);
      (by[k] = by[k] || []).push(s);
    });
    return by;
  }

  // mergeDaySlots — one day's entries in time order, each tagged `.taken`, for event
  // types that show already-booked times greyed out instead of hiding them.
  //
  // Free and taken arrive as separate arrays from the API and are only ever combined
  // here, for display. Keeping them apart on the wire is deliberate: a merged list is
  // one field away from a client submitting a taken start as a booking.
  function mergeDaySlots(free, taken) {
    var out = [];
    (free || []).forEach(function (s) { out.push(withTaken(s, false)); });
    (taken || []).forEach(function (s) { out.push(withTaken(s, true)); });
    // Parsed rather than string-compared: slot times carry a UTC offset, and two
    // entries on the same calendar day can straddle a DST change and sort wrongly.
    out.sort(function (a, b) { return Date.parse(a.start) - Date.parse(b.start); });
    return out;
  }

  function withTaken(slot, taken) {
    var copy = {};
    for (var k in slot) { if (Object.prototype.hasOwnProperty.call(slot, k)) copy[k] = slot[k]; }
    copy.taken = taken;
    return copy;
  }


  // formatTime — "1:30 PM" in the selected tz.
  function formatTime(iso, tz, locale) {
    return new Intl.DateTimeFormat(locale || [], {
      timeZone: tz, hour: 'numeric', minute: '2-digit'
    }).format(new Date(iso));
  }

  // formatDay — a date label in the selected tz. style 'short' → "Mon, Jan 15"; 'long' →
  // "Monday, January 15".
  function formatDay(iso, tz, style, locale) {
    var long = style === 'long';
    return new Intl.DateTimeFormat(locale || [], {
      timeZone: tz, weekday: long ? 'long' : 'short',
      month: long ? 'long' : 'short', day: 'numeric'
    }).format(new Date(iso));
  }

  // dowIndex — Monday-first weekday index (0=Mon … 6=Sun) for the calendar grid offset.
  function dowIndex(date) { return (date.getDay() + 6) % 7; }

  // dowLabels — Monday-first weekday header labels (2-char abbreviations), via Intl for
  // the given locale rather than a hardcoded English array. Replaces the old
  // ['Mo','Tu',…] literal that was duplicated (in English, regardless of visitor
  // language) across book.html/manage.html. 2024-01-01 is an arbitrary fixed Monday
  // anchor — only its weekday matters, not the actual date.
  function dowLabels(locale) {
    var labels = [];
    var monday = new Date(Date.UTC(2024, 0, 1));
    for (var i = 0; i < 7; i++) {
      var d = new Date(monday.getTime() + i * 86400000);
      var full = new Intl.DateTimeFormat(locale || [], { weekday: 'short', timeZone: 'UTC' }).format(d);
      labels.push(full.slice(0, 2));
    }
    return labels;
  }

  function startOfMonth(d) { return new Date(d.getFullYear(), d.getMonth(), 1); }
  function endOfMonth(d) { return new Date(d.getFullYear(), d.getMonth() + 1, 0); }
  function addMonths(d, n) { return new Date(d.getFullYear(), d.getMonth() + n, 1); }
  function daysInMonth(year, month) { return new Date(year, month + 1, 0).getDate(); }

  // fmt — argument substitution for the translated strings the booking surfaces render
  // themselves, so the three of them don't each grow their own. Supports exactly the two
  // forms the locale files use for these keys: plain %s, taken in order, and the indexed
  // %[n]s that lets a translation reorder its arguments ("%[1]s has no available times on
  // %[2]s" is date-first in several languages). Server-side, Go's fmt does this job; this
  // is the client half of the same contract.
  //
  // Deliberately not a printf. Accepting %d without implementing number formatting would
  // be worse than not claiming to: the keys these surfaces substitute carry %s only, and
  // internal/i18n's verb-parity test holds every locale to English's verbs.
  //
  // A missing argument renders as an empty string rather than leaving "%s" on screen —
  // visibly wrong copy beats a literal format verb in front of a customer.
  function fmt(template, args) {
    var list = args || [];
    var next = 0;
    return String(template).replace(/%(?:\[(\d+)\])?s/g, function (_match, index) {
      var pick = index ? Number(index) - 1 : next++;
      var value = list[pick];
      return value === undefined || value === null ? '' : String(value);
    });
  }

  // ---- Phone questions (the "phone" question type) ----------------------------------------
  // A country picker plus the national number, sent as ONE answer string "+<dial> <digits>"
  // that the webhook automations receive unchanged. The data ([ISO, dial] pairs from
  // libphonenumber and an IANA zone -> ISO map, in phone-data.json) comes from the server; the
  // display names come from Intl.DisplayNames in the browser. Everything here is pure.
  //
  // embed.js does not load this file (see the top): it keeps deliberate copies of these four
  // functions. Each one is self-contained (its helpers live inside it), so the copy is the
  // whole function, verbatim. Change one here, change its copy there.

  // phoneDetectCountry — the country the picker starts on, or '' for none. There is no fixed
  // default: visitors and mentors are in many countries, and a wrong preselected code sends a
  // wrong number to the webhook more quietly than an empty picker does. The signals, strongest
  // first, each taken only if it names a country in `known` (the Set of ISO codes the picker
  // offers; an array works too; a guess outside it would have no dial code):
  //   1. hint      — the server's guess from the visitor's IP (Cloudflare's CF-IPCountry);
  //   2. timeZone  — the BROWSER's IANA zone looked up in tz2cc ("America/Lima" -> "PE"). Pass
  //                  the real browser zone, not the one the visitor picked for the calendar;
  //   3. languages — navigator.languages: the region of the first entry whose region is known
  //                  ("es-PE" -> "PE", "pt-BR" -> "BR"). A bare "es" names no country.
  function phoneDetectCountry(opts) {
    var o = opts || {};
    function isKnown(iso) {
      var known = o.known;
      if (!iso || !known) return false;
      if (typeof known.has === 'function') return known.has(iso);
      return Array.isArray(known) && known.indexOf(iso) !== -1;
    }
    // "es-PE" -> "PE", "zh-Hant-TW" -> "TW" (script skipped), "en_US" -> "US" (some Android
    // WebViews use '_'); "es-419" (a UN M.49 area, not a country) and "de" -> ''.
    function regionOf(tag) {
      var parts = String(tag || '').split(/[-_]/);
      for (var i = 1; i < parts.length; i++) {
        if (/^[A-Za-z]{2}$/.test(parts[i])) return parts[i].toUpperCase();
        // An extlang (3 letters) or a script (4) may come before the region; anything else
        // (a numeric area, a variant, an extension's singleton) means there is no region.
        if (!/^[A-Za-z]{3,4}$/.test(parts[i])) break;
      }
      return '';
    }

    var hint = String(o.hint || '').trim().toUpperCase();
    if (/^[A-Z]{2}$/.test(hint) && isKnown(hint)) return hint;

    var tz2cc = o.tz2cc;
    if (tz2cc && o.timeZone && Object.prototype.hasOwnProperty.call(tz2cc, o.timeZone)) {
      var byZone = String(tz2cc[o.timeZone] || '').toUpperCase();
      if (isKnown(byZone)) return byZone;
    }

    var langs = typeof o.languages === 'string' ? [o.languages] : (o.languages || []);
    for (var i = 0; i < langs.length; i++) {
      var region = regionOf(langs[i]);
      if (isKnown(region)) return region;
    }
    return '';
  }

  // phoneCombine — the ONE string a phone answer travels as: "+<dial> <digits>".
  //   phoneCombine("51", "987 654-321") -> "+51 987654321"
  // The visitor's separators are dropped; no digits at all -> '' (not answered, like an empty
  // field). Digits from Arabic, Persian and full-width keyboards count as digits: the server
  // (validPhone) only counts 0-9, so without this they would vanish, and the answer with them.
  //
  // A number typed with a leading '+' carries its own country code, and that code wins over
  // the picker, which is only a default:
  //   "+51 987 654 321" with Peru picked -> "+51 987654321"  (the code is not doubled)
  //   "+34 612 34 56 78" with Peru picked -> "+34 612345678" (a pasted Spanish number; the
  //                                          literal "+51 34612..." does not exist)
  // Without a '+' the digits are national and need the picked code: with no dial, '' (never
  // "+ 987..." nor bare digits). The caller tells "not answered" from "no country picked" by
  // whether the field has digits.
  //
  // Deliberately NOT handled, because they need per-country rules: a national trunk prefix
  // (UK "07700 900123" comes out "+44 07700900123"; WhatsApp wants "+44 7700900123") and
  // international dialling prefixes such as "00".
  function phoneCombine(dial, raw) {
    // Country calling codes form a prefix code (none is the start of another); the one- and
    // two-digit ones are this fixed list, and every other code has three digits. The node
    // tests hold it against every code in phone-data.json.
    var SHORT_CODE = /^(?:1|7|2[07]|3[0-469]|4[013-9]|5[1-8]|6[0-6]|8[1246]|9[0-58])/;
    function ascii(s) {
      return s.replace(/[\u0660-\u0669\u06f0-\u06f9\uff10-\uff19\uff0b]/g, function (ch) {
        var c = ch.charCodeAt(0);
        if (c === 0xff0b) return '+';
        return String(c <= 0x0669 ? c - 0x0660 : c <= 0x06f9 ? c - 0x06f0 : c - 0xff10);
      });
    }
    var text = ascii(String(raw == null ? '' : raw));
    var digits = text.replace(/\D/g, '');
    if (!digits) return '';
    var code = ascii(String(dial == null ? '' : dial)).replace(/\D/g, '');
    if (/^\s*\+/.test(text)) {
      if (!code || digits.indexOf(code) !== 0) {
        var m = SHORT_CODE.exec(digits);
        code = m ? m[0] : digits.slice(0, 3);
      }
      digits = digits.slice(code.length);
      return digits ? '+' + code + ' ' + digits : '';
    }
    return code ? '+' + code + ' ' + digits : '';
  }

  // phoneFilter — the entries of `list` ([iso, dial] pairs) matching the picker's search box.
  //   Letters: the localized name nameOf(iso), ignoring case and accents ("peru" finds
  //   "Perú"), or the exact ISO code ("pe").
  //   Digits, with an optional '+' and spaces, dashes, dots or parentheses: the calling code.
  //   "+34" and "34" find Spain, "+5" narrows to the 5x codes as it is typed, and a whole
  //   pasted number ("+1 809 555 1234") finds the code it starts with.
  // Order: exact matches first (the ISO, the whole name, the whole code), then prefix matches
  // (the name or one of its words starts with the query; the code starts with it), then the
  // rest, keeping `list`'s own order inside each group, so a pinned-then-alphabetical list
  // stays that way. An empty query returns all of `list` (a copy). A nameOf that throws or
  // returns nothing falls back to the ISO code, so a browser without Intl.DisplayNames still
  // gets a working filter.
  function phoneFilter(list, query, nameOf) {
    function fold(s) {
      s = String(s == null ? '' : s).toLowerCase();
      if (s.normalize) s = s.normalize('NFD').replace(/[\u0300-\u036f]/g, '');
      return s.replace(/[\u2018\u2019\u02bc\u00b4`]/g, "'").replace(/\s+/g, ' ').trim();
    }
    function nameFor(iso) {
      try {
        var n = nameOf ? nameOf(iso) : '';
        return n ? String(n) : String(iso);
      } catch (e) {
        return String(iso);
      }
    }
    var items = (list || []).slice();
    var q = fold(query);
    var groups = [[], [], []];
    if (/^\+?[\d\s().-]*$/.test(q)) {
      var qd = q.replace(/\D/g, '');
      if (!qd) return items;
      items.forEach(function (c) {
        var dial = String(c[1]);
        if (dial === qd) groups[0].push(c);
        else if (dial.indexOf(qd) === 0) groups[1].push(c);
        else if (qd.indexOf(dial) === 0) groups[2].push(c);
      });
    } else {
      var iso = q.toUpperCase();
      items.forEach(function (c) {
        var name = fold(nameFor(c[0]));
        if (String(c[0]).toUpperCase() === iso || name === q) { groups[0].push(c); return; }
        for (var at = name.indexOf(q); at !== -1; at = name.indexOf(q, at + 1)) {
          if (at === 0 || /[\s'(,./-]/.test(name.charAt(at - 1))) { groups[1].push(c); return; }
        }
        if (name.indexOf(q) !== -1) groups[2].push(c);
      });
    }
    return groups[0].concat(groups[1], groups[2]);
  }

  // phoneSort — the picker's order: the `pinned` ISO codes first, in the order given (the
  // detected country, say), then the rest alphabetically by localized name for `locale`. It
  // uses an Intl.Collator, which orders exactly like localeCompare(locale) but is built once
  // instead of per comparison, and looks each name up once. `list` is not modified. An empty
  // or unusable locale falls back to the browser default instead of throwing (Intl throws a
  // RangeError on '', and the widget's locale can be '').
  function phoneSort(list, nameOf, locale, pinned) {
    function nameFor(iso) {
      try {
        var n = nameOf ? nameOf(iso) : '';
        return n ? String(n) : String(iso);
      } catch (e) {
        return String(iso);
      }
    }
    var plain = function (a, b) { return a < b ? -1 : a > b ? 1 : 0; };
    var locales = [].concat(locale == null ? [] : locale).filter(function (l) { return !!l; });
    var compare = plain;
    try {
      compare = new Intl.Collator(locales.length ? locales : undefined).compare;
    } catch (e) {
      try { compare = new Intl.Collator().compare; } catch (e2) { compare = plain; }
    }
    var byIso = {};
    (list || []).forEach(function (c) { byIso[String(c[0]).toUpperCase()] = c; });
    var head = [];
    var taken = {};
    [].concat(pinned == null ? [] : pinned).forEach(function (p) {
      var key = String(p).toUpperCase();
      if (Object.prototype.hasOwnProperty.call(byIso, key) && !taken[key]) {
        taken[key] = true;
        head.push(byIso[key]);
      }
    });
    var rest = [];
    (list || []).forEach(function (c) {
      var key = String(c[0]).toUpperCase();
      if (!taken[key]) rest.push({ entry: c, name: nameFor(c[0]), iso: key });
    });
    rest.sort(function (a, b) { return compare(a.name, b.name) || plain(a.iso, b.iso); });
    return head.concat(rest.map(function (r) { return r.entry; }));
  }

  // NOTE: there is deliberately no host-label helper here. Each surface builds its own
  // (hostsLabel in book.go for the server-rendered page, in book.html's script for the
  // post-slot-pick rewrite, and in embed.js), because the label needs the resolved locale's
  // separator/conjunction keys and this module is locale-free by design. A copy used to
  // live here, exported and unit-tested but called by nothing — which made it a trap: it
  // hardcoded English " & " and would have silently un-translated the label for anyone who
  // consolidated onto it. If these are ever unified, the shared version must take the
  // locale's list_separator/list_conjunction, not hardcode punctuation.

  return {
    dateKeyFromISO: dateKeyFromISO,
    ymd: ymd,
    groupSlotsByDay: groupSlotsByDay,
    mergeDaySlots: mergeDaySlots,
    fmt: fmt,
    formatTime: formatTime,
    formatDay: formatDay,
    dowIndex: dowIndex,
    dowLabels: dowLabels,
    startOfMonth: startOfMonth,
    endOfMonth: endOfMonth,
    addMonths: addMonths,
    daysInMonth: daysInMonth,
    phoneDetectCountry: phoneDetectCountry,
    phoneCombine: phoneCombine,
    phoneFilter: phoneFilter,
    phoneSort: phoneSort
  };
});
