package garmin

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

const connectAPIBase = "https://connectapi.garmin.com"

// get performs an authenticated GET to the Garmin Connect API using OAuth2 Bearer token
func (c *Client) get(path string) ([]byte, error) {
	// Auto-refresh if token expired
	if c.oauth2.expired() && c.oauth1.OAuthToken != "" {
		log.Println("Access token expired — refreshing...")
		refreshed, err := c.exchangeOAuth2(c.oauth1, false)
		if err != nil {
			return nil, fmt.Errorf("token refresh: %w", err)
		}
		c.oauth2 = refreshed
	}

	req, err := http.NewRequest("GET", connectAPIBase+path, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.oauth2.AccessToken)
	req.Header.Set("User-Agent", apiUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 204 || resp.StatusCode == 404 {
		return nil, nil // no data for this date/range
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("unauthorized (HTTP %d) — try logging in again", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d for %s: %s", resp.StatusCode, path, truncate(string(body), 200))
	}
	return body, nil
}

// FetchHRV fetches HRV data for a specific date (format: 2006-01-02)
func (c *Client) FetchHRV(date string) (*HRVSummary, error) {
	body, err := c.get("/hrv-service/hrv/" + date)
	if err != nil || body == nil {
		return nil, err
	}
	var resp HRVResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing HRV response: %w", err)
	}
	return &resp.HRVSummary, nil
}

// FetchTrainingStatusDay fetches training status (ATL/CTL/TSB) for one day.
// Endpoint: /metrics-service/metrics/trainingstatus/aggregated/{date}
//
// Real API response structure (Garmin Connect, 2026):
//
//	{
//	  "mostRecentTrainingStatus": {
//	    "latestTrainingStatusData": {
//	      "<deviceId>": {
//	        "calendarDate": "2026-03-25",
//	        "primaryTrainingDevice": true,
//	        "acuteTrainingLoadDTO": {
//	          "dailyTrainingLoadAcute": 330,
//	          "dailyTrainingLoadChronic": 438
//	        },
//	        "trainingStatusFeedbackPhrase": "MAINTAINING_4"
//	      }
//	    }
//	  },
//	  "mostRecentVO2Max": {
//	    "generic": { "calendarDate": "...", "vo2MaxPreciseValue": 48.7 }
//	  }
//	}
func (c *Client) FetchTrainingStatusDay(date string) (*TrainingStatusEntry, *VO2MaxPoint, error) {
	body, err := c.get("/metrics-service/metrics/trainingstatus/aggregated/" + date)
	if err != nil || body == nil {
		return nil, nil, err
	}

	var raw struct {
		MostRecentTrainingStatus *struct {
			LatestTrainingStatusData map[string]struct {
				CalendarDate        string `json:"calendarDate"`
				PrimaryDevice       bool   `json:"primaryTrainingDevice"`
				TrainingStatusPhrase string `json:"trainingStatusFeedbackPhrase"`
				AcuteLoadDTO        *struct {
					ATL float64 `json:"dailyTrainingLoadAcute"`
					CTL float64 `json:"dailyTrainingLoadChronic"`
				} `json:"acuteTrainingLoadDTO"`
			} `json:"latestTrainingStatusData"`
		} `json:"mostRecentTrainingStatus"`
		MostRecentVO2Max *struct {
			Generic *struct {
				CalendarDate  string  `json:"calendarDate"`
				VO2MaxPrecise float64 `json:"vo2MaxPreciseValue"`
			} `json:"generic"`
		} `json:"mostRecentVO2Max"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, nil, fmt.Errorf("trainingstatus parse: %w", err)
	}

	var entry *TrainingStatusEntry
	if raw.MostRecentTrainingStatus != nil {
		for _, dev := range raw.MostRecentTrainingStatus.LatestTrainingStatusData {
			if dev.AcuteLoadDTO != nil && dev.AcuteLoadDTO.ATL > 0 {
				atl := dev.AcuteLoadDTO.ATL
				ctl := dev.AcuteLoadDTO.CTL
				tsb := ctl - atl
				d := dev.CalendarDate
				if d == "" {
					d = date
				}
				entry = &TrainingStatusEntry{
					CalendarDate:          d,
					AcuteTrainingLoad:     atl,
					ChronicTrainingLoad:   ctl,
					TrainingStressBalance: tsb,
					TrainingStatusPhrase:  dev.TrainingStatusPhrase,
				}
				if dev.PrimaryDevice {
					break // prefer primary device
				}
			}
		}
	}

	var vo2pt *VO2MaxPoint
	if raw.MostRecentVO2Max != nil && raw.MostRecentVO2Max.Generic != nil {
		g := raw.MostRecentVO2Max.Generic
		if g.VO2MaxPrecise > 0 && g.CalendarDate != "" {
			vo2pt = &VO2MaxPoint{Date: g.CalendarDate, Value: g.VO2MaxPrecise}
		}
	}

	return entry, vo2pt, nil
}

// FetchVO2MaxRange fetches VO2Max estimates for a date range.
// Endpoint: /metrics-service/metrics/maxmet/daily/{startDate}/{endDate}
//
// Real API response (Garmin Connect, 2026):
//
//	[
//	  {
//	    "generic": { "calendarDate": "2026-03-18", "vo2MaxPreciseValue": 48.5, "vo2MaxValue": 48 },
//	    "cycling": null,
//	    "userId": 83278396
//	  }, ...
//	]
func (c *Client) FetchVO2MaxRange(startDate, endDate string) (map[string]float64, error) {
	path := fmt.Sprintf("/metrics-service/metrics/maxmet/daily/%s/%s", startDate, endDate)
	body, err := c.get(path)
	if err != nil || body == nil {
		return nil, err
	}

	result := make(map[string]float64)

	var items []struct {
		Generic *struct {
			CalendarDate  string  `json:"calendarDate"`
			VO2MaxPrecise float64 `json:"vo2MaxPreciseValue"`
			VO2MaxValue   float64 `json:"vo2MaxValue"`
		} `json:"generic"`
		Cycling *struct {
			CalendarDate  string  `json:"calendarDate"`
			VO2MaxPrecise float64 `json:"vo2MaxPreciseValue"`
		} `json:"cycling"`
	}

	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("maxmet parse: %w — body: %s", err, truncate(string(body), 200))
	}

	for _, item := range items {
		// Prefer running (generic) VO2Max; fall back to cycling
		if item.Generic != nil && item.Generic.VO2MaxPrecise > 0 && item.Generic.CalendarDate != "" {
			result[item.Generic.CalendarDate] = item.Generic.VO2MaxPrecise
		} else if item.Cycling != nil && item.Cycling.VO2MaxPrecise > 0 && item.Cycling.CalendarDate != "" {
			if _, exists := result[item.Cycling.CalendarDate]; !exists {
				result[item.Cycling.CalendarDate] = item.Cycling.VO2MaxPrecise
			}
		}
	}

	log.Printf("maxmet: parsed %d VO2Max data points", len(result))
	return result, nil
}

// FetchActivitiesRange fetches all activities between startDate and endDate (YYYY-MM-DD).
// Endpoint: /activitylist-service/activities/search/activities
// Results are paginated at 100 per page.
func (c *Client) FetchActivitiesRange(startDate, endDate string) ([]Activity, error) {
	var all []Activity
	const pageSize = 100

	for offset := 0; ; offset += pageSize {
		path := fmt.Sprintf(
			"/activitylist-service/activities/search/activities?startDate=%s&endDate=%s&start=%d&limit=%d",
			startDate, endDate, offset, pageSize,
		)
		body, err := c.get(path)
		if err != nil {
			return nil, fmt.Errorf("activities fetch: %w", err)
		}
		if body == nil {
			break
		}

		var items []struct {
			ActivityID     json.Number `json:"activityId"`
			ActivityName   string      `json:"activityName"`
			StartTimeLocal string      `json:"startTimeLocal"`
			ActivityType   struct {
				TypeKey string `json:"typeKey"`
			} `json:"activityType"`
			Distance  float64 `json:"distance"`
			Duration  float64 `json:"duration"`
			AverageHR float64 `json:"averageHR"`
			Calories  float64 `json:"calories"`
		}
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("activities parse: %w — body: %s", err, truncate(string(body), 200))
		}

		for _, item := range items {
			date := ""
			if len(item.StartTimeLocal) >= 10 {
				date = item.StartTimeLocal[:10]
			}
			all = append(all, Activity{
				ActivityID:   item.ActivityID.String(),
				Date:         date,
				ActivityType: item.ActivityType.TypeKey,
				DistanceM:    item.Distance,
				DurationS:    item.Duration,
				AvgHR:        int(item.AverageHR),
				Calories:     int(item.Calories),
				Name:         item.ActivityName,
			})
		}

		if len(items) < pageSize {
			break // last page
		}
	}

	log.Printf("activities: fetched %d total in %s → %s", len(all), startDate, endDate)
	return all, nil
}

// FetchWellnessDay fetches sleep score, body battery, stress, and resting HR for one day.
// Makes two API calls: daily summary (stress/HR/battery) + sleep data (sleep score).
func (c *Client) FetchWellnessDay(date string) (*WellnessDay, error) {
	wd := &WellnessDay{CalendarDate: date}

	// ── Wellness summary: stress, resting HR, body battery ────────────────
	if body, err := c.get("/wellness-service/wellness/dailySummary/" + date); err == nil && body != nil {
		var raw struct {
			RestingHeartRate        *int `json:"restingHeartRate"`
			AverageStressLevel      *int `json:"averageStressLevel"`
			BodyBatteryHighestValue *int `json:"bodyBatteryHighestValue"`
		}
		if json.Unmarshal(body, &raw) == nil {
			wd.RestingHR   = raw.RestingHeartRate
			wd.AvgStress   = raw.AverageStressLevel
			wd.BodyBattery = raw.BodyBatteryHighestValue
		}
	}

	// ── Sleep score ───────────────────────────────────────────────────────
	if body, err := c.get("/wellness-service/wellness/dailySleepData/" + date); err == nil && body != nil {
		var raw struct {
			DailySleepDTO *struct {
				SleepScores *struct {
					Overall *struct {
						Value *int `json:"value"`
					} `json:"overall"`
				} `json:"sleepScores"`
			} `json:"dailySleepDTO"`
		}
		if json.Unmarshal(body, &raw) == nil &&
			raw.DailySleepDTO != nil &&
			raw.DailySleepDTO.SleepScores != nil &&
			raw.DailySleepDTO.SleepScores.Overall != nil {
			wd.SleepScore = raw.DailySleepDTO.SleepScores.Overall.Value
		}
	}

	return wd, nil
}

// RawGet is a pass-through for the debug endpoint — returns raw JSON body
func (c *Client) RawGet(path string) ([]byte, error) {
	return c.get(path)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
