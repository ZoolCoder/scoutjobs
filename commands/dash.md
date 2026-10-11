---
description: Start the ScoutJobs dashboard (local only) and print its URL
allowed-tools: Bash(scoutjobs:*)
---

Run `scoutjobs serve` in the background (for example `nohup scoutjobs serve >/dev/null 2>&1 &` or the platform's background-task feature), wait one second, then tell the user the URL printed by `scoutjobs serve` (default http://127.0.0.1:7070) and that it is bound to localhost only. If the port is busy, assume a dashboard is already running and just give the URL.
