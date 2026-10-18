## Capacity estimation cheatsheet

Arithmetic shortcuts:

- 86,400 seconds/day; ~2.5M seconds/month (30 × 86,400 = 2,592,000).
- 1M requests/day ≈ 12 RPS. Scale linearly: 100M/day ≈ 1,200 RPS, 1B/day ≈ 12K RPS.
- Peak ≈ 2-3× average. Size for peak.
- Powers of two: 2^10 = 1,024 ≈ 10^3 (KB ↔ thousand), 2^20 ≈ 10^6 (MB ↔ million), 2^30 ≈ 10^9 (GB ↔ billion). So 1M items × 1 KB ≈ 1 GB.
- Availability math: 99.9% allows ~43 min downtime/month, 99.99% ~4.3 min (43,200 min/month × 0.1% and 0.01%).
- Round every input to one significant figure. Precision theater burns interview minutes.

Latency reference (memorize):

| Operation | Latency |
|---|---|
| L1/L2 cache hit | ~1–10 ns (< 0.01 ms) |
| RAM read | ~0.0001 ms (100 ns) |
| SSD random read | ~0.1 ms |
| Intra-datacenter network RTT | ~0.5 ms |
| HDD seek | ~10 ms |
| Cross-region network RTT | 60-150 ms |

Rules of thumb that fall out of the table:

- SSD is ~1,000× slower than RAM (0.1 ms vs 0.0001 ms); HDD seek is ~100× slower than SSD random read.
- One cross-region round trip (60-150 ms) costs more than 100 intra-DC round trips (~0.5 ms each) — keep chatty call chains inside one region.
- A 200 ms latency budget affords at most one cross-region hop at worst-case (150 ms) RTT.

Worked example — photo app, 50M DAU, 2 uploads/user/day, 5 MB average photo:

- Writes: 100M uploads/day ≈ 1,200 RPS average, ~3,600 RPS peak (3×).
- Reads at an assumed 10:1 read:write ratio: 1B views/day ≈ 12K RPS — the CDN, not the origin, must absorb this.
- Ingest bandwidth: 100M × 5 MB = 500 TB/day ÷ 86,400 s ≈ 5.8 GB/s.
- Storage: 500 TB/day ≈ 180 PB/year raw, ~550 PB/year with 3× replication.

## Building blocks

### Load balancers

- L4 (transport layer): routes on IP/port without reading payloads. Cheaper per connection, protocol-agnostic — use for raw throughput, non-HTTP traffic, TLS passthrough.
- L7 (application layer): parses HTTP — path/header/cookie routing, TLS termination, retries, sticky sessions. Use for microservice routing and canary/A-B traffic splits.
- Algorithms: round-robin (default), least-connections (uneven request costs), consistent hash (cache affinity).
- Health checks eject bad backends; connection draining for deploys. The LB itself is made HA via a redundant pair or anycast.

### Caching

The four standard patterns:

- Cache-aside (lazy loading): app reads cache, on miss loads from DB and populates. Most common; survives cache failure; first hit per key is slow; stale until TTL or invalidation.
- Write-through: write to cache and DB synchronously. Reads always warm; costs write latency and caches data that may never be read.
- Write-back (write-behind): write to cache, flush to DB asynchronously. Fastest writes, absorbs bursts; risks losing acked writes if the cache dies before flush — needs replication or a WAL.
- Write-around: write to DB only; cache fills on later reads. Good for write-heavy data that is rarely re-read soon after writing.

Eviction and hot keys:

- Eviction policies: LRU (sane default), LFU (stable popularity distributions), TTL (bounds staleness — the freshness knob for cache-aside).
- For hot keys, use a local cache replica — a per-process in-memory copy of the hot row — so one cache shard doesn't absorb every read for a celebrity key.

### Message queues

- Decouple producers from consumers, absorb bursts, enable retry with backoff plus a dead-letter queue for poison messages.
- At-least-once delivery: ack after processing; duplicates possible on redelivery, so consumers must be idempotent. The default choice.
- At-most-once delivery: ack before processing; messages can be lost but never duplicated. Only for loss-tolerant data (metrics, logs).
- "Exactly-once" is effectively-once: at-least-once delivery plus idempotent consumers or dedup keys (e.g., Kafka transactional IDs). True exactly-once delivery over an unreliable network is impossible — say this in the interview.
- Ordering is usually guaranteed only per partition/key, not globally — pick the partition key so events that must stay ordered share it.
- Watch consumer lag; a growing backlog is the failure signal that triggers scaling consumers or shedding load.

### Consistent hashing

- Keys and nodes hash onto the same ring; each key belongs to the next node clockwise.
- Why it matters: adding or removing a node reshuffles only ~1/N of keys — bounded reshuffle — versus nearly all keys under mod-N hashing.
- Virtual nodes: each physical node takes many ring positions, smoothing load variance and letting bigger machines own proportionally more of the ring.
- Where it shows up: distributed caches, Cassandra/DynamoDB-style partitioning, sticky load balancing.

### Sharding

- Range sharding: keeps order, so range scans are cheap; risks hot ranges (time-ordered keys hammer the newest shard).
- Hash sharding: uniform spread; range queries become scatter-gather.
- Choose the shard key from the dominant access pattern; resharding is the expensive event, so plan it (consistent hashing or a directory service).
- Hot-shard mitigation: split the hot shard, salt the key (random suffix fan-out), and for celebrity keys serve reads from a local cache replica in each app process.

### Replication

- Leader/follower: writes to the leader, reads from followers. Simple; replication lag means followers serve stale reads; failover requires leader election.
- Quorum (leaderless): write to W replicas, read from R; R + W > N guarantees the read set overlaps the latest write (e.g., N=3, W=2, R=2). Trade W against R for latency while keeping R+W>N — lowering one means raising the other.
- Sync replication survives failover without losing acked writes but raises write latency; async is fast but can drop the latest writes on failover. Say which you picked and why.
- Multi-leader (write in every region) buys local write latency at the cost of conflicts — if you propose it, name the resolution policy (last-write-wins, CRDTs, app-level merge).

### CAP theorem

During a network partition you choose consistency or availability — partition tolerance is not optional in a distributed system. Without a partition you can have both.

| Choice | Behavior during partition | Examples |
|---|---|---|
| CP | Returns error or times out rather than serving inconsistent data | MongoDB (single-primary), HBase, ZooKeeper |
| AP | Returns possibly stale data rather than error | Cassandra, DynamoDB, CouchDB |
| CA | Assumes no partitions — meaningful only single-node | Single-node RDBMS |

Real systems choose per operation: many stores expose tunable consistency (e.g., quorum reads on an AP store for the paths that need freshness).

### ACID vs BASE

- ACID (atomicity, consistency, isolation, durability): single-system transactions with invariants enforced. Pick for money, inventory, uniqueness constraints.
- BASE (basically available, soft state, eventually consistent): availability and scale-out first. Pick for feeds, counters, presence — data where brief staleness is invisible.
- Not a binary: NewSQL (Spanner, CockroachDB) offers distributed ACID at a latency cost, and BASE stores bolt on per-operation consistency knobs.

### CDN

- Edge servers cache content near users, cutting origin load and replacing a 60-150 ms cross-region RTT with a nearby edge hop.
- The operational knobs are Cache-Control headers (max-age, s-maxage, stale-while-revalidate) for freshness policy, and the purge/invalidation API for immediate removal.
- Fingerprinted asset URLs (app.3f9a2c.js) allow year-long TTLs — a deploy changes the URL instead of requiring a purge.
- Pull CDN (edge fetches from origin on first miss) is the default; push (pre-upload) only for predictable large releases.
