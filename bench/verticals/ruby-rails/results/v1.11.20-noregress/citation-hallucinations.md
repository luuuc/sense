# Citation hallucinations

Citations the answer printed that did not resolve against the repo checked out at the benchmarked commit. **Hallucinated** = line number beyond end-of-file (a made-up number). **Unresolved** = file not in the repo, or symbol not within ±5 lines of the cited line.

Reported for transparency; not folded into the headline score.

## sense

### sense/llm.rb  — 65/67 grounded

**Unresolved**
- `.../response_adapter/completion.rb:138` — file not found at .../response_adapter/completion.rb
- `anthropic/.../completion.rb:123` — file not found at anthropic/.../completion.rb

### sense/ruby_llm  — 57/58 grounded

**Unresolved**
- `chat.rb:format_role` — ambiguous: 14 files match `chat.rb` (spec/dummy/app/models/chat.rb, lib/ruby_llm/chat.rb, lib/ruby_llm/providers/openrouter/chat.rb...)

### sense/ruby_llm  — 100/104 grounded

**Unresolved**
- `chat.rb:completion_url` — ambiguous: 14 files match `chat.rb` (spec/dummy/app/models/chat.rb, lib/ruby_llm/chat.rb, lib/ruby_llm/providers/openrouter/chat.rb...)
- `models.rb:models_url` — ambiguous: 13 files match `models.rb` (lib/ruby_llm/models.rb, lib/ruby_llm/providers/openrouter/models.rb, lib/ruby_llm/providers/azure/models.rb...)
- `streaming.rb:stream_url` — ambiguous: 7 files match `streaming.rb` (lib/ruby_llm/streaming.rb, lib/ruby_llm/providers/openrouter/streaming.rb, lib/ruby_llm/protocols/gemini/streaming.rb...)
- `media.rb:format_content` — ambiguous: 9 files match `media.rb` (lib/ruby_llm/providers/azure/media.rb, lib/ruby_llm/providers/perplexity/media.rb, lib/ruby_llm/providers/ollama/media.rb...)
