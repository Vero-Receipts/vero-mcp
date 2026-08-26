-- Two columns supporting LLM-vetted merchant matching.
--
-- llm_confidence records how sure the model was, not just which way it answered.
-- The decision now turns on that number — it separates an automatic link from a
-- suggestion from a discard — so without it the bands cannot be tuned against
-- what actually happened in production.
--
-- match_attempted_at is when the pipeline last reached a verdict for a receipt.
-- The sweep re-runs the entire unmatched backlog whenever any single receipt or
-- transaction arrives, so a receipt whose own content has not changed, and whose
-- candidate window holds nothing newly synced, would otherwise be re-decided —
-- and re-charged to the LLM — on every sweep for an answer that cannot differ.

ALTER TABLE match_audit_log ADD COLUMN llm_confidence REAL;

ALTER TABLE receipts ADD COLUMN match_attempted_at TEXT;
