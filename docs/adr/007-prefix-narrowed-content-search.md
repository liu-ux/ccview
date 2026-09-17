# ADR-007: Prefix-Narrowed Content Search Behind an Enter Gate

## Status

Accepted

## Context

The session search overlay had two modes. Title search scans the loaded tree in memory. Content search reads every conversation on disk — `rg -l` (a process spawn costing ~190ms on Windows) plus a scan of the OpenCode `part` table (~20–36ms on a 51MB database) — and both ran on a 250ms debounce after every keystroke.

Typing therefore paid a disk search per pause, and the overlay flickered through `Searching...` on the way. The natural fix is to reuse the previous search: if the query only grew, the previous match set must contain everything the longer query can match, so the new answer can be derived locally.

Three properties had to be established before that reuse could be trusted, and none of them held:

1. **Truncation.** OpenCode's SQL capped at 50 rows *inside* the query, ordered by recency, and the merged result was capped again in Go. Narrowing a truncated set silently loses matches — a result that matched `ab` but ranked 60th for `a` would vanish, with nothing on screen to say so.
2. **No text to narrow with.** A result list says *which* conversations matched, not *where*. Testing whether a conversation contains a longer query needs its text, so the cache has to hold text, not just paths.
3. **Three different matching rules.** `rg` and `grep` took the query as a regex, the pure-Go fallback matched it case-insensitively, and OpenCode's `LIKE` was case-insensitive with `%` and `_` as wildcards. Searching `(` only returned literal results by failing over to a different matcher. A cache built under one rule cannot answer a query judged by another.

## Decision

Content search is gated behind explicit actions, and each search keeps a bounded window of matched text for later narrowing.

### The gate

Typing never searches. `Enter` searches, as do explicit scope (`tab`) and mode (`alt+c`) changes — the rule is "explicit actions search, typing narrows". The initial search was gated to stop the flicker, and that flicker comes from typing, not from a deliberate keypress.

`Enter` is therefore dual-purpose: it searches while the query is unsearched, and opens the selected result afterwards. The overlay states which one it will do (`enter:search` / `enter:open`, plus `enter to search again` next to the input), and it no longer claims `No matches found` for a query that has not been run.

### The cache

`ContentMatch.Windows` holds, per matching line, the text from that line's **first** match to `contentMatchWindowRunes` (50) runes past its **last** one, bounded by the line end. 50 runes covers every realistic query growth; runes rather than bytes, because CJK text is three bytes a character.

Narrowing tests each window for an occurrence of the base query followed by the extension. This is exact within those 50 runes: every occurrence in the line lies inside its window, so the test on windows is the test on the file. Beyond 50 runes, or without windows, the UI falls back to asking for `Enter`.

Two invariants make it sound, and both are load-bearing:

- **Every occurrence must be inside its window.** A line like `ac ab` must match base `a` extended to `ab` from its second occurrence. Windows that start at the first match and stop 50 runes later would miss it.
- **All or nothing under budget.** Past `searchCacheBudgetBytes` (32MB default, `--search-cache-mb`, `CCVIEW_SEARCH_CACHE_MB`) the entire window set is dropped and narrowing is disabled. Caching what fits would leave an incomplete window set, and an incomplete window set silently loses matches — the one failure mode worth paying for to avoid.

Measured on the developer's corpus: a 2-character query costs ~0.8MB of windows, a 1-character query ~42MB. So the budget only bites on single-character queries, precisely where a full search is cheap to ask for again.

### Display cap moves to the caller

Providers return complete results. The 50-result cap lives in `setSearchResults()`, which keeps the complete set in `allResults` for narrowing and the capped prefix in `results` for display and navigation.

### One matching rule

The query is literal and case-sensitive everywhere: `rg -F`, `grep -F`, `strings.Contains`, and SQLite `instr()` in place of `LIKE`. This is a deliberate narrowing of behaviour — content search no longer accepts regexes. It never advertised them, the title search and the in-viewer `/` search were already literal, and it fixes `(` failing over to an entirely different matcher and `50%` acting as a wildcard.

## Consequences

### Positive

- A search costs one I/O pass per explicit action instead of one per keystroke pause, and narrowing is a memory scan (<1ms).
- The result list stops flickering and no longer flashes `Searching...` while it is being narrowed.
- Truncation-driven silent misses are gone from the design: results are complete, and the display cap can no longer hide a match from a narrowed query.
- `%`, `.`, `(` and case now behave the same on every backend.

### Negative

- Two search modes in one overlay behave differently: title search filters live, content search waits for `Enter`. It is a principled split (the free one stays live), but it is still two behaviours to learn.
- Content search lost regex support, and there was no way to announce that beyond the release notes.
- The overlay gained a third state — "these results are valid but incomplete, because the query is shorter than the one searched" — carried by one grey line of text.
- A cache of up to 32MB of matched text lives as long as the overlay is open (1.5–2.5× that in RSS); it is released when the overlay is rebuilt.
- Narrowing is capped at 50 characters of growth, which is invisible until a long query is typed and must be re-searched.

### Not done

- **Invalidating on disk changes.** The cache is a snapshot of the moment `Enter` was pressed; a conversation another agent writes afterwards will not appear until the next search. Watching the filesystem was rejected as disproportionate for a search overlay, and a time-based invalidation would reintroduce exactly the unrequested I/O the gate exists to avoid.
- **Narrowing the result set in the provider** (passing candidate files to `rg`, or session ids into the SQL). It is sound but buys almost nothing where the cost is a process spawn, and it would put the cache's invariants in four places instead of one.
- **Matching a regex query while narrowing only literal expansions.** Keeping two matchers in one input — where the same text means different things depending on whether the search has been committed — was worse than dropping regexes.
