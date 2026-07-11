# Citation hallucinations

Citations the answer printed that did not resolve against the repo checked out at the benchmarked commit. **Hallucinated** = line number beyond end-of-file (a made-up number). **Unresolved** = file not in the repo, or symbol not within ±5 lines of the cited line.

Reported for transparency; not folded into the headline score.

## baseline

### baseline/litellm  — 221/224 grounded

**Unresolved**
- `.../amazon_nova_transformation.py:22` — file not found at .../amazon_nova_transformation.py
- `.../amazon_cohere_transformation.py:10` — file not found at .../amazon_cohere_transformation.py
- `.../anthropic_claude3_transformation.py:39` — file not found at .../anthropic_claude3_transformation.py

### baseline/litellm  — 181/191 grounded

**Unresolved**
- `bedrock/.../base_invoke_transformation.py:41` — file not found at bedrock/.../base_invoke_transformation.py
- `.../amazon_llama_transformation.py:10` — file not found at .../amazon_llama_transformation.py
- `.../amazon_qwen3_transformation.py:22` — file not found at .../amazon_qwen3_transformation.py
- `.../amazon_ai21_transformation.py:10` — file not found at .../amazon_ai21_transformation.py
- `.../amazon_mistral_transformation.py:14` — file not found at .../amazon_mistral_transformation.py
- `.../amazon_titan_transformation.py:12` — file not found at .../amazon_titan_transformation.py
- `.../amazon_twelvelabs_pegasus_transformation.py:35` — file not found at .../amazon_twelvelabs_pegasus_transformation.py
- `.../amazon_deepseek_transformation.py:28` — file not found at .../amazon_deepseek_transformation.py
- `.../amazon_qwen2_transformation.py:24` — file not found at .../amazon_qwen2_transformation.py
- `.../amazon_moonshot_transformation.py:32` — file not found at .../amazon_moonshot_transformation.py

### baseline/litellm.pre-l1-dedup.bak  — 170/174 grounded

**Unresolved**
- `.../audio_transcription/transformation.py:38` — file not found at .../audio_transcription/transformation.py
- `.../completion/transformation.py:18` — file not found at .../completion/transformation.py
- `.../image_variations/transformation.py:27` — file not found at .../image_variations/transformation.py
- `.../files/transformation.py:51` — file not found at .../files/transformation.py

### baseline/litellm.pre-l1-dedup.bak  — 272/286 grounded

**Unresolved**
- `.../amazon_ai21_transformation.py:10` — file not found at .../amazon_ai21_transformation.py
- `.../amazon_llama_transformation.py:10` — file not found at .../amazon_llama_transformation.py
- `.../amazon_mistral_transformation.py:14` — file not found at .../amazon_mistral_transformation.py
- `.../amazon_qwen3_transformation.py:22` — file not found at .../amazon_qwen3_transformation.py
- `.../amazon_titan_transformation.py:12` — file not found at .../amazon_titan_transformation.py
- `.../amazon_twelvelabs_pegasus_transformation.py:35` — file not found at .../amazon_twelvelabs_pegasus_transformation.py
- `.../gpt_oss/transformation.py:5` — file not found at .../gpt_oss/transformation.py
- `.../llama3/transformation.py:23` — file not found at .../llama3/transformation.py
- `.../anthropic_claude2_transformation.py:9` — file not found at .../anthropic_claude2_transformation.py
- `.../amazon_cohere_transformation.py:10` — file not found at .../amazon_cohere_transformation.py
- `.../amazon_moonshot_transformation.py:32` — file not found at .../amazon_moonshot_transformation.py
- `.../amazon_nova_transformation.py:22` — file not found at .../amazon_nova_transformation.py
- `.../amazon_deepseek_transformation.py:28` — file not found at .../amazon_deepseek_transformation.py
- `.../amazon_qwen2_transformation.py:24` — file not found at .../amazon_qwen2_transformation.py

### baseline/sentry  — 118/119 grounded

**Unresolved**
- `src/sentry/sentry_apps/receivers/sentry_apps.py:66` — file not found at src/sentry/sentry_apps/receivers/sentry_apps.py

### baseline/sentry  — 130/132 grounded

**Unresolved**
- `.../serializers/workflow_group_history_serializer.py:19` — file not found at .../serializers/workflow_group_history_serializer.py
- `notifications/.../base.py:56` — file not found at notifications/.../base.py

### baseline/sentry.produce-occurrence-tie.bak  — 125/126 grounded

**Unresolved**
- `spans/.../message.py:366` — file not found at spans/.../message.py

### baseline/sentry.produce-occurrence-tie.bak  — 132/134 grounded

**Unresolved**
- `spans/.../message.py:366` — file not found at spans/.../message.py
- `uptime/.../subscriptions.py:78` — file not found at uptime/.../subscriptions.py

## sense

### sense/litellm  — 219/222 grounded

**Unresolved**
- `.../gpt_oss/transformation.py:5` — file not found at .../gpt_oss/transformation.py
- `.../llama3/transformation.py:23` — file not found at .../llama3/transformation.py
- `.../anthropic_claude3_transformation.py:39` — file not found at .../anthropic_claude3_transformation.py

### sense/litellm  — 168/170 grounded

**Unresolved**
- `.../base_invoke_transformation.py:41` — file not found at .../base_invoke_transformation.py
- `.../amazon_nova_transformation.py:22` — file not found at .../amazon_nova_transformation.py

### sense/litellm.pre-l1-dedup.bak  — 227/242 grounded

**Unresolved**
- `.../gpt_oss/transformation.py:5` — file not found at .../gpt_oss/transformation.py
- `.../llama3/transformation.py:23` — file not found at .../llama3/transformation.py
- `.../base_invoke_transformation.py:41` — file not found at .../base_invoke_transformation.py
- `.../amazon_ai21_transformation.py:10` — file not found at .../amazon_ai21_transformation.py
- `.../amazon_cohere_transformation.py:10` — file not found at .../amazon_cohere_transformation.py
- `.../amazon_llama_transformation.py:10` — file not found at .../amazon_llama_transformation.py
- `.../amazon_mistral_transformation.py:14` — file not found at .../amazon_mistral_transformation.py
- `.../amazon_moonshot_transformation.py:32` — file not found at .../amazon_moonshot_transformation.py
- `.../amazon_nova_transformation.py:22` — file not found at .../amazon_nova_transformation.py
- `.../amazon_qwen3_transformation.py:22` — file not found at .../amazon_qwen3_transformation.py
- `.../amazon_titan_transformation.py:12` — file not found at .../amazon_titan_transformation.py
- `.../amazon_twelvelabs_pegasus_transformation.py:35` — file not found at .../amazon_twelvelabs_pegasus_transformation.py
- `.../anthropic_claude2_transformation.py:9` — file not found at .../anthropic_claude2_transformation.py
- `.../anthropic_claude3_transformation.py:39` — file not found at .../anthropic_claude3_transformation.py
- `.../vertex_and_google_ai_studio_gemini.py:171` — file not found at .../vertex_and_google_ai_studio_gemini.py

### sense/litellm.pre-l1-dedup.bak  — 162/165 grounded

**Unresolved**
- `.../amazon_moonshot_transformation.py:32` — file not found at .../amazon_moonshot_transformation.py
- `.../amazon_cohere_transformation.py:10` — file not found at .../amazon_cohere_transformation.py
- `.../amazon_nova_transformation.py:22` — file not found at .../amazon_nova_transformation.py

### sense/netbox  — 157/158 grounded

**Unresolved**
- `/virtualmachines.py:101` — file not found at /virtualmachines.py

### sense/saleor.8dep-drop.bak  — 171/172 grounded

**Unresolved**
- `.../product_variant_update.py:39` — file not found at .../product_variant_update.py

### sense/saleor.8dep-drop.bak  — 135/137 grounded

**Unresolved**
- `create.py:116` — file not found at create.py
- `update.py:39` — file not found at update.py

### sense/saleor.8dep-forcing.bak  — 157/161 grounded

**Unresolved**
- `.../product_variant_update.py:39` — file not found at .../product_variant_update.py
- `.../product_variant_create.py:116` — file not found at .../product_variant_create.py
- `_update.py:39` — file not found at _update.py
- `_bulk_create.py:217` — file not found at _bulk_create.py

### sense/saleor.pv6-cited075.bak  — 126/127 grounded

**Unresolved**
- `.../product_variant_update.py:39` — file not found at .../product_variant_update.py

### sense/sentry  — 245/247 grounded

**Unresolved**
- `history/postgres.py:27` — file not found at history/postgres.py
- `gsAdmin/debuggingTools.tsx:23` — file not found at gsAdmin/debuggingTools.tsx

### sense/sentry  — 194/197 grounded

**Unresolved**
- `workflow_engine/.../workflow_group_history_serializer.py:19` — file not found at workflow_engine/.../workflow_group_history_serializer.py
- `.../user_report.py:29` — file not found at .../user_report.py
- `.../issue_link_creator.py:21` — file not found at .../issue_link_creator.py

### sense/sentry.produce-occurrence-tie.bak  — 204/207 grounded

**Unresolved**
- `.../save_event_feedback.py:16` — file not found at .../save_event_feedback.py
- `.../shim_to_feedback.py:17` — file not found at .../shim_to_feedback.py
- `.../userreport.py:38` — file not found at .../userreport.py
