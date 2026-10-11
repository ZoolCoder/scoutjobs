package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// flattenSignals turns a decision's JSON signals into "key" -> "value" strings.
// Arrays become one entry per element so counts work per tag.
func flattenSignals(raw string) map[string]string {
	out := map[string]string{}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return out
	}
	for k, v := range m {
		switch t := v.(type) {
		case []any:
			var parts []string
			for _, e := range t {
				parts = append(parts, fmt.Sprint(e))
			}
			sort.Strings(parts)
			for _, p := range parts {
				out[k+":"+strings.ToLower(p)] = "1"
			}
		case nil:
		default:
			out[k] = strings.ToLower(fmt.Sprint(t))
		}
	}
	return out
}
