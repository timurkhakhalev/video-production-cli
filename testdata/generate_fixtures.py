#!/usr/bin/env python3
"""Generate invented lesson/short fixtures; no source recordings are used."""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent / "fixtures"


def write(name, duration, boundaries, fps="30/1", mono=False, speedups=False, parts=False):
    enc = {
        "video": {"codec": "h264", "profile": "High", "level": 40, "pix_fmt": "yuv420p",
                  "width": 1280, "height": 720, "fps": fps, "time_base": "1/30000",
                  "color_range": "tv", "color_space": "bt709", "color_primaries": "bt709",
                  "color_transfer": "bt709", "source_bit_rate": 2000000},
        "audio": {"codec": "aac", "profile": "LC", "sample_rate": 48000,
                  "channels": 1 if mono else 2, "channel_layout": "mono" if mono else "stereo",
                  "source_bit_rate": 128000, "bit_rate": 192000},
        "duration": float(duration),
    }
    doc = {"source": f"/media/synthetic/{name}.mp4", "mode": "step", "markers": ["Delete"],
           "chunks": [{"id": i + 1, "start": float(a), "end": float(b), "status": "draft"}
                      for i, (a, b) in enumerate(zip(boundaries, boundaries[1:]))],
           "cuts": [], "speedups": [],
           "review_flags": [{"at": 20.0, "note": "Invented example: leave this pause for review."}],
           "lessons": ["Invented preference: preserve pauses during screen actions."]}
    for i, (a, b) in enumerate(zip(boundaries, boundaries[1:])):
        for j in range(4):
            start = a + 5 + j * (b - a) / 6
            doc["cuts"].append({"from": round(start, 3), "to": round(start + 1.2 + j / 10, 3),
                                "reason": f"Synthetic restart {i + 1}.{j + 1}"})
        if speedups:
            doc["speedups"].append({"from": a + (b-a)*0.8, "to": a + (b-a)*0.9,
                                    "speed": [2, 4, 8, 16][i % 4], "reason": "Synthetic wait"})
    if parts:
        doc["parts"] = [{"path": f"/media/synthetic/part-{i + 1}.mp4", "start": duration*i/2,
                         "duration": duration/2} for i in range(2)]
    if name == "syn-short-multirange":
        doc["mode"] = "full"
        for chunk in doc["chunks"]:
            chunk["status"] = "approved"
        doc["cuts"] = [{"from": a, "to": b, "reason": "Synthetic removal"} for a, b in
                       [(0, 100), (110, 112), (140, 300), (300, 600), (600, 650), (670, 900)]]
    target = ROOT / name
    target.mkdir(exist_ok=True)
    for filename, value in [("edit.json", doc), ("encode.json", enc)]:
        (target / filename).write_text(json.dumps(value, indent=2) + "\n")


if __name__ == "__main__":
    write("syn-lesson-stereo", 720, [0, 240, 480, 720])
    write("syn-lesson-mono", 1080, [0, 540, 1080], fps="25/1", mono=True)
    write("syn-lesson-long", 1800, [0, 450, 900, 1350, 1800])
    write("syn-multipart", 1500, [0, 375, 750, 1125, 1500], speedups=True, parts=True)
    write("syn-short-single", 900, [0, 300, 600, 900])
    write("syn-short-speedups", 900, [0, 300, 600, 900], speedups=True)
    write("syn-short-multirange", 900, [0, 300, 600, 900])
    write("syn-long-60fps", 3600, list(range(0, 3601, 300)), fps="60/1", speedups=True)
