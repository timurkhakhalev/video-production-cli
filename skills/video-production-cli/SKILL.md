---
name: video-production-cli
description: Cut and review screencast lessons, tutorials and shorts from source videos using video-production-cli. Use for video trimming, false starts, pauses, requested time-lapses and verified final renders; not for effects, transcription or general media conversion.
---

# Video production CLI

Before editing, load the current workflow from the installed binary:

```sh
video-production-cli skills get core
```

The binary bundles its own instructions, matching the implementation version.
Read sections when their task applies:

- `skills get cutting`: choose cuts and check every splice.
- `skills get audio`: find silent boundaries, then listen to verify.
- `skills get speedups`: requested muted time-lapses.
- `skills get shorts`: keep selected excerpts from a long recording.
- `skills get review`: preview feedback, source time mapping and delivery.
- `skills get core --full`: workflow, commands and edit.json schema.

Run `video-production-cli doctor` to check dependencies, and `--help` on a command
for its exact syntax. Resume from `edit.json` and `status`.

Use the CLI for inspection and rendering. Do not improvise ffmpeg commands,
change encode.json, add effects, or mark edits approved without the user's review.
If the CLI cannot do what is needed, report the missing capability.
