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
}

// VO2MaxPoint is a single VO2Max data point with its date
type VO2MaxPoint struct {
	Date  string
	Value float64
}

// DashboardResponse is the full response to the frontend
type DashboardResponse struct {
	Metrics  []DailyMetrics `json:"metrics"`
	LastSync string         `json:"lastSync"`
}
