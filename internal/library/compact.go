package library

// CompactEntry is the small library_find row: enough to decide whether a card
// is relevant, with library_get for the full text. Each row marshals to under
// 300 characters for plain text (see the rune caps below; JSON escaping of
// quotes or control characters in the source text can add a few).
type CompactEntry struct {
	Dewey        string `json:"dewey"`
	Title        string `json:"title"`
	Establishes  string `json:"establishes"`
	Status       string `json:"status"`
	MatchedField string `json:"matched_field,omitempty"`
}

// Rune caps for the two free-text compact fields. The JSON keys, dewey,
// status and the longest matched_field label take about 110 characters, so
// these keep a row under 300.
const (
	compactTitleRunes       = 72
	compactEstablishesRunes = 96
)

// Compact projects an entry onto a CompactEntry. Title is the first line of
// the raw citation (author, year and title in the usual citation style);
// Establishes is its first line. Both are truncated with an ellipsis.
// matchedField is the keyword-mode field label, or "" for other modes.
func Compact(e LibraryEntry, matchedField string) CompactEntry {
	return CompactEntry{
		Dewey:        e.Dewey,
		Title:        summarizeFirstLine(e.Citation.Raw, compactTitleRunes),
		Establishes:  summarizeFirstLine(e.Establishes, compactEstablishesRunes),
		Status:       e.Status.Type,
		MatchedField: matchedField,
	}
}
