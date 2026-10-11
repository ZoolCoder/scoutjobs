package apply

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

// PostingName is the posting text the fit review, the drafts and the draft review are checked against.
const PostingName = "posting.md"

// HasPosting is true when the application folder holds the posting text.
func HasPosting(s *store.Store, j store.Job) bool {
	_, err := os.Stat(filepath.Join(Dir(s, j), PostingName))
	return err == nil
}

// SavePosting writes the posting's description to posting.md, headed by the job, unless the folder
// already has one: a posting the user saved or edited is never replaced.
func SavePosting(s *store.Store, j store.Job, description string) error {
	description = strings.TrimSpace(description)
	if description == "" || HasPosting(s, j) {
		return nil
	}
	dir := Dir(s, j)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	head := fmt.Sprintf("# %s at %s\n\n", j.Title, j.Company)
	if j.URL != "" {
		head += "Source: " + j.URL + "\n\n"
	}
	return os.WriteFile(filepath.Join(dir, PostingName), []byte(head+description+"\n"), 0o644)
}
