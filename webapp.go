package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// startWebServer starts the embedded HTTP server that serves the Telegram Mini
// App stats dashboard. It is only called when WEB_APP_URL is configured.
func startWebServer(store *Store) {
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", serveStatsPage)
	mux.HandleFunc("/api/stats", makeStatsAPIHandler(store))

	log.Printf("🌐 [WEBAPP] Starting web server on :%s", webAppPort)
	go func() {
		if err := http.ListenAndServe(":"+webAppPort, mux); err != nil {
			log.Printf("⚠️  [WEBAPP] Web server error: %v", err)
		}
	}()
}

// validateInitData validates Telegram WebApp initData using HMAC-SHA256 and
// returns the user ID on success.
func validateInitData(initData string) (userID int64, ok bool) {
	params, err := url.ParseQuery(initData)
	if err != nil {
		return 0, false
	}
	hash := params.Get("hash")
	if hash == "" {
		return 0, false
	}

	// Build check string: all fields except "hash", sorted alphabetically.
	var pairs []string
	for k, vs := range params {
		if k == "hash" {
			continue
		}
		pairs = append(pairs, k+"="+vs[0])
	}
	sort.Strings(pairs)
	checkString := strings.Join(pairs, "\n")

	// secret_key = HMAC-SHA256("WebAppData", bot_token)
	h1 := hmac.New(sha256.New, []byte("WebAppData"))
	h1.Write([]byte(TelegramBotToken))
	secretKey := h1.Sum(nil)

	// expected = HMAC-SHA256(secret_key, check_string)
	h2 := hmac.New(sha256.New, secretKey)
	h2.Write([]byte(checkString))
	expected := hex.EncodeToString(h2.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(hash)) {
		return 0, false
	}

	userJSON := params.Get("user")
	if userJSON == "" {
		return 0, false
	}
	var u struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(userJSON), &u); err != nil || u.ID == 0 {
		return 0, false
	}
	return u.ID, true
}

// makeStatsAPIHandler returns the /api/stats HTTP handler bound to the store.
func makeStatsAPIHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		initData := r.URL.Query().Get("initData")
		chatID, ok := validateInitData(initData)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		st, err := store.UserStats(chatID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		quizPct := 0
		if st.QuizAnswered > 0 {
			quizPct = st.QuizCorrect * 100 / st.QuizAnswered
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"current_streak": st.CurrentStreak,
			"longest_streak": st.LongestStreak,
			"words":          st.Words,
			"mastered":       st.Mastered,
			"verbs":          st.Verbs,
			"quiz_answered":  st.QuizAnswered,
			"quiz_correct":   st.QuizCorrect,
			"quiz_pct":       quizPct,
			"active_days":    st.ActiveDays,
			"activity_days":  st.ActivityDays,
			"level":          levelLabel(st.Level),
		})
	}
}

// serveStatsPage renders the Telegram Mini App HTML page.
func serveStatsPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(statsPageHTML))
}

const statsPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Your Progress</title>
<script src="https://telegram.org/js/telegram-web-app.js"></script>
<script src="https://cdn.jsdelivr.net/npm/chart.js@4/dist/chart.umd.min.js"></script>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;
     background:var(--tg-theme-bg-color,#fff);
     color:var(--tg-theme-text-color,#111);padding:16px}
h1{font-size:18px;font-weight:700;margin-bottom:16px}
.card{background:var(--tg-theme-secondary-bg-color,#f5f5f5);
      border-radius:14px;padding:16px;margin-bottom:12px}
.card h2{font-size:13px;color:var(--tg-theme-hint-color,#888);
         font-weight:500;margin-bottom:6px;text-transform:uppercase;letter-spacing:.5px}
.big{font-size:34px;font-weight:700;line-height:1.1}
.sub{font-size:13px;color:var(--tg-theme-hint-color,#888);margin-top:4px}
.bar-wrap{background:#e0e0e0;border-radius:6px;height:8px;margin-top:10px;overflow:hidden}
.bar-fill{height:100%;border-radius:6px;transition:width .4s ease}
.grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}
.chart-box{height:140px;margin-top:8px}
</style>
</head>
<body>
<div id="app"><p style="color:#888;padding:32px 0;text-align:center">Loading...</p></div>
<script>
const tg = window.Telegram.WebApp;
tg.ready();
tg.expand();

const apiBase = window.location.origin;
fetch(apiBase + '/api/stats?initData=' + encodeURIComponent(tg.initData))
  .then(r => { if (!r.ok) throw new Error(r.status); return r.json(); })
  .then(render)
  .catch(() => {
    document.getElementById('app').innerHTML =
      '<p style="padding:32px;text-align:center;color:#888">Could not load your stats.<br>Try again later.</p>';
  });

function bar(pct, colour) {
  return '<div class="bar-wrap"><div class="bar-fill" style="width:' +
         Math.min(100,pct) + '%;background:' + colour + '"></div></div>';
}

function render(s) {
  const streakPct = s.longest_streak > 0
    ? Math.round(s.current_streak * 100 / s.longest_streak) : 100;
  const flame = s.current_streak >= 3 ? ' 🔥' : '';

  let html = '<h1>📊 Your Progress</h1>';

  // Streak card
  html += '<div class="card"><h2>Streak</h2>' +
    '<div class="big">' + s.current_streak + ' day' + (s.current_streak===1?'':'s') + flame + '</div>' +
    '<div class="sub">Best: ' + s.longest_streak + ' days</div>' +
    bar(streakPct, 'var(--tg-theme-button-color,#2196F3)') + '</div>';

  // Words + Drills grid
  html += '<div class="grid">' +
    '<div class="card"><h2>Words</h2><div class="big">' + s.words + '</div>' +
    '<div class="sub">' + s.mastered + ' mastered</div></div>' +
    '<div class="card"><h2>Drills</h2><div class="big">' + s.verbs + '</div></div>' +
    '</div>';

  // Quiz accuracy
  if (s.quiz_answered > 0) {
    html += '<div class="card"><h2>Quiz accuracy</h2>' +
      '<div class="big">' + s.quiz_pct + '%</div>' +
      '<div class="sub">' + s.quiz_correct + ' / ' + s.quiz_answered + ' correct</div>' +
      bar(s.quiz_pct, '#4CAF50') + '</div>';
  }

  // Activity chart (last 30 days)
  html += '<div class="card"><h2>Activity · last 30 days</h2>' +
    '<div class="chart-box"><canvas id="actChart"></canvas></div></div>';

  // Level
  html += '<div class="card"><h2>Level</h2>' +
    '<div class="big" style="font-size:22px">' + s.level + '</div>' +
    '<div class="sub">' + s.active_days + ' active day' + (s.active_days===1?'':'s') + ' total</div></div>';

  document.getElementById('app').innerHTML = html;

  // Build 30-day activity array
  const set = new Set(s.activity_days || []);
  const labels = [], data = [], colors = [];
  for (let i = 29; i >= 0; i--) {
    const d = new Date(); d.setDate(d.getDate() - i);
    const key = d.toISOString().slice(0, 10);
    const mm = (d.getMonth()+1).toString().padStart(2,'0');
    const dd = d.getDate().toString().padStart(2,'0');
    labels.push(mm + '/' + dd);
    const active = set.has(key);
    data.push(active ? 1 : 0.15);
    colors.push(active ? 'var(--tg-theme-button-color,#2196F3)' : '#e0e0e0');
  }
  new Chart(document.getElementById('actChart'), {
    type: 'bar',
    data: { labels, datasets: [{ data, backgroundColor: colors, borderRadius: 3, borderSkipped: false }] },
    options: {
      plugins: { legend: { display: false }, tooltip: { enabled: false } },
      scales: {
        x: { display: false },
        y: { display: false, min: 0, max: 1.4 }
      },
      animation: { duration: 400 }
    }
  });
}
</script>
</body>
</html>`
