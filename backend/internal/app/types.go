package app

type GeneralSettings struct {
	Timezone      string `json:"timezone"`
	Theme         string `json:"theme"`
	SyncMinutes   int    `json:"syncMinutes"`
	RetentionDays int    `json:"retentionDays"`
	BeforeMinutes int    `json:"beforeMinutes"`
	NotifyBefore  bool   `json:"notifyBefore"`
	NotifyAfter   bool   `json:"notifyAfter"`
}
type SMTPSettings struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password,omitempty"`
	From       string `json:"from"`
	FromName   string `json:"fromName"`
	To         string `json:"to"`
	Security   string `json:"security"`
	Enabled    bool   `json:"enabled"`
	Configured bool   `json:"configured"`
}
type TelegramSettings struct {
	Token       string `json:"token,omitempty"`
	ChatID      int64  `json:"chatId"`
	Enabled     bool   `json:"enabled"`
	MenuEnabled bool   `json:"menuEnabled"`
	Configured  bool   `json:"configured"`
	BotName     string `json:"botName,omitempty"`
}
type TelegramSettingsResponse struct {
	TelegramSettings
	Warning string `json:"warning,omitempty"`
}
type AccountView struct {
	Email     *string `json:"email"`
	AuthMode  *string `json:"authMode"`
	PlanType  *string `json:"planType"`
	Connected bool    `json:"connected"`
}
type LimitBucket struct {
	LimitID               string  `json:"limitId"`
	LimitName             *string `json:"limitName"`
	WindowType            string  `json:"windowType"`
	UsedPercent           float64 `json:"usedPercent"`
	WindowDurationMinutes int     `json:"windowDurationMinutes"`
	ResetsAt              int64   `json:"resetsAt"`
	PlanType              *string `json:"planType"`
}
type MonthlyCreditLimit struct {
	RemainingPercent float64 `json:"remainingPercent"`
	ResetsAt         int64   `json:"resetsAt"`
	Used             string  `json:"used"`
	Limit            string  `json:"limit"`
}
type UsageSummary struct {
	LifetimeTokens        *int64 `json:"lifetimeTokens"`
	PeakDailyTokens       *int64 `json:"peakDailyTokens"`
	LongestRunningTurnSec *int64 `json:"longestRunningTurnSec"`
	CurrentStreakDays     *int   `json:"currentStreakDays"`
	LongestStreakDays     *int   `json:"longestStreakDays"`
	CallCount             *int64 `json:"callCount"`
	InputTokens           *int64 `json:"inputTokens"`
	OutputTokens          *int64 `json:"outputTokens"`
}
type UsagePoint struct {
	Date         string `json:"date"`
	TotalTokens  int64  `json:"totalTokens"`
	CallCount    *int64 `json:"callCount"`
	InputTokens  *int64 `json:"inputTokens"`
	OutputTokens *int64 `json:"outputTokens"`
}
type Dashboard struct {
	AccountID          int64               `json:"accountId"`
	DisplayName        string              `json:"displayName"`
	Account            AccountView         `json:"account"`
	Limits             []LimitBucket       `json:"limits"`
	MonthlyCreditLimit *MonthlyCreditLimit `json:"monthlyCreditLimit"`
	Summary            UsageSummary        `json:"summary"`
	Usage              []UsagePoint        `json:"usage"`
	FetchedAt          int64               `json:"fetchedAt"`
	Stale              bool                `json:"stale"`
	LastError          string              `json:"lastError,omitempty"`
}

func defaults() GeneralSettings {
	return GeneralSettings{Timezone: "UTC", Theme: "system", SyncMinutes: 5, RetentionDays: 90, BeforeMinutes: 30, NotifyBefore: true, NotifyAfter: true}
}
