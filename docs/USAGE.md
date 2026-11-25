# Converting clips

How to turn ProRes RAW clips into CinemaDNG with the **ProRes Raw to DNG Converter** app
and open them in DaVinci Resolve. Not installed yet? See
[INSTALLATION.md](INSTALLATION.md).

## The window

```
┌──────────────────────────────────────────────────────────┐
│ ffmpeg: …/ffmpeg  (8.0)                                  │  ← ready
│ [Add clips…] [Add folder…] [Clear]                       │
│ ┌──────────────────────────────────────────────────────┐ │
│ │ A001_C003.mov                              12 / 240  │ │  ← clip list
│ │ ████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░  │ │
│ │ A001_C004.mov                                queued  │ │
│ └──────────────────────────────────────────────────────┘ │
│ Output: [~/Movies/CinemaDNG                  ] [Choose…] │
│ [✓] Extract audio (WAV)  Compression: [JPEG 12-bit ▾]    │
│                                       [Cancel] [Convert] │
│ ██████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░  │  ← all clips
│ log…                                                     │
│ ──────────────────────────────────────────────────────── │
│         Free and open source. … buy me a coffee          │  ← support link
└──────────────────────────────────────────────────────────┘
```

## Step by step

1. **Add clips.** Drag `.mov` files or whole folders onto the window, or use
   **Add clips…** / **Add folder…**. Folders are searched with all their
   subfolders, so you can drop a whole card. A clip added twice appears once.
   **Clear** empties the list.
2. **Choose the output folder.** Type it or press **Choose…**. The default is
   `Movies/CinemaDNG` in your home folder; it is created if it does not exist.
3. **Choose the compression.** `JPEG 12-bit (visually lossless)` is the
   default and opens everywhere. For much smaller files that only DaVinci
   Resolve opens, pick a `Resolve only: DCT …` mode. What each mode means:
   [COMPRESSION.md](COMPRESSION.md).
4. **Audio.** With **Extract audio (WAV)** on, the clip's sound is saved next
   to the frames.
5. Press **Convert**. Each clip shows its frame count and a bar; the bottom
   bar is the whole list. **Cancel** stops the conversion; frames already
   written stay on disk.

The output folder and the compression are remembered for next time.

## What you get

For a clip `A001_C003.mov` converted with `JPEG 12-bit`:

```
Movies/CinemaDNG/
└── A001_C003_12bit/
    ├── A001_C003_12bit_000000.dng
    ├── A001_C003_12bit_000001.dng
    ├── …
    ├── A001_C003_12bit.wav
    └── metadata.json
```

The folder name ends with the compression (`_12bit`, `_3to1`,
`_resolve-8to1`, …), so converting the same clip with another mode makes a
second folder next to the first instead of overwriting it. Converting again
with the same mode writes over that folder's frames.

`metadata.json` keeps everything the source clip said about itself: camera,
recorder, lens settings, dates. You do not need it to edit.

## Converting again

Press **Convert** again at any time: the whole list is converted again with
the current settings, finished clips included. To compare modes, convert
once, change the compression, and convert again.

## Opening in DaVinci Resolve

1. In the **Media** page, browse to the output folder and open the clip's
   folder: Resolve shows the frames as one clip.
2. Add it to the media pool. Timecode, frame rate and audio come along.
3. Leave the clip's Camera Raw settings **As shot**: white balance, colour and
   exposure then match the ProRes RAW source.

If Resolve still shows old frames after converting again into the same
folder, remove the clip from the media pool and import it again.

## The log

The box at the bottom says what happens to each clip: camera, ISO and shutter,
the compression reached, and the result. Things to look for:

| Message | Meaning |
| --- | --- |
| `⚠ ffmpeg with ProRes RAW support (8.0+) not found` at the top | The app cannot find its ffmpeg: keep the app's files together, or reinstall. **Convert** stays off until it is found. |
| `warning: 8:1 would posterise this clip; stopping at its noise floor, about 5.2:1` | A `3:1`/`5:1`/`8:1` mode stopped short of its ratio to avoid banding on clean footage. Use a `Resolve only` mode for higher ratios. |
| `ERROR <clip>: …` | The clip failed; the rest of the list still converts. Often: not ProRes RAW, or the disk is full. |
| `skip <path>: …` | A dropped file or folder could not be read. |

## Tips

* Converting is heavy: about 3 frames per second for 5.7K on a laptop. Try a
  short clip first when you change settings.
* `resolve-*` frames look black in Finder, Preview and photo apps. That is
  expected; they are for Resolve only.
* Finder and Preview ignore the DNG exposure, so frames can look darker there
  than in Resolve. Judge exposure in Resolve.
