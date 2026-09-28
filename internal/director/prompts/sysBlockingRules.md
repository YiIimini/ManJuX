## BLOCKING & PROP HANDLING (write these INTO each shot's description)

H3 renders every shot independently and keeps no memory of the previous one, so each shot must state, in prose:
- **Who holds what, in which hand, facing where**: e.g. "in his right hand he holds the thin stone-cutting blade, its edge turned toward the stele". A prop mentioned without a holder/hand/orientation is a prop the model re-invents.
- **Relative staging when two or more people share the frame**: who is in the foreground, who is behind whom, how far apart, which side of frame each occupies: e.g. "he stands at the centre of the lane, the old woman three paces in front of him, the boy half a step to her right rear".
- **Special effects: origin, shape, colour**: e.g. "a thin gold light leaves his knuckles in a spray and lights the right side of his face".
- On a return to the same location within the same scene, restate that staging rather than saying "as before" (there is no "before" for this generation).
- One main action per shot; the action is performed with the hands and body at ARM level, never at finger level (see THE SHOT MUST MOVE).
- **Two structured fields carry the anchors** (official shot-table-spec.md STEP 5 requires four sub-fields
  in every row; the platform had landmarks and lighting but **no field at all** for these two, so "someone
  appeared out of nowhere / vanished" had no judgement to catch it):
  · 「positions」例：{"char_id": "画面左三分之一、前景、面朝右"} — EVERY on-screen character gets an entry:
    which third of the frame, which depth layer (前景/中景/后景), and which way they face/stand at the start.
    Camera-view wording, not story wording.
  · 「offscreen」例：{"char_id": "上一镜在场、本镜已出画，走向门洞"} — a character who was on screen in the
    PREVIOUS shot but is not in this one must be listed here with where they went and why.
    Official discipline: keep that row for **at least one shot**; only drop it after two consecutive shots
    clearly place them off screen. This is what turns "the person is gone" from an audience guess into data.