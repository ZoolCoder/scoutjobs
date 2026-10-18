## Pattern taxonomy

One section per pattern: recognition cues → a Python template skeleton → complexity → classic problems → pitfalls. Pattern names match the `patterns:` frontmatter in `library/coding/` (e.g. `sliding-window`, `two-pointers`, `dynamic-programming`, `topological-sort`).

### Two-pointers

When you see a **sorted array** and need a pair/triple summing to a target, or you're partitioning/dedup in place, reach for two indices moving toward each other or in tandem.

```python
def two_sum_sorted(a, target):
    lo, hi = 0, len(a) - 1
    while lo < hi:
        s = a[lo] + a[hi]
        if s == target: return [lo, hi]
        lo, hi = (lo + 1, hi) if s < target else (lo, hi - 1)
    return []
```

Complexity: O(n) time, O(1) space. Classic: Two Sum II, 3Sum, Trapping Rain Water (`two-pointers`), Valid Palindrome. Pitfalls: only valid on sorted data (or after an O(n log n) sort, which may dominate); off-by-one at the `lo < hi` boundary; forgetting to skip duplicates in 3Sum.

### Sliding-window

When you see "longest/shortest/count of **contiguous** subarray/substring" under a constraint, grow a window with `right` and shrink from `left` when the invariant breaks.

```python
def longest_no_repeat(s):
    seen, left, best = {}, 0, 0
    for right, c in enumerate(s):
        if seen.get(c, -1) >= left: left = seen[c] + 1   # shrink past dup
        seen[c], best = right, max(best, right - left + 1)
    return best
```

Complexity: O(n) time, O(k) space (k = alphabet/window keys). Classic: Longest Substring Without Repeating Characters (`sliding-window`), Minimum Window Substring, Max Consecutive Ones III. Pitfalls: moving `left` past a stale index (guard `seen[c] >= left`); confusing fixed-size vs variable-size windows.

### Fast-and-slow-pointers

When you need cycle detection, the middle of a list, or the k-th-from-end node in a linked list (or a cycle in a functional sequence), advance two pointers at different speeds.

```python
def has_cycle(head):
    slow = fast = head
    while fast and fast.next:                     # guard before .next.next
        slow, fast = slow.next, fast.next.next
        if slow is fast: return True
    return False
```

Complexity: O(n) time, O(1) space. Classic: Linked List Cycle, Find Cycle Start (Floyd), Middle of the List, Happy Number. Pitfalls: dereferencing `fast.next` without the `fast and fast.next` guard; off-by-one on which pointer is the answer for "middle".

### Hashing / frequency-map

When you need O(1) membership, counting, or grouping-by-key, reach for a `set` or a `dict`/`Counter`. The default tool for "have I seen this" and "how many of each".

```python
from collections import Counter
def top_k_frequent(nums, k):
    return [v for v, _ in Counter(nums).most_common(k)]
```

Complexity: O(n) build; lookups O(1) average, O(n) worst (pathological collisions). Classic: Two Sum (`hashing`), Group Anagrams, Top K Frequent (`heap, hashing`). Pitfalls: assuming O(1) is worst-case; mutating a dict while iterating it; using a list where a set would make membership O(1).

### Prefix-sum

When you see repeated range-sum queries or "subarray summing to k", precompute cumulative sums so any range is one subtraction; pair with a hashmap for the "count subarrays" variant.

```python
def subarray_sum_equals_k(nums, k):
    count, run, seen = 0, 0, {0: 1}   # seed {0:1} for subarrays from index 0
    for x in nums:
        run += x
        count += seen.get(run - k, 0)
        seen[run] = seen.get(run, 0) + 1
    return count
```

Complexity: O(n) time, O(n) space. Classic: Subarray Sum Equals K, Range Sum Query Immutable, Pivot Index. Pitfalls: forgetting the `{0: 1}` seed (misses subarrays starting at index 0); off-by-one on inclusive vs exclusive prefix indices.

### Binary-search

When the search space is **monotonic** — sorted array, or a predicate that flips false→true exactly once — halve it each step. Includes *binary-search-on-the-answer*: when you can cheaply check "is value `x` feasible?" and feasibility is monotonic in `x`, binary-search over the answer range itself.

```python
def lower_bound(a, target):           # first index with a[i] >= target
    lo, hi = 0, len(a)
    while lo < hi:
        mid = (lo + hi) // 2
        if a[mid] < target: lo = mid + 1
        else: hi = mid
    return lo
```

Complexity: O(log n) per search. Classic: Search in Rotated Sorted Array (`binary-search`), Koko Eating Bananas (search-on-answer), Find Minimum in Rotated Array. Pitfalls: inconsistent interval convention (`[lo, hi]` vs `[lo, hi)`) causing infinite loops; wrong half kept on the rotated/duplicate case; computing `mid` after the loop instead of `lo`.

### Linked-list manipulation

When you splice, reverse, or merge nodes, use a **dummy/sentinel** head so the first node needs no special case, and rewire pointers carefully.

```python
def reverse_list(head):
    prev, cur = None, head
    while cur:
        cur.next, prev, cur = prev, cur, cur.next   # rewire, then advance both
    return prev
```

Complexity: O(n) time, O(1) space (iterative). Classic: Reverse Linked List (`linked-list`), Merge Two Sorted Lists, Remove Nth From End, Reorder List. Pitfalls: losing the rest of the list by reassigning `next` before saving it; returning `dummy` instead of `dummy.next`; recursion stack making "O(1) space" actually O(n).

### Stack & monotonic-stack

When you see matching/nesting (parentheses, undo) reach for a plain stack. When you need "next greater/smaller element" or to bound a histogram, use a **monotonic stack** that pops while the invariant breaks.

```python
def daily_temperatures(t):
    res, stack = [0] * len(t), []     # stack holds indices, temps decreasing
    for i, temp in enumerate(t):
        while stack and t[stack[-1]] < temp:
            j = stack.pop(); res[j] = i - j
        stack.append(i)
    return res
```

Complexity: O(n) time (each index pushed/popped once), O(n) space. Classic: Valid Parentheses (`stack`), Daily Temperatures, Largest Rectangle in Histogram, Trapping Rain Water (`monotonic-stack`). Pitfalls: pushing values when you need indices (or vice versa); wrong strict/non-strict comparison admitting equal elements incorrectly.

### BFS

When you need the **shortest path in an unweighted graph/grid** or level-order traversal, use a queue and expand frontier by frontier.

```python
from collections import deque
def bfs_shortest(start, goal, neighbors):
    q, seen = deque([(start, 0)]), {start}
    while q:
        node, d = q.popleft()
        if node == goal: return d
        for nxt in neighbors(node):
            if nxt not in seen:                   # mark at enqueue, not dequeue
                seen.add(nxt); q.append((nxt, d + 1))
    return -1
```

Complexity: O(V + E) time, O(V) space for the queue/visited set. Classic: Number of Islands (`bfs, dfs, grid`), Word Ladder, Rotting Oranges. Pitfalls: marking visited at dequeue instead of enqueue (nodes enqueued many times); using a list `.pop(0)` (O(n)) instead of `deque.popleft()` (O(1)).

### DFS

When you need to explore connected components, tree paths, or all reachable nodes (and shortest-path is irrelevant), recurse or use an explicit stack; mark visited to avoid revisiting.

```python
def num_islands(grid):
    R, C = len(grid), len(grid[0])
    def dfs(r, c):
        if not (0 <= r < R and 0 <= c < C) or grid[r][c] != '1': return
        grid[r][c] = '0'                          # mark visited in place
        for dr, dc in ((1,0),(-1,0),(0,1),(0,-1)): dfs(r + dr, c + dc)
    return sum(dfs(r, c) or 1 for r in range(R) for c in range(C) if grid[r][c] == '1')
```

Complexity: O(V + E) time; O(V) recursion-stack space (grid depth up to `rows*cols`). Classic: Number of Islands (`dfs`), Clone Graph, Course Schedule cycle-check. Pitfalls: missing the visited mark → infinite recursion on cycles; recursion depth exceeding Python's ~1000 default limit on large grids.

### Backtracking

When you must enumerate all combinations/permutations/subsets or fill a board under constraints, build a partial solution, recurse, and **undo the choice** on the way back up.

```python
def subsets(nums):
    res = []
    def bt(start, path):
        res.append(path[:])                       # record (copy, don't alias)
        for i in range(start, len(nums)):
            path.append(nums[i])                  # choose
            bt(i + 1, path)
            path.pop()                            # undo
    bt(0, [])
    return res
```

Complexity: exponential — O(2^n) subsets, O(n!) permutations — times O(n) to copy each. Classic: Subsets, Permutations, Word Search (`backtracking, grid`), N-Queens, Combination Sum. Pitfalls: appending `path` instead of `path[:]` (all results alias one list); forgetting the `path.pop()` undo; no pruning so feasible branches aren't cut early.

### Dynamic-programming

When the problem has **overlapping subproblems and optimal substructure** — "count ways", "min/max cost", "longest/shortest" with choices — define a state and a transition, then fill a table (or memoize). 1-D state (one axis) vs 2-D/grid state (two axes — strings, grids, knapsack capacity):

```python
def coin_change(coins, amount):                  # 1-D: dp[a] = fewest coins for a
    dp = [0] + [float('inf')] * amount
    for a in range(1, amount + 1):
        dp[a] = min((dp[a - c] + 1 for c in coins if c <= a), default=float('inf'))
    return dp[amount] if dp[amount] != float('inf') else -1

def unique_paths(m, n):                           # 2-D: dp[r][c] from top + from left
    dp = [[1] * n for _ in range(m)]
    for r in range(1, m):
        for c in range(1, n): dp[r][c] = dp[r-1][c] + dp[r][c-1]
    return dp[-1][-1]
```

Complexity: typically O(states × transition) — coin change O(amount × coins); grid DP O(m × n). Classic: Coin Change (`dynamic-programming`), Longest Common Subsequence, Edit Distance, 0/1 Knapsack, House Robber. Pitfalls: wrong base case / off-by-one on table size; iterating dimensions in an order that reads a not-yet-computed cell; missing that O(n) space suffices when only the previous row is needed.

### Greedy

When a **locally optimal choice provably leads to the global optimum** (and you can argue why), sort or scan once and commit to the best step each time — no backtracking.

```python
def jump_game(nums):                 # can you reach the last index?
    reach = 0
    for i, n in enumerate(nums):
        if i > reach: return False
        reach = max(reach, i + n)
    return True
```

Complexity: often O(n) or O(n log n) with a sort. Classic: Jump Game, Activity Selection / Non-overlapping Intervals, Gas Station, Huffman coding. Pitfalls: assuming greedy works without an exchange-argument proof (many problems need DP instead); choosing the wrong sort key.

### Heap / top-k

When you need the k largest/smallest, a running median, or repeated "min/max so far" with insertions, use a heap. For top-k, keep a **size-k heap** rather than sorting everything.

```python
import heapq
def k_largest(nums, k):
    return heapq.nlargest(k, nums)   # or maintain a size-k min-heap manually
```

Complexity: push/pop O(log n); building a size-k heap over n items is O(n log k); `heapify` a list is O(n). Classic: Kth Largest Element (`heap, quickselect`), Top K Frequent (`heap, hashing`), Merge K Sorted Lists, Find Median From Data Stream. Pitfalls: `heapq` is a **min-heap only** — negate values for a max-heap; sorting fully (O(n log n)) when a size-k heap (O(n log k)) suffices.

### Intervals (merge-and-sweep)

When you see overlapping ranges — merging, inserting, counting concurrent events — **sort by start**, then sweep, comparing each interval to the last kept one.

```python
def merge(intervals):
    intervals.sort(key=lambda x: x[0])            # sort by start
    out = [intervals[0]]
    for s, e in intervals[1:]:
        if s <= out[-1][1]: out[-1][1] = max(out[-1][1], e)   # overlap → extend
        else: out.append([s, e])
    return out
```

Complexity: O(n log n) for the sort, O(n) sweep. Classic: Merge Intervals (`intervals, sorting`), Insert Interval, Meeting Rooms II (min rooms via a sweep / min-heap of end times). Pitfalls: sorting by end when you needed start (or vice versa); wrong overlap test (`<` vs `<=` decides whether touching intervals merge).

### Graph: topological sort (Kahn) and union-find

When you order tasks under dependencies (a DAG), use **Kahn's algorithm**: repeatedly emit zero-in-degree nodes. When you query/merge connected components incrementally, use **union-find** (disjoint-set) with path compression + union by rank.

```python
from collections import deque
def topo_sort(n, edges):                          # edges as (u -> v) pairs
    indeg, adj = [0] * n, [[] for _ in range(n)]
    for u, v in edges: adj[u].append(v); indeg[v] += 1
    q, order = deque(i for i in range(n) if not indeg[i]), []
    while q:
        u = q.popleft(); order.append(u)
        for v in adj[u]:
            indeg[v] -= 1
            if not indeg[v]: q.append(v)
    return order if len(order) == n else []       # [] means a cycle exists
```

Complexity: Kahn O(V + E); union-find near O(α(n)) ≈ O(1) amortized per op with both optimizations. Classic: Course Schedule (`topological-sort, graph`), Alien Dictionary, Number of Connected Components, Redundant Connection. Pitfalls: not detecting a cycle (`len(order) < n`); union-find without path compression degrading to O(n) chains; confusing edge direction.

### Trie

When you do many **prefix** queries over a set of strings — autocomplete, word-dictionary search, prefix counting — build a trie of character-keyed children.

```python
class Trie:                                       # node = dict of char -> child; '$' ends a word
    def __init__(self): self.root = {}
    def insert(self, word):
        node = self.root
        for ch in word: node = node.setdefault(ch, {})
        node['$'] = True
    def starts_with(self, prefix):                # full-word search: also check '$' in node
        node = self.root
        for ch in prefix:
            if ch not in node: return False
            node = node[ch]
        return True
```

Complexity: insert/search O(L) for word length L; space O(total characters). Classic: Implement Trie, Word Search II (trie + backtracking), Replace Words, Design Add and Search Words. Pitfalls: forgetting the end-of-word marker (so `"app"` matches when only `"apple"` was inserted); reusing children dicts across nodes.

### Bit-manipulation

When you toggle/test flags, build subset bitmasks, or exploit XOR's self-cancelling property, work directly on bits.

```python
def single_number(nums):             # every element appears twice but one
    x = 0
    for n in nums:
        x ^= n                       # pairs cancel; the loner remains
    return x
```

Complexity: O(n) time, O(1) space here; bitmask-DP is O(2^n × n). Classic: Single Number, Number of 1 Bits, Counting Bits, Subsets (via bitmask), Sum of Two Integers. Pitfalls: Python ints are arbitrary-precision so there's no fixed-width overflow — mask with `& 0xFFFFFFFF` when emulating 32-bit; confusing `&`/`|`/`^`; operator precedence (bitwise binds looser than comparison — parenthesize).
