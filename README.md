# KTA - Kanban Terminal for Agents

A terminal kanban of your Linear or Jira tickets that runs Claude Code sessions right next to them.

Juggling a dozen tickets and a dozen Claude sessions gets messy fast. You lose track of which terminal belongs to which card, which session is waiting for you, and which one finished an hour ago. kta puts everything in one place: your tickets on the left, the Claude session you're looking at on the right, and a status dot on every card so you can see at a glance what's running.

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

- **tmux** and **Go**. It's tested with tmux 3.7 and Go 1.27.
- **Claude Code** (`claude`), installed and logged in.
- A **Linear personal API key** (Settings → Account → Security & access → Personal API keys), or a **Jira Cloud** account with an API token (id.atlassian.com → Security → Create and manage API tokens).
- For the reviews tab, the **GitHub CLI** (`gh`), logged in with `gh auth login`.
- For notifications on Linux, `notify-send` plus a notification daemon such as dunst or mako, or the one your desktop already ships. On macOS they work out of the box.
- **Linux or macOS.** On Windows it runs inside WSL, since tmux doesn't run on Windows itself. Install everything inside WSL and start `./kta` from a WSL terminal. Desktop notifications don't reach Windows from there.

On macOS:

```sh
brew install tmux go
```

On Arch:

```sh
sudo pacman -S tmux go libnotify
```

## Setup

```sh
git clone <this repo> ~/projects/kta
~/projects/kta/kta
```

The first run opens a short setup on screen:

1. **Where your tickets live**: Linear or Jira (Cloud).
2. **Your account.** For Linear, a personal API key (Settings → Account → Security & access → Personal API keys). For Jira, your site (`acme.atlassian.net`, or just `acme`), the email you sign in with, and an API token from id.atlassian.com → Security → Create and manage API tokens. `enter` checks it right away and shows whose account it is.
3. **The folder where Claude sessions start**, usually the repo or workspace you work in. `~/…` works.

It's saved to `~/.config/kta/env`, readable only by you. To change it later, run `./kta setup` (secrets left empty keep the current ones), or edit the file by hand:

```sh
TRACKER='linear'
LINEAR_API_KEY='lin_api_…'
WORKSPACE_DIR='~/projects/my-workspace'
```

or, for Jira:

```sh
TRACKER='jira'
JIRA_URL='https://acme.atlassian.net'
JIRA_EMAIL='you@acme.com'
JIRA_API_TOKEN='…'
WORKSPACE_DIR='~/projects/my-workspace'
```

The first run compiles the binary, which takes a few seconds. After that, `./kta` rebuilds on its own whenever the code changes, so updating is just `git pull` and running `./kta` again.

An alias makes life easier:

```sh
alias kta=~/projects/kta/kta
```

Running `./kta` again while it's already open re-attaches to the same board instead of starting a second one.

### Workspaces

One workspace is one tracker account plus the folder where Claude starts. To keep a second one, say a Jira project next to your Linear one, press `alt+w` anywhere (also from inside a Claude session) to open the workspace picker:

- type to filter, `↑`/`↓` (or `ctrl+n`/`ctrl+p`) to move, `enter` to open, `esc` to close
- typing a name that doesn't exist offers to create it, and opening it runs the same setup as the first run. `ctrl+c` there goes back to where you were

Only one board runs at a time: opening another workspace **stops every Claude session of the current one** (the picker warns how many are running) and reopens the board on the other. Nothing is lost, the sessions stay listed and resume with `enter` when you come back.

From the terminal, `./kta jira` opens that workspace directly, `./kta default` goes back to the default one, and plain `./kta` re-attaches to whatever is open. The default workspace lives in `~/.config/kta/`, the others in `~/.config/kta/workspaces/<name>/`, each with its own key, folder, sessions, groups and settings. `./kta jira setup` changes a workspace's key or folder.

### Claude and your tracker

Every Claude session started by the board gets the workspace's credentials as environment variables (`$LINEAR_API_KEY`, or `$JIRA_URL`, `$JIRA_EMAIL` and `$JIRA_API_TOKEN`), and kta tells it how to use them: Linear's GraphQL API, or Jira's REST API with transitions to move a card. So "move this ticket to In Review" works with no extra setup.

Sessions you start outside the board (your editor, a plain terminal) don't get the variables.

## How to use it

### The kanban

You see every ticket assigned to you, in every status, with one column per status of your tickets' teams (or Jira projects), even the empty ones, from triage and backlog to done and canceled. In settings you can also bring in the tickets you created or subscribed to, and hide the columns you don't care about, like done and canceled. Tickets with a running session sit at the top of their column, then the ones with stopped sessions, then everything else. The board refreshes from your tracker every 30 seconds, so moving a card there shows up here on its own.

Press `enter` on a card to open it in a panel under the kanban. You get the description and comments rendered as markdown, plus that ticket's Claude sessions.

### Sessions

Press `n` on a ticket and write what you want Claude to do. Claude gets your request plus the full ticket: title, description and comments. If the ticket already has older sessions, the new one is told where their transcripts are, so it can catch up on what was done or decided. You don't need to name the session: it's named after the first line of your request.

Every session starts in `WORKSPACE_DIR`. A ticket can have as many sessions as you want, and they're remembered across restarts. A stopped session comes back with `enter`, which resumes the same Claude conversation.

Once a ticket is finished, meaning any completed or canceled status (in production, done, canceled, duplicate), the board forgets its sessions and takes it out of its group. The ticket itself stays in its column, unless you hide that column in settings. A session that's still running is kept until it stops. Claude's own transcripts aren't touched, so the daily summary still counts that work.

Each session shows a status:

| | meaning |
| --- | --- |
| ● green | Claude is working |
| ◉ orange | Claude is waiting for you (finished, or asking for permission) |
| ◐ blue | Claude finished its turn, but subagents or background shells are still running. It wakes up again when they're done. |
| ○ grey | stopped |

Cards show the same dots, with a number when more than one session is in the same state (`●2`).

### Several tickets at once

When a few tickets are really one piece of work, mark them with `g` (they get a double purple border and a `✓`), then press `n`. That starts one session with all of them in the prompt, told to handle them together. The session belongs to every ticket in the group. Each group gets its own color, picked at random among the ones farthest from the colors already in use. Cards of a group that share a column sit together, joined by a bar in that color on their left; each card also shows a colored `◆`, the ticket panel says who else is in it (`◆ with KAN-1118 KAN-1119`), and when the cursor is on one of them, the others light up. `esc` clears the marks. `z` folds a group into a single card (`◆ 3 cards`) and `z` again unfolds it. When the tickets don't fit, their numbers scroll by, and the line below cycles through each ticket's title every few seconds. `Z` folds or unfolds every group at once, and the board remembers what's folded. On a folded card, `n` starts a session for the whole group and `p` opens the PRs of all of its tickets.

A group is its own thing, with an id, saved in `~/.config/kta/groups.json`, and a ticket belongs to one group at most. To change who's in it, just ask the session, like "add KAN-1120 to this", "bring KAN-1117 over here" or "take KAN-1117 out". Every session started by kta knows its own id (`$KTA_SESSION`) and is told about the commands:

| command | does |
| --- | --- |
| `kta group` | lists the tickets of this session's group |
| `kta group add KAN-123 …` | adds tickets, moving them out of any other group, and prints them in full so Claude reads them right away. On a session of a single ticket, it turns it into a group. |
| `kta group remove KAN-123 …` | takes tickets out. A group keeps at least one. |
| `kta groups` | lists every group with its id and sessions |

Add `--group <id>` to change a group other than the session's own. The board picks changes up within a couple of seconds. A finished ticket leaves its group on its own, and removing a group's last session (`d d`) ends the group.

### PRs on cards

Cards show the pull requests that belong to them, in purple, tagged by repo: `A#1782` for the API, `W#2210` for the web. The letter is the first one of the repo name's last word, so other repos get their own letter (`D#347` for docs).

The PRs come from Linear: its GitHub integration attaches them to the ticket, and the board reads them every time it refreshes. There's nothing to link by hand. If a PR is missing, link it in Linear or GitHub and it shows up here.

`p` opens all of a ticket's PRs in the browser. In the ticket panel they're also listed above the sessions, and `enter` on one opens just that PR.

When a session finishes or needs your attention, you get a desktop notification titled with the ticket (`KAN-1118`, or `KAN-1115 +2` for a group), unless you're already looking at that session or turned them off in settings.

### Tabs

| tab | what's there |
| --- | --- |
| `1` kanban | your tickets |
| `2` sessions | Claude sessions that don't belong to any ticket, for quick questions or anything else |
| `3` reviews | your open pull requests and your team's, with Claude reviewing them |
| `4` daily | a short summary of what you did since the last working day |
| `5` settings | what the board shows, see below |

The **daily** tab is meant for stand-ups. It reads your Claude sessions since the last working day (on Monday that means since Friday) and writes one plain bullet per thing you worked on, grouped by day. It describes the work by its subject, like "worked on the card about spreadsheet imports", and never uses ticket numbers, because nobody remembers those. The summary is written by Claude Haiku in the language picked in settings (Portuguese by default) and takes about a minute. It's saved per day, so opening the tab again is instant, and `r` regenerates it.

### Reviews

The `3` tab lists pull requests in two groups, refreshed every 30 seconds while you're on it:

- **Mine**: your open PRs, with CI, the review decision and how many review threads are still open.
- **Team**: open PRs from other people that carry the labels picked in settings, across your GitHub organizations.

`enter` opens a PR in a panel underneath.

On **your PR**, the panel lists the open threads (file, line, who said what) and that PR's Claude sessions. `n` starts a session with the PR and its open threads, to answer or fix them, just like on a ticket.

On a **team PR**, the panel is a review cockpit. `r` asks Claude to review the PR in the background (`claude -p`, read-only tools, a few minutes). It comes back with threads, and you go through them one at a time with `←` / `→`. Each thread shows the code around it with the commented lines marked, why it matters, and the comment ready to post.

- `a` asks about the why: question it, disagree, or ask Claude to annotate the code.
- `c` changes the comment: shorter, more human, another tone.
- `g` asks about the review as a whole.
- `x x` drops a thread you don't agree with.
- `s s` sends the threads to GitHub as a **draft review**. Only you see it until you submit it on GitHub.

Each box keeps its own conversation with Claude, so follow-ups pick up where you left off. Reviews are saved in `~/.config/kta/reviews/`, one file per PR.

### Settings

The `5` tab is where you make the board yours. Move with `↑` / `↓`, toggle with `space`, and change the daily language with `←` / `→`. Every change is saved on the spot to `~/.config/kta/settings.json` and applied right away.

| setting | what it does |
| --- | --- |
| Columns | which status columns show up on the kanban. It lists every status of your tickets' teams (or Jira projects), and a new status shows up on its own. |
| Tickets | which tickets come in: assigned to you, created by you, subscribed to. Pick at least one. |
| Daily language | the language of the daily summary: Portuguese, English or Spanish |
| Notifications | the desktop notifications. The status dots keep working either way. |
| Team labels for reviews | which GitHub labels mark your team's pull requests, like `team-platform`. It lists the labels you use on your own PRs. |
| Tabs | hide the sessions, reviews or daily tab. The other tabs keep their numbers. |

The footer only shows the main keys of each screen. `?` shows all of them.

### Keys

Anywhere:

| key | does |
| --- | --- |
| `alt+1` … `alt+5` | switch tabs, also from inside a Claude session |
| `?` | every key for the screen you're on |
| `alt+←` / `alt+→` | move between the board and the Claude session |
| `alt+w` | open the workspace picker |
| `q` / `ctrl+c` | quit |

On the kanban:

| key | does |
| --- | --- |
| arrows or `hjkl` | move around |
| `enter` | open the ticket |
| `n` | new session for the ticket, or one session for all the marked tickets |
| `g` | mark or unmark the ticket, to work on several together |
| `z` / `Z` | fold or unfold the group under the cursor / every group |
| `p` | open the ticket's PRs in the browser |
| `o` | open the ticket in the browser |
| `/` | search |
| `r` | refresh from Linear |
| `alt+↓` or `tab` | go to the ticket panel |
| `esc` | close the ticket |

In the ticket panel and the sessions tab:

| key | does |
| --- | --- |
| `↑` / `↓` | pick a PR or a session |
| `enter` | open the PR in the browser, or show the session on the right (resuming it if it's stopped) |
| `n` | new session |
| `p` | open the ticket's PRs in the browser |
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

- `1` … `5` switch tabs while the board has focus.
- `ctrl+b` then `1` … `5` switches tabs from anywhere, including from inside a Claude session.
- `ctrl+b` then `←` / `→` moves between the board and Claude. Clicking a pane works too.
- `tab` and `esc` move between the kanban and the ticket panel.

Notifications go through macOS itself. If they don't show up, check System Settings → Notifications and allow your terminal. On some macOS versions they're listed under Script Editor.

## How it works

If you're curious, or something looks off:

- **The tmux server.** The board runs in its own tmux server (`tmux -L kta`), with the settings in `tmux.conf`. Your own tmux is never touched.
- **Sessions are tmux windows.** Each Claude session runs in its own tmux window. The one you're looking at is moved next to the board, and the others wait in the background.
- **Status comes from Claude Code hooks.** The hooks are passed with `--settings` when a session starts. They call `kta hook`, which writes the status to a small file and sends the notification.
- **State lives in `~/.config/kta/`**, or `~/.config/kta/workspaces/<name>/` for a named workspace. That's `env`, `sessions.json` (which sessions belong to which ticket, group or PR), `groups.json`, `settings.json`, `status/`, `daily/` and `reviews/`. Writes to `sessions.json` and `groups.json` take a file lock, so the board and `kta group` running inside Claude never step on each other. The conversations themselves are Claude's own transcripts in `~/.claude/projects/`.
- **The daily summary** runs `claude -p --model haiku` with no tools, no hooks and no saved session.
- **Reviews** use `gh` for everything on GitHub, and `claude -p` limited to Read, Grep and Glob, so a review running in the background can't change your files.
- **Tests** cover the logic that's easy to break without noticing: PR tags, columns and their order, settings, the daily window, forgetting finished tickets, the status hook, and applying Claude's answers to a review. Run `go test ./...` before sending a change; tests never touch your real `~/.config/kta`. To build by hand, use `go build -o bin/kta .` (a plain `go build` collides with the `kta` script).

## TODO

- **PRs on Jira cards.** Linear lists a ticket's PRs as attachments; Jira only exposes them through the undocumented dev-status API, so Jira cards don't show PRs yet.
- **Jira Server / Data Center**, which use personal access tokens instead of email + API token.
- **More trackers**, like GitHub Projects. A tracker is a small file next to `linear.go` and `jira.go` with the same five calls (board, ticket, done, states, account).

## Troubleshooting

- **Alt shortcuts don't do anything**: on macOS, see the Option key section above.
- **"Linear didn't respond"**: Linear was slow for a moment. Press `r`.
- **The layout got weird**: run `tmux -L kta kill-server` and start `./kta` again. Your sessions are still there to resume.
- **The board doesn't start**: `./kta` checks `~/.config/kta/env` before starting and asks for whatever's missing. `./kta setup` asks again from scratch.
