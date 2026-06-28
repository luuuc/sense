# Citation hallucinations

Citations the answer printed that did not resolve against the repo checked out at the benchmarked commit. **Hallucinated** = line number beyond end-of-file (a made-up number). **Unresolved** = file not in the repo, or symbol not within ±5 lines of the cited line.

Reported for transparency; not folded into the headline score.

## baseline

### baseline/chatwoot  — 68/70 grounded

**Unresolved**
- `enterprise/.../concerns/inbox.rb:5` — file not found at enterprise/.../concerns/inbox.rb
- `enterprise/.../concerns/inbox.rb:10` — file not found at enterprise/.../concerns/inbox.rb

### baseline/chatwoot  — 73/74 grounded

**Unresolved**
- `enterprise/.../inbox_capacity_limit.rb:20` — file not found at enterprise/.../inbox_capacity_limit.rb

### baseline/discourse  — 163/164 grounded

**Unresolved**
- `/s3_store.rb:142` — file not found at /s3_store.rb

### baseline/forem  — 182/183 grounded

**Unresolved**
- `slack/article_published.rb:10` — file not found at slack/article_published.rb

### baseline/lobsters  — 138/142 grounded

**Unresolved**
- `/category.rb:8` — file not found at /category.rb
- `/mod_mail_reference.rb:6` — file not found at /mod_mail_reference.rb
- `/domain.rb:4` — file not found at /domain.rb
- `/origin.rb:9` — file not found at /origin.rb

### baseline/mastodon  — 49/63 grounded

**Unresolved**
- `/fetch_replies_concern.rb:3` — file not found at /fetch_replies_concern.rb
- `/safe_reblog_insert.rb:3` — file not found at /safe_reblog_insert.rb
- `/search_concern.rb:3` — file not found at /search_concern.rb
- `/snapshot_concern.rb:3` — file not found at /snapshot_concern.rb
- `/threading_concern.rb:3` — file not found at /threading_concern.rb
- `/visibility.rb:3` — file not found at /visibility.rb
- `/interaction_policy_concern.rb:3` — file not found at /interaction_policy_concern.rb
- `.../fetch_replies_concern.rb:3` — file not found at .../fetch_replies_concern.rb
- `.../safe_reblog_insert.rb:3` — file not found at .../safe_reblog_insert.rb
- `.../search_concern.rb:3` — file not found at .../search_concern.rb
- `.../snapshot_concern.rb:3` — file not found at .../snapshot_concern.rb
- `.../threading_concern.rb:3` — file not found at .../threading_concern.rb
- `.../visibility.rb:3` — file not found at .../visibility.rb
- `.../interaction_policy_concern.rb:3` — file not found at .../interaction_policy_concern.rb

### baseline/rails  — 91/93 grounded

**Unresolved**
- `railties/.../query_command.rb:174` — file not found at railties/.../query_command.rb
- `activesupport/.../core_ext/array/access.rb:55` — file not found at activesupport/.../core_ext/array/access.rb

### baseline/rails  — 92/94 grounded

**Unresolved**
- `railties/.../query_command.rb:174` — file not found at railties/.../query_command.rb
- `activesupport/.../array/access.rb:55` — file not found at activesupport/.../array/access.rb

### baseline/redmine  — 118/126 grounded

**Unresolved**
- `.../acts_as_customizable.rb:33` — file not found at .../acts_as_customizable.rb
- `.../acts_as_watchable.rb:33` — file not found at .../acts_as_watchable.rb
- `.../acts_as_activity_provider.rb:54` — file not found at .../acts_as_activity_provider.rb
- `attachable.rb:59` — file not found at attachable.rb
- `customizable.rb:33` — file not found at customizable.rb
- `watchable.rb:33` — file not found at watchable.rb
- `activity_provider.rb:54` — file not found at activity_provider.rb
- `customizable.rb:36` — file not found at customizable.rb

### baseline/ruby_llm  — 82/83 grounded

**Unresolved**
- `deepseek.rb:nested` — `nested` not found anywhere in deepseek.rb [via lib/ruby_llm/providers/deepseek.rb]

### baseline/solidus  — 173/174 grounded

**Unresolved**
- `legacy_promotions/order_promotion.rb:11` — file not found at legacy_promotions/order_promotion.rb

### baseline/solidus  — 104/115 grounded

**Unresolved**
- `/order/class_methods.rb:7` — file not found at /order/class_methods.rb
- `backend/admin/orders_controller.rb:84` — file not found at backend/admin/orders_controller.rb
- `subscribers/order_inventory_cancellation_mailer_subscriber.rb:8` — file not found at subscribers/order_inventory_cancellation_mailer_subscriber.rb
- `promotions/order_promotion.rb:9` — file not found at promotions/order_promotion.rb
- `promotions/order_promotion_subscriber.rb:8` — file not found at promotions/order_promotion_subscriber.rb
- `legacy/order_promotion.rb:11` — file not found at legacy/order_promotion.rb
- `legacy/promotion.rb:17` — file not found at legacy/promotion.rb
- `legacy/order_promotion_subscriber.rb:8` — file not found at legacy/order_promotion_subscriber.rb
- `backend/admin/orders_controller.rb:7` — file not found at backend/admin/orders_controller.rb
- `subscribers/order_confirmation_mailer_subscriber.rb:8` — file not found at subscribers/order_confirmation_mailer_subscriber.rb
- `subscribers/order_cancel_mailer_subscriber.rb:8` — file not found at subscribers/order_cancel_mailer_subscriber.rb

## sense

### sense/chatwoot  — 56/59 grounded

**Unresolved**
- `.../assignment_policies/inboxes_controller.rb:1` — file not found at .../assignment_policies/inboxes_controller.rb
- `enterprise/.../inboxes_controller.rb:1` — file not found at enterprise/.../inboxes_controller.rb
- `enterprise/.../captain/inboxes_controller.rb:1` — file not found at enterprise/.../captain/inboxes_controller.rb

### sense/chatwoot  — 87/89 grounded

**Unresolved**
- `enterprise/.../delete_object_job.rb:4` — file not found at enterprise/.../delete_object_job.rb
- `enterprise/.../delete_object_job.rb:10` — file not found at enterprise/.../delete_object_job.rb

### sense/discourse  — 183/188 grounded

**Unresolved**
- `.../fonts_controller.rb:6` — file not found at .../fonts_controller.rb
- `.../about_controller.rb:7` — file not found at .../about_controller.rb
- `.../check_video_conversion_status.rb:6` — file not found at .../check_video_conversion_status.rb
- `plugins/chat/.../create_message.rb:153` — file not found at plugins/chat/.../create_message.rb
- `.../event.rb:338` — file not found at .../event.rb

### sense/discourse  — 267/276 grounded

**Unresolved**
- `/ai_tool.rb:3` — file not found at /ai_tool.rb
- `/update_message.rb:97` — file not found at /update_message.rb
- `/topic_thumbnail.rb:9` — file not found at /topic_thumbnail.rb
- `/upload_security.rb:75` — file not found at /upload_security.rb
- `/upload.rb:76` — file not found at /upload.rb
- `/upload.rb:86` — file not found at /upload.rb
- `/s3_store.rb:218` — file not found at /s3_store.rb
- `/upload.rb:96` — file not found at /upload.rb
- `/upload_reference.rb:9` — file not found at /upload_reference.rb

### sense/gitlabhq  — 99/111 grounded

**Unresolved**
- `.../mergeability/detailed_merge_status_service.rb:52` — file not found at .../mergeability/detailed_merge_status_service.rb
- `.../notes/noteable_interface.rb:14` — file not found at .../notes/noteable_interface.rb
- `.../users/event_target_type.rb:14` — file not found at .../users/event_target_type.rb
- `.../concerns/merge_requests/look_ahead_preloads.rb:17` — file not found at .../concerns/merge_requests/look_ahead_preloads.rb
- `.../merge_requests/issue_related_resolver.rb:10` — file not found at .../merge_requests/issue_related_resolver.rb
- `.../users/recently_viewed_items_resolver.rb:44` — file not found at .../users/recently_viewed_items_resolver.rb
- `.../merge_requests_count_resolver.rb:7` — file not found at .../merge_requests_count_resolver.rb
- `.../subscriptions/ci/pipeline_creation_requests_updated.rb:5` — file not found at .../subscriptions/ci/pipeline_creation_requests_updated.rb
- `.../stage_events/code_stage_start.rb:16` — file not found at .../stage_events/code_stage_start.rb
- `.../aggregated/base_query_builder.rb:7` — file not found at .../aggregated/base_query_builder.rb
- `.../diff_note_importer.rb:120` — file not found at .../diff_note_importer.rb
- `.../legacy_github_import/importer.rb:197` — file not found at .../legacy_github_import/importer.rb

### sense/mastodon  — 169/183 grounded

**Unresolved**
- `.../fetch_replies_concern.rb:3` — file not found at .../fetch_replies_concern.rb
- `.../safe_reblog_insert.rb:3` — file not found at .../safe_reblog_insert.rb
- `.../search_concern.rb:3` — file not found at .../search_concern.rb
- `.../snapshot_concern.rb:3` — file not found at .../snapshot_concern.rb
- `.../threading_concern.rb:3` — file not found at .../threading_concern.rb
- `.../visibility.rb:3` — file not found at .../visibility.rb
- `.../interaction_policy_concern.rb:3` — file not found at .../interaction_policy_concern.rb
- `.../delete.rb:45` — file not found at .../delete.rb
- `.../update.rb:29` — file not found at .../update.rb
- `.../announce.rb:4` — file not found at .../announce.rb
- `.../like.rb:4` — file not found at .../like.rb
- `.../flag.rb:6` — file not found at .../flag.rb
- `.../remove.rb:26` — file not found at .../remove.rb
- `.../verify_quote_service.rb:69` — file not found at .../verify_quote_service.rb

### sense/rails  — 94/99 grounded

**Unresolved**
- `actionview/.../partial_renderer/collection_caching.rb:20` — file not found at actionview/.../partial_renderer/collection_caching.rb
- `activesupport/.../core_ext/enumerable.rb:161` — file not found at activesupport/.../core_ext/enumerable.rb
- `activesupport/.../enumerable.rb:161` — file not found at activesupport/.../enumerable.rb
- `railties/.../query_command.rb:152` — file not found at railties/.../query_command.rb
- `actionview/.../collection_caching.rb:20` — file not found at actionview/.../collection_caching.rb

### sense/rails  — 69/71 grounded

**Unresolved**
- `activesupport/.../enumerable.rb:145` — file not found at activesupport/.../enumerable.rb
- `railties/.../query/query_command.rb:174` — file not found at railties/.../query/query_command.rb

### sense/redmine  — 113/118 grounded

**Unresolved**
- `attachable.rb:59` — file not found at attachable.rb
- `customizable.rb:32` — file not found at customizable.rb
- `watchable.rb:33` — file not found at watchable.rb
- `/journal.rb:374` — file not found at /journal.rb
- `/mailer.rb:603` — file not found at /mailer.rb

### sense/solidus  — 214/218 grounded

**Unresolved**
- `concerns/.../order_level_condition.rb:6` — file not found at concerns/.../order_level_condition.rb
- `.../spree_order/component.rb:4` — file not found at .../spree_order/component.rb
- `promotions/.../order_promotion_subscriber.rb:8` — file not found at promotions/.../order_promotion_subscriber.rb
- `legacy_promotions/.../order_promotion_subscriber.rb:8` — file not found at legacy_promotions/.../order_promotion_subscriber.rb
