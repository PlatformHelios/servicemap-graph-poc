package cmdb

import (
	"fmt"
	"strings"
)

// FormatGeneratedID builds a compact identifier PREFIX-000001. Sequence is the
// next unused number for that kind (numeric suffixes only, so a one-off ULID
// left over from an earlier generator is skipped).
func FormatGeneratedID(kind NodeKind, sequence int) (string, error) {
	prefix, err := GeneratedIDPrefix(kind)
	if err != nil {
		return "", err
	}
	if sequence < 1 {
		sequence = 1
	}
	return fmt.Sprintf("%s-%06d", prefix, sequence), nil
}

// ListOptions narrows and pages a node or relationship list. A zero Limit
// returns every matching record (the graph map and pickers need the full set);
// a positive Limit returns one page and the total count alongside it.
type ListOptions struct {
	IncludeRetired bool
	Limit          int
	Offset         int
	// Query is a case-insensitive substring matched against the record's id and
	// property values.
	Query string
	// InvolvedID, for request lists, keeps only the requests an identity raised
	// or that were raised for it, anchoring the read at that identity instead
	// of scanning every request.
	InvolvedID string
	// RequestType, for request lists, keeps only one catalog form's requests.
	RequestType RequestType
}

// Validate rejects negative paging values and trims the free-text filters.
func (options ListOptions) Validate() (ListOptions, error) {
	if options.Limit < 0 {
		return options, fmt.Errorf("%w: limit cannot be negative", ErrInvalid)
	}
	if options.Offset < 0 {
		return options, fmt.Errorf("%w: offset cannot be negative", ErrInvalid)
	}
	options.Query = strings.ToLower(strings.TrimSpace(options.Query))
	options.InvolvedID = strings.TrimSpace(options.InvolvedID)
	return options, nil
}

// ListTotals counts the records a list matched before paging, so a page can
// report where it sits; Active excludes retired records.
type ListTotals struct {
	Total  int `json:"total"`
	Active int `json:"active"`
}
