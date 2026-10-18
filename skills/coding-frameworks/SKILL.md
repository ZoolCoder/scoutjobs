---
name: coding-frameworks
description: Reference frameworks for coding interviews — the UMPIRE method, a pattern taxonomy (two-pointers, sliding-window, BFS/DFS, backtracking, dynamic-programming, graphs, and more), a Big-O cheatsheet, Python idioms, communication guidance, and anti-patterns. Used by the coding commands (/coding-explain, /practice-coding, /mock-coding, /coding-drill, /debrief-coding) to ground teaching and scoring; not read aloud verbatim.
license: MIT
metadata:
  source: https://github.com/kirilxd/swe-interview-coach
  vendored-by: scoutjobs
---

# Coding Frameworks

## UMPIRE method

The default scaffold for a 35-45 minute algorithmic question — the coding analog of RESHADED. Six phases, run top to bottom: **U**nderstand, **M**atch, **P**lan, **I**mplement, **R**eview, **E**valuate. The signal an interviewer scores is the process, not just a passing solution — narrate every phase transition out loud. The single most common failure is jumping straight to Implement; the first three phases are where senior signal lives.

### Understand

- Restate the problem in your own words and get the interviewer to confirm before touching the keyboard.
- Ask the constraint questions that change the algorithm: input size (`n` up to 10? 10^5? 10^9?), value ranges (negatives? overflow?), duplicates, empty/single-element input, sorted-ness, and the return shape (index vs value, in-place vs new, all answers vs one). Then work one tiny example by hand so you and the interviewer share the same notion of "correct".

**Weak:** "Two-sum, got it — I'll loop and check pairs." (Never asked sorted-ness, duplicates, or indices-vs-values.)
**Strong:** "Restating: return the *indices* of the two numbers summing to target. Exactly one solution? Negatives or duplicates? Is `n` up to 10^5? Can I reuse an element? Good — indices, one solution, unsorted, no reuse."

### Match

- Name the pattern this problem resembles before designing anything: "pair summing to a target in a sorted array → two-pointers"; "longest window under a constraint → sliding-window". Recognition is mostly pattern-matching on input shape and asked-for output — say the candidate pattern aloud; being wrong out loud is cheap and invites a hint.

**Weak:** Silently starts writing nested loops with no name for what they're doing.
**Strong:** "This smells like sliding-window: contiguous subarray, longest-under-a-constraint. If the window invariant doesn't hold I'll fall back to prefix-sum + hashmap."

### Plan

- State the approach AND its time/space complexity BEFORE writing code. This is the phase candidates skip and lose the most points on.
- Start from the brute force, name its cost, then state the optimization and its cost — "brute force is O(n²); with a hashmap I get O(n) time, O(n) space". Sketch the data structures and the core loop in words; get a nod before implementing.

**Weak:** Starts typing immediately and discovers the approach is O(n²) only when the interviewer asks about scale.
**Strong:** "Plan: one pass, a hashmap of value → index; for each `x` check if `target - x` was seen. O(n) time, O(n) space. Brute force is O(n²); the hashmap trades space for the inner loop."

### Implement

- Write the plan you just stated — no improvising a different algorithm mid-stream.
- Code in small testable chunks, narrating as you go; use clear names and handle the edge cases you surfaced in Understand as you reach them. If you get stuck, say what you're stuck on rather than going silent — a voiced partial idea is recoverable signal.

**Weak:** Codes in silence for five minutes, then presents a wall of code that doesn't compile.
**Strong:** "I'll set up the hashmap, then the single pass — lookup before insert so I don't match an element with itself…" (typing, narrating, names readable).

### Review

- Dry-run the code by hand on a small input — trace variables line by line, out loud. Don't claim it works; show it.
- Then run it on at least one edge case you identified earlier (empty, single element, duplicates, all-negative, cycle/overflow), and fix bugs you find by reasoning — not by randomly mutating lines until the example passes.

**Weak:** "I think that's right." (No trace, no edge case.)
**Strong:** "Trace `[2,7,11], target 9`: i=0 store 2→0; i=1 need 2, hit → return [0,1]. Edge case `[]` → loop never runs, returns []. Correct."

### Evaluate

- State the final time and space complexity explicitly, separating auxiliary space from output and recursion-stack space. Then name the next optimization or the tradeoff you'd revisit with more time, even if you wouldn't implement it ("O(n) time now; if memory were tight, two-pointers on a sorted copy gives O(1) extra space at O(n log n) time").

**Weak:** "Done." (No complexity, no next step.)
**Strong:** "Final: O(n) time, O(n) space for the map. If the input were pre-sorted, two-pointers gives O(1) extra space at O(n) time. The space-for-time trade is worth it on unsorted input."

## Pattern taxonomy

One section per pattern (recognition cues, Python template, complexity, classic problems, pitfalls) lives in `patterns.md` next to this file. Read only the section of the pattern at hand, never the whole file.

## Big-O and Python idioms

The Big-O cheatsheet (reading complexity off code, standard operation costs, space, amortized analysis) and the Python idioms live in `complexity.md` next to this file. Read it when scoring complexity or reviewing Python code.

## Communication

The transcript is scored, not just the final code — narrate throughout.

- **State approach and complexity before coding.** Say the algorithm and its time/space in the Plan phase; never let the interviewer discover the approach by reading your code.
- **Narrate tradeoffs as you choose.** "I'll use a hashmap — that's O(n) space but turns the inner loop into an O(1) lookup" beats silently picking a structure.
- **Test out loud.** In Review, dry-run a concrete input and an edge case aloud, tracing variables — demonstrate correctness instead of asserting it.
- **Announce final complexity.** Close every solution by stating its time and space and naming the next optimization, so the interviewer hears the loop close.
- When stuck, **think out loud** — voice the partial idea and the obstacle; a hint usually follows. Silence reads as being lost.

## Anti-patterns

- **Coding before clarifying constraints** — typing before asking about input size, value ranges, duplicates, empties, and return shape; the algorithm often hinges on an unasked question.
- **Never stating complexity** — solving without ever saying the time/space cost, so the interviewer can't tell you understand what you wrote.
- **Never testing** — declaring "done" with no dry-run; bugs that a 30-second trace would catch sink the solution.
- **Premature optimization** — reaching for a clever O(n) trick before a correct brute force exists; a working O(n²) you can optimize beats a broken O(n) you can't finish.
- **Silent debugging** — mutating lines at random until the example passes, instead of reasoning about why it's wrong; the interviewer can't follow and you usually re-break it.
- **Unacknowledged brute force** — presenting the naive solution as if it were the intended one, with no "this is my O(n²) baseline; here's how I'd improve it".
- **Neglecting edge cases** — no handling for empty, single-element, duplicates, negatives, integer overflow, or cycles; these are the inputs interviewers test first.
- **Freezing silently** — going quiet when stuck instead of voicing a partial approach and the specific obstacle; an interviewer can hint on a spoken idea but not on silence.
