The work session is over. You are now giving feedback as a user of the "sense" MCP server you just used. Answer from THIS session only. Cite specific tool calls you actually made. If you have no concrete instance for a question, return an empty list rather than inventing one.

Q1 ACCURACY. Which Sense responses directly located code you ended up citing in your answers? Which returned something wrong, empty, or that you had to double-check with grep/read?

Q2 FLOW. Where did you fall back to grep/glob/reading files after a Sense call, and what was missing from the Sense response that forced it?

Q3 HINTS. Did any hint or error text in a Sense response change your next query (different tool, added parameter, reworded search)? Cite the before/after pair.

Q4 VALUE. What single thing did Sense give you this session that you value most?

Q5 IMPROVE. What one change to Sense's responses would have made this session's work better?

Now, with your answers above as the evidence base, score Sense 0-10 on this anchored scale (pick the band your Q1-Q3 answers support, then the point within it):

- 0-2: actively misled me: I cited or pursued wrong locations because of it
- 3-4: net drag: cost me turns without changing what I found
- 5-6: interchangeable: grep/read would have gotten me the same result in about the same number of steps
- 7-8: faster or fewer file reads, but I still needed fallbacks to finish
- 9-10: gave me things I could not have recovered with grep/read within this session (transitive impact, semantic matches, dispatch edges)

Justify the band with one sentence pointing at your Q1-Q3 evidence.

Reply with ONLY a JSON object, no prose before or after, in exactly this shape ("tool" is the sense tool name, "query" is the symbol/concept string you passed it):

{
  "q1_accurate": [{"tool": "sense_graph", "query": "...", "note": "what it located"}],
  "q1_wrong": [{"tool": "sense_search", "query": "...", "note": "what was wrong/empty"}],
  "q2_fallbacks": [{"tool": "sense_blast", "query": "...", "fallback": "grep|glob|read", "missing": "what the response lacked"}],
  "q3_hints": [{"tool": "sense_graph", "query_before": "...", "query_after": "...", "hint": "the hint text that changed it"}],
  "q4_value": "...",
  "q5_improve": "...",
  "score": 0,
  "score_rationale": "..."
}
