# ralphex skills for pi

pi-adapted versions of the ralphex Claude skills. They give pi users the same
slash-command convenience that Claude Code users get from `assets/claude/skills/`.

These are independent of the `pi-as-claude` wrapper
(`scripts/pi-as-claude/pi-as-claude.sh`): the wrapper lets ralphex run *with* pi,
these skills let you drive ralphex *from inside* pi.

## Skills

| Skill | Invoke | Purpose |
|-------|--------|---------|
| `ralphex` | `/skill:ralphex [plan-file]` | Launch ralphex execution and monitor progress |
| `ralphex-plan` | `/skill:ralphex-plan <task>` | Create a structured plan in `docs/plans/` |
| `ralphex-update` | `/skill:ralphex-update` | Smart-merge updated defaults into customized config |
| `ralphex-adopt` | `/skill:ralphex-adopt <source>` | Convert a plan from another format into ralphex format |

## Installation

pi discovers skills from several locations. Copy the skill directories into one
of them:

```bash
# user-level (all projects)
mkdir -p ~/.pi/agent/skills
cp -r assets/pi/skills/* ~/.pi/agent/skills/

# project-level (this project only)
mkdir -p .pi/skills
cp -r assets/pi/skills/* .pi/skills/
```

Or point pi at a single skill directly for a one-off run:

```bash
pi --skill assets/pi/skills/ralphex-plan
```

## Usage

Invoke a skill as `/skill:<name> [args]`. pi appends any args after the name as
user input — there is no `$ARGUMENTS` placeholder in pi skills.

```
/skill:ralphex-plan add a health-check endpoint
/skill:ralphex docs/plans/20260607-feature.md
```

## Notes

These skills use pi's built-in tools (`read`, `bash`, `edit`, `write`). pi has no
parallel sub-agent surface, so exploration that Claude runs through `Task`
sub-agents is done inline with `read` + `bash` (`find`/`grep`), and user
questions are asked inline (pi is interactive) rather than through a structured
multi-choice tool.

## Testing

```bash
bash assets/pi/skills_test.sh
```
