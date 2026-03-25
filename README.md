# Garmin Dashboard

A Go web app that fetches your Garmin Connect data and displays health & training trends.

## Setup

1. **Copy the example config:**
   ```
   cp config.json.example config.json
   ```

2. **Edit `config.json`** with your Garmin Connect email and password:
   ```json
   {
     "email":    "you@example.com",
     "password": "yourpassword",
     "port":     8080
   }
   ```

3. **Run:**
   ```
   go run .
   ```
   Or build first:
   ```
   go build -o garmin_dashboard.exe .
   ./garmin_dashboard.exe
   ```

4. **Open** http://localhost:8080

5. **Click Sync** to fetch your data (choose a time range: 1–12 months).

## Notes

- **Session** is saved to `garmin_session.json` — you won't need to re-login each time.
- **Data** is cached in `garmin.db` (SQLite) — only new/missing days are re-fetched.
- **HRV** is fetched per day (~150ms between calls to respect Garmin's rate limits).
- **2FA**: If your Garmin account uses two-factor authentication, you'll need to temporarily disable it or log in and supply a token. The app shows a clear error message if 2FA is detected.
- If Garmin changes their login page, the auth may break — check for updates to the [garth](https://github.com/matin/garth) Python library for reference.

## Charts

| Chart | What it shows |
|-------|--------------|
| **Training Overview** | HRV, ATL (fatigue), CTL (fitness) on one timeline |
| **Performance Management** | CTL vs ATL vs TSB (form) — the classic sports science chart |
| **HRV vs Training Load** | Correlation scatter: does high ATL suppress your HRV? |
| **HRV vs VO₂Max** | Do days with higher HRV correlate with better VO₂Max? |
| **VO₂Max Trend** | Long-term VO₂Max progression vs training load context |

### Reading the PMC (Performance Management Chart)

- **CTL** (blue) = Chronic Training Load = **fitness baseline**
- **ATL** (amber) = Acute Training Load = **fatigue**
- **TSB** (white line) = CTL − ATL = **form**
  - TSB > +5 → fresh and ready
  - TSB −5 to −20 → productive training zone
  - TSB < −20 → fatigued / overreaching
  - TSB > +25 → detraining (too little load)
