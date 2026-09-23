// room-logic.js — the PURE decision logic for the LiveKit room (no DOM, no SDK), extracted so it
// can be unit-tested (see room-logic.test.js). It's served concatenated ahead of livekit-room.js
// (so `RoomLogic` is a page global there) and is also require()-able by the node tests. This is
// the fragile host/consent/screen-share logic that previously lived inline and caused bugs.
// Also the room's string lookup (fmt / translate / EN / tokenErrorKey) — see the bottom.
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.RoomLogic = factory();
})(typeof self !== 'undefined' ? self : this, function () {
  // amHost — I'm the host if my live flag says so, OR my room metadata is "host" right now.
  function amHost(s) {
    return !!(s.isHost || s.hostMeta);
  }

  // nextIsHost — how a ParticipantMetadataChanged updates MY host status. Upgrade on "host"
  // (reassigned/promoted), downgrade only on the explicit "attendee" demote; a transient/empty
  // value must NOT change it (that flake is what stripped a host's controls before).
  function nextIsHost(cur, metadata) {
    if (metadata === 'host') return true;
    if (metadata === 'attendee') return false;
    return cur;
  }

  // hostUi — derive EVERY host/consent/screen-share UI flag from one state snapshot. Pure: the
  // caller applies these booleans to the DOM. Keeps the "what shows when" rules in one tested place.
  function hostUi(s) {
    var host = amHost(s);
    return {
      host: host,
      recordVisible: host && !!s.recordingAvailable, // host + instance can record
      screenVisible: host || !!s.allowShare,         // host always; attendees only when allowed
      gearVisible: !!s.hostCapable || host,          // host now, OR owner who can reclaim
      hostActions: host,                             // share toggle + make-host (active host only)
      reclaimVisible: !!s.hostCapable && !host,      // stepped-down owner
      consentPrompt: !!s.recording && !s.consentDecided && !host // attendee acknowledges recording
    };
  }

  // ---- Translation (i18n) ------------------------------------------------------------------
  // The room is translated by the SAME mechanism as book.html/manage.html: livekit_room.go
  // resolves the visitor's locale per request (Accept-Language + ?lang= + the operator's
  // fallback, internal/handler/i18n.go) and injects that locale's finished string table as
  // window.__CALNODE_I18N, built from internal/i18n/locales/<code>.json. There is no second
  // translation system here: this file only LOOKS UP what the server sent.

  // fmt — DELIBERATE MIRROR of BookingLogic.fmt in booking-logic.js (and of the fmt copy in
  // embed.js). The room's asset is room-logic.js + livekit-room.js concatenated
  // (livekit_room.go), so BookingLogic is undefined here and the helper is copied, not shared.
  // Keep the three in step; room-logic.test.js checks this copy against BookingLogic.fmt.
  // Supports exactly %s (in order) and the indexed %[n]s that lets a translation reorder its
  // arguments. Deliberately NOT a printf. A missing argument renders as '' rather than
  // leaving a format verb in front of a customer.
  function fmt(template, args) {
    var list = args || [];
    var next = 0;
    return String(template).replace(/%(?:\[(\d+)\])?s/g, function (_match, index) {
      var pick = index ? Number(index) - 1 : next++;
      var value = list[pick];
      return value === undefined || value === null ? '' : String(value);
    });
  }

  // EN — the English the room JS showed before it was translated, for every key livekit-room.js
  // looks up. It is only the safety net for a table that is missing or lacks a key (a stale
  // cached page, a locale file behind the code): the customer then reads English, never a raw
  // "room_..." key. It MUST match internal/i18n/locales/en.json — room-logic.test.js checks
  // both that and that every 'room_...' literal in livekit-room.js has an entry here.
  // Text the Go template renders ({{call .T "room_..."}}) is not listed: Go falls back itself.
  var EN = {
    room_camera_unavailable: 'Camera unavailable',
    room_preview_camera_off: 'Camera off',
    room_toggle_camera_on: 'Camera on',
    room_toggle_camera_off: 'Camera off',
    room_toggle_mic_on: 'Mic on',
    room_toggle_mic_off: 'Mic off',
    room_device_fallback: 'Device %s',
    room_joining: 'Joining…',
    room_chat_you: 'You',
    room_host_menu_guests_can_share: 'Guests can share screen',
    room_host_menu_allow_guests_share: 'Allow guests to share screen',
    room_host_menu_no_one_else: 'No one else here yet',
    room_participant: 'Participant',
    room_record_start: 'Record meeting',
    room_record_stop: 'Stop recording',
    room_host_badge: 'Host',
    room_tile_you: '%s (you)',
    room_guest: 'Guest',
    room_consent_spoken: 'This meeting is being recorded.',
    room_error_link_invalid: 'This meeting link is invalid or has expired.',
    room_error_missing_token: 'This meeting link is missing its access token.',
    room_error_token: 'Could not get a meeting token.',
    room_error_connect: 'Could not connect to the meeting server.',
    room_error_library: 'Video library failed to load.',
    room_error_not_configured: 'Video meetings aren\'t available on this site.'
  };

  // translate — the pure core of the room's t(): the page's table first, then EN, then (only
  // if a key is in neither, a programming error the tests guard against) the key itself —
  // the same order as internal/i18n.Locale.T. An empty string counts as missing, as in
  // book.html's t(). Then fmt's %s / %[n]s substitution, and nothing more.
  function translate(table, key, args) {
    var s = table && typeof table[key] === 'string' && table[key] ? table[key]
      : Object.prototype.hasOwnProperty.call(EN, key) ? EN[key] : key;
    return fmt(s, args);
  }

  // tokenErrorKey — which message a failed POST /v1/livekit/token shows. The server's `error`
  // text is English and technical ("livekit: bad room token signature") and stays that way
  // because other API clients read it, so the room never displays it: it picks a translated
  // message from the HTTP status. 403 = the room token failed verification (bad, expired or
  // malformed link); 404 = LiveKit isn't configured on this instance; anything else — a 4xx/5xx,
  // a non-JSON proxy page, a network failure (status undefined) — is the generic message.
  function tokenErrorKey(status) {
    if (status === 403) return 'room_error_link_invalid';
    if (status === 404) return 'room_error_not_configured';
    return 'room_error_token';
  }

  return {
    amHost: amHost, nextIsHost: nextIsHost, hostUi: hostUi,
    fmt: fmt, EN: EN, translate: translate, tokenErrorKey: tokenErrorKey
  };
});
