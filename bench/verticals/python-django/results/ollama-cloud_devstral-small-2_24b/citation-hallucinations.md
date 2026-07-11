# Citation hallucinations

Citations the answer printed that did not resolve against the repo checked out at the benchmarked commit. **Hallucinated** = line number beyond end-of-file (a made-up number). **Unresolved** = file not in the repo, or symbol not within ±5 lines of the cited line.

Reported for transparency; not folded into the headline score.

## baseline

### baseline/netbox  — 19/21 grounded

**Unresolved**
- `dcim/device_components.py:50` — file not found at dcim/device_components.py
- `dcim/device_components.py:73` — file not found at dcim/device_components.py

### baseline/sentry  — 108/124 grounded

**Hallucinated**
- `src/sentry/models/groupseen.py:33` — line 33 out of range (file only 28 lines)
- `src/sentry/models/groupbookmark.py:33` — line 33 out of range (file only 31 lines)
- `src/sentry/models/groupshare.py:37` — line 37 out of range (file only 35 lines)
- `src/sentry/tasks/reprocessing2.py:584` — line 584 out of range (file only 291 lines)
- `src/sentry/processing_errors/provisioning.py:1060` — line 1060 out of range (file only 87 lines)

**Unresolved**
- `src/sentry/seer/lightweight_rca_cluster.py:7` — file not found at src/sentry/seer/lightweight_rca_cluster.py
- `src/sentry/seer/night_shift/skip_cache.py:22` — file not found at src/sentry/seer/night_shift/skip_cache.py
- `src/sentry/seer/night_shift/cron.py:529` — file not found at src/sentry/seer/night_shift/cron.py
- `src/sentry/seer/backfill_supergroups_lightweight.py:123` — file not found at src/sentry/seer/backfill_supergroups_lightweight.py
- `src/sentry/seer/statistical_detectors/detector.py:20` — file not found at src/sentry/seer/statistical_detectors/detector.py
- `src/sentry/seer/usecases/replay_counts.py:135` — file not found at src/sentry/seer/usecases/replay_counts.py
- `src/sentry/incidents/logic/incident_occurrence.py:23` — file not found at src/sentry/incidents/logic/incident_occurrence.py
- `src/sentry/incidents/metric_issue_post_process.py:7` — file not found at src/sentry/incidents/metric_issue_post_process.py
- `src/sentry/notifications/models/notification_action.py:32` — file not found at src/sentry/notifications/models/notification_action.py
- `src/sentry/notifications/services/notifications_service.py:22` — file not found at src/sentry/notifications/services/notifications_service.py
- `notifications/services/notifications_service.py:22` — file not found at notifications/services/notifications_service.py

## sense

### sense/sentry  — 151/155 grounded

**Hallucinated**
- `src/sentry/models/groupredirect.py:62` — line 62 out of range (file only 52 lines)
- `src/sentry/models/groupsearchview.py:76` — line 76 out of range (file only 70 lines)
- `src/sentry/models/groupenvironment.py:80` — line 80 out of range (file only 61 lines)
- `src/sentry/models/grouprulestatus.py:82` — line 82 out of range (file only 27 lines)

### sense/sentry  — 132/133 grounded

**Unresolved**
- `src/sentry/api/endpoints/organization_group_index.py:77` — file not found at src/sentry/api/endpoints/organization_group_index.py
