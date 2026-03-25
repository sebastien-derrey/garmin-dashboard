package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"garmin_dashboard/garmin"
	"garmin_dashboard/storage"
)

//go:embed web
var webFS embed.FS

// Config is loaded from config.json
type Config struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Port     int    `json:"port"`
	DBPath   string `json:"db_path"`
}

func loadConfig() Config {
	cfg := Config{Port: 8080, DBPath: "garmin.db"}
	f, err := os.Open("config.json")
	if err != nil {
		return cfg
	}
	defer f.Close()
	json.NewDecoder(f).Decode(&cfg)
	return cfg
}

// SyncState tracks background sync progress
type SyncState struct {
	mu       sync.Mutex
	Running  bool      `json:"running"`
	Progress int       `json:"progress"`
	Total    int       `json:"total"`
	Message  string    `json:"message"`
	Error    string    `json:"error,omitempty"`
	LastSync time.Time `json:"lastSync"`
}

func (s *SyncState) set(running bool, progress, total int, msg string, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Running = running
	s.Progress = progress
	s.Total = total
	s.Message = msg
	s.Error = errMsg
	if !running && errMsg == "" {
		s.LastSync = time.Now()
	}
}

func (s *SyncState) snapshot() map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	pct := 0
	if s.Total > 0 {
		pct = s.Progress * 100 / s.Total
	}
	return map[string]interface{}{
		"running":  s.Running,
		"progress": s.Progress,
		"total":    s.Total,
		"percent":  pct,
		"message":  s.Message,
		"error":    s.Error,
		"lastSync": s.LastSync,
	}
}

var syncState SyncState

func main() {
	cfg := loadConfig()

	db, err := storage.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	client := garmin.NewClient()

	// Try to restore saved OAuth tokens
	if err := client.LoadTokens("garmin_tokens.json"); err != nil {
		log.Printf("No saved session (%v)", err)
		if cfg.Email != "" && cfg.Password != "" {
			log.Println("Logging in to Garmin Connect...")
			if err := client.Login(cfg.Email, cfg.Password); err != nil {
				log.Printf("Warning: Garmin login failed: %v", err)
				log.Println("You can log in via the dashboard")
			} else {
				client.SaveTokens("garmin_tokens.json")
				log.Printf("Login successful!")
			}
		} else {
			log.Println("No credentials in config.json — log in via the dashboard")
		}
	}

	mux := http.NewServeMux()

	// Serve static files from web/ directory
	webContent, _ := fs.Sub(webFS, "web")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(webContent))))

	// ── API routes ──────────────────────────────────────────────────────────

	// GET /api/data?months=3  →  returns cached metrics as JSON
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		months := 3
		if m := r.URL.Query().Get("months"); m != "" {
			if v, err := strconv.Atoi(m); err == nil && v >= 1 && v <= 12 {
				months = v
			}
		}

		now := time.Now()
		endDate := now.Format("2006-01-02")
		startDate := now.AddDate(0, -months, 0).Format("2006-01-02")

		metrics, err := db.GetMetrics(startDate, endDate)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		resp := garmin.DashboardResponse{
			Metrics:  metrics,
			LastSync: db.LastSync(),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// POST /api/sync?months=3  →  starts a background sync
	mux.HandleFunc("/api/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", 405)
			return
		}

		syncState.mu.Lock()
		running := syncState.Running
		syncState.mu.Unlock()

		if running {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(syncState.snapshot())
			return
		}

		if !client.LoggedIn {
			http.Error(w, "Not logged in — please log in first", 401)
			return
		}

		months := 3
		if m := r.URL.Query().Get("months"); m != "" {
			if v, err := strconv.Atoi(m); err == nil && v >= 1 && v <= 12 {
				months = v
			}
		}

		go runSync(client, db, months)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "started"})
	})

	// GET /api/sync/status  →  returns current sync progress
	mux.HandleFunc("/api/sync/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(syncState.snapshot())
	})

	// POST /api/login  →  logs in with provided credentials
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", 405)
			return
		}
		var creds struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if creds.Email == "" || creds.Password == "" {
			http.Error(w, "email and password required", 400)
			return
		}

		if err := client.Login(creds.Email, creds.Password); err != nil {
			http.Error(w, fmt.Sprintf("Login failed: %v", err), 401)
			return
		}
		client.SaveTokens("garmin_tokens.json")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "ok",
			"displayName": client.DisplayName(),
		})
	})

	// GET /api/status  →  returns login status
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"loggedIn":    client.LoggedIn,
			"displayName": client.DisplayName(),
		})
	})

	// POST /api/clear  →  wipes all cached data so the next sync starts fresh
	mux.HandleFunc("/api/clear", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", 405)
			return
		}
		if err := db.ClearAll(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "cleared"})
	})

	// GET /api/stats  →  row counts per table + most recent dates (diagnostic)
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(db.Stats())
	})

	// GET /api/probe?path=/metrics-service/...  →  raw Garmin API response (debug)
	mux.HandleFunc("/api/probe", func(w http.ResponseWriter, r *http.Request) {
		if !client.LoggedIn {
			http.Error(w, "not logged in", 401)
			return
		}
		path := r.URL.Query().Get("path")
		if path == "" {
			// Default: probe today's training status and VO2Max
			today := time.Now().Format("2006-01-02")
			yesterday := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
			paths := []string{
				"/metrics-service/metrics/trainingstatus/aggregated/" + today,
				"/metrics-service/metrics/maxmet/daily/" + yesterday + "/" + today,
				"/hrv-service/hrv/" + today,
			}
			results := map[string]interface{}{}
			for _, p := range paths {
				b, err := client.RawGet(p)
				if err != nil {
					results[p] = map[string]string{"error": err.Error()}
				} else if b == nil {
					results[p] = nil
				} else {
					var v interface{}
					json.Unmarshal(b, &v)
					results[p] = v
				}
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(results)
			return
		}
		b, err := client.RawGet(path)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if b == nil {
			w.Write([]byte("null"))
		} else {
			w.Write(b)
		}
	})

	// All other routes serve index.html (SPA)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		content, err := webFS.ReadFile("web/index.html")
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(content)
	})

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("Garmin Dashboard → http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// runSync fetches all missing data from Garmin for the given month range
func runSync(client *garmin.Client, db *storage.DB, months int) {
	syncState.set(true, 0, 0, "Starting sync...", "")

	now := time.Now()
	endDate := now
	startDate := now.AddDate(0, -months, 0)
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")

	totalDays := int(endDate.Sub(startDate).Hours()/24) + 1

	// ── Phase 1: VO2Max range call (one request for the full range) ───────
	syncState.set(true, 0, totalDays, "Fetching VO2Max...", "")
	log.Printf("Fetching VO2Max %s → %s", startStr, endStr)
	vo2map, err := client.FetchVO2MaxRange(startStr, endStr)
	if err != nil {
		log.Printf("VO2Max fetch error: %v", err)
	} else if len(vo2map) > 0 {
		if err := db.SaveVO2MaxMap(vo2map); err != nil {
			log.Printf("VO2Max save error: %v", err)
		}
		log.Printf("VO2Max: saved %d days", len(vo2map))
	} else {
		log.Printf("VO2Max: no data returned (device may not support VO2Max estimation)")
	}

	// ── Phase 2: Per-day HRV + training status (cached) ──────────────────
	log.Printf("Fetching per-day data for %d days", totalDays)

	hrvFetched, tsFetched := 0, 0
	for i := 0; i < totalDays; i++ {
		date := startDate.AddDate(0, 0, i).Format("2006-01-02")
		syncState.set(true, i+1, totalDays,
			fmt.Sprintf("Day %d/%d: %s", i+1, totalDays, date), "")

		// HRV
		if !db.HasHRV(date) {
			hrv, err := client.FetchHRV(date)
			if err != nil {
				log.Printf("HRV %s: %v", date, err)
			} else if hrv != nil {
				if err := db.SaveHRV(hrv); err != nil {
					log.Printf("HRV save %s: %v", date, err)
				}
				hrvFetched++
			}
			time.Sleep(150 * time.Millisecond)
		}

		// Training status (ATL/CTL/TSB) — also returns a VO2Max point as a bonus
		if !db.HasTrainingStatus(date) {
			ts, vo2pt, err := client.FetchTrainingStatusDay(date)
			if err != nil {
				log.Printf("TrainingStatus %s: %v", date, err)
			} else {
				if ts != nil {
					if err := db.SaveTrainingStatus(ts); err != nil {
						log.Printf("TrainingStatus save %s: %v", date, err)
					}
					tsFetched++
				}
				if vo2pt != nil {
					if err := db.SaveVO2MaxMap(map[string]float64{vo2pt.Date: vo2pt.Value}); err != nil {
						log.Printf("VO2Max (from ts) save %s: %v", vo2pt.Date, err)
					}
				}
			}
			time.Sleep(150 * time.Millisecond)
		}
	}

	log.Printf("Sync done — HRV: %d new, TrainingStatus: %d new", hrvFetched, tsFetched)
	syncState.set(false, totalDays, totalDays, "Sync complete!", "")
}
