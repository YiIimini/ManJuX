## SHOT TABLE (the official six columns — every shot must fill them)

The per-shot JSON below IS the official standardized shot table. Each field maps to one of its six columns, and the official self-check has to pass before anything is rendered:
  · Shot ID & Duration      → id + duration
  · Continuity Handoff      → end_state (see SHOT END STATE & CROSS-SHOT CONTINUITY)
  · Reference Anchors       → scene (its landmarks + light_state) + characters (approved card names) + the reference pictures attached to this shot
  · Hook Type               → hook_type: ONE label, from this controlled vocabulary ONLY: setup / visual-joke / reversal / reveal / callback / suspense / tender / chase / expression-beat / climax
  · Shot Description        → action + h3_prompt (keep the detail inside the shot paragraph, second by second where the beat needs it)
  · Audio & Dialogue Track  → dialogue / narration, and the <d> blocks inside h3_prompt

Self-check (official STEP 5.5 — the three checks a table can fail on):
  1. HOOK DENSITY: every shot carries a hook_type; every THREE consecutive shots include at least one reveal / reversal / callback; the opening shot and the closing shot each carry a strong hook (visual-joke / reversal / reveal / suspense / tender). A stretch of shots with no strong hook reads as flat, no matter how good the pictures are.
  2. CAST PER SHOT: no shot has more than THREE important characters (characters with on-screen action or dialogue). More than that dilutes attention — everyone renders worse and faces start swapping. Split the beat, or leave the extras as environment instead of cast.
  3. SPATIAL ANCHORS: every shot inherits its scene card's landmarks and its light state. If a shot deliberately moves a landmark on screen or changes the light, say so inside that shot's description (e.g. "the door frame moves from the right third to centre as the camera arcs left").