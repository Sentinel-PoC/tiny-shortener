package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const base62 = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

var (
	db        *sql.DB
	baseURL   string
	codeRe    = regexp.MustCompile(`^[0-9a-zA-Z_-]{1,32}$`)
	indexTmpl = template.Must(template.New("index").Parse(indexHTML))
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	dbPath := getenv("DB_PATH", "/data/urls.db")
	baseURL = strings.TrimRight(getenv("BASE_URL", "https://tiny.haist.farm"), "/")
	addr := ":" + getenv("PORT", "8080")

	var err error
	db, err = sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(DELETE)")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1) // single writer — safe over NFS
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS links (
		code TEXT PRIMARY KEY,
		url TEXT NOT NULL,
		created_at INTEGER NOT NULL)`); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/api/shorten", handleShorten)
	mux.HandleFunc("/", handleRoot)

	log.Printf("tiny-shortener listening on %s (base=%s db=%s)", addr, baseURL, dbPath)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := db.Ping(); err != nil {
		http.Error(w, "db down", http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("ok"))
}

// validTarget enforces the security guardrail: only http/https targets.
// This blocks javascript:, data:, file: and other dangerous schemes that
// would turn the redirector into an XSS / open-redirect vector.
func validTarget(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	if u.Host == "" {
		return "", false
	}
	return u.String(), true
}

func genCode(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i := range b {
		out[i] = base62[int(b[i])%len(base62)]
	}
	return string(out), nil
}

func handleShorten(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	target, ok := validTarget(r.FormValue("url"))
	if !ok {
		respond(w, r, http.StatusBadRequest, "", "", "only http:// and https:// URLs are allowed")
		return
	}
	custom := strings.TrimSpace(r.FormValue("code"))
	var code string
	if custom != "" {
		if !codeRe.MatchString(custom) {
			respond(w, r, http.StatusBadRequest, "", "", "custom code must be 1-32 chars of [A-Za-z0-9_-]")
			return
		}
		if _, err := db.Exec(`INSERT INTO links(code,url,created_at) VALUES(?,?,?)`, custom, target, time.Now().Unix()); err != nil {
			respond(w, r, http.StatusConflict, "", "", "that code is already taken")
			return
		}
		code = custom
	} else {
		// random code with collision retry
		for attempt := 0; attempt < 6; attempt++ {
			c, err := genCode(6)
			if err != nil {
				respond(w, r, http.StatusInternalServerError, "", "", "internal error")
				return
			}
			if _, err := db.Exec(`INSERT INTO links(code,url,created_at) VALUES(?,?,?)`, c, target, time.Now().Unix()); err == nil {
				code = c
				break
			}
		}
		if code == "" {
			respond(w, r, http.StatusInternalServerError, "", "", "could not allocate code")
			return
		}
	}
	respond(w, r, http.StatusOK, code, target, "")
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		indexTmpl.Execute(w, nil)
		return
	}
	code := strings.TrimPrefix(r.URL.Path, "/")
	if !codeRe.MatchString(code) {
		http.NotFound(w, r)
		return
	}
	var target string
	if err := db.QueryRow(`SELECT url FROM links WHERE code=?`, code).Scan(&target); err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// respond writes JSON when the client asks for it, otherwise an HTML page.
func respond(w http.ResponseWriter, r *http.Request, status int, code, target, errMsg string) {
	wantJSON := strings.Contains(r.Header.Get("Accept"), "application/json") || r.FormValue("format") == "json"
	if wantJSON {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		out := map[string]any{}
		if errMsg != "" {
			out["error"] = errMsg
		} else {
			out["code"] = code
			out["url"] = target
			out["short"] = baseURL + "/" + code
		}
		json.NewEncoder(w).Encode(out)
		return
	}
	w.WriteHeader(status)
	indexTmpl.Execute(w, map[string]string{"Code": code, "Target": target, "Short": baseURL + "/" + code, "Error": errMsg})
}

const indexHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>tiny.haist.farm — URL shortener</title>
<style>
body{font-family:system-ui,sans-serif;max-width:640px;margin:4rem auto;padding:0 1rem;background:#0b0e14;color:#d6deeb}
h1{font-weight:600} input,button{font-size:1rem;padding:.6rem;border-radius:6px;border:1px solid #2a3346}
input{width:100%;box-sizing:border-box;margin:.4rem 0;background:#0f1320;color:#d6deeb}
button{background:#3b82f6;color:#fff;border:0;cursor:pointer;width:100%}
.short{margin-top:1rem;padding:.8rem;background:#0f1320;border:1px solid #2a3346;border-radius:6px;word-break:break-all}
.err{color:#f97316;margin-top:1rem} a{color:#7dd3fc} .muted{color:#5b6478;font-size:.85rem}
</style></head><body>
<h1>tiny.haist.farm</h1>
<p class="muted">Paste a link to shorten it. http/https only.</p>
<form method="post" action="/api/shorten">
<input name="url" type="url" placeholder="https://example.com/very/long/link" required>
<input name="code" type="text" placeholder="optional custom code (a-z, 0-9, -, _)">
<button type="submit">Shorten</button>
</form>
{{if .Short}}<div class="short">Short link: <a href="{{.Short}}">{{.Short}}</a><br><span class="muted">&rarr; {{.Target}}</span></div>{{end}}
{{if .Error}}<div class="err">{{.Error}}</div>{{end}}
</body></html>`
