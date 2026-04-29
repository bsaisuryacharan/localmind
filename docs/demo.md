# Recording the demo

How to (re-)record `scripts/demo.gif`, the gif embedded in the project README.

## 1. Why

The dataset analysis we ran on similar self-hosted projects showed a clear
correlation: repos with a working demo gif in their README accumulate
measurably more stars than equivalent repos without one. Our README used
to carry a `[Demo gif coming soon]` placeholder; this playbook is what
replaces it.

The pipeline is intentionally low-tech:

```
scripts/demo.sh  --(asciinema rec)-->  scripts/demo.cast
                                            |
                                            +--(agg)-->  scripts/demo.gif
                                                              |
                                                              +-->  README.md
```

Both the `.cast` and the `.gif` live in the repo. The cast is the source of
truth; contributors can replay or adapt it without re-running the stack.

## 2. One-time setup

Install asciinema (terminal recorder):

```bash
# macOS
brew install asciinema

# Debian / Ubuntu
sudo apt install asciinema

# anything else with pipx
pipx install asciinema
```

Install `agg` (asciinema gif generator) for the cast -> gif step:

```bash
cargo install --git https://github.com/asciinema/agg
```

`agg` requires Rust; if you don't have it, `rustup` is the easiest install.

## 3. Before recording

You want a warm but clean state:

```bash
localmind down
```

For a true cold-start recording you can also drop the named volumes so the
demo shows model pulls happening live:

```bash
docker volume rm localmind_ollama localmind_open-webui \
                 localmind_whisper-models localmind_piper-voices
```

WARNING: that re-pulls multi-gigabyte model weights the next time you run
`localmind up`. The cold path takes 3-10 minutes depending on your
connection — too long for a punchy demo. Recommend recording warm; the
gif's job is to convey the shape of the experience, not benchmark cold
boot.

Also: close noisy background processes, set your terminal to a clean
prompt (`PS1='$ '` for the recording shell helps), and pick a font size
that won't look tiny when downscaled into a 100-column gif.

## 4. Recording the cast

From the repo root:

```bash
asciinema rec scripts/demo.cast -c "bash scripts/demo.sh"
```

The `-c` flag tells asciinema to spawn `bash scripts/demo.sh` as the
recorded session instead of an interactive shell. When the script's final
`==> done.` message prints, asciinema's child process exits and the
recording stops automatically. (If you launched it interactively without
`-c`, hit Ctrl+D when finished.)

If you want to redo a take, just delete `scripts/demo.cast` and run the
command again — asciinema refuses to overwrite by default.

## 5. Converting to gif

```bash
agg --rows 28 --cols 100 --speed 1.5 scripts/demo.cast scripts/demo.gif
```

Tuning notes:

- `--speed 1.5` plays back at 1.5x. Bump to `2.0` if the demo still feels
  slow; drop to `1.0` if commands look like they flash by.
- `--cols 100 --rows 28` keeps the gif at a size GitHub renders inline
  without horizontal scroll on a typical README column. Don't go above
  `--cols 120`.
- Aim for under 5 MB. GitHub will still serve larger files, but they hurt
  page load and some clients (mobile, slow connections) won't autoplay.
- If the file is too big, drop `--rows`/`--cols` first, then bump
  `--speed`. Don't reduce frame count — `agg` already does the right thing.

Sanity-check the gif by opening it in a browser. Pay attention to:

- Is the install command readable?
- Does the wizard's hardware-detected line stay on screen long enough?
- Is there a clear "open `http://localhost:3000`" frame at the end?

## 6. Updating the README

The README currently has:

```markdown
## Demo

<!-- The gif renders here once recorded. ... -->

![](scripts/demo.gif)

If the gif above is missing, see [docs/walkthrough.md](docs/walkthrough.md)
for a textual walkthrough.
```

Once `scripts/demo.gif` is committed, the `![](scripts/demo.gif)` line
will start rendering automatically. You can leave the fallback paragraph
in place — it's harmless when the gif loads, and it's a graceful
degradation when the gif fails (raw GitHub viewer, terminal browsers,
slow connections).

The gif is committed into the repo, not hosted on a CDN. Reasoning: the
repo gets cloned, forked, mirrored, and archived; an external gif breaks
in any of those contexts. A 5 MB blob in git history is cheap.

## 7. Re-recording

Re-record the demo when:

- The wizard's `init` or `up` output changes user-visibly.
- The default chat model changes (the demo currently uses `qwen2.5:3b`,
  matching the `cpu_low` profile in `models.yml`).
- A release adds a flow worth showing — `responder install`, `tunnel
  start`, etc. Keep the demo tight; a separate gif per-feature is better
  than a 4-minute kitchen-sink reel.

Commit both `scripts/demo.cast` and `scripts/demo.gif` together so the
gif always has a reproducible source.
