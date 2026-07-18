# ADR-002: Direct Project Selection via CLI Flag

## Status

Accepted

## Context

Users who work primarily in one project currently must: launch ccview → navigate the project list → find their project → press Enter. For frequent use, this is tedious. A `-project` flag lets them jump directly to a known project.

## Decision

Add a `-project` flag that accepts a project identifier (directory name or partial match). When specified:

1. Level 0 loads as normal (project list appears).
2. The model stores `directProject` alongside the existing `directFile`.
3. Once Level 0 completes and the project list is populated, the model auto-opens the matching project — triggering Level 2 load and showing the sidebar immediately.
4. If no match is found, an error message is shown and the user falls back to the normal project list.

### Matching rules (in priority order)

1. Exact `DirName` match
2. Exact `DisplayName` match
3. Case-insensitive substring match on `DirName`

### Interaction with `-file`

`-project` and `-file` are independent. `-file` skips providers entirely (existing behavior). `-project` uses providers but auto-navigates.

## Consequences

### Positive

- One command to jump to a project: `ccview -project my-app`
- Works with both full names and partial matches
- Falls back gracefully to the project list on no match

### Negative

- Ambiguous matches (multiple substring hits) use the first match — user may get the wrong project. Mitigated by accepting exact names.
