package web

import (
	"strings"
	"time"

	"github.com/umputun/ralphex/pkg/status"
)

// ParsedLineType indicates what kind of progress line was parsed.
type ParsedLineType int

// parsed line type constants.
const (
	ParsedLineSkip      ParsedLineType = iota // header line or separator, should be skipped
	ParsedLineSection                         // section header (--- name ---)
	ParsedLineTimestamp                       // timestamped content line
	ParsedLinePlain                           // plain text without timestamp
)

// ParsedLine is the result of parsing a single progress file line.
// callers convert this to Event objects with their own context (phase, pending section logic, etc.).
type ParsedLine struct {
	Type      ParsedLineType
	Text      string       // content text (without timestamp prefix for timestamped lines)
	Section   string       // section name (only for ParsedLineSection)
	Timestamp time.Time    // parsed timestamp (only for ParsedLineTimestamp)
	EventType EventType    // detected event type (output, error, warn, signal)
	Signal    string       // extracted signal name, if any
	Phase     status.Phase // phase derived from section name (only for ParsedLineSection)
}

// parseProgressLine parses a single progress file line into a ParsedLine.
// handles header separator detection, section headers, timestamped lines, and plain lines.
// inHeader indicates whether we're still in the file header section.
// returns the parsed result and updated inHeader state.
func parseProgressLine(line string, inHeader bool) (ParsedLine, bool) {
	// check for header separator (line of dashes without spaces, e.g. "----...----")
	if isHeaderSeparator(line) {
		return ParsedLine{Type: ParsedLineSkip}, false
	}

	// skip header lines
	if inHeader {
		return ParsedLine{Type: ParsedLineSkip}, true
	}

	// check for section header (--- section name ---)
	if matches := sectionRegex.FindStringSubmatch(line); matches != nil {
		sectionName := matches[1]
		return ParsedLine{
			Type:    ParsedLineSection,
			Text:    sectionName,
			Section: sectionName,
			Phase:   phaseFromSection(sectionName),
		}, false
	}

	// check for timestamped line
	if matches := timestampRegex.FindStringSubmatch(line); matches != nil {
		text := matches[2]

		ts, err := time.Parse("06-01-02 15:04:05", matches[1])
		if err != nil {
			ts = time.Now()
		}

		eventType := detectEventType(text)
		signal := extractSignalFromText(text)
		if signal != "" {
			eventType = EventTypeSignal
		}

		return ParsedLine{
			Type:      ParsedLineTimestamp,
			Text:      text,
			Timestamp: ts,
			EventType: eventType,
			Signal:    signal,
		}, false
	}

	// plain line (no timestamp)
	return ParsedLine{
		Type:      ParsedLinePlain,
		Text:      line,
		EventType: EventTypeOutput,
	}, false
}

// isHeaderSeparator checks if a line is a header separator (line of dashes without spaces).
func isHeaderSeparator(line string) bool {
	return len(line) >= 3 && line[0] == '-' && line[1] == '-' && line[2] == '-' &&
		strings.Count(line, "-") > 20 && !strings.ContainsRune(line, ' ')
}
