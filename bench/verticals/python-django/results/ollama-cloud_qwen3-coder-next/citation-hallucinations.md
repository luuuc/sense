# Citation hallucinations

Citations the answer printed that did not resolve against the repo checked out at the benchmarked commit. **Hallucinated** = line number beyond end-of-file (a made-up number). **Unresolved** = file not in the repo, or symbol not within ±5 lines of the cited line.

Reported for transparency; not folded into the headline score.

## _backup_healthchecks_session2_20260710

### _backup_healthchecks_session2_20260710/sense  — 46/47 grounded

**Unresolved**
- `hc/integrations/prometheus/transport.py:8` — file not found at hc/integrations/prometheus/transport.py

## _backup_litellm_session2_20260710

### _backup_litellm_session2_20260710/baseline  — 111/112 grounded

**Unresolved**
- `litellm/llms/intercom/chat/transformation.py:27` — file not found at litellm/llms/intercom/chat/transformation.py

### _backup_litellm_session2_20260710/baseline  — 154/170 grounded

**Unresolved**
- `litellm/llms/deepinfra/embedding/transformation.py:14` — file not found at litellm/llms/deepinfra/embedding/transformation.py
- `litellm/llms/xinference/chat/transformation.py:13` — file not found at litellm/llms/xinference/chat/transformation.py
- `litellm/llms/xinference/embedding/transformation.py:12` — file not found at litellm/llms/xinference/embedding/transformation.py
- `litellm/llms/mistralai/chat/transformation.py:18` — file not found at litellm/llms/mistralai/chat/transformation.py
- `litellm/llms/mistralai/embedding/transformation.py:13` — file not found at litellm/llms/mistralai/embedding/transformation.py
- `litellm/llms/groq/embedding/transformation.py:13` — file not found at litellm/llms/groq/embedding/transformation.py
- `litellm/llms/intercom/chat/transformation.py:27` — file not found at litellm/llms/intercom/chat/transformation.py
- `litellm/llms/nlp_cloud/completion/transformation.py:16` — file not found at litellm/llms/nlp_cloud/completion/transformation.py
- `litellm/llms/elasticsearch/embedding/transformation.py:13` — file not found at litellm/llms/elasticsearch/embedding/transformation.py
- `litellm/llms/petals/chat/transformation.py:18` — file not found at litellm/llms/petals/chat/transformation.py
- `litellm/llms/azure_ai/passthrough/transformation.py:13` — file not found at litellm/llms/azure_ai/passthrough/transformation.py
- `litellm/llms/vertex_ai/passthrough/transformation.py:13` — file not found at litellm/llms/vertex_ai/passthrough/transformation.py
- `litellm/llms/gemini/passthrough/transformation.py:13` — file not found at litellm/llms/gemini/passthrough/transformation.py
- `litellm/llms/mistralai/passthrough/transformation.py:12` — file not found at litellm/llms/mistralai/passthrough/transformation.py
- `litellm/llms/openai/passthrough/transformation.py:12` — file not found at litellm/llms/openai/passthrough/transformation.py
- `litellm/llms/groq/passthrough/transformation.py:12` — file not found at litellm/llms/groq/passthrough/transformation.py

### _backup_litellm_session2_20260710/sense  — 8/11 grounded

**Unresolved**
- `transformation.py:transform_request` — ambiguous: 317 files match `transformation.py` (tests/test_litellm/llms/bedrock/rerank/transformation.py, litellm/interactions/litellm_responses_transformation/transformation.py, litellm/endpoints/speech/speech_to_completion_bridge/transformation.py...)
- `openai.py:transform_request` — ambiguous: 2 files match `openai.py` (litellm/types/llms/openai.py, litellm/llms/openai/openai.py)
- `gpt_transformation.py:transform_request` — ambiguous: 6 files match `gpt_transformation.py` (litellm/llms/azure/chat/gpt_transformation.py, litellm/llms/azure/image_generation/gpt_transformation.py, litellm/llms/azure_ai/image_generation/gpt_transformation.py...)

## _backup_saleor_session2_20260710

### _backup_saleor_session2_20260710/baseline  — 128/130 grounded

**Hallucinated**
- `saleor/csv/utils/products_data.py:764` — line 764 out of range (file only 574 lines)
- `saleor/csv/utils/products_data.py:834` — line 834 out of range (file only 574 lines)

### _backup_saleor_session2_20260710/baseline  — 183/184 grounded

**Hallucinated**
- `saleor/warehouse/models.py:707` — line 707 out of range (file only 689 lines)

### _backup_saleor_session2_20260710/sense  — 166/169 grounded

**Hallucinated**
- `saleor/warehouse/management.py:1111` — line 1111 out of range (file only 1077 lines)
- `saleor/webhook/serializers.py:195` — line 195 out of range (file only 194 lines)

**Unresolved**
- `saleor/plugin/manager.py:356` — file not found at saleor/plugin/manager.py

## _backup_wagtail_3102cell_20260710

### _backup_wagtail_3102cell_20260710/baseline  — 195/202 grounded

**Hallucinated**
- `wagtail/admin/api/actions/move.py:64` — line 64 out of range (file only 53 lines)
- `wagtail/admin/api/actions/delete.py:38` — line 38 out of range (file only 26 lines)
- `wagtail/admin/api/actions/publish.py:40` — line 40 out of range (file only 34 lines)
- `wagtail/admin/api/actions/unpublish.py:43` — line 43 out of range (file only 35 lines)
- `wagtail/admin/panels/signal_handlers.py:41` — line 41 out of range (file only 19 lines)
- `wagtail/admin/views/pages/workflow.py:301` — line 301 out of range (file only 56 lines)
- `wagtail/models/content_types.py:35` — line 35 out of range (file only 11 lines)

### _backup_wagtail_3102cell_20260710/sense  — 234/249 grounded

**Hallucinated**
- `wagtail/actions/move_page.py:1724` — line 1724 out of range (file only 107 lines)
- `wagtail/actions/delete_page.py:822` — line 822 out of range (file only 49 lines)
- `wagtail/actions/publish_page_revision.py:1228` — line 1228 out of range (file only 63 lines)
- `wagtail/actions/unpublish_page.py:1237` — line 1237 out of range (file only 69 lines)
- `wagtail/contrib/frontend_cache/models.py:5` — line 5 out of range (file only 0 lines)
- `wagtail/contrib/sitemaps/models.py:1` — line 1 out of range (file only 0 lines)
- `wagtail/rich_text/pages.py:174` — line 174 out of range (file only 38 lines)
- `wagtail/admin/api/views.py:580` — line 580 out of range (file only 163 lines)
- `wagtail/admin/api/views.py:628` — line 628 out of range (file only 163 lines)

**Unresolved**
- `wagtail/contrib/table_block/models.py:1` — file not found at wagtail/contrib/table_block/models.py
- `wagtail/contrib/typed_table_block/models.py:1` — file not found at wagtail/contrib/typed_table_block/models.py
- `wagtail/admin/api/v2/views.py:580` — file not found at wagtail/admin/api/v2/views.py
- `wagtail/admin/api/v2/views.py:628` — file not found at wagtail/admin/api/v2/views.py
- `wagtail/admin/templetags/wagtailuserbar.py:9` — file not found at wagtail/admin/templetags/wagtailuserbar.py
- `wagtail/admin/api/v2/views.py:518` — file not found at wagtail/admin/api/v2/views.py

## _backup_wagtail_degraded_window_20260710

### _backup_wagtail_degraded_window_20260710/baseline  — 56/143 grounded

**Hallucinated**
- `wagtail/models/view_restrictions.py:2471` — line 2471 out of range (file only 81 lines)
- `wagtail/management/commands/set_url_paths.py:30` — line 30 out of range (file only 17 lines)

**Unresolved**
- `wagtail/models/audit_log.py:No` — `No` not found anywhere in wagtail/models/audit_log.py
- `wagtail/models/locking.py:No` — `No` not found anywhere in wagtail/models/locking.py
- `wagtail/models/collections.py:No` — `No` not found anywhere in wagtail/models/collections.py
- `wagtail/models/media.py:No` — `No` not found anywhere in wagtail/models/media.py
- `wagtail/models/orderable.py:No` — `No` not found anywhere in wagtail/models/orderable.py
- `wagtail/models/preview.py:No` — `No` not found anywhere in wagtail/models/preview.py
- `wagtail/models/reference_index.py:No` — `No` not found anywhere in wagtail/models/reference_index.py
- `wagtail/models/copying.py:No` — `No` not found anywhere in wagtail/models/copying.py
- `wagtail/models/draft_state.py:No` — `No` not found anywhere in wagtail/models/draft_state.py
- `wagtail/models/specific.py:No` — `No` not found anywhere in wagtail/models/specific.py
- `wagtail/models/panels.py:No` — `No` not found anywhere in wagtail/models/panels.py
- `wagtail/admin/views/pages/list.py:Page.objects.filter` — file not found at wagtail/admin/views/pages/list.py
- `wagtail/admin/views/pages/create.py:Page.objects.filter` — `filter` not found anywhere in wagtail/admin/views/pages/create.py
- `wagtail/admin/views/pages/delete.py:Page.objects.get` — `get` not found anywhere in wagtail/admin/views/pages/delete.py
- `wagtail/admin/views/pages/publish.py:Page.objects.get` — file not found at wagtail/admin/views/pages/publish.py
- `wagtail/admin/views/pages/revisions.py:Revision.objects.filter` — `filter` not found anywhere in wagtail/admin/views/pages/revisions.py
- `wagtail/admin/views/pages/move.py:Page.objects.get` — `get` not found anywhere in wagtail/admin/views/pages/move.py
- `wagtail/admin/views/pages/usage.py:Page.objects.filter` — `filter` not found anywhere in wagtail/admin/views/pages/usage.py
- `wagtail/admin/views/pages/history.py:Page.objects.get` — `get` not found anywhere in wagtail/admin/views/pages/history.py
- `wagtail/admin/views/pages/workflow.py:Page.objects.filter` — `filter` not found anywhere in wagtail/admin/views/pages/workflow.py
- `wagtail/admin/views/pages/lock.py:Page.objects.get` — `get` not found anywhere in wagtail/admin/views/pages/lock.py
- `wagtail/admin/views/pages/unlock.py:Page.objects.get` — file not found at wagtail/admin/views/pages/unlock.py
- `wagtail/admin/views/pages/privacy.py:Page.objects.get` — file not found at wagtail/admin/views/pages/privacy.py
- `wagtail/admin/views/pages/prefix.py:Page.objects.filter` — file not found at wagtail/admin/views/pages/prefix.py
- `wagtail/admin/views/pages/operations.py:Page.objects.get` — file not found at wagtail/admin/views/pages/operations.py
- `wagtail/admin/views/pages/audit.py:Page.objects.filter` — file not found at wagtail/admin/views/pages/audit.py
- `wagtail/admin/views/pages/notifications.py:Page.objects.filter` — file not found at wagtail/admin/views/pages/notifications.py
- `wagtail/admin/forms/models.py:Page.objects.filter` — `filter` not found anywhere in wagtail/admin/forms/models.py
- `wagtail/admin/panels/page_utils.py:Page.objects.get` — `get` not found anywhere in wagtail/admin/panels/page_utils.py
- `wagtail/admin/panels/page_chooser_panel.py:Page.objects.filter` — `filter` not found anywhere in wagtail/admin/panels/page_chooser_panel.py
- `wagtail/admin/ui/menus/pages.py:Page.objects.filter` — `filter` not found anywhere in wagtail/admin/ui/menus/pages.py
- `wagtail/admin/ui/tables/pages.py:Page.objects.filter` — `filter` not found anywhere in wagtail/admin/ui/tables/pages.py
- `wagtail/admin/widgets/chooser.py:Page.objects.filter` — `filter` not found anywhere in wagtail/admin/widgets/chooser.py
- `wagtail/api/v2/endpoints.py:PagesAPIViewSet` — file not found at wagtail/api/v2/endpoints.py
- `wagtail/api/v2/utils.py:Page.objects.get` — `get` not found anywhere in wagtail/api/v2/utils.py
- `wagtail/contrib/forms/urls.py:FormPage` — `FormPage` not found anywhere in wagtail/contrib/forms/urls.py
- `wagtail/contrib/redirects/models.py:No` — `No` not found anywhere in wagtail/contrib/redirects/models.py
- `wagtail/contrib/redirects/signal_handlers.py:No` — `No` not found anywhere in wagtail/contrib/redirects/signal_handlers.py
- `wagtail/contrib/sitemaps/sitemap_generator.py:Page.objects.descendant_of` — `descendant_of` not found anywhere in wagtail/contrib/sitemaps/sitemap_generator.py
- `wagtail/contrib/frontend_cache/models.py:No` — `No` not found anywhere in wagtail/contrib/frontend_cache/models.py
- `wagtail/contrib/simple_translation/models.py:Translation` — `Translation` not found anywhere in wagtail/contrib/simple_translation/models.py
- `wagtail/contrib/settings/models.py:No` — `No` not found anywhere in wagtail/contrib/settings/models.py
- `wagtail/contrib/styleguide/models.py:No` — `No` not found anywhere in wagtail/contrib/styleguide/models.py
- `wagtail/contrib/search_promotions/urls.py:SearchPromotion` — file not found at wagtail/contrib/search_promotions/urls.py
- `wagtail/search/index.py:Generic` — `Generic` not found anywhere in wagtail/search/index.py
- `wagtail/search/queryset.py:PageQuerySet` — `PageQuerySet` not found anywhere in wagtail/search/queryset.py
- `wagtail/search/models.py:SearchQuery` — `SearchQuery` not found anywhere in wagtail/search/models.py
- `wagtail/search/views.py:search_view` — file not found at wagtail/search/views.py
- `wagtail/rich_text/feature_registry.py:No` — `No` not found anywhere in wagtail/rich_text/feature_registry.py
- `wagtail/rich_text/rewriters.py:No` — `No` not found anywhere in wagtail/rich_text/rewriters.py
- `wagtail/rich_text/__init__.py:No` — `No` not found anywhere in wagtail/rich_text/__init__.py
- `wagtail/rich_text/forms.py:No` — file not found at wagtail/rich_text/forms.py
- `wagtail/rich_text/fields.py:No` — file not found at wagtail/rich_text/fields.py
- `wagtail/rich_text/blocks.py:No` — file not found at wagtail/rich_text/blocks.py
- `wagtail/users/views/groups.py:No` — `No` not found anywhere in wagtail/users/views/groups.py
- `wagtail/users/views/users.py:No` — `No` not found anywhere in wagtail/users/views/users.py
- `wagtail/users/panels.py:No` — file not found at wagtail/users/panels.py
- `wagtail/users/urls.py:No` — file not found at wagtail/users/urls.py
- `wagtail/embeds/models.py:No` — `No` not found anywhere in wagtail/embeds/models.py
- `wagtail/embeds/views/chooser.py:No` — `No` not found anywhere in wagtail/embeds/views/chooser.py
- `wagtail/embeds/embeds.py:No` — `No` not found anywhere in wagtail/embeds/embeds.py
- `wagtail/embeds/format.py:No` — `No` not found anywhere in wagtail/embeds/format.py
- `wagtail/documents/models.py:No` — `No` not found anywhere in wagtail/documents/models.py
- `wagtail/documents/panels.py:No` — file not found at wagtail/documents/panels.py
- `wagtail/documents/forms.py:No` — `No` not found anywhere in wagtail/documents/forms.py
- `wagtail/images/panels.py:No` — file not found at wagtail/images/panels.py
- `wagtail/images/forms.py:No` — `No` not found anywhere in wagtail/images/forms.py
- `wagtail/users/models.py:No` — `No` not found anywhere in wagtail/users/models.py
- `wagtail/views.py:Page.objects.filter` — `filter` not found anywhere in wagtail/views.py
- `wagtail/urls.py:Page` — `Page` not found anywhere in wagtail/urls.py
- `wagtail/signal_handlers.py:Page.objects.filter` — `filter` not found anywhere in wagtail/signal_handlers.py
- `wagtail/log_actions.py:No` — `No` not found anywhere in wagtail/log_actions.py
- `wagtail/permissions.py:Page.objects.filter` — `filter` not found anywhere in wagtail/permissions.py
- `wagtail/jinja2tags.py:No` — `No` not found anywhere in wagtail/jinja2tags.py
- `wagtail/forms.py:No` — `No` not found anywhere in wagtail/forms.py
- `wagtail/management/commands/set_url_paths.py:Page.objects.all` — `all` not found anywhere in wagtail/management/commands/set_url_paths.py
- `wagtail/actions/delete_page.py:Page.objects.get` — `get` not found anywhere in wagtail/actions/delete_page.py
- `wagtail/actions/publish_page_revision.py:Revision.objects.get` — `get` not found anywhere in wagtail/actions/publish_page_revision.py
- `wagtail/actions/create_alias.py:Page.objects.filter` — `filter` not found anywhere in wagtail/actions/create_alias.py
- `wagtail/actions/copy_for_translation.py:Page.objects.get` — `get` not found anywhere in wagtail/actions/copy_for_translation.py
- `wagtail/templatetags/wagtailuser_tags.py:No` — file not found at wagtail/templatetags/wagtailuser_tags.py
- `wagtail/templatetags/wagtailsearch_tags.py:No` — file not found at wagtail/templatetags/wagtailsearch_tags.py
- `wagtail/rich_text/converters/html_to_contentstate.py:236` — file not found at wagtail/rich_text/converters/html_to_contentstate.py
- `wagtail/search/index.py:generic` — `generic` not found anywhere in wagtail/search/index.py
- `wagtail/models/pages.py:Lines` — `Lines` not found anywhere in wagtail/models/pages.py

### _backup_wagtail_degraded_window_20260710/baseline  — 188/192 grounded

**Hallucinated**
- `wagtail/admin/api/serializers.py:368` — line 368 out of range (file only 192 lines)
- `wagtail/admin/api/views.py:529` — line 529 out of range (file only 163 lines)

**Unresolved**
- `wagtail/models/views.py:166` — file not found at wagtail/models/views.py
- `wagtail/models/views.py:184` — file not found at wagtail/models/views.py

### _backup_wagtail_degraded_window_20260710/sense  — 286/288 grounded

**Hallucinated**
- `wagtail/templatetags/wagtailcore_tags.py:299` — line 299 out of range (file only 216 lines)

**Unresolved**
- `wagail/actions/move_page.py:41` — file not found at wagail/actions/move_page.py

### _backup_wagtail_degraded_window_20260710/sense  — 355/389 grounded

**Hallucinated**
- `wagtail/models/pages.py:2859` — line 2859 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2865` — line 2865 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2871` — line 2871 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2877` — line 2877 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2883` — line 2883 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2889` — line 2889 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2895` — line 2895 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2901` — line 2901 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2907` — line 2907 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2913` — line 2913 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2919` — line 2919 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2925` — line 2925 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2931` — line 2931 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2937` — line 2937 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2943` — line 2943 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2949` — line 2949 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2955` — line 2955 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2961` — line 2961 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2967` — line 2967 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2973` — line 2973 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2979` — line 2979 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2985` — line 2985 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2991` — line 2991 out of range (file only 2858 lines)
- `wagtail/models/pages.py:2997` — line 2997 out of range (file only 2858 lines)
- `wagtail/models/pages.py:3003` — line 3003 out of range (file only 2858 lines)
- `wagtail/models/pages.py:3009` — line 3009 out of range (file only 2858 lines)
- `wagtail/models/pages.py:3015` — line 3015 out of range (file only 2858 lines)
- `wagtail/models/pages.py:3021` — line 3021 out of range (file only 2858 lines)
- `wagtail/models/pages.py:3027` — line 3027 out of range (file only 2858 lines)
- `wagtail/models/pages.py:3033` — line 3033 out of range (file only 2858 lines)
- `wagtail/models/pages.py:3039` — line 3039 out of range (file only 2858 lines)
- `wagtail/models/pages.py:3045` — line 3045 out of range (file only 2858 lines)
- `wagtail/models/pages.py:3051` — line 3051 out of range (file only 2858 lines)

**Unresolved**
- `/wagtail/models/pages.py:289` — file not found at /wagtail/models/pages.py

## _invalid_netbox_20260710

### _invalid_netbox_20260710/baseline  — 15/19 grounded

**Hallucinated**
- `dcim/signals.py:1150` — line 1150 out of range (file only 273 lines) [via netbox/dcim/signals.py]
- `extras/api/serializers_/configcontexts.py:166` — line 166 out of range (file only 157 lines) [via netbox/extras/api/serializers_/configcontexts.py]
- `extras/models/__init__.py:1200` — line 1200 out of range (file only 8 lines) [via netbox/extras/models/__init__.py]
- `dcim/signals.py:1073` — line 1073 out of range (file only 273 lines) [via netbox/dcim/signals.py]

## _invalid_saleor_baseline_run1_20260710

_No ungrounded citations._

## _invalid_saleor_sense_run2_20260710

_No ungrounded citations._

## _invalid_sentry_20260709

### _invalid_sentry_20260709/sense-sentry  — 205/211 grounded

**Unresolved**
- `src/sentry/api/endpoints/organization_issue_timeseries.py:30` — file not found at src/sentry/api/endpoints/organization_issue_timeseries.py
- `src/sentry/tasks/seer/night_shift/delivery.py:97` — file not found at src/sentry/tasks/seer/night_shift/delivery.py
- `src/sentry/tasks/summary.py:634` — file not found at src/sentry/tasks/summary.py
- `src/sentry/api/endpoints/organization_group_index.py:321` — file not found at src/sentry/api/endpoints/organization_group_index.py
- `src/sentry/integrations/github/webhooks.py:63` — file not found at src/sentry/integrations/github/webhooks.py
- `src/sentry/monitoring/issue_platform_adapter.py:122` — file not found at src/sentry/monitoring/issue_platform_adapter.py

## _invalid_sentry_20260709_attempt2

_No ungrounded citations._

## _invalid_sentry_20260709_attempt3

### _invalid_sentry_20260709_attempt3/baseline  — 166/170 grounded

**Unresolved**
- `src/sentry/integrations/jira/plugin.py:24` — file not found at src/sentry/integrations/jira/plugin.py
- `src/sentry/analytics/events/issue_owners_assignment.py:8` — file not found at src/sentry/analytics/events/issue_owners_assignment.py
- `src/sentry/notifications/utils/helpers.py:30` — file not found at src/sentry/notifications/utils/helpers.py
- `src/sentry_plugins/sentry_webhooks/plugin.py:106` — file not found at src/sentry_plugins/sentry_webhooks/plugin.py

## _invalid_sentry_sense_run2_20260710

_No ungrounded citations._

## _invalid_wagtail_3102cell_20260710

### _invalid_wagtail_3102cell_20260710/sense  — 402/552 grounded

**Hallucinated**
- `wagtail/search/index.py:30` — line 30 out of range (file only 1 lines)
- `wagtail/search/index.py:103` — line 103 out of range (file only 1 lines)
- `wagtail/search/index.py:122` — line 122 out of range (file only 1 lines)
- `wagtail/search/index.py:140` — line 140 out of range (file only 1 lines)
- `wagtail/search/index.py:156` — line 156 out of range (file only 1 lines)
- `wagtail/admin/api/serializers.py:228` — line 228 out of range (file only 192 lines)
- `wagtail/search/index.py:402` — line 402 out of range (file only 1 lines)

**Unresolved**
- `/wagtail/models/pages.py:289` — file not found at /wagtail/models/pages.py
- `wagtail/admin/views/pages/bulk_actions/add_tags.py:39` — file not found at wagtail/admin/views/pages/bulk_actions/add_tags.py
- `wagtail/admin/views/pages/bulk_actions/add_to_collection.py:51` — file not found at wagtail/admin/views/pages/bulk_actions/add_to_collection.py
- `wagtail/locales/models.py:1` — file not found at wagtail/locales/models.py
- `wagtail/admin/lock.py:1` — file not found at wagtail/admin/lock.py
- `wagtail/images/hooks.py:1` — file not found at wagtail/images/hooks.py
- `wagtail/document_parsing/registry.py:1` — file not found at wagtail/document_parsing/registry.py
- `wagtail/images/images.py:146` — file not found at wagtail/images/images.py
- `wagtail/images/images.py:175` — file not found at wagtail/images/images.py
- `wagtail/management/commands/reorder_pages.py:1` — file not found at wagtail/management/commands/reorder_pages.py
- `wagtail/management/commands/reorder_pages.py:10` — file not found at wagtail/management/commands/reorder_pages.py
- `wagtail/contrib.forms/views.py:56` — file not found at wagtail/contrib.forms/views.py
- `wagtail/contrib/search_promotions/admin.py:1` — file not found at wagtail/contrib/search_promotions/admin.py
- `wagtail/contrib/search_promotions/views.py:1` — file not found at wagtail/contrib/search_promotions/views.py
- `wagtail/contrib.forms/models.py:141` — file not found at wagtail/contrib.forms/models.py
- `/wagtail/models/pages.py:2470` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2537` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2629` — file not found at /wagtail/models/pages.py
- `/wagtail/models/revisions.py:34` — file not found at /wagtail/models/revisions.py
- `/wagtail/models/workflows.py:600` — file not found at /wagtail/models/workflows.py
- `/wagtail/models/workflows.py:596` — file not found at /wagtail/models/workflows.py
- `/wagtail/models/workflows.py:652` — file not found at /wagtail/models/workflows.py
- `/wagtail/models/sites.py:129` — file not found at /wagtail/models/sites.py
- `/wagtail/models/workflows.py:609` — file not found at /wagtail/models/workflows.py
- `/wagtail/actions/copy_page.py:31` — file not found at /wagtail/actions/copy_page.py
- `/wagtail/actions/copy_page.py:78` — file not found at /wagtail/actions/copy_page.py
- `/wagtail/actions/copy_page.py:96` — file not found at /wagtail/actions/copy_page.py
- `/wagtail/actions/copy_page.py:189` — file not found at /wagtail/actions/copy_page.py
- `/wagtail/actions/copy_page.py:193` — file not found at /wagtail/actions/copy_page.py
- `/wagtail/actions/copy_page.py:222` — file not found at /wagtail/actions/copy_page.py
- `/wagtail/actions/copy_page.py:246` — file not found at /wagtail/actions/copy_page.py
- `/wagtail/actions/copy_page.py:257` — file not found at /wagtail/actions/copy_page.py
- `/wagtail/actions/copy_page.py:275` — file not found at /wagtail/actions/copy_page.py
- `/wagtail/actions/copy_page.py:342` — file not found at /wagtail/actions/copy_page.py
- `/wagtail/actions/delete_page.py:14` — file not found at /wagtail/actions/delete_page.py
- `/wagtail/actions/delete_page.py:32` — file not found at /wagtail/actions/delete_page.py
- `/wagtail/actions/move_page.py:21` — file not found at /wagtail/actions/move_page.py
- `/wagtail/actions/move_page.py:41` — file not found at /wagtail/actions/move_page.py
- `/wagtail/actions/move_page.py:43` — file not found at /wagtail/actions/move_page.py
- `/wagtail/actions/move_page.py:69` — file not found at /wagtail/actions/move_page.py
- `/wagtail/actions/publish_page_revision.py:20` — file not found at /wagtail/actions/publish_page_revision.py
- `/wagtail/actions/publish_page_revision.py:53` — file not found at /wagtail/actions/publish_page_revision.py
- `/wagtail/actions/unpublish_page.py:17` — file not found at /wagtail/actions/unpublish_page.py
- `/wagtail/actions/unpublish_page.py:59` — file not found at /wagtail/actions/unpublish_page.py
- `/wagtail/actions/convert_alias.py:22` — file not found at /wagtail/actions/convert_alias.py
- `/wagtail/actions/convert_alias.py:43` — file not found at /wagtail/actions/convert_alias.py
- `/wagtail/actions/convert_alias.py:47` — file not found at /wagtail/actions/convert_alias.py
- `/wagtail/actions/create_alias.py:29` — file not found at /wagtail/actions/create_alias.py
- `/wagtail/actions/create_alias.py:119` — file not found at /wagtail/actions/create_alias.py
- `/wagtail/admin/views/pages/listing.py:43` — file not found at /wagtail/admin/views/pages/listing.py
- `/wagtail/admin/views/pages/listing.py:49` — file not found at /wagtail/admin/views/pages/listing.py
- `/wagtail/admin/views/pages/search.py:22` — file not found at /wagtail/admin/views/pages/search.py
- `/wagtail/admin/views/pages/bulk_actions/page_bulk_action.py:17` — file not found at /wagtail/admin/views/pages/bulk_actions/page_bulk_action.py
- `/wagtail/admin/views/pages/copy.py:46` — file not found at /wagtail/admin/views/pages/copy.py
- `/wagtail/admin/views/pages/create.py:70` — file not found at /wagtail/admin/views/pages/create.py
- `/wagtail/admin/views/pages/delete.py:22` — file not found at /wagtail/admin/views/pages/delete.py
- `/wagtail/admin/views/pages/edit.py:338` — file not found at /wagtail/admin/views/pages/edit.py
- `/wagtail/admin/views/pages/history.py:96` — file not found at /wagtail/admin/views/pages/history.py
- `/wagtail/admin/views/pages/listing.py:260` — file not found at /wagtail/admin/views/pages/listing.py
- `/wagtail/admin/views/pages/lock.py:13` — file not found at /wagtail/admin/views/pages/lock.py
- `/wagtail/admin/views/pages/unpublish.py:14` — file not found at /wagtail/admin/views/pages/unpublish.py
- `/wagtail/admin/views/generic/chooser.py:186` — file not found at /wagtail/admin/views/generic/chooser.py
- `/wagtail/admin/views/generic/chooser.py:252` — file not found at /wagtail/admin/views/generic/chooser.py
- `/wagtail/admin/views/pages/revisions.py:87` — file not found at /wagtail/admin/views/pages/revisions.py
- `/wagtail/admin/views/pages/search.py:110` — file not found at /wagtail/admin/views/pages/search.py
- `/wagtail/admin/views/reports/page_types_usage.py:127` — file not found at /wagtail/admin/views/reports/page_types_usage.py
- `/wagtail/admin/views/reports/workflows.py:198` — file not found at /wagtail/admin/views/reports/workflows.py
- `/wagtail/admin/views/workflows.py:473` — file not found at /wagtail/admin/views/workflows.py
- `/wagtail/admin/api/views.py:92` — file not found at /wagtail/admin/api/views.py
- `/wagtail/admin/api/serializers.py:56` — file not found at /wagtail/admin/api/serializers.py
- `/wagtail/admin/api/serializers.py:84` — file not found at /wagtail/admin/api/serializers.py
- `/wagtail/admin/api/serializers.py:131` — file not found at /wagtail/admin/api/serializers.py
- `/wagtail/admin/api/serializers.py:158` — file not found at /wagtail/admin/api/serializers.py
- `/wagtail/admin/views/pages/bulk_actions/page_bulk_action.py:60` — file not found at /wagtail/admin/views/pages/bulk_actions/page_bulk_action.py
- `/wagtail/admin/views/pages/listing.py:59` — file not found at /wagtail/admin/views/pages/listing.py
- `/wagtail/admin/views/pages/bulk_actions/page_bulk_action.py:68` — file not found at /wagtail/admin/views/pages/bulk_actions/page_bulk_action.py
- `/wagtail/admin/views/pages/bulk_actions/move.py:30` — file not found at /wagtail/admin/views/pages/bulk_actions/move.py
- `/wagtail/admin/views/pages/bulk_actions/delete.py:67` — file not found at /wagtail/admin/views/pages/bulk_actions/delete.py
- `/wagtail/admin/views/pages/bulk_actions/publish.py:72` — file not found at /wagtail/admin/views/pages/bulk_actions/publish.py
- `/wagtail/admin/views/pages/bulk_actions/unpublish.py:66` — file not found at /wagtail/admin/views/pages/bulk_actions/unpublish.py
- `/wagtail/admin/views/home.py:105` — file not found at /wagtail/admin/views/home.py
- `/wagtail/admin/views/home.py:160` — file not found at /wagtail/admin/views/home.py
- `/wagtail/api/v2/views.py:437` — file not found at /wagtail/api/v2/views.py
- `/wagtail/api/v2/views.py:518` — file not found at /wagtail/api/v2/views.py
- `/wagtail/api/v2/views.py:580` — file not found at /wagtail/api/v2/views.py
- `/wagtail/api/v2/views.py:608` — file not found at /wagtail/api/v2/views.py
- `/wagtail/api/v2/serializers.py:65` — file not found at /wagtail/api/v2/serializers.py
- `/wagtail/api/v2/serializers.py:87` — file not found at /wagtail/api/v2/serializers.py
- `/wagtail/api/v2/serializers.py:131` — file not found at /wagtail/api/v2/serializers.py
- `/wagtail/api/v2/serializers.py:158` — file not found at /wagtail/api/v2/serializers.py
- `/wagtail/api/v2/filters.py:167` — file not found at /wagtail/api/v2/filters.py
- `/wagtail/api/v2/filters.py:199` — file not found at /wagtail/api/v2/filters.py
- `/wagtail/api/v2/filters.py:223` — file not found at /wagtail/api/v2/filters.py
- `/wagtail/contrib/forms/models.py:323` — file not found at /wagtail/contrib/forms/models.py
- `/wagtail/contrib/forms/models.py:390` — file not found at /wagtail/contrib/forms/models.py
- `/wagtail/contrib/routable_page/models.py:221` — file not found at /wagtail/contrib/routable_page/models.py
- `/wagtail/contrib/search_promotions/models.py:52` — file not found at /wagtail/contrib/search_promotions/models.py
- `/wagtail/contrib/settings/models.py:73` — file not found at /wagtail/contrib/settings/models.py
- `/wagtail/contrib/redirects/models.py:106` — file not found at /wagtail/contrib/redirects/models.py
- `/wagtail/contrib/frontend_cache/utils.py:202` — file not found at /wagtail/contrib/frontend_cache/utils.py
- `/wagtail/contrib/simple_translation/views.py:116` — file not found at /wagtail/contrib/simple_translation/views.py
- `/wagtail/templatetags/wagtailcore_tags.py:18` — file not found at /wagtail/templatetags/wagtailcore_tags.py
- `/wagtail/templatetags/wagtailcore_tags.py:70` — file not found at /wagtail/templatetags/wagtailcore_tags.py
- `/wagtail/templatetags/wagtailcore_tags.py:74` — file not found at /wagtail/templatetags/wagtailcore_tags.py
- `/wagtail/rich_text/pages.py:8` — file not found at /wagtail/rich_text/pages.py
- `/wagtail/rich_text/pages.py:16` — file not found at /wagtail/rich_text/pages.py
- `/wagtail/rich_text/pages.py:29` — file not found at /wagtail/rich_text/pages.py
- `/wagtail/rich_text/pages.py:36` — file not found at /wagtail/rich_text/pages.py
- `/wagtail/search/index.py:103` — file not found at /wagtail/search/index.py
- `/wagtail/search/index.py:122` — file not found at /wagtail/search/index.py
- `/wagtail/search/index.py:140` — file not found at /wagtail/search/index.py
- `/wagtail/search/index.py:156` — file not found at /wagtail/search/index.py
- `/wagtail/url_routing.py:10` — file not found at /wagtail/url_routing.py
- `/wagtail/views.py:17` — file not found at /wagtail/views.py
- `/wagtail/views.py:42` — file not found at /wagtail/views.py
- `/wagtail/models/reference_index.py:119` — file not found at /wagtail/models/reference_index.py
- `/wagtail/models/reference_index.py:600` — file not found at /wagtail/models/reference_index.py
- `/wagtail/models/reference_index.py:616` — file not found at /wagtail/models/reference_index.py
- `/wagtail/models/reference_index.py:681` — file not found at /wagtail/models/reference_index.py
- `/wagtail/signal_handlers.py:66` — file not found at /wagtail/signal_handlers.py
- `/wagtail/signal_handlers.py:79` — file not found at /wagtail/signal_handlers.py
- `/wagtail/models/pages.py:910` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:1219` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:1236` — file not found at /wagtail/models/pages.py
- `/wagtail/admin/api/filters.py:30` — file not found at /wagtail/admin/api/filters.py
- `/wagtail/models/pages.py:1649` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:1719` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:1501` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:1896` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:1903` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:1910` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:889` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:577` — file not found at /wagtail/models/pages.py
- `/wagtail/contrib/settings/models.py:68` — file not found at /wagtail/contrib/settings/models.py
- `/wagtail/models/pages.py:367` — file not found at /wagtail/models/pages.py
- `/wagtail/contrib/redirects/signal_handlers.py:17` — file not found at /wagtail/contrib/redirects/signal_handlers.py
- `/wagtail/blocks/migrations/migrate_operation.py:217` — file not found at /wagtail/blocks/migrations/migrate_operation.py
- `/wagtail/admin/views/reports/workflows.py:42` — file not found at /wagtail/admin/views/reports/workflows.py
- `/wagtail/admin/views/reports/page_types_usage.py:57` — file not found at /wagtail/admin/views/reports/page_types_usage.py
- `wagtail/admin/views/reports/lockedy_pages.py:59` — file not found at wagtail/admin/views/reports/lockedy_pages.py
- `wagtail/templatetags/wagtailadmin_tags.py:9` — file not found at wagtail/templatetags/wagtailadmin_tags.py
- `wagtail/templatetags/wagtailadmin_tags.py:171` — file not found at wagtail/templatetags/wagtailadmin_tags.py
- `wagtail/templatetags/wagtailuserbar.py:9` — file not found at wagtail/templatetags/wagtailuserbar.py

## _invalid_wagtail_run1_20260709

### _invalid_wagtail_run1_20260709/sense  — 3/4 grounded

**Unresolved**
- `/wagtail/models/pages.py:289` — file not found at /wagtail/models/pages.py

## _invalid_wagtail_run1_20260710

_No ungrounded citations._

## _invalid_wagtail_run1_20260710b

_No ungrounded citations._

## _invalid_wagtail_run1_20260710c

_No ungrounded citations._

## baseline

### baseline/litellm  — 0/228 grounded

**Unresolved**
- `/litellm/llms/base_llm/chat/transformation.py:75` — file not found at /litellm/llms/base_llm/chat/transformation.py
- `/litellm/llms/a2a/chat/transformation.py:23` — file not found at /litellm/llms/a2a/chat/transformation.py
- `/litellm/llms/anthropic/completion/transformation.py:49` — file not found at /litellm/llms/anthropic/completion/transformation.py
- `/litellm/llms/azure/chat/gpt_transformation.py:29` — file not found at /litellm/llms/azure/chat/gpt_transformation.py
- `/litellm/llms/azure_ai/agents/transformation.py:53` — file not found at /litellm/llms/azure_ai/agents/transformation.py
- `/litellm/llms/bedrock/chat/agentcore/transformation.py:51` — file not found at /litellm/llms/bedrock/chat/agentcore/transformation.py
- `/litellm/llms/bedrock/chat/converse_transformation.py:102` — file not found at /litellm/llms/bedrock/chat/converse_transformation.py
- `/litellm/llms/bedrock/chat/invoke_agent/transformation.py:47` — file not found at /litellm/llms/bedrock/chat/invoke_agent/transformation.py
- `/litellm/llms/bedrock/chat/invoke_transformations/amazon_ai21_transformation.py:10` — file not found at /litellm/llms/bedrock/chat/invoke_transformations/amazon_ai21_transformation.py
- `/litellm/llms/bedrock/chat/invoke_transformations/amazon_llama_transformation.py:10` — file not found at /litellm/llms/bedrock/chat/invoke_transformations/amazon_llama_transformation.py
- `/litellm/llms/bedrock/chat/invoke_transformations/amazon_mistral_transformation.py:14` — file not found at /litellm/llms/bedrock/chat/invoke_transformations/amazon_mistral_transformation.py
- `/litellm/llms/bedrock/chat/invoke_transformations/amazon_qwen3_transformation.py:22` — file not found at /litellm/llms/bedrock/chat/invoke_transformations/amazon_qwen3_transformation.py
- `/litellm/llms/bedrock/chat/invoke_transformations/amazon_titan_transformation.py:12` — file not found at /litellm/llms/bedrock/chat/invoke_transformations/amazon_titan_transformation.py
- `/litellm/llms/bedrock/chat/invoke_transformations/amazon_twelvelabs_pegasus_transformation.py:35` — file not found at /litellm/llms/bedrock/chat/invoke_transformations/amazon_twelvelabs_pegasus_transformation.py
- `/litellm/llms/bytez/chat/transformation.py:37` — file not found at /litellm/llms/bytez/chat/transformation.py
- `/litellm/llms/cohere/chat/transformation.py:42` — file not found at /litellm/llms/cohere/chat/transformation.py
- `/litellm/llms/gigachat/chat/transformation.py:50` — file not found at /litellm/llms/gigachat/chat/transformation.py
- `/litellm/llms/huggingface/embedding/transformation.py:38` — file not found at /litellm/llms/huggingface/embedding/transformation.py
- `/litellm/llms/langflow/chat/transformation.py:35` — file not found at /litellm/llms/langflow/chat/transformation.py
- `/litellm/llms/langgraph/chat/transformation.py:44` — file not found at /litellm/llms/langgraph/chat/transformation.py
- `/litellm/llms/litellm_proxy/chat/transformation.py:17` — file not found at /litellm/llms/litellm_proxy/chat/transformation.py
- `/litellm/llms/nlp_cloud/chat/transformation.py:24` — file not found at /litellm/llms/nlp_cloud/chat/transformation.py
- `/litellm/llms/oci/chat/transformation.py:255` — file not found at /litellm/llms/oci/chat/transformation.py
- `/litellm/llms/ollama/chat/transformation.py:49` — file not found at /litellm/llms/ollama/chat/transformation.py
- `/litellm/llms/ollama/completion/transformation.py:40` — file not found at /litellm/llms/ollama/completion/transformation.py
- `/litellm/llms/openai/openai.py:111` — file not found at /litellm/llms/openai/openai.py
- `/litellm/llms/petals/completion/transformation.py:17` — file not found at /litellm/llms/petals/completion/transformation.py
- `/litellm/llms/predibase/chat/transformation.py:28` — file not found at /litellm/llms/predibase/chat/transformation.py
- `/litellm/llms/replicate/chat/transformation.py:29` — file not found at /litellm/llms/replicate/chat/transformation.py
- `/litellm/llms/sagemaker/completion/transformation.py:35` — file not found at /litellm/llms/sagemaker/completion/transformation.py
- `/litellm/llms/triton/completion/transformation.py:31` — file not found at /litellm/llms/triton/completion/transformation.py
- `/litellm/llms/watsonx/completion/transformation.py:38` — file not found at /litellm/llms/watsonx/completion/transformation.py
- `/litellm/llms/watsonx/embed/transformation.py:24` — file not found at /litellm/llms/watsonx/embed/transformation.py
- `/litellm/llms/xai/chat/transformation.py:30` — file not found at /litellm/llms/xai/chat/transformation.py
- `/litellm/llms/aiml/chat/transformation.py:7` — file not found at /litellm/llms/aiml/chat/transformation.py
- `/litellm/llms/azure_ai/azure_model_router/transformation.py:37` — file not found at /litellm/llms/azure_ai/azure_model_router/transformation.py
- `/litellm/llms/baseten/chat.py:5` — file not found at /litellm/llms/baseten/chat.py
- `/litellm/llms/claude/api/chat/transformation.py:17` — file not found at /litellm/llms/claude/api/chat/transformation.py
- `/litellm/llms/cerebras/chat.py:13` — file not found at /litellm/llms/cerebras/chat.py
- `/litellm/llms/clarifai/chat/transformation.py:23` — file not found at /litellm/llms/clarifai/chat/transformation.py
- `/litellm/llms/cloudflare/chat/transformation.py:29` — file not found at /litellm/llms/cloudflare/chat/transformation.py
- `/litellm/llms/cometapi/chat/transformation.py:21` — file not found at /litellm/llms/cometapi/chat/transformation.py
- `/litellm/llms/compactifai/chat/transformation.py:24` — file not found at /litellm/llms/compactifai/chat/transformation.py
- `/litellm/llms/dashscope/chat/transformation.py:15` — file not found at /litellm/llms/dashscope/chat/transformation.py
- `/litellm/llms/deepinfra/chat/transformation.py:11` — file not found at /litellm/llms/deepinfra/chat/transformation.py
- `/litellm/llms/deepseek/chat/transformation.py:18` — file not found at /litellm/llms/deepseek/chat/transformation.py
- `/litellm/llms/docker_model_runner/chat/transformation.py:18` — file not found at /litellm/llms/docker_model_runner/chat/transformation.py
- `/litellm/llms/featherless_ai/chat/transformation.py:8` — file not found at /litellm/llms/featherless_ai/chat/transformation.py
- `/litellm/llms/fireworks_ai/chat/transformation.py:73` — file not found at /litellm/llms/fireworks_ai/chat/transformation.py
- `/litellm/llms/heroku/chat/transformation.py:22` — file not found at /litellm/llms/heroku/chat/transformation.py
- `/litellm/llms/hosted_vllm/chat/transformation.py:40` — file not found at /litellm/llms/hosted_vllm/chat/transformation.py
- `/litellm/llms/huggingface/chat/transformation.py:41` — file not found at /litellm/llms/huggingface/chat/transformation.py
- `/litellm/llms/llamafile/chat/transformation.py:8` — file not found at /litellm/llms/llamafile/chat/transformation.py
- `/litellm/llms/lm_studio/chat/transformation.py:12` — file not found at /litellm/llms/lm_studio/chat/transformation.py
- `/litellm/llms/meta_llama/chat/transformation.py:17` — file not found at /litellm/llms/meta_llama/chat/transformation.py
- `/litellm/llms/minimax/chat/transformation.py:13` — file not found at /litellm/llms/minimax/chat/transformation.py
- `/litellm/llms/mistral/chat/transformation.py:42` — file not found at /litellm/llms/mistral/chat/transformation.py
- `/litellm/llms/modelscope/chat/transformation.py:23` — file not found at /litellm/llms/modelscope/chat/transformation.py
- `/litellm/llms/moonshot/chat/transformation.py:18` — file not found at /litellm/llms/moonshot/chat/transformation.py
- `/litellm/llms/nebius/chat/transformation.py:10` — file not found at /litellm/llms/nebius/chat/transformation.py
- `/litellm/llms/novita/chat/transformation.py:15` — file not found at /litellm/llms/novita/chat/transformation.py
- `/litellm/llms/nscale/chat/transformation.py:7` — file not found at /litellm/llms/nscale/chat/transformation.py
- `/litellm/llms/nvidia_nim/chat/transformation.py:14` — file not found at /litellm/llms/nvidia_nim/chat/transformation.py
- `/litellm/llms/ovhcloud/chat/transformation.py:20` — file not found at /litellm/llms/ovhcloud/chat/transformation.py
- `/litellm/llms/perplexity/chat/transformation.py:20` — file not found at /litellm/llms/perplexity/chat/transformation.py
- `/litellm/llms/sambanova/chat.py:16` — file not found at /litellm/llms/sambanova/chat.py
- `/litellm/llms/sap/chat/transformation.py:108` — file not found at /litellm/llms/sap/chat/transformation.py
- `/litellm/llms/together_ai/chat.py:17` — file not found at /litellm/llms/together_ai/chat.py
- `/litellm/llms/volcengine/chat/transformation.py:48` — file not found at /litellm/llms/volcengine/chat/transformation.py
- `/litellm/llms/ai21/chat/transformation.py:18` — file not found at /litellm/llms/ai21/chat/transformation.py
- `/litellm/llms/aiohttp_openai/chat/transformation.py:17` — file not found at /litellm/llms/aiohttp_openai/chat/transformation.py
- `/litellm/llms/amazon_nova/chat/transformation.py:23` — file not found at /litellm/llms/amazon_nova/chat/transformation.py
- `/litellm/llms/databricks/chat/transformation.py:103` — file not found at /litellm/llms/databricks/chat/transformation.py
- `/litellm/llms/datarobot/chat/transformation.py:22` — file not found at /litellm/llms/datarobot/chat/transformation.py
- `/litellm/llms/empower/chat/transformation.py:17` — file not found at /litellm/llms/empower/chat/transformation.py
- `/litellm/llms/groq/chat/transformation.py:20` — file not found at /litellm/llms/groq/chat/transformation.py
- `/litellm/llms/hyperbolic/chat/transformation.py:20` — file not found at /litellm/llms/hyperbolic/chat/transformation.py
- `/litellm/llms/inception/chat/transformation.py:17` — file not found at /litellm/llms/inception/chat/transformation.py
- `/litellm/llms/lambda_ai/chat/transformation.py:20` — file not found at /litellm/llms/lambda_ai/chat/transformation.py
- `/litellm/llms/lemonade/chat/transformation.py:17` — file not found at /litellm/llms/lemonade/chat/transformation.py
- `/litellm/llms/morph/chat/transformation.py:22` — file not found at /litellm/llms/morph/chat/transformation.py
- `/litellm/llms/runwayml/chat/transformation.py:23` — file not found at /litellm/llms/runwayml/chat/transformation.py
- `/litellm/llms/v0/chat/transformation.py:20` — file not found at /litellm/llms/v0/chat/transformation.py
- `/litellm/llms/vercel_ai_gateway/chat/transformation.py:22` — file not found at /litellm/llms/vercel_ai_gateway/chat/transformation.py
- `/litellm/llms/xinference/chat/transformation.py:17` — file not found at /litellm/llms/xinference/chat/transformation.py
- `/litellm/llms/bedrock/chat/invoke_transformations/amazon_cohere_transformation.py:15` — file not found at /litellm/llms/bedrock/chat/invoke_transformations/amazon_cohere_transformation.py
- `/litellm/llms/bedrock/chat/invoke_transformations/amazon_deepseek_transformation.py:17` — file not found at /litellm/llms/bedrock/chat/invoke_transformations/amazon_deepseek_transformation.py
- `/litellm/llms/bedrock/chat/invoke_transformations/amazon_moonshot_transformation.py:18` — file not found at /litellm/llms/bedrock/chat/invoke_transformations/amazon_moonshot_transformation.py
- `/litellm/llms/bedrock/chat/invoke_transformations/amazon_nova_transformation.py:23` — file not found at /litellm/llms/bedrock/chat/invoke_transformations/amazon_nova_transformation.py
- `/litellm/llms/bedrock/chat/invoke_transformations/amazon_openai_transformation.py:28` — file not found at /litellm/llms/bedrock/chat/invoke_transformations/amazon_openai_transformation.py
- `/litellm/llms/bedrock/chat/invoke_transformations/anthropic_claude2_transformation.py:14` — file not found at /litellm/llms/bedrock/chat/invoke_transformations/anthropic_claude2_transformation.py
- `/litellm/llms/bedrock/chat/invoke_transformations/anthropic_claude3_transformation.py:17` — file not found at /litellm/llms/bedrock/chat/invoke_transformations/anthropic_claude3_transformation.py
- `/litellm/llms/bedrock/mantle/chat/transformation.py:30` — file not found at /litellm/llms/bedrock/mantle/chat/transformation.py
- `/litellm/llms/bedrock/messages/mantle_transformation.py:16` — file not found at /litellm/llms/bedrock/messages/mantle_transformation.py
- `/litellm/llms/bedrock/messages/invoke_transformations/anthropic_claude3_transformation.py:20` — file not found at /litellm/llms/bedrock/messages/invoke_transformations/anthropic_claude3_transformation.py
- `/litellm/llms/cohere/embed/transformation.py:36` — file not found at /litellm/llms/cohere/embed/transformation.py
- `/litellm/llms/cometapi/embed/transformation.py:16` — file not found at /litellm/llms/cometapi/embed/transformation.py
- `/litellm/llms/dashscope/embed/transformation.py:18` — file not found at /litellm/llms/dashscope/embed/transformation.py
- `/litellm/llms/databricks/responses/transformation.py:16` — file not found at /litellm/llms/databricks/responses/transformation.py
- `/litellm/llms/hosted_vllm/embedding/transformation.py:15` — file not found at /litellm/llms/hosted_vllm/embedding/transformation.py
- `/litellm/llms/infinity/embedding/transformation.py:22` — file not found at /litellm/llms/infinity/embedding/transformation.py
- `/litellm/llms/jina_ai/embedding/transformation.py:19` — file not found at /litellm/llms/jina_ai/embedding/transformation.py
- `/litellm/llms/minimax/text_to_speech/transformation.py:15` — file not found at /litellm/llms/minimax/text_to_speech/transformation.py
- `/litellm/llms/openai/openai.py:68` — file not found at /litellm/llms/openai/openai.py
- `/litellm/llms/openrouter/embedding/transformation.py:17` — file not found at /litellm/llms/openrouter/embedding/transformation.py
- `/litellm/llms/perplexity/embedding/transformation.py:16` — file not found at /litellm/llms/perplexity/embedding/transformation.py
- `/litellm/llms/sagemaker/embedding/cohere_transformation.py:32` — file not found at /litellm/llms/sagemaker/embedding/cohere_transformation.py
- `/litellm/llms/sagemaker/embedding/transformation.py:23` — file not found at /litellm/llms/sagemaker/embedding/transformation.py
- `/litellm/llms/snowflake/embedding/transformation.py:14` — file not found at /litellm/llms/snowflake/embedding/transformation.py
- `/litellm/llms/volcengine/embedding/transformation.py:20` — file not found at /litellm/llms/volcengine/embedding/transformation.py
- `/litellm/llms/voyage/embedding/transformation.py:20` — file not found at /litellm/llms/voyage/embedding/transformation.py
- `/litellm/llms/aiml/image_generation/transformation.py:16` — file not found at /litellm/llms/aiml/image_generation/transformation.py
- `/litellm/llms/azure_ai/image_generation/mai_transformation.py:20` — file not found at /litellm/llms/azure_ai/image_generation/mai_transformation.py
- `/litellm/llms/black_forest_labs/image_generation/transformation.py:19` — file not found at /litellm/llms/black_forest_labs/image_generation/transformation.py
- `/litellm/llms/cometapi/image_generation/transformation.py:18` — file not found at /litellm/llms/cometapi/image_generation/transformation.py
- `/litellm/llms/fal_ai/image_generation/bria_transformation.py:18` — file not found at /litellm/llms/fal_ai/image_generation/bria_transformation.py
- `/litellm/llms/fal_ai/image_generation/bytedance_transformation.py:85` — file not found at /litellm/llms/fal_ai/image_generation/bytedance_transformation.py
- `/litellm/llms/fal_ai/image_generation/bytedance_transformation.py:96` — file not found at /litellm/llms/fal_ai/image_generation/bytedance_transformation.py
- `/litellm/llms/fal_ai/image_generation/flux_pro_v11_ultra_transformation.py:18` — file not found at /litellm/llms/fal_ai/image_generation/flux_pro_v11_ultra_transformation.py
- `/litellm/llms/fal_ai/image_generation/ideogram_v3_transformation.py:18` — file not found at /litellm/llms/fal_ai/image_generation/ideogram_v3_transformation.py
- `/litellm/llms/fal_ai/image_generation/imagen4_transformation.py:18` — file not found at /litellm/llms/fal_ai/image_generation/imagen4_transformation.py
- `/litellm/llms/fal_ai/image_generation/nano_banana_transformation.py:9` — file not found at /litellm/llms/fal_ai/image_generation/nano_banana_transformation.py
- `/litellm/llms/fal_ai/image_generation/recraft_v3_transformation.py:18` — file not found at /litellm/llms/fal_ai/image_generation/recraft_v3_transformation.py
- `/litellm/llms/fal_ai/image_generation/stable_diffusion_transformation.py:18` — file not found at /litellm/llms/fal_ai/image_generation/stable_diffusion_transformation.py
- `/litellm/llms/modelscope/image_generation/transformation.py:15` — file not found at /litellm/llms/modelscope/image_generation/transformation.py
- `/litellm/llms/openai/image_generation/gpt_transformation.py:16` — file not found at /litellm/llms/openai/image_generation/gpt_transformation.py
- `/litellm/llms/openai/image_generation/dall_e_2_transformation.py:16` — file not found at /litellm/llms/openai/image_generation/dall_e_2_transformation.py
- `/litellm/llms/openai/image_generation/dall_e_3_transformation.py:16` — file not found at /litellm/llms/openai/image_generation/dall_e_3_transformation.py
- `/litellm/llms/recraft/image_generation/transformation.py:24` — file not found at /litellm/llms/recraft/image_generation/transformation.py
- `/litellm/llms/runwayml/image_generation/transformation.py:18` — file not found at /litellm/llms/runwayml/image_generation/transformation.py
- `/litellm/llms/stability/image_generation/transformation.py:19` — file not found at /litellm/llms/stability/image_generation/transformation.py
- `/litellm/llms/xinference/image_generation/transformation.py:13` — file not found at /litellm/llms/xinference/image_generation/transformation.py
- `/litellm/llms/azure/audio_transcription/transformation.py:31` — file not found at /litellm/llms/azure/audio_transcription/transformation.py
- `/litellm/llms/deepgram/audio_transcription/transformation.py:17` — file not found at /litellm/llms/deepgram/audio_transcription/transformation.py
- `/litellm/llms/elevenlabs/audio_transcription/transformation.py:18` — file not found at /litellm/llms/elevenlabs/audio_transcription/transformation.py
- `/litellm/llms/mistral/audio_transcription/transformation.py:18` — file not found at /litellm/llms/mistral/audio_transcription/transformation.py
- `/litellm/llms/nvidia_riva/audio_transcription/transformation.py:16` — file not found at /litellm/llms/nvidia_riva/audio_transcription/transformation.py
- `/litellm/llms/ovhcloud/audio_transcription/transformation.py:20` — file not found at /litellm/llms/ovhcloud/audio_transcription/transformation.py
- `/litellm/llms/scaleway/audio_transcription/transformation.py:18` — file not found at /litellm/llms/scaleway/audio_transcription/transformation.py
- `/litellm/llms/soniox/audio_transcription/transformation.py:17` — file not found at /litellm/llms/soniox/audio_transcription/transformation.py
- `/litellm/llms/vllm/passthrough/transformation.py:13` — file not found at /litellm/llms/vllm/passthrough/transformation.py
- `/litellm/llms/azure/completion/transformation.py:6` — file not found at /litellm/llms/azure/completion/transformation.py
- `/litellm/llms/codestral/completion/transformation.py:17` — file not found at /litellm/llms/codestral/completion/transformation.py
- `/litellm/llms/inception/completion/transformation.py:17` — file not found at /litellm/llms/inception/completion/transformation.py
- `/litellm/llms/openai/completion/transformation.py:15` — file not found at /litellm/llms/openai/completion/transformation.py
- `/litellm/llms/together_ai/completion/transformation.py:17` — file not found at /litellm/llms/together_ai/completion/transformation.py
- `/litellm/llms/azure/image_edit/transformation.py:13` — file not found at /litellm/llms/azure/image_edit/transformation.py
- `/litellm/llms/black_forest_labs/image_edit/transformation.py:40` — file not found at /litellm/llms/black_forest_labs/image_edit/transformation.py
- `/litellm/llms/azure_ai/image_edit/flux2_transformation.py:14` — file not found at /litellm/llms/azure_ai/image_edit/flux2_transformation.py
- `/litellm/llms/azure_ai/image_edit/flux_transformation.py:14` — file not found at /litellm/llms/azure_ai/image_edit/flux_transformation.py
- `/litellm/llms/azure_ai/image_edit/mai_transformation.py:14` — file not found at /litellm/llms/azure_ai/image_edit/mai_transformation.py
- `/litellm/llms/recraft/image_edit/transformation.py:24` — file not found at /litellm/llms/recraft/image_edit/transformation.py
- `/litellm/llms/azure_ai/image_edit/transformation.py:14` — file not found at /litellm/llms/azure_ai/image_edit/transformation.py
- `/litellm/llms/stability/image_edit/transformations.py:15` — file not found at /litellm/llms/stability/image_edit/transformations.py
- `/litellm/llms/bedrock/batches/transformation.py:30` — file not found at /litellm/llms/bedrock/batches/transformation.py
- `/litellm/llms/bedrock/files/transformation.py:136` — file not found at /litellm/llms/bedrock/files/transformation.py
- `/litellm/llms/bedrock/realtime/transformation.py:16` — file not found at /litellm/llms/bedrock/realtime/transformation.py
- `/litellm/llms/bedrock/vector_stores/transformation.py:16` — file not found at /litellm/llms/bedrock/vector_stores/transformation.py
- `/litellm/llms/bedrock/image_edit/amazon_nova_canvas_image_edit_transformation.py:217` — file not found at /litellm/llms/bedrock/image_edit/amazon_nova_canvas_image_edit_transformation.py
- `/litellm/llms/bedrock/image_edit/stability_transformation.py:16` — file not found at /litellm/llms/bedrock/image_edit/stability_transformation.py
- `/litellm/llms/bedrock/passthrough/transformation.py:20` — file not found at /litellm/llms/bedrock/passthrough/transformation.py
- `/litellm/llms/bedrock/rerank/transformation.py:31` — file not found at /litellm/llms/bedrock/rerank/transformation.py
- `/litellm/llms/bedrock/claude_platform/transformation.py:11` — file not found at /litellm/llms/bedrock/claude_platform/transformation.py
- `/litellm/llms/bedrock/claude_platform/messages_transformation.py:14` — file not found at /litellm/llms/bedrock/claude_platform/messages_transformation.py
- `/litellm/llms/azure_ai/anthropic/transformation.py:32` — file not found at /litellm/llms/azure_ai/anthropic/transformation.py
- `/litellm/llms/vertex_ai/vertex_ai_partner_models/anthropic/transformation.py:25` — file not found at /litellm/llms/vertex_ai/vertex_ai_partner_models/anthropic/transformation.py
- `/litellm/llms/vertex_ai/vertex_ai_partner_models/anthropic/experimental_pass_through/transformation.py:16` — file not found at /litellm/llms/vertex_ai/vertex_ai_partner_models/anthropic/experimental_pass_through/transformation.py
- `/litellm/llms/deepseek/messages/transformation.py:17` — file not found at /litellm/llms/deepseek/messages/transformation.py
- `/litellm/llms/minimax/messages/transformation.py:17` — file not found at /litellm/llms/minimax/messages/transformation.py
- `/litellm/llms/snowflake/chat/transformation.py:53` — file not found at /litellm/llms/snowflake/chat/transformation.py
- `/litellm/llms/vertex_ai/agent_engine/transformation.py:53` — file not found at /litellm/llms/vertex_ai/agent_engine/transformation.py
- `/litellm/llms/vertex_ai/files/transformation.py:17` — file not found at /litellm/llms/vertex_ai/files/transformation.py
- `/litellm/llms/vertex_ai/image_edit/vertex_gemini_transformation.py:19` — file not found at /litellm/llms/vertex_ai/image_edit/vertex_gemini_transformation.py
- `/litellm/llms/vertex_ai/image_edit/vertex_imagen_transformation.py:18` — file not found at /litellm/llms/vertex_ai/image_edit/vertex_imagen_transformation.py
- `/litellm/llms/vertex_ai/image_generation/vertex_gemini_transformation.py:18` — file not found at /litellm/llms/vertex_ai/image_generation/vertex_gemini_transformation.py
- `/litellm/llms/vertex_ai/image_generation/vertex_imagen_transformation.py:17` — file not found at /litellm/llms/vertex_ai/image_generation/vertex_imagen_transformation.py
- `/litellm/llms/vertex_ai/realtime/transformation.py:17` — file not found at /litellm/llms/vertex_ai/realtime/transformation.py
- `/litellm/llms/vertex_ai/rerank/transformation.py:22` — file not found at /litellm/llms/vertex_ai/rerank/transformation.py
- `/litellm/llms/vertex_ai/text_to_speech/transformation.py:19` — file not found at /litellm/llms/vertex_ai/text_to_speech/transformation.py
- `/litellm/llms/vertex_ai/vector_stores/rag_api/transformation.py:15` — file not found at /litellm/llms/vertex_ai/vector_stores/rag_api/transformation.py
- `/litellm/llms/vertex_ai/vector_stores/search_api/transformation.py:15` — file not found at /litellm/llms/vertex_ai/vector_stores/search_api/transformation.py
- `/litellm/llms/vertex_ai/videos/transformation.py:18` — file not found at /litellm/llms/vertex_ai/videos/transformation.py
- `/litellm/llms/azure_ai/ocr/document_intelligence/transformation.py:16` — file not found at /litellm/llms/azure_ai/ocr/document_intelligence/transformation.py
- `/litellm/llms/azure_ai/ocr/transformation.py:17` — file not found at /litellm/llms/azure_ai/ocr/transformation.py
- `/litellm/llms/vertex_ai/ocr/deepseek_transformation.py:15` — file not found at /litellm/llms/vertex_ai/ocr/deepseek_transformation.py
- `/litellm/llms/vertex_ai/ocr/transformation.py:18` — file not found at /litellm/llms/vertex_ai/ocr/transformation.py
- `/litellm/llms/reducto/ocr/transformation.py:22` — file not found at /litellm/llms/reducto/ocr/transformation.py
- `/litellm/llms/reducto/ocr/transformation.py:21` — file not found at /litellm/llms/reducto/ocr/transformation.py
- `/litellm/llms/apiserpent/search/transformation.py:14` — file not found at /litellm/llms/apiserpent/search/transformation.py
- `/litellm/llms/brave/search/transformation.py:15` — file not found at /litellm/llms/brave/search/transformation.py
- `/litellm/llms/dataforseo/search/transformation.py:15` — file not found at /litellm/llms/dataforseo/search/transformation.py
- `/litellm/llms/duckduckgo/search/transformation.py:14` — file not found at /litellm/llms/duckduckgo/search/transformation.py
- `/litellm/llms/exa_ai/search/transformation.py:14` — file not found at /litellm/llms/exa_ai/search/transformation.py
- `/litellm/llms/inception/search/transformation.py:16` — file not found at /litellm/llms/inception/search/transformation.py
- `/litellm/llms/linkup/search/transformation.py:14` — file not found at /litellm/llms/linkup/search/transformation.py
- `/litellm/llms/parallel_ai/search/transformation.py:14` — file not found at /litellm/llms/parallel_ai/search/transformation.py
- `/litellm/llms/searchapi/search/transformation.py:14` — file not found at /litellm/llms/searchapi/search/transformation.py
- `/litellm/llms/searxng/search/transformation.py:14` — file not found at /litellm/llms/searxng/search/transformation.py
- `/litellm/llms/serper/search/transformation.py:14` — file not found at /litellm/llms/serper/search/transformation.py
- `/litellm/llms/tavily/search/transformation.py:15` — file not found at /litellm/llms/tavily/search/transformation.py
- `/litellm/llms/tinyfish/search/transformation.py:14` — file not found at /litellm/llms/tinyfish/search/transformation.py
- `/litellm/llms/you_com/search/transformation.py:14` — file not found at /litellm/llms/you_com/search/transformation.py
- `/litellm/llms/aws_polly/text_to_speech/transformation.py:18` — file not found at /litellm/llms/aws_polly/text_to_speech/transformation.py
- `/litellm/llms/azure/text_to_speech/transformation.py:27` — file not found at /litellm/llms/azure/text_to_speech/transformation.py
- `/litellm/llms/elevenlabs/text_to_speech/transformation.py:18` — file not found at /litellm/llms/elevenlabs/text_to_speech/transformation.py
- `/litellm/llms/runwayml/text_to_speech/transformation.py:18` — file not found at /litellm/llms/runwayml/text_to_speech/transformation.py
- `/litellm/llms/azure_ai/vector_stores/transformation.py:16` — file not found at /litellm/llms/azure_ai/vector_stores/transformation.py
- `/litellm/llms/azure/vector_stores/transformation.py:8` — file not found at /litellm/llms/azure/vector_stores/transformation.py
- `/litellm/llms/milvus/vector_stores/transformation.py:15` — file not found at /litellm/llms/milvus/vector_stores/transformation.py
- `/litellm/llms/pg_vector/vector_stores/transformation.py:15` — file not found at /litellm/llms/pg_vector/vector_stores/transformation.py
- `/litellm/llms/ragflow/vector_stores/transformation.py:15` — file not found at /litellm/llms/ragflow/vector_stores/transformation.py
- `/litellm/llms/s3_vectors/vector_stores/transformation.py:15` — file not found at /litellm/llms/s3_vectors/vector_stores/transformation.py
- `/litellm/llms/azure/passthrough/transformation.py:19` — file not found at /litellm/llms/azure/passthrough/transformation.py
- `/litellm/llms/anthropic/batches/transformation.py:16` — file not found at /litellm/llms/anthropic/batches/transformation.py
- `/litellm/llms/openai/vector_store_files/transformation.py:29` — file not found at /litellm/llms/openai/vector_store_files/transformation.py
- `/litellm/llms/openai/image_variations/transformation.py:15` — file not found at /litellm/llms/openai/image_variations/transformation.py
- `/litellm/llms/azure/responses/transformation.py:24` — file not found at /litellm/llms/azure/responses/transformation.py
- `/litellm/llms/azure/responses/o_series_transformation.py:27` — file not found at /litellm/llms/azure/responses/o_series_transformation.py
- `/litellm/llms/openai/responses/transformation.py:31` — file not found at /litellm/llms/openai/responses/transformation.py
- `/litellm/llms/cohere/rerank/transformation.py:18` — file not found at /litellm/llms/cohere/rerank/transformation.py
- `/litellm/llms/deepinfra/rerank/transformation.py:15` — file not found at /litellm/llms/deepinfra/rerank/transformation.py
- `/litellm/llms/huggingface/rerank/transformation.py:15` — file not found at /litellm/llms/huggingface/rerank/transformation.py
- `/litellm/llms/jina_ai/rerank/transformation.py:15` — file not found at /litellm/llms/jina_ai/rerank/transformation.py
- `/litellm/llms/nvidia_nim/rerank/transformation.py:14` — file not found at /litellm/llms/nvidia_nim/rerank/transformation.py
- `/litellm/llms/anthropic/files/transformation.py:15` — file not found at /litellm/llms/anthropic/files/transformation.py
- `/litellm/llms/manus/files/transformation.py:15` — file not found at /litellm/llms/manus/files/transformation.py
- `/litellm/llms/anthropic/skills/transformation.py:16` — file not found at /litellm/llms/anthropic/skills/transformation.py
- `/litellm/llms/sagemaker/chat/transformation.py:39` — file not found at /litellm/llms/sagemaker/chat/transformation.py

### baseline/litellm  — 51/81 grounded

**Unresolved**
- `litellm/llms/litellm/llms/a2a/chat/transformation.py:A2AConfig` — file not found at litellm/llms/litellm/llms/a2a/chat/transformation.py
- `litellm/llms/litellm/llms/anthropic/chat/transformation.py:AnthropicConfig` — file not found at litellm/llms/litellm/llms/anthropic/chat/transformation.py
- `litellm/llms/litellm/llms/anthropic/completion/transformation.py:AnthropicTextConfig` — file not found at litellm/llms/litellm/llms/anthropic/completion/transformation.py
- `litellm/llms/litellm/llms/azure/chat/gpt_transformation.py:AzureOpenAIConfig` — file not found at litellm/llms/litellm/llms/azure/chat/gpt_transformation.py
- `litellm/llms/litellm/llms/azure_ai/agents/transformation.py:AzureAIAgentsConfig` — file not found at litellm/llms/litellm/llms/azure_ai/agents/transformation.py
- `litellm/llms/litellm/llms/azure_ai/vector_stores/transformation.py:AzureAIVectorStoreConfig` — file not found at litellm/llms/litellm/llms/azure_ai/vector_stores/transformation.py
- `litellm/llms/litellm/llms/bedrock/batches/transformation.py:BedrockBatchesConfig` — file not found at litellm/llms/litellm/llms/bedrock/batches/transformation.py
- `litellm/llms/litellm/llms/bedrock/chat/agentcore/transformation.py:AmazonAgentCoreConfig` — file not found at litellm/llms/litellm/llms/bedrock/chat/agentcore/transformation.py
- `litellm/llms/litellm/llms/bedrock/chat/converse_transformation.py:AmazonConverseConfig` — file not found at litellm/llms/litellm/llms/bedrock/chat/converse_transformation.py
- `litellm/llms/litellm/llms/bedrock/chat/invoke_agent/transformation.py:AmazonInvokeAgentConfig` — file not found at litellm/llms/litellm/llms/bedrock/chat/invoke_agent/transformation.py
- `litellm/llms/litellm/llms/bedrock/files/transformation.py:BedrockFilesConfig` — file not found at litellm/llms/litellm/llms/bedrock/files/transformation.py
- `litellm/llms/litellm/llms/bytez/chat/transformation.py:BytezChatConfig` — file not found at litellm/llms/litellm/llms/bytez/chat/transformation.py
- `litellm/llms/litellm/llms/cohere/chat/transformation.py:CohereChatConfig` — file not found at litellm/llms/litellm/llms/cohere/chat/transformation.py
- `litellm/llms/litellm/llms/gigachat/chat/transformation.py:GigaChatConfig` — file not found at litellm/llms/litellm/llms/gigachat/chat/transformation.py
- `litellm/llms/litellm/llms/huggingface/embedding/transformation.py:HuggingFaceEmbeddingConfig` — file not found at litellm/llms/litellm/llms/huggingface/embedding/transformation.py
- `litellm/llms/litellm/llms/langflow/chat/transformation.py:LangFlowConfig` — file not found at litellm/llms/litellm/llms/langflow/chat/transformation.py
- `litellm/llms/litellm/llms/langgraph/chat/transformation.py:LangGraphConfig` — file not found at litellm/llms/litellm/llms/langgraph/chat/transformation.py
- `litellm/llms/litellm/llms/nlp_cloud/chat/transformation.py:NLPCloudConfig` — file not found at litellm/llms/litellm/llms/nlp_cloud/chat/transformation.py
- `litellm/llms/litellm/llms/oci/chat/transformation.py:OCIChatConfig` — file not found at litellm/llms/litellm/llms/oci/chat/transformation.py
- `litellm/llms/litellm/llms/ollama/chat/transformation.py:OllamaChatConfig` — file not found at litellm/llms/litellm/llms/ollama/chat/transformation.py
- `litellm/llms/litellm/llms/ollama/completion/transformation.py:OllamaConfig` — file not found at litellm/llms/litellm/llms/ollama/completion/transformation.py
- `litellm/llms/litellm/llms/petals/completion/transformation.py:PetalsConfig` — file not found at litellm/llms/litellm/llms/petals/completion/transformation.py
- `litellm/llms/litellm/llms/predibase/chat/transformation.py:PredibaseConfig` — file not found at litellm/llms/litellm/llms/predibase/chat/transformation.py
- `litellm/llms/litellm/llms/replicate/chat/transformation.py:ReplicateConfig` — file not found at litellm/llms/litellm/llms/replicate/chat/transformation.py
- `litellm/llms/litellm/llms/sagemaker/completion/transformation.py:SagemakerConfig` — file not found at litellm/llms/litellm/llms/sagemaker/completion/transformation.py
- `litellm/llms/litellm/llms/snowflake/chat/transformation.py:SnowflakeConfig` — file not found at litellm/llms/litellm/llms/snowflake/chat/transformation.py
- `litellm/llms/litellm/llms/triton/completion/transformation.py:TritonConfig` — file not found at litellm/llms/litellm/llms/triton/completion/transformation.py
- `litellm/llms/litellm/llms/vertex_ai/agent_engine/transformation.py:VertexAgentEngineConfig` — file not found at litellm/llms/litellm/llms/vertex_ai/agent_engine/transformation.py
- `litellm/llms/litellm/llms/vertex_ai/gemini/vertex_and_google_ai_studio_gemini.py:VertexGeminiConfig` — file not found at litellm/llms/litellm/llms/vertex_ai/gemini/vertex_and_google_ai_studio_gemini.py
- `litellm/llms/litellm/llms/watsonx/completion/transformation.py:IBMWatsonXAIConfig` — file not found at litellm/llms/litellm/llms/watsonx/completion/transformation.py

### baseline/netbox  — 141/144 grounded

**Hallucinated**
- `dcim/api/serializers_/device_components.py:473` — line 473 out of range (file only 472 lines) [via netbox/dcim/api/serializers_/device_components.py]
- `dcim/graphql/types.py:1034` — line 1034 out of range (file only 1013 lines) [via netbox/dcim/graphql/types.py]

**Unresolved**
- `dcim/models/virtualmachines.py:139` — file not found at dcim/models/virtualmachines.py

### baseline/netbox  — 133/134 grounded

**Unresolved**
- `netbox/extras/api/serializers_/configcontexts.py:config` — `config` not found anywhere in netbox/extras/api/serializers_/configcontexts.py

### baseline/saleor  — 37/117 grounded

**Hallucinated**
- `saleor/product/models.py:4002` — line 4002 out of range (file only 731 lines)
- `saleor/product/models.py:4059` — line 4059 out of range (file only 731 lines)
- `saleor/warehouse/models.py:30329` — line 30329 out of range (file only 689 lines)
- `saleor/discount/models.py:4049` — line 4049 out of range (file only 651 lines)
- `saleor/giftcard/models.py:3939` — line 3939 out of range (file only 151 lines)
- `saleor/checkout/models.py:3448` — line 3448 out of range (file only 538 lines)
- `saleor/order/models.py:3552` — line 3552 out of range (file only 995 lines)
- `saleor/discount/models.py:3449` — line 3449 out of range (file only 651 lines)
- `saleor/giftcard/models.py:33939` — line 33939 out of range (file only 151 lines)
- `saleor/warehouse/models.py:32529` — line 32529 out of range (file only 689 lines)
- `saleor/warehouse/reservations.py:34451` — line 34451 out of range (file only 437 lines)
- `saleor/warehouse/reservations.py:34452` — line 34452 out of range (file only 437 lines)
- `saleor/warehouse/management.py:3919` — line 3919 out of range (file only 1077 lines)
- `saleor/warehouse/management.py:3920` — line 3920 out of range (file only 1077 lines)
- `saleor/warehouse/management.py:3917` — line 3917 out of range (file only 1077 lines)
- `saleor/warehouse/models.py:3127` — line 3127 out of range (file only 689 lines)
- `saleor/warehouse/models.py:3182` — line 3182 out of range (file only 689 lines)
- `saleor/warehouse/webhooks/payloads.py:237` — line 237 out of range (file only 35 lines)
- `saleor/checkout/models.py:3530` — line 3530 out of range (file only 538 lines)
- `saleor/order/models.py:33758` — line 33758 out of range (file only 995 lines)
- `saleor/order/models.py:33759` — line 33759 out of range (file only 995 lines)
- `saleor/checkout/fetch.py:3101` — line 3101 out of range (file only 387 lines)
- `saleor/checkout/fetch.py:3110` — line 3110 out of range (file only 387 lines)
- `saleor/product/utils/availability.py:357` — line 357 out of range (file only 228 lines)
- `saleor/product/utils/availability.py:3179` — line 3179 out of range (file only 228 lines)
- `saleor/product/utils/variant_prices.py:3358` — line 3358 out of range (file only 363 lines)
- `saleor/product/utils/costs.py:325` — line 325 out of range (file only 81 lines)
- `saleor/product/utils/costs.py:354` — line 354 out of range (file only 81 lines)
- `saleor/tax/calculations/order.py:3149` — line 3149 out of range (file only 189 lines)
- `saleor/giftcard/utils.py:3139` — line 3139 out of range (file only 290 lines)
- `saleor/checkout/utils.py:3333` — line 3333 out of range (file only 1061 lines)
- `saleor/order/utils.py:3247` — line 3247 out of range (file only 1325 lines)
- `saleor/order/utils.py:3419` — line 3419 out of range (file only 1325 lines)
- `saleor/webhook/tests/test_webhook_serializers.py:3212` — line 3212 out of range (file only 268 lines)
- `saleor/graphql/order/mutations/order_fulfill.py:3221` — line 3221 out of range (file only 305 lines)
- `saleor/discount/utils/promotion.py:3489` — line 3489 out of range (file only 971 lines)
- `saleor/discount/utils/promotion.py:3877` — line 3877 out of range (file only 971 lines)
- `saleor/discount/utils/voucher.py:3232` — line 3232 out of range (file only 719 lines)
- `saleor/discount/utils/voucher.py:3233` — line 3233 out of range (file only 719 lines)
- `saleor/checkout/tests/test_cart.py:3119` — line 3119 out of range (file only 290 lines)
- `saleor/checkout/tests/test_checkout.py:31248` — line 31248 out of range (file only 1963 lines)
- `saleor/plugins/avatax/tests/test_avatax.py:31439` — line 31439 out of range (file only 6863 lines)
- `saleor/plugins/avatax/tests/test_avatax.py:33118` — line 33118 out of range (file only 6863 lines)
- `saleor/warehouse/reservations.py:3333` — line 3333 out of range (file only 437 lines)
- `saleor/warehouse/reservations.py:3343` — line 3343 out of range (file only 437 lines)
- `saleor/warehouse/reservations.py:3354` — line 3354 out of range (file only 437 lines)
- `saleor/warehouse/tests/test_stock.py:3319` — line 3319 out of range (file only 319 lines)
- `saleor/warehouse/tests/test_warehouse.py:3254` — line 3254 out of range (file only 825 lines)
- `saleor/order/tests/test_order_actions.py:3567` — line 3567 out of range (file only 3197 lines)
- `saleor/giftcard/tests/test_utils.py:3713` — line 3713 out of range (file only 865 lines)
- `saleor/warehouse/reservations.py:3262` — line 3262 out of range (file only 437 lines)
- `saleor/warehouse/reservations.py:3278` — line 3278 out of range (file only 437 lines)
- `saleor/warehouse/webhooks/payloads.py:3237` — line 3237 out of range (file only 35 lines)
- `saleor/warehouse/tests/test_stock.py:348` — line 348 out of range (file only 319 lines)
- `saleor/checkout/tests/test_order_from_checkout.py:3876` — line 3876 out of range (file only 1072 lines)
- `saleor/checkout/tests/test_fetch.py:3342` — line 3342 out of range (file only 359 lines)
- `saleor/order/tests/test_order_utils.py:3202` — line 3202 out of range (file only 803 lines)
- `saleor/discount/tests/test_discounts.py:3684` — line 3684 out of range (file only 803 lines)
- `saleor/tax/tests/test_checkout_calculations.py:3137` — line 3137 out of range (file only 1486 lines)
- `saleor/tax/tests/test_checkout_calculations.py:31252` — line 31252 out of range (file only 1486 lines)
- `saleor/tax/tests/test_order_calculations.py:3260` — line 3260 out of range (file only 905 lines)
- `saleor/webhook/tests/test_webhook_payloads.py:31031` — line 31031 out of range (file only 1812 lines)
- `saleor/graphql/checkout/tests/test_checkout.py:32066` — line 32066 out of range (file only 5406 lines)
- `saleor/graphql/product/tests/mutations/test_product_variant_bulk_create.py:3509` — line 3509 out of range (file only 2784 lines)
- `saleor/checkout/utils.py:3334` — line 3334 out of range (file only 1061 lines)
- `saleor/order/utils.py:3248` — line 3248 out of range (file only 1325 lines)
- `saleor/tax/calculations/order.py:3150` — line 3150 out of range (file only 189 lines)
- `saleor/plugins/avatax/tests/test_avatax.py:31109` — line 31109 out of range (file only 6863 lines)
- `saleor/webhook/tests/test_webhook_serializers.py:3213` — line 3213 out of range (file only 268 lines)
- `saleor/order/utils.py:3255` — line 3255 out of range (file only 1325 lines)
- `saleor/checkout/utils.py:3336` — line 3336 out of range (file only 1061 lines)
- `saleor/webhook/tests/test_webhook_serializers.py:3254` — line 3254 out of range (file only 268 lines)
- `saleor/giftcard/utils.py:3140` — line 3140 out of range (file only 290 lines)
- `saleor/warehouse/models.py:3128` — line 3128 out of range (file only 689 lines)
- `saleor/product/utils/costs.py:365` — line 365 out of range (file only 81 lines)
- `saleor/core/utils/random_data.py:3891` — line 3891 out of range (file only 1905 lines)
- `saleor/order/utils.py:3420` — line 3420 out of range (file only 1325 lines)
- `saleor/product/utils/variant_prices.py:3276` — line 3276 out of range (file only 363 lines)
- `saleor/warehouse/models.py:3329` — line 3329 out of range (file only 689 lines)

**Unresolved**
- `saleor/discount/tests/test_utils.py:3263` — file not found at saleor/discount/tests/test_utils.py

### baseline/sentry  — 385/390 grounded

**Hallucinated**
- `src/sentry_plugins/pagerduty/plugin.py:137` — line 137 out of range (file only 122 lines)

**Unresolved**
- `src/sentry/api/endpoints/organization_group_index.py:107` — file not found at src/sentry/api/endpoints/organization_group_index.py
- `src/sentry/api/endpoints/project_group_index.py:159` — file not found at src/sentry/api/endpoints/project_group_index.py
- `src/sentry/api/endpoints/group_details.py:406` — file not found at src/sentry/api/endpoints/group_details.py
- `src/sentry/api/endpoints/organization_shortid.py:21` — file not found at src/sentry/api/endpoints/organization_shortid.py

### baseline/sentry  — 212/218 grounded

**Unresolved**
- `src/sentry/models/groupderiveddata.py:31` — file not found at src/sentry/models/groupderiveddata.py
- `src/sentry/issues/endpoints/group_external_issues.py:23` — file not found at src/sentry/issues/endpoints/group_external_issues.py
- `src/sentry/issues/endpoints/group_external_issue_details.py:22` — file not found at src/sentry/issues/endpoints/group_external_issue_details.py
- `src/sentry/api/serializers/rest_framework/group.py:26` — file not found at src/sentry/api/serializers/rest_framework/group.py
- `integration/slack/message_builder/issues.py:47` — file not found at integration/slack/message_builder/issues.py
- `integration/jira/integration.py:41` — file not found at integration/jira/integration.py

### baseline/wagtail  — 95/96 grounded

**Unresolved**
- `wagtail/templates/templatetags/wagtailcore_tags.py:70` — file not found at wagtail/templates/templatetags/wagtailcore_tags.py

### baseline/wagtail  — 5/111 grounded

**Unresolved**
- `/wagtail/models/pages.py:289` — file not found at /wagtail/models/pages.py
- `/wagtail/admin/views/pages/edit.py:52` — file not found at /wagtail/admin/views/pages/edit.py
- `/wagtail/admin/views/pages/create.py:82` — file not found at /wagtail/admin/views/pages/create.py
- `/wagtail/admin/views/pages/delete.py:1` — file not found at /wagtail/admin/views/pages/delete.py
- `/wagtail/admin/views/pages/preview.py:30` — file not found at /wagtail/admin/views/pages/preview.py
- `/wagtail/admin/views/pages/search.py:1` — file not found at /wagtail/admin/views/pages/search.py
- `/wagtail/admin/views/pages/listing.py:1` — file not found at /wagtail/admin/views/pages/listing.py
- `/wagtail/admin/views/pages/choose_parent.py:1` — file not found at /wagtail/admin/views/pages/choose_parent.py
- `/wagtail/admin/views/pages/convert_alias.py:1` — file not found at /wagtail/admin/views/pages/convert_alias.py
- `/wagtail/admin/views/pages/move.py:1` — file not found at /wagtail/admin/views/pages/move.py
- `/wagtail/admin/views/pages/bulk_actions/move.py:1` — file not found at /wagtail/admin/views/pages/bulk_actions/move.py
- `/wagtail/admin/views/pages/copy.py:49` — file not found at /wagtail/admin/views/pages/copy.py
- `/wagtail/admin/views/pages/bulk_actions/page_bulk_action.py:1` — file not found at /wagtail/admin/views/pages/bulk_actions/page_bulk_action.py
- `/wagtail/admin/views/reports/page_types_usage.py:37` — file not found at /wagtail/admin/views/reports/page_types_usage.py
- `/wagtail/admin/views/reports/locked_pages.py:65` — file not found at /wagtail/admin/views/reports/locked_pages.py
- `/wagtail/admin/views/workflows.py:301` — file not found at /wagtail/admin/views/workflows.py
- `/wagtail/admin/views/home.py:124` — file not found at /wagtail/admin/views/home.py
- `/wagtail/admin/views/home.py:241` — file not found at /wagtail/admin/views/home.py
- `/wagtail/admin/views/home.py:278` — file not found at /wagtail/admin/views/home.py
- `/wagtail/admin/views/chooser.py:259` — file not found at /wagtail/admin/views/chooser.py
- `/wagtail/admin/views/chooser.py:514` — file not found at /wagtail/admin/views/chooser.py
- `/wagtail/admin/views/page_privacy.py:26` — file not found at /wagtail/admin/views/page_privacy.py
- `/wagtail/admin/userbar.py:264` — file not found at /wagtail/admin/userbar.py
- `/wagtail/admin/wagtail_hooks.py:310` — file not found at /wagtail/admin/wagtail_hooks.py
- `/wagtail/admin/ui/menus/pages.py:42` — file not found at /wagtail/admin/ui/menus/pages.py
- `/wagtail/admin/models.py:33` — file not found at /wagtail/admin/models.py
- `/wagtail/admin/forms/workflows.py:85` — file not found at /wagtail/admin/forms/workflows.py
- `/wagtail/admin/forms/workflows.py:119` — file not found at /wagtail/admin/forms/workflows.py
- `/wagtail/admin/forms/pages.py:34` — file not found at /wagtail/admin/forms/pages.py
- `/wagtail/admin/forms/pages.py:270` — file not found at /wagtail/admin/forms/pages.py
- `/wagtail/admin/forms/pages.py:288` — file not found at /wagtail/admin/forms/pages.py
- `/wagtail/actions/copy_page.py:83` — file not found at /wagtail/actions/copy_page.py
- `/wagtail/actions/move_page.py:41` — file not found at /wagtail/actions/move_page.py
- `/wagtail/actions/move_page.py:97` — file not found at /wagtail/actions/move_page.py
- `/wagtail/actions/move_page.py:63` — file not found at /wagtail/actions/move_page.py
- `/wagtail/actions/publish_page_revision.py:1` — file not found at /wagtail/actions/publish_page_revision.py
- `/wagtail/actions/unpublish_page.py:1` — file not found at /wagtail/actions/unpublish_page.py
- `/wagtail/actions/delete_page.py:1` — file not found at /wagtail/actions/delete_page.py
- `/wagtail/actions/create_alias.py:213` — file not found at /wagtail/actions/create_alias.py
- `/wagtail/permission_policies/pages.py:89` — file not found at /wagtail/permission_policies/pages.py
- `/wagtail/models/pages.py:2154` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2198` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2471` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2538` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2558` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2658` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2849` — file not found at /wagtail/models/pages.py
- `/wagtail/models/workflows.py:116` — file not found at /wagtail/models/workflows.py
- `/wagtail/models/workflows.py:2558` — file not found at /wagtail/models/workflows.py
- `/wagtail/users/forms.py:286` — file not found at /wagtail/users/forms.py
- `/wagtail/users/tests/test_admin_views.py:2024` — file not found at /wagtail/users/tests/test_admin_views.py
- `/wagtail/search/models.py:1` — file not found at /wagtail/search/models.py
- `/wagtail/models/pages.py:402` — file not found at /wagtail/models/pages.py
- `/wagtail/api/v2/views.py:529` — file not found at /wagtail/api/v2/views.py
- `/wagtail/api/v2/views.py:1` — file not found at /wagtail/api/v2/views.py
- `/wagtail/contrib/forms/models.py:45` — file not found at /wagtail/contrib/forms/models.py
- `/wagtail/contrib/forms/models.py:104` — file not found at /wagtail/contrib/forms/models.py
- `/wagtail/contrib/forms/views.py:163` — file not found at /wagtail/contrib/forms/views.py
- `/wagtail/contrib/redirects/models.py:1` — file not found at /wagtail/contrib/redirects/models.py
- `/wagtail/contrib/redirects/signal_handlers.py:180` — file not found at /wagtail/contrib/redirects/signal_handlers.py
- `/wagtail/contrib/sitemaps/models.py:1` — file not found at /wagtail/contrib/sitemaps/models.py
- `/wagtail/contrib/sitemaps/tests.py:20` — file not found at /wagtail/contrib/sitemaps/tests.py
- `/wagtail/contrib/frontend_cache/models.py:1` — file not found at /wagtail/contrib/frontend_cache/models.py
- `/wagtail/contrib/simple_translation/models.py:1` — file not found at /wagtail/contrib/simple_translation/models.py
- `/wagtail/models/reference_index.py:366` — file not found at /wagtail/models/reference_index.py
- `/wagtail/models/pages.py:2674` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2746` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2629` — file not found at /wagtail/models/pages.py
- `/wagtail/models/audit_log.py:1` — file not found at /wagtail/models/audit_log.py
- `/wagtail/tests/test_audit_log.py:157` — file not found at /wagtail/tests/test_audit_log.py
- `/wagtail/admin/rich_text/converters/editor_html.py:174` — file not found at /wagtail/admin/rich_text/converters/editor_html.py
- `/wagtail/admin/rich_text/converters/html_to_contentstate.py:236` — file not found at /wagtail/admin/rich_text/converters/html_to_contentstate.py
- `/wagtail/project_template/search/views.py:20` — file not found at /wagtail/project_template/search/views.py
- `/wagtail/admin/panels/page_utils.py:1` — file not found at /wagtail/admin/panels/page_utils.py
- `/wagtail/admin/panels/page_chooser_panel.py:1` — file not found at /wagtail/admin/panels/page_chooser_panel.py
- `/wagtail/models/pages.py:2147` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2188` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2470` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2537` — file not found at /wagtail/models/pages.py
- `/wagtail/contrib/forms/models.py:36` — file not found at /wagtail/contrib/forms/models.py
- `/wagtail/contrib/forms/models.py:74` — file not found at /wagtail/contrib/forms/models.py
- `/wagtail/contrib/forms/panels.py:7` — file not found at /wagtail/contrib/forms/panels.py
- `/wagtail/models/pages.py:2669` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2744` — file not found at /wagtail/models/pages.py
- `/wagtail/models/revisions.py:84` — file not found at /wagtail/models/revisions.py
- `/wagtail/models/pages.py:499` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:526` — file not found at /wagtail/models/pages.py
- `/wagtail/models/sites.py:1` — file not found at /wagtail/models/sites.py
- `/wagtail/admin/models.py:102` — file not found at /wagtail/admin/models.py
- `/wagtail/models/pages.py:2133` — file not found at /wagtail/models/pages.py
- `/wagtail/admin/views/pages/bulk_actions/move.py:106` — file not found at /wagtail/admin/views/pages/bulk_actions/move.py
- `/wagtail/models/pages.py:2199` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2200` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2201` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2202` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2203` — file not found at /wagtail/models/pages.py
- `/wagtail/models/pages.py:2204` — file not found at /wagtail/models/pages.py
- `/wagtail/models/workflows.py:117` — file not found at /wagtail/models/workflows.py
- `/wagtail/models/workflows.py:118` — file not found at /wagtail/models/workflows.py
- `/wagtail/models/workflows.py:119` — file not found at /wagtail/models/workflows.py
- `/wagtail/models/workflows.py:120` — file not found at /wagtail/models/workflows.py
- `/wagtail/models/workflows.py:121` — file not found at /wagtail/models/workflows.py
- `/wagtail/models/workflows.py:122` — file not found at /wagtail/models/workflows.py
- `/wagtail/models/workflows.py:123` — file not found at /wagtail/models/workflows.py
- `/wagtail/api/v2/tests/test_pages.py:25` — file not found at /wagtail/api/v2/tests/test_pages.py
- `/wagtail/models/i18n.py:1` — file not found at /wagtail/models/i18n.py

## sense

### sense/healthchecks  — 36/37 grounded

**Unresolved**
- `hc/api/transports.py:Now` — `Now` not found anywhere in hc/api/transports.py

### sense/litellm  — 165/166 grounded

**Unresolved**
- `/Users/luc/Developer/luuuc/oss/sense-benchmark/sense/litellm/litellm/litellm/llms/triton/completion/transformation.py:10` — file not found at /Users/luc/Developer/luuuc/oss/sense-benchmark/sense/litellm/litellm/litellm/llms/triton/completion/transformation.py

### sense/netbox  — 238/241 grounded

**Unresolved**
- `netbox/dcim/migrations/0235_cabletermination_cable_site_cache.py:5` — file not found at netbox/dcim/migrations/0235_cabletermination_cable_site_cache.py
- `netbox/vpn/models.py:1` — file not found at netbox/vpn/models.py
- `netbox/vpn/api/serializers_/vpns.py:1` — file not found at netbox/vpn/api/serializers_/vpns.py

### sense/netbox  — 148/169 grounded

**Hallucinated**
- `netbox/dcim/models/devices.py:1463` — line 1463 out of range (file only 1421 lines)
- `netbox/dcim/models/devices.py:1548` — line 1548 out of range (file only 1421 lines)
- `netbox/dcim/models/devices.py:1518` — line 1518 out of range (file only 1421 lines)
- `netbox/dcim/models/devices.py:1545` — line 1545 out of range (file only 1421 lines)
- `netbox/dcim/models/devices.py:1542` — line 1542 out of range (file only 1421 lines)
- `netbox/dcim/models/devices.py:1533` — line 1533 out of range (file only 1421 lines)
- `netbox/dcim/models/devices.py:1527` — line 1527 out of range (file only 1421 lines)
- `netbox/dcim/models/devices.py:1536` — line 1536 out of range (file only 1421 lines)
- `netbox/ipam/models/services.py:1289` — line 1289 out of range (file only 112 lines)
- `netbox/ipam/models/fhrp.py:923` — line 923 out of range (file only 130 lines)
- `netbox/ipam/models/vlans.py:1134` — line 1134 out of range (file only 447 lines)
- `netbox/virtualization/models/clusters.py:362` — line 362 out of range (file only 152 lines)
- `netbox/circuits/models/virtual_circuits.py:504` — line 504 out of range (file only 201 lines)
- `netbox/circuits/models/virtual_circuits.py:577` — line 577 out of range (file only 201 lines)
- `netbox/netbox/api/views.py:426` — line 426 out of range (file only 77 lines)
- `netbox/netbox/graphql/filters.py:167` — line 167 out of range (file only 62 lines)
- `netbox/dcim/utils.py:1063` — line 1063 out of range (file only 171 lines)
- `netbox/dcim/utils.py:1070` — line 1070 out of range (file only 171 lines)

**Unresolved**
- `netbox/extras/models/webhooks.py:153` — file not found at netbox/extras/models/webhooks.py
- `netbox/extras/models/event_rules.py:100` — file not found at netbox/extras/models/event_rules.py
- `netbox/dcim/migrations/0235_cabletermination_cable_site_cache.py:5` — file not found at netbox/dcim/migrations/0235_cabletermination_cable_site_cache.py

### sense/saleor  — 218/227 grounded

**Hallucinated**
- `saleor/checkout/models.py:586` — line 586 out of range (file only 538 lines)
- `saleor/warehouse/availability.py:642` — line 642 out of range (file only 623 lines)
- `saleor/graphql/product/filters/product_variant.py:815` — line 815 out of range (file only 799 lines)
- `warehouse/availability.py:642` — line 642 out of range (file only 623 lines) [via saleor/warehouse/availability.py]

**Unresolved**
- `saleor/graphql/product/tests/test_product.py::test_variant_query` — file not found at saleor/graphql/product/tests/test_product.py
- `saleor/graphql/product/tests/test_product.py::test_product_variant_by_id` — file not found at saleor/graphql/product/tests/test_product.py
- `saleor/plugins/webhook/tests/test_webhook.py::test_checkout_payload_includes_promotion` — `test_checkout_payload_includes_promotion` not found anywhere in saleor/plugins/webhook/tests/test_webhook.py
- `saleor/graphql/product/tests/test_product.py::test_product_variant_restriction` — file not found at saleor/graphql/product/tests/test_product.py
- `saleor/product/tests/test_product.py::test_get_product_price_range` — `test_get_product_price_range` not found anywhere in saleor/product/tests/test_product.py

### sense/sentry  — 177/178 grounded

**Unresolved**
- `/Users/luc/Developer/luuuc/oss/sence-benchmark/sense/sentry/src/sentry/issues/endpoints/organization_group_index.py:321` — file not found at /Users/luc/Developer/luuuc/oss/sence-benchmark/sense/sentry/src/sentry/issues/endpoints/organization_group_index.py

### sense/sentry  — 119/121 grounded

**Unresolved**
- `src/sentry/events/event_manager.py:1532` — file not found at src/sentry/events/event_manager.py
- `src/sentry/tasks/seer/night_shift/delivery.py:97` — file not found at src/sentry/tasks/seer/night_shift/delivery.py

### sense/wagtail  — 213/215 grounded

**Hallucinated**
- `wagtail/contrib/frontend_cache/signal_handlers.py:76` — line 76 out of range (file only 23 lines)
- `contrib/frontend_cache/signal_handlers.py:76` — line 76 out of range (file only 23 lines) [via wagtail/contrib/frontend_cache/signal_handlers.py]

### sense/wagtail  — 79/81 grounded

**Hallucinated**
- `wagtail/models/sites.py:790` — line 790 out of range (file only 328 lines)
- `wagtail/url_routing.py:931` — line 931 out of range (file only 16 lines)
