package processor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
)

// testAppConfig loads config with embedded defaults for testing.
func testAppConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	require.NoError(t, err)
	return cfg
}

// stubLogger is a simple logger stub for internal tests that need to test
// methods on Runner without using the mock package (to avoid import cycles).
type stubLogger struct {
	path       string
	printCalls []printCall
}

type printCall struct {
	Format string
	Args   []any
}

func (s *stubLogger) SetPhase(_ Phase) {}
func (s *stubLogger) Print(f string, a ...any) {
	s.printCalls = append(s.printCalls, printCall{Format: f, Args: a})
}
func (s *stubLogger) PrintRaw(_ string, _ ...any)      {}
func (s *stubLogger) PrintSection(_ Section)           {}
func (s *stubLogger) PrintAligned(_ string)            {}
func (s *stubLogger) LogQuestion(_ string, _ []string) {}
func (s *stubLogger) LogAnswer(_ string)               {}
func (s *stubLogger) LogDraftReview(_, _ string)       {}
func (s *stubLogger) Path() string                     { return s.path }
func (s *stubLogger) PrintCalls() []printCall          { return s.printCalls }

// newMockLogger creates a stub logger for internal tests.
func newMockLogger(path string) *stubLogger { //nolint:unparam // path is used by callers
	return &stubLogger{path: path}
}
