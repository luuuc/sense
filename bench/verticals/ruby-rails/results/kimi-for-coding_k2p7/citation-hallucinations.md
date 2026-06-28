# Citation hallucinations

Citations the answer printed that did not resolve against the repo checked out at the benchmarked commit. **Hallucinated** = line number beyond end-of-file (a made-up number). **Unresolved** = file not in the repo, or symbol not within ±5 lines of the cited line.

Reported for transparency; not folded into the headline score.

## baseline

### baseline/chatwoot  — 65/66 grounded

**Hallucinated**
- `app/services/whatsapp/incoming_message_service.rb:7` — line 7 out of range (file only 5 lines)

### baseline/mastodon  — 276/280 grounded

**Unresolved**
- `app/lib/mastodon/cli/statuses.rb:89` — file not found at app/lib/mastodon/cli/statuses.rb
- `app/lib/mastodon/cli/maintenance.rb:590` — file not found at app/lib/mastodon/cli/maintenance.rb
- `app/lib/mastodon/cli/maintenance.rb:764` — file not found at app/lib/mastodon/cli/maintenance.rb
- `app/lib/mastodon/cli/cache.rb:48` — file not found at app/lib/mastodon/cli/cache.rb

### baseline/redmine  — 2/3 grounded

**Unresolved**
- `Mailer.rb:108` — file not found at Mailer.rb

## sense

### sense/forem  — 206/207 grounded

**Unresolved**
- `app/services/moderator/sink_articles_worker.rb:7` — file not found at app/services/moderator/sink_articles_worker.rb

### sense/mastodon  — 183/187 grounded

**Unresolved**
- `app/lib/mastodon/cli/statuses.rb:40` — file not found at app/lib/mastodon/cli/statuses.rb
- `app/lib/mastodon/cli/accounts.rb:576` — file not found at app/lib/mastodon/cli/accounts.rb
- `app/lib/mastodon/cli/cache.rb:47` — file not found at app/lib/mastodon/cli/cache.rb
- `app/lib/mastodon/cli/maintenance.rb:13` — file not found at app/lib/mastodon/cli/maintenance.rb
