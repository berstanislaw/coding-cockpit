# linear-board

A terminal kanban of your Linear tickets that runs Claude Code sessions right next to them.

Juggling a dozen tickets and a dozen Claude sessions gets messy fast. You lose track of which terminal belongs to which card, which session is waiting for you, and which one finished an hour ago. linear-board puts everything in one place: your tickets on the left, the Claude session you're looking at on the right, and a status dot on every card so you can see at a glance what's running.

```
┌ kanban ──────────────────────────────────┐┌ ● KAN-937 · adjust the sheet… ─────────┐
│ Todo         In Progress     In Review   ││                                        │
│ ╭─────────╮  ╭─────────╮ ●   ╭─────────╮ ││  Claude Code, running in your          │
│ │ KAN-12  │  │ KAN-937 │     │ KAN-850 │ ││  workspace, for the selected ticket    │
│ ╰─────────╯  ╰─────────╯     ╰─────────╯ ││                                        │
├ ticket ──────────────────────────────────┤│                                        │
│ KAN-937 · In Progress · Spreadsheet …    ││                                        │
│ ▸ ● adjust the sheet so we can start…    ││                                        │
│   ○ first look at the ticket             ││                                        │
└──────────────────────────────────────────┘└────────────────────────────────────────┘
```

It's a small Go program running inside its own tmux server, so it's fast and doesn't touch your regular tmux setup.

## What you need

- **tmux**, **Go** and **jq**. It's tested with tmux 3.7 and Go 1.27.
- **Claude Code** (`claude`), installed and logged in.
- A **Linear personal API key**: in Linear, go to Settings → Account → Security & access → Personal API keys.
- For notifications on Linux, `notify-send` plus a notification daemon such as dunst or mako, or the one your desktop already ships. On macOS they work out of the box.

On macOS:

```sh
brew install tmux go jq
```

On Arch:

```sh
sudo pacman -S tmux go jq libnotify
```

## Setup

```sh
git clone <this repo> ~/projects/linear-board
cd ~/projects/linear-board
cp .env.example .env
```

Then fill in `.env`:

| variable | what it is |
| --- | --- |
| `LINEAR_API_KEY` | your personal Linear API key |
| `WORKSPACE_DIR` | the folder where every Claude session starts, usually the repo or workspace you work in. `~/…` works. |

Then start it:

```sh
./board
```

The first run compiles the binary, which takes a few seconds. After that, `./board` rebuilds on its own whenever the code changes, so updating is just `git pull` and running `./board` again.

An alias makes life easier:

```sh
alias lb=~/projects/linear-board/board
```

Running `./board` again while it's already open re-attaches to the same board instead of starting a second one.

### Claude and Linear

Every Claude session started by the board gets your key as `$LINEAR_API_KEY`, so Claude can read tickets or move cards through the Linear API with no extra setup. Claude just needs to know it's there. A line like this in your `CLAUDE.md` does it:

> To read or move Linear tickets, call `https://api.linear.app/graphql` with `$LINEAR_API_KEY` as the `Authorization` header (no `Bearer`).

Sessions you start outside the board (your editor, a plain terminal) don't get the variable.

## How to use it

### The kanban

You see every ticket assigned to you that isn't done, canceled or a duplicate, grouped into columns by status. Tickets with a running session sit at the top of their column, then the ones with stopped sessions, then everything else. The board refreshes from Linear every 30 seconds, so moving a card in Linear shows up here on its own.

Press `enter` on a card to open it in a panel under the kanban. You get the description and comments rendered as markdown, plus that ticket's Claude sessions.

### Sessions

Press `n` on a ticket and write what you want Claude to do. Claude gets your request plus the full ticket: title, description and comments. If the ticket already has older sessions, the new one is told where their transcripts are, so it can catch up on what was done or decided. You don't need to name the session: it's named after the first line of your request.

Every session starts in `WORKSPACE_DIR`. A ticket can have as many sessions as you want, and they're remembered across restarts. A stopped session comes back with `enter`, which resumes the same Claude conversation.

Each session shows a status:

| | meaning |
| --- | --- |
| ● green | Claude is working |
| ◉ orange | Claude is waiting for you (finished, or asking for permission) |
| ◐ blue | Claude finished its turn, but subagents or background shells are still running. It wakes up again when they're done. |
| ○ grey | stopped |

Cards show the same dots, with a number when more than one session is in the same state (`●2`).

When a session finishes or needs your attention, you get a desktop notification, unless you're already looking at that session.

### Tabs

| tab | what's there |
| --- | --- |
| `1` kanban | your tickets |
| `2` sessions | Claude sessions that don't belong to any ticket, for quick questions or anything else |
| `3` reviews | nothing yet, work in progress |
| `4` daily | a short summary of what you did since the last working day |

The **daily** tab is meant for stand-ups. It reads your Claude sessions since the last working day (on Monday that means since Friday) and writes one plain bullet per thing you worked on, grouped by day. It describes the work by its subject, like "worked on the card about spreadsheet imports", and never uses ticket numbers, because nobody remembers those. The summary is written by Claude Haiku in Brazilian Portuguese and takes about a minute. It's saved per day, so opening the tab again is instant, and `r` regenerates it.

### Keys

Anywhere:

| key | does |
| --- | --- |
| `alt+1` … `alt+4` | switch tabs, also from inside a Claude session |
| `alt+←` / `alt+→` | move between the board and the Claude session |
| `q` / `ctrl+c` | quit |

On the kanban:

| key | does |
| --- | --- |
| arrows or `hjkl` | move around |
| `enter` | open the ticket |
| `n` | new session for the ticket |
| `o` | open the ticket in the browser |
| `/` | search |
| `r` | refresh from Linear |
| `alt+↓` or `tab` | go to the ticket panel |
| `esc` | close the ticket |

In the ticket panel and the sessions tab:

| key | does |
| --- | --- |
| `↑` / `↓` | pick a session |
| `enter` | show it on the right, or resume it if it's stopped |
| `n` | new session |
| `x` `x` | stop the session |
| `d` `d` | remove the session, stopping it first if it's running |
| `pgup` / `pgdn` | scroll the ticket |
| `alt+↑` or `esc` | back to the kanban |

When writing a new session, `ctrl+s` starts it and `esc` cancels.

### Quitting

`q`, `ctrl+c` or closing the terminal window ends the board **and every Claude session in it**. Nothing is lost: the sessions stay listed under their tickets, and `enter` resumes any of them.

## macOS

Everything works on macOS. The only snag is the **Option key**: by default, macOS terminals use Option to type special characters (Option+1 types `¡`), so the `alt+…` shortcuts never reach the board. Fixing it is a single setting:

| terminal | setting |
| --- | --- |
| iTerm2 | Settings → Profiles → Keys → Left Option key: `Esc+` |
| Terminal.app | Settings → Profiles → Keyboard → Use Option as Meta key |
| Ghostty | `macos-option-as-alt = true` |
| Alacritty | `option_as_alt = "Both"` under `[window]` |

If you'd rather not change it, you can still get around without Option:

- `1` … `4` switch tabs while the board has focus.
- `ctrl+b` then `1` … `4` switches tabs from anywhere, including from inside a Claude session.
- `ctrl+b` then `←` / `→` moves between the board and Claude. Clicking a pane works too.
- `tab` and `esc` move between the kanban and the ticket panel.

Notifications go through macOS itself. If they don't show up, check System Settings → Notifications and allow your terminal. On some macOS versions they're listed under Script Editor.

## How it works

If you're curious, or something looks off:

- **The tmux server.** The board runs in its own tmux server (`tmux -L linear-board`), with the settings in `board.tmux.conf`. Your own tmux is never touched.
- **Sessions are tmux windows.** Each Claude session runs in its own tmux window. The one you're looking at is moved next to the board, and the others wait in the background.
- **Status comes from Claude Code hooks.** The hooks are passed with `--settings` when a session starts. They write the status to a small file and call `notify.sh` for notifications.
- **State lives in `~/.config/linear-board/`.** That's `sessions.json` (which sessions belong to which ticket), `status/` and `daily/`. The conversations themselves are Claude's own transcripts in `~/.claude/projects/`.
- **The daily summary** runs `claude -p --model haiku` with no tools, no hooks and no saved session.

## Troubleshooting

- **Alt shortcuts don't do anything**: on macOS, see the Option key section above.
- **"Linear didn't respond"**: Linear was slow for a moment. Press `r`.
- **The layout got weird**: run `tmux -L linear-board kill-server` and start `./board` again. Your sessions are still there to resume.
- **The board doesn't start**: `./board` checks your `.env` before starting and tells you what's missing.
