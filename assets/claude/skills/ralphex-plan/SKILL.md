---
description: Create structured implementation plan in docs/plans/
---

# Implementation Plan Creation

Create an implementation plan in `docs/plans/yyyymmdd-<task-name>.md` with interactive context gathering.

## Prerequisites: Verify CLI Installation

Check if ralphex CLI is installed (needed to execute the plan after creation):
```bash
which ralphex
```

**If not found**, inform user they'll need it to execute the plan:
- **macOS (Homebrew)**: `brew install umputun/apps/ralphex`
- **Linux (Debian/Ubuntu)**: download `.deb` from https://github.com/umputun/ralphex/releases
- **Linux (RHEL/Fedora)**: download `.rpm` from https://github.com/umputun/ralphex/releases
- **Any platform with Go**: `go install github.com/umputun/ralphex/cmd/ralphex@latest`

Proceed with plan creation regardless, but remind user to install before execution.

## Step 0: Parse Intent and Gather Context

Before asking questions, understand what the user is working on:

1. **Parse user's command arguments** to identify intent:
   - "add feature Z" / "implement W" → feature development
   - "fix bug" / "debug issue" → bug fix plan
   - "refactor X" / "improve Y" → refactoring plan
   - "migrate to Z" / "upgrade W" → migration plan
   - generic request → explore current work

2. **Launch Explore agent** (via Task tool with `subagent_type: Explore`) to gather relevant context based on intent:

   **For feature development:**
   - locate related existing code and patterns
   - check project structure (README, config files, existing similar implementations)
   - identify affected components and dependencies

   **For bug fixing:**
   - look for error logs, test failures, or stack traces
   - find related code that might be involved
   - check recent git changes in problem areas

   **For refactoring/migration:**
   - identify all files/components affected
   - check test coverage of affected areas
   - find dependencies and integration points

   **For generic/unclear requests:**
   - check `git status` and recent file activity
   - examine current working directory structure
   - identify primary language/framework from file extensions and config files

3. **Synthesize findings** into context summary:
   - what work is in progress
   - which files/areas are involved
   - what the apparent goal is
   - relevant patterns or structure discovered

## Step 1: Present Context and Ask Focused Questions

Show the discovered context, then ask questions **one at a time** using the AskUserQuestion tool:

"Based on your request, I found: [context summary]"

**Ask questions one at a time (do not overwhelm with multiple questions):**

1. **Plan purpose**: use AskUserQuestion - "What is the main goal?"
   - provide multiple choice with suggested answer based on discovered intent
   - wait for response before next question

2. **Scope**: use AskUserQuestion - "Which components/files are involved?"
   - provide multiple choice with suggested discovered files/areas
   - wait for response before next question

3. **Constraints**: use AskUserQuestion - "Any specific requirements or limitations?"
   - can be open-ended if constraints vary widely
   - wait for response before next question

4. **Testing approach**: use AskUserQuestion - "Do you prefer TDD or regular approach?"
   - options: "TDD (tests first)" and "Regular (code first, then tests)"
   - store preference for reference during implementation
   - wait for response before next question

5. **Plan title**: use AskUserQuestion - "Short descriptive title?"
   - provide suggested name based on intent

After all questions answered, synthesize responses into plan context.

## Step 1.5: Explore Approaches

Once the problem is understood, propose implementation approaches:

1. **Propose 2-3 different approaches** with trade-offs for each
2. **Lead with recommended option** and explain reasoning
3. **Present conversationally** - not a formal document yet

Example format:
```
I see three approaches:

**Option A: [name]** (recommended)
- How it works: ...
- Pros: ...
- Cons: ...

**Option B: [name]**
- How it works: ...
- Pros: ...
- Cons: ...

Which direction appeals to you?
```

Use AskUserQuestion tool to let user select preferred approach before creating the plan.

**Skip this step** if:
- the implementation approach is obvious (single clear path)
- user explicitly specified how they want it done
- it's a bug fix with clear solution

## Step 2: Create Plan File

Check `docs/plans/` for existing files, then create `docs/plans/<task-name>.md`:

### Plan Structure

```markdown
# [Plan Title]

## Overview
- Clear description of the feature/change being implemented
- Problem it solves and key benefits
- How it integrates with existing system

## Context (from discovery)
- Files/components involved: [list from step 0]
- Related patterns found: [patterns discovered]
- Dependencies identified: [dependencies]

## Development Approach
- **Testing approach**: [TDD / Regular - from user preference in planning]
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility

## Testing Strategy
- **Unit tests**: required for every task (see Development Approach above)
- **E2E tests**: if project has UI-based e2e tests (Playwright, Cypress, etc.):
  - UI changes → add/update e2e tests in same task as UI code
  - Backend changes supporting UI → add/update e2e tests in same task
  - Treat e2e tests with same rigor as unit tests (must pass before next task)
  - Store e2e tests alongside unit tests (or in designated e2e directory)

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope
- Keep plan in sync with actual work done

## What Goes Where
- **Implementation Steps** (`[ ]` checkboxes): tasks achievable within this codebase - code changes, tests, documentation updates
- **Post-Completion** (no checkboxes): items requiring external action - manual testing, changes in consuming projects, deployment configs, third-party verifications

## Implementation Steps

<!--
Task structure guidelines:
- Each task = ONE logical unit (one function, one endpoint, one component)
- Use specific descriptive names, not generic "[Core Logic]" or "[Implementation]"
- Aim for ~5 checkboxes per task (more is OK if logically atomic)
- **CRITICAL: Each task MUST end with writing/updating tests before moving to next**
  - tests are not optional - they are a required deliverable of every task
  - write tests for all NEW code added in this task
  - write tests for all MODIFIED code in this task
  - include both success and error scenarios in tests
  - list tests as SEPARATE checklist items, not bundled with implementation

Example (NOTICE: tests are separate checklist items):

### Task 1: Add password hashing utility
- [ ] create `auth/hash` module with HashPassword and VerifyPassword functions
- [ ] implement secure hashing with configurable cost
- [ ] write tests for HashPassword (success + error cases)
- [ ] write tests for VerifyPassword (success + error cases)
- [ ] run project tests - must pass before task 2

### Task 2: Add user registration endpoint
- [ ] create `POST /api/users` handler
- [ ] add input validation (email format, password strength)
- [ ] integrate with password hashing utility
- [ ] write tests for handler success case with table-driven cases
- [ ] write tests for handler error cases (invalid input, missing fields)
- [ ] run project tests - must pass before task 3
-->

### Task 1: [specific name - what this task accomplishes]
- [ ] [specific action with file reference - code implementation]
- [ ] [specific action with file reference - code implementation]
- [ ] write tests for new/changed functionality (success cases)
- [ ] write tests for error/edge cases
- [ ] run tests - must pass before next task

### Task N-1: Verify acceptance criteria
- [ ] verify all requirements from Overview are implemented
- [ ] verify edge cases are handled
- [ ] run full test suite (unit tests)
- [ ] run e2e tests if project has them
- [ ] run linter - all issues must be fixed
- [ ] verify test coverage meets project standard (80%+)

### Task N: [Final] Update documentation
- [ ] update README.md if needed
- [ ] update project knowledge docs if new patterns discovered

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`*

## Technical Details
- Data structures and changes
- Parameters and formats
- Processing flow

## Post-Completion
*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Manual verification** (if applicable):
- Manual UI/UX testing scenarios
- Performance testing under load
- Security review considerations

**External system updates** (if applicable):
- Consuming projects that need updates after this library change
- Configuration changes in deployment systems
- Third-party service integrations to verify
```

## Step 3: Offer to Start

After creating the file, tell user:

"Created plan: `docs/plans/<task-name>.md`

Ready to start implementation?"

If yes, begin with task 1.

## Execution Enforcement

**CRITICAL testing rules during implementation:**

1. **After completing code changes in a task**:
   - STOP before moving to next task
   - Add tests for all new functionality
   - Update tests for modified functionality
   - Run project test command
   - Mark completed items with `[x]` in plan file
   - **Use TodoWrite tool to track progress and mark todos completed immediately (do not batch)**

2. **If tests fail**:
   - Fix the failures before proceeding
   - Do NOT move to next task with failing tests
   - Do NOT skip test writing

3. **Only proceed to next task when**:
   - All task items completed and marked `[x]`
   - Tests written/updated
   - All tests passing

4. **Plan tracking during implementation**:
   - Update checkboxes immediately when tasks complete
   - Add ➕ prefix for newly discovered tasks
   - Add ⚠️ prefix for blockers
   - Modify plan if scope changes significantly

5. **On completion**:
   - Verify all checkboxes marked
   - Run final test suite
   - *ralphex automatically moves plan to `docs/plans/completed/`*

6. **Partial implementation exception**:
   - If a task provides partial implementation where tests cannot pass until a later task:
     - Still write the tests as part of this task (required)
     - Add TODO comment in test code noting the dependency
     - Mark the test checkbox as completed with note: `[x] write tests ... (fails until Task X)`
     - Do NOT skip test writing or defer until later
   - When the dependent task completes, remove the TODO comment and verify tests pass

This ensures each task is solid before building on top of it.

## Key Principles

- **One question at a time** - do not overwhelm user with multiple questions in a single message
- **Multiple choice preferred** - easier to answer than open-ended when possible
- **YAGNI ruthlessly** - remove unnecessary features from all designs, keep scope minimal
- **Lead with recommendation** - have an opinion, explain why, but let user decide
- **Explore alternatives** - always propose 2-3 approaches before settling (unless obvious)
- **Duplication vs abstraction** - when code repeats, ask user: prefer duplication (simpler, no coupling) or abstraction (DRY but adds complexity)? explain trade-offs before deciding
