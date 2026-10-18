---
name: system-design-frameworks
description: Reference frameworks (RESHADED, the 4S shorthand), capacity estimation cheatsheet, core building blocks (load balancing, caching, queues, sharding, replication, CAP), common tradeoffs, and anti-patterns for system design interview prep. Use when running /mock-sysdesign, /practice-sysdesign, /sysdesign-explain, or /debrief-sysdesign, or whenever discussing architecture, capacity estimation, or design tradeoffs.
license: MIT
metadata:
  source: https://github.com/kirilxd/swe-interview-coach
  vendored-by: scoutjobs
---

# System Design Frameworks

## RESHADED framework

Default structure for a 45-60 minute open-ended design question. Budget roughly 10 minutes for Requirements + Estimation, 20-25 for Storage through Detailed design, 10 for Evaluation + wrap-up. Announce each stage transition out loud — interviewers score process, not just the final diagram.

### Requirements

- Split functional (what the system does) from non-functional (latency, availability, consistency, durability targets).
- Ask scoping questions before drawing anything: who uses it, what ships in v1, read-heavy or write-heavy?
- Attach numbers to non-functional requirements: "redirect p99 < 100 ms", "99.9% available" — never "fast and reliable".
- Restate the agreed scope and get explicit interviewer sign-off before moving on.

**Weak:** "A URL shortener — okay. I'll hash the URL, store the mapping in a database, and redirect on lookup."

**Strong:** "Scope check first: shorten and redirect are v1; custom aliases and analytics are out. I'll assume ~100:1 read:write. Non-functional targets: redirect p99 < 100 ms, 99.9% availability, and no mapping may ever be lost — durability beats freshness here."

### Estimation

- Produce three numbers minimum: QPS (read and write), storage growth, bandwidth — each rounded to one significant figure.
- Derive them from stated assumptions (DAU, actions per user per day) so the interviewer can correct your inputs, not your math.
- Use the shortcuts: 86,400 s/day, 1M requests/day ≈ 12 RPS, ~2.5M s/month.
- State peak vs average (peak ≈ 2-3× average) and size for peak.
- Don't skip bandwidth (RPS × object size) — for media-heavy systems it shapes the design more than request count does.

**Weak:** "It'll be a lot of traffic, so we'll need to scale horizontally from day one."

**Strong:** "100M DAU × 10 timeline reads/day = 1B reads/day ≈ 12K RPS average, ~30K peak. Writes: 100M posts/day ≈ 1.2K RPS. At ~1 KB per post that's ~100 GB/day of new data, ~36 TB/year before replication."

### Storage

- Sketch the data model before naming a technology: core entities, key fields, relationships, and the access patterns against each.
- Let volume plus access pattern drive the store choice — relational joins and transactions vs key-based access at horizontal-write scale.
- Mark which data needs transactions and which tolerates eventual consistency.
- Plan growth: hot vs cold data, retention, archival tier.

**Weak:** "I'll put everything in MongoDB because it scales well."

**Strong:** "Users and follow-edges are relational and join-heavy — Postgres. Posts are append-heavy at 100M/day with key-based reads — a wide-column store keyed by user_id, time-bucketed so no partition grows unbounded."

### High-level design

- Draw the end-to-end path first: client → load balancer → services → cache/DB → async consumers. 5-8 boxes at this altitude.
- Give every box one responsibility you can state in a sentence.
- Trace one read and one write through the diagram aloud.
- Flag deep dives and defer them: "I'll come back to the fan-out service."

**Weak:** "We'll have Kubernetes, Kafka, Redis, Cassandra, and Elasticsearch." (A shopping list, not a design.)

**Strong:** "Write path: client → L7 LB → post service → queue → fan-out workers → followers' timeline caches. Read path: timeline service reads the cache, falls back to the post store on miss. Fan-out is the hard part — I'll detail it later."

### API design

- Define the 3-5 core endpoints: method, path, key params, response shape, error cases.
- Make mutations idempotent — client-generated idempotency keys make retries safe.
- Paginate every list endpoint; prefer cursor over offset at scale.
- Note where authn/authz and rate limiting sit (usually the gateway).

**Weak:** "There'll be a REST API for posting and for reading timelines."

**Strong:** "POST /v1/posts with a client idempotency key returns 201 + post_id; retries are no-ops. GET /v1/timeline?cursor=…&limit=50 is cursor-paginated — offset pagination degrades on deep pages and shifts under concurrent inserts."

### Detailed design

- Zoom into the 1-2 components the requirements make hardest; invite the interviewer to pick.
- Specify the concrete choices: partition key, cache pattern and eviction, queue delivery semantics, replication mode — each with a reason.
- Quantify each choice against your estimation numbers.
- State the consistency model per path, not for the system as a whole.

**Weak:** "The cache layer will make reads fast."

**Strong:** "Timeline cache: Redis, cache-aside, LRU eviction, 1-hour TTL, keyed by user_id, holding the latest 800 post IDs. At 12K read RPS and a 90% hit rate the post store sees ~1.2K RPS — comfortable for one primary with replicas."

### Evaluation & edge cases

- Walk back through each non-functional requirement and show it is met — or say plainly where it is not.
- Probe failure modes: cache tier down, DB primary fails over, queue backlog, region loss. What degrades vs what breaks?
- Cover the hot-key/celebrity case and the thundering-herd case explicitly.
- Name the first bottleneck at 10× traffic and the lever you'd pull.

**Weak:** "I think this design covers everything we discussed."

**Strong:** "If the cache tier dies we'd thundering-herd the post store, so: request coalescing plus serve-stale-on-error. At 10× the bottleneck is fan-out for >1M-follower accounts — those switch to fan-out-on-read."

### Done

*(Canonical RESHADED calls this final D "Distinctive component"; this adaptation folds that into Detailed design and uses Done for the wrap-up.)*

- Close with a 60-second recap: the design in one breath, the top 2-3 tradeoffs you made, and what you'd tackle next with more time.
- Map the recap back to the requirements so the interviewer hears the loop close.
- Offer extension paths (multi-region, analytics pipeline, abuse prevention) rather than waiting silently for the next question.
- If minutes remain, ask which area the interviewer wants deeper — don't monologue past the buzzer.

**Weak:** "…so yeah, that's pretty much the design." (Trails off mid-component.)

**Strong:** "Recap: cache-aside timelines for read scale, hybrid fan-out to absorb celebrities, eventual consistency on timelines but strong on the posts themselves. Next I'd add multi-region active-passive and an abuse/dedup pipeline."

## The 4S shorthand

Scenario (clarify the use cases and constraints), Service (split the system into services and draw the boxes), Storage (data model and store choice), Scale (estimate load, then cache, shard, and replicate to meet it). Same muscles as RESHADED, compressed into four beats: estimation folds into Scale, API design into Service.

- Prefer 4S for 30-minute screens, scope-constrained single-component questions ("design a rate limiter"), or follow-up rounds where requirements are already fixed.
- Prefer RESHADED for 45+ minute open-ended designs: its explicit Estimation and Evaluation stages are where senior signal lives, and 4S makes them easy to skip.

## Capacity estimation and building blocks

The capacity estimation cheatsheet and the building blocks (load balancers, caching, queues, consistent hashing, sharding, replication, CAP, ACID vs BASE, CDN) live in `building-blocks.md` next to this file. Read the block the design needs, not the whole file.

## Common tradeoffs

**SQL vs NoSQL.**
- Choose SQL when data is relational and you need joins, transactions, or ad-hoc queries — and volume fits one primary plus read replicas, which carries further than people assume (TB-scale, thousands of RPS).
- Choose NoSQL when access is key-shaped at volumes that force horizontal write scaling, the schema is denormalized by design, or you need a specific model (document, wide-column, graph).
- "NoSQL because scale" without a write-volume number is a red flag — so is "SQL because familiar" at 100K write RPS.

**Fan-out-on-write vs fan-out-on-read.**
- Choose fan-out-on-write (push) when reads dominate and follower counts are bounded: each feed is precomputed at post time, so reads are O(1) cache hits.
- Choose fan-out-on-read (pull) for write-heavy or highly skewed graphs: writes stay cheap; each read merges followees' posts at request time. A celebrity post under push triggers millions of timeline writes — pull avoids that.
- Production answer is usually hybrid: push for normal users, pull for celebrity authors, merge at read time.

**Push vs pull.**
- Choose push (WebSockets/SSE) when freshness is the product — chat, live scores, collaborative editing — and you can afford per-client connection state.
- Choose pull (polling) when seconds of staleness are fine: simpler, stateless, cache-friendly, self-rate-limiting.
- Long-polling is the middle ground when full WebSocket infrastructure isn't justified.

**Sync vs async processing.**
- Choose sync when the caller cannot proceed without the result (auth check, payment authorization) and the latency budget covers it.
- Choose async (queue + workers) when work is slow, bursty, or retryable — email, transcoding, fan-out — buying burst absorption and failure isolation.
- Async's bill: job-status tracking, results that appear later than the request, harder debugging. Budget for all three.

**Strong vs eventual consistency.**
- Choose strong when violating an invariant has real cost — balances, inventory, unique usernames — and accept the price in latency, and in availability during partitions.
- Choose eventual when staleness is invisible or tolerable — like counts, view counters, timelines — and accept conflict resolution plus "where did my write go" UX; mitigate with read-your-own-writes sessions.
- State the choice per data path, not per system — most designs need both.

## Anti-patterns

- Designing before requirements — naming components before asking who uses the system and at what scale.
- No numbers anywhere — zero QPS, storage, or latency figures, so every sizing decision is a guess.
- "Just add more servers" — scaling answered without naming the actual bottleneck (write throughput? hot key? bandwidth?).
- Ignoring failure modes — no answer for cache loss, primary failover, queue backlog, or a lost region.
- Name-dropping tech without justification — "Kafka, Redis, Cassandra" as nouns, with no tradeoff tied to a requirement.
- Skipping the wrap-up — ending mid-component with no recap of the design, its tradeoffs, and next steps.
