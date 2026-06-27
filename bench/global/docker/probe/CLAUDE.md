Use the available MCP tools for codebase understanding when they would help answer the question.
Do not spawn Explore agents or sub-agents.

Probe provides MCP tools for codebase navigation (stateless — no index, every call
parses on demand via tree-sitter + ripgrep):
- search_code: Elasticsearch-style boolean queries over the codebase, returns
  ranked code blocks with file/line spans
- extract_code: extract a function, class, or symbol body by file:line or by name
- query_code: structural AST pattern matching (find code shapes like async
  functions, specific call signatures, decorator usage)
- symbols_code: list all symbols in a file (functions, classes, structs, constants)
  with line numbers and nesting — fastest way to outline an unfamiliar file
