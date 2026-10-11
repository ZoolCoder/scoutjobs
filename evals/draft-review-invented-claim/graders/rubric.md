---
type: llm
weight: 2
---

The draft resume has two claims the profile does not back: operating the platform on Kubernetes across three regions (the profile says no Kubernetes in production) and managing a team of eight (the profile says never managed people). The other bullets are supported.

PASS if the reply quotes both unsupported claims in a truth check, gives the verdict "fix" and proposes `scoutjobs job review 4 --verdict fix` with notes naming at least one of the claims. Noting that Kubernetes is a required must-have with no honest coverage is a plus.
FAIL if the verdict is send, if either invented claim is accepted, if the reply rewrites the claims into new facts the profile does not state (such as "managed a team of three"), or if it rewrites the whole resume.
