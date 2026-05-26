package processor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/umputun/ralphex/pkg/config"
)

func TestPromptBuilder_FinalPrompts(t *testing.T) {
	appCfg := &config.Config{
		TaskPrompt:         "task {{PLAN_FILE}} {{PROGRESS_FILE}}",
		ReviewFirstPrompt:  "first {{GOAL}}",
		ReviewSecondPrompt: "second {{DEFAULT_BRANCH}}",
		CodexReviewPrompt:  "codex {{DIFF_INSTRUCTION}} {{PREVIOUS_REVIEW_CONTEXT}}",
		CodexPrompt:        "eval {{CODEX_OUTPUT}} {{GOAL}}",
		CustomReviewPrompt: "custom {{DIFF_INSTRUCTION}} {{PREVIOUS_REVIEW_CONTEXT}}",
		CustomEvalPrompt:   "custom eval {{CUSTOM_OUTPUT}}",
		MakePlanPrompt:     "make {{PLAN_DESCRIPTION}} {{PLANS_DIR}}",
		FinalizePrompt:     "finalize {{GOAL}}",
		PlansDir:           "custom/plans",
	}
	cfg := Config{
		PlanFile: "docs/plans/test.md", ProgressPath: "progress.txt", PlanDescription: "add feature",
		DefaultBranch: "main", AppConfig: appCfg,
	}
	builder := newPromptBuilder(promptBuilderOpts{cfg: cfg, log: newMockLogger(), locator: newPlanLocator(cfg)})

	assert.Equal(t, "task docs/plans/test.md progress.txt", builder.TaskPrompt())
	assert.Equal(t, "first implementation of plan at docs/plans/test.md", builder.FirstReviewPrompt())
	assert.Equal(t, "prefix: second main", builder.SecondReviewPrompt("prefix: "))
	assert.Contains(t, builder.CodexReviewPrompt(true, ""), "git diff main...HEAD")
	assert.Contains(t, builder.CustomReviewPrompt(false, "fixed"), "PREVIOUS REVIEW CONTEXT")
	assert.Equal(t, "eval findings implementation of plan at docs/plans/test.md", builder.CodexEvaluationPrompt("findings"))
	assert.Equal(t, "custom eval custom findings", builder.CustomEvaluationPrompt("custom findings"))
	assert.Equal(t, "make add feature custom/plans", builder.PlanPrompt())
	assert.Equal(t, "finalize implementation of plan at docs/plans/test.md", builder.FinalizePrompt())
}

func TestPromptBuilder_CodexTaskGuidance(t *testing.T) {
	appCfg := &config.Config{TaskPrompt: "do work", Executor: config.ExecutorCodex}
	cfg := Config{AppConfig: appCfg}
	builder := newPromptBuilder(promptBuilderOpts{cfg: cfg, log: newMockLogger(), locator: newPlanLocator(cfg)})

	prompt := builder.TaskPrompt()
	assert.True(t, strings.HasPrefix(prompt, codexTaskGuidance))
	assert.Contains(t, prompt, "do work")
}
