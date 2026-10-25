---
name: speedups
description: Compress waits with whole-number muted speed-ups only when requested by the user.
---

## Speed-ups

Only when the user asks for them. Typical case: the author waits while an AI agent, a build, or a test run works, and the screen shows the progress. Cutting the wait would hide the work; a speed-up shows it compressed.

- A speed-up plays every N-th frame (`speed` is a whole number ≥ 2) and **mutes the audio** for its whole length. So it must not contain any speech the viewer needs: if the author comments during the wait, end the speed-up before the comment and start a new one after it, or ask the user.
- Its boundaries follow the join rules below: start after the last word plus its decay, end 100–150 ms before the next word. Leave the moment the result appears at 1x, so the viewer sees it.
- Pick the speed so the sped-up part lasts roughly 3–10 s and the progress is still readable: `8` for a ~1 min wait, `16` for ~2 min. Long waits above ~3 min can use a cut inside plus a speed-up on each side; ask the user first.
- Speed-ups and cuts must not overlap and a speed-up may not cross a chunk boundary; the CLI rejects both. A speed-up may touch a cut.
- Check the edges with `frames --range` and listen to them in the preview. `joins` lists every speed-up with its output time.

```json
{"from": 1702.400, "to": 1765.900, "speed": 8, "reason": "ожидание агента: генерирует тесты"}
```
