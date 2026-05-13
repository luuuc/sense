"""Combined fairness formula (post-20-05).

fairness = 0.10 * keyword_coverage          (smoke-test keywords)
         + 0.55 * llm_quality               (judge-rated answer quality, headline)
         + 0.15 * citation_grounding_rate   (file:line citations that resolved)
         + 0.20 * efficiency                (half tokens, half time)

Scorer (score.sh) writes the keyword_coverage / citation_grounding /
efficiency components into scored.json. Judge (judge.sh) writes
scenario_quality into judged.json as llm_quality. The reporter combines
both files via this module — neither scored.json nor judged.json carries
the combined `fairness_score` on its own.

If judged.json is missing, the fairness score is reported as None
(rendered as `—` in tables). Run judge.sh to fill in llm_quality.
"""

WEIGHTS = {
    "keyword_coverage": 0.10,
    "llm_quality": 0.55,
    "citation_grounding": 0.15,
    "efficiency": 0.20,
}


def _safe(value, default=0.0):
    return float(value) if value is not None else default


def extract_components(scored, judged=None):
    """Return the four fairness components from scored.json + judged.json.

    citation_grounding is expressed as a 0–1 rate; if the answer had no
    structured citations to check (total == 0), the component is 0.0 —
    "no map" is not credit-worthy for the AI-agent audience this bench
    scores for.
    """
    cg = scored.get("citation_grounding") or {}
    components = {
        "keyword_coverage": _safe(scored.get("keyword_coverage")),
        "efficiency": _safe(scored.get("efficiency")),
        "citation_grounding": _safe(cg.get("rate")),
        "llm_quality": _safe(judged.get("scenario_quality")) if judged else None,
    }
    return components


def compute(scored, judged=None):
    """Compute the combined fairness score.

    Returns {"score": float|None, "components": {...}, "complete": bool}.
    Failed runs (scored["failed"] truthy) return 0.0 directly — there is
    no answer to score and judge.py short-circuits to the same.
    """
    if scored.get("failed"):
        return {
            "score": 0.0,
            "components": {
                "keyword_coverage": 0.0,
                "efficiency": 0.0,
                "citation_grounding": 0.0,
                "llm_quality": 0.0,
            },
            "complete": True,
        }

    components = extract_components(scored, judged)
    if components["llm_quality"] is None:
        return {"score": None, "components": components, "complete": False}

    total = sum(WEIGHTS[k] * components[k] for k in WEIGHTS)
    return {"score": round(total, 4), "components": components, "complete": True}
