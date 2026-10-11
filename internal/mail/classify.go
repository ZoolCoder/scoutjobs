package mail

import "strings"

var (
	rejectWords    = []string{"unfortunately", "regret", "not moving forward", "not be moving forward", "other candidates", "decided not to", "will not be proceeding", "no longer under consideration", "position has been filled", "leider", "absage"}
	interviewWords = []string{"interview", "schedule a call", "book a call", "meet the team", "next step", "phone screen", "video call", "calendly", "availability", "vorstellungsgespräch", "kennenlernen"}
)

// Classify guesses what a reply means from its subject and first lines: "rejected", "interview" or "".
// Rejections win: "unfortunately we will not schedule an interview" is a rejection.
func Classify(subject, text string) string {
	s := strings.ToLower(subject + "\n" + text)
	for _, w := range rejectWords {
		if strings.Contains(s, w) {
			return "rejected"
		}
	}
	for _, w := range interviewWords {
		if strings.Contains(s, w) {
			return "interview"
		}
	}
	return ""
}
