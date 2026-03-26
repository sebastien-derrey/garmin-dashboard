package garmin

// HRVResponse is the raw API response for a single day's HRV
type HRVResponse struct {
	HRVSummary HRVSummary `json:"hrvSummary"`
}

type HRVSummary struct {
	CalendarDate   string       `json:"calendarDate"`
	LastNight      *int         `json:"lastNightAvg"` // API field is lastNightAvg, not lastNight
	WeeklyAvg      *int         `json:"weeklyAvg"`
	Status         string       `json:"status"`
	FeedbackPhrase string       `json:"feedbackPhrase"`
	Baseline       *HRVBaseline `json:"baseline"`
}

type HRVBaseline struct {
	LowUpper      int     `json:"lowUpper"`
	BalancedLow   int     `json:"balancedLow"`
	BalancedUpper int     `json:"balancedUpper"`
	MarkerValue   float64 `json:"markerValue"`
}

// TrainingStatusEntry is a single day in the training status response
type TrainingStatusEntry struct {
	CalendarDate          string  `json:"calendarDate"`
	TrainingStatusPhrase  string  `json:"trainingStatusPhrase"`
	TrainingLoadBalance   float64 `json:"trainingLoadBalance"`
	AcuteTrainingLoad     float64 `json:"acuteTrainingLoad"`
	ChronicTrainingLoad   float64 `json:"chronicTrainingLoad"`
	TrainingStressBalance float64 `json:"trainingStressBalance"`
	VO2MaxPreciseValue    float64 `json:"vo2MaxPreciseValue"`
}

// DailyMetrics is the aggregated data point sent to the frontend
type DailyMetrics struct {
	Date      string   `json:"date"`
	HRV       *float64 `json:"hrv"`
	HRVStatus string   `json:"hrvStatus"`
	ATL       *float64 `json:"atl"`    // Acute Training Load (fatigue)
	CTL       *float64 `json:"ctl"`    // Chronic Training Load (fitness)
	TSB       *float64 `json:"tsb"`    // Training Stress Balance (form)
	VO2Max    *float64 `json:"vo2max"` // VO2Max estimate
	Status    string   `json:"status"` // Training status phrase
	KmRun     *float64 `json:"kmRun"`  // Total km run that day
	SleepScore  *float64 `json:"sleepScore"`  // Overall sleep quality 0-100
	BodyBattery *float64 `json:"bodyBattery"` // Highest body battery of the day
	AvgStress   *float64 `json:"avgStress"`   // Average stress level
	RestingHR   *float64 `json:"restingHr"`   // Resting heart rate bpm
}

// Activity represents a single Garmin activity (run, ride, etc.)
type Activity struct {
	ActivityID   string  // Garmin activity ID
	Date         string  // YYYY-MM-DD local date
	ActivityType string  // e.g. "running", "trail_running"
	DistanceM    float64 // meters
	DurationS    float64 // seconds
	AvgHR        int
	Calories     int
	Name         string
}

// VO2MaxPoint is a single VO2Max data point with its date
type VO2MaxPoint struct {
	Date  string
	Value float64
}

// WellnessDay holds daily wellness metrics fetched from Garmin wellness endpoints
type WellnessDay struct {
	CalendarDate string
	SleepScore   *int // overall sleep score 0–100
	BodyBattery  *int // highest body battery value of the day
	AvgStress    *int // average stress level 0–100
	RestingHR    *int // resting heart rate in bpm
}

// DashboardResponse is the full response to the frontend
type DashboardResponse struct {
	Metrics      []DailyMetrics `json:"metrics"`
	LastSync     string         `json:"lastSync"`
	EarliestInDB string         `json:"earliestInDB"`
	LatestInDB   string         `json:"latestInDB"`
}
