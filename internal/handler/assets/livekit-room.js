/* Calnode built-in video room — vanilla JS over the LiveKit browser SDK (global LivekitClient).
 *
 * Flow: read the opaque room token (?t) from the URL → prejoin (name + camera/mic preview +
 * device pick) → POST /v1/livekit/token to exchange it for a real LiveKit access token →
 * connect, publish, and render a participant grid with mic/cam/screen-share/leave controls,
 * plus ephemeral chat, recording (with the consent notice) and the host menu.
 *
 * Text: every string this file shows goes through t('room_...'), which reads the table the
 * server injected (window.__CALNODE_I18N, the visitor's locale — see RoomLogic.translate). */
(function () {
  'use strict';
  var LK = window.LivekitClient;
  var $ = function (id) { return document.getElementById(id); };
  var I18N = window.__CALNODE_I18N || {};
  var LOCALE = document.documentElement.lang || 'en';
  // t(key, ...args): translated text, %s / %[n]s filled from args. Falls back to the English
  // in RoomLogic.EN, never to the raw key. NB: don't name a local variable `t` in a function
  // that calls t() — `var` hoisting would shadow it for the whole function.
  function t(key) { return RoomLogic.translate(I18N, key, Array.prototype.slice.call(arguments, 1)); }

  var ICON = {
    mic: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z"/><path d="M19 10v2a7 7 0 0 1-14 0v-2"/><line x1="12" y1="19" x2="12" y2="23"/></svg>',
    micOff: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="1" y1="1" x2="23" y2="23"/><path d="M9 9v3a3 3 0 0 0 5.12 2.12M15 9.34V4a3 3 0 0 0-5.94-.6"/><path d="M17 16.95A7 7 0 0 1 5 12v-2m14 0v2a7 7 0 0 1-.11 1.23"/><line x1="12" y1="19" x2="12" y2="23"/></svg>',
    cam: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M23 7l-7 5 7 5V7z"/><rect x="1" y="5" width="15" height="14" rx="2" ry="2"/></svg>',
    camOff: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M16 16v1a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2h2m5.66 0H14a2 2 0 0 1 2 2v3.34l1 1L23 7v10"/><line x1="1" y1="1" x2="23" y2="23"/></svg>',
    screen: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/></svg>',
    layout: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><rect x="3" y="15" width="6" height="6" rx="1" fill="currentColor"/><rect x="11" y="15" width="6" height="6" rx="1" fill="currentColor"/></svg>',
    chat: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg>',
    record: '<svg viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="12" r="7"/></svg>',
    gear: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>',
  };

  function showOnly(id) {
    ['lk-prejoin', 'lk-room', 'lk-left', 'lk-error'].forEach(function (s) {
      $(s).classList.toggle('hidden', s !== id);
    });
    if (id !== 'lk-room') $('lk-rec-banner').classList.add('hidden'); // the banner only belongs in-room
  }
  function fail(msg) {
    if (msg) $('lk-error-msg').textContent = msg;
    showOnly('lk-error');
  }
  function status(msg) {
    var el = $('lk-status');
    if (!msg) { el.classList.add('hidden'); return; }
    el.textContent = msg; el.classList.remove('hidden');
  }
  function initial(name) { return (name || '?').trim().charAt(0).toUpperCase() || '?'; }

  // ----- Prejoin -----
  var roomToken = new URLSearchParams(location.search).get('t');
  var accessToken = ''; // the LiveKit access JWT (proves our identity for temp-host actions)
  // The prejoin camera and mic tracks. join() PUBLISHES these same tracks: it never stops the
  // camera and opens it again (on an iPhone that re-acquisition, outside any tap, is where the
  // camera used to fail silently).
  var previewVideo = null, previewAudio = null;
  var previewBusy = null; // the running startPreview(), so toggles queue up and join() can wait
  var camOn = true, micOn = true;
  var camPicked = false, micPicked = false; // the person chose a device in the list (else: front camera / default mic)
  var UA = navigator.userAgent || '';
  var IOS = RoomLogic.isIOS(UA, navigator.platform, navigator.maxTouchPoints);
  var IN_APP = RoomLogic.inAppBrowser(UA);

  async function listDevices() {
    try {
      var devices = await navigator.mediaDevices.enumerateDevices();
      fillSelect($('lk-cam'), devices.filter(function (d) { return d.kind === 'videoinput'; }), trackDeviceId(previewVideo));
      fillSelect($('lk-mic'), devices.filter(function (d) { return d.kind === 'audioinput'; }), trackDeviceId(previewAudio));
    } catch (e) { /* labels need permission; ignore */ }
  }
  // The device a running track really uses, so the list shows THAT one (on an iPhone the first
  // listed camera is often a back or virtual multi-lens one, not the front camera we opened).
  function trackDeviceId(track) {
    try { return (track && track.mediaStreamTrack && track.mediaStreamTrack.getSettings().deviceId) || ''; } catch (e) { return ''; }
  }
  function fillSelect(sel, devs, current) {
    var keep = current || sel.value;
    sel.innerHTML = '';
    devs.forEach(function (d, i) {
      var o = document.createElement('option');
      o.value = d.deviceId; o.textContent = cleanLabel(d.label) || t('room_device_fallback', String(i + 1));
      sel.appendChild(o);
    });
    if (keep && devs.some(function (d) { return d.deviceId === keep; })) sel.value = keep;
  }
  function camOptions() { return RoomLogic.captureOptions('video', camPicked, $('lk-cam').value); }
  function micOptions() { return RoomLogic.captureOptions('audio', micPicked, $('lk-mic').value); }
  // Browsers tack on noisy hardware detail — USB vendor:product IDs, Windows "Default - " /
  // "Communications - " role prefixes, and enumeration indices like "(3- …)". Strip them for a
  // clean picker (e.g. "Default - Microphone (3- AT2020 USB ) (17a0:0002)" → "AT2020 USB").
  function cleanLabel(label) {
    var s = (label || '')
      .replace(/\s*\([0-9a-fA-F]{4}:[0-9a-fA-F]{4}\)\s*$/, '') // trailing USB vendor:product id
      .replace(/^(Default|Communications)\s*-\s*/i, '')         // Windows audio role prefix
      .replace(/^(Microphone|Camera|Speaker)\s*\(\s*\d*-?\s*(.+?)\s*\)\s*$/i, '$2') // "Microphone (3- AT2020 USB )" → "AT2020 USB"
      .replace(/\s{2,}/g, ' ')
      .trim();
    return s;
  }

  // startPreview brings the prejoin tracks in line with the two toggles. Calls queue up (a tap
  // during a permission prompt waits for it) instead of racing two getUserMedia calls.
  function startPreview() {
    var run = (previewBusy || Promise.resolve()).then(acquirePreview, acquirePreview);
    previewBusy = run;
    return run;
  }
  async function acquirePreview() {
    if (!camOn) stopVideoPreview();
    if (!micOn) stopAudioPreview();
    // Camera AND microphone in ONE getUserMedia (an iPhone then asks one question, not two);
    // if that fails, each device on its own, so a blocked camera never costs the mic too
    // (RoomLogic.acquireTracks, tested).
    var got = await RoomLogic.acquireTracks({ video: camOn && !previewVideo, audio: micOn && !previewAudio }, {
      both: LK.createLocalTracks ? function () { return LK.createLocalTracks({ video: camOptions(), audio: micOptions() }); } : null,
      video: function () { return LK.createLocalVideoTrack(camOptions()); },
      audio: function () { return LK.createLocalAudioTrack(micOptions()); }
    });
    if (got.video) previewVideo = got.video;
    if (got.audio) previewAudio = got.audio;
    var errs = got.errs;
    if (errs.video) { camOn = false; syncToggle($('lk-pre-cam'), camOn, 'room_toggle_camera_on', 'room_toggle_camera_off'); }
    if (errs.audio) { micOn = false; syncToggle($('lk-pre-mic'), micOn, 'room_toggle_mic_on', 'room_toggle_mic_off'); }
    paintPreview(!!errs.video);
    showMediaHelp(errs.video || errs.audio, { video: !!errs.video, audio: !!errs.audio });
    if (previewVideo || previewAudio) await listDevices(); // labels now available
  }
  // paintPreview shows the camera, or the overlay. Set the overlay text in BOTH branches:
  // after a failure it read "Camera unavailable", and a later switch-off must say "Camera off"
  // again, not keep the stale failure text.
  function paintPreview(failed) {
    var off = $('lk-preview-off'), v = $('lk-preview');
    off.classList.remove('tap'); off.onclick = null;
    if (previewVideo) {
      off.classList.add('hidden');
      previewVideo.attach(v); // idempotent for an element it is already attached to
      noAutoplayOnIOS(v);
      playVideo(v);
      return;
    }
    off.textContent = t(failed ? 'room_camera_unavailable' : 'room_preview_camera_off');
    off.classList.remove('hidden');
  }
  function stopVideoPreview() {
    if (previewVideo) { previewVideo.detach(); previewVideo.stop(); previewVideo = null; }
  }
  function stopAudioPreview() {
    if (previewAudio) { previewAudio.stop(); previewAudio = null; }
  }
  function stopPreview() { stopVideoPreview(); stopAudioPreview(); }

  // ----- Camera / mic problems: say what happened and offer the one button that can fix it -----
  // Never silent any more: before, every failure ended in a bare "Camera unavailable" (or
  // nothing at all, in the room) and the person joined with the camera off without knowing why.
  function problemFor(err) { return RoomLogic.mediaProblem(err, { ios: IOS, inApp: !!IN_APP }); }
  function paintHelp(box, msgEl, retryBtn, copyBtn, p, onRetry) {
    if (!p) { box.classList.add('hidden'); return; }
    msgEl.textContent = t(p.key);
    retryBtn.classList.toggle('hidden', p.action === 'none');
    retryBtn.textContent = t(p.action === 'reload' ? 'room_media_reload' : 'room_media_retry');
    retryBtn.onclick = p.action === 'reload' ? reloadPage : onRetry;
    if (copyBtn) copyBtn.classList.toggle('hidden', p.action !== 'none');
    box.classList.remove('hidden');
  }
  // showMediaHelp — the box under the prejoin preview.
  function showMediaHelp(err, failed) {
    paintHelp($('lk-media-help'), $('lk-media-help-msg'), $('lk-media-retry'), $('lk-copy-link'), problemFor(err), function () {
      // "Try again" runs inside the tap, which is what lets the browser ask again.
      if (failed.video) { camOn = true; syncToggle($('lk-pre-cam'), camOn, 'room_toggle_camera_on', 'room_toggle_camera_off'); }
      if (failed.audio) { micOn = true; syncToggle($('lk-pre-mic'), micOn, 'room_toggle_mic_on', 'room_toggle_mic_off'); }
      startPreview();
    });
  }
  // reloadPage — after "No permitir", Safari does not ask again on the same page; a reload does.
  // The typed name survives it.
  function reloadPage() {
    try { var n = ($('lk-name').value || '').trim(); if (n) localStorage.setItem('calnode_name', n); } catch (e) {}
    location.reload();
  }
  // copyLink — for an in-app browser that cannot use the camera: copy this page's address so
  // the person can paste it into Safari / Chrome.
  function copyLink(btn) {
    var url = location.href;
    var done = function () { btn.textContent = t('room_link_copied'); };
    var fallback = function () {
      var ta = document.createElement('textarea');
      ta.value = url; ta.setAttribute('readonly', ''); ta.style.position = 'fixed'; ta.style.opacity = '0';
      document.body.appendChild(ta); ta.select();
      var ok = false; try { ok = document.execCommand('copy'); } catch (e) {}
      ta.remove();
      if (ok) done();
    };
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) { navigator.clipboard.writeText(url).then(done, fallback); return; }
    } catch (e) {}
    fallback();
  }

  // ----- Video playback (iPhone) -----
  // Every <video> is muted + playsinline as ATTRIBUTES (not only properties) before any track
  // is attached: iOS only plays video inline, without a tap, when both are on the element.
  function prepVideo(v) {
    v.setAttribute('playsinline', ''); v.setAttribute('webkit-playsinline', ''); v.setAttribute('muted', '');
    v.playsInline = true; v.muted = true; // sound always comes from the separate <audio> elements
  }
  // No autoplay attribute on iOS: the SDK leaves it off on Safari on purpose (in Low Power Mode
  // Safari answers it with a play overlay over a black box), but it does set it in Chrome or
  // Firefox for iPhone, which are WebKit too. playVideo() starts the video instead.
  function noAutoplayOnIOS(v) {
    if (IOS) { v.autoplay = false; v.removeAttribute('autoplay'); }
  }
  // playVideo — play() a video that has a stream but is paused, on the next tick (after the
  // SDK's own Safari srcObject re-assignment). If the browser refuses (NotAllowedError: Low
  // Power Mode, or a browser that wants a tap first), the tap-to-play prompt appears instead of
  // a black box with sound.
  var blockedVideos = []; // our <video> elements the browser refused to start
  function playVideo(v) {
    if (!v) return;
    setTimeout(function () {
      if (!v.srcObject || !v.paused || typeof v.play !== 'function') return;
      var p; try { p = v.play(); } catch (e) { return; }
      if (p && typeof p.then === 'function') {
        p.then(function () { onPlayed(v); }, function (e) { if (e && e.name === 'NotAllowedError') onPlayBlocked(v); });
      }
    }, 0);
  }
  function onPlayBlocked(v) {
    if (blockedVideos.indexOf(v) < 0) blockedVideos.push(v);
    if (v === $('lk-preview')) {
      var off = $('lk-preview-off');
      off.textContent = t('room_preview_tap_to_play');
      off.classList.add('tap'); off.classList.remove('hidden');
      off.onclick = unlockPlayback;
    }
    paintPlayGate();
  }
  function onPlayed(v) {
    blockedVideos = blockedVideos.filter(function (b) { return b !== v; });
    if (v === $('lk-preview') && previewVideo) {
      var off = $('lk-preview-off');
      off.classList.add('hidden'); off.classList.remove('tap'); off.onclick = null;
    }
    paintPlayGate();
  }
  function allVideos() {
    var list = [$('lk-preview')];
    Object.keys(tiles).forEach(function (id) { list.push(tiles[id].video); });
    return list;
  }
  function kickVideos() { allVideos().forEach(playVideo); }
  function needsUnlock() {
    if (room && (room.canPlaybackVideo === false || room.canPlaybackAudio === false)) return true;
    return blockedVideos.length > 0;
  }
  // unlockPlayback — runs INSIDE a tap (the gate button, the preview overlay, or any tap on the
  // page while something is blocked). The play() calls must be its first, synchronous work: a
  // browser only honours them while it still counts as the user's gesture. room.startVideo()
  // only covers remote videos, so our own tile and the preview are played here too.
  function unlockPlayback() {
    if (room) {
      try { var sv = room.startVideo(); if (sv && sv.catch) sv.catch(function () {}); } catch (e) {}
      try { var sa = room.startAudio(); if (sa && sa.catch) sa.catch(function () {}); } catch (e) {}
    }
    allVideos().forEach(function (v) {
      if (!v || !v.srcObject || !v.paused || typeof v.play !== 'function') return;
      try { var p = v.play(); if (p && p.then) p.then(function () { onPlayed(v); }, function () {}); } catch (e) {}
    });
  }
  function onAnyTap() { if (needsUnlock()) unlockPlayback(); }
  // paintPlayGate — the big in-room "Toca aquí para ver y escuchar a los demás" button.
  function paintPlayGate() {
    blockedVideos = blockedVideos.filter(function (v) { return v.srcObject && v.paused && v.isConnected !== false; });
    var show = !!room && RoomLogic.playGate({
      canVideo: room ? room.canPlaybackVideo : true,
      canAudio: room ? room.canPlaybackAudio : true,
      localBlocked: blockedVideos.length > 0
    });
    $('lk-play-gate').classList.toggle('hidden', !show);
  }
  // Two whole-sentence keys, not label + ' on'/' off': word order and gender differ by language
  // ("Cámara activada", "Micrófono desactivado").
  function syncToggle(btn, on, onKey, offKey) {
    btn.textContent = t(on ? onKey : offKey);
    btn.classList.toggle('off', !on);
  }

  function initPrejoin() {
    if (!roomToken) { fail(t('room_error_missing_token')); return; }
    syncToggle($('lk-pre-cam'), camOn, 'room_toggle_camera_on', 'room_toggle_camera_off');
    syncToggle($('lk-pre-mic'), micOn, 'room_toggle_mic_on', 'room_toggle_mic_off');
    $('lk-pre-cam').onclick = function () { camOn = !camOn; syncToggle($('lk-pre-cam'), camOn, 'room_toggle_camera_on', 'room_toggle_camera_off'); startPreview(); };
    $('lk-pre-mic').onclick = function () { micOn = !micOn; syncToggle($('lk-pre-mic'), micOn, 'room_toggle_mic_on', 'room_toggle_mic_off'); startPreview(); };
    $('lk-cam').onchange = function () { camPicked = true; stopVideoPreview(); startPreview(); };
    $('lk-mic').onchange = function () { micPicked = true; stopAudioPreview(); startPreview(); };
    $('lk-join').onclick = join;
    $('lk-copy-link').onclick = function () { copyLink($('lk-copy-link')); };
    try { $('lk-name').value = localStorage.getItem('calnode_name') || ''; } catch (e) {}
    // Any tap while a video is blocked starts it: older guests tap the black square anyway.
    // click/touchend (capture) are the events iOS counts as a user gesture for play().
    document.addEventListener('click', onAnyTap, true);
    document.addEventListener('touchend', onAnyTap, { capture: true, passive: true });
    // Back from another app or the lock screen: iOS pauses our videos meanwhile.
    document.addEventListener('visibilitychange', function () { if (!document.hidden) kickVideos(); });
    startPreview();
  }

  // ----- Room -----
  var room = null;
  var tiles = {}; // identity -> { el, video, camoff }
  var myName = t('room_guest'); // placeholder only: join() requires a name and overwrites it
  var layoutMode = 'grid';   // 'grid' | 'speaker'
  var pinnedId = null;       // identity manually pinned to the stage (speaker mode)
  var activeSpeakerId = null;
  var chatOpen = false, unread = 0;
  var isHost = false; // current live host status; can change as host is reassigned/reclaimed
  var hostCapable = false; // held host capability at join (host link / owner) — enables the menu + reclaim
  var recordingAvailable = false, recording = false; // instance can record / is recording now
  var consentDecided = false, consentAnnounced = false; // recording-consent (notice + consent-or-leave)
  var canScreenShare = false, allowShare = false; // me / attendees-in-general (host opts in)
  var paintControls = null; // setupControls' repaint of the mic/cam/screen buttons (null until in the room)
  var publishing = null; // the running publishLocal() right after joining; null once it settled
  // Every iPhone browser lacks getDisplayMedia: the screen-share button is not offered there.
  var SCREEN_SUPPORTED = !!(navigator.mediaDevices && navigator.mediaDevices.getDisplayMedia);

  // applyRoomMeta reflects shared room state (recording + screen-share permission) to everyone:
  // the recording banner + button, and whether non-hosts may see the screen-share button.
  function applyRoomMeta() {
    if (!room) return;
    var meta = {};
    try { meta = JSON.parse(room.metadata || '{}'); } catch (e) {}
    recording = !!meta.recording;          // banner + record-button state
    allowShare = meta.allowShare === true; // default off — host opts attendees in
    $('lk-rec-banner').classList.toggle('hidden', !recording);
    applyHostUi();
  }

  // applyHostUi paints every host/consent/screen-share control from one derived snapshot — the
  // single place the DOM is updated for that state (RoomLogic.hostUi holds the rules, tested).
  function applyHostUi() {
    var ui = RoomLogic.hostUi(snapshot());
    var rb = $('lk-record-btn');
    if (rb) {
      rb.classList.toggle('hidden', !ui.recordVisible);
      rb.classList.toggle('recording', recording);
      var recLabel = t(recording ? 'room_record_stop' : 'room_record_start');
      rb.title = recLabel; rb.setAttribute('aria-label', recLabel); // screen readers track the state too
    }
    var sc = $('lk-screen');
    if (sc) sc.classList.toggle('hidden', !ui.screenVisible);
    var wrap = $('lk-host-menu-wrap');
    if (wrap) wrap.classList.toggle('hidden', !ui.gearVisible);
    // Recording consent: attendees acknowledge; a host's record click is itself consent.
    if (ui.consentPrompt) showConsentModal();
    else if (!recording && !consentDecided) $('lk-consent-modal').classList.add('hidden');
    var menu = $('lk-host-menu');
    if (menu && !menu.classList.contains('hidden')) renderHostMenu(); // keep the menu live while open
  }
  async function toggleSharePerm() {
    await postLK('room/screenshare', { allow: !allowShare });
    allowShare = !allowShare; applyRoomMeta(); // optimistic; the room-metadata event also reconciles
  }

  // showConsentModal — the Zoom-style notice: an audio announcement (once) + a Continue/Leave
  // prompt. Idempotent; the buttons (setupControls) record the decision and stop the re-prompt.
  function showConsentModal() {
    var m = $('lk-consent-modal');
    if (!m || !m.classList.contains('hidden')) return; // already up
    if (!consentAnnounced) {
      consentAnnounced = true;
      // Browser speech (Web Speech API), not an audio file: speak the translated sentence and say
      // which language it is, or the browser may read it with a voice for another language.
      // A device with no voice for the locale falls back to another voice; the written modal
      // below stays the notice of record.
      try {
        var notice = new SpeechSynthesisUtterance(t('room_consent_spoken'));
        notice.lang = LOCALE;
        window.speechSynthesis.speak(notice);
      } catch (e) {}
    }
    m.classList.remove('hidden');
  }

  // ----- Host controls menu (gear popover) -----
  function openHostMenu() { renderHostMenu(); $('lk-host-menu').classList.toggle('hidden'); }
  function renderHostMenu() {
    var ui = RoomLogic.hostUi(snapshot());
    var share = $('lk-hm-share');
    share.classList.toggle('hidden', !ui.hostActions);
    share.textContent = (allowShare ? '✓ ' + t('room_host_menu_guests_can_share') : t('room_host_menu_allow_guests_share'));
    $('lk-hm-makehost').classList.toggle('hidden', !ui.hostActions);
    var list = $('lk-hm-participants'); list.innerHTML = '';
    if (ui.hostActions) {
      var others = room ? Array.from(room.remoteParticipants.values()) : [];
      if (others.length === 0) {
        var none = document.createElement('div'); none.className = 'hm-empty';
        none.textContent = t('room_host_menu_no_one_else'); list.appendChild(none);
      }
      others.forEach(function (p) {
        var b = document.createElement('button'); b.type = 'button'; b.className = 'hm-sub';
        b.textContent = p.name || t('room_participant');
        b.onclick = function () { makeHost(p.identity); };
        list.appendChild(b);
      });
    }
    // Reclaim: shown to the owner once they've stepped down (capable but not currently host).
    $('lk-hm-reclaim').classList.toggle('hidden', !ui.reclaimVisible);
  }
  async function makeHost(identity) {
    $('lk-host-menu').classList.add('hidden');
    // Server demotes me + promotes them; my ParticipantMetadataChanged flips isHost to false.
    await postLK('room/reassign-host', { identity: identity });
  }
  async function reclaimHost() {
    $('lk-host-menu').classList.add('hidden');
    var me = room && room.localParticipant ? room.localParticipant.identity : '';
    if (me) await postLK('room/reassign-host', { identity: me });
  }
  async function toggleRecord() {
    var btn = $('lk-record-btn'); if (btn) btn.disabled = true;
    await postLK(recording ? 'record/stop' : 'record/start');
    if (btn) btn.disabled = false;
    // The room-metadata change will drive the banner/button via applyRoomMeta.
  }

  // relayout places tiles into the grid, or (speaker mode) a big stage + a filmstrip. Moving a
  // tile's element between containers via appendChild preserves its playing <video>, so no track
  // re-attach is needed. The stage shows the pinned tile, else the active speaker, else the first.
  function relayout() {
    var grid = $('lk-grid'), stage = $('lk-stage'), strip = $('lk-strip');
    var ids = Object.keys(tiles);
    if (layoutMode === 'grid' || !ids.length) {
      grid.classList.remove('hidden'); stage.classList.add('hidden'); strip.classList.add('hidden');
      ids.forEach(function (id) { grid.appendChild(tiles[id].el); });
    } else {
      grid.classList.add('hidden'); stage.classList.remove('hidden'); strip.classList.remove('hidden');
      var focus = (pinnedId && tiles[pinnedId]) ? pinnedId
        : (activeSpeakerId && tiles[activeSpeakerId]) ? activeSpeakerId : ids[0];
      ids.forEach(function (id) { (id === focus ? stage : strip).appendChild(tiles[id].el); });
    }
    kickVideos(); // WebKit may pause a video whose container was hidden or moved: start it again
  }

  // togglePin: click a tile → spotlight it; click the spotlighted tile again → back to grid.
  function togglePin(id) {
    if (layoutMode === 'grid') { layoutMode = 'speaker'; pinnedId = id; }
    else if (pinnedId === id) { layoutMode = 'grid'; pinnedId = null; }
    else { pinnedId = id; }
    paintLayoutBtn(); relayout();
  }
  function toggleLayout() {
    layoutMode = layoutMode === 'grid' ? 'speaker' : 'grid';
    pinnedId = null;
    paintLayoutBtn(); relayout();
  }
  function paintLayoutBtn() {
    var b = $('lk-layout-btn'); if (!b) return;
    b.innerHTML = ICON.layout;
    b.classList.toggle('active', layoutMode === 'speaker');
  }

  // ----- Ephemeral chat (LiveKit data channel — peer-to-peer, never stored) -----
  function onData(payload, participant) {
    try {
      var msg = JSON.parse(new TextDecoder().decode(payload));
      if (msg && msg.t === 'chat' && msg.text) {
        addMsg(msg.name || (participant && participant.name) || t('room_guest'), String(msg.text), false);
      }
    } catch (e) { /* ignore non-chat data */ }
  }
  function addMsg(who, text, mine) {
    var empty = $('lk-chat-empty'); if (empty) empty.remove();
    var el = document.createElement('div'); el.className = 'msg' + (mine ? ' me' : '');
    var w = document.createElement('div'); w.className = 'who'; w.textContent = mine ? t('room_chat_you') : who;
    var body = document.createElement('div'); body.textContent = text; // not `t`: that would shadow t()
    el.appendChild(w); el.appendChild(body);
    var box = $('lk-chat-msgs'); box.appendChild(el); box.scrollTop = box.scrollHeight;
    if (!chatOpen && !mine) { unread++; paintChatBadge(); }
  }
  function sendChat(text) {
    text = (text || '').trim();
    if (!text || !room) return;
    var data = new TextEncoder().encode(JSON.stringify({ t: 'chat', name: myName, text: text }));
    try { room.localParticipant.publishData(data, { reliable: true }); } catch (e) {}
    addMsg(t('room_chat_you'), text, true);
  }
  function setChat(open) {
    chatOpen = open;
    $('lk-chat').classList.toggle('hidden', !open);
    $('lk-chat-btn').classList.toggle('active', open);
    if (open) { unread = 0; paintChatBadge(); $('lk-chat-input').focus(); }
  }
  function paintChatBadge() {
    var btn = $('lk-chat-btn'); if (!btn) return;
    var b = btn.querySelector('.badge'); if (!b) return;
    // A simple red dot — presence of unread, not a count.
    b.classList.toggle('hidden', unread === 0);
  }

  // ----- Host controls: leave / end-for-all / reassign -----
  // snapshot gathers the host/consent/screen-share flags into a plain object for RoomLogic — the
  // single source the pure (tested) decision functions read from.
  function snapshot() {
    var meta = (room && room.localParticipant) ? room.localParticipant.metadata : '';
    return {
      isHost: isHost, hostMeta: meta === 'host', hostCapable: hostCapable,
      recordingAvailable: recordingAvailable, recording: recording,
      consentDecided: consentDecided, allowShare: allowShare, screenSupported: SCREEN_SUPPORTED
    };
  }
  function amHost() { return RoomLogic.amHost(snapshot()); }
  function leaveOrPrompt() {
    // Non-hosts just leave. Hosts always get the modal (end / pass host / just leave); the
    // pass-host option only appears when there's someone else to hand off to.
    if (!amHost()) { if (room) room.disconnect(); return; }
    var others = room ? Array.from(room.remoteParticipants.values()) : [];
    var sel = $('lk-reassign-sel'); sel.innerHTML = '';
    others.forEach(function (p) {
      var o = document.createElement('option'); o.value = p.identity; o.textContent = p.name || t('room_participant');
      sel.appendChild(o);
    });
    $('lk-reassign-wrap').classList.toggle('hidden', others.length === 0);
    $('lk-leave-modal').classList.remove('hidden');
  }
  function closeLeaveModal() { $('lk-leave-modal').classList.add('hidden'); }
  // postLK POSTs to /v1/livekit/<path> with the opaque room token (the host capability).
  async function postLK(path, extra) {
    try {
      var res = await fetch('/v1/livekit/' + path, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(Object.assign({ t: roomToken, at: accessToken }, extra || {}))
      });
      return res.ok;
    } catch (e) { return false; }
  }
  // A recording belongs to the meeting, not a lingering egress: stop it whenever the host leaves
  // so it never runs unattended. (End-for-all finalizes server-side via room/end.)
  async function stopRecIfHosting() { if (amHost() && recording) await postLK('record/stop'); }
  async function endForAll() { closeLeaveModal(); await postLK('room/end'); if (room) room.disconnect(); }
  async function reassignAndLeave() {
    closeLeaveModal();
    await stopRecIfHosting();
    var id = $('lk-reassign-sel').value;
    if (id) await postLK('room/reassign-host', { identity: id });
    if (room) room.disconnect();
  }

  function tileFor(identity, name, isLocal) {
    if (tiles[identity]) return tiles[identity];
    var el = document.createElement('div');
    el.className = 'tile' + (isLocal ? ' local' : '');
    var video = document.createElement('video');
    prepVideo(video); // muted + playsinline attributes; no autoplay attribute (see noAutoplayOnIOS)
    var camoff = document.createElement('div');
    camoff.className = 'camoff';
    camoff.innerHTML = '<div class="avatar">' + initial(name) + '</div>';
    var label = document.createElement('div');
    label.className = 'label';
    label.innerHTML = '<span class="host-badge hidden"></span><span class="name"></span><span class="mic-off hidden">' + ICON.micOff + '</span>';
    // Translated text goes in via textContent, never concatenated into the innerHTML string.
    label.querySelector('.host-badge').textContent = t('room_host_badge');
    label.querySelector('.name').textContent = isLocal ? t('room_tile_you', name) : name;
    el.appendChild(video); el.appendChild(camoff); el.appendChild(label);
    el.addEventListener('click', function () { togglePin(identity); });
    $('lk-grid').appendChild(el);
    tiles[identity] = { el: el, video: video, camoff: camoff, label: label, hasVideo: false };
    setCamOff(identity, true);
    return tiles[identity];
  }
  function removeTile(identity) {
    var t = tiles[identity];
    if (t) { t.el.remove(); delete tiles[identity]; }
    if (pinnedId === identity) { pinnedId = null; }
    relayout();
    paintPlayGate(); // a blocked video that just left must not keep the gate up
  }
  // setCamOff lays the avatar overlay over the video, or lifts it. The <video> itself is never
  // hidden: WebKit pauses a display:none MediaStream video and can keep it black when it is
  // shown again (the "audio but no video on iPhone" symptom). Shown again = played again.
  function setCamOff(identity, off) {
    var t = tiles[identity]; if (!t) return;
    t.camoff.classList.toggle('hidden', !off);
    if (!off) playVideo(t.video);
  }
  // setTileNote — a short line over the tile (today: "video paused, weak connection").
  function setTileNote(identity, text) {
    var tile = tiles[identity]; if (!tile) return;
    if (!tile.note) {
      tile.note = document.createElement('div'); tile.note.className = 'note hidden';
      tile.el.appendChild(tile.note);
    }
    tile.note.textContent = text;
    tile.note.classList.toggle('hidden', !text);
  }
  function setMicOff(identity, off) {
    var t = tiles[identity]; if (!t) return;
    t.label.querySelector('.mic-off').classList.toggle('hidden', !off);
  }
  function setHostBadge(identity, on) {
    var t = tiles[identity]; if (!t) return;
    t.label.querySelector('.host-badge').classList.toggle('hidden', !on);
  }

  function attachVideo(identity, track) {
    var t = tiles[identity]; if (!t) return;
    track.attach(t.video); t.hasVideo = true;
    noAutoplayOnIOS(t.video);
    // A screen share must not be mirrored (the .local tile mirrors the selfie camera).
    t.el.classList.toggle('screen', track.source === LK.Track.Source.ScreenShare);
    // A camera that is already off when we arrive shows the avatar, not a frozen frame.
    setCamOff(identity, !!track.isMuted);
    playVideo(t.video);
  }
  function detachVideo(identity, track) {
    var t = tiles[identity]; if (!t) return;
    if (track) track.detach(t.video);
    t.hasVideo = false; setCamOff(identity, true);
  }

  function wireParticipant(p) {
    var t = tileFor(p.identity, p.name || p.identity, false);
    setMicOff(p.identity, !p.isMicrophoneEnabled);
    setHostBadge(p.identity, p.metadata === 'host');
    // Attach any already-subscribed tracks.
    p.trackPublications.forEach(function (pub) {
      if (pub.track) handleTrack(pub.track, pub, p);
    });
    relayout();
    return t;
  }

  function handleTrack(track, pub, participant) {
    if (track.kind === 'video') {
      // Never drop a video because its tile is not there yet: make the tile now.
      if (!tiles[participant.identity]) {
        tileFor(participant.identity, participant.name || participant.identity, !!participant.isLocal);
        relayout();
      }
      attachVideo(participant.identity, track);
    } else if (track.kind === 'audio' && !participant.isLocal) {
      // One <audio> per voice: wireParticipant re-handles tracks already subscribed, and a
      // second element would play the same voice twice (an echo).
      if (track.attachedElements && track.attachedElements.length) return;
      var a = document.createElement('audio');
      a.autoplay = true; a.setAttribute('playsinline', '');
      track.attach(a); document.body.appendChild(a);
    }
  }
  function dropTrack(track, pub, participant) {
    if (track.kind === 'video') { detachVideo(participant.identity, track); paintPlayGate(); }
    else if (track.kind === 'audio') {
      try { track.detach().forEach(function (el) { el.remove(); }); } catch (e) {}
    }
  }

  async function join() {
    var name = ($('lk-name').value || '').trim();
    if (!name) { $('lk-name').focus(); return; }
    myName = name;
    try { localStorage.setItem('calnode_name', name); } catch (e) {}
    $('lk-join').disabled = true; $('lk-join').textContent = t('room_joining');

    // Never show the server's `error` text or the browser's exception message ("livekit: bad
    // room token signature", "Failed to fetch", a JSON SyntaxError from a proxy's HTML page):
    // they're English and technical. The message is picked from the status (tokenErrorKey).
    var data, status;
    try {
      var res = await fetch('/v1/livekit/token', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ t: roomToken, name: name })
      });
      status = res.status;
      if (!res.ok) throw new Error('token request failed: ' + status);
      data = await res.json();
    } catch (e) {
      stopPreview(); fail(t(RoomLogic.tokenErrorKey(status))); return;
    }
    // The preview tracks are NOT stopped here: publishLocal() publishes them as they are.
    accessToken = (data && data.token) || '';
    isHost = !!(data && data.role === 'host');
    hostCapable = isHost; // sticky: the owner can reclaim host even after stepping down
    recordingAvailable = !!(data && data.recording_available);
    canScreenShare = !!(data && data.can_screenshare); // default off
    allowShare = !!(data && data.allow_share);

    // Codec: the SDK default (VP8 + simulcast) is what iPhones decode and encode reliably; it
    // is left alone on purpose (AV1 renders black on iOS Safari, VP9 needs Safari 16+).
    // No room-wide videoCaptureDefaults: the SDK merges them into EVERY camera request, so a
    // facingMode 'user' there would sit next to the deviceId of a camera the person picked and
    // WebKit could open the front camera instead. captureOptions() already asks for the front
    // camera whenever nothing was picked.
    room = new LK.Room({ adaptiveStream: true, dynacast: true });
    var RE = LK.RoomEvent;
    room
      .on(RE.TrackSubscribed, handleTrack)
      .on(RE.TrackUnsubscribed, dropTrack)
      .on(RE.TrackMuted, function (pub, p) { if (pub.kind === 'video') setCamOff(p.identity, true); if (pub.kind === 'audio') setMicOff(p.identity, true); })
      .on(RE.TrackUnmuted, function (pub, p) {
        // Lift the avatar only over a video that is really attached, never over an empty box.
        if (pub.kind === 'video' && tiles[p.identity] && tiles[p.identity].hasVideo) setCamOff(p.identity, false);
        if (pub.kind === 'audio') setMicOff(p.identity, false);
      })
      // The browser blocked remote video (iPhone Low Power Mode) or sound until a tap.
      .on(RE.VideoPlaybackStatusChanged, paintPlayGate)
      .on(RE.AudioPlaybackStatusChanged, paintPlayGate)
      // The server paused someone's video for a weak connection (sound keeps going): say so,
      // instead of a frozen picture that looks like a broken camera.
      .on(RE.TrackStreamStateChanged, function (pub, state, p) {
        if (!p || pub.kind !== 'video') return;
        setTileNote(p.identity, state === 'paused' ? t('room_video_paused') : '');
        if (state === 'active' && tiles[p.identity]) playVideo(tiles[p.identity].video);
      })
      .on(RE.LocalTrackPublished, function (pub) { if (pub.track && pub.track.kind === 'video') attachVideo(room.localParticipant.identity, pub.track); })
      .on(RE.LocalTrackUnpublished, function (pub) { if (pub.source === LK.Track.Source.ScreenShare) reattachLocalCamera(); })
      .on(RE.ParticipantConnected, wireParticipant)
      .on(RE.ParticipantDisconnected, function (p) { removeTile(p.identity); })
      .on(RE.ActiveSpeakersChanged, function (speakers) {
        var ids = {}; speakers.forEach(function (s) { ids[s.identity] = true; });
        Object.keys(tiles).forEach(function (id) { tiles[id].el.classList.toggle('speaking', !!ids[id]); });
        if (speakers.length) activeSpeakerId = speakers[0].identity;
        // In speaker mode with nothing pinned, follow whoever's talking.
        if (layoutMode === 'speaker' && !pinnedId) relayout();
      })
      .on(RE.DataReceived, onData)
      .on(RE.RoomMetadataChanged, applyRoomMeta)
      .on(RE.ParticipantMetadataChanged, function (prev, participant) {
        if (!room || !participant) return;
        var m = participant.metadata;
        // Reflect the host badge for whoever this is (newly-promoted host, or a demoted one).
        setHostBadge(participant.identity, m === 'host');
        if (participant.identity === room.localParticipant.identity) {
          isHost = RoomLogic.nextIsHost(isHost, m); // see room-logic.js (tested)
          applyRoomMeta(); // refresh record + screen buttons + host menu for the new status
        }
      })
      .on(RE.Disconnected, function () { closeLeaveModal(); showOnly('lk-left'); });

    try {
      await room.connect(data.url, data.token);
    } catch (e) {
      stopPreview(); fail(t('room_error_connect')); return;
    }
    showOnly('lk-room');
    tileFor(room.localParticipant.identity, name, true);
    setMicOff(room.localParticipant.identity, !micOn);
    setHostBadge(room.localParticipant.identity, amHost());

    // The people already here get their tiles and the buttons work BEFORE we publish our own
    // camera and mic. It used to be the other way round: a slow or stuck publish (an iPhone
    // permission question, a camera that would not start) left the person hearing everyone -
    // audio needs no tile - but seeing no one, with dead buttons.
    // While the publish runs, the mic/cam buttons show the prejoin choice and a tap waits for
    // the publish to settle (see `publishing`), so it can never open a second camera or mic.
    room.remoteParticipants.forEach(wireParticipant);
    relayout();
    publishing = publishLocal();
    setupControls();
    paintPlayGate();
    try { await publishing; } finally {
      publishing = null;
      if (paintControls) paintControls();
    }
  }

  // publishLocal publishes the prejoin camera and mic tracks as they are (no second
  // getUserMedia), or asks for a device afresh when there is no preview track for it. A
  // failure shows what happened and how to fix it, never an empty catch.
  async function publishLocal() {
    if (previewBusy) { try { await previewBusy; } catch (e) {} }
    var v = previewVideo, a = previewAudio;
    previewVideo = null; previewAudio = null; // the room owns them now
    if (v) { try { v.detach(); } catch (e) {} }
    var jobs = [];
    if (camOn) jobs.push(publishOne('video', v)); else if (v) v.stop();
    if (micOn) jobs.push(publishOne('audio', a)); else if (a) a.stop();
    await Promise.all(jobs); // publishOne never rejects: a failure becomes the in-room notice
  }
  async function publishOne(kind, track) {
    if (!room) return;
    var lp = room.localParticipant;
    // iOS can end a capture while the page is in the background: an ended track would publish
    // a black picture, so it is replaced by a fresh one.
    if (track && track.mediaStreamTrack && track.mediaStreamTrack.readyState === 'ended') { try { track.stop(); } catch (e) {} track = null; }
    if (track) {
      try { await lp.publishTrack(track); return; } catch (e) { try { track.stop(); } catch (x) {} }
    }
    try {
      if (kind === 'video') await lp.setCameraEnabled(true, camOptions());
      else await lp.setMicrophoneEnabled(true, micOptions());
    } catch (e) {
      showNotice(e, kind);
    }
  }

  // ----- In-room camera / mic notice -----
  var noticeKind = 'video';
  function showNotice(err, kind) {
    noticeKind = kind || 'video';
    paintHelp($('lk-notice'), $('lk-notice-msg'), $('lk-notice-retry'), $('lk-notice-copy'), problemFor(err), retryDevice);
  }
  function hideNotice() { $('lk-notice').classList.add('hidden'); }
  // retryDevice — "Try again" in the room: asks for the device inside the tap.
  async function retryDevice() {
    hideNotice();
    var lp = room && room.localParticipant; if (!lp) return;
    try {
      if (noticeKind === 'audio') await lp.setMicrophoneEnabled(true, micOptions());
      else await lp.setCameraEnabled(true, camOptions());
    } catch (e) { showNotice(e, noticeKind); }
    if (paintControls) paintControls();
  }

  function reattachLocalCamera() {
    if (!room) return;
    var pub = room.localParticipant.getTrackPublication(LK.Track.Source.Camera);
    if (pub && pub.track) attachVideo(room.localParticipant.identity, pub.track);
    else detachVideo(room.localParticipant.identity, null);
  }

  function setupControls() {
    var lp = room.localParticipant;
    var micBtn = $('lk-mic-btn'), camBtn = $('lk-cam-btn'), screenBtn = $('lk-screen');
    // What each button shows: the prejoin choice while our tracks are still being published,
    // then what the SDK really has (RoomLogic.controlOn, tested).
    var micShown = function () { return RoomLogic.controlOn(!!publishing, micOn, lp.isMicrophoneEnabled); };
    var camShown = function () { return RoomLogic.controlOn(!!publishing, camOn, lp.isCameraEnabled); };
    var paint = function () {
      var mic = micShown(), cam = camShown();
      micBtn.innerHTML = mic ? ICON.mic : ICON.micOff;
      micBtn.classList.toggle('off', !mic);
      camBtn.innerHTML = cam ? ICON.cam : ICON.camOff;
      camBtn.classList.toggle('off', !cam);
      screenBtn.innerHTML = ICON.screen;
      screenBtn.classList.toggle('active', lp.isScreenShareEnabled);
      setCamOff(lp.identity, !lp.isCameraEnabled); // the avatar lifts only over a real picture
      setMicOff(lp.identity, !mic);
    };
    paintControls = paint;
    // toggler — one mic/cam button. The target is the OPPOSITE of what the button showed when
    // tapped. A tap while the join publish is still running waits for it, then only changes
    // the device if it is not already where the person wanted it: switching on during the
    // publish would open a SECOND camera/mic (the SDK guards duplicates only on its own
    // setCameraEnabled path, not on publishTrack). A second tap while one is in flight is
    // ignored for the same reason. A failed switch-on says why (no silent catch).
    function toggler(kind) {
      var busy = false;
      return async function () {
        if (busy) return;
        busy = true;
        var audio = kind === 'audio';
        var target = !(audio ? micShown() : camShown());
        try {
          if (publishing) { try { await publishing; } catch (e) {} }
          var now = audio ? lp.isMicrophoneEnabled : lp.isCameraEnabled;
          if (now !== target) {
            if (audio) await lp.setMicrophoneEnabled(target, target ? micOptions() : undefined);
            else await lp.setCameraEnabled(target, target ? camOptions() : undefined);
          }
          if (target) hideNotice();
        } catch (e) {
          showNotice(e, kind);
        } finally {
          busy = false;
          paint();
        }
      };
    }
    micBtn.onclick = toggler('audio');
    camBtn.onclick = toggler('video');
    $('lk-play-gate').onclick = unlockPlayback;
    $('lk-notice-close').onclick = hideNotice;
    $('lk-notice-copy').onclick = function () { copyLink($('lk-notice-copy')); };
    // Hidden where the browser cannot share a screen (applyHostUi / SCREEN_SUPPORTED: every
    // iPhone browser). What is left to catch here is the person closing the browser's own
    // screen picker (NotAllowedError), which is a choice, not a failure to explain.
    screenBtn.onclick = async function () {
      try { await lp.setScreenShareEnabled(!lp.isScreenShareEnabled); } catch (e) { /* picker closed */ }
      paint();
    };
    $('lk-layout-btn').onclick = toggleLayout;
    $('lk-chat-btn').innerHTML = ICON.chat + '<span class="badge hidden"></span>';
    paintChatBadge();
    $('lk-chat-btn').onclick = function () { setChat(!chatOpen); };
    $('lk-chat-close').onclick = function () { setChat(false); };
    $('lk-chat-form').onsubmit = function (e) { e.preventDefault(); var inp = $('lk-chat-input'); sendChat(inp.value); inp.value = ''; };
    $('lk-leave').onclick = leaveOrPrompt;
    $('lk-end-all').onclick = endForAll;
    $('lk-reassign-leave').onclick = reassignAndLeave;
    $('lk-just-leave').onclick = async function () { await stopRecIfHosting(); if (room) room.disconnect(); };
    $('lk-leave-cancel').onclick = closeLeaveModal;
    $('lk-consent-continue').onclick = function () {
      consentDecided = true;
      $('lk-consent-modal').classList.add('hidden');
      postLK('consent', { decision: 'continue', name: myName });
    };
    $('lk-consent-leave').onclick = function () {
      consentDecided = true;
      $('lk-consent-modal').classList.add('hidden');
      postLK('consent', { decision: 'leave', name: myName });
      if (room) room.disconnect();
    };
    if (recordingAvailable) {
      // Wire the button; applyRoomMeta shows it only while we're host (durable or reassigned).
      var rb = $('lk-record-btn');
      rb.innerHTML = ICON.record; rb.onclick = toggleRecord;
    }
    // Host menu handlers are wired for everyone; the gear only SHOWS while you're host — durable
    // OR reassigned. applyRoomMeta toggles its visibility as host status changes.
    $('lk-host-menu-btn').innerHTML = ICON.gear;
    $('lk-host-menu-btn').onclick = openHostMenu;
    $('lk-hm-share').onclick = function () { $('lk-host-menu').classList.add('hidden'); toggleSharePerm(); };
    $('lk-hm-reclaim').onclick = reclaimHost;
    document.addEventListener('click', function (e) {
      var menu = $('lk-host-menu'), wrap = $('lk-host-menu-wrap');
      if (!menu || menu.classList.contains('hidden')) return;
      if (wrap && wrap.contains(e.target)) return; // clicks on the gear/menu stay open
      menu.classList.add('hidden');
    });
    applyRoomMeta(); // reflect recording + screen-share state already set
    paintLayoutBtn();
    paint();
  }

  if (!LK) { fail(t('room_error_library')); }
  else if (document.readyState !== 'loading') initPrejoin();
  else document.addEventListener('DOMContentLoaded', initPrejoin);
})();
