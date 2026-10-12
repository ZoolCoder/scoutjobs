package calsync

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/gmail"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// AgentEvents is the setting that holds the events the AI agent's calendar sync last listed.
const AgentEvents = "calendar_agent_events"

// AgentEvent is one calendar event as the agent reports it.
type AgentEvent struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Start    string `json:"start"`
	End      string `json:"end"`
	AllDay   bool   `json:"all_day"`
	Location string `json:"location"`
}

var agentLayouts = []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"}

func agentTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range agentLayouts {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// SaveAgentEvents replaces the saved events with the agent's list, given as a JSON array.
// It returns how many it kept; events without a readable start are dropped.
func SaveAgentEvents(s *store.Store, raw string) (int, error) {
	var in []AgentEvent
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return 0, fmt.Errorf("events must be a JSON array of {id,title,start,end,all_day,location}: %w", err)
	}
	out := make([]gmail.Event, 0, len(in))
	for i, a := range in {
		start, ok := agentTime(a.Start)
		if !ok {
			continue
		}
		end, ok := agentTime(a.End)
		if !ok || !end.After(start) {
			end = start.Add(time.Hour)
			if a.AllDay || len(strings.TrimSpace(a.Start)) == 10 {
				end = start.AddDate(0, 0, 1)
			}
		}
		id := a.ID
		if id == "" {
			id = fmt.Sprintf("a%d-%d", start.Unix(), i)
		}
		out = append(out, gmail.Event{ID: id, Title: a.Title, Start: start, End: end,
			AllDay: a.AllDay || len(strings.TrimSpace(a.Start)) == 10, Location: a.Location})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return 0, err
	}
	return len(out), s.SetSetting(AgentEvents, string(b))
}

// LoadAgentEvents returns the events the agent last saved.
func LoadAgentEvents(s *store.Store) []gmail.Event {
	var evs []gmail.Event
	_ = json.Unmarshal([]byte(s.Setting(AgentEvents)), &evs)
	return evs
}
