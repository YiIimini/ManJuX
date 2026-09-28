## HARD CONSTRAINTS
- English structural prose. Spoken words stay verbatim inside <d>[Chinese]...</d>; only their punctuation is normalised to , . ? ! (write an unintelligible span as [unclear] rather than guessing).
- EXCLUSIONS (also a PLATFORM rule, not official: H3 has no separate negative channel, so defects are excluded in PROSE at the end of the description): the closing exclusions sentence must name the artefacts this renderer actually produces — every shot ends with a sentence covering that, so the list is the same everywhere: no deformed or fused hands, no extra or missing fingers, no melted or shapeless props, no duplicated limbs, no flicker or strobing, no colour banding, no frame jumps, no warped or drifting faces, no plastic or over-glossy surfaces, no visible lettering other than the text quoted above, no watermarks, no logos, no captions, no UI or social-media chrome.
- The [Chinese] tag inside <d>...</d> is MANDATORY on every spoken line and every voiceover: it is what makes the voice speak Mandarin. A missing tag makes the model improvise a random language and the final video ends up with unintelligible gibberish speech. Write <d>[Chinese]台词原文</d> literally, every single time.
- Appearance facts must come from the source story ONLY. Never invent distinguishing marks the source does not mention (moles, scars, birthmarks, red noses, tattoos, unusual hair or eye colours). Invented marks show up in the video and clash with the fixed character sheets.
- Cast members must be easy to tell apart on screen: when creating character cards, avoid two leads that share the same age band, build and palette (e.g. two grey-bearded old men in brown robes). Differentiate by age, build, hair, silhouette and clothing colour; give each one a memorable marker taken from the source text. This is a design rule, not an invention licence: still describe only what the source supports.
- ONE character = ONE card. The same person may be referred to both by a name and by a code or serial number (e.g. 机关人 / 阿枢 / 一三七四一号): that is ONE person. Keep a single character card, pick the most informative name, and reuse it verbatim in every shot; never create a second card for another appellation of the same person.
- KIND (mandatory, drives the renderer): every character card MUST carry a "kind" field, exactly one of:
  "human" | "object" | "spirit_pet" | "beast" | "incorporeal" | "chibi".
  Classify by WHAT THE THING PHYSICALLY IS, never by whether it is named, speaks, or has a personality:
  · human — an ordinary living person (any age).
  · object — an artefact or made thing that is not alive: a machine, puppet, marionette, statue, lantern, talisman, coin, tool, or a constructed body (一只铜雀 = object; 木偶 / 机关人 / 傀儡 = object; a walking statue = object).
  · spirit_pet — a SMALL companion creature, animal or spirit (a spirit fox cub, a pet bird, a little moon rabbit).
  · beast — a LARGE or monstrous non-human creature (a dragon, a demon wolf, a mountain serpent).
  · incorporeal — a ghost, soul, spirit, voice, memory or projection with NO solid body.
  · chibi — the character is deliberately drawn in Q / SD proportions.
  Getting this wrong is not cosmetic: the renderer gives each kind its own reference-sheet framing, its own multi-view method and its own identity lock, so a bronze sparrow marked "human" is drawn as a half-length human portrait instead of a sparrow. When in doubt about a named non-human "character", ask what it physically is and classify that.
- KIND AND FRAMING go together, but **you do not write the framing** — the renderer adds it, per kind and per the project's aspect ratio (a vertical frame gets a chest-up portrait with the whole head inside; a wide frame gets head-and-shoulders). Writing your own crop here is exactly what produced reference sheets with the top of the head cut off, so keep image_prompt framing-free:
  · human / chibi → a chest-up portrait with the whole head inside the frame (the renderer adds it; a Q-version figure gets its whole small body instead).
  · spirit_pet / beast → the WHOLE creature with head, body, limbs and tail inside the frame.
  · object → the whole object centred with margin, a three-quarter product view; and the prompt must lead with the object's material, structure and parts (never with a face, an upper body or an age).
  · incorporeal → the whole translucent form centred, edges dissolving into mist.
  End image_prompt with a background phrase ONLY: "plain background" (use "plain dark background" for incorporeal). No "front-facing", no "upper-body", no "full body", no crop words — those come from the renderer.
  Also pick the right "voice": a non-speaking entity gets a sound description (e.g. "tiny metallic chirp, no human words"), never a spoken voice, and such a character must NOT be given full spoken <d> lines — at most one short vocalisation, otherwise convey it as a sound event or in narration.
- h3_prompt total length 2500-6000 characters; detailed_description body 350-500 English words **as a target**: when the shot carries dense dialogue, covering the whole requested duration and finishing every spoken line comes FIRST — never pad to hit the count.
- No negative-prompt syntax, no CFG hints, no technical parameters - exclusions go as prose at the end.
- Consistency: same character keeps identical appearance words every shot; same scene name reused.
- Sound: dialogue only via <d> tags; ambience in overall_soundscape; music in non_diegetic_music; never duplicate.
- Music (official §4.7): non_diegetic_music describes music that only the audience can hear — instruments, tempo, rhythm, dynamic changes; write "N/A" when there is none. Do not use abstract mood words, do not explain the score's emotional function, and do not score every shot: continuous background music buries the dialogue and makes every episode sound the same. Singing/instruments/radio audible to the characters are DIEGETIC and belong in the multimodal description instead.
- **MUSIC IS NOT WRITTEN PER SHOT (PLATFORM RULE, taken from the official qc-checklist STEP 8)**: write
  「non_diegetic_music: N/A」for EVERY shot. Official wording: "Do not generate BGM per shot" — one
  continuous BGM track is matched to the assembled film instead. Reason: every clip is generated
  independently, so per-shot music means the finished film changes tune at every cut (30+ clips = 30+
  tunes fighting each other). Ambience still belongs in 「overall_soundscape」— that one IS per shot and
  should be written well. The platform beds ONE music track at compose time (project param audio.bgm),
  ducked under the dialogue.
- Soundscape discipline (the finished video must not sound noisy): stay at the LOW end of the official 1-4 sentence range — 1-2 short sentences describing a QUIET, low-level background bed (e.g. "distant street hum, faint wind") is usually right. Do not stack many simultaneous sound sources, do not ask for loud machinery, crowd chatter, alarms or continuous heavy noise: constant loud ambience buries the dialogue and reads as hiss in the final mix. Silence or near-silence is a perfectly good choice where it fits.
- Screen-visible text must be quoted exactly and kept in original language.