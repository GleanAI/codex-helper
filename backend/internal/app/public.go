package app

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"codex-helper/internal/store"
)

type publicOverviewResponse struct {
	Cards []publicOverviewCard `json:"cards"`
}

type publicOverviewCard struct {
	Title           string                     `json:"title"`
	EmailIdentified bool                       `json:"emailIdentified"`
	Connections     []publicOverviewConnection `json:"connections"`
}

type publicOverviewConnection struct {
	DisplayName        string                    `json:"displayName"`
	PlanType           *string                   `json:"planType"`
	Kind               string                    `json:"kind"`
	Status             string                    `json:"status"`
	FetchedAt          int64                     `json:"fetchedAt"`
	Limits             []publicOverviewLimit     `json:"limits"`
	MonthlyCreditLimit *publicMonthlyCreditLimit `json:"monthlyCreditLimit"`
}

type publicOverviewLimit struct {
	LimitName             *string `json:"limitName"`
	WindowDurationMinutes int     `json:"windowDurationMinutes"`
	UsedPercent           float64 `json:"usedPercent"`
	ResetsAt              int64   `json:"resetsAt"`
}

type publicMonthlyCreditLimit struct {
	RemainingPercent float64 `json:"remainingPercent"`
	ResetsAt         int64   `json:"resetsAt"`
}

func (a *App) publicOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonOut(w, http.StatusMethodNotAllowed, map[string]string{"error": "方法不允许"})
		return
	}
	if !a.store.Initialized() {
		jsonOut(w, http.StatusConflict, map[string]string{"error": "请先初始化"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")

	accounts, err := a.store.Accounts()
	if err != nil {
		jsonOut(w, http.StatusInternalServerError, map[string]string{"error": "无法读取公开用量"})
		return
	}

	response := publicOverviewResponse{Cards: []publicOverviewCard{}}
	cardIndexes := make(map[string]int)
	for _, account := range accounts {
		key := "account:" + account.DisplayName
		title := account.DisplayName
		email := ""
		if account.Email != nil {
			email = strings.TrimSpace(*account.Email)
		}
		emailIdentified := email != ""
		if emailIdentified {
			key = "email:" + strings.ToLower(email)
			title = maskEmail(email)
		} else {
			// Accounts without an email must never be grouped merely because their
			// display names happen to match.
			key += ":" + strconv.FormatInt(account.ID, 10)
		}
		index, ok := cardIndexes[key]
		if !ok {
			index = len(response.Cards)
			cardIndexes[key] = index
			response.Cards = append(response.Cards, publicOverviewCard{
				Title:           title,
				EmailIdentified: emailIdentified,
				Connections:     []publicOverviewConnection{},
			})
		}
		response.Cards[index].Connections = append(response.Cards[index].Connections, a.publicConnection(account))
	}
	for i := range response.Cards {
		sort.SliceStable(response.Cards[i].Connections, func(left, right int) bool {
			return publicKindOrder(response.Cards[i].Connections[left].Kind) < publicKindOrder(response.Cards[i].Connections[right].Kind)
		})
	}
	jsonOut(w, http.StatusOK, response)
}

func publicKindOrder(kind string) int {
	switch kind {
	case "personal":
		return 0
	case "team":
		return 1
	default:
		return 2
	}
}

func (a *App) publicConnection(account store.Account) publicOverviewConnection {
	dashboard := Dashboard{}
	if runtime := a.runtime(account.ID); runtime != nil {
		runtime.syncing.Lock()
		dashboard = runtime.dash
		runtime.syncing.Unlock()
	}

	status := "healthy"
	switch {
	case !account.Connected:
		status = "offline"
	case dashboard.LastError != "":
		status = "failed"
	case dashboard.FetchedAt == 0:
		status = "loading"
	case account.ActualKind == "unknown":
		status = "pending"
	case dashboard.Stale:
		status = "stale"
	}

	limits := make([]publicOverviewLimit, 0, len(dashboard.Limits))
	for _, limit := range dashboard.Limits {
		limits = append(limits, publicOverviewLimit{
			LimitName:             limit.LimitName,
			WindowDurationMinutes: limit.WindowDurationMinutes,
			UsedPercent:           limit.UsedPercent,
			ResetsAt:              limit.ResetsAt,
		})
	}
	var monthly *publicMonthlyCreditLimit
	if dashboard.MonthlyCreditLimit != nil {
		monthly = &publicMonthlyCreditLimit{
			RemainingPercent: dashboard.MonthlyCreditLimit.RemainingPercent,
			ResetsAt:         dashboard.MonthlyCreditLimit.ResetsAt,
		}
	}
	return publicOverviewConnection{
		DisplayName:        account.DisplayName,
		PlanType:           account.PlanType,
		Kind:               account.ActualKind,
		Status:             status,
		FetchedAt:          dashboard.FetchedAt,
		Limits:             limits,
		MonthlyCreditLimit: monthly,
	}
}

func maskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 1 || at == len(email)-1 {
		return maskEmailPart(email)
	}
	local := maskEmailPart(email[:at])
	domain := strings.Split(email[at+1:], ".")
	for i := range domain {
		if i != len(domain)-1 {
			domain[i] = maskEmailPart(domain[i])
		}
	}
	return local + "@" + strings.Join(domain, ".")
}

func maskEmailPart(part string) string {
	length := utf8.RuneCountInString(part)
	if length <= 1 {
		return "*"
	}
	runes := []rune(part)
	if length == 2 {
		return string(runes[0]) + "*"
	}
	return string(runes[0]) + strings.Repeat("*", length-2) + string(runes[length-1])
}
