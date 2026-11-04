---
name: flow-diagram
description: Update docs/flow.html, the flow diagram of oracle, and the docs that describe it (README.md, CLAUDE.md). Use when a command, package, route, data flow or invariant changes, when the diagram or its step texts look stale, or when asked to update the diagrams or the overview documentation.
---

# Flow diagram of oracle

`docs/flow.html` is a single self-contained HTML page (inline CSS and JS, no build step) with
three parts: the flow diagram with a picker per command, the "one trade, session by session"
timeline, and the "what must not break" rules. Keep it a single file; open it in a browser to read it.

The helper script runs from the repository root:

```
python3 -I .claude/skills/flow-diagram/scripts/flow.py check
```

`check` compares the page with the code and exits non-zero on any gap:
- every command in `cmd/oracle/main.go` has a picker button and an entry in `steps`, and the page has no command the binary lacks;
- every route in `internal/command/api.go` (except `/health` and `/swagger`) is mentioned in the steps of `oracle api`;
- every package in `internal/` (except `command`) appears in the page;
- every name in a `flows` list is a command;
- the script of the page is valid JavaScript (needs `node`).

The README shows the diagram as two images, `docs/flow-light.png` and `docs/flow-dark.png` (GitHub picks one by theme). `flow.py png` regenerates both from the page, with headless Google Chrome (`CHROME=/path/to/chrome` to use another binary). The page opens in a diagram-only view with `flow.html?shot&theme=light` (or `dark`), which is what `png` captures.

It does not check geometry or whether the texts are true: that is what step 5 is for.

## Workflow

1. **Find what changed.** Run `check` first, then compare the page with the code, not with memory:
   - commands: the map in `cmd/oracle/main.go` and `internal/command`;
   - packages and who calls whom: imports in `internal/*`, and the Architecture section of `CLAUDE.md`;
   - API routes: `internal/command/api.go`, `newMux`;
   - the rule and its numbers (tiers, ATR multiples, hold of 5 sessions, 3 years, volume floor, 0.6 split ratio): `internal/swing`, `internal/plan`, `internal/command/*.go` flag defaults;
   - invariants: the list in `CLAUDE.md`.
   `git log -p -- internal cmd` since the last change to `docs/flow.html` shows what moved.
2. **Edit the diagram data** in the script at the bottom of `docs/flow.html`:
   - `nodes`: `{ id, x, y, t, s, kind, flows }`. Box size is fixed (`W`=220, `H`=60); `kind` is `external`, `store` or `shared`. Columns sit at x = 90/110, 370 and 630, rows about 100 apart.
   - `edges`: `{ d, aux, flows }`, an SVG path with right angles. Run it between box borders (a box spans `x..x+220`, `y..y+60`) and avoid crossing other boxes.
   - `flows`: the commands that light the node or edge (`sync`, `recommend`, `backtest`, `api`). An empty list means it is never lit.
   - `steps`: one entry per picker button plus `all`; each item is one sentence of HTML.
   - A new command also needs a `<button class="picker">` with `data-flow`, an entry in `steps`, and its name in the `lead` paragraph and the `aria-label` of the SVG. If the new nodes do not fit in the 960×840 `viewBox`, enlarge it.
3. **Keep the texts true.** Numbers in the steps, the trade timeline and the rules must match the code. Subtitles (`s`) fit in one box line, about 30 characters.
4. **Update the docs that mention the diagram:**
   - `README.md`: the images and the sentence that introduces `docs/flow.html` at the top (and the `alt` text of the image, which describes the diagram), the "Project layout" tree and the paragraph after it;
   - `CLAUDE.md`: the Architecture section and the invariants, if the flow changed.
   Docs are in English, like the code.
5. **Regenerate the images.** Run `flow.py png` and open both PNGs: the diagram must be complete and not cropped (the window is 980×860 at 2x; if you enlarge the `viewBox`, enlarge `--window-size` in `flow.py` too). Commit them with the page, so the README never shows an old diagram.
6. **Check.** Run `check` again; it must print `check ok`. Then open the page in a browser (the `file://` URL is blocked in Claude in Chrome, so serve it: `python3 -m http.server 8765 --directory docs`, and stop the server afterwards). Click every picker button and read the result: no overlapping boxes, arrows end on box borders, the lit nodes are the ones that command uses, light and dark themes both read well.

## Rules

- No external script or image; only the Google Fonts stylesheet already there.
- Colors come from the `:root` tokens, so dark mode keeps working.
- Do not commit; leave the changes (the PNGs included) in the working tree for the author.
