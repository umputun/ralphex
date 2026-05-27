package phase

// GitState reads git state for review loops.
type GitState struct {
	deps *Deps
	log  Logger
}

type gitSnapshot struct {
	head string
	diff string
}

type stalemateState struct {
	cfg             Config
	log             Logger
	unchangedRounds int
}

// NewGitState creates a git state reader backed by shared dependencies.
func NewGitState(deps *Deps, log Logger) *GitState {
	return &GitState{deps: deps, log: log}
}

func (g *GitState) headHash() string {
	if g == nil || g.deps == nil || g.deps.Git == nil {
		return ""
	}
	hash, err := g.deps.Git.HeadHash()
	if err != nil {
		g.log.Print("warning: failed to get HEAD hash: %v", err)
		return ""
	}
	return hash
}

func (g *GitState) diffFingerprint() string {
	if g == nil || g.deps == nil || g.deps.Git == nil {
		return ""
	}
	fp, err := g.deps.Git.DiffFingerprint()
	if err != nil {
		g.log.Print("warning: failed to get diff fingerprint: %v", err)
		return ""
	}
	return fp
}

func (g *GitState) snapshot() gitSnapshot {
	return gitSnapshot{head: g.headHash(), diff: g.diffFingerprint()}
}

func newStalemateState(cfg Config, log Logger) *stalemateState {
	return &stalemateState{cfg: cfg, log: log}
}

func (s *stalemateState) Update(before, after gitSnapshot) bool {
	if s.cfg.ReviewPatience <= 0 || before.head == "" {
		return false
	}
	if after.head == "" || after.diff == "" {
		return false
	}

	unchanged := after.head == before.head
	if before.diff != "" && after.diff != "" {
		unchanged = unchanged && after.diff == before.diff
	}
	if unchanged {
		s.unchangedRounds++
	} else {
		s.unchangedRounds = 0
	}

	if s.unchangedRounds >= s.cfg.ReviewPatience {
		s.log.Print("stalemate detected after %d unchanged rounds, external review terminated early", s.unchangedRounds)
		return true
	}
	return false
}
