package processor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/processor/mocks"
	"github.com/umputun/ralphex/pkg/status"
)

// testAppConfig loads config with embedded defaults for testing.
func testAppConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	require.NoError(t, err)
	return cfg
}

// newMockLogger creates a moq-generated logger mock with no-op implementations.
func newMockLogger(path string) *mocks.LoggerMock { //nolint:unparam // path is used by callers
	return &mocks.LoggerMock{
		PrintFunc:          func(_ string, _ ...any) {},
		PrintRawFunc:       func(_ string, _ ...any) {},
		PrintSectionFunc:   func(_ status.Section) {},
		PrintAlignedFunc:   func(_ string) {},
		LogQuestionFunc:    func(_ string, _ []string) {},
		LogAnswerFunc:      func(_ string) {},
		LogDraftReviewFunc: func(_, _ string) {},
		PathFunc:           func() string { return path },
	}
}
