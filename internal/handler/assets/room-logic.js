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
      // host always; attendees only when allowed. Never where the browser cannot share a screen
      // at all (every iPhone browser has no getDisplayMedia): the button did nothing there.
      screenVisible: (host || !!s.allowShare) && s.screenSupported !== false,
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
    room_error_not_configured: 'Video meetings aren\'t available on this site.',
    room_preview_tap_to_play: 'Tap here to see your camera',
    room_media_denied_ios: 'Your iPhone did not allow the camera or microphone. Tap the "aA" (or menu) icon next to the web address, choose "Website Settings", set Camera and Microphone to "Allow", then tap "Reload page".',
    room_media_denied: 'The browser did not allow the camera or microphone. Tap the lock icon next to the web address, allow Camera and Microphone, then tap "Reload page".',
    room_media_busy: 'Another app is using the camera or microphone (for example WhatsApp or FaceTime). Close it and tap "Try again".',
    room_media_not_found: 'No camera or microphone was found on this device. Check it and tap "Try again".',
    room_media_unsupported: 'This browser cannot use the camera. Open the link in Safari (iPhone) or Chrome (Android).',
    room_media_inapp: 'You opened the meeting inside another app, and it does not let us use the camera. Open the link in Safari (iPhone) or Chrome (Android): tap the ··· menu and choose "Open in browser", or copy the link and paste it there.',
    room_media_failed: 'The camera or microphone could not be turned on. Tap "Try again".',
    room_media_retry: 'Try again',
    room_media_reload: 'Reload page',
    room_link_copied: 'Link copied. Paste it in Safari or Chrome.',
    room_video_paused: 'Video paused: weak connection'
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

  // ---- Camera / microphone / playback (iPhone fixes) -----------------------------------------
  // Pure helpers for the media code in livekit-room.js. They take the user agent and the
  // error as plain values, so the iPhone and in-app-browser branches are testable in node.

  // isIOS — iPhone/iPad/iPod, including an iPad that reports itself as a Mac ("MacIntel" with
  // a touch screen). Every browser on iOS is WebKit underneath (Chrome = CriOS, Firefox =
  // FxiOS, the WhatsApp/Instagram/Facebook in-app views), and the LiveKit SDK 2.7.5 only
  // applies its Safari video workarounds when the browser NAME is "Safari", so the room keys
  // its own iOS handling on this instead.
  function isIOS(ua, platform, maxTouchPoints) {
    if (/iPhone|iPad|iPod/i.test(ua || '')) return true;
    return platform === 'MacIntel' && Number(maxTouchPoints) > 1;
  }

  // inAppBrowser — non-empty when the page is open inside another app's built-in browser
  // (Facebook, Instagram, Messenger, TikTok, LINE, WeChat, Snapchat, WhatsApp). Those views
  // often cannot use the camera at all, or refuse the microphone; the room then tells the
  // person to open the link in Safari / Chrome instead of failing silently.
  function inAppBrowser(ua) {
    var s = ua || '';
    if (/FBAN|FBAV|FB_IAB|FBIOS|FB4A|MESSENGER/i.test(s)) return 'facebook';
    if (/Instagram/i.test(s)) return 'instagram';
    if (/musical_ly|BytedanceWebview|TikTok/i.test(s)) return 'tiktok';
    if (/\bLine\//i.test(s)) return 'line';
    if (/MicroMessenger/i.test(s)) return 'wechat';
    if (/Snapchat/i.test(s)) return 'snapchat';
    if (/WhatsApp/i.test(s)) return 'whatsapp';
    return '';
  }

  // mediaProblem — what to tell the person when the camera or microphone could not start,
  // from the error getUserMedia (through the SDK, which passes it on unchanged) rejected with.
  // Returns null for no error, else { key, action }: key = the room_* message, action = what
  // the button under it does — 'again' (ask for the device again, inside the tap), 'reload'
  // (Safari does not ask twice on one page: after a "No permitir" only a reload shows the
  // question again) or 'none' (nothing on this page can fix it: open the link elsewhere).
  // env = { ios, inApp }.
  function mediaProblem(err, env) {
    if (!err) return null;
    var e = env || {};
    var name = String(err.name || '');
    var msg = String(err.message || '');
    var denied = /^(NotAllowedError|PermissionDeniedError|SecurityError)$/.test(name) ||
      (!name || name === 'Error') && /permission|denied|not allowed/i.test(msg);
    if (denied) {
      if (e.inApp) return { key: 'room_media_inapp', action: 'none' };
      return { key: e.ios ? 'room_media_denied_ios' : 'room_media_denied', action: 'reload' };
    }
    if (/^(NotReadableError|TrackStartError|AbortError)$/.test(name)) return { key: 'room_media_busy', action: 'again' };
    if (/^(NotFoundError|DevicesNotFoundError|OverconstrainedError|ConstraintNotSatisfiedError)$/.test(name)) {
      return { key: 'room_media_not_found', action: 'again' };
    }
    // No navigator.mediaDevices at all (an in-app view, an old browser, a non-HTTPS page):
    // the SDK then throws a TypeError reading getUserMedia of undefined.
    if (name === 'TypeError' || name === 'DeviceUnsupportedError' || /mediaDevices|getUserMedia/.test(msg)) {
      return { key: e.inApp ? 'room_media_inapp' : 'room_media_unsupported', action: 'none' };
    }
    return { key: e.inApp ? 'room_media_inapp' : 'room_media_failed', action: e.inApp ? 'none' : 'again' };
  }

  // captureOptions — the constraints for a camera (or mic) request. A device the person
  // picked in the list is used as given (a bare, "ideal" deviceId, never {exact}); otherwise
  // the camera asks for the FRONT one (facingMode 'user'), so an iPhone never opens a back or
  // virtual multi-lens camera ("Cámara triple posterior") just because it is listed first.
  function captureOptions(kind, picked, deviceId) {
    if (picked && deviceId) return { deviceId: deviceId };
    return kind === 'video' ? { facingMode: 'user' } : {};
  }

  // playGate — whether the big "Toca aquí para ver y escuchar a los demás" button shows:
  // the browser blocked remote video (iPhone Low Power Mode) or audio, or one of our own
  // video elements refused to play. It goes away once a tap starts everything.
  function playGate(s) {
    return s.canVideo === false || s.canAudio === false || !!s.localBlocked;
  }

  // acquireTracks — the prejoin camera + mic. want = { video, audio }; make = { both, video,
  // audio }, each a function returning a promise (the SDK's createLocalTracks /
  // createLocalVideoTrack / createLocalAudioTrack, already bound to their options). Resolves to
  // { video, audio, errs: { video, audio } } and never rejects.
  // Both devices go in ONE request first, so an iPhone asks one question ("allow camera and
  // microphone") instead of two. When that request fails - for ANY reason, a refusal included -
  // each device is asked for on its own: browsers reject the combined request when either
  // device is blocked, and a person who blocked only the camera must still join with the mic
  // (and the other way round). A device whose permission is really denied fails again at once,
  // with no second question, so the per-device retry costs nothing.
  function acquireTracks(want, make) {
    var out = { video: null, audio: null, errs: { video: null, audio: null } };
    var w = want || {}, m = make || {};
    var first = (w.video && w.audio && typeof m.both === 'function')
      ? Promise.resolve().then(m.both).then(function (tracks) {
        (tracks || []).forEach(function (tr) {
          if (!tr) return;
          if (tr.kind === 'video' && !out.video) out.video = tr;
          else if (tr.kind === 'audio' && !out.audio) out.audio = tr;
        });
      }, function () { /* retried per device below */ })
      : Promise.resolve();
    function one(kind) {
      if (!w[kind] || out[kind]) return Promise.resolve();
      return Promise.resolve().then(m[kind]).then(function (tr) { out[kind] = tr || null; }, function (e) { out.errs[kind] = e; });
    }
    // In sequence, not in parallel: two getUserMedia calls at once race on iOS.
    return first.then(function () { return one('video'); }).then(function () { return one('audio'); })
      .then(function () { return out; });
  }

  // controlOn — what the in-room mic / camera button shows. While our prejoin tracks are still
  // being published the SDK reports the device as off, and a button drawn "off" invites a tap
  // that would open a SECOND camera or mic next to the one being published (on an iPhone that
  // second capture ends or blacks out the first). So until the publish settles the button shows
  // what the person chose in the prejoin; afterwards, what the SDK really has.
  function controlOn(publishing, intended, actual) {
    return publishing ? !!intended : !!actual;
  }

  return {
    amHost: amHost, nextIsHost: nextIsHost, hostUi: hostUi,
    fmt: fmt, EN: EN, translate: translate, tokenErrorKey: tokenErrorKey,
    isIOS: isIOS, inAppBrowser: inAppBrowser, mediaProblem: mediaProblem,
    captureOptions: captureOptions, playGate: playGate,
    acquireTracks: acquireTracks, controlOn: controlOn
  };
});
