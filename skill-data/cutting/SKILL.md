---
name: cutting
description: Choose cuts in speech and screen recordings, and verify every splice without clipped words or visible head jumps.
---

## What to cut

1. **Spoken delete markers.** Many authors mark a bad take by saying a fixed word, such as «Удалить» or «Стоп». The markers in use are listed in `edit.json` → `markers` (default `["Удалить"]`); ask the user if unsure. Remove the bad take and the marker word itself. The bad take starts where the repeated thought first began, not just the last few words.
2. **Restarts and reformulations.** The author starts a phrase, stops, and says it again, often with different words («Это влияет на… это определяет скорость сборки»; «Сначала мы откр— / Сначала мы открываем настройки»). Keep only the **last complete take**. Restarts are often separated by a tiny pause or none at all. Listen for them; don't assume a fluent-sounding stretch is clean.
3. **Abandoned phrases**: a started thought that is dropped and never finished («а вот здесь, если, ну…», «и тогда получается, в общем»).
4. **Filler sounds** (э-э, мм, эм) **when they stand alone in a pause**. If a filler is glued to a word, leave it.
5. **Coughs, throat clearing, sips of water, adjusting glasses, looking at the phone.** Remove the whole action, from the moment the hand or head starts moving until the author is back in a still, neutral pose.
6. **Dead air**: the silence and preparation before the first word (including clap markers) and after the last word. Inside the lesson, only pauses **longer than ~3 s with nothing happening on screen**. Shorten these to ~0.7 s; don't remove them entirely.

## What not to cut

- **Normal pauses and breaths.** Natural rhythm matters more than tightness. Compressing every pause longer than 1 s makes the lesson feel choppy.
- **Benign fillers inside fluent speech.** Small hesitations like «вот», «ну», «как бы» stay unless they form a restart.
- **Silent screen action.** Clicks, page loads, typing, scrolling, switching tabs: the viewer needs time to see it. Silence during a demo is content, not dead air.
- **A breath, split in half.** A breath is either kept whole or removed whole.

## How to cut: the join rules

Every join must pass all four rules. They are listed in order of priority.

1. **Audio: cut only in silence.**
   - Leave 100–150 ms before the first sound of the next phrase; plosives (П Б Т Д К Г) lose their attack otherwise («…роблема» instead of «проблема»).
   - Leave 80–120 ms after the last sound of the previous phrase for the decay.
   - Never cut inside a word.
   - Find the boundary on the audio track first, then listen. Run `audio FROM TO --at T` over the area and pick the nearest pause; check level/peaks if needed. Listening is for verifying the finished join (`listen --join K`), not for locating it. Avoid hunting the cut point by slicing 0.5–1 s clips and listening to each one: every listen is a full model turn, ~10 turns per join, and each turn gets slower as the context grows.
2. **Head: a still pose on both sides.** The last kept frame before the join and the first kept frame after it must show the author settled: not turning, nodding, looking down at notes, or glancing at a second monitor, and in about the same position. If the author turns away during the bad take, the cut must span the whole turn, from before it starts until after they are back.
3. **Where to hide the cut.** Inside the pause, move the cut to the frame where:
   - a slide changes, code scrolls, or the page loads (attention jumps to the screen);
   - or, if nothing changes on screen, the two head poses match best.

   When a cut sits on a slide change, the last kept frame must still show the old slide and the first kept frame must already show the new one. A single frame of the wrong slide on either side flashes on screen. The same goes for any kept fragment shorter than ~0.3 s; `joins` warns about those.
4. **Demo continuity.** During a demo, the action the speech refers to (the click, the opened page) must stay visible after the cut. Never cut out the moment that explains what happened.

**Verify every join frame by frame.** Native video understanding samples about one frame per second. That is enough to find a mistake, but not to judge a head pose at the cut point. For every join run `video-production-cli frames --join K` and look at the sheet (15 frames on each side) and the full-resolution pair (last frame before | first frame after). If the pose jumps, slide the cut points within the pause (`--range FROM TO` shows any span frame by frame) and check again.

### When a clean cut is impossible

If no position inside the pause passes rules 1–2:

1. try widening the cut to the previous or next settled pause (removing a bit more is fine if no content is lost);
2. otherwise **don't cut**: remove the cut from `cuts` and add a `review_flags` entry with the source time and what is wrong («перезаход, но голова повёрнута на обеих сторонах паузы»). The user decides.

Never fall back to a dissolve, zoom, or audio-only fix.
