# visionary

Describe scientific plots using AI vision.

`visionary` is a client–server tool: the server holds API keys and handles Gemini vision and TTS; the client (`visionary`) is a small binary that sends images and receives descriptions. Descriptions go to stdout so screen readers (VoiceOver, Orca) pick them up naturally.

## Install

Grab a binary from [Releases](https://github.com/btraven00/plsdescribe/releases), or build from source:

```
go build -o plsdescribe .
```

## Configuration

Create `~/.visionaryrc` with your server details:

```
server   = https://your-visionary-server.example.com
token    = your-bearer-token
tts_rate = 1.5   # optional, 0.25–2.0 (default 2.0)
```

These can also be passed as flags (`-server`, `-token`, `-tts-rate`) or env vars (`VISIONARY_SERVER`, `VISIONARY_TOKEN`).

## Usage

```
plsdescribe -f plot.png                 # concise one-sentence description
plsdescribe -f plot.png -v              # detailed bullet points
plsdescribe -f plot.png -i              # interactive session
plsdescribe -f plot.png -v -tts         # describe and speak aloud
```

### Interactive mode

`-i` opens a session where the image is loaded once and you can ask follow-up questions:

```
$ plsdescribe -f umap.png -i
The UMAP plot shows 21 cell-type clusters distributed across two dimensions...

> how many clusters overlap?
> which cluster is the largest?
> /tts
> /save notes.txt
> /quit
```

Commands: `/tts` (speak last response), `/save [file]` (save to file), `/quit`, `/help`.

### Flags

| Flag | Description |
|------|-------------|
| `-f` | Image file to describe (required) |
| `-v` | Verbose output (detailed bullet points) |
| `-i` | Interactive session for follow-up questions |
| `-q` | Append a question to the initial prompt |
| `-o` | Output file (default: `description.txt`) |
| `-tts` | Speak via Google Cloud TTS |

## TTS

TTS is handled server-side; no local GCP credentials needed. Audio plays through `mpv`, `ffplay`, `afplay` (macOS), or `pw-play`/`paplay` (Linux) — whichever is found first. If none are available, the MP3 is saved to `description.mp3`.

## R package

Install directly from GitHub:

```r
devtools::install_github("btraven00/plsdescribe")
```

The binary is downloaded automatically on first use (with a confirmation prompt).

```r
library(plsdescribe)

# Describe an image file
describe_image("umap.png")
describe_image("umap.png", verbose = TRUE)
describe_image("umap.png", question = "how many clusters overlap?")

# Describe a plot expression
describe_plot(plot(iris$Sepal.Length, iris$Sepal.Width))
describe_plot(
  ggplot2::qplot(mpg, wt, data = mtcars),
  question = "is there a trend?"
)

# Speak instead of printing (won't collide with screen reader)
describe_image("umap.png", tts = TRUE)
describe_plot(plot(1:10), tts = TRUE)
```

`GEMINI_API_KEY` must be set in your environment. For TTS, Google Cloud credentials are also needed.

## Screen reader compatibility

All descriptions print to stdout; UI prompts and status go to stderr. TTS only fires when explicitly requested (`-tts` flag or `/tts` command), so it never collides with system screen readers.
