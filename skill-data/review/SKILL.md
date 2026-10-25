---
name: review
description: Hand over previews, translate feedback to source times, preserve lessons, and verify the approved final.
---

# Review and delivery

In step mode render `preview --chunk N`, then `chunks set N in_review`. In full mode use
`preview` and `chunks set all in_review`. Share a link to the preview, the joins table with
preview times/reasons, speed-up factors and times, and unresolved `review_flags`. Flags are
only places left unresolved, e.g. a cut not made because a clean join is impossible; explain
them instead of hiding them. Do not put notes about successful joins there.

The user's timestamps are preview times. Convert with `map 1:28.5 --chunk N` for a chunk,
or `map 1:28.5` for a full preview/final. This handles cuts and muted speed-ups. Do not add
an offset yourself. Change cuts in source seconds, check affected joins, rebuild the preview.
Store general preferences in `lessons`, recheck similar places in the current chunk and apply
them to later chunks. One local correction does not automatically create a general rule.

Only user approval authorizes `chunks set N approved` or `chunks set all approved`.
Run `final` after approval. It renders from the original once; never concatenate chunk previews.
`verify --final` reruns checks; `verify FILE --chunk N` checks an existing chunk preview.
The verification covers durations, frame count and A/V alignment; final also compares source
stream parameters and sampled 1x picture PSNR. It does not prove editorial or audio quality.
Report failed checks without running an improvised ffmpeg repair.

Delivery includes the final file, source and output length, verification result, unresolved flags
and any limits of inspection. Do not call the job complete while a required check is skipped.
