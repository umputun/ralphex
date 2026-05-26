package processor

import "strings"

type promptBuilder struct {
	cfg                    Config
	log                    Logger
	locator                *planLocator
	codexFrontmatterWarned map[string]bool
}

type promptBuilderOpts struct {
	cfg     Config
	log     Logger
	locator *planLocator
}

func newPromptBuilder(opts promptBuilderOpts) *promptBuilder {
	return &promptBuilder{cfg: opts.cfg, log: opts.log, locator: opts.locator}
}

func (b *promptBuilder) TaskPrompt() string {
	return b.prependCodexTaskGuidance(b.replacePromptVariables(b.cfg.AppConfig.TaskPrompt))
}

func (b *promptBuilder) FirstReviewPrompt() string {
	return b.prependCodexReviewGuidance(b.replacePromptVariables(b.cfg.AppConfig.ReviewFirstPrompt))
}

func (b *promptBuilder) SecondReviewPrompt(prefix string) string {
	return prefix + b.prependCodexReviewGuidance(b.replacePromptVariables(b.cfg.AppConfig.ReviewSecondPrompt))
}

func (b *promptBuilder) CodexReviewPrompt(isFirst bool, claudeResponse string) string {
	return b.replaceVariablesWithIteration(b.cfg.AppConfig.CodexReviewPrompt, isFirst, claudeResponse)
}

func (b *promptBuilder) CodexEvaluationPrompt(codexOutput string) string {
	prompt := b.replacePromptVariables(b.cfg.AppConfig.CodexPrompt)
	return strings.ReplaceAll(prompt, "{{CODEX_OUTPUT}}", codexOutput)
}

func (b *promptBuilder) CustomReviewPrompt(isFirst bool, claudeResponse string) string {
	return b.replaceVariablesWithIteration(b.cfg.AppConfig.CustomReviewPrompt, isFirst, claudeResponse)
}

func (b *promptBuilder) CustomEvaluationPrompt(customOutput string) string {
	prompt := b.replacePromptVariables(b.cfg.AppConfig.CustomEvalPrompt)
	return strings.ReplaceAll(prompt, "{{CUSTOM_OUTPUT}}", customOutput)
}

func (b *promptBuilder) PlanPrompt() string {
	prompt := b.cfg.AppConfig.MakePlanPrompt
	prompt = strings.ReplaceAll(prompt, "{{PLAN_DESCRIPTION}}", b.cfg.PlanDescription)
	result := b.replaceBaseVariables(prompt)
	return b.appendCommitTrailerInstruction(result)
}

func (b *promptBuilder) FinalizePrompt() string {
	return b.replacePromptVariables(b.cfg.AppConfig.FinalizePrompt)
}
