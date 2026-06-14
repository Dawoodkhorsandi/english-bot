'use strict';

const tg = window.Telegram.WebApp;
tg.ready();
tg.expand();
// Blend the native bottom-bar area with the app's tab bar.
try { tg.setBottomBarColor(tg.themeParams.bottom_bar_bg_color || 'bg_color'); } catch (e) { /* older clients */ }

// Public config (bot handle + web app URL) for the share/invite link. Filled
// from /api/config at boot; defaults keep the share button working offline.
const config = { botUsername: '@mymusclememorybot', webAppURL: '' };

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

// Wrap English words (2+ letters) in tappable spans for dictionary lookup.
// Operates on raw text, escaping each segment individually.
function tappableText(text) {
  if (!text) return '';
  var s = String(text);
  var result = '';
  var i = 0;
  var re = /[A-Za-z]{2,}(?:['\u2019-][A-Za-z]+)*/g;
  var m;
  while ((m = re.exec(s)) !== null) {
    result += esc(s.slice(i, m.index));
    result += '<span class="tw">' + esc(m[0]) + '</span>';
    i = re.lastIndex;
  }
  result += esc(s.slice(i));
  return result;
}

// ---------------------------------------------------------------------------
// Tab navigation
// ---------------------------------------------------------------------------
const views = {};
document.querySelectorAll('.view').forEach(v => { views[v.id.replace('view-', '')] = v; });
let currentView = 'profile';

function showView(name) {
  try { tg.BackButton.hide(); } catch (e) { /* ignore */ } // reset on tab switch
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

// streakRing renders the hero streak ring: a circular gauge (current toward
// best) with the streak count + flame centred.
function streakRing(current, best) {
  const pct = best > 0 ? Math.min(100, current * 100 / best) : (current > 0 ? 100 : 0);
  const r = 34, c = 2 * Math.PI * r, off = c * (1 - pct / 100);
  return '<div class="streak-ring">' +
    '<svg class="ring" width="84" height="84" viewBox="0 0 84 84">' +
    '<circle class="track" cx="42" cy="42" r="' + r + '" fill="none" stroke-width="7"></circle>' +
    '<circle class="fill streak" cx="42" cy="42" r="' + r + '" fill="none" stroke-width="7" stroke-linecap="round"' +
    ' stroke-dasharray="' + c.toFixed(1) + '" stroke-dashoffset="' + off.toFixed(1) + '"></circle></svg>' +
    '<div class="streak-center"><div class="streak-num">' + current + '</div>' +
    '<div class="streak-flame">' + (current >= 3 ? '🔥' : '✨') + '</div></div></div>';
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
  const flame = s.current_streak >= 3 ? ' 🔥' : '';

  let html = '<h1>📊 Profile</h1>' +
    '<div class="chips dash-tabs">' +
    '<button class="chip chip-on" data-dtab="overview">Overview</button>' +
    '<button class="chip" data-dtab="achievements">🏆 Achievements</button>' +
    '<button class="chip" data-dtab="statistics">📈 Statistics</button></div>';

  // --- Overview sub-tab (minimal: streak + vocab tiles) ---
  html += '<div id="dtab-overview">';
  if (s.paused) {
    html += '<div class="card self-paced"><h2>🧘 Self-paced mode</h2>' +
      '<div class="sub">Automatic messages are silenced. Learn whenever you like — grab a new word or run a review right here.</div>' +
      '<div class="chips" style="margin-top:12px">' +
      '<button class="chip chip-on" id="sp-word">📘 Get a new word</button>' +
      '<button class="chip" id="sp-review">🧠 Review now</button></div></div>';
  }
  const shareBtn = s.current_streak >= 3
    ? '<button class="chip" id="share-streak">📣 Share my streak</button>' : '';
  html += '<div class="card hero"><h2>Daily Streak</h2>' +
    '<div class="hero-row">' + streakRing(s.current_streak, s.longest_streak) +
    '<div class="hero-meta">' +
    '<div class="hero-line">' + s.current_streak + ' day' + (s.current_streak === 1 ? '' : 's') + flame + '</div>' +
    '<div class="sub">Best: ' + s.longest_streak + ' day' + (s.longest_streak === 1 ? '' : 's') + '</div>' +
    '<button class="link-btn" id="streak-info">ⓘ How streaks work</button>' +
    '</div></div>' +
    '<div id="streak-explain" class="explain" hidden>A <b>streak</b> is how many days in a row you keep learning. ' +
    'Do anything that counts — get a new word, finish a Review, or study a deck — at least once a day to keep it alive. ' +
    'Miss a whole day and it resets to zero, so a little every day beats a lot once a week. 💪</div>' +
    (shareBtn ? '<div class="hero-actions">' + shareBtn + '</div>' : '') +
    '</div>';

  html += '<div class="card"><h2>Vocabulary</h2><div class="stat-tiles">' +
    '<div class="stat-tile"><div class="stat-emoji">📘</div><div class="stat-n">' + s.words + '</div><div class="sub">Words</div></div>' +
    '<div class="stat-tile"><div class="stat-emoji">✅</div><div class="stat-n">' + s.mastered + '</div><div class="sub">Mastered</div></div>' +
    '<div class="stat-tile"><div class="stat-emoji">🎯</div><div class="stat-n">' + s.verbs + '</div><div class="sub">Drills</div></div>' +
    '</div></div>';

  html += activitySection(s);

  html += '<div class="card"><h2>Level</h2>' +
    '<div class="big" style="font-size:22px">' + esc(s.level) + '</div>' +
    '<div class="sub">' + s.active_days + ' active day' + (s.active_days === 1 ? '' : 's') + ' total' +
    (s.member_since ? ' · member since ' + esc(s.member_since) : '') + '</div></div>';

  // Link to English App card
  html += '<div class="card" id="link-app-card"><h2>📱 Link to English App</h2>' +
    '<div class="sub" style="margin-bottom:12px">Copy your login code to sign in to the mobile app.</div>' +
    '<button id="copy-login-btn" style="background:var(--accent);color:var(--accent-text);border:none;border-radius:12px;padding:14px 24px;font-size:15px;font-weight:600;cursor:pointer;width:100%;">Copy Login Code</button>' +
    '<p id="copy-status" style="color:var(--success);margin-top:8px;font-weight:600;text-align:center;" hidden>✓ Copied! Return to English App.</p>' +
    '</div>';

  html += '</div>'; // end dtab-overview

  // --- Achievements sub-tab ---
  html += '<div id="dtab-achievements" hidden>';
  html += achievementsSection(s);
  html += '</div>';

  // --- Statistics sub-tab ---
  html += '<div id="dtab-statistics" hidden>';

  const lib = [
    { emoji: '💬', label: 'Idioms', n: s.idioms || 0 },
    { emoji: '🔗', label: 'Collocations', n: s.collocations || 0 },
    { emoji: '📖', label: 'Stories', n: s.stories || 0 },
    { emoji: '💡', label: 'Tips', n: s.tips || 0 },
  ];
  if (lib.some(k => k.n > 0)) {
    html += '<div class="card"><h2>Library</h2><div class="stat-tiles">' +
      lib.map(k => '<div class="stat-tile"><div class="stat-emoji">' + k.emoji + '</div>' +
        '<div class="stat-n">' + k.n + '</div><div class="sub">' + k.label + '</div></div>').join('') +
      '</div></div>';
  }

  if (s.quiz_answered > 0) {
    html += '<div class="card"><h2>Quiz accuracy</h2>' +
      '<div class="big">' + s.quiz_pct + '%</div>' +
      '<div class="sub">' + s.quiz_correct + ' / ' + s.quiz_answered + ' correct</div>' +
      bar(s.quiz_pct, 'var(--success)') + '</div>';
  }

  html += analyticsSection(s);

  html += '</div>'; // end dtab-statistics

  views.profile.innerHTML = html;

  // Sub-tab switching
  document.querySelectorAll('.dash-tabs .chip').forEach(btn => {
    btn.addEventListener('click', () => {
      hapticSelect();
      document.querySelectorAll('.dash-tabs .chip').forEach(b => b.classList.remove('chip-on'));
      btn.classList.add('chip-on');
      const tab = btn.dataset.dtab;
      document.getElementById('dtab-overview').hidden = (tab !== 'overview');
      document.getElementById('dtab-achievements').hidden = (tab !== 'achievements');
      document.getElementById('dtab-statistics').hidden = (tab !== 'statistics');
    });
  });

  const info = document.getElementById('streak-info');
  if (info) {
    info.addEventListener('click', () => {
      hapticSelect();
      const ex = document.getElementById('streak-explain');
      ex.hidden = !ex.hidden;
    });
  }

  // Self-paced banner CTAs: jump straight into pull-based learning.
  const spWord = document.getElementById('sp-word');
  if (spWord) spWord.addEventListener('click', () => { hapticSelect(); showView('decks'); openPractice('word'); });
  const spReview = document.getElementById('sp-review');
  if (spReview) spReview.addEventListener('click', () => { hapticSelect(); showView('review'); });

  const share = document.getElementById('share-streak');
  if (share) {
    share.addEventListener('click', () => {
      haptic('light');
      // Invite friends to the bot itself (t.me/<handle>); t.me/share works on
      // every client with no media asset or Bot API needed.
      const handle = (config.botUsername || '@mymusclememorybot').replace(/^@/, '');
      const botLink = 'https://t.me/' + handle;
      const text = 'I\'m learning English with @' + handle + ' — ' + s.current_streak +
                   '-day streak! 🔥 Join me:';
      const link = 'https://t.me/share/url?url=' + encodeURIComponent(botLink) +
                   '&text=' + encodeURIComponent(text);
      try { tg.openTelegramLink(link); } catch (e) { window.open(link, '_blank'); }
    });
  }

  maybeOfferHomeScreen(s.current_streak);

  // Copy Login Code button for mobile app linking
  const copyBtn = document.getElementById('copy-login-btn');
  if (copyBtn) {
    copyBtn.addEventListener('click', () => {
      navigator.clipboard.writeText(tg.initData).then(() => {
        document.getElementById('copy-status').hidden = false;
        copyBtn.textContent = 'Copied!';
        haptic('success');
      }).catch(() => {
        const ta = document.createElement('textarea');
        ta.value = tg.initData;
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
        document.getElementById('copy-status').hidden = false;
        copyBtn.textContent = 'Copied!';
      });
    });
  }
}

// After a week-long streak, offer the home-screen shortcut once (Bot API 8.0+).
async function maybeOfferHomeScreen(streak) {
  if (streak < 7 || typeof tg.addToHomeScreen !== 'function') return;
  if (await cloudGet('ui.hs.prompted')) return;
  try {
    tg.checkHomeScreenStatus(status => {
      if (status !== 'missed' && status !== 'unknown') return;
      const card = document.createElement('div');
      card.className = 'card';
      card.innerHTML = '<h2>One tap away</h2>' +
        '<div class="sub">A ' + streak + '-day streak! Add the app to your home screen to keep it going.</div>' +
        '<div class="chips" style="margin:12px 0 0">' +
        '<button class="chip chip-on" id="hs-add">➕ Add to Home Screen</button>' +
        '<button class="chip" id="hs-later">Not now</button></div>';
      views.profile.prepend(card);
      card.querySelector('#hs-add').addEventListener('click', () => {
        haptic('light');
        try { tg.addToHomeScreen(); } catch (e) { /* ignore */ }
        cloudSet('ui.hs.prompted', '1');
        card.remove();
      });
      card.querySelector('#hs-later').addEventListener('click', () => {
        cloudSet('ui.hs.prompted', '1');
        card.remove();
      });
    });
  } catch (e) { /* older clients */ }
}

// GitHub-style activity heatmap: HEAT_WEEKS week columns × 7 day rows (Mon top),
// ending in the current week. Shade deepens with the day's activity count,
// GitHub-commit style. Pure CSS grid — no chart library.
const HEAT_WEEKS = 17;
const MONTH_ABBR = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

function heatLevel(n) {
  if (n >= 10) return 4;
  if (n >= 6) return 3;
  if (n >= 3) return 2;
  if (n >= 1) return 1;
  return 0;
}

// heatStart returns the Monday of the leftmost heatmap column.
function heatStart() {
  const today = new Date();
  const start = new Date(today);
  start.setDate(today.getDate() - ((today.getDay() + 6) % 7) - (HEAT_WEEKS - 1) * 7);
  return start;
}

// activitySection renders the whole "Activity" card: headline numbers, a
// per-week bar plot, the month-labelled heatmap, and the intensity legend.
function activitySection(s) {
  const counts = s.activity_counts || {};
  let total = 0, best = 0, bestKey = '';
  for (const k in counts) {
    const n = counts[k] || 0; total += n;
    if (n > best) { best = n; bestKey = k; }
  }
  const today = new Date();
  let week = 0;
  for (let i = 0; i < 7; i++) {
    const d = new Date(today); d.setDate(today.getDate() - i);
    week += counts[localDateKey(d)] || 0;
  }
  const tile = (emoji, n, label) =>
    '<div class="stat-tile"><div class="stat-emoji">' + emoji + '</div>' +
    '<div class="stat-n">' + n + '</div><div class="sub">' + label + '</div></div>';

  return '<div class="card"><h2>Activity · last 4 months</h2>' +
    '<div class="stat-tiles t3">' +
    tile('⚡', total, 'Activities') +
    tile('📆', week, 'This week') +
    tile('🌟', best, 'Best day') +
    '</div>' +
    weeklyBarsHTML(counts) +
    monthLabelsHTML() +
    heatmapHTML(counts) +
    '<div class="heat-legend"><span>Less</span>' +
    '<i class="heat-cell"></i><i class="heat-cell l1"></i><i class="heat-cell l2"></i>' +
    '<i class="heat-cell l3"></i><i class="heat-cell l4"></i><span>More</span></div></div>';
}

// monthLabelsHTML renders a row of month abbreviations aligned to the heatmap's
// week columns — a label appears on the first column that falls in a new month.
function monthLabelsHTML() {
  const d = heatStart();
  let prevMonth = -1, cells = '';
  for (let w = 0; w < HEAT_WEEKS; w++) {
    const m = d.getMonth();
    cells += '<span>' + (m !== prevMonth ? MONTH_ABBR[m] : '') + '</span>';
    prevMonth = m;
    d.setDate(d.getDate() + 7);
  }
  return '<div class="heat-months">' + cells + '</div>';
}

// weeklyBarsHTML renders a compact bar plot of total activity per week across
// the heatmap window — the "fancier" companion to the day-level heatmap.
function weeklyBarsHTML(counts) {
  const todayKey = localDateKey(new Date());
  const d = heatStart();
  const weeks = [];
  let max = 1;
  for (let w = 0; w < HEAT_WEEKS; w++) {
    let sum = 0, label = '';
    for (let i = 0; i < 7; i++) {
      const key = localDateKey(d);
      if (i === 0) label = key;
      if (key <= todayKey) sum += counts[key] || 0;
      d.setDate(d.getDate() + 1);
    }
    if (sum > max) max = sum;
    weeks.push({ sum, label });
  }
  const bars = weeks.map(wk =>
    '<div class="wbar" data-tip="' + wk.sum + ' in week of ' + wk.label + '">' +
    '<div class="wbar-fill" style="height:' + Math.round(wk.sum * 100 / max) + '%"></div></div>'
  ).join('');
  return '<div class="wbars">' + bars + '</div>';
}

function heatmapHTML(counts) {
  const todayKey = localDateKey(new Date());
  let cells = '';
  const d = heatStart();
  for (let i = 0; i < HEAT_WEEKS * 7; i++) {
    const key = localDateKey(d);
    const n = counts[key] || 0;
    let cls = 'heat-cell';
    if (key > todayKey) cls += ' future';
    else if (n > 0) cls += ' l' + heatLevel(n);
    if (key === todayKey) cls += ' today';
    const tip = n > 0 ? n + ' item' + (n === 1 ? '' : 's') + ' on ' + key : key;
    cells += '<div class="' + cls + '" data-tip="' + tip + '"></div>';
    d.setDate(d.getDate() + 1);
  }
  return '<div class="heatmap">' + cells + '</div>';
}

function localDateKey(d) {
  return d.getFullYear() + '-' + ((d.getMonth() + 1) + '').padStart(2, '0') +
         '-' + (d.getDate() + '').padStart(2, '0');
}

// ---------------------------------------------------------------------------
// Achievements (expandable by category)
// ---------------------------------------------------------------------------
function achievementsSection(s) {
  const achs = s.achievements || [];
  if (achs.length === 0) return '';
  const unlocked = s.ach_unlocked || 0;
  const total = s.ach_total || achs.length;

  // Group by category
  const cats = {};
  for (const a of achs) {
    if (!cats[a.category]) cats[a.category] = [];
    cats[a.category].push(a);
  }

  // Sort categories: those with most unlocked first
  const catOrder = Object.keys(cats).sort((a, b) => {
    const aU = cats[a].filter(x => x.unlocked).length;
    const bU = cats[b].filter(x => x.unlocked).length;
    return bU - aU;
  });

  let html = '<div class="card"><h2>🏆 Achievements · ' + unlocked + '/' + total + '</h2>' +
    '<div class="ach-bar">' + bar(unlocked * 100 / total, 'var(--accent)') +
    '<div class="sub" style="margin-top:4px">' + unlocked + ' unlocked</div></div>';

  for (const cat of catOrder) {
    const items = cats[cat];
    const catUnlocked = items.filter(x => x.unlocked).length;
    const catTotal = items.length;
    const allDone = catUnlocked === catTotal;

    html += '<div class="ach-cat">' +
      '<button class="ach-cat-header" data-cat="' + esc(cat) + '">' +
      '<span class="ach-cat-title">' + esc(cat) + ' <span class="sub">(' + catUnlocked + '/' + catTotal + ')</span></span>' +
      '<span class="ach-cat-toggle">▼</span></button>' +
      '<div class="ach-cat-body">';

    for (const a of items) {
      const cls = a.unlocked ? 'badge unlocked' : 'badge locked';
      let progress = '';
      if (!a.unlocked && a.target > 1) {
        progress = '<div class="badge-progress">' +
          bar(a.progress * 100 / a.target, 'var(--accent)') +
          '<div class="sub">' + a.progress + ' / ' + a.target + '</div></div>';
      }
      html += '<div class="' + cls + '">' +
        '<span class="badge-icon">' + a.icon + '</span>' +
        '<div class="badge-name">' + esc(a.name) + '</div>' +
        '<div class="badge-desc">' + esc(a.description) + '</div>' +
        progress + '</div>';
    }
    html += '</div></div>';
  }

  html += '</div>';

  // Wire expand/collapse after render
  setTimeout(() => {
    document.querySelectorAll('.ach-cat-header').forEach(btn => {
      btn.addEventListener('click', () => {
        hapticSelect();
        const body = btn.nextElementSibling;
        const toggle = btn.querySelector('.ach-cat-toggle');
        const open = body.style.display !== 'none';
        body.style.display = open ? 'none' : '';
        toggle.textContent = open ? '▶' : '▼';
      });
    });
  }, 0);

  return html;
}

// Render achievement badges for a profile comparison view (compact, no expand).
function achCompareHTML(myAchs, theirAchs, myName, theirName) {
  if (!myAchs || !theirAchs) return '';
  // Group by category
  const cats = {};
  for (const a of myAchs) {
    if (!cats[a.category]) cats[a.category] = [];
    cats[a.category].push({ me: a, them: null });
  }
  for (const a of theirAchs) {
    if (!cats[a.category]) cats[a.category] = [];
    const existing = cats[a.category].find(x => x.me && x.me.id === a.id);
    if (existing) {
      existing.them = a;
    } else {
      cats[a.category].push({ me: null, them: a });
    }
  }

  let html = '<div class="card"><h2>🏆 Achievements</h2>';
  for (const cat of Object.keys(cats)) {
    const items = cats[cat];
    const myDone = items.filter(x => x.me && x.me.unlocked).length;
    const theirDone = items.filter(x => x.them && x.them.unlocked).length;
    html += '<div class="ach-compare-cat"><div class="sub" style="margin-bottom:6px;font-weight:600">' +
      esc(cat) + ' — ' + myDone + '/' + items.length + ' vs ' + theirDone + '/' + items.length + '</div>';
    html += '<div class="ach-grid">';
    for (const { me, them } of items) {
      const a = me || them;
      const iHave = me && me.unlocked;
      const theyHave = them && them.unlocked;
      let ring = '';
      if (iHave && theyHave) ring = 'ach-ring-both';
      else if (iHave) ring = 'ach-ring-mine';
      else if (theyHave) ring = 'ach-ring-theirs';
      html += '<div class="badge ' + (iHave || theyHave ? 'unlocked' : 'locked') + ' ' + ring + '">' +
        '<span class="badge-icon">' + a.icon + '</span>' +
        '<div class="badge-name">' + esc(a.name) + '</div>' +
        '<div class="badge-desc">' + (iHave ? '✅' : theyHave ? '🔸' : '—') + '</div></div>';
    }
    html += '</div></div>';
  }
  html += '</div>';
  return html;
}

// ---------------------------------------------------------------------------
// Learning Analytics
// ---------------------------------------------------------------------------
function analyticsSection(s) {
  if (!s._analytics) return '';
  const a = s._analytics;

  let html = '<div class="card"><h2>📊 Learning Analytics</h2>';

  // Content diversity
  if (a.content_diversity && a.content_diversity.length > 0) {
    html += '<h2>Content breakdown</h2>';
    const maxC = Math.max(...a.content_diversity.map(c => c.count));
    for (const c of a.content_diversity) {
      html += '<div class="analytics-bar">' +
        '<div class="analytics-bar-label">' + esc(c.label) + '</div>' +
        '<div class="analytics-bar-track"><div class="analytics-bar-fill" style="width:' +
        Math.round(c.count * 100 / maxC) + '%"></div></div>' +
        '<div class="analytics-bar-val">' + c.count + '</div></div>';
    }
  }

  // Quiz accuracy trend (sparkline)
  if (a.quiz_accuracy_trend && a.quiz_accuracy_trend.length > 0) {
    html += '<h2>Quiz accuracy · last 30 days</h2><div class="sparkline">';
    for (const d of a.quiz_accuracy_trend) {
      const h = Math.max(4, Math.round(d.pct * 0.36));
      html += '<div class="sparkline-cell" style="height:' + h + 'px" data-tip="' +
        esc(d.date) + ': ' + d.pct + '% (' + d.correct + '/' + d.total + ')"></div>';
    }
    html += '</div>';
  }

  // Activity by hour
  if (a.activity_by_hour && a.activity_by_hour.some(h => h.count > 0)) {
    html += '<h2>Activity by hour</h2><div class="hour-heat">';
    const maxH = Math.max(...a.activity_by_hour.map(h => h.count), 1);
    for (const h of a.activity_by_hour) {
      const pct = Math.round(h.count * 100 / maxH);
      const opacity = h.count === 0 ? 0.1 : Math.max(0.2, pct / 100);
      html += '<div class="hour-heat-cell" style="height:' + Math.max(3, pct) +
        '%;opacity:' + opacity + '" data-tip="' + h.hour + ':00 — ' + h.count + '"></div>';
    }
    html += '</div>';
  }

  // Weekly velocity
  if (a.weekly_velocity && a.weekly_velocity.length > 0) {
    html += '<h2>Words per week</h2><div class="sparkline">';
    const maxW = Math.max(...a.weekly_velocity.map(w => w.count), 1);
    for (const w of a.weekly_velocity) {
      const h = Math.max(4, Math.round(w.count * 36 / maxW));
      html += '<div class="sparkline-cell" style="height:' + h + 'px" data-tip="' +
        esc(w.week) + ': ' + w.count + ' words"></div>';
    }
    html += '</div>';
  }

  html += '</div>';
  return html;
}

// Fetch analytics data and attach it to the stats object.
async function loadAnalytics(s) {
  try {
    s._analytics = await api('/api/analytics');
  } catch (e) { /* analytics are optional — render without them */ }
  return s;
}

async function loadDashboard() {
  // Skeleton on first paint only; on later visits the old content stays
  // until fresh data replaces it (no flash).
  if (!views.profile.dataset.ready) views.profile.innerHTML = skeletonCards(3);
  try {
    const s = await api('/api/stats');
    await loadAnalytics(s);
    renderDashboard(s);
    views.profile.dataset.ready = '1';
  } catch (e) { views.profile.innerHTML = '<p class="empty">Could not load your stats.<br>Try again later.</p>'; }
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
  el.className = 'word expandable';
  el.innerHTML =
    '<div class="word-main"><div class="word-term">' + esc(w.term) + '</div>' +
    '<div class="word-meaning">' + esc(w.meaning || '—') + '</div>' +
    '<div class="word-text" hidden></div></div>' +
    '<span class="word-mastery" title="' + w.mastery + '">' + (MASTERY[w.mastery] || '🆕') + '</span>' +
    '<button class="star" aria-label="Bookmark word" aria-pressed="' + (w.bookmarked ? 'true' : 'false') + '">' + (w.bookmarked ? '⭐' : '☆') + '</button>';
  const star = el.querySelector('.star');
  let on = w.bookmarked;
  star.addEventListener('click', async ev => {
    ev.stopPropagation(); // the row itself toggles the detail view
    on = !on;
    star.textContent = on ? '⭐' : '☆';
    star.setAttribute('aria-pressed', on ? 'true' : 'false');
    haptic('light');
    try { await api('/api/bookmark', { method: 'POST', body: JSON.stringify({ term: w.term, on }) }); }
    catch (e) { on = !on; star.textContent = on ? '⭐' : '☆'; hapticNotify('error'); }
  });

  // Tap the row to open the full word card (fetched once, then toggled).
  const body = el.querySelector('.word-text');
  let loaded = false;
  el.addEventListener('click', async () => {
    haptic('light');
    if (loaded) { body.hidden = !body.hidden; return; }
    loaded = true;
    body.hidden = false;
    body.textContent = 'Loading…';
    try {
      const card = await api('/api/vocab/card?term=' + encodeURIComponent(w.term));
      var txt = card.text || card.meaning || '';
      body.innerHTML = txt ? tappableText(txt) : 'No saved card for this word yet.';
    } catch (e) {
      loaded = false;
      body.textContent = 'Could not load the card. Tap to retry.';
    }
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

// contentRow renders one Library item; tapping it always opens the details
// (full card text when the pool still has it, otherwise the full meaning).
function contentRow(it) {
  const el = document.createElement('div');
  el.className = 'word expandable';
  const detail = it.text || it.meaning || 'No saved card for this item.';
  const preview = it.meaning || (it.text ? it.text.slice(0, 90) : '—');
  el.innerHTML =
    '<div class="word-main"><div class="word-term">' + esc(it.term) + '</div>' +
    '<div class="word-meaning">' + esc(preview) + '</div>' +
    '<div class="word-text" hidden>' + tappableText(detail) + '</div>' +
    '</div>' +
    '<span class="word-date">' + esc(it.sent_at || '') + '</span>';
  const body = el.querySelector('.word-text');
  el.addEventListener('click', () => {
    haptic('light');
    body.hidden = !body.hidden;
    el.querySelector('.word-meaning').hidden = !body.hidden;
  });
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

const dictViewEl = document.getElementById('dict-view');
const filterChipsEl = document.querySelector('#view-vocab .chips:not(.lib-dtab)');

function initDictSearch() {
  loadDictionary();
}

function showLibTab(tab) {
  const isDict = tab === 'dict';
  searchEl.hidden = isDict;
  listEl.hidden = isDict;
  moreEl.hidden = isDict;
  emptyEl.hidden = true;
  dictViewEl.hidden = !isDict;
  filterChipsEl.hidden = isDict;
  if (isDict) initDictSearch();
  else loadVocab(true);
}

document.querySelectorAll('[data-lib-tab]').forEach(c => {
  c.addEventListener('click', () => {
    document.querySelectorAll('[data-lib-tab]').forEach(x => x.classList.toggle('chip-on', x === c));
    hapticSelect();
    showLibTab(c.dataset.libTab);
  });
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
  el.className = 'word expandable' + (r.isMe ? ' me' : '');
  // Privacy: we never show user profile photos — just an initial-letter badge.
  const initial = (r.name || '?').trim().charAt(0).toUpperCase();
  const avatar = '<div class="avatar-fallback">' + esc(initial) + '</div>';
  el.innerHTML =
    '<span class="rank">' + medal(r.rank) + '</span>' + avatar +
    '<div class="word-main"><div class="word-term">' + esc(r.name) + (r.isMe ? ' <span class="you">you</span>' : '') + '</div></div>' +
    '<span class="word-term">' + r.value + '</span>';
  // Tap a row to open that user's profile (opaque id — never a chat_id).
  if (r.id) el.addEventListener('click', () => openProfile(r.id));
  return el;
}

// ---------------------------------------------------------------------------
// Profile drill-down (Ranks tab): head-to-head comparison + kudos.
// ---------------------------------------------------------------------------
const BOARD_SUBVIEWS = ['board-home', 'board-profile'];
let boardBack = null;

function showBoardSub(id, onBack) {
  BOARD_SUBVIEWS.forEach(s => { const el = document.getElementById(s); if (el) el.hidden = (s !== id); });
  if (boardBack) { try { tg.BackButton.offClick(boardBack); } catch (e) { /* ignore */ } boardBack = null; }
  if (id === 'board-home') {
    try { tg.BackButton.hide(); } catch (e) { /* ignore */ }
  } else {
    boardBack = onBack || backToBoard;
    try { tg.BackButton.onClick(boardBack); tg.BackButton.show(); } catch (e) { /* ignore */ }
  }
}
function backToBoard() { showBoardSub('board-home'); }

const CMP_VERDICT = { 1: '<span class="vs-verdict up">▲ you lead</span>', '-1': '<span class="vs-verdict down">▼ they lead</span>', 0: '<span class="vs-verdict tie">= tied</span>' };

// versusRow draws one metric as a single tug-of-war track: your share fills from
// the left (accent), theirs from the right (muted); the divider sits where the
// two meet, so the longer side — the winner — is read at a glance. The share is
// of the pair total, so the bar always encodes who's ahead (a 0–0 metric sits at
// the centre and is called out as "no data yet").
function versusRow(m) {
  const total = m.me + m.them;
  const head = '<div class="vs-head"><span class="vs-metric">' + esc(m.label) + '</span>' +
    (total > 0 ? CMP_VERDICT[m.better] : '<span class="vs-verdict tie">no data yet</span>') + '</div>';
  if (total === 0) {
    return '<div class="vs-row">' + head + '<div class="vs-track empty"><span class="vs-none">—</span></div></div>';
  }
  const mePct = Math.round((m.me * 100) / total);
  return '<div class="vs-row">' + head +
    '<div class="vs-track">' +
    '<div class="vs-me" style="width:' + mePct + '%"><span class="vs-val">' + m.me + '</span></div>' +
    '<div class="vs-them" style="width:' + (100 - mePct) + '%"><span class="vs-val">' + m.them + '</span></div>' +
    '</div></div>';
}

async function openProfile(id) {
  showBoardSub('board-profile', backToBoard);
  haptic('light');
  const body = document.getElementById('profile-body');
  body.innerHTML = skeletonCards(2);
  let p;
  try { p = await api('/api/profile?id=' + encodeURIComponent(id)); }
  catch (e) { body.innerHTML = '<p class="empty">Could not load this profile.</p>'; return; }
  renderProfile(id, p);
}

function renderProfile(id, p) {
  const body = document.getElementById('profile-body');
  const initial = (p.name || '?').trim().charAt(0).toUpperCase();
  // Matchup header: you on the left, a VS medallion, them on the right.
  const kudosBtn = p.isMe ? '' :
    '<button class="kudos-btn' + (p.kudos.gaveByMe ? ' on' : '') + '" id="kudos-btn"' +
    ' aria-pressed="' + (p.kudos.gaveByMe ? 'true' : 'false') + '" aria-label="Give kudos">' +
    '👏 <span id="kudos-count">' + p.kudos.count + '</span></button>';
  let html = '<div class="card matchup">' +
    '<div class="vs-side"><div class="avatar-fallback lg you-av">★</div><div class="vs-name">You</div></div>' +
    '<div class="vs-medallion" aria-hidden="true">VS</div>' +
    '<div class="vs-side"><div class="avatar-fallback lg">' + esc(initial) + '</div>' +
    '<div class="vs-name">' + esc(p.name) + (p.isMe ? ' <span class="you">you</span>' : '') + '</div></div>' +
    '</div>';
  if (!p.isMe) {
    html += '<div class="kudos-bar">' + kudosBtn +
      '<span class="sub">' + p.kudos.count + (p.kudos.count === 1 ? ' learner cheered them on' : ' learners cheered them on') + '</span></div>';
  }
  html += '<div class="card"><h2>Head to head</h2>' +
    (p.metrics || []).map(versusRow).join('') + '</div>';
  html += '<div class="card"><h2>Their activity · last 4 months</h2>' +
    heatmapHTML(p.heatmap || {}) +
    '<div class="heat-legend"><span>Less</span>' +
    '<i class="heat-cell"></i><i class="heat-cell l1"></i><i class="heat-cell l2"></i>' +
    '<i class="heat-cell l3"></i><i class="heat-cell l4"></i><span>More</span></div></div>';

  // Achievement comparison
  if (p.achievements && p.achievements_my && p.achievements_their) {
    html += achCompareHTML(p.achievements_my, p.achievements_their, 'You', p.name);
  }

  body.innerHTML = html;

  const btn = document.getElementById('kudos-btn');
  if (btn) btn.addEventListener('click', async () => {
    haptic('light');
    const countEl = document.getElementById('kudos-count');
    const wasOn = btn.classList.contains('on');
    // Optimistic toggle.
    btn.classList.toggle('on');
    countEl.textContent = Math.max(0, parseInt(countEl.textContent, 10) + (wasOn ? -1 : 1));
    try {
      const res = await api('/api/kudos', { method: 'POST', body: JSON.stringify({ id }) });
      btn.classList.toggle('on', res.gaveByMe);
      btn.setAttribute('aria-pressed', res.gaveByMe ? 'true' : 'false');
      countEl.textContent = res.count;
      hapticNotify('success');
    } catch (e) {
      btn.classList.toggle('on', wasOn);
      btn.setAttribute('aria-pressed', wasOn ? 'true' : 'false');
      countEl.textContent = Math.max(0, parseInt(countEl.textContent, 10) + (wasOn ? 1 : -1));
      hapticNotify('error');
    }
  });
}

async function loadBoard() {
  showBoardSub('board-home'); // reset the drill-down when (re)entering the tab
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

// cardBack builds the reveal face shared by Review and Deck swipe cards:
// meaning/definition, then example, then Persian (RTL). Fields may be absent.
function cardBack(it) {
  const meaning = it.meaning || it.definition || '';
  let html = meaning
    ? '<div class="rev-meaning">' + tappableText(meaning) + '</div>'
    : '<div class="rev-meaning">(no definition saved)</div>';
  if (it.example) html += '<div class="ex">“' + tappableText(it.example) + '”</div>';
  if (it.persian) html += '<div class="rev-fa" dir="rtl">🇮🇷 ' + esc(it.persian) + '</div>';
  if (it.mnemonic) html += '<div class="rev-mnem">💡 ' + esc(it.mnemonic) + '</div>';
  return html;
}

// Note: native MainButton/SecondaryButton were tried as answer buttons
// (v1.26.0) and reverted — Telegram renders them below the webview, i.e.
// under the fixed tab bar. The in-page buttons stay above the navigation.
function createSwipeSession(container, opts) {
  let queue = [];
  let answered = 0, knownCount = 0;
  let finished = false;
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
    // Let the caller append a post-session footer (e.g. a level-up suggestion).
    if (opts.onFinish && answered > 0) opts.onFinish(knownCount, answered, container);
  }

  function showCard() {
    if (!queue.length) { finish(opts.doneText || 'All done for now!'); return; }
    finished = false;
    actions.style.display = 'flex';
    const card = queue[0];
    progress.textContent = queue.length + ' card' + (queue.length === 1 ? '' : 's') + ' left';
    if (opts.onProgress) opts.onProgress(queue.length);

    const el = document.createElement('div');
    el.className = 'swipe-card';
    el.innerHTML =
      '<div class="swipe-stamp known">Knew it</div>' +
      '<div class="swipe-stamp forgot">Forgot</div>' +
      '<div class="swipe-front">' + esc(card.front) + '</div>' +
      (card.sub ? '<div class="swipe-sub">' + esc(card.sub) + '</div>' : '') +
      '<div class="swipe-back" hidden>' + card.back + '</div>' +
      '<div class="swipe-hint">Tap to reveal · swipe → knew it · ← forgot</div>';
    area.innerHTML = '';
    area.appendChild(el);

    const front = el.querySelector('.swipe-front');
    const sub = el.querySelector('.swipe-sub');
    const back = el.querySelector('.swipe-back');
    const hint = el.querySelector('.swipe-hint');
    let revealed = false;
    // Tap flips the card: front (word) ⇄ back (meaning). A drag must not flip —
    // `moved` tells a tap from a swipe.
    function flip() {
      revealed = !revealed;
      front.hidden = revealed;
      if (sub) sub.hidden = revealed;
      back.hidden = !revealed;
      hint.textContent = (revealed ? 'Tap to hide' : 'Tap to reveal') + ' · swipe → knew it · ← forgot';
    }
    el.addEventListener('click', () => { if (!moved) flip(); });

    // Drag to swipe — Touch events are primary (reliable in the iOS Telegram
    // webview), Pointer events are the desktop/non-touch fallback. `usingTouch`
    // stops the two pipelines double-firing on touchscreens.
    let startX = 0, dx = 0, dragging = false, moved = false, usingTouch = false;
    const knownStamp = el.querySelector('.swipe-stamp.known');
    const forgotStamp = el.querySelector('.swipe-stamp.forgot');
    function dragStart(x) { dragging = true; moved = false; startX = x; el.classList.add('dragging'); }
    function dragMove(x) {
      if (!dragging) return;
      dx = x - startX;
      if (Math.abs(dx) > 6) moved = true;
      el.style.transform = 'translateX(' + dx + 'px) rotate(' + (dx / 20) + 'deg)';
      knownStamp.style.opacity = dx > 0 ? Math.min(1, dx / 80) : 0;
      forgotStamp.style.opacity = dx < 0 ? Math.min(1, -dx / 80) : 0;
    }
    function dragEnd() {
      if (!dragging) return;
      dragging = false;
      el.classList.remove('dragging');
      if (Math.abs(dx) > 90) { commit(dx > 0, el); }
      else { el.style.transform = ''; knownStamp.style.opacity = 0; forgotStamp.style.opacity = 0; }
      dx = 0;
    }

    el.addEventListener('touchstart', e => { usingTouch = true; dragStart(e.touches[0].clientX); }, { passive: true });
    el.addEventListener('touchmove', e => { if (dragging) e.preventDefault(); dragMove(e.touches[0].clientX); }, { passive: false });
    el.addEventListener('touchend', () => { dragEnd(); setTimeout(() => { usingTouch = false; }, 0); });
    el.addEventListener('touchcancel', () => { dragEnd(); usingTouch = false; });

    el.addEventListener('pointerdown', e => {
      if (usingTouch || e.pointerType === 'touch') return;
      dragStart(e.clientX);
      try { el.setPointerCapture(e.pointerId); } catch (_) { /* ignore */ }
    });
    el.addEventListener('pointermove', e => { if (!usingTouch && e.pointerType !== 'touch') dragMove(e.clientX); });
    el.addEventListener('pointerup', e => { if (!usingTouch && e.pointerType !== 'touch') dragEnd(); });
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
        sub: it.pronunciation || '',
        back: cardBack(it),
        term: it.term,
      }));
    },
    onAnswer: (card, known) => api('/api/review/answer', { method: 'POST', body: JSON.stringify({ term: card.term, known }) }),
    onFinish: maybeSuggestLevel,
    emptyText: 'No words are due for review right now. Great job — come back later!',
    doneText: 'Review complete! Come back tomorrow to keep your streak going.',
  });
}

// maybeSuggestLevel asks the server whether the user's *sustained* review
// performance (a rolling window across many sessions, not this one batch)
// warrants a difficulty change. The server throttles this — it only returns a
// suggestion occasionally, once it's confident — so we just render whatever it
// says, with a one-tap switch (reusing the /api/settings level POST). Silent on
// errors or no suggestion.
async function maybeSuggestLevel(correct, total, container) {
  let s;
  try { s = await api('/api/review/summary', { method: 'POST', body: '{}' }); }
  catch (e) { return; }
  if (!s || !s.suggest) return;
  const card = document.createElement('div');
  card.className = 'card level-suggest';
  card.innerHTML =
    '<div class="ls-emoji">' + (s.direction === 'harder' ? '🚀' : '🌱') + '</div>' +
    '<div class="ls-msg">' + esc(s.message) + '</div>' +
    '<div class="sub">Your recent reviews: ' + s.accuracy + '% correct.</div>' +
    '<div class="chips" style="margin-top:12px">' +
    '<button class="chip chip-on" id="ls-switch">Switch to ' + esc(s.targetLabel) + '</button>' +
    '<button class="chip" id="ls-keep">Keep ' + esc(s.currentLabel) + '</button>' +
    '</div>';
  container.appendChild(card);
  card.querySelector('#ls-switch').addEventListener('click', async () => {
    haptic('light');
    try {
      await api('/api/settings', { method: 'POST', body: JSON.stringify({ key: 'level', value: s.targetLevel }) });
      hapticNotify('success');
      card.innerHTML = '<div class="ls-emoji">✅</div><div class="ls-msg">You\'re now learning at <b>' +
        esc(s.targetLabel) + '</b>. New words will match.</div>';
    } catch (e) { hapticNotify('error'); }
  });
  card.querySelector('#ls-keep').addEventListener('click', () => { hapticSelect(); card.remove(); });
}

// ---------------------------------------------------------------------------
// Decks (Leitner) — the Study tab hosts several subviews (deck list, deck
// detail, study session, practice, grammar). showDeckSub switches between them
// and wires a single BackButton so handlers never stack.
// ---------------------------------------------------------------------------
const decksListEl = document.getElementById('decks-list');
const decksEmptyEl = document.getElementById('decks-empty');
const DECK_SUBVIEWS = ['decks-home', 'decks-detail', 'decks-study', 'decks-practice', 'decks-grammar'];
let deckBack = null;

function showDeckSub(id, onBack) {
  DECK_SUBVIEWS.forEach(s => { const el = document.getElementById(s); if (el) el.hidden = (s !== id); });
  if (deckBack) { try { tg.BackButton.offClick(deckBack); } catch (e) { /* ignore */ } deckBack = null; }
  if (id === 'decks-home') {
    setSwipeGuard(false); setCloseGuard(false);
    try { tg.BackButton.hide(); } catch (e) { /* ignore */ }
  } else {
    deckBack = onBack || backToDeckHome;
    try { tg.BackButton.onClick(deckBack); tg.BackButton.show(); } catch (e) { /* ignore */ }
  }
}
function backToDeckHome() { setSwipeGuard(false); setCloseGuard(false); showDeckSub('decks-home'); loadDecks(); }

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
  el.addEventListener('click', () => openDeckDetail(d));
  return el;
}

async function loadDecks() {
  showDeckSub('decks-home'); // reset to the list when (re)entering the tab
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

// renderDeckDetail builds the deck detail page: progress, Leitner box
// distribution, key stats and a Study call-to-action.
function renderDeckDetail(det) {
  const maxc = Math.max(1, det.new, ...det.boxes.map(b => b.count));
  const row = (label, count, lvl) =>
    '<div class="box-row"><span class="box-label">' + esc(label) + '</span>' +
    '<div class="box-bar"><div class="box-fill l' + lvl + '" style="width:' + (count * 100 / maxc) + '%"></div></div>' +
    '<span class="box-n">' + count + '</span></div>';
  const boxes = det.boxes.map(b => row(b.label, b.count, b.box)).join('') + row('New', det.new, 0);
  return '<h1>📚 ' + esc(det.name) + '</h1>' +
    '<div class="card"><div class="sub">' + esc(det.description) + '</div>' +
    bar(det.progressPct, 'var(--accent)') +
    '<div class="sub" style="margin-top:8px">' + det.progressPct + '% mastered · ' +
    det.mastered + '/' + det.total + ' cards</div></div>' +
    '<div class="card"><h2>Your boxes</h2>' + boxes + '</div>' +
    '<div class="card"><div class="stat-tiles">' +
    '<div class="stat-tile"><div class="stat-n">' + det.due + '</div><div class="sub">Due now</div></div>' +
    '<div class="stat-tile"><div class="stat-n">' + det.mastered + '</div><div class="sub">Mastered</div></div>' +
    '<div class="stat-tile"><div class="stat-n">' + det.new + '</div><div class="sub">New</div></div>' +
    '<div class="stat-tile"><div class="stat-n" style="font-size:15px">' + (det.nextReview || '—') + '</div><div class="sub">Next</div></div>' +
    '</div></div>' +
    '<button class="quiz-next" id="deck-study-btn">' +
    (det.due > 0 ? 'Study ' + det.due + ' due' : 'Study deck') + ' →</button>';
}

async function openDeckDetail(d) {
  haptic('light');
  showDeckSub('decks-detail', backToDeckHome);
  const body = document.getElementById('detail-body');
  body.innerHTML = '<h1>📚 ' + esc(d.name) + '</h1>' + skeletonCards(2);
  let det;
  try { det = await api('/api/decks/detail?deck=' + encodeURIComponent(d.id)); }
  catch (e) { body.innerHTML = '<h1>📚 ' + esc(d.name) + '</h1><p class="empty">Could not load this deck.</p>'; return; }
  body.innerHTML = renderDeckDetail(det);
  document.getElementById('deck-study-btn').addEventListener('click', () => startDeckStudy(d));
}

function startDeckStudy(d) {
  showDeckSub('decks-study', () => openDeckDetail(d)); // Back returns to detail
  document.getElementById('decks-study-title').textContent = '📚 ' + d.name;
  haptic('light');
  setSwipeGuard(true);
  createSwipeSession(document.getElementById('decks-swipe'), {
    load: async () => {
      const data = await api('/api/decks/study?deck=' + encodeURIComponent(d.id) + '&limit=30');
      return (data.items || []).map(it => ({
        front: it.term,
        sub: it.pronunciation || '',
        back: cardBack(it),
        term: it.term,
      }));
    },
    onAnswer: (card, known) => api('/api/decks/swipe', { method: 'POST', body: JSON.stringify({ deck: d.id, term: card.term, known }) }),
    emptyText: 'Nothing due in this deck right now — come back later!',
    doneText: 'Session complete! 🎉',
  });
}

// ---------------------------------------------------------------------------
// Practice — on-demand quiz / word / idiom / collocation (Decks tab)
// ---------------------------------------------------------------------------
const practiceBody = document.getElementById('practice-body');
const PRACTICE_TITLES = {
  quiz: '🧩 Quiz', word: '📘 New word', idiom: '💬 Idiom', collocation: '🔗 Collocation',
};

function openPractice(kind) {
  showDeckSub('decks-practice', backToDeckHome);
  document.getElementById('practice-title').textContent = PRACTICE_TITLES[kind] || 'Practice';
  haptic('light');
  if (kind === 'quiz') loadQuizQuestion({ asked: 0, right: 0 });
  else loadPracticeCard(kind);
}

async function loadPracticeCard(kind) {
  practiceBody.innerHTML = skeletonCards(1);
  let data;
  try { data = await api('/api/practice?kind=' + kind); }
  catch (e) {
    practiceBody.innerHTML = '<p class="empty">' +
      (String(e).includes('429') ? 'Easy there! You\'ve hit the hourly practice limit — come back in a bit.'
                                 : 'Could not load. Try again later.') + '</p>';
    return;
  }
  if (!data.available) {
    practiceBody.innerHTML = '<p class="empty">Nothing in the pool for your level yet — try again later.</p>';
    return;
  }
  practiceBody.innerHTML =
    '<div class="card"><div class="word-term" style="font-size:22px">' + esc(data.term) + '</div>' +
    '<div class="practice-text" style="margin-top:10px">' + tappableText(data.text) + '</div></div>' +
    '<div class="chips"><button class="chip chip-on" id="practice-more">🔄 Another one</button></div>';
  document.getElementById('practice-more').addEventListener('click', () => {
    hapticSelect();
    loadPracticeCard(kind);
  });
}

async function loadQuizQuestion(score) {
  practiceBody.innerHTML = skeletonCards(1);
  let q;
  try { q = await api('/api/quiz/next'); }
  catch (e) {
    practiceBody.innerHTML = '<p class="empty">' +
      (String(e).includes('429') ? 'Easy there! You\'ve hit the hourly limit — come back in a bit.'
                                 : 'Could not load a question. Try again later.') + '</p>';
    return;
  }
  if (!q.available) {
    practiceBody.innerHTML = '<p class="empty">Not enough learned words for a quiz yet. Send /word in the chat to get started!</p>';
    return;
  }

  const letters = ['A', 'B', 'C', 'D', 'E', 'F'];
  let html = '<div class="quiz-head">' +
    '<span class="quiz-progress">Question ' + (score.asked + 1) + '</span>' +
    (score.asked > 0 ? '<span class="quiz-score-pill">✓ ' + score.right + '/' + score.asked + '</span>' : '') +
    '</div>' +
    '<div class="card quiz-q"><div class="practice-text">' + esc(q.prompt) + '</div></div>' +
    '<div class="quiz-opts">';
  q.options.forEach((opt, i) => {
    html += '<button class="quiz-opt" data-i="' + i + '">' +
      '<span class="quiz-badge">' + letters[i] + '</span>' +
      '<span class="quiz-opt-text">' + esc(opt) + '</span></button>';
  });
  html += '</div>';
  practiceBody.innerHTML = html;

  const buttons = practiceBody.querySelectorAll('.quiz-opt');
  buttons.forEach(btn => btn.addEventListener('click', async () => {
    buttons.forEach(b => { b.disabled = true; });
    const answer = parseInt(btn.dataset.i, 10);
    let res;
    try {
      res = await api('/api/quiz/answer', {
        method: 'POST',
        body: JSON.stringify({ word: q.word, correct: q.correct, exp: q.exp, token: q.token, answer }),
      });
    } catch (e) {
      buttons.forEach(b => { b.disabled = false; });
      hapticNotify('error');
      return;
    }
    hapticNotify(res.correct ? 'success' : 'error');
    btn.classList.add(res.correct ? 'correct' : 'wrong');
    btn.querySelector('.quiz-badge').textContent = res.correct ? '✓' : '✗';
    if (!res.correct) {
      buttons[q.correct].classList.add('correct');
      buttons[q.correct].querySelector('.quiz-badge').textContent = '✓';
    }
    score.asked++;
    if (res.correct) score.right++;

    const next = document.createElement('button');
    next.className = 'quiz-next';
    next.textContent = res.correct ? '🎉 Next question' : 'Next question →';
    next.addEventListener('click', () => { hapticSelect(); loadQuizQuestion(score); });
    practiceBody.appendChild(next);
  }));
}

document.querySelectorAll('[data-practice]').forEach(c => {
  c.addEventListener('click', () => { hapticSelect(); openPractice(c.dataset.practice); });
});

// ---------------------------------------------------------------------------
// Grammar lessons (static; lives on the Study tab as a drill-down)
// ---------------------------------------------------------------------------
const grammarBody = document.getElementById('grammar-body');
const GRAMMAR_LEVELS = [
  ['beginner', 'Beginner'], ['elementary', 'Elementary'],
  ['intermediate', 'Intermediate'], ['upper-intermediate', 'Upper-Intermediate'],
  ['advanced', 'Advanced'],
];

function openGrammar() {
  showDeckSub('decks-grammar', backToDeckHome);
  haptic('light');
  loadGrammarList();
}

async function loadGrammarList() {
  grammarBody.innerHTML = skeletonRows(5);
  let data;
  try { data = await api('/api/grammar'); }
  catch (e) { grammarBody.innerHTML = '<p class="empty">Could not load lessons. Try again later.</p>'; return; }
  const lessons = data.lessons || [];
  let html = '';
  GRAMMAR_LEVELS.forEach(([key, label]) => {
    const inLevel = lessons.filter(l => l.level === key);
    if (!inLevel.length) return;
    html += '<h2 class="grammar-level">' + esc(label) + '</h2><div class="list">';
    inLevel.forEach(l => {
      html += '<div class="word expandable" data-lesson="' + esc(l.id) + '">' +
        '<span class="rank">' + l.order + '</span>' +
        '<div class="word-main"><div class="word-term">' + esc(l.title) + '</div></div>' +
        '<span class="word-mastery">›</span></div>';
    });
    html += '</div>';
  });
  grammarBody.innerHTML = '<h1>📖 Grammar</h1>' + (html || '<p class="empty">No lessons yet.</p>');
  grammarBody.querySelectorAll('[data-lesson]').forEach(row => {
    row.addEventListener('click', () => openGrammarLesson(row.dataset.lesson));
  });
}

async function openGrammarLesson(id) {
  showDeckSub('decks-grammar', loadGrammarList); // Back returns to the lesson list
  haptic('light');
  grammarBody.innerHTML = skeletonCards(2);
  let l;
  try { l = await api('/api/grammar/lesson?id=' + encodeURIComponent(id)); }
  catch (e) { grammarBody.innerHTML = '<p class="empty">Could not load this lesson.</p>'; return; }

  let html = '<h1>📖 ' + esc(l.title) + '</h1>';
  if (l.image) html += '<div class="card"><img class="grammar-img" src="' + esc(l.image) + '" alt="' + esc(l.title) + ' diagram" loading="lazy" decoding="async"></div>';
  html += '<div class="card"><h2>Pattern</h2><div class="grammar-pattern">' + tappableText(l.pattern) + '</div></div>';
  html += '<div class="card"><h2>How it works</h2><div class="grammar-text">' + tappableText(l.explanation) + '</div></div>';
  if ((l.examples || []).length) {
    html += '<div class="card"><h2>Examples</h2>' +
      l.examples.map(e => '<div class="grammar-ex">\u2022 ' + tappableText(e) + '</div>').join('') + '</div>';
  }
  if (l.tip) html += '<div class="card"><div class="grammar-tip">💡 ' + tappableText(l.tip) + '</div></div>';
  if ((l.practice || []).length) html += '<div class="card"><h2>Practice</h2><div id="grammar-practice"></div></div>';
  grammarBody.innerHTML = html;

  if ((l.practice || []).length) renderGrammarPractice(l.practice);
}

// renderGrammarPractice scores the pre-authored multiple-choice items entirely
// client-side (answers ship in the lesson data — no AI, instant feedback).
function renderGrammarPractice(items) {
  const root = document.getElementById('grammar-practice');
  root.innerHTML = items.map((p, qi) =>
    '<div class="gp-item" data-answer="' + p.answer + '"><div class="gp-q">' + (qi + 1) + '. ' + esc(p.q) + '</div>' +
    '<div class="quiz-opts">' + p.options.map((o, oi) =>
      '<button class="quiz-opt" data-i="' + oi + '"><span class="quiz-badge">' +
      'ABCD'[oi] + '</span><span class="quiz-opt-text">' + esc(o) + '</span></button>').join('') +
    '</div></div>').join('');
  root.querySelectorAll('.gp-item').forEach(item => {
    const answer = parseInt(item.dataset.answer, 10);
    const opts = item.querySelectorAll('.quiz-opt');
    opts.forEach(btn => btn.addEventListener('click', () => {
      const chosen = parseInt(btn.dataset.i, 10);
      const ok = chosen === answer;
      opts.forEach(b => { b.disabled = true; });
      btn.classList.add(ok ? 'correct' : 'wrong');
      btn.querySelector('.quiz-badge').textContent = ok ? '✓' : '✗';
      if (!ok) {
        opts[answer].classList.add('correct');
        opts[answer].querySelector('.quiz-badge').textContent = '✓';
      }
      hapticNotify(ok ? 'success' : 'error');
    }));
  });
}

const grammarOpen = document.getElementById('grammar-open');
if (grammarOpen) grammarOpen.addEventListener('click', () => { hapticSelect(); openGrammar(); });

// ---------------------------------------------------------------------------
// Settings (opened via Telegram's native Settings button)
// ---------------------------------------------------------------------------
const TOGGLE_LABELS = {
  tts: 'Pronunciation audio', tips: 'Daily grammar tips', quiz: 'Quizzes',
  idiom: 'Idiom of the day', collocation: 'Collocation of the day', story: 'Mini stories',
  review: 'Spaced-repetition reviews', daily_review: 'Daily word recap', digest: 'Weekly digest',
};
let settingsReturnView = 'profile';

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

  html += '<div class="card"><h2>Self-paced mode</h2>' +
    row('Silence all automatic messages', toggleHTML('paused', s.paused, 'Silence all automatic messages')) +
    '<div class="sub">Stops daily words, reviews, quizzes &amp; tips. You can still learn anytime in the app — grab a new word in Study and review here whenever you like.</div>' +
    '<div class="set-row" style="margin-top:12px"><span>Delivery every (minutes)</span>' +
    '<input class="num" id="set-interval" type="number" min="15" max="1440" value="' + s.interval + '"></div>' +
    '<div class="sub">How often automatic messages arrive when self-paced mode is off.</div>' +
    '</div>';

  html += '<div class="card"><h2>Content</h2>';
  Object.keys(TOGGLE_LABELS).forEach(k => { html += row(TOGGLE_LABELS[k], toggleHTML(k, !!s.toggles[k], TOGGLE_LABELS[k])); });
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

function toggleHTML(key, on, label) {
  const lbl = label ? ' aria-label="' + esc(label) + '"' : '';
  return '<label class="switch"><input type="checkbox" data-key="' + key + '"' + lbl + (on ? ' checked' : '') + '><span class="slider"></span></label>';
}

async function openSettings() {
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
// Dictionary tab
// ---------------------------------------------------------------------------
const dictState = { timer: null };

function renderDictEntry(e) {
  let html = '<div class="dict-card card">';
  html += '<div class="dict-head"><span class="dict-word">' + esc(e.word) + '</span>';
  if (e.pos) html += ' <span class="dict-pos">' + esc(e.pos) + '</span>';
  if (e.pronunciation) html += ' <span class="dict-ipa">' + esc(e.pronunciation) + '</span>';
  html += '</div>';
  html += '<div class="dict-persian">' + esc(e.persian) + '</div>';
  if (e.romanization) html += '<div class="dict-roman">' + esc(e.romanization) + '</div>';
  if (e.definition) html += '<div class="dict-def">' + tappableText(e.definition) + '</div>';
  if (e.example) html += '<div class="dict-example">' + tappableText(e.example) + '</div>';
  if (e.sense) html += '<div class="dict-sense">Sense: ' + esc(e.sense) + '</div>';
  if (e.tags) html += '<div class="dict-tags">' + esc(e.tags) + '</div>';
  html += '</div>';
  return html;
}

async function loadDictionary() {
  const searchEl = document.getElementById('dict-search');
  const listEl = document.getElementById('dict-list');
  const emptyEl = document.getElementById('dict-empty');
  const statusEl = document.getElementById('dict-status');

  // Show initial state.
  if (!searchEl.dataset.wired) {
    searchEl.dataset.wired = '1';
    searchEl.addEventListener('input', () => {
      clearTimeout(dictState.timer);
      dictState.timer = setTimeout(() => dictSearch(searchEl.value), 300);
    });
    searchEl.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        clearTimeout(dictState.timer);
        dictSearch(searchEl.value, true);
      }
    });
  }

  // Fetch status (seeding, total count).
  try {
    const data = await api('/api/dictionary?q=');
    if (data.seeding) {
      statusEl.hidden = false;
      statusEl.textContent = 'Dictionary is being prepared… (' + (data.total || 0).toLocaleString() + ' entries so far)';
    } else if (data.total > 0) {
      statusEl.hidden = false;
      statusEl.textContent = data.total.toLocaleString() + ' words available';
    } else {
      statusEl.hidden = true;
    }
  } catch (e) { /* ignore */ }

  // Show empty state if no search in progress.
  if (!searchEl.value) {
    listEl.innerHTML = '';
    emptyEl.hidden = false;
    emptyEl.textContent = 'Type an English word to see its Persian meaning.';
  }
}

async function dictSearch(q, exact) {
  const listEl = document.getElementById('dict-list');
  const emptyEl = document.getElementById('dict-empty');
  q = (q || '').trim();
  if (!q) {
    listEl.innerHTML = '';
    emptyEl.hidden = false;
    emptyEl.textContent = 'Type an English word to see its Persian meaning.';
    return;
  }

  emptyEl.hidden = true;
  listEl.innerHTML = skeletonRows(3);

  try {
    const path = '/api/dictionary?q=' + encodeURIComponent(q) + (exact ? '' : '&prefix=1');
    const data = await api(path);
    listEl.innerHTML = '';
    if (!data.results || data.results.length === 0) {
      emptyEl.hidden = false;
      emptyEl.textContent = 'No results for "' + q + '".';
      if (data.seeding) emptyEl.textContent += ' Dictionary is still loading — try again later.';
      return;
    }
    data.results.forEach(e => { listEl.innerHTML += renderDictEntry(e); });
  } catch (e) {
    listEl.innerHTML = '';
    emptyEl.hidden = false;
    emptyEl.textContent = 'Could not look up this word. Try again later.';
  }
}

// ---------------------------------------------------------------------------
// Heatmap touch tooltip (replaces native title — works on mobile)
// ---------------------------------------------------------------------------
const heatTip = document.createElement('div');
heatTip.className = 'heat-tip';
heatTip.hidden = true;
document.body.appendChild(heatTip);

function showHeatTip(el) {
  const tip = el.getAttribute('data-tip');
  if (!tip) return;
  heatTip.textContent = tip;
  heatTip.hidden = false;
  const r = el.getBoundingClientRect();
  // Centre above the element; CSS transform: translate(-50%, -100%) does the rest.
  heatTip.style.left = (r.left + r.width / 2) + 'px';
  heatTip.style.top = (r.top - 4) + 'px';
}

function hideHeatTip() { heatTip.hidden = true; }

document.addEventListener('click', function (e) {
  const cell = e.target.closest('[data-tip]');
  if (cell && (cell.classList.contains('heat-cell') || cell.classList.contains('wbar') ||
      cell.classList.contains('sparkline-cell') || cell.classList.contains('hour-heat-cell'))) {
    haptic('light'); showHeatTip(cell); return;
  }
  hideHeatTip();
});

// ---------------------------------------------------------------------------
// Word popup — tap any tappable word (.tw) for an inline dictionary lookup
// ---------------------------------------------------------------------------
let wpOverlay = null;

function ensureWordPopup() {
  if (wpOverlay) return;
  wpOverlay = document.createElement('div');
  wpOverlay.className = 'wp-overlay';
  // Toggle via inline display (not the `hidden` attribute) so visibility never
  // depends on a possibly-stale external CSS rule. All clicks — open, close,
  // and "Open in Dictionary" — are routed through the single capture-phase
  // document listener below, the one delivery path proven reliable in the iOS
  // Telegram webview (per-element bubble handlers were dropped on touch).
  wpOverlay.style.display = 'none';
  wpOverlay.innerHTML =
    '<div class="wp-sheet">' +
      '<div class="wp-head"><span class="wp-title"></span>' +
      '<button class="wp-x">\u00d7</button></div>' +
      '<div class="wp-body"></div>' +
      '<button class="wp-open chip">📖 Open in Dictionary</button>' +
    '</div>';
  document.body.appendChild(wpOverlay);
}

function closeWordPopup() { if (wpOverlay) wpOverlay.style.display = 'none'; }

function wordPopupOpen() { return wpOverlay && wpOverlay.style.display !== 'none'; }

async function openWordPopup(raw) {
  const word = raw.trim().replace(/^[^A-Za-z]+/, '').replace(/[^A-Za-z]+$/, '').toLowerCase();
  if (!word || word.length < 2) return;
  ensureWordPopup();
  hideHeatTip();
  const title = wpOverlay.querySelector('.wp-title');
  const body = wpOverlay.querySelector('.wp-body');
  title.textContent = word;
  body.innerHTML = '<div class="sub">Looking up\u2026</div>';
  wpOverlay.style.display = 'flex';
  haptic('light');

  try {
    const data = await api('/api/dictionary?q=' + encodeURIComponent(word));
    if (!data.results || data.results.length === 0) {
      body.innerHTML = '<div class="sub">No translation found for \u201c' + esc(word) + '\u201d.</div>';
      return;
    }
    let html = '';
    data.results.slice(0, 3).forEach(function (e) {
      html += '<div class="wp-entry">';
      if (e.pos) html += '<span class="dict-pos">' + esc(e.pos) + '</span> ';
      if (e.pronunciation) html += '<span class="dict-ipa">' + esc(e.pronunciation) + '</span>';
      if (e.persian) html += '<div class="dict-persian" style="font-size:18px;margin:4px 0 2px">' + esc(e.persian) + '</div>';
      if (e.definition) html += '<div class="dict-def">' + esc(e.definition) + '</div>';
      if (e.example) html += '<div class="dict-example">' + esc(e.example) + '</div>';
      html += '</div>';
    });
    if (data.results.length > 3) {
      html += '<div class="sub">' + (data.results.length - 3) + ' more sense' +
        (data.results.length - 3 === 1 ? '' : 's') + ' \u2014 open in Dictionary.</div>';
    }
    body.innerHTML = html;
  } catch (e) {
    body.innerHTML = '<div class="sub">Could not look up this word.</div>';
  }
}

// Single capture-phase handler for the whole word-popup lifecycle. Capture
// fires before ancestor bubble-phase handlers (swipe card flip, expandable-row
// toggle) and is the one click path that's reliable in the iOS Telegram webview
// — so close (X / backdrop) and "Open in Dictionary" go through it too, not
// per-element bubble listeners (which the popup couldn't be dismissed with on
// Android/iOS).
document.addEventListener('click', function (e) {
  // Popup is open: handle its own controls first.
  if (wordPopupOpen()) {
    if (e.target.closest('.wp-x') || e.target === wpOverlay) {
      e.stopPropagation();
      closeWordPopup();
      return;
    }
    if (e.target.closest('.wp-open')) {
      e.stopPropagation();
      const word = wpOverlay.querySelector('.wp-title').textContent;
      closeWordPopup();
      showView('vocab');
      document.querySelectorAll('[data-lib-tab]').forEach(x => x.classList.toggle('chip-on', x.dataset.libTab === 'dict'));
      showLibTab('dict');
      const input = document.getElementById('dict-search');
      input.value = word;
      dictSearch(word, true);
      return;
    }
  }
  // Otherwise: tap a tappable word to open the popup.
  const tw = e.target.closest('.tw');
  if (tw && !tw.closest('.wp-sheet')) {
    e.stopPropagation();
    openWordPopup(tw.textContent);
  }
}, true);

// Touch fallback for dismissing the popup. In the iOS/Android Telegram webview,
// taps on the popup's own controls (× / backdrop / Open-in-Dictionary) don't
// reliably synthesize a click that reaches the handler above — so the popup
// couldn't be closed, even though opening (a tap on a normal-flow .tw word)
// works. touchend is the delivery path this codebase already trusts on mobile
// (the swipe cards use it). It's wired only while the popup is open, so it can
// never cause an accidental open while scrolling the page. preventDefault stops
// the late synthesized click from firing this logic a second time.
document.addEventListener('touchend', function (e) {
  if (!wordPopupOpen()) return;
  if (e.target.closest('.wp-x') || e.target === wpOverlay) {
    e.preventDefault();
    e.stopPropagation();
    closeWordPopup();
  } else if (e.target.closest('.wp-open')) {
    e.preventDefault();
    e.stopPropagation();
    const word = wpOverlay.querySelector('.wp-title').textContent;
    closeWordPopup();
    showView('vocab');
    document.querySelectorAll('[data-lib-tab]').forEach(x => x.classList.toggle('chip-on', x.dataset.libTab === 'dict'));
    showLibTab('dict');
    const input = document.getElementById('dict-search');
    input.value = word;
    dictSearch(word, true);
  }
}, true);

// ---------------------------------------------------------------------------
// Boot
// ---------------------------------------------------------------------------
const loaders = {
  profile: loadDashboard,
  vocab: () => loadVocab(true),
  decks: loadDecks,
  board: loadBoard,
  review: loadReview,
};

// Restore cross-device UI state, then land on the user's last tab.
(async () => {
  try { Object.assign(config, await (await fetch('/api/config')).json()); } catch (e) { /* keep defaults */ }
  const metric = await cloudGet('ui.board.metric');
  if (['words', 'mastered', 'weekly', 'today'].includes(metric)) {
    boardState.metric = metric;
    document.querySelectorAll('[data-metric]').forEach(x =>
      x.classList.toggle('chip-on', x.dataset.metric === metric));
  }
  let tab = await cloudGet('lastTab');
  if (!tab || !loaders[tab]) tab = 'profile';
  showView(tab);
})();
