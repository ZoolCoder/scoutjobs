package cli

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config mirrors ~/.scoutjobs/config.yaml. The file is flat key: value so no yaml dependency is needed.
type Config struct {
	FollowUpDays      int    `json:"follow_up_days"`
	StaleDays         int    `json:"stale_days"`
	WeeklyApplyTarget int    `json:"weekly_apply_target"`
	FitApplyFloor     int    `json:"fit_apply_floor"`
	Currency          string `json:"currency"`
	DashboardAddr     string `json:"dashboard_addr"`
	// integrations
	APIToken      string   `json:"-"`
	WebhookURL    string   `json:"-"`
	WebhookSecret string   `json:"-"`
	EmailFrom     string   `json:"email_from"`
	SMTPHost      string   `json:"-"`
	SMTPUser      string   `json:"-"`
	IMAPHost      string   `json:"-"`
	IMAPUser      string   `json:"-"`
	SearchQueries []string `json:"search_queries"`
	SearchBoards  []string `json:"search_boards"`
	SearchRemote  bool     `json:"search_remote"`
	Theme         string   `json:"theme"`
}

func DefaultConfig() Config {
	return Config{FollowUpDays: 10, StaleDays: 60, WeeklyApplyTarget: 5, FitApplyFloor: 60, Currency: "EUR", DashboardAddr: "127.0.0.1:7070", SearchRemote: true, Theme: "light"}
}

func LoadConfig(home string) Config {
	c := DefaultConfig()
	f, err := os.Open(filepath.Join(home, "config.yaml"))
	if err != nil {
		return c
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		n, _ := strconv.Atoi(v)
		switch k {
		case "follow_up_days":
			if n > 0 {
				c.FollowUpDays = n
			}
		case "stale_days":
			if n > 0 {
				c.StaleDays = n
			}
		case "weekly_apply_target":
			if n > 0 {
				c.WeeklyApplyTarget = n
			}
		case "fit_apply_floor":
			if n > 0 {
				c.FitApplyFloor = n
			}
		case "currency":
			if v != "" {
				c.Currency = v
			}
		case "dashboard_addr":
			if v != "" {
				c.DashboardAddr = v
			}
		case "api_token":
			c.APIToken = strings.Trim(v, `"'`)
		case "webhook_url":
			c.WebhookURL = strings.Trim(v, `"'`)
		case "webhook_secret":
			c.WebhookSecret = strings.Trim(v, `"'`)
		case "email_from":
			c.EmailFrom = strings.Trim(v, `"'`)
		case "smtp_host":
			c.SMTPHost = strings.Trim(v, `"'`)
		case "smtp_user":
			c.SMTPUser = strings.Trim(v, `"'`)
		case "imap_host":
			c.IMAPHost = strings.Trim(v, `"'`)
		case "imap_user":
			c.IMAPUser = strings.Trim(v, `"'`)
		case "search_queries":
			c.SearchQueries = splitList(v)
		case "search_boards":
			c.SearchBoards = splitList(v)
		case "search_remote":
			c.SearchRemote = v != "false" && v != "no" && v != "0"
		case "theme":
			if v == "dark" || v == "light" || v == "auto" {
				c.Theme = v
			}
		}
	}
	return c
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(strings.Trim(v, "[]"), ",") {
		if p = strings.Trim(strings.TrimSpace(p), `"'`); p != "" {
			out = append(out, p)
		}
	}
	return out
}
