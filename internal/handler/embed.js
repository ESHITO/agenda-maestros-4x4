/* Calnode embeddable booking widget.
 *
 * A dependency-free Web Component that renders the booking flow into a Shadow DOM —
 * real HTML in the host page (no iframe), styles encapsulated. It reuses the SAME
 * stylesheet and class names as the server-rendered /book page (loaded via
 * <link href="<base>/booking.css">) so the two never drift; only the responsive
 * pane layout (container-query driven) and a :host reset are widget-specific.
 *
 * Calls the instance's public, CORS-enabled endpoints: /public, /slots, /questions,
 * POST /bookings, and /v1/phone-countries (only when the form has a 'phone' question).
 * The phone picker's flags are <img> from the instance's own /assets/flags/.
 *
 * Usage:
 *   <script src="https://booking.example.com/embed.js" async></script>
 *   <calnode-booking slug="intro-call"></calnode-booking>        <!-- inline -->
 *   <button data-calnode-popup="intro-call">Book a call</button>  <!-- popup  -->
 */
(function () {
  'use strict';
  if (window.customElements && customElements.get('calnode-booking')) return;

  var SELF = document.currentScript;
  var BASE = SELF ? new URL(SELF.src).origin : window.location.origin;

  var TZ = (Intl.DateTimeFormat().resolvedOptions().timeZone) || 'UTC';
  var STEP_BP = 560; // below this width → step-flow (one view at a time)

  // i18n — the server resolves locale from the browser's own Accept-Language header
  // automatically (fetch() always sends it; not CORS-blocked), so no client-side
  // detection is needed for the default auto-detect path. An explicit lang="" attribute
  // on <calnode-booking> (a host page choosing to force a language) is sent as ?lang= on
  // the /public call, matching the ?lang= override semantics on book.html/manage.html.
  // /public returns the resolved {locale, i18n} once; cached on the instance and reused
  // by every subsequent render, not refetched per-request.
  function langOverride(el) {
    var v = el.getAttribute('lang');
    return v ? v.split('-')[0] : '';
  }
  // t: pure lookup, not a method — mirrors internal/i18n.Locale.T (falls back to the
  // key itself if the string table hasn't loaded yet or the key is missing).
  function t(i18n, key) { return (i18n && i18n[key]) || key; }
  // fmt: argument substitution for the translated strings that carry one — %s in order,
  // plus the indexed %[n]s a translation may use to reorder. Mirrors BookingLogic.fmt in
  // internal/handler/assets/booking-logic.js; this widget does NOT load that module (see
  // dowLabels below), so the two must be kept in step. Deliberately not a printf: the keys
  // substituted here carry %s only.
  function fmt(template, args) {
    var list = args || [];
    var next = 0;
    return String(template).replace(/%(?:\[(\d+)\])?s/g, function (_match, index) {
      var pick = index ? Number(index) - 1 : next++;
      var value = list[pick];
      return value === undefined || value === null ? '' : String(value);
    });
  }
  // dowLabels: Monday-first weekday header labels via Intl, matching
  // BookingLogic.dowLabels in booking-logic.js (not literally shared code — this widget
  // doesn't import that module — but the same approach, replacing what used to be a
  // hardcoded English DOW array).
  function dowLabels(locale) {
    var out = [];
    var monday = new Date(Date.UTC(2024, 0, 1));
    for (var i = 0; i < 7; i++) {
      var day = new Date(monday.getTime() + i * 86400000);
      out.push(new Intl.DateTimeFormat(locale || [], { weekday: 'short', timeZone: 'UTC' }).format(day).slice(0, 2));
    }
    return out;
  }

  // ── Phone questions: country picker ──────────────────────────────────────────────
  // Clients and mentors are in many countries, so there is NO default country, ever: the
  // picker starts from a best guess and may start with nothing chosen. Its data (the
  // [ISO, calling code] list, a time zone -> country map, and the instance's guess from the
  // visitor's IP via Cloudflare's CF-IPCountry) comes from GET /v1/phone-countries, fetched
  // only when the form has a 'phone' question. Country names are never translated by hand:
  // Intl.DisplayNames gives them in the resolved locale. Flags are the instance's own SVGs
  // (BASE + /assets/flags/xx.svg), never a third-party CDN.
  //
  // The four phone* functions below are DELIBERATE COPIES, verbatim, of the originals in
  // internal/handler/assets/booking-logic.js (tested there with node --test), which this
  // widget does not load (see the note on fmt above). Each is self-contained, so the copy is
  // the whole function: change one, change the other.

  // Mirror of BookingLogic.phoneDetectCountry in booking-logic.js - keep in step.
  // The country the picker starts on, or '' for none: the server's hint, then the browser's
  // time zone, then the region of navigator.languages, each only if it is in `known`.
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

  // Mirror of BookingLogic.phoneCombine in booking-logic.js - keep in step.
  // The ONE string a phone answer travels as: "+<dial> <digits>" ("51", "987 654-321" ->
  // "+51 987654321"); a number typed with a leading '+' keeps its own code; '' with no digits.
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

  // Mirror of BookingLogic.phoneFilter in booking-logic.js - keep in step.
  // The [iso, dial] pairs matching the search box: by localized name (no case, no accents),
  // exact ISO code, or calling code ("+34", "34"); exact matches first, then prefixes.
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

  // Mirror of BookingLogic.phoneSort in booking-logic.js - keep in step.
  // The pinned ISO codes first, in the order given, then the rest by localized name.
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

  // PHONE_NO_FLAG: no SVG in assets/flags for these, so they are never requested.
  var PHONE_NO_FLAG = { AC: true, TA: true };
  // Any digit phoneCombine accepts: ASCII, Arabic-Indic, Persian and full-width. Same as
  // PHONE_ANY_DIGIT in book.html (phoneDigits below only counts 0-9, which is right for the
  // degraded field only, whose value is built from it).
  var PHONE_ANY_DIGIT = /[0-9\u0660-\u0669\u06F0-\u06F9\uFF10-\uFF19]/;
  function phoneIsIntl(raw) { return /^\s*\+/.test(raw); }
  function phoneDigits(raw) { return String(raw || '').replace(/\D/g, ''); }
  // Lower case: /assets/flags/{name} answers only ^[a-z]{2}\.svg$. Absolute, since the
  // widget runs on the customer's origin.
  function phoneFlagURL(iso) { return BASE + '/assets/flags/' + iso.toLowerCase() + '.svg'; }

  // phonePickerData: the /v1/phone-countries payload, checked on the way in (it names the
  // flag URLs and feeds the picker), or null when unusable: the field then degrades.
  function phonePickerData(d) {
    var list = (d && Array.isArray(d.countries) ? d.countries : []).filter(function (c) {
      return Array.isArray(c) && /^[A-Z]{2}$/.test(c[0]) && /^[0-9]{1,4}$/.test(String(c[1]));
    }).map(function (c) { return [c[0], String(c[1])]; });
    if (!list.length) return null;
    return {
      countries: list,
      tz2cc: d.tz2cc && typeof d.tz2cc === 'object' ? d.tz2cc : {},
      visitor_country: typeof d.visitor_country === 'string' ? d.visitor_country : '',
    };
  }

  // phoneShared: everything the pickers of one widget share, computed once. Mirrors
  // phoneShared in book.html's "Phone questions" block, except that the hint arrives as
  // visitor_country (the API's snake_case) and locale may be '' here, which Intl rejects
  // with a RangeError, so it is left out rather than passed. Throws if the data is
  // unusable; the caller then degrades.
  function phoneShared(data, locale) {
    var countries = data.countries;
    if (!countries || !countries.length) throw new Error('no countries');
    var dialOf = {}, byDial = {}, zones = {}, known = new Set();
    countries.forEach(function (c) {
      dialOf[c[0]] = c[1];
      (byDial[c[1]] = byDial[c[1]] || []).push(c[0]);
      known.add(c[0]);
    });
    // Names from the browser in the resolved locale, never from a hand-made table.
    // Intl.DisplayNames is missing in old Safari/WebViews and returns the code itself for
    // rare ones (AC, TA): the ISO code is the last resort either way.
    var names = null;
    try { names = new Intl.DisplayNames(locale ? [locale, 'en'] : ['en'], { type: 'region' }); } catch (e) { names = null; }
    var nameCache = {};
    var nameOf = function (iso) {
      if (!nameCache[iso]) {
        var n = '';
        try { n = names ? names.of(iso) : ''; } catch (e) { n = ''; }
        nameCache[iso] = n || iso;
      }
      return nameCache[iso];
    };
    var detected = '';
    try {
      detected = phoneDetectCountry({
        hint: data.visitor_country || '',
        // The browser's own zone: this widget has no zone picker, so TZ is exactly that.
        timeZone: TZ,
        // navigator.languages carries the region ("es-PE"); the locale the server resolved
        // usually does not ("es").
        languages: (navigator.languages && navigator.languages.length) ? navigator.languages : [navigator.language || ''],
        tz2cc: data.tz2cc || {},
        known: known,
      }) || '';
    } catch (e) { detected = ''; }
    if (!known.has(detected)) detected = '';
    // How many time zones each country owns: only used to pick which flag to show when a
    // typed "+<code>" is shared (US over the other 24 "+1" countries). The stored value is
    // the same whichever of them is shown.
    Object.keys(data.tz2cc || {}).forEach(function (z) { var cc = data.tz2cc[z]; zones[cc] = (zones[cc] || 0) + 1; });
    var sorted = null; // built on first open: nobody pays for sorting 245 names who never opens it
    var sortedList = function () { return sorted || (sorted = phoneSort(countries.slice(), nameOf, locale, detected)); };
    return { dialOf: dialOf, byDial: byDial, zones: zones, nameOf: nameOf, detected: detected, sortedList: sortedList };
  }

  // phoneCountryFor: the country to show for a dial code the visitor typed, or '' if the
  // data has none. For a shared code: the current choice if it has it, then the detected
  // country, then the one with the most time zones. Mirror of phoneCountryFor in book.html's
  // "Phone questions" block - keep in step.
  function phoneCountryFor(S, code, current) {
    var cands = S.byDial[code];
    if (!cands) return '';
    if (cands.indexOf(current) !== -1) return current;
    if (cands.indexOf(S.detected) !== -1) return S.detected;
    return cands.reduce(function (best, iso) { return (S.zones[iso] || 0) > (S.zones[best] || 0) ? iso : best; }, cands[0]);
  }

  // The country button's fixed insides, the same markup book.html renders: flag (hidden
  // until there is one), globe (shown when there is none), caret, the chosen country's name
  // for screen readers, and the code ("+51") or the "Country" label. Static: no data in it.
  // The space between the two spans keeps the accessible name "Perú +51", not "Perú+51"
  // (a flex container does not render it).
  var PHONE_BTN_HTML =
    '<img class="phone-flag" alt="" width="20" height="15" hidden>' +
    '<svg class="phone-globe" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>' +
    '<svg class="phone-caret" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="6 9 12 15 18 9"/></svg>' +
    '<span class="phone-sr"></span> ' +
    '<span class="phone-cc-code"></span>';

  var SVG_CLOCK = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>';
  var SVG_PIN = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 10c0 7-9 13-9 13s-9-6-9-13a9 9 0 0 1 18 0z"/><circle cx="12" cy="10" r="3"/></svg>';
  var SVG_CARD = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="5" width="20" height="14" rx="2"/><line x1="2" y1="10" x2="22" y2="10"/></svg>';
  var SVG_PREV = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>';
  var SVG_NEXT = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 18 15 12 9 6"/></svg>';
  var SVG_BACK = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>';
  var SVG_CHECK = '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#16a34a" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>';
  var SVG_X = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="6" y1="6" x2="18" y2="18"/><line x1="18" y1="6" x2="6" y2="18"/></svg>';
  var SVG_SPARK = '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l1.9 4.8L18.7 9.7l-4.8 1.9L12 16.4l-1.9-4.8L5.3 9.7l4.8-1.9L12 3z"/></svg>';
  var SVG_CHEV2 = '<svg class="asst-link-arrow" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="13 17 18 12 13 7"/><polyline points="6 17 11 12 6 7"/></svg>';

  function el(tag, attrs, kids) {
    var n = document.createElement(tag);
    if (attrs) for (var k in attrs) {
      if (k === 'class') n.className = attrs[k];
      else if (k === 'text') n.textContent = attrs[k];
      else if (k === 'html') n.innerHTML = attrs[k];
      else n.setAttribute(k, attrs[k]);
    }
    (kids || []).forEach(function (c) { if (c) n.appendChild(c); });
    return n;
  }

  function dayKey(iso) { return new Intl.DateTimeFormat('en-CA', { timeZone: TZ, year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(iso)); }
  // locale is the resolved server-side locale (this.locale), not the browser's own
  // ([]) — see the matching fix/comment in book.html / internal-docs/i18n-plan.md.
  function timeLabel(iso, locale) { return new Intl.DateTimeFormat(locale || [], { timeZone: TZ, hour: 'numeric', minute: '2-digit' }).format(new Date(iso)); }
  function shortDay(iso, locale) { return new Intl.DateTimeFormat(locale || [], { timeZone: TZ, weekday: 'short', month: 'short', day: 'numeric' }).format(new Date(iso)); }
  function ymd(d) { return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0'); }
  function startOfMonth(d) { return new Date(d.getFullYear(), d.getMonth(), 1); }
  function endOfMonth(d) { return new Date(d.getFullYear(), d.getMonth() + 1, 0); }
  function addMonths(d, n) { return new Date(d.getFullYear(), d.getMonth() + n, 1); }
  function mondayIndex(d) { return (d.getDay() + 6) % 7; }
  function esc(s) { return String(s).replace(/[&<>"]/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]; }); }
  // Group host label: "Alex", "Alex & Sam", "Alex, Sam & Jo", "A, B, C & 2 others" — or
  // "Alex, Sam y 2 más" in Spanish. Separator and conjunction come from the locale, not
  // hardcoded punctuation; translating only the trailing noun gives a half-English
  // "Alex, Sam & 2 otros". Mirrors hostsLabel in book.go — keep the two in step.
  function hostsLabel(hosts, i18n) {
    function fn(h) { return String(h.name || '').split(' ')[0]; }
    var sep = t(i18n, 'list_separator'), and = t(i18n, 'list_conjunction');
    var n = hosts.length;
    if (n === 0) return '';
    if (n === 1) return hosts[0].name || '';
    if (n === 2) return fn(hosts[0]) + and + fn(hosts[1]);
    if (n === 3) return fn(hosts[0]) + sep + fn(hosts[1]) + and + fn(hosts[2]);
    return fn(hosts[0]) + sep + fn(hosts[1]) + sep + fn(hosts[2]) + and + (n - 3) + ' ' + (n - 3 === 1 ? t(i18n, 'other') : t(i18n, 'others'));
  }
  function money(cents, cur) {
    var amt = (cents / 100).toFixed(2);
    var c = (cur || 'usd').toUpperCase();
    var sym = { USD: '$', EUR: '€', GBP: '£', AUD: 'A$', CAD: 'C$', NZD: 'NZ$' }[c];
    return sym ? sym + amt : amt + ' ' + c;
  }

  // Widget-only layer: :host reset, container-query responsive layout (3-pane →
  // letterbox banner → stacked), step-flow visibility, powered footer. The visual
  // primitives all come from the shared booking.css <link>.
  var STYLE = '' +
    ':host{all:initial;display:block;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;color:#111827;line-height:1.5;}' +
    '.wrap{container-type:inline-size;}' +
    '.card{box-shadow:0 1px 3px rgba(0,0,0,.06);}' +
    // Constrained widths: info becomes a compact horizontal header bar (avatar left,
    // host name + event + inline meta right) spanning the top; calendar + right below.
    '@container (max-width:719px){' +
      '.card{flex-wrap:wrap;}' +
      // min-width:0 lets the info pane shrink to the card width so its text wraps
      // instead of overflowing to the right.
      '.info{width:100%;flex-basis:100%;min-width:0;border-right:none;border-bottom:1px solid #e5e7eb;}' +
      '.info-head{display:flex;align-items:center;gap:14px;}' +
      '.info .host-faces{margin-bottom:0;flex-shrink:0;}' +
      '.info .avatar-img,.info .avatar-initials{width:46px;height:46px;margin-bottom:0;font-size:1.05rem;}' +
      '.titlewrap{min-width:0;}' +
      '.info .host-name{margin-bottom:1px;}' +
      '.info .event-name{margin-bottom:0;}' +
      // meta + description align to the pane's left edge (under the avatar). The
      // 2-line clamp is applied by JS (the shared .clamp class) only when overflowing.
      '.info .meta{flex-direction:row;flex-wrap:wrap;gap:5px 14px;margin-top:12px;}' +
      '.info .description{margin-top:6px;overflow-wrap:break-word;}' +
      '.cal-col{border-right:1px solid #e5e7eb;}' +
    '}' +
    // Narrow / mobile: stack the panes; JS shows one at a time (step-flow). The info
    // header stays the horizontal bar (flex-basis reset so it sizes to content).
    '@container (max-width:559px){' +
      '.card{flex-direction:column;flex-wrap:nowrap;}' +
      '.info{flex-basis:auto;}' +
      '.cal-col{border-right:none;border-bottom:1px solid #e5e7eb;}' +
      '.cal-grid{grid-template-columns:repeat(7,1fr);width:100%;}' +
      '.ch,.cd{width:100%;}' +
    '}' +
    // Step-flow: when narrow, show one step at a time. Calendar step keeps the info
    // banner (so you see what you are booking); the slot/form/confirm step shows just
    // the right pane with a back button.
    '.card.step-cal .right-col{display:none;}' +
    '.card.step-right .cal-col{display:none;}' +
    '.card.step-right .info{display:none;}' +
    '.powered{text-align:center;font-size:.6875rem;color:#9ca3af;padding:10px;}' +
    '.powered a{color:#6b7280;text-decoration:none;font-weight:600;}' +
    '.powered a:hover{text-decoration:underline;}' +
    '.loading{padding:48px 24px;color:#6b7280;font-size:.875rem;text-align:center;}' +
    '.infotext{display:block;}' +
    // Phone picker: its look is booking.css's (.phone-picker and friends, shared verbatim
    // with book.html). Widget-only: a list flag the host page blocks (its CSP img-src, or
    // COEP) is swapped for its ISO code, sized here to fit the flag's 20x15 box.
    '.phone-list .phone-flag-none{font-size:8px;line-height:15px;text-align:center;color:#6b7280;overflow:hidden;}' +
    '@media (max-width:560px){:host([data-modal]) .card{min-height:100dvh;border-radius:0;}}';

  function api(path) {
    return fetch(BASE + path, { headers: { 'Accept': 'application/json' } }).then(function (r) {
      if (!r.ok) throw new Error('HTTP ' + r.status);
      return r.json();
    });
  }

  class CalnodeBooking extends HTMLElement {
    connectedCallback() {
      if (this._mounted) return;
      this._mounted = true;
      this.slug = this.getAttribute('slug');
      this.root = this.attachShadow({ mode: 'open' });
      var cssLink = el('link', { rel: 'stylesheet', href: BASE + '/booking.css' });
      // .clamp styling arrives with the stylesheet, so re-measure the description
      // overflow once it loads.
      cssLink.addEventListener('load', this.syncDesc.bind(this));
      this.root.appendChild(cssLink);
      this.root.appendChild(el('style', { text: STYLE }));
      this.wrap = el('div', { class: 'wrap' });
      this.root.appendChild(this.wrap);
      this.state = { month: startOfMonth(new Date()), slotsByDay: {}, noticeDates: [], degraded: false, day: null, view: 'pick', slot: null };
      this.narrow = false;
      this.cw = 9999;
      this.descExpanded = false;
      // Drive step-flow + description clamp off the widget's own width (not viewport).
      if (window.ResizeObserver) {
        this._ro = new ResizeObserver(function (entries) {
          this.cw = entries[0].contentRect.width;
          var n = this.cw < STEP_BP;
          if (n !== this.narrow) { this.narrow = n; this.applyStep(); }
          this.syncDesc();
        }.bind(this));
        this._ro.observe(this.wrap);
      }
      this.load();
    }
    disconnectedCallback() { if (this._ro) this._ro.disconnect(); }

    async load() {
      this.wrap.innerHTML = '';
      // Not translated: no string table exists yet until /public resolves below — the
      // widget can't know the language before its first network request completes.
      this.wrap.appendChild(el('div', { class: 'loading', text: 'Loading…' }));
      try {
        var lang = langOverride(this);
        var publicPath = '/v1/event-types/' + encodeURIComponent(this.slug) + '/public' + (lang ? '?lang=' + encodeURIComponent(lang) : '');
        var r = await Promise.all([
          api(publicPath),
          api('/v1/event-types/' + encodeURIComponent(this.slug) + '/questions'),
        ]);
        this.info = r[0];
        this.style.setProperty('--booking-accent', this.info.booking_accent || '#111827');
        this.style.setProperty('--booking-accent-text', this.info.booking_accent_foreground || '#ffffff');
        this.locale = this.info.locale || '';
        this.i18n = this.info.i18n || {};
        this.dow = dowLabels(this.locale);
        this.setAttribute('lang', this.locale || 'en'); // accessibility: announce the resolved language
        this.questions = (r[1] && r[1].items) || [];
        this.ensureAsstDrawer();
        // Country picker data: only for a form with a 'phone' question, fetched alongside
        // the first month of slots. Never fatal: a failure must not end in 'Could not load
        // this booking page.' - the phone field degrades to one plain international input.
        var phoneReq = this.questions.some(function (q) { return q.type === 'phone'; })
          ? api('/v1/phone-countries').then(phonePickerData).catch(function () { return null; })
          : Promise.resolve(null);
        var ready = await Promise.all([this.loadMonth(), phoneReq]);
        this.phoneData = ready[1];
        this.phoneS = undefined; // phoneShared(phoneData), computed on the first form view
        this.render();
      } catch (e) {
        this.wrap.innerHTML = '';
        // Same bootstrapping limitation as the "Loading…" text above — if /public itself
        // failed, there's no string table to translate this with.
        this.wrap.appendChild(el('div', { class: 'loading', text: 'Could not load this booking page.' }));
      }
    }

    async loadMonth() {
      var first = this.state.month, last = endOfMonth(first);
      var today = new Date(); today.setHours(0, 0, 0, 0);
      var from = first < today ? today : first;
      try {
        var r = await api('/v1/event-types/' + encodeURIComponent(this.slug) + '/slots?from=' + ymd(from) + '&to=' + ymd(last) + '&tz=' + encodeURIComponent(TZ));
        // `taken` is present only when the event type opts into showing booked times.
        // Tag on the way in so the renderer needs no second lookup, and so a taken entry
        // can never be mistaken for a bookable one further down.
        var by = {};
        (r.slots || []).forEach(function (s) {
          s.taken = false;
          (by[dayKey(s.start)] = by[dayKey(s.start)] || []).push(s);
        });
        (r.taken || []).forEach(function (s) {
          s.taken = true;
          (by[dayKey(s.start)] = by[dayKey(s.start)] || []).push(s);
        });
        this.state.slotsByDay = by;
        // Days the minimum-notice policy took starts away from, so an empty or thin day
        // can say why instead of leaving the visitor to guess (#20). Server-side these are
        // already in TZ, so they match dayKey's output.
        this.state.noticeDates = (r.min_notice && r.min_notice.dates) || [];
        // An external calendar check failed: busy data is incomplete, so offered
        // times may be unbookable (booking stays fail-closed at commit time).
        if (r.degraded) this.state.degraded = true;
        // Capture the id→host map so the header can narrow to a slot's actual host once
        // one is picked. Avatar URLs come back relative; make them absolute (the widget
        // runs cross-origin to the Calnode instance).
        this.hostMeta = this.hostMeta || {};
        var hm = this.hostMeta;
        Object.keys(r.hosts || {}).forEach(function (id) {
          var m = r.hosts[id] || {}, av = m.avatar_url || '';
          hm[id] = { name: m.name || '', avatar_url: av && av.charAt(0) === '/' ? BASE + av : av };
        });
      } catch (e) { this.state.slotsByDay = {}; this.state.noticeDates = []; }
    }

    infoPane() {
      // Default: show every host the endpoint returns (round-robin: the whole rotation
      // team; fixed/group: the required set), stacked via .face + z-index — same as the
      // native page. Showing only hosts[0] surfaced one person (often one with no
      // availability) over slots that belong to someone else. Once a slot is picked,
      // narrow to that slot's actual assigned host(s), resolved from the id→host map.
      var hosts, sel = this.state.slot;
      if ((this.state.view === 'form' || this.state.view === 'confirm') && sel && sel.host_ids && this.hostMeta) {
        var hm = this.hostMeta;
        hosts = sel.host_ids.map(function (id) { return hm[id]; }).filter(Boolean);
      }
      if (!hosts || !hosts.length) hosts = (this.info.hosts && this.info.hosts.length) ? this.info.hosts : [];
      var faceKids = hosts.map(function (host, i) {
        var z = (hosts.length - i) * 10;
        var inner = host.avatar_url
          ? el('img', { class: 'avatar-img', src: host.avatar_url, alt: host.name || '' })
          : el('span', { class: 'avatar-initials', text: ((host.name || '?')[0] || '?').toUpperCase() });
        return el('span', { class: 'face', style: 'z-index:' + z }, [inner]);
      });
      // info-head = avatar + title (host name + event name). On compact widths the
      // avatar centers against this title only; meta + description sit below, indented
      // to line up under the title. On desktop these wrappers are plain blocks, so the
      // vertical column is unchanged.
      var titleKids = [];
      var label = hostsLabel(hosts, this.i18n);
      if (label) titleKids.push(el('p', { class: 'host-name', text: label }));
      titleKids.push(el('h1', { class: 'event-name', text: this.info.name }));
      var head = el('div', { class: 'info-head' }, [
        el('div', { class: 'host-faces' }, faceKids),
        el('div', { class: 'titlewrap' }, titleKids),
      ]);
      var meta = el('ul', { class: 'meta' }, [
        el('li', { html: SVG_CLOCK + ' ' + esc(this.info.duration_label || (this.info.duration_minutes + ' min')) }),
        this.info.location_label ? el('li', { html: SVG_PIN + ' ' + esc(this.info.location_label) }) : null,
        this.info.price_cents > 0 ? el('li', { html: SVG_CARD + ' ' + money(this.info.price_cents, this.info.currency) }) : null,
      ]);
      var kids = [head, meta];
      if (this.info.assistant_enabled) {
        var self = this;
        var asstLink = el('button', { class: 'asst-link', type: 'button', html: SVG_SPARK + ' ' + t(this.i18n, 'book_by_chat') + ' ' + SVG_CHEV2 });
        asstLink.addEventListener('click', function () { self.toggleAsst(); });
        kids.push(asstLink);
      }
      if (this.info.description) {
        kids.push(el('div', { class: 'description', text: this.info.description }));
        kids.push(el('button', { class: 'desc-toggle', type: 'button', text: t(this.i18n, 'show_more') }));
      }
      return el('aside', { class: 'info' }, kids);
    }

    // Conversational booking — a drawer appended to the shadow root (so it survives
    // re-renders, which wipe .wrap), opened by the inline "Book by chat" link. No global
    // floating button, to avoid colliding with the host site's own widgets. Uses the same
    // assistant endpoint + shared .asst-* styles as the hosted booking page.
    ensureAsstDrawer() {
      if (this.asstPanel || !this.info || !this.info.assistant_enabled) return;
      var self = this;
      var log = el('div', { class: 'asst-log' }, [
        el('div', { class: 'asst-msg bot', text: this.info.assistant_greeting || t(this.i18n, 'assistant_greeting') }),
      ]);
      var input = el('input', { class: 'asst-input', type: 'text', placeholder: t(this.i18n, 'assistant_input_placeholder'), maxlength: '500', 'aria-label': t(this.i18n, 'assistant_input_aria') });
      var sendBtn = el('button', { class: 'asst-send', type: 'submit', text: t(this.i18n, 'send') });
      var form = el('form', { class: 'asst-row', autocomplete: 'off' }, [input, sendBtn]);
      var closeBtn = el('button', { class: 'asst-close', type: 'button', 'aria-label': t(this.i18n, 'close'), html: SVG_X });
      var headRow = el('div', { class: 'asst-head-row' }, [el('span', { class: 'asst-title', html: SVG_SPARK + ' ' + t(this.i18n, 'book_by_chat') }), closeBtn]);
      // Persistent AI-disclosure notice (EU AI Act Art. 50(1)) — text must match the
      // "assistant_disclosure" key in internal/i18n/locales/en.json; keep in sync on edit.
      var disclosure = el('p', { class: 'asst-disclosure', role: 'note', text: t(this.i18n, 'assistant_disclosure') });
      var head = el('div', { class: 'asst-head' }, [headRow, disclosure]);
      var panel = el('div', { class: 'asst-panel', role: 'dialog', 'aria-label': t(this.i18n, 'book_by_chat') }, [head, log, form]);
      panel.hidden = true;
      this.root.appendChild(panel);
      this.asstPanel = panel; this.asstLog = log; this.asstInput = input; this.asstSend = sendBtn;
      this.asstMessages = []; this.asstBusy = false;
      closeBtn.addEventListener('click', function () { self.toggleAsst(false); });
      form.addEventListener('submit', function (e) { e.preventDefault(); self.asstSubmit(); });
    }

    toggleAsst(force) {
      if (!this.asstPanel) return;
      var show = (force === undefined) ? this.asstPanel.hidden : force;
      this.asstPanel.hidden = !show;
      if (show) this.asstInput.focus();
    }

    asstAdd(text, cls) {
      var d = el('div', { class: 'asst-msg ' + cls, text: text });
      this.asstLog.appendChild(d);
      this.asstLog.scrollTop = this.asstLog.scrollHeight;
      return d;
    }

    async asstSubmit() {
      var self = this;
      var text = (this.asstInput.value || '').trim();
      if (!text || this.asstBusy) return;
      this.asstMessages.push({ role: 'user', content: text });
      this.asstAdd(text, 'user');
      this.asstInput.value = ''; this.asstBusy = true; this.asstSend.disabled = true;
      var typing = this.asstAdd('…', 'asst-typing');
      var botEl = null, booking = null;
      var onEvent = function (obj) {
        if (obj.type === 'token') {
          if (!botEl) { typing.remove(); botEl = self.asstAdd('', 'bot'); }
          botEl.textContent += obj.text;
          self.asstLog.scrollTop = self.asstLog.scrollHeight;
        } else if (obj.type === 'status') {
          typing.textContent = obj.text;
        } else if (obj.type === 'fallback') {
          if (typing.parentNode) typing.remove();
          self.asstAdd(obj.text, 'note');
        } else if (obj.type === 'done') {
          booking = obj.booking || null;
        }
      };
      try {
        var res = await fetch(BASE + '/v1/event-types/' + encodeURIComponent(this.slug) + '/assistant', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
          body: JSON.stringify({ messages: this.asstMessages, timezone: TZ, language: this.locale }),
        });
        if (!res.ok || !res.body) throw new Error('http ' + res.status);
        var reader = res.body.getReader(), dec = new TextDecoder(), buf = '';
        while (true) {
          var chunk = await reader.read();
          if (chunk.done) break;
          buf += dec.decode(chunk.value, { stream: true });
          var parts = buf.split('\n\n'); buf = parts.pop();
          for (var i = 0; i < parts.length; i++) {
            var line = parts[i].trim();
            if (line.indexOf('data:') !== 0) continue;
            try { onEvent(JSON.parse(line.slice(5).trim())); } catch (e) {}
          }
        }
        if (typing.parentNode) typing.remove();
        if (botEl && botEl.textContent) {
          this.asstMessages.push({ role: 'assistant', content: botEl.textContent });
          if (booking) botEl.className = 'asst-msg ok';
        }
        if (booking) this.dispatchEvent(new CustomEvent('calnode:booked', { bubbles: true, composed: true, detail: booking }));
      } catch (e) {
        if (typing.parentNode) typing.remove();
        this.asstAdd(t(this.i18n, 'assistant_error'), 'note');
      } finally {
        this.asstBusy = false; this.asstSend.disabled = false; this.asstInput.focus();
      }
    }

    // syncDesc clamps the description to 2 lines (via the shared .clamp class) only
    // when the widget is too narrow for the 3-column layout; the toggle shows only
    // when the clamped text overflows. (Local var renamed toggle, not t — t is the
    // module-level i18n lookup function and would otherwise be shadowed here.)
    syncDesc() {
      var d = this.card && this.card.querySelector('.description');
      var toggle = this.card && this.card.querySelector('.desc-toggle');
      if (!d || !toggle) return;
      if (this.cw > 719) { d.classList.remove('clamp'); toggle.classList.remove('visible'); return; }
      if (this.descExpanded) { d.classList.remove('clamp'); toggle.textContent = t(this.i18n, 'show_less'); toggle.classList.add('visible'); return; }
      d.classList.add('clamp');
      toggle.textContent = t(this.i18n, 'show_more');
      toggle.classList.toggle('visible', d.scrollHeight > d.clientHeight + 1);
    }

    calPane() {
      var self = this, st = this.state, first = st.month;
      var grid = el('div', { class: 'cal-grid' });
      this.dow.forEach(function (d) { grid.appendChild(el('div', { class: 'ch', text: d })); });
      for (var i = 0; i < mondayIndex(first); i++) grid.appendChild(el('div', { class: 'cd', text: '' }));
      var days = endOfMonth(first).getDate(), todayKey = ymd(new Date());
      for (var d = 1; d <= days; d++) {
        var key = ymd(new Date(first.getFullYear(), first.getMonth(), d));
        var has = !!st.slotsByDay[key] && key >= todayKey;
        var cls = 'cd' + (has ? ' available' : '') + (st.day === key ? ' sel' : '') + (key === todayKey ? ' today' : '');
        var btn = el('button', { class: cls, text: String(d) });
        if (!has) btn.disabled = true;
        else btn.addEventListener('click', (function (k) { return function () { self.state.day = k; self.state.view = 'pick'; self.render(); }; })(key));
        grid.appendChild(btn);
      }
      var prev = el('button', { 'aria-label': t(this.i18n, 'prev_month_aria'), html: SVG_PREV });
      prev.disabled = !(startOfMonth(first) > startOfMonth(new Date()));
      prev.addEventListener('click', function () { self.nav(-1); });
      var next = el('button', { 'aria-label': t(this.i18n, 'next_month_aria'), html: SVG_NEXT });
      next.addEventListener('click', function () { self.nav(1); });
      var nav = el('div', { class: 'cal-nav' }, [
        el('span', { class: 'month-label', text: first.toLocaleDateString(this.locale, { month: 'long', year: 'numeric' }) }),
        prev, next,
      ]);
      return el('section', { class: 'cal-col' }, [nav, grid, el('p', { class: 'tz-label', text: t(this.i18n, 'times_shown_in') + TZ })]);
    }

    // noticeHint — the minimum-notice explanation, or '' when the event type sets none.
    // The label is translated server-side and arrives on /public: the /slots call carries
    // no language of its own, and rebuilding a plural-aware duration here would duplicate
    // what the server already knows (#20).
    noticeHint() {
      var label = this.info && this.info.min_notice_label;
      return label ? fmt(t(this.i18n, 'min_notice_hint'), [label]) : '';
    }

    // emptyDayText — "Alex has no available times on Mon, 15 Jun", or the date-only form
    // when the event type has several hosts (a group label cannot be the subject of that
    // sentence in any of the shipped locales).
    emptyDayText(dayKeyStr) {
      var hosts = (this.info && this.info.hosts) || [];
      // 'T00:00:00' (not the bare date) so the browser reads it as local midnight rather
      // than UTC, which would name the previous day west of Greenwich.
      var label = new Intl.DateTimeFormat(this.locale || [], {
        weekday: 'long', month: 'long', day: 'numeric'
      }).format(new Date(dayKeyStr + 'T00:00:00'));
      if (hosts.length === 1 && hosts[0].name) {
        return fmt(t(this.i18n, 'no_available_times_host'), [hosts[0].name, label]);
      }
      return fmt(t(this.i18n, 'no_available_times'), [label]);
    }

    rightPane() {
      var self = this, st = this.state;
      var inner;
      var notice = this.noticeHint();
      if (st.view === 'form') inner = this.formView(st.slot);
      else if (st.view === 'confirm') inner = this.confirmView(st.slot);
      else if (st.day) {
        var list = (st.slotsByDay[st.day] || []).slice().sort(function (a, b) { return a.start < b.start ? -1 : 1; });
        var listEl = el('div', { class: 'slots-list' });
        var periodFor = function (slot) {
          var hour = Number(new Intl.DateTimeFormat('en-GB', {timeZone: TZ, hour: 'numeric', hourCycle: 'h23'}).format(new Date(slot.start)));
          return hour < 12 ? 0 : hour < 17 ? 1 : 2;
        };
        var periods = Array.from(new Set(list.map(periodFor))).sort();
        if (periods.indexOf(st.period) === -1) st.period = periods[0];
        var choices = el('div', { class: 'time-periods', role: 'group', 'aria-label': t(this.i18n, 'time_period') });
        periods.forEach(function (period) {
          var button = el('button', { type: 'button', 'data-period': String(period), 'aria-pressed': String(period === st.period), text: t(self.i18n, ['time_morning', 'time_afternoon', 'time_evening'][period]) });
          button.addEventListener('click', function () {
            st.period = period;
            self.render();
            self.shadowRoot.querySelector('[data-period="' + period + '"]').focus();
          });
          choices.appendChild(button);
        });
        var grid = el('div', { class: 'time-grid' });
        if (list.length) listEl.appendChild(choices);
        listEl.appendChild(grid);
        list.filter(function (s) { return periodFor(s) === st.period; }).forEach(function (s) {
          if (s.taken) {
            // Disabled rather than click-guarded: it keeps the same box as a bookable
            // slot and is announced as unavailable instead of read out as a plain time.
            var d = el('button', {
              class: 'slot-btn taken',
              text: timeLabel(s.start, self.locale),
              'aria-label': timeLabel(s.start, self.locale) + ' - ' + t(self.i18n, 'slot_taken'),
            });
            d.disabled = true;
            grid.appendChild(d);
            return;
          }
          var b = el('button', { class: 'slot-btn', text: timeLabel(s.start, self.locale) });
          b.addEventListener('click', function () { self.state.slot = s; self.state.view = 'form'; self.render(); });
          grid.appendChild(b);
        });
        if (!list.length) {
          // Name the day, and the host when there is one: a bare "No available times."
          // never said whether another day would help.
          listEl.appendChild(el('p', { class: 'hint', text: this.emptyDayText(st.day) }));
        }
        if (list.length && !list.some(function (s) { return !s.taken; })) {
          listEl.appendChild(el('p', { class: 'hint', text: t(self.i18n, 'all_times_taken') }));
        }
        // Only on a day the policy actually thinned - including one it emptied, and one
        // that still shows later times, which is the commonest "why can't I see those
        // times" case.
        if (notice && st.noticeDates && st.noticeDates.indexOf(st.day) !== -1) {
          listEl.appendChild(el('p', { class: 'hint notice-hint', text: notice }));
        }
        // Shown whenever an external calendar check failed: the busy data behind
        // this list is incomplete.
        if (st.degraded) {
          listEl.appendChild(el('p', { class: 'hint notice-hint', text: t(self.i18n, 'calendar_degraded_notice') }));
        }
        inner = el('div', {}, [el('p', { class: 'slots-header', text: list[0] ? shortDay(list[0].start, self.locale) : this.dayHeader(st.day) }), listEl]);
      } else {
        // Before a day is chosen. The notice line belongs here as well as in the list: a
        // day the policy emptied completely is greyed out in the calendar, so this is the
        // only place the explanation can be reached.
        var kids = [el('p', { class: 'hint', text: t(this.i18n, 'select_day_hint') })];
        if (notice && st.noticeDates && st.noticeDates.length) {
          kids.push(el('p', { class: 'hint notice-hint', text: notice }));
        }
        inner = el('div', {}, kids);
      }
      return el('section', { class: 'right-col' }, [inner]);
    }

    // dayHeader — the selected day's short label when no slot is available to derive it
    // from, so an empty day still gets the same header as a full one.
    dayHeader(dayKeyStr) {
      if (!dayKeyStr) return '';
      return new Intl.DateTimeFormat(this.locale || [], {
        weekday: 'short', month: 'short', day: 'numeric'
      }).format(new Date(dayKeyStr + 'T00:00:00'));
    }

    // phoneField builds one 'phone' question: a country picker (button + search + list)
    // beside the national number. A DELIBERATE MIRROR of the 'phone' branch of book.html
    // (markup, the booking.css class names, keyboard model, detection order and stored
    // value) and of phonePicker / phoneFallback in its "Phone questions" script: change one,
    // change the other. Two controls, ONE answer: the hidden input holds
    // "+<dial> <digits>" (phoneCombine), so answers[].value stays a plain string and the
    // webhook payload keeps its shape. It stays EMPTY until there is a country AND digits.
    // The picker's state is the ISO code, never the dial code: "1" alone is shared by 25
    // countries (US, CA, DO, PR...).
    //
    // Widget-only differences, all forced by the Shadow DOM or the third-party page: ids
    // are per shadow root (they carry the question id, so two phone questions cannot
    // collide); the outside-press check reads composedPath(), because a document listener
    // sees every press inside the widget retargeted to the <calnode-booking> host; flags
    // use absolute URLs and load as they scroll into the list (IntersectionObserver rooted
    // on the list: native lazy loading measures against the viewport, which the whole list
    // is within the margin of, so it would fetch all 243 on a phone), and one the host
    // page blocks shows its ISO code; the chosen country survives a re-render (back to the
    // times and forward again).
    //
    // S = phoneShared(this.phoneData), or null when the data is missing or unusable: then
    // the button hides and the number is accepted only in international form, stored as
    // "+<digits>" (with no dial table there is no knowing where the code ends, so this is
    // the one case where the value carries no space).
    //
    // Returns {field, num, value, check}: value() is the string to send ('' = no answer);
    // check() is null when the answer is usable, else the control to focus.
    phoneField(q, S) {
      var self = this, i18n = this.i18n;
      var base = 'ans-' + String(q.id).replace(/[^A-Za-z0-9_-]/g, '_');
      var label = el('label', { id: 'lbl-' + base, for: base + '-num', html: esc(q.label) + (q.required ? ' <span class="required-star">*</span>' : '') });
      // First control of the .field, as in book.html, whose collector reads the first input
      // of each field. This widget's own submit calls value() instead of walking the DOM.
      var hidden = el('input', { type: 'hidden', value: '', 'data-phone-hidden': '' });
      var btn = el('button', {
        type: 'button', class: 'phone-cc-btn', id: base + '-cc', 'aria-haspopup': 'listbox', 'aria-expanded': 'false',
        'aria-controls': base + '-list', 'aria-describedby': 'lbl-' + base, 'data-phone-cc': '', html: PHONE_BTN_HTML,
      });
      var flag = btn.querySelector('.phone-flag');
      var nameEl = btn.querySelector('.phone-sr');
      var codeEl = btn.querySelector('.phone-cc-code');
      var num = el('input', { class: 'phone-num', id: base + '-num', type: 'tel', inputmode: 'tel', autocomplete: 'tel-national', maxlength: '20', placeholder: t(i18n, 'phone_number_label'), 'data-phone-num': '' });
      if (q.required) num.required = true;
      // No name and no required on the search box: it is not an answer.
      var search = el('input', {
        class: 'phone-search', id: base + '-search', type: 'text', role: 'combobox', 'aria-expanded': 'true',
        'aria-controls': base + '-list', 'aria-autocomplete': 'list', autocomplete: 'off', autocapitalize: 'off',
        spellcheck: 'false', enterkeyhint: 'done', placeholder: t(i18n, 'phone_country_search'), 'aria-label': t(i18n, 'phone_country_search'),
      });
      var list = el('ul', { class: 'phone-list', id: base + '-list', role: 'listbox', 'aria-label': t(i18n, 'phone_country_label') });
      var empty = el('p', { class: 'phone-empty', role: 'status', text: t(i18n, 'phone_no_results') });
      empty.hidden = true;
      var panel = el('div', { class: 'phone-panel', id: base + '-panel' }, [search, list, empty]);
      panel.hidden = true;
      var picker = el('div', { class: 'phone-picker' }, [el('div', { class: 'phone-row' }, [btn, num]), panel]);
      var field = el('div', { class: 'field' }, [label, hidden, picker]);
      codeEl.textContent = t(i18n, 'phone_country_label');

      if (!S) {
        btn.hidden = true;
        num.setAttribute('autocomplete', 'tel');
        num.placeholder = '+';
        var syncPlain = function () {
          var digits = phoneDigits(num.value);
          hidden.value = phoneIsIntl(num.value) && digits ? '+' + digits : '';
        };
        num.addEventListener('input', syncPlain);
        num.addEventListener('change', syncPlain);
        syncPlain();
        return {
          field: field, num: num,
          value: function () { syncPlain(); return hidden.value; },
          check: function () {
            var digits = phoneDigits(num.value);
            if (!digits) return q.required ? num : null;
            return phoneIsIntl(num.value) && digits.length >= 8 && digits.length <= 15 ? null : num;
          },
        };
      }

      this.phoneSel = this.phoneSel || {};
      var iso = S.dialOf[this.phoneSel[q.id]] ? this.phoneSel[q.id] : S.detected;
      var nodes = null;     // iso -> <li>, created on first open (243 flags are not loaded up front)
      var shown = [];       // the [iso, dial] pairs currently listed, in order
      var active = -1, activeLi = null;

      // As in book.html, phoneCombine gets the number exactly as typed: it drops the
      // separators, recognises a typed "+<dial>" however it is spaced, and reads Arabic,
      // Persian and full-width digits and a full-width plus itself. (Pre-normalising with
      // phoneDigits, which keeps 0-9 only, used to erase such a number entirely.)
      var sync = function () {
        hidden.value = iso ? phoneCombine(S.dialOf[iso], num.value) : '';
      };
      var renderButton = function () {
        var hasFlag = !!iso && !PHONE_NO_FLAG[iso];
        flag.hidden = !hasFlag;
        if (hasFlag) flag.src = phoneFlagURL(iso); else flag.removeAttribute('src');
        btn.classList.toggle('has-flag', hasFlag);
        nameEl.textContent = iso ? S.nameOf(iso) : '';
        codeEl.textContent = iso ? '+' + S.dialOf[iso] : t(i18n, 'phone_country_label');
        btn.title = iso ? S.nameOf(iso) + ' +' + S.dialOf[iso] : '';
      };
      flag.addEventListener('error', function () { flag.hidden = true; btn.classList.remove('has-flag'); });
      var setIso = function (code) {
        if (nodes && nodes[iso]) nodes[iso].setAttribute('aria-selected', 'false');
        iso = code;
        self.phoneSel[q.id] = code;
        if (nodes && nodes[iso]) nodes[iso].setAttribute('aria-selected', 'true');
        renderButton();
        sync();
      };

      var io = null;
      var loadFlag = function (img) { img.src = img.getAttribute('data-src'); img.removeAttribute('data-src'); };
      var buildList = function () {
        nodes = {};
        if (window.IntersectionObserver) {
          io = new IntersectionObserver(function (entries) {
            entries.forEach(function (en) { if (en.isIntersecting) { io.unobserve(en.target); loadFlag(en.target); } });
          }, { root: list, rootMargin: '160px 0px' });
        }
        S.sortedList().forEach(function (c) {
          var code = c[0], mark;
          var li = el('li', { class: 'phone-opt' + (code === S.detected ? ' is-pinned' : ''), id: list.id + '-' + code, role: 'option', 'aria-selected': code === iso ? 'true' : 'false', 'data-iso': code });
          if (PHONE_NO_FLAG[code]) {
            mark = el('span', { class: 'phone-flag phone-flag-none', 'aria-hidden': 'true' });
          } else {
            mark = el('img', { class: 'phone-flag', alt: '', width: '20', height: '15', decoding: 'async' });
            mark.addEventListener('error', function () {
              if (mark.parentNode) mark.parentNode.replaceChild(el('span', { class: 'phone-flag phone-flag-none', 'aria-hidden': 'true', text: code }), mark);
            });
            if (io) { mark.setAttribute('data-src', phoneFlagURL(code)); io.observe(mark); }
            else { mark.setAttribute('loading', 'lazy'); mark.src = phoneFlagURL(code); } // loading before src, or it does not apply
          }
          li.appendChild(mark);
          li.appendChild(el('span', { class: 'phone-opt-name', text: S.nameOf(code) }));
          li.appendChild(el('span', { class: 'phone-opt-dial', text: '+' + c[1] }));
          nodes[code] = li;
        });
      };
      // Scroll inside the list only: scrollIntoView would scroll the host page too.
      var scrollToLi = function (li) {
        var top = li.offsetTop, bottom = top + li.offsetHeight;
        if (top < list.scrollTop) list.scrollTop = top;
        else if (bottom > list.scrollTop + list.clientHeight) list.scrollTop = bottom - list.clientHeight;
      };
      var setActive = function (i, scroll) {
        if (activeLi) activeLi.classList.remove('is-active');
        active = (i >= 0 && i < shown.length) ? i : -1;
        activeLi = active >= 0 ? nodes[shown[active][0]] : null;
        if (activeLi) {
          activeLi.classList.add('is-active');
          search.setAttribute('aria-activedescendant', activeLi.id);
          if (scroll !== false) scrollToLi(activeLi);
        } else {
          search.removeAttribute('aria-activedescendant');
        }
      };
      var indexOfIso = function (code) {
        for (var i = 0; i < shown.length; i++) if (shown[i][0] === code) return i;
        return -1;
      };
      // Existing nodes are re-appended in the filter's order: no flag is re-requested per
      // keystroke, and the search text only ever reaches textContent.
      var renderList = function () {
        var qs = search.value;
        shown = qs.trim() ? phoneFilter(S.sortedList(), qs, S.nameOf) : S.sortedList();
        var frag = document.createDocumentFragment();
        shown.forEach(function (c) { if (nodes[c[0]]) frag.appendChild(nodes[c[0]]); });
        list.textContent = '';
        list.appendChild(frag);
        list.hidden = shown.length === 0;
        empty.hidden = shown.length > 0;
      };

      var downEvt = window.PointerEvent ? 'pointerdown' : 'mousedown';
      var onOutside = function (e) {
        var path = e.composedPath ? e.composedPath() : [e.target];
        if (path.indexOf(picker) === -1) close(false);
      };
      var open = function () {
        if (!panel.hidden) return;
        if (!nodes) buildList();
        search.value = '';
        renderList();
        panel.hidden = false;
        picker.classList.add('is-open');
        btn.setAttribute('aria-expanded', 'true');
        var i = indexOfIso(iso);
        setActive(i >= 0 ? i : 0);
        search.focus();
        document.addEventListener(downEvt, onOutside, true);
      };
      var close = function (focusBtn) {
        if (panel.hidden) return;
        panel.hidden = true;
        picker.classList.remove('is-open');
        btn.setAttribute('aria-expanded', 'false');
        document.removeEventListener(downEvt, onOutside, true);
        if (focusBtn) btn.focus();
      };
      var choose = function (code) {
        setIso(code);
        close(false);
        num.focus();
      };

      btn.addEventListener('click', function () { if (panel.hidden) open(); else close(true); });
      btn.addEventListener('keydown', function (e) {
        if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && panel.hidden) { e.preventDefault(); open(); }
        // stopPropagation: in popup mode Escape also closes the whole popup from a document
        // listener (keydown is composed and leaves the shadow root), form and all.
        else if (e.key === 'Escape' && !panel.hidden) { e.preventDefault(); e.stopPropagation(); close(true); }
      });
      search.addEventListener('input', function () { renderList(); list.scrollTop = 0; setActive(shown.length ? 0 : -1); });
      search.addEventListener('keydown', function (e) {
        if (e.key === 'ArrowDown') { e.preventDefault(); if (shown.length) setActive(Math.min(active + 1, shown.length - 1)); }
        else if (e.key === 'ArrowUp') { e.preventDefault(); if (shown.length) setActive(Math.max(active - 1, 0)); }
        // Always swallowed: in a <form>, Enter in a text box is an implicit submit, which
        // would run the booking handler with a half-filled form.
        else if (e.key === 'Enter') { e.preventDefault(); if (activeLi) choose(activeLi.getAttribute('data-iso')); }
        else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(true); }
        // The panel vanishes, so land where the visitor was heading: forward to the number,
        // back to the country button.
        else if (e.key === 'Tab') { e.preventDefault(); close(false); (e.shiftKey ? btn : num).focus(); }
      });
      list.addEventListener('click', function (e) {
        var li = e.target.closest ? e.target.closest('.phone-opt') : null;
        if (li) choose(li.getAttribute('data-iso'));
      });
      list.addEventListener('mousemove', function (e) {
        var li = e.target.closest ? e.target.closest('.phone-opt') : null;
        if (li && li !== activeLi) setActive(indexOfIso(li.getAttribute('data-iso')), false);
      });

      // A number typed or pasted in international form ("+34 612...") carries its own code,
      // which phoneCombine already stores in place of the picked one; this moves the picker
      // to match, so the flag on screen is the country the webhook gets. Same probe as
      // book.html: with an empty dial phoneCombine answers only for "+" input (full-width
      // too), and only once a digit follows a complete code, hence the "0": the flag
      // follows as soon as the code is typed.
      var onNumber = function () {
        var own = phoneCombine('', num.value + '0');
        if (own) {
          var found = phoneCountryFor(S, own.slice(1, own.indexOf(' ')), iso);
          if (found && found !== iso) setIso(found);
        }
        sync();
      };
      num.addEventListener('input', onNumber);
      // 'change' as well as 'input': a value can land without an 'input' (browser or
      // password-manager autofill, which autocomplete="tel-national" invites).
      num.addEventListener('change', onNumber);

      renderButton();
      onNumber(); // a restored country or number is reflected before any event fires
      return {
        field: field, num: num,
        value: function () { onNumber(); return hidden.value; },
        // Mirror of the phoneChecks validator in book.html: it judges the stored string,
        // i.e. exactly what would be sent.
        check: function () {
          onNumber();
          if (!PHONE_ANY_DIGIT.test(num.value)) return q.required ? num : null;
          if (!iso) return btn;
          var m = /^\+(\d+) (\d+)$/.exec(hidden.value);
          // No number after the code, or a typed "+<code>" the data does not know (so the
          // picker could not follow it): either way not the chosen country's number.
          if (!m || m[1] !== S.dialOf[iso]) return num;
          // At least 6 national digits (the server's validPhone floor counts the code too),
          // and no more than E.164's 15 in total.
          if (m[2].length < 6 || m[1].length + m[2].length > 15) return num;
          return null;
        },
      };
    }

    formView(slot) {
      var self = this;
      var back = el('button', { class: 'back-btn', html: SVG_BACK + ' ' + t(this.i18n, 'back') });
      back.addEventListener('click', function () { self.state.view = 'pick'; self.render(); });
      var form = el('form', { novalidate: 'novalidate' });
      var hp = el('input', { type: 'text', name: 'hp_extra', tabindex: '-1', autocomplete: 'off' });
      form.appendChild(el('div', { 'aria-hidden': 'true', style: 'position:absolute;left:-5000px;height:0;width:0;overflow:hidden;' }, [hp]));
      var name = el('input', { type: 'text', required: 'required', autocomplete: 'name', placeholder: t(this.i18n, 'name_placeholder') });
      var email = el('input', { type: 'email', required: 'required', autocomplete: 'email', placeholder: t(this.i18n, 'email_placeholder') });
      form.appendChild(el('div', { class: 'field' }, [el('label', { text: t(this.i18n, 'name_label') }), name]));
      form.appendChild(el('div', { class: 'field' }, [el('label', { text: t(this.i18n, 'email_label') }), email]));
      var phone = el('input', { type: 'tel', maxlength: '40', autocomplete: 'tel', id: 'phone-call-number' });
      if (this.info.allow_phone_call) form.appendChild(el('div', { class: 'field' }, [el('label', { for: 'phone-call-number', text: t(this.i18n, 'phone_call_number') }), phone]));
      var qInputs = [];
      this.questions.forEach(function (q) {
        // ph stays null for every other type; a non-null ph (the picker built by phoneField)
        // is what marks this entry as a phone question further down.
        var inp, field, ph = null;
        if (q.type === 'checkbox') {
          inp = el('input', { type: 'checkbox' });
          // Native required on a checkbox means "must be ticked" — the browser blocks
          // submit with its own prompt. This was the only branch not setting it, so a
          // required consent box relied entirely on the server to reject it.
          if (q.required) inp.required = true;
          field = el('div', { class: 'field' }, [el('div', { class: 'field-checkbox' }, [inp, el('label', { html: esc(q.label) + (q.required ? ' <span class="required-star">*</span>' : '') })])]);
        } else if (q.type === 'select') {
          inp = el('select', {}, [el('option', { value: '', text: t(self.i18n, 'choose_option') })].concat((q.options || []).map(function (o) { return el('option', { value: o, text: o }); })));
          if (q.required) inp.required = true;
          field = el('div', { class: 'field' }, [el('label', { html: esc(q.label) + (q.required ? ' <span class="required-star">*</span>' : '') }), inp]);
        } else if (q.type === 'phone') {
          // Country picker + national number (phoneField), ONE stored answer. The shared
          // country state is computed once per widget. If that or the picker throws (an
          // Intl quirk in some WebView), the field degrades to the plain international
          // input: render() has already emptied .wrap, so an exception here would leave
          // the widget blank right after the visitor picked a time.
          if (self.phoneS === undefined) {
            try { self.phoneS = self.phoneData ? phoneShared(self.phoneData, self.locale) : null; } catch (err) { self.phoneS = null; }
          }
          try { ph = self.phoneField(q, self.phoneS); } catch (err) { ph = self.phoneField(q, null); }
          inp = ph.num;
          field = ph.field;
        } else {
          inp = el('textarea', { rows: '3' });
          if (q.required) inp.required = true;
          field = el('div', { class: 'field' }, [el('label', { html: esc(q.label) + (q.required ? ' <span class="required-star">*</span>' : '') }), inp]);
        }
        form.appendChild(field);
        qInputs.push({ q: q, inp: inp, ph: ph });
      });
      var errBox = el('p', { class: 'form-error' });
      var cta = el('button', { class: 'btn-primary', type: 'submit', text: t(this.i18n, 'confirm_booking') });
      form.appendChild(errBox); form.appendChild(cta);
      form.addEventListener('submit', function (e) {
        e.preventDefault();
        errBox.textContent = '';
        cta.disabled = true; cta.textContent = t(self.i18n, 'confirming');
        var answers = [];
        // A required phone question left blank is caught here, before the request, with
        // the same string book.html shows. This form is novalidate, so inp.required is
        // inert and the widget would otherwise round-trip to the server and surface its
        // differently-worded err_required_field — the number field would behave one way
        // on the booking page and another inside the customer's site. Deliberately
        // phone-only: the other types still rely on the server, as they always have.
        var badPhone = null;
        qInputs.forEach(function (x) {
          // Phone questions send ONE combined string, "+51 987654321" (phoneCombine) -
          // byte-identical to what book.html submits. An empty number omits the answer
          // entirely: a bare dial code would look non-empty to the server's required check
          // and fire the WhatsApp webhook at a number that does not exist. check() holds
          // back the same answers book.html's phoneChecks do: a required one left blank,
          // digits with no country, a "+<code>" that is not the chosen country's, and a
          // number too short or too long to be real; badPhone is the control to fix.
          if (x.ph) {
            var bad = x.ph.check();
            if (bad) { if (!badPhone) badPhone = bad; return; }
            var v = x.ph.value();
            if (v) answers.push({ question_id: x.q.id, value: v });
            return;
          }
          // Checkboxes always send an explicit yes/no, matching book.html — this used to
          // send 'Yes' or omit the answer entirely, which both diverged from the booking
          // page's stored value and made "declined" indistinguishable from "never asked".
          if (x.inp.type === 'checkbox') {
            answers.push({ question_id: x.q.id, value: x.inp.checked ? 'yes' : 'no' });
          } else if (x.inp.value) {
            answers.push({ question_id: x.q.id, value: x.inp.value });
          }
        });
        if (badPhone) {
          errBox.textContent = t(self.i18n, 'required_fields_error');
          cta.disabled = false; cta.textContent = t(self.i18n, 'confirm_booking');
          badPhone.focus();
          return;
        }
        fetch(BASE + '/v1/bookings', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ event_type_slug: self.slug, start_at: slot.start, name: name.value.trim(), email: email.value.trim().toLowerCase(), phone: phone.value.trim(), timezone: TZ, language: self.locale, hp_extra: hp.value, answers: answers }),
        }).then(function (r) {
          return r.json().then(function (data) { return { ok: r.ok, status: r.status, data: data }; });
        }).then(function (res) {
          // 409 = the slot went while the form was open. Substitute our own translated
          // copy, matching book.html/manage.html — this is the most common booking
          // failure, and it used to surface the API's raw English message here.
          if (res.status === 409) throw new Error(t(self.i18n, 'slot_taken_error'));
          // Other failures: the API's message is translated server-side from the
          // "language" field sent above (booker-reachable errors only — malformed-request
          // messages stay English for API consumers), so showing it directly is correct.
          if (!res.ok) throw new Error(res.data && res.data.error ? res.data.error : t(self.i18n, 'booking_failed_error'));
          // Paid event types: the server returns a Stripe Checkout URL. Send the visitor
          // there (top window, so it isn't trapped in the host page's frame).
          if (res.data && res.data.checkout_url) { (window.top || window).location.href = res.data.checkout_url; return; }
          self.state.view = 'confirm'; self.render();
          self.dispatchEvent(new CustomEvent('calnode:booked', { bubbles: true, composed: true, detail: res.data }));
        }).catch(function (err) {
          errBox.textContent = err.message || t(self.i18n, 'booking_failed_error');
          cta.disabled = false; cta.textContent = t(self.i18n, 'confirm_booking');
        });
      });
      return el('div', {}, [back, el('p', { class: 'slot-label', text: shortDay(slot.start, this.locale) + ' · ' + timeLabel(slot.start, this.locale) }), form]);
    }

    confirmView(slot) {
      return el('div', {}, [
        el('div', { class: 'confirm-icon', html: SVG_CHECK }),
        el('div', { class: 'confirm-view' }, [
          el('h3', { text: t(this.i18n, 'booking_confirmed') }),
          el('p', { class: 'when', text: shortDay(slot.start, this.locale) + ' · ' + timeLabel(slot.start, this.locale) }),
          el('p', { class: 'sub', text: t(this.i18n, 'confirmation_email_sent') }),
        ]),
      ]);
    }

    nav(delta) {
      this.state.month = addMonths(this.state.month, delta);
      this.state.day = null; this.state.view = 'pick';
      var self = this;
      this.loadMonth().then(function () { self.render(); });
    }

    // applyStep toggles which panes show when narrow (step-flow). Wide = all visible.
    applyStep() {
      if (!this.card) return;
      this.card.classList.remove('step', 'step-cal', 'step-right');
      if (!this.narrow) return;
      this.card.classList.add('step');
      // calendar step = day not yet chosen and not in form/confirm; else right pane.
      var onRight = this.state.view === 'form' || this.state.view === 'confirm' || (this.state.view === 'pick' && this.state.day);
      this.card.classList.add(onRight ? 'step-right' : 'step-cal');
    }

    render() {
      var self = this;
      this.wrap.innerHTML = '';
      this.card = el('div', { class: 'card' }, [this.infoPane(), this.calPane(), this.rightPane()]);
      // In step-flow, a slots/form view needs a back-to-calendar affordance.
      if (this.narrow && (this.state.view === 'pick' && this.state.day)) {
        var rc = this.card.querySelector('.right-col');
        var back = el('button', { class: 'back-btn', html: SVG_BACK + ' ' + t(this.i18n, 'back') });
        back.addEventListener('click', function () { self.state.day = null; self.render(); });
        rc.insertBefore(back, rc.firstChild);
      }
      var toggle = this.card.querySelector('.desc-toggle');
      if (toggle) toggle.addEventListener('click', function () { self.descExpanded = !self.descExpanded; self.syncDesc(); });
      this.wrap.appendChild(this.card);
      this.wrap.appendChild(el('div', { class: 'powered', html: t(this.i18n, 'powered_by') + ' <a href="https://calnode.com" target="_blank" rel="noopener">Calnode</a>' }));
      this.applyStep();
      this.cw = this.wrap.getBoundingClientRect().width || this.cw;
      requestAnimationFrame(function () { self.syncDesc(); });
    }
  }

  customElements.define('calnode-booking', CalnodeBooking);

  // ── popup mode (isolated in its own Shadow DOM so host CSS can't break it) ──
  var POPUP_STYLE = '' +
    ':host{all:initial;}' +
    '*{box-sizing:border-box;}' +
    '.ovl{position:fixed;inset:0;background:rgba(15,23,42,.55);display:flex;align-items:flex-start;justify-content:center;overflow:auto;padding:5vh 16px;}' +
    '.wrap{position:relative;width:100%;max-width:860px;}' +
    '.x{position:absolute;top:14px;right:14px;z-index:2;width:32px;height:32px;border-radius:50%;border:none;background:#fff;box-shadow:0 1px 5px rgba(15,23,42,.2);cursor:pointer;color:#334155;display:flex;align-items:center;justify-content:center;}' +
    '.x:hover{background:#f1f5f9;}' +
    '@media (max-width:560px){.ovl{padding:0;}.wrap{max-width:none;min-height:100%;}}';

  // lang: optional explicit language override, same semantics as the inline
  // <calnode-booking lang=""> attribute — for popup mode there's no persistent element
  // to put it on ahead of time, so it's read off the trigger button instead (see
  // wirePopups) and forwarded here. window.Calnode.openPopup(slug, lang) also accepts
  // it directly for callers driving the popup from their own JS rather than a
  // data-calnode-popup button.
  function openPopup(slug, lang) {
    var hostEl = el('div', {});
    hostEl.setAttribute('style', 'position:fixed;inset:0;z-index:2147483647;');
    var sr = hostEl.attachShadow({ mode: 'open' });
    sr.appendChild(el('style', { text: POPUP_STYLE }));
    var widget = document.createElement('calnode-booking');
    widget.setAttribute('slug', slug);
    widget.setAttribute('data-modal', '');
    if (lang) widget.setAttribute('lang', lang);
    // This popup-chrome close button is created synchronously, before the inner
    // <calnode-booking> has fetched /public and resolved a locale, so it starts English.
    // The widget sets its own lang attribute once loaded (and has populated .i18n by
    // then — it assigns i18n first), so re-label off that mutation: a screen reader on a
    // Spanish booking then announces "Cerrar" rather than "Close". If the widget never
    // loads, the observer simply never fires and the English label stands.
    var close = el('button', { class: 'x', html: SVG_X, 'aria-label': 'Close' });
    new MutationObserver(function (_, obs) {
      if (!widget.i18n) return;
      close.setAttribute('aria-label', t(widget.i18n, 'close'));
      obs.disconnect();
    }).observe(widget, { attributes: true, attributeFilter: ['lang'] });
    var overlay = el('div', { class: 'ovl' }, [el('div', { class: 'wrap' }, [close, widget])]);
    function shut() { hostEl.remove(); document.removeEventListener('keydown', onKey); }
    function onKey(e) { if (e.key === 'Escape') shut(); }
    overlay.addEventListener('click', function (e) { if (e.target === overlay) shut(); });
    close.addEventListener('click', shut);
    document.addEventListener('keydown', onKey);
    sr.appendChild(overlay);
    document.body.appendChild(hostEl);
  }

  function wirePopups(scope) {
    (scope || document).querySelectorAll('[data-calnode-popup]:not([data-calnode-wired])').forEach(function (b) {
      b.setAttribute('data-calnode-wired', '1');
      b.addEventListener('click', function (e) { e.preventDefault(); openPopup(b.getAttribute('data-calnode-popup'), b.getAttribute('lang')); });
    });
  }
  if (document.readyState !== 'loading') wirePopups();
  else document.addEventListener('DOMContentLoaded', function () { wirePopups(); });
  window.Calnode = { openPopup: openPopup, wirePopups: wirePopups };
})();
