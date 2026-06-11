'use strict';

const tg = window.Telegram.WebApp;
tg.ready();
tg.expand();
// Blend the native bottom-bar area with the app's tab bar.
try { tg.setBottomBarColor(tg.themeParams.bottom_bar_bg_color || 'bg_color'); } catch (e) { /* older clients */ }

// ---------------------------------------------------------------------------
// API helper — every request carries the Telegram initData for HMAC auth.
// Sent as a header so GET URLs stay clean; the server also accepts ?initData=.
// ---------------------------------------------------------------------------
async function api(path, opts = {}) {
  const headers = Object.assign({ 'X-Init-Data': tg.initData }, opts.headers || {});
  if (opts.body) headers['Content-Type'] = 'application/json';
  const res = await fetch(path, Object.assign({}, opts, { headers }));
  if (!res.ok) throw new Error('HTTP ' + res.status);
  const ct = res.headers.get('content-type') || '';
  return ct.includes('application/json') ? res.json() : res.text();
}

function haptic(kind) {
  try { tg.HapticFeedback.impactOccurred(kind || 'light'); } catch (e) { /* ignore */ }
}

// Result feedback (review answers, errors): success | error | warning.
function hapticNotify(type) {
  try { tg.HapticFeedback.notificationOccurred(type); } catch (e) { /* ignore */ }
}

// Selection feedback: tab switches, chips, pickers.
function hapticSelect() {
  try { tg.HapticFeedback.selectionChanged(); } catch (e) { /* ignore */ }
}

// Keep a vertical drag on a swipe card from collapsing the Mini App.
function setSwipeGuard(on) {
  try { on ? tg.disableVerticalSwipes() : tg.enableVerticalSwipes(); } catch (e) { /* ignore */ }
}

function setCloseGuard(on) {
  try { on ? tg.enableClosingConfirmation() : tg.disableClosingConfirmation(); } catch (e) { /* ignore */ }
}

// Telegram CloudStorage (Bot API 6.9+) for cross-device UI state — never
// learning data; SQLite stays the source of truth.
function cloudGet(key) {
  return new Promise(resolve => {
    try { tg.CloudStorage.getItem(key, (err, v) => resolve(err ? null : v)); }
    catch (e) { resolve(null); }
  });
}

function cloudSet(key, value) {
  try { tg.CloudStorage.setItem(key, String(value)); } catch (e) { /* ignore */ }
}

function skeletonRows(n) {
  let html = '';
  for (let i = 0; i < n; i++) html += '<div class="skeleton skel-row"></div>';
  return html;
}

function skeletonCards(n) {
  let html = '';
  for (let i = 0; i < n; i++) {
    html += '<div class="card"><div class="skeleton skel-line" style="width:30%"></div>' +
            '<div class="skeleton skel-big"></div><div class="skeleton skel-line" style="width:45%"></div></div>';
  }
  return html;
}

function esc(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// ---------------------------------------------------------------------------
// Tab navigation
// ---------------------------------------------------------------------------
const views = {};
document.querySelectorAll('.view').forEach(v => { views[v.id.replace('view-', '')] = v; });
let currentView = 'dashboard';

function showView(name) {
  try { tg.BackButton.hide(); } catch (e) { /* ignore */ } // reset on tab switch
  if (teardownSession) teardownSession(); // hide native session buttons
  setSwipeGuard(name === 'review'); // deck study sets its own guard
  setCloseGuard(false);
  currentView = name;
  if (name !== 'settings') cloudSet('lastTab', name);
  document.querySelectorAll('.view').forEach(v => { v.hidden = true; });
  document.querySelectorAll('.tab').forEach(t => t.classList.toggle('tab-on', t.dataset.view === name));
  views[name].hidden = false;
  loaders[name] && loaders[name](); // always reload so data stays fresh
}

document.querySelectorAll('.tab').forEach(t => {
  t.addEventListener('click', () => { hapticSelect(); showView(t.dataset.view); });
});

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------
function bar(pct, colour) {
  return '<div class="bar-wrap"><div class="bar-fill" style="width:' +
         Math.min(100, pct) + '%;background:' + colour + '"></div></div>';
}

// SVG progress ring for the session completion screen.
function completionRing(known, total) {
  const pct = Math.round(known * 100 / total);
  const r = 28, c = 2 * Math.PI * r;
  const off = c * (1 - known / total);
  return '<div class="ring-wrap">' +
    '<svg class="ring" width="68" height="68" viewBox="0 0 68 68">' +
    '<circle class="track" cx="34" cy="34" r="' + r + '" fill="none" stroke-width="6"></circle>' +
    '<circle class="fill" cx="34" cy="34" r="' + r + '" fill="none" stroke-width="6" stroke-linecap="round"' +
    ' stroke-dasharray="' + c.toFixed(1) + '" stroke-dashoffset="' + off.toFixed(1) + '"></circle></svg>' +
    '<div><div class="ring-pct">' + pct + '% known</div>' +
    '<div class="sub">✅ ' + known + ' · ❌ ' + (total - known) + '</div></div></div>';
}

function renderDashboard(s) {
  const streakPct = s.longest_streak > 0
    ? Math.round(s.current_streak * 100 / s.longest_streak) : 100;
  const flame = s.current_streak >= 3 ? ' 🔥' : '';

  let html = '<h1>📊 Your Progress</h1>';
  if (s.paused) {
    html += '<div class="card" style="background:rgba(244,67,54,.12)">' +
      '<div class="sub" style="color:var(--danger)">⏸️ Scheduled sends are paused — send /resume in the chat.</div></div>';
  }
  html += '<div class="card"><h2>Streak</h2>' +
    '<div class="big">' + s.current_streak + ' day' + (s.current_streak === 1 ? '' : 's') + flame + '</div>' +
    '<div class="sub">Best: ' + s.longest_streak + ' days</div>' +
    bar(streakPct, 'var(--accent)') + '</div>';

  html += '<div class="grid">' +
    '<div class="card"><h2>Words</h2><div class="big">' + s.words + '</div>' +
    '<div class="sub">' + s.mastered + ' mastered</div></div>' +
    '<div class="card"><h2>Drills</h2><div class="big">' + s.verbs + '</div></div>' +
    '</div>';

  if (s.quiz_answered > 0) {
    html += '<div class="card"><h2>Quiz accuracy</h2>' +
      '<div class="big">' + s.quiz_pct + '%</div>' +
      '<div class="sub">' + s.quiz_correct + ' / ' + s.quiz_answered + ' correct</div>' +
      bar(s.quiz_pct, 'var(--success)') + '</div>';
  }

  html += '<div class="card"><h2>Activity · last 4 months</h2>' +
    heatmapHTML(new Set(s.activity_days || [])) + '</div>';

  html += '<div class="card"><h2>Level</h2>' +
    '<div class="big" style="font-size:22px">' + esc(s.level) + '</div>' +
    '<div class="sub">' + s.active_days + ' active day' + (s.active_days === 1 ? '' : 's') + ' total' +
    (s.member_since ? ' · member since ' + esc(s.member_since) : '') + '</div></div>';

  views.dashboard.innerHTML = html;
}

// GitHub-style activity heatmap: 17 week columns × 7 day rows (Mon top),
// ending in the current week. Pure CSS grid — no chart library.
function heatmapHTML(activeSet) {
  const WEEKS = 17;
  const today = new Date();
  const todayKey = localDateKey(today);
  const start = new Date(today);
  start.setDate(today.getDate() - ((today.getDay() + 6) % 7) - (WEEKS - 1) * 7);
  let cells = '';
  const d = new Date(start);
  for (let i = 0; i < WEEKS * 7; i++) {
    const key = localDateKey(d);
    let cls = 'heat-cell';
    if (key > todayKey) cls += ' future';
    else if (activeSet.has(key)) cls += ' on';
    if (key === todayKey) cls += ' today';
    cells += '<div class="' + cls + '"></div>';
    d.setDate(d.getDate() + 1);
  }
  return '<div class="heatmap">' + cells + '</div>';
}

function localDateKey(d) {
  return d.getFullYear() + '-' + ((d.getMonth() + 1) + '').padStart(2, '0') +
         '-' + (d.getDate() + '').padStart(2, '0');
}

async function loadDashboard() {
  // Skeleton on first paint only; on later visits the old content stays
  // until fresh data replaces it (no flash).
  if (!views.dashboard.dataset.ready) views.dashboard.innerHTML = skeletonCards(3);
  try {
    renderDashboard(await api('/api/stats'));
    views.dashboard.dataset.ready = '1';
  } catch (e) { views.dashboard.innerHTML = '<p class="empty">Could not load your stats.<br>Try again later.</p>'; }
}

// ---------------------------------------------------------------------------
// Vocabulary
// ---------------------------------------------------------------------------
const vocabState = { q: '', filter: 'all', offset: 0, total: 0, limit: 20 };
const listEl = document.getElementById('vocab-list');
const moreEl = document.getElementById('vocab-more');
const emptyEl = document.getElementById('vocab-empty');
const searchEl = document.getElementById('vocab-search');

const MASTERY = { mastered: '✅', learning: '📖', new: '🆕' };

function wordRow(w) {
  const el = document.createElement('div');
  el.className = 'word';
  el.innerHTML =
    '<div class="word-main"><div class="word-term">' + esc(w.term) + '</div>' +
    '<div class="word-meaning">' + esc(w.meaning || '—') + '</div></div>' +
    '<span class="word-mastery" title="' + w.mastery + '">' + (MASTERY[w.mastery] || '🆕') + '</span>' +
    '<button class="star">' + (w.bookmarked ? '⭐' : '☆') + '</button>';
  const star = el.querySelector('.star');
  let on = w.bookmarked;
  star.addEventListener('click', async () => {
    on = !on;
    star.textContent = on ? '⭐' : '☆';
    haptic('light');
    try { await api('/api/bookmark', { method: 'POST', body: JSON.stringify({ term: w.term, on }) }); }
    catch (e) { on = !on; star.textContent = on ? '⭐' : '☆'; hapticNotify('error'); }
  });
  return el;
}

// Library chips beyond words/bookmarks browse other content kinds.
const LIBRARY_EMPTY = {
  idiom: 'No idioms yet. They arrive with your daily mix — or send /idiom in the chat!',
  collocation: 'No collocations yet. Send /collocation in the chat to get one!',
  story: 'No stories yet. Send /story in the chat to read one!',
  tip: 'No grammar tips yet. Send /tip in the chat to get one!',
  quiz: 'No quizzes answered yet. Send /quiz in the chat to play!',
};

// contentRow renders one Library item; tapping it expands the full card text.
function contentRow(it) {
  const el = document.createElement('div');
  el.className = 'word' + (it.text ? ' expandable' : '');
  const preview = it.meaning || (it.text ? it.text.slice(0, 90) : '—');
  el.innerHTML =
    '<div class="word-main"><div class="word-term">' + esc(it.term) + '</div>' +
    '<div class="word-meaning">' + esc(preview) + '</div>' +
    (it.text ? '<div class="word-text" hidden>' + esc(it.text) + '</div>' : '') +
    '</div>' +
    '<span class="word-date">' + esc(it.sent_at || '') + '</span>';
  const body = el.querySelector('.word-text');
  if (body) {
    el.addEventListener('click', () => {
      haptic('light');
      body.hidden = !body.hidden;
      el.querySelector('.word-meaning').hidden = !body.hidden;
    });
  }
  return el;
}

// quizRow renders one past quiz attempt.
function quizRow(it) {
  const el = document.createElement('div');
  el.className = 'word';
  el.innerHTML =
    '<div class="word-main"><div class="word-term">' + esc(it.word) + '</div></div>' +
    '<span class="word-mastery">' + (it.correct ? '✅' : '❌') + '</span>' +
    '<span class="word-date">' + esc(it.answered_at || '') + '</span>';
  return el;
}

async function loadVocab(reset) {
  const f = vocabState.filter;
  const isWords = f === 'all' || f === 'bookmarks';
  searchEl.hidden = !isWords; // server search covers words only
  if (reset) { vocabState.offset = 0; listEl.innerHTML = skeletonRows(5); }

  let path;
  if (isWords) {
    path = '/api/vocab?' + new URLSearchParams({
      offset: vocabState.offset, limit: vocabState.limit,
      bookmarks: f === 'bookmarks' ? '1' : '0',
      q: vocabState.q,
    });
  } else if (f === 'quiz') {
    path = '/api/quizzes?offset=' + vocabState.offset + '&limit=' + vocabState.limit;
  } else {
    path = '/api/content?kind=' + f + '&offset=' + vocabState.offset + '&limit=' + vocabState.limit;
  }

  let data;
  try { data = await api(path); }
  catch (e) { listEl.innerHTML = ''; emptyEl.hidden = false; emptyEl.textContent = 'Could not load. Try again later.'; return; }
  if (f !== vocabState.filter) return; // user switched chips mid-flight

  if (reset) listEl.innerHTML = '';
  vocabState.total = data.total || 0;
  (data.items || []).forEach(it => {
    listEl.appendChild(isWords ? wordRow(it) : (f === 'quiz' ? quizRow(it) : contentRow(it)));
  });
  vocabState.offset += (data.items || []).length;

  const has = listEl.children.length > 0;
  emptyEl.hidden = has;
  if (!has) {
    if (f === 'bookmarks') emptyEl.textContent = 'No bookmarks yet. Tap ☆ on a word to save it.';
    else if (f === 'all') emptyEl.textContent = vocabState.q ? 'No words match your search.' : 'No words learned yet. Send /word in the chat!';
    else emptyEl.textContent = LIBRARY_EMPTY[f] || 'Nothing here yet.';
  }
  moreEl.hidden = vocabState.offset >= vocabState.total;
}

moreEl.addEventListener('click', () => loadVocab(false));

let searchTimer;
searchEl.addEventListener('input', () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => { vocabState.q = searchEl.value.trim(); loadVocab(true); }, 250);
});

document.querySelectorAll('[data-filter]').forEach(c => {
  c.addEventListener('click', () => {
    document.querySelectorAll('[data-filter]').forEach(x => x.classList.toggle('chip-on', x === c));
    vocabState.filter = c.dataset.filter;
    hapticSelect();
    loadVocab(true);
  });
});

// ---------------------------------------------------------------------------
// Leaderboard
// ---------------------------------------------------------------------------
const boardState = { metric: 'words', promptedForName: false };
const boardListEl = document.getElementById('board-list');
const boardMeEl = document.getElementById('board-me');
const boardEmptyEl = document.getElementById('board-empty');

function medal(rank) {
  return rank === 1 ? '🥇' : rank === 2 ? '🥈' : rank === 3 ? '🥉' : '#' + rank;
}

function boardRow(r) {
  const el = document.createElement('div');
  el.className = 'word' + (r.isMe ? ' me' : '');
  el.innerHTML =
    '<span class="rank">' + medal(r.rank) + '</span>' +
    '<div class="word-main"><div class="word-term">' + esc(r.name) + (r.isMe ? ' <span class="you">you</span>' : '') + '</div></div>' +
    '<span class="word-term">' + r.value + '</span>';
  return el;
}

async function loadBoard() {
  if (!boardListEl.children.length) boardListEl.innerHTML = skeletonRows(6);
  let data;
  try { data = await api('/api/leaderboard?metric=' + boardState.metric); }
  catch (e) { boardListEl.innerHTML = ''; boardEmptyEl.hidden = false; boardEmptyEl.textContent = 'Could not load the leaderboard.'; return; }

  // First visit and no chosen name yet → invite the user to pick one.
  if (!boardState.promptedForName && data.me && !data.me.hasName) {
    boardState.promptedForName = true;
    askDisplayName();
  }

  boardListEl.innerHTML = '';
  (data.rows || []).forEach(r => boardListEl.appendChild(boardRow(r)));
  const has = boardListEl.children.length > 0;
  boardEmptyEl.hidden = has;
  if (!has) boardEmptyEl.textContent = 'No one is on the board yet. Learn some words to claim a spot!';

  // Show the user's own standing if they're outside the visible rows.
  const meInList = (data.rows || []).some(r => r.isMe);
  if (data.me && data.me.rank > 0 && !meInList) {
    boardMeEl.hidden = false;
    boardMeEl.innerHTML = '<h2>Your rank</h2><div class="big" style="font-size:22px">' +
      medal(data.me.rank) + ' · ' + data.me.value + '</div>';
  } else {
    boardMeEl.hidden = true;
  }
}

document.querySelectorAll('[data-metric]').forEach(c => {
  c.addEventListener('click', () => {
    document.querySelectorAll('[data-metric]').forEach(x => x.classList.toggle('chip-on', x === c));
    boardState.metric = c.dataset.metric;
    cloudSet('ui.board.metric', boardState.metric);
    hapticSelect();
    loadBoard();
  });
});

// ---------------------------------------------------------------------------
// Display-name modal (no native Telegram text input, so we roll our own)
// ---------------------------------------------------------------------------
function askDisplayName() {
  const back = document.createElement('div');
  back.className = 'modal-back';
  back.innerHTML =
    '<div class="modal">' +
    '<h2>Pick a leaderboard name</h2>' +
    '<p class="sub">This is what others see next to your rank. Skip it and you\'ll get a fun random name.</p>' +
    '<input class="search" id="name-input" maxlength="24" placeholder="e.g. WordNinja" autocomplete="off">' +
    '<div class="modal-actions">' +
    '<button class="chip" id="name-skip">Skip</button>' +
    '<button class="chip chip-on" id="name-save">Save</button>' +
    '</div></div>';
  document.body.appendChild(back);
  const input = back.querySelector('#name-input');
  input.focus();
  const close = () => back.remove();
  back.querySelector('#name-skip').addEventListener('click', close);
  back.querySelector('#name-save').addEventListener('click', async () => {
    const name = input.value.trim();
    if (!name) { close(); return; }
    try { await api('/api/leaderboard/name', { method: 'POST', body: JSON.stringify({ name }) }); }
    catch (e) { /* keep funny name on failure */ }
    close();
    loadBoard();
  });
}

// ---------------------------------------------------------------------------
// Swipe trainer — reusable by Review (SRS) and Decks (Leitner).
//   opts.load()         → Promise<[{front, back}]>  (initial + refills)
//   opts.onAnswer(c, k) → Promise   (k = true for "knew it")
//   opts.doneText / opts.emptyText / opts.onProgress(remaining)
// ---------------------------------------------------------------------------

// Native bottom buttons (Bot API 7.10+) replace the in-page answer buttons.
const SUCCESS_HEX = '#4CAF50', DANGER_HEX = '#f44336'; // mirror --success/--danger
const nativeAnswerButtons = (() => {
  try { return tg.isVersionAtLeast && tg.isVersionAtLeast('7.10') && !!tg.SecondaryButton; }
  catch (e) { return false; }
})();

// Only one swipe session is live at a time; switching tabs/views tears the
// previous one down so its native buttons can't leak onto other screens.
let teardownSession = null;

function createSwipeSession(container, opts) {
  let queue = [];
  let answered = 0, knownCount = 0;
  let finished = false;

  if (teardownSession) teardownSession();
  const onMain = () => commit(true, area.querySelector('.swipe-card'));
  const onSecondary = () => commit(false, area.querySelector('.swipe-card'));
  let buttonsShown = false;
  function showNativeButtons(on) {
    // Idempotent: showCard() runs once per card and onClick must not stack.
    if (!nativeAnswerButtons || on === buttonsShown) return;
    buttonsShown = on;
    try {
      if (on) {
        tg.MainButton.setParams({ text: '✅ Knew it', color: SUCCESS_HEX, text_color: '#ffffff', is_visible: true, is_active: true });
        tg.SecondaryButton.setParams({ text: '❌ Forgot', color: DANGER_HEX, text_color: '#ffffff', position: 'left', is_visible: true, is_active: true });
        tg.MainButton.onClick(onMain);
        tg.SecondaryButton.onClick(onSecondary);
      } else {
        tg.MainButton.offClick(onMain); tg.MainButton.hide();
        tg.SecondaryButton.offClick(onSecondary); tg.SecondaryButton.hide();
      }
    } catch (e) { /* ignore */ }
  }
  teardownSession = () => { showNativeButtons(false); teardownSession = null; };
  container.innerHTML =
    '<div class="swipe-area"></div>' +
    '<div class="swipe-actions">' +
    '<button class="swipe-btn forgot">❌ Forgot</button>' +
    '<button class="swipe-btn known">✅ Knew it</button>' +
    '</div><div class="swipe-progress"></div>';
  const area = container.querySelector('.swipe-area');
  const actions = container.querySelector('.swipe-actions');
  const progress = container.querySelector('.swipe-progress');

  function finish(msg) {
    finished = true;
    setCloseGuard(false);
    showNativeButtons(false);
    let summary = '';
    if (answered > 0) {
      hapticNotify('success');
      summary = completionRing(knownCount, answered);
    }
    area.innerHTML = '<div class="swipe-card" style="cursor:default">' +
      '<div class="swipe-front" style="font-size:42px">🎉</div>' +
      '<div class="swipe-back">' + esc(msg) + '</div>' + summary + '</div>';
    actions.style.display = 'none';
    progress.textContent = '';
  }

  function showCard() {
    if (!queue.length) { finish(opts.doneText || 'All done for now!'); return; }
    finished = false;
    showNativeButtons(true);
    actions.style.display = nativeAnswerButtons ? 'none' : 'flex';
    const card = queue[0];
    progress.textContent = queue.length + ' card' + (queue.length === 1 ? '' : 's') + ' left';
    if (opts.onProgress) opts.onProgress(queue.length);

    const el = document.createElement('div');
    el.className = 'swipe-card';
    el.innerHTML =
      '<div class="swipe-stamp known">Knew it</div>' +
      '<div class="swipe-stamp forgot">Forgot</div>' +
      '<div class="swipe-front">' + esc(card.front) + '</div>' +
      '<div class="swipe-back" hidden>' + card.back + '</div>' +
      '<div class="swipe-hint">Tap to reveal · swipe → knew it · ← forgot</div>';
    area.innerHTML = '';
    area.appendChild(el);

    const front = el.querySelector('.swipe-front');
    const back = el.querySelector('.swipe-back');
    const hint = el.querySelector('.swipe-hint');
    let revealed = false;
    function reveal() { revealed = true; front.hidden = true; back.hidden = false; hint.textContent = 'Swipe → knew it · ← forgot'; }
    // A tap reveals the back; a drag must not. `moved` tells them apart.
    el.addEventListener('click', () => { if (!revealed && !moved) reveal(); });

    // Drag to swipe.
    let startX = 0, dx = 0, dragging = false, moved = false;
    const knownStamp = el.querySelector('.swipe-stamp.known');
    const forgotStamp = el.querySelector('.swipe-stamp.forgot');
    el.addEventListener('pointerdown', e => { dragging = true; moved = false; startX = e.clientX; el.classList.add('dragging'); el.setPointerCapture(e.pointerId); });
    el.addEventListener('pointermove', e => {
      if (!dragging) return;
      dx = e.clientX - startX;
      if (Math.abs(dx) > 6) moved = true;
      el.style.transform = 'translateX(' + dx + 'px) rotate(' + (dx / 20) + 'deg)';
      knownStamp.style.opacity = dx > 0 ? Math.min(1, dx / 80) : 0;
      forgotStamp.style.opacity = dx < 0 ? Math.min(1, -dx / 80) : 0;
    });
    el.addEventListener('pointerup', () => {
      dragging = false;
      el.classList.remove('dragging');
      if (Math.abs(dx) > 90) { commit(dx > 0, el); }
      else { el.style.transform = ''; knownStamp.style.opacity = 0; forgotStamp.style.opacity = 0; }
      dx = 0;
    });
  }

  function commit(known, el) {
    if (finished || !queue.length) return; // native buttons can race the teardown
    hapticNotify(known ? 'success' : 'error');
    const card = queue.shift();
    answered++;
    if (known) knownCount++;
    setCloseGuard(true); // a stray swipe-down must not lose the session
    if (el) {
      el.classList.add('leaving');
      el.style.transform = 'translateX(' + (known ? 600 : -600) + 'px) rotate(' + (known ? 40 : -40) + 'deg)';
      el.style.opacity = '0';
    }
    Promise.resolve(opts.onAnswer(card, known)).catch(() => {
      // The answer didn't reach the server — put the card back so the
      // session can't silently lose progress (max one retry per card).
      card._retries = (card._retries || 0) + 1;
      if (card._retries > 1) return;
      queue.push(card);
      if (finished) showCard(); else progress.textContent = queue.length + ' card' + (queue.length === 1 ? '' : 's') + ' left';
    });
    setTimeout(showCard, 180);
  }

  actions.querySelector('.known').addEventListener('click', () => commit(true, area.querySelector('.swipe-card')));
  actions.querySelector('.forgot').addEventListener('click', () => commit(false, area.querySelector('.swipe-card')));

  area.innerHTML = '<div class="skeleton" style="position:absolute;inset:0;border-radius:18px"></div>';
  Promise.resolve(opts.load()).then(cards => {
    queue = cards || [];
    if (!queue.length) { finish(opts.emptyText || 'Nothing to review right now.'); return; }
    showCard();
  }).catch(() => { area.innerHTML = '<p class="empty">Could not load. Try again later.</p>'; });
}

// ---------------------------------------------------------------------------
// Review (SRS) tab
// ---------------------------------------------------------------------------
function loadReview() {
  createSwipeSession(document.getElementById('review-swipe'), {
    load: async () => {
      const data = await api('/api/review/next?limit=30');
      return (data.items || []).map(it => ({
        front: it.term,
        back: it.meaning ? esc(it.meaning) : '<span class="ex">(no definition saved)</span>',
        term: it.term,
      }));
    },
    onAnswer: (card, known) => api('/api/review/answer', { method: 'POST', body: JSON.stringify({ term: card.term, known }) }),
    emptyText: 'No words are due for review right now. Great job — come back later!',
    doneText: 'Review complete! Come back tomorrow to keep your streak going.',
  });
}

// ---------------------------------------------------------------------------
// Decks (Leitner)
// ---------------------------------------------------------------------------
const decksHome = document.getElementById('decks-home');
const decksStudy = document.getElementById('decks-study');
const decksListEl = document.getElementById('decks-list');
const decksEmptyEl = document.getElementById('decks-empty');

function deckCardEl(d) {
  const el = document.createElement('div');
  el.className = 'card deck';
  el.innerHTML =
    '<div class="deck-head"><div class="word-term">' + esc(d.name) + '</div>' +
    '<div class="deck-pct">' + d.progressPct + '%</div></div>' +
    '<div class="sub">' + esc(d.description) + '</div>' +
    bar(d.progressPct, 'var(--accent)') +
    '<div class="sub" style="margin-top:8px">' +
    (d.due > 0 ? '🔵 ' + d.due + ' to study now' : '✅ All caught up') +
    ' · ' + d.mastered + '/' + d.total + ' mastered</div>';
  el.addEventListener('click', () => openDeck(d));
  return el;
}

async function loadDecks() {
  // Always return to the deck list when (re)entering the tab.
  closeDeck(true);
  decksListEl.innerHTML = skeletonRows(2);
  let data;
  try { data = await api('/api/decks'); }
  catch (e) { decksListEl.innerHTML = ''; decksEmptyEl.hidden = false; decksEmptyEl.textContent = 'Could not load decks.'; return; }
  decksListEl.innerHTML = '';
  (data.decks || []).forEach(d => decksListEl.appendChild(deckCardEl(d)));
  const has = decksListEl.children.length > 0;
  decksEmptyEl.hidden = has;
  if (!has) decksEmptyEl.textContent = 'No decks available yet.';
}

function openDeck(d) {
  decksHome.hidden = true;
  decksStudy.hidden = false;
  document.getElementById('decks-study-title').textContent = '📚 ' + d.name;
  haptic('light');
  setSwipeGuard(true);
  tg.BackButton.show();
  tg.BackButton.onClick(backToDecks);

  createSwipeSession(document.getElementById('decks-swipe'), {
    load: async () => {
      const data = await api('/api/decks/study?deck=' + encodeURIComponent(d.id) + '&limit=30');
      return (data.items || []).map(it => ({
        front: it.term,
        back: esc(it.definition || '') + (it.example ? '<span class="ex">' + esc(it.example) + '</span>' : ''),
        term: it.term,
      }));
    },
    onAnswer: (card, known) => api('/api/decks/swipe', { method: 'POST', body: JSON.stringify({ deck: d.id, term: card.term, known }) }),
    emptyText: 'Nothing due in this deck right now — come back later!',
    doneText: 'Session complete! 🎉',
  });
}

function backToDecks() { closeDeck(); loadDecks(); }

function closeDeck(silent) {
  decksStudy.hidden = true;
  decksHome.hidden = false;
  if (teardownSession) teardownSession();
  setSwipeGuard(false);
  setCloseGuard(false);
  tg.BackButton.hide();
  if (!silent) tg.BackButton.offClick(backToDecks);
}

// ---------------------------------------------------------------------------
// Settings (opened via Telegram's native Settings button)
// ---------------------------------------------------------------------------
const TOGGLE_LABELS = {
  tts: 'Pronunciation audio', tips: 'Daily grammar tips', quiz: 'Quizzes',
  idiom: 'Idiom of the day', collocation: 'Collocation of the day', story: 'Mini stories',
  review: 'Spaced-repetition reviews', daily_review: 'Daily word recap', digest: 'Weekly digest',
};
let settingsReturnView = 'dashboard';

function row(label, control) {
  return '<div class="set-row"><span>' + esc(label) + '</span>' + control + '</div>';
}

async function postSetting(key, value) {
  haptic('light');
  try {
    await api('/api/settings', { method: 'POST', body: JSON.stringify({ key, value }) });
    return true;
  } catch (e) {
    hapticNotify('error');
    return false; // callers roll the optimistic UI back
  }
}

function renderSettings(s) {
  const body = document.getElementById('settings-body');
  const labels = s.levelLabels || {};
  let html = '<div class="card"><h2>Difficulty level</h2><div class="chips" id="set-levels">';
  s.levels.forEach(l => {
    html += '<button class="chip' + (l === s.level ? ' chip-on' : '') + '" data-level="' + l + '">' + esc(labels[l] || l) + '</button>';
  });
  html += '</div></div>';

  html += '<div class="card"><h2>Leaderboard name</h2>' +
    '<div class="set-row" style="gap:8px">' +
    '<input class="search" id="set-name" maxlength="24" style="margin-bottom:0" placeholder="Choose a name…" value="' + esc(s.name || '') + '">' +
    '<button class="chip chip-on" id="set-name-save">Save</button></div>' +
    '<div class="sub">This is what others see next to your rank. Leave blank for a fun random name.</div></div>';

  html += '<div class="card">' +
    row('Pause scheduled sends', toggleHTML('paused', s.paused)) +
    row('Delivery every (minutes)', '<input class="num" id="set-interval" type="number" min="15" max="1440" value="' + s.interval + '">') +
    '</div>';

  html += '<div class="card"><h2>Content</h2>';
  Object.keys(TOGGLE_LABELS).forEach(k => { html += row(TOGGLE_LABELS[k], toggleHTML(k, !!s.toggles[k])); });
  html += '</div>';

  body.innerHTML = html;

  body.querySelectorAll('[data-level]').forEach(c => c.addEventListener('click', async () => {
    const prev = body.querySelector('[data-level].chip-on');
    body.querySelectorAll('[data-level]').forEach(x => x.classList.toggle('chip-on', x === c));
    hapticSelect();
    if (!await postSetting('level', c.dataset.level)) {
      body.querySelectorAll('[data-level]').forEach(x => x.classList.toggle('chip-on', x === prev));
    }
  }));

  const nameInput = document.getElementById('set-name');
  const nameSave = document.getElementById('set-name-save');
  nameSave.addEventListener('click', async () => {
    const name = nameInput.value.trim();
    if (!name) return;
    haptic('light');
    nameSave.disabled = true;
    nameSave.textContent = 'Saved ✓';
    try { await api('/api/leaderboard/name', { method: 'POST', body: JSON.stringify({ name }) }); }
    catch (e) { nameSave.textContent = 'Retry'; nameSave.disabled = false; return; }
    setTimeout(() => { nameSave.textContent = 'Save'; nameSave.disabled = false; }, 1200);
  });
  body.querySelectorAll('.switch input').forEach(inp => inp.addEventListener('change', async () => {
    if (!await postSetting(inp.dataset.key, inp.checked)) inp.checked = !inp.checked;
  }));
  const intv = document.getElementById('set-interval');
  let lastInterval = parseInt(intv.value, 10);
  intv.addEventListener('change', async () => {
    let n = parseInt(intv.value, 10); if (isNaN(n)) n = 60;
    n = Math.max(15, Math.min(1440, n)); intv.value = n;
    if (await postSetting('interval', n)) lastInterval = n;
    else intv.value = lastInterval;
  });
}

function toggleHTML(key, on) {
  return '<label class="switch"><input type="checkbox" data-key="' + key + '"' + (on ? ' checked' : '') + '><span class="slider"></span></label>';
}

async function openSettings() {
  if (teardownSession) teardownSession(); // hide native session buttons
  settingsReturnView = currentView;
  document.querySelectorAll('.view').forEach(v => { v.hidden = true; });
  views.settings.hidden = false;
  tg.BackButton.show();
  tg.BackButton.onClick(closeSettings);
  document.getElementById('settings-body').innerHTML = skeletonCards(3);
  try { renderSettings(await api('/api/settings')); }
  catch (e) { document.getElementById('settings-body').innerHTML = '<p class="empty">Could not load settings.</p>'; }
}

function closeSettings() {
  tg.BackButton.offClick(closeSettings);
  showView(settingsReturnView);
}

try {
  tg.SettingsButton.show();
  tg.SettingsButton.onClick(openSettings);
} catch (e) { /* older Telegram clients */ }

// ---------------------------------------------------------------------------
// Boot
// ---------------------------------------------------------------------------
const loaders = {
  dashboard: loadDashboard,
  vocab: () => loadVocab(true),
  decks: loadDecks,
  board: loadBoard,
  review: loadReview,
};

// Restore cross-device UI state, then land on the user's last tab.
(async () => {
  const metric = await cloudGet('ui.board.metric');
  if (metric === 'words' || metric === 'mastered') {
    boardState.metric = metric;
    document.querySelectorAll('[data-metric]').forEach(x =>
      x.classList.toggle('chip-on', x.dataset.metric === metric));
  }
  let tab = await cloudGet('lastTab');
  if (!tab || !loaders[tab]) tab = 'dashboard';
  showView(tab);
})();
