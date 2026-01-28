# Malformed Plan for Edge Case Testing

## Overview

This plan tests edge cases in plan parsing. It contains:
1. A valid h1 title (above)
2. No valid task headers
3. Checkboxes outside of any task (will be ignored)

## Non-Task Section

This section has checkboxes but no task header:

- [ ] This checkbox won't be parsed
- [x] Neither will this one

## Another Section

More content without task structure.

Some regular text here.

- Regular bullet point (not a checkbox)
- Another bullet point

## Notes

Since there are no `### Task N:` or `### Iteration N:` headers,
this plan will have an empty tasks array.
