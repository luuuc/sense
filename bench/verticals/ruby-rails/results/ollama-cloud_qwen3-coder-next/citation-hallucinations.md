# Citation hallucinations

Citations the answer printed that did not resolve against the repo checked out at the benchmarked commit. **Hallucinated** = line number beyond end-of-file (a made-up number). **Unresolved** = file not in the repo, or symbol not within ±5 lines of the cited line.

Reported for transparency; not folded into the headline score.

## baseline

### baseline/discourse  — 290/296 grounded

**Hallucinated**
- `app/models/upload.rb:772` — line 772 out of range (file only 736 lines)
- `cooked_post_processor.rb:515` — line 515 out of range (file only 288 lines) [via lib/cooked_post_processor.rb]
- `theme_field.rb:877` — line 877 out of range (file only 760 lines) [via app/models/theme_field.rb]
- `user_avatars.rb:63` — line 63 out of range (file only 54 lines) [via migrations/lib/importer/steps/users/user_avatars.rb]
- `user_avatars.rb:70` — line 70 out of range (file only 54 lines) [via migrations/lib/importer/steps/users/user_avatars.rb]

**Unresolved**
- `plugins/chat/spec/plugins_spec.rb:30` — file not found at plugins/chat/spec/plugins_spec.rb

### baseline/forem  — 166/168 grounded

**Hallucinated**
- `app/controllers/api/v0/articles_controller.rb:117` — line 117 out of range (file only 19 lines)

**Unresolved**
- `app/services/users/deleteArticles.rb:8` — file not found at app/services/users/deleteArticles.rb

### baseline/gitlabhq  — 286/302 grounded

**Unresolved**
- `app/services/merge_requests/drafts/update_service.rb:3` — file not found at app/services/merge_requests/drafts/update_service.rb
- `app/services/merge_requests/pipelines/create_service.rb:3` — file not found at app/services/merge_requests/pipelines/create_service.rb
- `app/services/merge_requests/revert_service.rb:3` — file not found at app/services/merge_requests/revert_service.rb
- `app/services/merge_requests/commit_service.rb:3` — file not found at app/services/merge_requests/commit_service.rb
- `app/services/merge_requests/resolve_service.rb:3` — file not found at app/services/merge_requests/resolve_service.rb
- `app/services/merge_requests/resolve_todos_after_approval_service.rb:3` — file not found at app/services/merge_requests/resolve_todos_after_approval_service.rb
- `app/services/merge_requests/approve_service.rb:3` — file not found at app/services/merge_requests/approve_service.rb
- `app/services/merge_requests/refresh/notify_about_push_service.rb:3` — file not found at app/services/merge_requests/refresh/notify_about_push_service.rb
- `app/services/merge_requests/drafts/close_service.rb:3` — file not found at app/services/merge_requests/drafts/close_service.rb
- `app/services/merge_requests/drafts/comment_service.rb:3` — file not found at app/services/merge_requests/drafts/comment_service.rb
- `app/services/merge_requests/link_service.rb:3` — file not found at app/services/merge_requests/link_service.rb
- `app/services/merge_requests/stuck_merge_jobs_service.rb:3` — file not found at app/services/merge_requests/stuck_merge_jobs_service.rb
- `app/services/merge_requests/keep_around_refs_worker.rb:3` — file not found at app/services/merge_requests/keep_around_refs_worker.rb
- `app/services/ci/create_pipeline_service/merge_requests.rb:3` — file not found at app/services/ci/create_pipeline_service/merge_requests.rb
- `lib/api/entities/merge_request-reviewer.rb:3` — file not found at lib/api/entities/merge_request-reviewer.rb
- `app/controllers/homepage_data.rb:6` — file not found at app/controllers/homepage_data.rb

### baseline/gitlabhq  — 227/233 grounded

**Hallucinated**
- `app/models/merge_request_diff.rb:1548` — line 1548 out of range (file only 1139 lines)
- `app/models/merge_request_diff.rb:2121` — line 2121 out of range (file only 1139 lines)
- `app/models/merge_request_diff.rb:2841` — line 2841 out of range (file only 1139 lines)

**Unresolved**
- `app/services/merge_requests/discard_draft_service.rb:4` — file not found at app/services/merge_requests/discard_draft_service.rb
- `aid_service.rb:4` — file not found at aid_service.rb
- `app/workers/merge_requests/create_merge_request_worker.rb:4` — file not found at app/workers/merge_requests/create_merge_request_worker.rb

### baseline/llm.rb  — 10/76 grounded

**Unresolved**
- `/lib/llm/provider.rb:8` — file not found at /lib/llm/provider.rb
- `/lib/llm.rb:112` — file not found at /lib/llm.rb
- `/lib/llm/registry.rb:18` — file not found at /lib/llm/registry.rb
- `/lib/llm/response.rb:32` — file not found at /lib/llm/response.rb
- `/lib/llm/provider.rb:103` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:94` — file not found at /lib/llm/provider.rb
- `/lib/llm/providers/openai.rb:73` — file not found at /lib/llm/providers/openai.rb
- `/lib/llm/providers/openai.rb:78` — file not found at /lib/llm/providers/openai.rb
- `/lib/llm/providers/openai/response_adapter.rb:24` — file not found at /lib/llm/providers/openai/response_adapter.rb
- `/lib/llm/providers/openai.rb:77` — file not found at /lib/llm/providers/openai.rb
- `/lib/llm/providers/openai/error_handler.rb:34` — file not found at /lib/llm/providers/openai/error_handler.rb
- `/lib/llm/provider.rb:384` — file not found at /lib/llm/provider.rb
- `/lib/llm/providers/openai/stream_parser.rb:16` — file not found at /lib/llm/providers/openai/stream_parser.rb
- `/lib/llm/providers/openai.rb:16` — file not found at /lib/llm/providers/openai.rb
- `/lib/llm/providers/anthropic.rb:16` — file not found at /lib/llm/providers/anthropic.rb
- `/lib/llm/providers/google.rb:17` — file not found at /lib/llm/providers/google.rb
- `/lib/llm/providers/bedrock.rb:33` — file not found at /lib/llm/providers/bedrock.rb
- `/lib/llm/providers/ollama.rb:18` — file not found at /lib/llm/providers/ollama.rb
- `/lib/llm/providers/deepseek.rb:20` — file not found at /lib/llm/providers/deepseek.rb
- `/lib/llm/providers/xai.rb:17` — file not found at /lib/llm/providers/xai.rb
- `/lib/llm/providers/zai.rb:16` — file not found at /lib/llm/providers/zai.rb
- `/lib/llm/providers/llamacpp.rb:22` — file not found at /lib/llm/providers/llamacpp.rb
- `/lib/llm/provider.rb:55` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:362` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:371` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:70` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:126` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:133` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:140` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:147` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:154` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:161` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:168` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:176` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:183` — file not found at /lib/llm/provider.rb
- `/lib/llm/providers/openai/request_adapter.rb:17` — file not found at /lib/llm/providers/openai/request_adapter.rb
- `/lib/llm/providers/anthropic/request_adapter.rb:13` — file not found at /lib/llm/providers/anthropic/request_adapter.rb
- `/lib/llm/providers/google/request_adapter.rb:13` — file not found at /lib/llm/providers/google/request_adapter.rb
- `/lib/llm/providers/bedrock/request_adapter.rb:18` — file not found at /lib/llm/providers/bedrock/request_adapter.rb
- `/lib/llm/providers/ollama/request_adapter.rb:13` — file not found at /lib/llm/providers/ollama/request_adapter.rb
- `/lib/llm/providers/deepseek/request_adapter.rb:12` — file not found at /lib/llm/providers/deepseek/request_adapter.rb
- `/lib/llm/providers/anthropic/response_adapter.rb:19` — file not found at /lib/llm/providers/anthropic/response_adapter.rb
- `/lib/llm/providers/google/response_adapter.rb:21` — file not found at /lib/llm/providers/google/response_adapter.rb
- `/lib/llm/providers/ollama/response_adapter.rb:17` — file not found at /lib/llm/providers/ollama/response_adapter.rb
- `/lib/llm/providers/anthropic/stream_parser.rb:16` — file not found at /lib/llm/providers/anthropic/stream_parser.rb
- `/lib/llm/providers/google/stream_parser.rb:16` — file not found at /lib/llm/providers/google/stream_parser.rb
- `/lib/llm/providers/bedrock/stream_parser.rb:28` — file not found at /lib/llm/providers/bedrock/stream_parser.rb
- `/lib/llm/providers/ollama/stream_parser.rb:14` — file not found at /lib/llm/providers/ollama/stream_parser.rb
- `/lib/llm/providers/openai/error_handler.rb:25` — file not found at /lib/llm/providers/openai/error_handler.rb
- `/lib/llm/providers/anthropic/error_handler.rb:25` — file not found at /lib/llm/providers/anthropic/error_handler.rb
- `/lib/llm/providers/google/error_handler.rb:25` — file not found at /lib/llm/providers/google/error_handler.rb
- `/lib/llm/providers/bedrock/error_handler.rb:25` — file not found at /lib/llm/providers/bedrock/error_handler.rb
- `/lib/llm/providers/ollama/error_handler.rb:25` — file not found at /lib/llm/providers/ollama/error_handler.rb
- `/lib/llm/providers/bedrock/stream_decoder.rb:30` — file not found at /lib/llm/providers/bedrock/stream_decoder.rb
- `/lib/llm/error.rb:28` — file not found at /lib/llm/error.rb
- `/lib/llm/error.rb:32` — file not found at /lib/llm/error.rb
- `/lib/llm/error.rb:36` — file not found at /lib/llm/error.rb
- `/lib/llm/error.rb:40` — file not found at /lib/llm/error.rb
- `/lib/llm/error.rb:56` — file not found at /lib/llm/error.rb
- `/lib/llm/error.rb:52` — file not found at /lib/llm/error.rb
- `/lib/llm/providers/openai/error_handler.rb:62` — file not found at /lib/llm/providers/openai/error_handler.rb
- `/lib/llm/providers/google/error_handler.rb:56` — file not found at /lib/llm/providers/google/error_handler.rb
- `/lib/llm/providers/openai/stream_parser.rb:29` — file not found at /lib/llm/providers/openai/stream_parser.rb
- `/lib/llm/providers/openai/stream_parser.rb:160` — file not found at /lib/llm/providers/openai/stream_parser.rb
- `/lib/llm/providers/openai/stream_parser.rb:174` — file not found at /lib/llm/providers/openai/stream_parser.rb
- `/lib/llm/providers/openai/stream_parser.rb:168` — file not found at /lib/llm/providers/openai/stream_parser.rb

### baseline/lobsters  — 117/120 grounded

**Hallucinated**
- `app/models/vote.rb:264` — line 264 out of range (file only 198 lines)
- `app/models/vote.rb:263` — line 263 out of range (file only 198 lines)

**Unresolved**
- `story_controller.rb:141` — file not found at story_controller.rb

### baseline/lobsters  — 12/13 grounded

**Unresolved**
- `app/models/notifications.rb:58` — file not found at app/models/notifications.rb

### baseline/mastodon  — 286/294 grounded

**Hallucinated**
- `app/workers/trigger_webhook_worker.rb:26` — line 26 out of range (file only 12 lines)

**Unresolved**
- `app/workers/announce_status_worker.rb:7` — file not found at app/workers/announce_status_worker.rb
- `app/workers/distribute_poll_update_worker.rb:18` — file not found at app/workers/distribute_poll_update_worker.rb
- `app/workers/quote_refresh_worker.rb:7` — file not found at app/workers/quote_refresh_worker.rb
- `app/workers/quote_request_worker.rb:18` — file not found at app/workers/quote_request_worker.rb
- `app/lib/activitypub/activity/favorite.rb:7` — file not found at app/lib/activitypub/activity/favorite.rb
- `app/workers/fan_out_on_write_service.rb:23` — file not found at app/workers/fan_out_on_write_service.rb
- `app/workers/fan_out_on_write_service.rb:170` — file not found at app/workers/fan_out_on_write_service.rb

### baseline/mastodon  — 169/170 grounded

**Hallucinated**
- `app/services/remove_status_service.rb:407` — line 407 out of range (file only 169 lines)

### baseline/rails  — 55/56 grounded

**Unresolved**
- `Relation.rb:69` — file not found at Relation.rb

### baseline/redmine  — 119/120 grounded

**Hallucinated**
- `app/models/mailer.rb:1160` — line 1160 out of range (file only 824 lines)

### baseline/ruby_llm  — 0/1 grounded

**Unresolved**
- `connection.rb:Now` — `Now` not found anywhere in connection.rb [via lib/ruby_llm/connection.rb]

### baseline/solidus  — 85/88 grounded

**Hallucinated**
- `core/app/models/spree/order_shipping.rb:181` — line 181 out of range (file only 78 lines)

**Unresolved**
- `promotions/app/models/solidus_promotions/order_patch.rb:23` — file not found at promotions/app/models/solidus_promotions/order_patch.rb
- `promotions/app/models/solidus_promotions/order_patch.rb:4` — file not found at promotions/app/models/solidus_promotions/order_patch.rb

### baseline/solidus  — 165/170 grounded

**Hallucinated**
- `core/lib/spree/core/controller_helpers/order.rb:25` — line 25 out of range (file only 7 lines)
- `promotions/app/models/solidus_promotions/conditions/first_order.rb:48` — line 48 out of range (file only 33 lines)

**Unresolved**
- `core/app/models/spree/tax/order_taxation.rb:5` — file not found at core/app/models/spree/tax/order_taxation.rb
- `core/app/models/spree/tax/order_taxation.rb:6` — file not found at core/app/models/spree/tax/order_taxation.rb
- `core/app/models/spree/tax/order_taxation.rb:1` — file not found at core/app/models/spree/tax/order_taxation.rb

## sense

### sense/chatwoot  — 103/107 grounded

**Unresolved**
- `app/services/contact_inbox_with_contact_builder.rb:38` — file not found at app/services/contact_inbox_with_contact_builder.rb
- `app/services/contact_inbox_with_contact_builder.rb:17` — file not found at app/services/contact_inbox_with_contact_builder.rb
- `app/services/contact_inbox_with_contact_builder.rb:52` — file not found at app/services/contact_inbox_with_contact_builder.rb
- `app/services/contact_inbox_with_contact_builder.rb:35` — file not found at app/services/contact_inbox_with_contact_builder.rb

### sense/chatwoot  — 46/110 grounded

**Unresolved**
- `/app/models/inbox.rb:42` — file not found at /app/models/inbox.rb
- `/app/models/conversation.rb:101` — file not found at /app/models/conversation.rb
- `/app/models/contact_inbox.rb:32` — file not found at /app/models/contact_inbox.rb
- `/app/models/message.rb:129` — file not found at /app/models/message.rb
- `/app/models/concerns/reportable.rb:3` — file not found at /app/models/concerns/reportable.rb
- `/app/models/concerns/avatarable.rb:3` — file not found at /app/models/concerns/avatarable.rb
- `/app/models/concerns/out_of_offisable.rb:3` — file not found at /app/models/concerns/out_of_offisable.rb
- `/app/models/concerns/account_cache_revalidator.rb:1` — file not found at /app/models/concerns/account_cache_revalidator.rb
- `/app/models/concerns/inbox_agent_availability.rb:1` — file not found at /app/models/concerns/inbox_agent_availability.rb
- `/app/models/inbox.rb:57` — file not found at /app/models/inbox.rb
- `/app/models/inbox.rb:80` — file not found at /app/models/inbox.rb
- `/enterprise/app/models/enterprise/inbox.rb:1` — file not found at /enterprise/app/models/enterprise/inbox.rb
- `/app/controllers/api/v1/accounts/inboxes_controller.rb:78` — file not found at /app/controllers/api/v1/accounts/inboxes_controller.rb
- `/app/jobs/delete_object_job.rb:6` — file not found at /app/jobs/delete_object_job.rb
- `/app/controllers/api/v1/accounts/inboxes_controller.rb:1` — file not found at /app/controllers/api/v1/accounts/inboxes_controller.rb
- `/app/controllers/api/v1/accounts/assignment_policies/inboxes_controller.rb:1` — file not found at /app/controllers/api/v1/accounts/assignment_policies/inboxes_controller.rb
- `/app/controllers/public/api/v1/inboxes_controller.rb:1` — file not found at /app/controllers/public/api/v1/inboxes_controller.rb
- `/app/controllers/super_admin/dashboard_controller.rb:8` — file not found at /app/controllers/super_admin/dashboard_controller.rb
- `/app/controllers/widget_tests_controller.rb:35` — file not found at /app/controllers/widget_tests_controller.rb
- `/enterprise/app/controllers/enterprise/api/v1/accounts/inboxes_controller.rb:1` — file not found at /enterprise/app/controllers/enterprise/api/v1/accounts/inboxes_controller.rb
- `/enterprise/app/controllers/api/v1/accounts/captain/inboxes_controller.rb:1` — file not found at /enterprise/app/controllers/api/v1/accounts/captain/inboxes_controller.rb
- `/app/jobs/auto_assignment/assignment_job.rb:25` — file not found at /app/jobs/auto_assignment/assignment_job.rb
- `/app/jobs/inboxes/fetch_imap_email_inboxes_job.rb:6` — file not found at /app/jobs/inboxes/fetch_imap_email_inboxes_job.rb
- `/app/jobs/inboxes/fetch_imap_emails_job.rb:9` — file not found at /app/jobs/inboxes/fetch_imap_emails_job.rb
- `/app/jobs/migration/remove_stale_notifications_job.rb:15` — file not found at /app/jobs/migration/remove_stale_notifications_job.rb
- `/app/services/twitter/webhook_subscribe_service.rb:21` — file not found at /app/services/twitter/webhook_subscribe_service.rb
- `/app/services/whatsapp/channel_creation_service.rb:59` — file not found at /app/services/whatsapp/channel_creation_service.rb
- `/lib/seeders/inbox_seeder.rb:31` — file not found at /lib/seeders/inbox_seeder.rb
- `/lib/test_data/inbox_creator.rb:2` — file not found at /lib/test_data/inbox_creator.rb
- `/enterprise/app/models/enterprise/inbox.rb:2` — file not found at /enterprise/app/models/enterprise/inbox.rb
- `/app/builders/messages/facebook/message_builder.rb:10` — file not found at /app/builders/messages/facebook/message_builder.rb
- `/app/builders/messages/instagram/base_message_builder.rb:136` — file not found at /app/builders/messages/instagram/base_message_builder.rb
- `/app/builders/messages/instagram/messenger/message_builder.rb:25` — file not found at /app/builders/messages/instagram/messenger/message_builder.rb
- `/app/builders/messages/message_builder.rb:136` — file not found at /app/builders/messages/message_builder.rb
- `/enterprise/app/models/agent_capacity_policy.rb:22` — file not found at /enterprise/app/models/agent_capacity_policy.rb
- `/enterprise/app/models/inbox_capacity_limit.rb:20` — file not found at /enterprise/app/models/inbox_capacity_limit.rb
- `/enterprise/app/models/call.rb:46` — file not found at /enterprise/app/models/call.rb
- `/enterprise/app/models/captain_inbox.rb:19` — file not found at /enterprise/app/models/captain_inbox.rb
- `/enterprise/app/models/sla_event.rb:24` — file not found at /enterprise/app/models/sla_event.rb
- `/app/builders/campaigns/campaign_conversation_builder.rb:4` — file not found at /app/builders/campaigns/campaign_conversation_builder.rb
- `/app/builders/contact_inbox_with_contact_builder.rb:67` — file not found at /app/builders/contact_inbox_with_contact_builder.rb
- `/app/actions/contact_merge_action.rb:1` — file not found at /app/actions/contact_merge_action.rb
- `/app/builders/account_builder.rb:46` — file not found at /app/builders/account_builder.rb
- `/lib/integrations/slack/link_unfurl_formatter.rb:17` — file not found at /lib/integrations/slack/link_unfurl_formatter.rb
- `/app/jobs/inboxes/fetch_imap_email_inboxes_job.rb:15` — file not found at /app/jobs/inboxes/fetch_imap_email_inboxes_job.rb
- `/app/controllers/api/v1/accounts/inboxes_controller.rb:84` — file not found at /app/controllers/api/v1/accounts/inboxes_controller.rb
- `/app/builders/messages/facebook/message_builder.rb:22` — file not found at /app/builders/messages/facebook/message_builder.rb
- `/app/builders/messages/facebook/message_builder.rb:31` — file not found at /app/builders/messages/facebook/message_builder.rb
- `/app/models/inbox_member.rb:35` — file not found at /app/models/inbox_member.rb
- `/app/models/inbox.rb:66` — file not found at /app/models/inbox.rb
- `/app/jobs/auto_assignment/assignment_job.rb:26` — file not found at /app/jobs/auto_assignment/assignment_job.rb
- `/app/builders/messages/facebook/message_builder.rb:15` — file not found at /app/builders/messages/facebook/message_builder.rb
- `/app/services/twitter/webhook_subscribe_service.rb:22` — file not found at /app/services/twitter/webhook_subscribe_service.rb
- `/app/controllers/api/v1/accounts/inboxes_controller.rb:85` — file not found at /app/controllers/api/v1/accounts/inboxes_controller.rb
- `/app/jobs/inboxes/fetch_imap_email_inboxes_job.rb:16` — file not found at /app/jobs/inboxes/fetch_imap_email_inboxes_job.rb
- `/app/models/inbox.rb:60` — file not found at /app/models/inbox.rb
- `/app/models/inbox.rb:62` — file not found at /app/models/inbox.rb
- `/app/models/inbox.rb:71` — file not found at /app/models/inbox.rb
- `/app/models/inbox.rb:73` — file not found at /app/models/inbox.rb
- `/app/jobs/migration/remove_stale_notifications_job.rb:11` — file not found at /app/jobs/migration/remove_stale_notifications_job.rb
- `/app/jobs/delete_object_job.rb:18` — file not found at /app/jobs/delete_object_job.rb
- `/app/models/agent_bot_inbox.rb:19` — file not found at /app/models/agent_bot_inbox.rb
- `/app/models/inbox_member.rb:23` — file not found at /app/models/inbox_member.rb
- `/app/models/campaign.rb:45` — file not found at /app/models/campaign.rb

### sense/discourse  — 97/99 grounded

**Unresolved**
- `lib/has_url.rb:21` — file not found at lib/has_url.rb
- `lib/has_url.rb:49` — file not found at lib/has_url.rb

### sense/forem  — 175/178 grounded

**Hallucinated**
- `app/models/mention.rb:170` — line 170 out of range (file only 34 lines)
- `app/models/context_notification.rb:172` — line 172 out of range (file only 12 lines)

**Unresolved**
- `app/models/concerns/cloudinary_helper.rb:1` — file not found at app/models/concerns/cloudinary_helper.rb

### sense/forem  — 254/259 grounded

**Unresolved**
- `app/workers/notifications/new_comment_worker.rb:2` — file not found at app/workers/notifications/new_comment_worker.rb
- `app/workers/notifications/new_mention_worker.rb:2` — file not found at app/workers/notifications/new_mention_worker.rb
- `Article.rb:50` — file not found at Article.rb
- `Article.rb:177` — file not found at Article.rb
- `Article.rb:396` — file not found at Article.rb

### sense/gitlabhq  — 132/151 grounded

**Hallucinated**
- `app/services/merge_requests/base_service.rb:1743` — line 1743 out of range (file only 376 lines)
- `app/services/merge_requests/merge_service.rb:1743` — line 1743 out of range (file only 216 lines)
- `app/services/merge_requests/create_service.rb:164` — line 164 out of range (file only 93 lines)

**Unresolved**
- `app/models/merge_request/concerns/merge_request_resource_event.rb:4` — file not found at app/models/merge_request/concerns/merge_request_resource_event.rb
- `app/services/merge_requests/remove_source_branch_service.rb:4` — file not found at app/services/merge_requests/remove_source_branch_service.rb
- `app/graphql/mutations/merge_requests/merge.rb:3` — file not found at app/graphql/mutations/merge_requests/merge.rb
- `app/graphql/mutations/merge_requests/close.rb:3` — file not found at app/graphql/mutations/merge_requests/close.rb
- `app/graphql/mutations/merge_requests/reopen.rb:3` — file not found at app/graphql/mutations/merge_requests/reopen.rb
- `app/graphql/mutations/merge_requests/lock.rb:3` — file not found at app/graphql/mutations/merge_requests/lock.rb
- `app/graphql/resolvers/merge_requests.rb:3` — file not found at app/graphql/resolvers/merge_requests.rb
- `app/graphql/resolvers/merge_requests/mergeability_check.rb:3` — file not found at app/graphql/resolvers/merge_requests/mergeability_check.rb
- `app/graphql/resolvers/merge_requests/pipelines.rb:3` — file not found at app/graphql/resolvers/merge_requests/pipelines.rb
- `app/graphql/resolvers/merge_requests/related_issues.rb:3` — file not found at app/graphql/resolvers/merge_requests/related_issues.rb
- `app/graphql/resolvers/merge_requests/approvals.rb:3` — file not found at app/graphql/resolvers/merge_requests/approvals.rb
- `app/graphql/subscriptions/merge_requests/merge_status_updated.rb:3` — file not found at app/graphql/subscriptions/merge_requests/merge_status_updated.rb
- `app/helpers/award_emojis_helper.rb:10` — file not found at app/helpers/award_emojis_helper.rb
- `lib/gitlab/merge_request_mergeability_check.rb:3` — file not found at lib/gitlab/merge_request_mergeability_check.rb
- `lib/gitlab/merge_request_diff.rb:3` — file not found at lib/gitlab/merge_request_diff.rb
- `lib/gitlab/merge_request_reviewers.rb:3` — file not found at lib/gitlab/merge_request_reviewers.rb

### sense/gitlabhq  — 284/337 grounded

**Hallucinated**
- `app/policies/merge_request_policy.rb:57` — line 57 out of range (file only 56 lines)
- `app/policies/merge_request_policy.rb:63` — line 63 out of range (file only 56 lines)
- `app/graphql/types/permission_types/merge_request.rb:41` — line 41 out of range (file only 35 lines)
- `app/graphql/types/permission_types/merge_request.rb:47` — line 47 out of range (file only 35 lines)
- `app/graphql/types/permission_types/merge_request.rb:53` — line 53 out of range (file only 35 lines)
- `app/graphql/types/permission_types/merge_request.rb:59` — line 59 out of range (file only 35 lines)

**Unresolved**
- `app/workers/merge_requests/update_head_pipeline_for_merge_request_worker.rb:12` — file not found at app/workers/merge_requests/update_head_pipeline_for_merge_request_worker.rb
- `app/assets/javascripts/notes.js:23` — file not found at app/assets/javascripts/notes.js
- `app/services/merge_requests/cleanup_ref_worker.rb:26` — file not found at app/services/merge_requests/cleanup_ref_worker.rb
- `app/services/merge_requests/refresh/notify_about_push_worker.rb:15` — file not found at app/services/merge_requests/refresh/notify_about_push_worker.rb
- `app/services/merge_requests/merge_data.rb:4` — file not found at app/services/merge_requests/merge_data.rb
- `app/services/auto_merge/enable_service.rb:4` — file not found at app/services/auto_merge/enable_service.rb
- `app/services/auto_merge/merge_service.rb:4` — file not found at app/services/auto_merge/merge_service.rb
- `app/services/auto_merge/process_worker.rb:12` — file not found at app/services/auto_merge/process_worker.rb
- `app/services/issuable/base_service.rb:404` — file not found at app/services/issuable/base_service.rb
- `app/controllers/projects/merge_requests/mergeability_check_controller.rb:3` — file not found at app/controllers/projects/merge_requests/mergeability_check_controller.rb
- `app/controllers/projects/merge_requests/metadata_controller.rb:3` — file not found at app/controllers/projects/merge_requests/metadata_controller.rb
- `app/graphql/merge_requests/base_mutation.rb:4` — file not found at app/graphql/merge_requests/base_mutation.rb
- `app/graphql/merge_requests/merge_mutation.rb:4` — file not found at app/graphql/merge_requests/merge_mutation.rb
- `app/graphql/merge_requests/close_mutation.rb:4` — file not found at app/graphql/merge_requests/close_mutation.rb
- `app/graphql/merge_requests/rebase_mutation.rb:4` — file not found at app/graphql/merge_requests/rebase_mutation.rb
- `app/serializers/merge_request_entity.rb:5` — file not found at app/serializers/merge_request_entity.rb
- `lib/api/merge_request_approval_rules.rb:4` — file not found at lib/api/merge_request_approval_rules.rb
- `lib/api/merge_request_approval_settings.rb:4` — file not found at lib/api/merge_request_approval_settings.rb
- `lib/api/merge_request_mergeability_checks.rb:4` — file not found at lib/api/merge_request_mergeability_checks.rb
- `lib/api/merge_request_reviews.rb:4` — file not found at lib/api/merge_request_reviews.rb
- `lib/api/merge_request_approval_rule_groups.rb:4` — file not found at lib/api/merge_request_approval_rule_groups.rb
- `lib/banzai/upload_controller.rb:4` — file not found at lib/banzai/upload_controller.rb
- `lib/gitlab/analytics/cycle_analytics/stage_events/code_stage_end.rb:16` — file not found at lib/gitlab/analytics/cycle_analytics/stage_events/code_stage_end.rb
- `lib/gitlab/analytics/cycle_analytics/stage_events/code_stage_duration.rb:16` — file not found at lib/gitlab/analytics/cycle_analytics/stage_events/code_stage_duration.rb
- `lib/gitlab/analytics/cycle_analytics/stage_events/code_stage_rework.rb:16` — file not found at lib/gitlab/analytics/cycle_analytics/stage_events/code_stage_rework.rb
- `lib/gitlab/analytics/cycle_analytics/stage_events/code_stage_deployment.rb:16` — file not found at lib/gitlab/analytics/cycle_analytics/stage_events/code_stage_deployment.rb
- `lib/gitlab/analytics/cycle_analytics/stage_events/code_stage_release.rb:16` — file not found at lib/gitlab/analytics/cycle_analytics/stage_events/code_stage_release.rb
- `lib/gitlab/analytics/cycle_analytics/stage_events/code_stage_monitoring.rb:16` — file not found at lib/gitlab/analytics/cycle_analytics/stage_events/code_stage_monitoring.rb
- `lib/gitlab/issuable/clone/clone_issue_service.rb:45` — file not found at lib/gitlab/issuable/clone/clone_issue_service.rb
- `app/models/integrations/base/hangouts_chat.rb:90` — file not found at app/models/integrations/base/hangouts_chat.rb
- `app/models/integrations/push_data_validations.rb:39` — file not found at app/models/integrations/push_data_validations.rb
- `app/models/integrations/project_test_data.rb:37` — file not found at app/models/integrations/project_test_data.rb
- `ee/app/models/merge_request.rb:3` — file not found at ee/app/models/merge_request.rb
- `ee/app/models/concerns/approvable.rb:3` — file not found at ee/app/models/concerns/approvable.rb
- `ee/app/models/concerns/visible_approvable.rb:3` — file not found at ee/app/models/concerns/visible_approvable.rb
- `ee/app/models/merge_request/approval_rule.rb:4` — file not found at ee/app/models/merge_request/approval_rule.rb
- `ee/app/models/merge_request/approval_rule_group.rb:4` — file not found at ee/app/models/merge_request/approval_rule_group.rb
- `ee/app/models/merge_request/approval_setting.rb:4` — file not found at ee/app/models/merge_request/approval_setting.rb
- `ee/app/models/merge_request/approval_state.rb:4` — file not found at ee/app/models/merge_request/approval_state.rb
- `ee/app/models/merge_request/approval_rule_moved_event.rb:4` — file not found at ee/app/models/merge_request/approval_rule_moved_event.rb
- `ee/app/models/merge_request/approval_rule_removed_event.rb:4` — file not found at ee/app/models/merge_request/approval_rule_removed_event.rb
- `ee/app/models/merge_request/approval_state_event.rb:4` — file not found at ee/app/models/merge_request/approval_state_event.rb
- `ee/app/models/merge_request/approval_settings.rb:4` — file not found at ee/app/models/merge_request/approval_settings.rb
- `ee/app/models/concerns/merge_request_resource_event.rb:3` — file not found at ee/app/models/concerns/merge_request_resource_event.rb
- `spec/graphql/resolvers/merge_requests_spec.rb:3` — file not found at spec/graphql/resolvers/merge_requests_spec.rb
- `app/models/concerns/issiable.rb:401` — file not found at app/models/concerns/issiable.rb
- `app/workers/merge_requests/commit_keep_around_worker.rb:3` — file not found at app/workers/merge_requests/commit_keep_around_worker.rb

### sense/langchainrb  — 1/62 grounded

**Unresolved**
- `/lib/langchain/assistant.rb:14` — file not found at /lib/langchain/assistant.rb
- `/lib/langchain/assistant/llm/adapter.rb:7` — file not found at /lib/langchain/assistant/llm/adapter.rb
- `/lib/langchain/assistant/llm/adapters/base.rb:7` — file not found at /lib/langchain/assistant/llm/adapters/base.rb
- `/lib/langchain/assistant/messages/base.rb:6` — file not found at /lib/langchain/assistant/messages/base.rb
- `/lib/langchain/llm/response/base_response.rb:5` — file not found at /lib/langchain/llm/response/base_response.rb
- `/lib/langchain/assistant.rb:341` — file not found at /lib/langchain/assistant.rb
- `/lib/langchain/assistant/llm/adapters/base.rb:16` — file not found at /lib/langchain/assistant/llm/adapters/base.rb
- `/lib/langchain/assistant/llm/adapters/openai.rb:16` — file not found at /lib/langchain/assistant/llm/adapters/openai.rb
- `/lib/langchain/llm/openai.rb:122` — file not found at /lib/langchain/llm/openai.rb
- `/lib/langchain/llm/openai.rb:142` — file not found at /lib/langchain/llm/openai.rb
- `/lib/langchain/llm/response/openai_response.rb:4` — file not found at /lib/langchain/llm/response/openai_response.rb
- `/lib/langchain/assistant.rb:305` — file not found at /lib/langchain/assistant.rb
- `/lib/langchain/assistant.rb:394` — file not found at /lib/langchain/assistant.rb
- `/lib/langchain/assistant/llm/adapters/openai.rb:42` — file not found at /lib/langchain/assistant/llm/adapters/openai.rb
- `/lib/langchain/assistant/messages/openai_message.rb:6` — file not found at /lib/langchain/assistant/messages/openai_message.rb
- `/lib/langchain/assistant/messages/openai_message.rb:52` — file not found at /lib/langchain/assistant/messages/openai_message.rb
- `/lib/langchain/llm/response/openai_response.rb:27` — file not found at /lib/langchain/llm/response/openai_response.rb
- `/lib/langchain/assistant/llm/adapter.rb:8` — file not found at /lib/langchain/assistant/llm/adapter.rb
- `/lib/langchain/assistant.rb:178` — file not found at /lib/langchain/assistant.rb
- `/lib/langchain/assistant/llm/adapters/openai.rb:81` — file not found at /lib/langchain/assistant/llm/adapters/openai.rb
- `/lib/langchain/assistant/llm/adapters/base.rb:56` — file not found at /lib/langchain/assistant/llm/adapters/base.rb
- `/lib/langchain/assistant/messages/openai_message.rb:15` — file not found at /lib/langchain/assistant/messages/openai_message.rb
- `/lib/langchain/assistant/llm/adapters/openai.rb:49` — file not found at /lib/langchain/assistant/llm/adapters/openai.rb
- `/lib/langchain/assistant.rb:366` — file not found at /lib/langchain/assistant.rb
- `/lib/langchain/assistant.rb:375` — file not found at /lib/langchain/assistant.rb
- `/lib/langchain/assistant.rb:379` — file not found at /lib/langchain/assistant.rb
- `/lib/langchain/assistant/llm/adapters/anthropic.rb:7` — file not found at /lib/langchain/assistant/llm/adapters/anthropic.rb
- `/lib/langchain/assistant/llm/adapters/openai.rb:7` — file not found at /lib/langchain/assistant/llm/adapters/openai.rb
- `/lib/langchain/assistant/llm/adapters/google_gemini.rb:7` — file not found at /lib/langchain/assistant/llm/adapters/google_gemini.rb
- `/lib/langchain/assistant/llm/adapters/mistralai.rb:7` — file not found at /lib/langchain/assistant/llm/adapters/mistralai.rb
- `/lib/langchain/assistant/llm/adapters/ollama.rb:7` — file not found at /lib/langchain/assistant/llm/adapters/ollama.rb
- `/lib/langchain/assistant/llm/adapters/aws_bedrock_anthropic.rb:7` — file not found at /lib/langchain/assistant/llm/adapters/aws_bedrock_anthropic.rb
- `/lib/langchain/assistant/messages/anthropic_message.rb:6` — file not found at /lib/langchain/assistant/messages/anthropic_message.rb
- `/lib/langchain/assistant/messages/google_gemini_message.rb:6` — file not found at /lib/langchain/assistant/messages/google_gemini_message.rb
- `/lib/langchain/assistant/messages/mistralai_message.rb:6` — file not found at /lib/langchain/assistant/messages/mistralai_message.rb
- `/lib/langchain/assistant/messages/ollama_message.rb:6` — file not found at /lib/langchain/assistant/messages/ollama_message.rb
- `/lib/langchain/llm/response/anthropic_response.rb:4` — file not found at /lib/langchain/llm/response/anthropic_response.rb
- `/lib/langchain/llm/response/google_gemini_response.rb:4` — file not found at /lib/langchain/llm/response/google_gemini_response.rb
- `/lib/langchain/llm/response/mistralai_response.rb:4` — file not found at /lib/langchain/llm/response/mistralai_response.rb
- `/lib/langchain/llm/response/ollama_response.rb:4` — file not found at /lib/langchain/llm/response/ollama_response.rb
- `/lib/langchain/llm/response/aws_bedrock_anthropic_response.rb:4` — file not found at /lib/langchain/llm/response/aws_bedrock_anthropic_response.rb
- `/lib/langchain/llm/response/anthropic_response.rb:18` — file not found at /lib/langchain/llm/response/anthropic_response.rb
- `/lib/langchain/llm/base.rb:20` — file not found at /lib/langchain/llm/base.rb
- `/lib/langchain/llm/openai.rb:17` — file not found at /lib/langchain/llm/openai.rb
- `/lib/langchain/llm/anthropic.rb:13` — file not found at /lib/langchain/llm/anthropic.rb
- `/lib/langchain/llm/google_gemini.rb:11` — file not found at /lib/langchain/llm/google_gemini.rb
- `/lib/langchain/llm/google_vertexai.rb:11` — file not found at /lib/langchain/llm/google_vertexai.rb
- `/lib/langchain/llm/mistralai.rb:9` — file not found at /lib/langchain/llm/mistralai.rb
- `/lib/langchain/llm/ollama.rb:13` — file not found at /lib/langchain/llm/ollama.rb
- `/lib/langchain/llm/aws_bedrock.rb:12` — file not found at /lib/langchain/llm/aws_bedrock.rb
- `/lib/langchain/llm/cohere.rb:10` — file not found at /lib/langchain/llm/cohere.rb
- `/lib/langchain/llm/replicate.rb:12` — file not found at /lib/langchain/llm/replicate.rb
- `/lib/langchain/llm/hugging_face.rb:10` — file not found at /lib/langchain/llm/hugging_face.rb
- `/lib/langchain/llm/ai21.rb:10` — file not found at /lib/langchain/llm/ai21.rb
- `/lib/langchain/llm/llama_cpp.rb:17` — file not found at /lib/langchain/llm/llama_cpp.rb
- `/lib/langchain/llm/openai.rb:34` — file not found at /lib/langchain/llm/openai.rb
- `/lib/langchain/llm/openai.rb:39` — file not found at /lib/langchain/llm/openai.rb
- `/lib/langchain/llm/openai.rb:45` — file not found at /lib/langchain/llm/openai.rb
- `/lib/langchain/llm/openai.rb:148` — file not found at /lib/langchain/llm/openai.rb
- `/lib/langchain/assistant/llm/adapter.rb:9` — file not found at /lib/langchain/assistant/llm/adapter.rb
- `/lib/langchain/llm/base.rb:16` — file not found at /lib/langchain/llm/base.rb

### sense/llm.rb  — 37/45 grounded

**Unresolved**
- `lib/llm/providers/foo.rb:1` — file not found at lib/llm/providers/foo.rb
- `lib/llm/providers/foo/request_adapter.rb:1` — file not found at lib/llm/providers/foo/request_adapter.rb
- `lib/llm/providers/foo/request_adapter/completion.rb:1` — file not found at lib/llm/providers/foo/request_adapter/completion.rb
- `lib/llm/providers/foo/response_adapter.rb:1` — file not found at lib/llm/providers/foo/response_adapter.rb
- `lib/llm/providers/foo/response_adapter/completion.rb:1` — file not found at lib/llm/providers/foo/response_adapter/completion.rb
- `lib/llm/providers/foo/stream_parser.rb:1` — file not found at lib/llm/providers/foo/stream_parser.rb
- `lib/llm/providers/foo/error_handler.rb:1` — file not found at lib/llm/providers/foo/error_handler.rb
- `_spec.rb:1` — file not found at _spec.rb

### sense/llm.rb  — 0/71 grounded

**Unresolved**
- `/lib/llm.rb:152` — file not found at /lib/llm.rb
- `/lib/llm.rb:25` — file not found at /lib/llm.rb
- `/lib/llm/provider.rb:8` — file not found at /lib/llm/provider.rb
- `/lib/llm/context.rb:36` — file not found at /lib/llm/context.rb
- `/lib/llm/providers/openai.rb:16` — file not found at /lib/llm/providers/openai.rb
- `/lib/llm/context.rb:82` — file not found at /lib/llm/context.rb
- `/lib/llm/context.rb:193` — file not found at /lib/llm/context.rb
- `/lib/llm/providers/openai.rb:73` — file not found at /lib/llm/providers/openai.rb
- `/lib/llm/providers/openai.rb:222` — file not found at /lib/llm/providers/openai.rb
- `/lib/llm/providers/openai.rb:77` — file not found at /lib/llm/providers/openai.rb
- `/lib/llm/response.rb:32` — file not found at /lib/llm/response.rb
- `/lib/llm/context.rb:200` — file not found at /lib/llm/context.rb
- `/lib/llm/providers/openai/stream_parser.rb:16` — file not found at /lib/llm/providers/openai/stream_parser.rb
- `/lib/llm/providers/openai/error_handler.rb:34` — file not found at /lib/llm/providers/openai/error_handler.rb
- `/lib/llm/providers/anthropic.rb:16` — file not found at /lib/llm/providers/anthropic.rb
- `/lib/llm/providers/google.rb:20` — file not found at /lib/llm/providers/google.rb
- `/lib/llm/providers/bedrock.rb:33` — file not found at /lib/llm/providers/bedrock.rb
- `/lib/llm/providers/ollama.rb:18` — file not found at /lib/llm/providers/ollama.rb
- `/lib/llm/providers/xai.rb:17` — file not found at /lib/llm/providers/xai.rb
- `/lib/llm/providers/zai.rb:16` — file not found at /lib/llm/providers/zai.rb
- `/lib/llm/providers/llamacpp.rb:22` — file not found at /lib/llm/providers/llamacpp.rb
- `/lib/llm/provider.rb:55` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:70` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:94` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:126` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:133` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:140` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:147` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:154` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:161` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:168` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:176` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:183` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:362` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:371` — file not found at /lib/llm/provider.rb
- `/lib/llm/provider.rb:384` — file not found at /lib/llm/provider.rb
- `/lib/llm/providers/openai/error_handler.rb:6` — file not found at /lib/llm/providers/openai/error_handler.rb
- `/lib/llm/providers/anthropic/error_handler.rb:6` — file not found at /lib/llm/providers/anthropic/error_handler.rb
- `/lib/llm/providers/google/error_handler.rb:6` — file not found at /lib/llm/providers/google/error_handler.rb
- `/lib/llm/providers/bedrock/error_handler.rb:6` — file not found at /lib/llm/providers/bedrock/error_handler.rb
- `/lib/llm/providers/ollama/error_handler.rb:6` — file not found at /lib/llm/providers/ollama/error_handler.rb
- `/lib/llm/providers/openai/stream_parser.rb:6` — file not found at /lib/llm/providers/openai/stream_parser.rb
- `/lib/llm/providers/anthropic/stream_parser.rb:6` — file not found at /lib/llm/providers/anthropic/stream_parser.rb
- `/lib/llm/providers/google/stream_parser.rb:6` — file not found at /lib/llm/providers/google/stream_parser.rb
- `/lib/llm/providers/bedrock/stream_parser.rb:19` — file not found at /lib/llm/providers/bedrock/stream_parser.rb
- `/lib/llm/providers/openai/request_adapter.rb:6` — file not found at /lib/llm/providers/openai/request_adapter.rb
- `/lib/llm/providers/anthropic/request_adapter.rb:6` — file not found at /lib/llm/providers/anthropic/request_adapter.rb
- `/lib/llm/providers/google/request_adapter.rb:6` — file not found at /lib/llm/providers/google/request_adapter.rb
- `/lib/llm/providers/ollama/request_adapter.rb:6` — file not found at /lib/llm/providers/ollama/request_adapter.rb
- `/lib/llm/providers/openai/response_adapter.rb:6` — file not found at /lib/llm/providers/openai/response_adapter.rb
- `/lib/llm/providers/anthropic/response_adapter.rb:6` — file not found at /lib/llm/providers/anthropic/response_adapter.rb
- `/lib/llm/providers/google/response_adapter.rb:6` — file not found at /lib/llm/providers/google/response_adapter.rb
- `/lib/llm/providers/ollama/response_adapter.rb:6` — file not found at /lib/llm/providers/ollama/response_adapter.rb
- `/lib/llm/stream.rb:62` — file not found at /lib/llm/stream.rb
- `/lib/llm/providers/anthropic/stream_parser.rb:27` — file not found at /lib/llm/providers/anthropic/stream_parser.rb
- `/lib/llm/providers/bedrock/stream_parser.rb:42` — file not found at /lib/llm/providers/bedrock/stream_parser.rb
- `/lib/llm/providers/google/stream_parser.rb:28` — file not found at /lib/llm/providers/google/stream_parser.rb
- `/lib/llm/providers/openai/error_handler.rb:51` — file not found at /lib/llm/providers/openai/error_handler.rb
- `/lib/llm/providers/anthropic/error_handler.rb:51` — file not found at /lib/llm/providers/anthropic/error_handler.rb
- `/lib/llm/providers/google/error_handler.rb:51` — file not found at /lib/llm/providers/google/error_handler.rb
- `/lib/llm/providers/bedrock/error_handler.rb:45` — file not found at /lib/llm/providers/bedrock/error_handler.rb
- `/lib/llm/error.rb:6` — file not found at /lib/llm/error.rb
- `/lib/llm/providers/openai.rb:17` — file not found at /lib/llm/providers/openai.rb
- `/lib/llm.rb:237` — file not found at /lib/llm.rb
- `/lib/llm/providers/deepllama.rb:16` — file not found at /lib/llm/providers/deepllama.rb
- `/lib/llm/providers/deepllama.rb:38` — file not found at /lib/llm/providers/deepllama.rb
- `/lib/llm/providers/deepllama.rb:89` — file not found at /lib/llm/providers/deepllama.rb
- `/lib/llm/agent.rb:36` — file not found at /lib/llm/agent.rb
- `/lib/llm/stream.rb:23` — file not found at /lib/llm/stream.rb
- `/lib/llm/response.rb:19` — file not found at /lib/llm/response.rb
- `/lib/llm/contract/completion.rb:7` — file not found at /lib/llm/contract/completion.rb

### sense/lobsters  — 112/113 grounded

**Hallucinated**
- `link.rb:1132` — line 1132 out of range (file only 95 lines) [via app/models/link.rb]

### sense/mastodon  — 142/143 grounded

**Hallucinated**
- `app/workers/local_notification_worker.rb:80` — line 80 out of range (file only 23 lines)

### sense/rails  — 57/58 grounded

**Unresolved**
- `activerecord/lib/active_record/predicate_builder.rb:2` — file not found at activerecord/lib/active_record/predicate_builder.rb

### sense/rails  — 39/40 grounded

**Unresolved**
- `mysql_adapter.rb:559` — file not found at mysql_adapter.rb

### sense/raix  — 36/42 grounded

**Unresolved**
- `FunctionDispatch.rb:46` — file not found at FunctionDispatch.rb
- `ChatCompletion.rb:91` — file not found at ChatCompletion.rb
- `FunctionToolAdapter.rb:50` — file not found at FunctionToolAdapter.rb
- `ChatCompletion.rb:303` — file not found at ChatCompletion.rb
- `FunctionToolAdapter.rb:6` — file not found at FunctionToolAdapter.rb
- `SseClient.rb:32` — file not found at SseClient.rb

### sense/redmine  — 17/18 grounded

**Unresolved**
- `IssueNestedSet.rb:22` — file not found at IssueNestedSet.rb

### sense/redmine  — 189/193 grounded

**Hallucinated**
- `app/models/issue.rb:2333` — line 2333 out of range (file only 2151 lines)

**Unresolved**
- `app/models/gantt_helper.rb:89` — file not found at app/models/gantt_helper.rb
- `app/models/issues_controller.rb:257` — file not found at app/models/issues_controller.rb
- `app/models/issues_controller.rb:259` — file not found at app/models/issues_controller.rb

### sense/ruby_llm  — 49/50 grounded

**Hallucinated**
- `lib/ruby_llm/protocols/gemini.rb:18` — line 18 out of range (file only 17 lines)

### sense/solidus  — 141/147 grounded

**Hallucinated**
- `core/app/models/spree/payment.rb:382` — line 382 out of range (file only 238 lines)
- `promotions/app/models/solidus_promotions/order_adjuster.rb:39` — line 39 out of range (file only 32 lines)

**Unresolved**
- `core/app/models/spree/metadata.rb:4` — file not found at core/app/models/spree/metadata.rb
- `core/app/controllers/spree/admin/orders_controller.rb:5` — file not found at core/app/controllers/spree/admin/orders_controller.rb
- `core/app/controllers/spree/admin/cancellations_controller.rb:6` — file not found at core/app/controllers/spree/admin/cancellations_controller.rb
- `core/app/controllers/spree/admin/cancellations_controller.rb:36` — file not found at core/app/controllers/spree/admin/cancellations_controller.rb
