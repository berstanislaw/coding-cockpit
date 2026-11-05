#!/bin/sh
title=$1
input=$(cat)
field() { printf %s "$input" | jq -r "$1"; }
file="$HOME/.config/linear-board/status/$(field .session_id)"
if [ "$2" = stop ]; then
  running=$(field '[.background_tasks[]?.type] | group_by(.) | map("\(length) \(if .[0] == "subagent" then "agent" else .[0] end)\(if length > 1 then "s" else "" end)") | join(", ")')
  if [ -n "$running" ]; then
    printf 'background %s' "$running" > "$file"
    exit 0
  fi
  body="finished, waiting for you"
elif [ "$(field '.notification_type // ""')" = idle_prompt ]; then
  case $(cat "$file" 2>/dev/null) in background*) ;; *) printf waiting > "$file" ;; esac
  exit 0
else
  body=$(field '.message // "needs your attention"')
fi
printf waiting > "$file"
[ "$(tmux display -p -t "$TMUX_PANE" '#{window_active}#{m:*focused*,#{client_flags}}' 2>/dev/null)" = 11 ] && exit 0
if command -v notify-send >/dev/null; then
  notify-send -a linear-board "$title" "$body" 2>/dev/null
elif command -v osascript >/dev/null; then
  osascript -e 'on run argv' -e 'display notification (item 2 of argv) with title (item 1 of argv)' -e 'end run' "$title" "$body" >/dev/null 2>&1
fi
exit 0
