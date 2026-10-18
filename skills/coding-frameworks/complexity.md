## Big-O cheatsheet

This is a factual reference; keep the numbers right.

### Reading complexity off code

- Sequential statements add: O(a) + O(b) → O(a + b), dominated by the larger.
- Nested loops over the same `n` multiply: two nested → O(n²), three → O(n³).
- Halving the search space each step → O(log n); halving inside a loop over n → O(n log n).
- Recursion: time ≈ (number of nodes in the call tree) × (work per node). A branching factor of 2 and depth `n` is O(2^n); divide-and-conquer that halves and does linear merge work (`T(n) = 2T(n/2) + O(n)`) is O(n log n).

### Standard operation costs

| Operation | Time |
|---|---|
| Comparison sort (Timsort, mergesort, heapsort) | O(n log n) |
| Hash insert / lookup / membership | O(1) average, O(n) worst case |
| Heap push / pop | O(log n) |
| `heapify` an existing list | O(n) |
| Binary search / balanced-BST insert & search | O(log n) |
| Array index access | O(1); unsorted search O(n) |
| `list.insert(0, x)` / `list.pop(0)` (shifts all) | O(n) |
| `deque.appendleft` / `popleft` | O(1) |

### Space accounting

- Distinguish **auxiliary** space (extra structures you allocate) from output space and from the input.
- The **recursion stack counts as space**: a recursion of depth `n` is O(n) space even if each frame is O(1) — an "in-place" recursive reversal is still O(n). An "O(1) space" claim must survive this test: iterative two-pointers is genuinely O(1); the recursive version is O(n).
- A size-k auxiliary structure (top-k heap, fixed window) is O(k) space regardless of n.

### Amortized analysis

- Dynamic-array append is **O(1) amortized**: most appends are O(1), the occasional resize copies all n elements (O(n)), but the cost averaged over a sequence of appends is constant.
- Union-find with path compression + union by rank is O(α(n)) amortized per operation — α (inverse Ackermann) is ≤ 4 for any practical n, so effectively O(1).
- Amortized ≠ average: amortized is a worst-case guarantee over a *sequence* of operations, not a probabilistic claim about one.

## Python idioms

Language-first guidance — the standard-library reach-for in an interview, with *when* to use each.

- **`collections.Counter`** — when you need a frequency map; `Counter(s)` tallies in one pass and `.most_common(k)` gives top-k. Reach for it any time you'd otherwise write `d[x] = d.get(x, 0) + 1`.
- **`collections.defaultdict`** — when building an adjacency list or grouping, so missing keys auto-initialize: `defaultdict(list)` for graphs, `defaultdict(int)` for counts. Avoids `KeyError` boilerplate.
- **`heapq`** — when you need a priority queue / top-k / streaming min. Note it's a **min-heap only**: push `-x` (or `(-priority, item)` tuples) to get max-heap behavior. `heapify(lst)` turns a list into a heap in O(n).
- **`bisect`** — when maintaining a sorted list with fast inserts/queries: `bisect_left(a, x)` finds the insertion point in O(log n); `insort(a, x)` inserts keeping order. Reach for it for "count elements < x" or sorted-stream problems.
- **`collections.deque`** — when you need a queue (BFS) or a sliding-window deque. Use it over a plain list whenever you pop from the front: `popleft()`/`appendleft()` are O(1), `list.pop(0)` is O(n).
- **Comprehensions** — when transforming/filtering a sequence: `[f(x) for x in xs if pred(x)]` is faster and clearer than an append loop. Set/dict comprehensions (`{x for x in xs}`) build sets/maps inline.
- **`enumerate` / `zip`** — `enumerate(xs)` when you need index and value together (no manual counter); `zip(a, b)` to walk two sequences in lockstep (pairwise comparisons, building dicts from key/value lists).
- **Dummy `ListNode` sentinels** — when building or splicing a linked list, start with `dummy = ListNode(0); tail = dummy` so the head needs no special case, then return `dummy.next`.
- **Tuple-unpacking swaps** — `a, b = b, a` to swap without a temp; `prev, cur = cur, nxt` to advance multiple pointers atomically (no clobbering). The idiomatic way to step linked-list and DP pointers.
- **`float('inf')` / `float('-inf')` sentinels** — when initializing a running min/max or an unreachable DP cell, so the first real value always wins the comparison without a special first-iteration case.
- **`set` for seen-tracking** — when you need O(1) membership: visited nodes in BFS/DFS, dedup, cycle detection. Reach for a set the moment you find yourself scanning a list to check "have I seen this".
