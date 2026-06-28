# Citation hallucinations

Citations the answer printed that did not resolve against the repo checked out at the benchmarked commit. **Hallucinated** = line number beyond end-of-file (a made-up number). **Unresolved** = file not in the repo, or symbol not within ±5 lines of the cited line.

Reported for transparency; not folded into the headline score.

## baseline

### baseline/forem  — 260/263 grounded

**Hallucinated**
- `app/services/articles/feeds/latest.rb:127` — line 127 out of range (file only 20 lines)
- `app/services/articles/feeds/timeframe.rb:123` — line 123 out of range (file only 19 lines)
- `app/services/moderator/sink_articles.rb:11` — line 11 out of range (file only 7 lines)

### baseline/gitlabhq  — 399/412 grounded

**Unresolved**
- `spec/models/merge_request_diff_spec.rb:verify` — `verify` not found anywhere in spec/models/merge_request_diff_spec.rb
- `spec/models/merge_requests/merge_data_spec.rb:verify` — `verify` not found anywhere in spec/models/merge_requests/merge_data_spec.rb
- `spec/services/merge_requests/create_service_spec.rb:verify` — `verify` not found anywhere in spec/services/merge_requests/create_service_spec.rb
- `spec/services/merge_requests/update_service_spec.rb:verify` — `verify` not found anywhere in spec/services/merge_requests/update_service_spec.rb
- `spec/services/merge_requests/after_create_service_spec.rb:verify` — `verify` not found anywhere in spec/services/merge_requests/after_create_service_spec.rb
- `spec/services/projects/destroy_service_spec.rb:verify` — `verify` not found anywhere in spec/services/projects/destroy_service_spec.rb
- `spec/workers/new_merge_request_worker_spec.rb:verify` — `verify` not found anywhere in spec/workers/new_merge_request_worker_spec.rb
- `spec/workers/merge_worker_spec.rb:verify` — `verify` not found anywhere in spec/workers/merge_worker_spec.rb
- `spec/workers/merge_requests/create_pipeline_worker_spec.rb:verify` — `verify` not found anywhere in spec/workers/merge_requests/create_pipeline_worker_spec.rb
- `spec/requests/api/merge_requests_spec.rb:verify` — `verify` not found anywhere in spec/requests/api/merge_requests_spec.rb
- `spec/requests/api/graphql/merge_request/merge_request_spec.rb:verify` — `verify` not found anywhere in spec/requests/api/graphql/merge_request/merge_request_spec.rb
- `spec/policies/merge_request_policy_spec.rb:verify` — `verify` not found anywhere in spec/policies/merge_request_policy_spec.rb
- `spec/policies/project_policy_spec.rb:verify` — `verify` not found anywhere in spec/policies/project_policy_spec.rb

### baseline/langchainrb  — 161/163 grounded

**Unresolved**
- `_response.rb:subclass` — file not found at _response.rb
- `_message.rb:subclass` — file not found at _message.rb

### baseline/langchainrb  — 124/126 grounded

**Unresolved**
- `_response.rb:define` — file not found at _response.rb
- `_message.rb:define` — file not found at _message.rb

### baseline/llm.rb  — 117/139 grounded

**Unresolved**
- `lib/llm/providers/newprovider.rb:provider` — file not found at lib/llm/providers/newprovider.rb
- `lib/llm/providers/newprovider/request_adapter.rb:provider` — file not found at lib/llm/providers/newprovider/request_adapter.rb
- `lib/llm/providers/newprovider/request_adapter/completion.rb:message` — file not found at lib/llm/providers/newprovider/request_adapter/completion.rb
- `lib/llm/providers/newprovider/response_adapter.rb:adapt` — file not found at lib/llm/providers/newprovider/response_adapter.rb
- `lib/llm/providers/newprovider/response_adapter/completion.rb:LLM::Contract::Completion` — file not found at lib/llm/providers/newprovider/response_adapter/completion.rb
- `lib/llm/providers/newprovider/response_adapter/models.rb:only` — file not found at lib/llm/providers/newprovider/response_adapter/models.rb
- `lib/llm/providers/newprovider/response_adapter/embedding.rb:only` — file not found at lib/llm/providers/newprovider/response_adapter/embedding.rb
- `lib/llm/providers/newprovider/models.rb:only` — file not found at lib/llm/providers/newprovider/models.rb
- `lib/llm/providers/newprovider/stream_parser.rb:required` — file not found at lib/llm/providers/newprovider/stream_parser.rb
- `lib/llm/providers/newprovider/stream_decoder.rb:only` — file not found at lib/llm/providers/newprovider/stream_decoder.rb
- `lib/llm/providers/newprovider/error_handler.rb:typed` — file not found at lib/llm/providers/newprovider/error_handler.rb
- `spec/newprovider/chat_spec.rb:context` — file not found at spec/newprovider/chat_spec.rb
- `spec/newprovider/models_spec.rb:models` — file not found at spec/newprovider/models_spec.rb
- `spec/newprovider/request_adapter_spec.rb:prompt` — file not found at spec/newprovider/request_adapter_spec.rb
- `spec/newprovider/response_adapter/response_adapter_completion_spec.rb:completion` — file not found at spec/newprovider/response_adapter/response_adapter_completion_spec.rb
- `spec/newprovider/stream_parser_spec.rb:incremental` — file not found at spec/newprovider/stream_parser_spec.rb
- `spec/newprovider/error_spec.rb:failed` — file not found at spec/newprovider/error_spec.rb
- `spec/newprovider/embedding_spec.rb:only` — file not found at spec/newprovider/embedding_spec.rb
- `lib/llm.rb:add` — `add` not found anywhere in lib/llm.rb
- `spec/setup.rb:add` — `add` not found anywhere in spec/setup.rb
- `spec/provider_spec.rb:add` — `add` not found anywhere in spec/provider_spec.rb
- `spec/registry_spec.rb:add` — `add` not found anywhere in spec/registry_spec.rb

### baseline/redmine  — 173/174 grounded

**Unresolved**
- `test/functional/journals_controller_test.rb:history` — `history` not found anywhere in test/functional/journals_controller_test.rb

### baseline/ruby_llm  — 121/126 grounded

**Unresolved**
- `lib/ruby_llm/providers/new_provider.rb:mirror` — file not found at lib/ruby_llm/providers/new_provider.rb
- `lib/ruby_llm/providers/new_provider/capabilities.rb:needed` — file not found at lib/ruby_llm/providers/new_provider/capabilities.rb
- `lib/ruby_llm/providers/new_provider/chat.rb:only` — file not found at lib/ruby_llm/providers/new_provider/chat.rb
- `lib/ruby_llm/providers/new_provider/media.rb:only` — file not found at lib/ruby_llm/providers/new_provider/media.rb
- `lib/ruby_llm/providers/new_provider/models.rb:only` — file not found at lib/ruby_llm/providers/new_provider/models.rb

## sense

### sense/langchainrb  — 235/243 grounded

**Unresolved**
- `lib/langchain/llm/new_provider.rb:new` — file not found at lib/langchain/llm/new_provider.rb
- `lib/langchain/llm/response/new_provider_response.rb:new` — file not found at lib/langchain/llm/response/new_provider_response.rb
- `lib/langchain/assistant/llm/adapters/new_provider.rb:new` — file not found at lib/langchain/assistant/llm/adapters/new_provider.rb
- `lib/langchain/assistant/messages/new_provider_message.rb:new` — file not found at lib/langchain/assistant/messages/new_provider_message.rb
- `spec/lib/langchain/llm/new_provider_spec.rb:raw` — file not found at spec/lib/langchain/llm/new_provider_spec.rb
- `spec/lib/langchain/llm/response/new_provider_response_spec.rb:response` — file not found at spec/lib/langchain/llm/response/new_provider_response_spec.rb
- `spec/lib/langchain/assistant/llm/adapters/new_provider_spec.rb:adapter` — file not found at spec/lib/langchain/assistant/llm/adapters/new_provider_spec.rb
- `spec/lib/langchain/assistant/messages/new_provider_message_spec.rb:message` — file not found at spec/lib/langchain/assistant/messages/new_provider_message_spec.rb

### sense/langchainrb  — 216/222 grounded

**Unresolved**
- `_response.rb:new` — file not found at _response.rb
- `_message.rb:new` — file not found at _message.rb
- `_spec.rb:client` — file not found at _spec.rb
- `_response_spec.rb:chat_completion` — file not found at _response_spec.rb
- `_message_spec.rb:role` — file not found at _message_spec.rb
- `_spec.rb:build_chat_params` — file not found at _spec.rb

### sense/llm.rb  — 198/213 grounded

**Unresolved**
- `/request_adapter.rb:1` — file not found at /request_adapter.rb
- `/request_adapter/completion.rb:1` — file not found at /request_adapter/completion.rb
- `/response_adapter.rb:1` — file not found at /response_adapter.rb
- `/response_adapter/completion.rb:1` — file not found at /response_adapter/completion.rb
- `/stream_parser.rb:1` — file not found at /stream_parser.rb
- `/error_handler.rb:1` — file not found at /error_handler.rb
- `/stream_decoder.rb:1` — file not found at /stream_decoder.rb
- `/models.rb:1` — file not found at /models.rb
- `/response_adapter/embedding.rb:1` — file not found at /response_adapter/embedding.rb
- `/chat_spec.rb:1` — file not found at /chat_spec.rb
- `/request_adapter_spec.rb:1` — file not found at /request_adapter_spec.rb
- `/response_adapter/response_adapter_completion_spec.rb:1` — file not found at /response_adapter/response_adapter_completion_spec.rb
- `/stream_parser_spec.rb:1` — file not found at /stream_parser_spec.rb
- `/error_spec.rb:1` — file not found at /error_spec.rb
- `/embedding_spec.rb:1` — file not found at /embedding_spec.rb

### sense/llm.rb.before-steering  — 204/217 grounded

**Unresolved**
- `lib/llm.rb:add` — `add` not found anywhere in lib/llm.rb
- `spec/setup.rb:add` — `add` not found anywhere in spec/setup.rb
- `/request_adapter.rb:aggregate` — file not found at /request_adapter.rb
- `/request_adapter/completion.rb:convert` — file not found at /request_adapter/completion.rb
- `/response_adapter.rb:select` — file not found at /response_adapter.rb
- `/response_adapter/completion.rb:expose` — file not found at /response_adapter/completion.rb
- `/response_adapter/embedding.rb:only` — file not found at /response_adapter/embedding.rb
- `/stream_parser.rb:merge` — file not found at /stream_parser.rb
- `/error_handler.rb:implement` — file not found at /error_handler.rb
- `/chat_spec.rb:include` — file not found at /chat_spec.rb
- `/error_spec.rb:status` — file not found at /error_spec.rb
- `/stream_parser_spec.rb:parser` — file not found at /stream_parser_spec.rb
- `spec/provider_spec.rb:add` — `add` not found anywhere in spec/provider_spec.rb

### sense/redmine.before-steering  — 194/202 grounded

**Unresolved**
- `test/unit/issue_test.rb:core` — `core` not found anywhere in test/unit/issue_test.rb
- `test/unit/issue_nested_set_test.rb:nested` — `nested` not found anywhere in test/unit/issue_nested_set_test.rb
- `test/unit/attachment_transaction_test.rb:issue` — `issue` not found anywhere in test/unit/attachment_transaction_test.rb
- `test/unit/mailer_test.rb:inbound` — `inbound` not found anywhere in test/unit/mailer_test.rb
- `test/unit/issue_scopes_test.rb:query` — `query` not found anywhere in test/unit/issue_scopes_test.rb
- `versions_controller_test.rb:reporting` — `reporting` not found anywhere in versions_controller_test.rb [via test/functional/versions_controller_test.rb]
- `attachments_test.rb:API` — ambiguous: 3 files match `attachments_test.rb` (test/integration/attachments_test.rb, test/integration/routing/attachments_test.rb, test/integration/api_test/attachments_test.rb)
- `gantts_test.rb:end` — ambiguous: 2 files match `gantts_test.rb` (test/integration/routing/gantts_test.rb, test/system/gantts_test.rb)

### sense/ruby_llm  — 129/137 grounded

**Hallucinated**
- `spec/support/models_to_test.rb:125` — line 125 out of range (file only 118 lines)

**Unresolved**
- `lib/ruby_llm/providers/new_provider.rb:provider` — file not found at lib/ruby_llm/providers/new_provider.rb
- `lib/ruby_llm/providers/new_provider/chat.rb:only` — file not found at lib/ruby_llm/providers/new_provider/chat.rb
- `lib/ruby_llm/providers/new_provider/models.rb:if` — file not found at lib/ruby_llm/providers/new_provider/models.rb
- `lib/ruby_llm/providers/new_provider/media.rb:if` — file not found at lib/ruby_llm/providers/new_provider/media.rb
- `lib/ruby_llm/providers/new_provider/streaming.rb:if` — file not found at lib/ruby_llm/providers/new_provider/streaming.rb
- `lib/ruby_llm/providers/new_provider/images.rb:if` — file not found at lib/ruby_llm/providers/new_provider/images.rb
- `lib/ruby_llm/providers/new_provider/capabilities.rb:if` — file not found at lib/ruby_llm/providers/new_provider/capabilities.rb
