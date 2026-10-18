// Package country holds what a job search needs to know about a country before it knows anything
// about the person: currency, working languages, time zone, free-movement area, local job boards and
// CV conventions. It is a fixed table, so setup costs no tokens and reads the same for every agent.
package country

import (
	"slices"
	"strings"
)

// Country is one row of the table.
type Country struct {
	Code      string   `json:"code"` // ISO 3166-1 alpha-2
	Name      string   `json:"name"`
	Currency  string   `json:"currency"`  // ISO 4217
	Languages []string `json:"languages"` // what employers write postings in, local language first
	TimeZone  string   `json:"time_zone"`
	Area      string   `json:"area,omitempty"`   // EU, EEA or CH: free movement for EU/EEA/Swiss citizens
	Boards    []string `json:"boards,omitempty"` // local boards scoutjobs searches, as search_boards entries
	Photo     string   `json:"cv_photo"`         // CV photo: common | optional | leave out
	Pages     string   `json:"cv_pages"`
	Aliases   []string `json:"-"`
}

// FreeMovement says whether EU/EEA citizens may work there without a permit.
func (c Country) FreeMovement() bool { return c.Area != "" }

var table = []Country{
	{"AT", "Austria", "EUR", []string{"German", "English"}, "CET (UTC+1)", "EU", nil, "common", "1-2 pages", []string{"Österreich"}},
	{"BE", "Belgium", "EUR", []string{"Dutch", "French", "English"}, "CET (UTC+1)", "EU", nil, "optional", "1-2 pages", []string{"België", "Belgique"}},
	{"BG", "Bulgaria", "EUR", []string{"Bulgarian", "English"}, "EET (UTC+2)", "EU", nil, "common", "1-2 pages", nil},
	{"HR", "Croatia", "EUR", []string{"Croatian", "English"}, "CET (UTC+1)", "EU", nil, "common", "1-2 pages", []string{"Hrvatska"}},
	{"CY", "Cyprus", "EUR", []string{"Greek", "English"}, "EET (UTC+2)", "EU", nil, "optional", "1-2 pages", nil},
	{"CZ", "Czechia", "CZK", []string{"Czech", "English"}, "CET (UTC+1)", "EU", nil, "common", "1-2 pages", []string{"Czech Republic", "Česko"}},
	{"DK", "Denmark", "DKK", []string{"Danish", "English"}, "CET (UTC+1)", "EU", []string{"jobindex:dk", "jobbank:dk", "jobdanmark:dk"}, "common", "1-2 pages", []string{"Danmark"}},
	{"EE", "Estonia", "EUR", []string{"Estonian", "English"}, "EET (UTC+2)", "EU", nil, "optional", "1-2 pages", []string{"Eesti"}},
	{"FI", "Finland", "EUR", []string{"Finnish", "English", "Swedish"}, "EET (UTC+2)", "EU", nil, "optional", "1-2 pages", []string{"Suomi"}},
	{"FR", "France", "EUR", []string{"French", "English"}, "CET (UTC+1)", "EU", nil, "optional", "1 page (2 when senior)", nil},
	{"DE", "Germany", "EUR", []string{"German", "English"}, "CET (UTC+1)", "EU", nil, "common", "2 pages", []string{"Deutschland"}},
	{"GR", "Greece", "EUR", []string{"Greek", "English"}, "EET (UTC+2)", "EU", nil, "common", "1-2 pages", []string{"Hellas"}},
	{"HU", "Hungary", "HUF", []string{"Hungarian", "English"}, "CET (UTC+1)", "EU", nil, "common", "1-2 pages", []string{"Magyarország"}},
	{"IE", "Ireland", "EUR", []string{"English"}, "GMT (UTC+0)", "EU", nil, "leave out", "2 pages", nil},
	{"IT", "Italy", "EUR", []string{"Italian", "English"}, "CET (UTC+1)", "EU", nil, "common", "1-2 pages", []string{"Italia"}},
	{"LV", "Latvia", "EUR", []string{"Latvian", "English"}, "EET (UTC+2)", "EU", nil, "optional", "1-2 pages", []string{"Latvija"}},
	{"LT", "Lithuania", "EUR", []string{"Lithuanian", "English"}, "EET (UTC+2)", "EU", nil, "optional", "1-2 pages", []string{"Lietuva"}},
	{"LU", "Luxembourg", "EUR", []string{"French", "German", "English"}, "CET (UTC+1)", "EU", nil, "common", "1-2 pages", nil},
	{"MT", "Malta", "EUR", []string{"English", "Maltese"}, "CET (UTC+1)", "EU", nil, "optional", "1-2 pages", nil},
	{"NL", "Netherlands", "EUR", []string{"Dutch", "English"}, "CET (UTC+1)", "EU", nil, "optional", "1-2 pages", []string{"Nederland", "Holland"}},
	{"PL", "Poland", "PLN", []string{"Polish", "English"}, "CET (UTC+1)", "EU", nil, "common", "1-2 pages", []string{"Polska"}},
	{"PT", "Portugal", "EUR", []string{"Portuguese", "English"}, "WET (UTC+0)", "EU", nil, "common", "1-2 pages", nil},
	{"RO", "Romania", "RON", []string{"Romanian", "English"}, "EET (UTC+2)", "EU", nil, "common", "1-2 pages", []string{"România"}},
	{"SK", "Slovakia", "EUR", []string{"Slovak", "English"}, "CET (UTC+1)", "EU", nil, "common", "1-2 pages", []string{"Slovensko"}},
	{"SI", "Slovenia", "EUR", []string{"Slovenian", "English"}, "CET (UTC+1)", "EU", nil, "common", "1-2 pages", []string{"Slovenija"}},
	{"ES", "Spain", "EUR", []string{"Spanish", "English"}, "CET (UTC+1)", "EU", nil, "common", "1-2 pages", []string{"España"}},
	{"SE", "Sweden", "SEK", []string{"Swedish", "English"}, "CET (UTC+1)", "EU", nil, "optional", "1-2 pages", []string{"Sverige"}},
	{"IS", "Iceland", "ISK", []string{"Icelandic", "English"}, "GMT (UTC+0)", "EEA", nil, "optional", "1-2 pages", []string{"Ísland"}},
	{"NO", "Norway", "NOK", []string{"Norwegian", "English"}, "CET (UTC+1)", "EEA", nil, "optional", "1-2 pages", []string{"Norge"}},
	{"CH", "Switzerland", "CHF", []string{"German", "French", "English"}, "CET (UTC+1)", "CH", nil, "common", "2 pages", []string{"Schweiz", "Suisse", "Svizzera"}},
	{"GB", "United Kingdom", "GBP", []string{"English"}, "GMT (UTC+0)", "", nil, "leave out", "2 pages", []string{"UK", "Great Britain", "England", "Scotland", "Wales", "Britain"}},
	{"US", "United States", "USD", []string{"English"}, "UTC-5 to UTC-8", "", nil, "leave out", "1 page (2 when senior)", []string{"USA", "America", "United States of America"}},
	{"CA", "Canada", "CAD", []string{"English", "French"}, "UTC-3:30 to UTC-8", "", nil, "leave out", "2 pages", nil},
	{"MX", "Mexico", "MXN", []string{"Spanish", "English"}, "UTC-6", "", nil, "common", "1-2 pages", []string{"México"}},
	{"BR", "Brazil", "BRL", []string{"Portuguese", "English"}, "UTC-3", "", nil, "optional", "1-2 pages", []string{"Brasil"}},
	{"AR", "Argentina", "ARS", []string{"Spanish", "English"}, "UTC-3", "", nil, "optional", "1-2 pages", nil},
	{"AU", "Australia", "AUD", []string{"English"}, "UTC+8 to UTC+10", "", nil, "leave out", "2-3 pages", nil},
	{"NZ", "New Zealand", "NZD", []string{"English"}, "UTC+12", "", nil, "leave out", "2 pages", []string{"Aotearoa"}},
	{"IN", "India", "INR", []string{"English", "Hindi"}, "IST (UTC+5:30)", "", nil, "optional", "1-2 pages", []string{"Bharat"}},
	{"SG", "Singapore", "SGD", []string{"English"}, "UTC+8", "", nil, "optional", "1-2 pages", nil},
	{"JP", "Japan", "JPY", []string{"Japanese", "English"}, "JST (UTC+9)", "", nil, "common", "rirekisho form plus a 1-2 page work history", []string{"Nippon", "Nihon"}},
	{"KR", "South Korea", "KRW", []string{"Korean", "English"}, "KST (UTC+9)", "", nil, "common", "1-2 pages", []string{"Korea"}},
	{"AE", "United Arab Emirates", "AED", []string{"English", "Arabic"}, "GST (UTC+4)", "", nil, "common", "1-2 pages", []string{"UAE", "Emirates", "Dubai"}},
	{"SA", "Saudi Arabia", "SAR", []string{"Arabic", "English"}, "AST (UTC+3)", "", nil, "common", "1-2 pages", []string{"KSA"}},
	{"EG", "Egypt", "EGP", []string{"Arabic", "English"}, "EET (UTC+2)", "", nil, "optional", "1-2 pages", []string{"Misr"}},
	{"IL", "Israel", "ILS", []string{"Hebrew", "English"}, "IST (UTC+2)", "", nil, "leave out", "1-2 pages", nil},
	{"TR", "Türkiye", "TRY", []string{"Turkish", "English"}, "TRT (UTC+3)", "", nil, "common", "1-2 pages", []string{"Turkey"}},
	{"ZA", "South Africa", "ZAR", []string{"English"}, "SAST (UTC+2)", "", nil, "optional", "2-3 pages", nil},
	{"NG", "Nigeria", "NGN", []string{"English"}, "WAT (UTC+1)", "", nil, "optional", "1-2 pages", nil},
}

// All lists every country in table order.
func All() []Country { return slices.Clone(table) }

// Lookup finds a country by code, English name or a local name, ignoring case.
func Lookup(s string) (Country, bool) {
	s = strings.TrimSpace(s)
	for _, c := range table {
		if strings.EqualFold(s, c.Code) || strings.EqualFold(s, c.Name) || slices.ContainsFunc(c.Aliases, func(a string) bool { return strings.EqualFold(s, a) }) {
			return c, true
		}
	}
	return Country{}, false
}

// BoardOwner names the country whose local board this search_boards entry is, or "".
func BoardOwner(board string) string {
	for _, c := range table {
		if slices.Contains(c.Boards, board) {
			return c.Code
		}
	}
	return ""
}
